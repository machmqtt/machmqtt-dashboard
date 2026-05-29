package collector

import (
	"context"
	"log/slog"
	"net/url"
	"sync"
	"sync/atomic"
	"time"

	"github.com/noodlebit/machmqtt-dashboard/internal/config"
	"github.com/noodlebit/machmqtt-dashboard/internal/store"
	"golang.org/x/sync/errgroup"
)

// Collector polls one NATS environment and maintains a Snapshot.
type Collector struct {
	env      config.Environment
	fetcher  *Fetcher
	log      *slog.Logger
	interval time.Duration
	store    *store.Store

	mu       sync.RWMutex
	snapshot *Snapshot
	prev     *Snapshot
	tick     uint64

	// Cached MQTT bridge discovery results.
	mqttMu          sync.RWMutex
	mqttBridges     []MQTTBridgeInstance
	mqttDiscovering atomic.Bool
}

func newCollector(env config.Environment, fetcher *Fetcher, interval time.Duration, log *slog.Logger, db *store.Store) *Collector {
	return &Collector{
		env:      env,
		fetcher:  fetcher,
		interval: interval,
		log:      log.With("env", env.Name),
		store:    db,
		snapshot: &Snapshot{
			Varz:     make(map[string]*Varz),
			Routez:   make(map[string]*Routez),
			Gatewayz: make(map[string]*Gatewayz),
			Leafz:    make(map[string]*Leafz),
			Health:   make(map[string]*HealthStatus),
			Connz:    make(map[string]*Connz),
			Subsz:    make(map[string]*SubszResp),
			JSInfo:   make(map[string]*JSInfo),
			Accountz: make(map[string]*Accountz),
		},
	}
}

func (c *Collector) run(ctx context.Context, onChange func(envName string)) {
	ticker := time.NewTicker(c.interval)
	defer ticker.Stop()

	// Initial poll.
	c.poll(ctx, true)
	if onChange != nil {
		onChange(c.env.Name)
	}

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			c.tick++
			slowPoll := c.tick%3 == 0
			c.poll(ctx, slowPoll)
			if onChange != nil {
				onChange(c.env.Name)
			}
		}
	}
}

// buildServerURLMap maps server ID → config URL hostname.
// Used to resolve 127.0.0.1 bridge IPs to the actual server hostname.
func (c *Collector) buildServerURLMap(snap *Snapshot) map[string]string {
	m := make(map[string]string)
	for _, srv := range c.env.Servers {
		u, err := url.Parse(srv.URL)
		if err != nil {
			continue
		}
		host := u.Hostname()
		// Find which server ID this URL corresponds to by matching the varz.
		for id := range snap.Varz {
			// We already fetched varz from this URL, so the ID is known.
			// Map all server IDs to their config hostnames.
			if _, ok := m[id]; !ok {
				m[id] = host
			}
		}
	}
	// More precise: fetch URL → server ID mapping from the fetch order.
	// Since we fetch all servers concurrently, we can't guarantee order.
	// Instead, use a direct approach: for each config server URL,
	// the hostname is what we'd use to resolve loopback bridges.
	// Store all config hostnames and let discovery pick the right one.
	return m
}

func (c *Collector) poll(ctx context.Context, slow bool) {
	snap := &Snapshot{
		Timestamp: time.Now(),
		Varz:      make(map[string]*Varz),
		Routez:    make(map[string]*Routez),
		Gatewayz:  make(map[string]*Gatewayz),
		Leafz:     make(map[string]*Leafz),
		Health:    make(map[string]*HealthStatus),
	}

	if slow {
		snap.Connz = make(map[string]*Connz)
		snap.Subsz = make(map[string]*SubszResp)
		snap.JSInfo = make(map[string]*JSInfo)
		snap.Accountz = make(map[string]*Accountz)
	}

	g, gCtx := errgroup.WithContext(ctx)
	var mu sync.Mutex

	// Track which server ID came from which config URL.
	serverURLMap := make(map[string]string)

	for _, srv := range c.env.Servers {
		srvURL := srv.URL
		g.Go(func() error {
			c.fetchServer(gCtx, srvURL, snap, &mu, slow, serverURLMap)
			return nil
		})
	}

	g.Wait()

	snap.ServerURLs = serverURLMap

	c.mu.Lock()
	c.prev = c.snapshot
	snap.Rates = computeRates(c.prev, snap)
	if !slow {
		snap.Connz = c.snapshot.Connz
		snap.Subsz = c.snapshot.Subsz
		snap.JSInfo = c.snapshot.JSInfo
		snap.Accountz = c.snapshot.Accountz
		snap.ServerURLs = c.snapshot.ServerURLs
	}
	c.snapshot = snap
	c.mu.Unlock()

	// Run MQTT bridge discovery on slow polls (skip if one is already running).
	if slow && c.env.MQTTDiscoveryEnabled() && c.mqttDiscovering.CompareAndSwap(false, true) {
		go func() {
			defer c.mqttDiscovering.Store(false)
			c.discoverMQTTBridges(ctx)
		}()
	}
}

