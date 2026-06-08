package collector

import (
	"context"
	"fmt"
	"log/slog"
	"net/url"
	"sync"
	"sync/atomic"
	"time"

	"github.com/noodlebit/machmqtt-dashboard/internal/config"
	"github.com/noodlebit/machmqtt-dashboard/internal/store"
	"golang.org/x/sync/errgroup"
)

// Collector polls one NATS cluster and maintains a Snapshot.
type Collector struct {
	mu       sync.RWMutex
	env      config.Environment // guarded by mu for name-only updates
	fetcher  *Fetcher
	log      *slog.Logger
	interval time.Duration
	store    *store.Store

	snapMu   sync.RWMutex
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
		log:      log.With("cluster", env.Name),
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

// setEnv updates the collector's environment in place (for label-only changes
// like a cluster rename that don't require rebuilding the fetcher).
func (c *Collector) setEnv(env config.Environment) {
	c.mu.Lock()
	c.env = env
	c.mu.Unlock()
}

// getEnv returns a snapshot copy of the current environment config.
func (c *Collector) getEnv() config.Environment {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.env
}

func (c *Collector) run(ctx context.Context, clusterID string, onChange func(clusterID string)) {
	ticker := time.NewTicker(c.interval)
	defer ticker.Stop()

	// Initial poll.
	c.poll(ctx, clusterID, true)
	if onChange != nil {
		onChange(clusterID)
	}

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			c.tick++
			slowPoll := c.tick%3 == 0
			c.poll(ctx, clusterID, slowPoll)
			if onChange != nil {
				onChange(clusterID)
			}
		}
	}
}