func (c *Collector) fetchServer(ctx context.Context, cfgURL string, snap *Snapshot, mu *sync.Mutex, slow bool, serverURLMap map[string]string) {
	varz, err := c.fetcher.FetchVarz(ctx, cfgURL)
	if err != nil {
		c.log.Warn("fetch varz", "url", cfgURL, "err", err)
		return
	}
	id := varz.ServerID

	// Extract hostname from config URL for loopback resolution.
	if u, err := url.Parse(cfgURL); err == nil {
		mu.Lock()
		serverURLMap[id] = u.Hostname()
		mu.Unlock()
	}

	routez, _ := c.fetcher.FetchRoutez(ctx, cfgURL)
	gatewayz, _ := c.fetcher.FetchGatewayz(ctx, cfgURL)
	leafz, _ := c.fetcher.FetchLeafz(ctx, cfgURL)
	health, _ := c.fetcher.FetchHealthz(ctx, cfgURL)

	mu.Lock()
	snap.Varz[id] = varz
	if routez != nil {
		snap.Routez[id] = routez
	}
	if gatewayz != nil {
		snap.Gatewayz[id] = gatewayz
	}
	if leafz != nil {
		snap.Leafz[id] = leafz
	}
	if health != nil {
		snap.Health[id] = health
	}
	mu.Unlock()

	if !slow {
		return
	}

	connz, _ := c.fetcher.FetchConnz(ctx, cfgURL, 1024, 0, "", "", "", "")
	subsz, _ := c.fetcher.FetchSubsz(ctx, cfgURL)
	jsInfo, _ := c.fetcher.FetchJSInfo(ctx, cfgURL)
	accountz, _ := c.fetcher.FetchAccountz(ctx, cfgURL)

	mu.Lock()
	if connz != nil {
		snap.Connz[id] = connz
	}
	if subsz != nil {
		snap.Subsz[id] = subsz
	}
	if jsInfo != nil {
		snap.JSInfo[id] = jsInfo
	}
	if accountz != nil {
		snap.Accountz[id] = accountz
	}
	mu.Unlock()
}

func (c *Collector) discoverMQTTBridges(ctx context.Context) {
	snap := c.Snapshot()
	prev := c.PrevSnapshot()
	if snap == nil {
		return
	}

	bridges := DiscoverMQTTBridges(ctx, snap, prev, c.env.MQTTDiscoveryPorts(), c.env.ResolveBridgeToken(""))

	// Persist discovered bridges.
	if c.store != nil {
		for _, b := range bridges {
			c.store.UpsertMQTTBridge(c.env.Name, b.IP, b.ServerID, b.AdminURL)
		}
		// Clean up bridges not seen in 24 hours.
		c.store.DeleteStaleMQTTBridges(c.env.Name, 24*time.Hour)
	}

	c.mqttMu.Lock()
	c.mqttBridges = bridges
	c.mqttMu.Unlock()
}

func (c *Collector) Snapshot() *Snapshot {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.snapshot
}

func (c *Collector) PrevSnapshot() *Snapshot {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.prev
}

func (c *Collector) MQTTBridges() []MQTTBridgeInstance {
	c.mqttMu.RLock()
	defer c.mqttMu.RUnlock()
	return c.mqttBridges
}

// Manager owns collectors for all environments.
type Manager struct {
	collectors map[string]*Collector
	onChange   func(envName string)
	log        *slog.Logger
}

func NewManager(cfg *config.Config, onChange func(envName string), log *slog.Logger, db *store.Store) (*Manager, error) {
	m := &Manager{
		collectors: make(map[string]*Collector),
		onChange:   onChange,
		log:        log,
	}

	for _, env := range cfg.Environments {
		fetcher, err := NewFetcher(env.TLS)
		if err != nil {
			return nil, err
		}
		m.collectors[env.Name] = newCollector(env, fetcher, cfg.PollInterval, log, db)
	}

	return m, nil
}

func (m *Manager) Start(ctx context.Context) {
	for _, c := range m.collectors {
		go c.run(ctx, m.onChange)
	}
}

func (m *Manager) Snapshot(envName string) *Snapshot {
	c, ok := m.collectors[envName]
	if !ok {
		return nil
	}
	return c.Snapshot()
}

func (m *Manager) PrevSnapshot(envName string) *Snapshot {
	c, ok := m.collectors[envName]
	if !ok {
		return nil
	}
	return c.PrevSnapshot()
}

func (m *Manager) Overview(envName string) *Overview {
	snap := m.Snapshot(envName)
	if snap == nil {
		return nil
	}
	return buildOverview(snap)
}

func (m *Manager) Topology(envName string) *TopologyGraph {
	c, ok := m.collectors[envName]
	if !ok {
		return nil
	}
	snap := c.Snapshot()
	if snap == nil {
		return nil
	}
	return buildTopology(snap, c.PrevSnapshot())
}

func (m *Manager) Health(envName string) map[string]*HealthStatus {
	snap := m.Snapshot(envName)
	if snap == nil {
		return nil
	}
	return snap.Health
}

func (m *Manager) MQTTBridges(envName string) []MQTTBridgeInstance {
	c, ok := m.collectors[envName]
	if !ok {
		return nil
	}
	return c.MQTTBridges()
}

func (m *Manager) Environments() []string {
	names := make([]string, 0, len(m.collectors))
	for name := range m.collectors {
		names = append(names, name)
	}
	return names
}

func (m *Manager) Fetcher(envName string) *Fetcher {
	c, ok := m.collectors[envName]
	if !ok {
		return nil
	}
	return c.fetcher
}

func (m *Manager) EnvServers(envName string) []string {
	c, ok := m.collectors[envName]
	if !ok {
		return nil
	}
	urls := make([]string, len(c.env.Servers))
	for i, s := range c.env.Servers {
		urls[i] = s.URL
	}
	return urls
}