// buildServerURLMap maps server ID → config URL hostname.
// Used to resolve 127.0.0.1 bridge IPs to the actual server hostname.
func (c *Collector) buildServerURLMap(snap *Snapshot) map[string]string {
	env := c.getEnv()
	m := make(map[string]string)
	for _, srv := range env.Servers {
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

func (c *Collector) poll(ctx context.Context, clusterID string, slow bool) {
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

	env := c.getEnv()
	for _, srv := range env.Servers {
		srvURL := srv.URL
		g.Go(func() error {
			c.fetchServer(gCtx, srvURL, snap, &mu, slow, serverURLMap)
			return nil
		})
	}

	g.Wait()

	snap.ServerURLs = serverURLMap

	c.snapMu.Lock()
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
	c.snapMu.Unlock()

	// Run MQTT bridge discovery on slow polls (skip if one is already running).
	if slow && env.MQTTDiscoveryEnabled() && c.mqttDiscovering.CompareAndSwap(false, true) {
		go func() {
			defer c.mqttDiscovering.Store(false)
			c.discoverMQTTBridges(ctx, clusterID)
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

func (c *Collector) discoverMQTTBridges(ctx context.Context, clusterID string) {
	snap := c.Snapshot()
	prev := c.PrevSnapshot()
	if snap == nil {
		return
	}

	env := c.getEnv()
	bridges := DiscoverMQTTBridges(ctx, snap, prev, env.MQTTDiscoveryPorts(), env.ResolveBridgeToken(""))

	// Persist discovered bridges keyed by cluster ID (stable identity).
	if c.store != nil {
		for _, b := range bridges {
			c.store.UpsertMQTTBridge(clusterID, b.IP, b.ServerID, b.AdminURL)
		}
		// Clean up bridges not seen in 24 hours.
		c.store.DeleteStaleMQTTBridges(clusterID, 24*time.Hour)
	}

	c.mqttMu.Lock()
	c.mqttBridges = bridges
	c.mqttMu.Unlock()
}

func (c *Collector) Snapshot() *Snapshot {
	c.snapMu.RLock()
	defer c.snapMu.RUnlock()
	return c.snapshot
}

func (c *Collector) PrevSnapshot() *Snapshot {
	c.snapMu.RLock()
	defer c.snapMu.RUnlock()
	return c.prev
}

func (c *Collector) MQTTBridges() []MQTTBridgeInstance {
	c.mqttMu.RLock()
	defer c.mqttMu.RUnlock()
	return c.mqttBridges
}

// Manager owns collectors for all clusters, supporting live add/remove/update
// without restarting the process.
type Manager struct {
	mu         sync.RWMutex
	collectors map[string]*Collector          // keyed by cluster ID
	cancels    map[string]context.CancelFunc  // per-collector stop func
	rootCtx    context.Context               // parent ctx from Start()
	onChange   func(clusterID string)
	interval   time.Duration
	log        *slog.Logger
	db         *store.Store
}

// NewManager loads clusters from the database and builds a collector per cluster.
// Clusters are keyed by their stable ID, not their display name.
func NewManager(cfg *config.Config, onChange func(clusterID string), log *slog.Logger, db *store.Store) (*Manager, error) {
	m := &Manager{
		collectors: make(map[string]*Collector),
		cancels:    make(map[string]context.CancelFunc),
		onChange:   onChange,
		interval:   cfg.PollInterval,
		log:        log,
		db:         db,
	}

	clusters, err := db.ListClusters()
	if err != nil {
		return nil, fmt.Errorf("load clusters: %w", err)
	}

	for _, cl := range clusters {
		env := cl.ToEnvironment()
		fetcher, err := NewFetcher(env.TLS)
		if err != nil {
			return nil, fmt.Errorf("cluster %q: build fetcher: %w", cl.Name, err)
		}
		m.collectors[cl.ID] = newCollector(env, fetcher, cfg.PollInterval, log, db)
	}

	return m, nil
}

// Start begins polling all clusters. It stores the root context so that
// AddCluster can derive child contexts for new collectors after startup.
func (m *Manager) Start(ctx context.Context) {
	m.mu.Lock()
	m.rootCtx = ctx
	for id, c := range m.collectors {
		cctx, cancel := context.WithCancel(ctx)
		m.cancels[id] = cancel
		go c.run(cctx, id, m.onChange)
	}
	m.mu.Unlock()
}

// AddCluster builds and starts a new collector for a cluster. Idempotent: if
// the cluster is already tracked, it is a no-op.
func (m *Manager) AddCluster(cl store.Cluster) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, exists := m.collectors[cl.ID]; exists {
		return nil
	}

	env := cl.ToEnvironment()
	fetcher, err := NewFetcher(env.TLS)
	if err != nil {
		return fmt.Errorf("build fetcher: %w", err)
	}

	c := newCollector(env, fetcher, m.interval, m.log, m.db)
	m.collectors[cl.ID] = c

	if m.rootCtx != nil {
		cctx, cancel := context.WithCancel(m.rootCtx)
		m.cancels[cl.ID] = cancel
		go c.run(cctx, cl.ID, m.onChange)
	}

	return nil
}

// RemoveCluster stops the collector for a cluster and removes it from tracking.
func (m *Manager) RemoveCluster(id string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if cancel, ok := m.cancels[id]; ok {
		cancel()
		delete(m.cancels, id)
	}
	delete(m.collectors, id)
}

// UpdateCluster applies cluster config changes live.
// For name-only changes the running collector's env label is swapped in place
// (zero polling blip). For connection-affecting changes (new servers or TLS)
// the old collector is stopped and a new one is started.
func (m *Manager) UpdateCluster(cl store.Cluster) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	existing, ok := m.collectors[cl.ID]
	if !ok {
		// Not yet tracked — start fresh.
		m.mu.Unlock()
		err := m.AddCluster(cl)
		m.mu.Lock()
		return err
	}

	oldEnv := existing.getEnv()
	newEnv := cl.ToEnvironment()

	// Determine whether only the display name changed.
	serversChanged := serversEqual(oldEnv.Servers, newEnv.Servers)
	tlsChanged := tlsEqual(oldEnv.TLS, newEnv.TLS)
	nameOnly := serversChanged && tlsChanged

	if nameOnly {
		// Fast path: update the env label in-place, no goroutine restart.
		existing.setEnv(newEnv)
		return nil
	}

	// Connection-affecting change: tear down and rebuild.
	if cancel, ok := m.cancels[cl.ID]; ok {
		cancel()
		delete(m.cancels, cl.ID)
	}
	delete(m.collectors, cl.ID)

	fetcher, err := NewFetcher(newEnv.TLS)
	if err != nil {
		return fmt.Errorf("build fetcher: %w", err)
	}

	c := newCollector(newEnv, fetcher, m.interval, m.log, m.db)
	m.collectors[cl.ID] = c

	if m.rootCtx != nil {
		cctx, cancel := context.WithCancel(m.rootCtx)
		m.cancels[cl.ID] = cancel
		go c.run(cctx, cl.ID, m.onChange)
	}

	return nil
}

// ClusterConfig returns a copy of the live environment config for a cluster ID,
// for use in MQTT proxy handlers that need token/bridge resolution.
func (m *Manager) ClusterConfig(id string) *config.Environment {
	m.mu.RLock()
	c, ok := m.collectors[id]
	m.mu.RUnlock()
	if !ok {
		return nil
	}
	env := c.getEnv()
	return &env
}

// ClusterIDs returns the IDs of all tracked clusters (unsorted).
func (m *Manager) ClusterIDs() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	ids := make([]string, 0, len(m.collectors))
	for id := range m.collectors {
		ids = append(ids, id)
	}
	return ids
}

// Snapshot returns the latest snapshot for a cluster ID.
func (m *Manager) Snapshot(clusterID string) *Snapshot {
	m.mu.RLock()
	c, ok := m.collectors[clusterID]
	m.mu.RUnlock()
	if !ok {
		return nil
	}
	return c.Snapshot()
}

// PrevSnapshot returns the previous snapshot for a cluster ID.
func (m *Manager) PrevSnapshot(clusterID string) *Snapshot {
	m.mu.RLock()
	c, ok := m.collectors[clusterID]
	m.mu.RUnlock()
	if !ok {
		return nil
	}
	return c.PrevSnapshot()
}

// Overview computes the aggregated overview for a cluster ID.
func (m *Manager) Overview(clusterID string) *Overview {
	snap := m.Snapshot(clusterID)
	if snap == nil {
		return nil
	}
	return buildOverview(snap)
}

// Topology builds the topology graph for a cluster ID.
func (m *Manager) Topology(clusterID string) *TopologyGraph {
	m.mu.RLock()
	c, ok := m.collectors[clusterID]
	m.mu.RUnlock()
	if !ok {
		return nil
	}
	snap := c.Snapshot()
	if snap == nil {
		return nil
	}
	return buildTopology(snap, c.PrevSnapshot())
}

// Health returns the health map for a cluster ID.
func (m *Manager) Health(clusterID string) map[string]*HealthStatus {
	snap := m.Snapshot(clusterID)
	if snap == nil {
		return nil
	}
	return snap.Health
}

// MQTTBridges returns the discovered MQTT bridges for a cluster ID.
func (m *Manager) MQTTBridges(clusterID string) []MQTTBridgeInstance {
	m.mu.RLock()
	c, ok := m.collectors[clusterID]
	m.mu.RUnlock()
	if !ok {
		return nil
	}
	return c.MQTTBridges()
}

// Environments returns the cluster IDs of all tracked clusters. The name
// "Environments" is kept for API compatibility; callers should migrate to
// ClusterIDs() where semantics matter.
func (m *Manager) Environments() []string {
	return m.ClusterIDs()
}

// Fetcher returns the HTTP fetcher for a cluster ID (used in admin proxy handlers).
func (m *Manager) Fetcher(clusterID string) *Fetcher {
	m.mu.RLock()
	c, ok := m.collectors[clusterID]
	m.mu.RUnlock()
	if !ok {
		return nil
	}
	return c.fetcher
}

// EnvServers returns the configured server URLs for a cluster ID.
func (m *Manager) EnvServers(clusterID string) []string {
	m.mu.RLock()
	c, ok := m.collectors[clusterID]
	m.mu.RUnlock()
	if !ok {
		return nil
	}
	env := c.getEnv()
	urls := make([]string, len(env.Servers))
	for i, s := range env.Servers {
		urls[i] = s.URL
	}
	return urls
}

// serversEqual reports whether two server slices are structurally identical.
// Returns true when they match (i.e. NOT changed), so the name-only fast path
// can skip rebuild.
func serversEqual(a, b []config.Server) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].URL != b[i].URL {
			return false
		}
	}
	return true
}

// tlsEqual reports whether two TLS configs are structurally identical.
func tlsEqual(a, b *config.TLSConfig) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	return a.CAFile == b.CAFile && a.Insecure == b.Insecure
}
