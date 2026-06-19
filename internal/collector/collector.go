package collector

import (
	"context"
	"fmt"
	"log/slog"
	"net/url"
	"slices"
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

	// Cached MQTT bridge discovery results (HTTP connz-scan path).
	mqttMu          sync.RWMutex
	mqttBridges     []MQTTBridgeInstance
	mqttDiscovering atomic.Bool

	// subscriber is non-nil when NATS push-based collection is configured.
	// When set it supersedes the connz-scan HTTP discovery path.
	subscriber *MQTTSubscriber

	// sys is non-nil when $SYS-based server collection is enabled (Tier 2b).
	// When set, poll() uses STATSZ cache + PING fan-in instead of HTTP.
	sys *SYSCollector

	// $SYS→HTTP fallback state. sysFirstFail/sysEverHealthy are touched only by
	// the single poll goroutine; sysFellBack is an atomic because Health() reads
	// it from the HTTP handler goroutine. sysFirstFail is when $SYS first started
	// returning no data (zero when healthy); sysFellBack is true while the HTTP
	// fallback is engaged; sysEverHealthy records whether $SYS has ever produced
	// data (so cold start can fall back immediately while a post-healthy outage
	// waits out the grace).
	sysFirstFail   time.Time
	sysFellBack    atomic.Bool
	sysEverHealthy bool
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

// sysFallbackGrace is how long $SYS collection may produce no server data
// before the collector falls back to HTTP polling. With a 30s poll interval
// this is ~2 empty polls.
const sysFallbackGrace = 60 * time.Second

// shouldConnzScan reports whether to run connz-scan MQTT bridge discovery this
// poll. The push subscriber is preferred, but while it has no live bridges yet
// — none configured, still waiting for the first metrics publish, or $SYS has
// fallen back to HTTP — the dashboard actively queries NATS connz + the MachMQTT
// admin API so the fleet has data immediately on startup instead of sitting
// blank until a publish arrives.
func (c *Collector) shouldConnzScan() bool {
	env := c.getEnv()
	if !env.MQTTDiscoveryEnabled() {
		return false
	}
	if c.subscriber != nil && len(c.subscriber.Bridges()) > 0 {
		return false // push metrics are flowing — prefer them
	}
	return true
}

// maybeDiscoverBridges launches connz-scan discovery in the background when
// appropriate. Safe to call from every poll path; the atomic guard keeps at
// most one discovery running at a time.
func (c *Collector) maybeDiscoverBridges(ctx context.Context, clusterID string) {
	if c.shouldConnzScan() && c.mqttDiscovering.CompareAndSwap(false, true) {
		go func() {
			defer c.mqttDiscovering.Store(false)
			c.discoverMQTTBridges(ctx, clusterID)
		}()
	}
}

func (c *Collector) poll(ctx context.Context, clusterID string, slow bool) {
	if c.sys != nil {
		// While in fallback, skip the $SYS poll (a 2s PING fan-in that times out
		// when there is no $SYS access) unless the STATSZ cache shows the server
		// is emitting events again. That keeps a permanent fallback cheap.
		trySys := !c.sysFellBack.Load() || c.sys.cacheLen() > 0
		if trySys && c.pollSYS(ctx, clusterID, slow) {
			// $SYS is healthy. If we were on the HTTP fallback, resume $SYS and
			// drop any connz-scanned bridges so the push subscriber is the sole
			// source again.
			if c.sysFellBack.Load() {
				c.log.Info("$SYS collection recovered — disengaging HTTP fallback, resuming $SYS-based collection")
				c.sysFellBack.Store(false)
				c.mqttMu.Lock()
				c.mqttBridges = nil
				c.mqttMu.Unlock()
			}
			c.sysEverHealthy = true
			c.sysFirstFail = time.Time{}
			// Even with $SYS serving server data, query MachMQTT until the push
			// subscriber has bridges, so the fleet isn't blank while it warms up.
			c.maybeDiscoverBridges(ctx, clusterID)
			return
		}
		// $SYS produced no server data this poll.
		if trySys && c.sysFirstFail.IsZero() {
			c.sysFirstFail = time.Now()
		}
		// Cold start (never any $SYS data — commonly a misconfig like missing
		// system-account credentials) falls back immediately so the user isn't
		// left staring at a blank page. A post-healthy outage waits out the
		// grace period first, to ride through a transient drop without flapping.
		if c.sysEverHealthy && !c.sysFellBack.Load() && time.Since(c.sysFirstFail) < sysFallbackGrace {
			c.maybeDiscoverBridges(ctx, clusterID)
			return // within grace period — keep the prior snapshot, wait for $SYS
		}
		if !c.sysFellBack.Load() {
			c.log.Warn("$SYS collection produced no data — falling back to HTTP monitoring; $SYS will resume automatically when events return", "servers", len(c.getEnv().Servers), "ever_healthy", c.sysEverHealthy)
			c.sysFellBack.Store(true)
		}
		// Fall through to the HTTP polling path below.
	}

	snap := &Snapshot{
		Timestamp: time.Now(),
		Varz:      make(map[string]*Varz),
		Routez:    make(map[string]*Routez),
		Gatewayz:  make(map[string]*Gatewayz),
		Leafz:     make(map[string]*Leafz),
		Health:    make(map[string]*HealthStatus),
	}

	snap.Connz = make(map[string]*Connz)
	if slow {
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
		snap.Subsz = c.snapshot.Subsz
		snap.JSInfo = c.snapshot.JSInfo
		snap.Accountz = c.snapshot.Accountz
		snap.ServerURLs = c.snapshot.ServerURLs
	}
	c.snapshot = snap
	c.snapMu.Unlock()

	// Query MachMQTT via connz-scan whenever the push subscriber has no bridges
	// yet (covers HTTP-only, push-still-warming-up, and $SYS-fallback).
	c.maybeDiscoverBridges(ctx, clusterID)
}

// pollSYS runs one $SYS-based poll. It returns true if it produced and stored a
// snapshot containing at least one server, and false when $SYS yielded no data
// (NATS not connected, or no STATSZ/PING replies) so the caller can fall back to
// HTTP polling. clusterID is unused here because $SYS bridge discovery is handled
// by the push subscriber, not connz-scan.
func (c *Collector) pollSYS(ctx context.Context, _ string, slow bool) bool {
	// Read the current snapshot outside the write lock so sys.poll can use it
	// for carry-forward of slow-polled data on fast polls.
	c.snapMu.RLock()
	cur := c.snapshot
	c.snapMu.RUnlock()

	snap := c.sys.poll(ctx, cur, slow)
	if snap == nil || len(snap.Varz) == 0 {
		// No server data: NATS isn't connected, or the STATSZ cache is empty and
		// the bootstrap PING returned nothing (commonly: the connection lacks
		// system-account access). Surface it on slow polls.
		if slow {
			c.log.Warn("$SYS poll produced no server data — NATS not connected or no $SYS.SERVER.*.STATSZ replies (check system-account credentials and that the servers emit system events)")
		}
		return false
	}

	// Heartbeat so the Server Logs page shows the $SYS collector is alive and
	// how many servers it currently sees.
	if slow {
		c.log.Info("$SYS poll", "servers", len(snap.Varz), "statsz_cached", c.sys.cacheLen())
	}

	c.snapMu.Lock()
	c.prev = c.snapshot
	snap.Rates = computeRates(c.prev, snap)
	c.snapshot = snap
	c.snapMu.Unlock()
	return true
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

	// varz already succeeded, so a failure on these is anomalous (transient blip
	// or partial outage). Log it and skip storing rather than overwriting with a
	// zero-value struct (these fetchers always return a non-nil pointer).
	routez, rErr := c.fetcher.FetchRoutez(ctx, cfgURL)
	gatewayz, gErr := c.fetcher.FetchGatewayz(ctx, cfgURL)
	leafz, lErr := c.fetcher.FetchLeafz(ctx, cfgURL)
	health, hErr := c.fetcher.FetchHealthz(ctx, cfgURL)
	c.logFetchErr("routez", cfgURL, rErr)
	c.logFetchErr("gatewayz", cfgURL, gErr)
	c.logFetchErr("leafz", cfgURL, lErr)
	c.logFetchErr("healthz", cfgURL, hErr)

	mu.Lock()
	snap.Varz[id] = varz
	if rErr == nil {
		snap.Routez[id] = routez
	}
	if gErr == nil {
		snap.Gatewayz[id] = gatewayz
	}
	if lErr == nil {
		snap.Leafz[id] = leafz
	}
	if hErr == nil {
		snap.Health[id] = health
	}
	mu.Unlock()

	connz, cErr := c.fetcher.FetchConnz(ctx, cfgURL, 1024, 0, "", "", "", "")
	c.logFetchErr("connz", cfgURL, cErr)
	if cErr == nil {
		mu.Lock()
		snap.Connz[id] = connz
		mu.Unlock()
	}

	if !slow {
		return
	}

	subsz, sErr := c.fetcher.FetchSubsz(ctx, cfgURL)
	jsInfo, jErr := c.fetcher.FetchJSInfo(ctx, cfgURL)
	accountz, aErr := c.fetcher.FetchAccountz(ctx, cfgURL)
	c.logFetchErr("subsz", cfgURL, sErr)
	if jErr != nil {
		// JetStream is commonly disabled, so a jsz error is expected — keep it at
		// Debug to avoid spamming the logs every slow poll.
		c.log.Debug("fetch jsz (JetStream may be disabled)", "url", cfgURL, "err", jErr)
	}
	c.logFetchErr("accountz", cfgURL, aErr)

	mu.Lock()
	if sErr == nil {
		snap.Subsz[id] = subsz
	}
	if jErr == nil {
		snap.JSInfo[id] = jsInfo
	}
	if aErr == nil {
		snap.Accountz[id] = accountz
	}
	mu.Unlock()
}

// logFetchErr logs a non-nil secondary-endpoint fetch error at Warn.
func (c *Collector) logFetchErr(endpoint, url string, err error) {
	if err != nil {
		c.log.Warn("fetch "+endpoint, "url", url, "err", err)
	}
}

func (c *Collector) discoverMQTTBridges(ctx context.Context, clusterID string) {
	snap := c.Snapshot()
	prev := c.PrevSnapshot()
	if snap == nil {
		return
	}

	// Count connections in Connz that match the bridge name filter so
	// operators can tell from logs whether NATS is reporting MachMQTT
	// connections at all.
	matchingConns := 0
	for _, connz := range snap.Connz {
		for _, conn := range connz.Conns {
			if isMQTTBridgeConn(conn.Name) {
				matchingConns++
			}
		}
	}
	c.log.Info("mqtt discovery starting", "connz_servers", len(snap.Connz), "matching_conns", matchingConns)

	env := c.getEnv()
	bridges := DiscoverMQTTBridges(ctx, snap, prev, env.MQTTDiscoveryPorts(), env.ResolveBridgeToken(""))

	for _, b := range bridges {
		if b.Reachable {
			c.log.Info("mqtt bridge discovered", "ip", b.IP, "admin_url", b.AdminURL, "reachable", true)
		} else {
			c.log.Warn("mqtt bridge found in connz but admin api unreachable", "ip", b.IP, "ports", env.MQTTDiscoveryPorts())
		}
	}
	if len(bridges) == 0 && matchingConns == 0 {
		c.log.Warn("mqtt discovery found no bridge connections in connz — verify MachMQTT is connected and NATS monitoring URL is correct")
	}

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

// ClusterHealth is the dashboard's own operational view of one cluster's
// collection pipeline (distinct from the NATS server /healthz the cluster
// reports). It powers the /api/admin/health endpoint and the UI degraded badge.
type ClusterHealth struct {
	ID                 string  `json:"id"`
	Name               string  `json:"name"`
	LastPollAgeSeconds float64 `json:"last_poll_age_seconds"`
	Servers            int     `json:"servers"`
	HealthyServers     int     `json:"healthy_servers"`
	CollectionMode     string  `json:"collection_mode"` // "http" | "sys" | "sys-fallback"
	SysFallbackEngaged bool    `json:"sys_fallback_engaged"`
	NATSPushConfigured bool    `json:"nats_push_configured"`
	NATSPushConnected  bool    `json:"nats_push_connected"`
	Stale              bool    `json:"stale"`
}

// Degraded reports whether the cluster's collection is in a less-than-nominal
// state worth surfacing to the user (stale data, $SYS fallback engaged, or a
// configured NATS push connection that is currently down).
func (h ClusterHealth) Degraded() bool {
	return h.Stale || h.SysFallbackEngaged || (h.NATSPushConfigured && !h.NATSPushConnected)
}

// Health returns the collector's current operational health. Safe to call from
// any goroutine: the snapshot is read under snapMu, sysFellBack is atomic, and
// sys/subscriber are immutable after construction.
func (c *Collector) Health() ClusterHealth {
	snap := c.Snapshot()
	h := ClusterHealth{
		Name:    c.getEnv().Name,
		Servers: len(snap.Varz),
	}
	for id := range snap.Varz {
		healthy := true
		if hs, ok := snap.Health[id]; ok {
			healthy = hs.Status == "ok"
		}
		if healthy {
			h.HealthyServers++
		}
	}
	if !snap.Timestamp.IsZero() {
		age := time.Since(snap.Timestamp)
		h.LastPollAgeSeconds = age.Seconds()
		interval := c.interval
		if interval <= 0 {
			interval = 30 * time.Second
		}
		h.Stale = age > 3*interval
	}

	h.SysFallbackEngaged = c.sysFellBack.Load()
	switch {
	case c.sys != nil && !h.SysFallbackEngaged:
		h.CollectionMode = "sys"
	case c.sys != nil && h.SysFallbackEngaged:
		h.CollectionMode = "sys-fallback"
	default:
		h.CollectionMode = "http"
	}

	h.NATSPushConfigured = c.sys != nil || c.subscriber != nil
	h.NATSPushConnected = (c.sys != nil && c.sys.Connected()) ||
		(c.subscriber != nil && c.subscriber.Connected())
	return h
}

func (c *Collector) MQTTBridges() []MQTTBridgeInstance {
	// Prefer push-subscriber bridges when present. When a subscriber is
	// configured but reports no bridges (e.g. MachMQTT isn't publishing
	// metrics, or $SYS has fallen back to HTTP), use the connz-scan results so
	// the fleet isn't blank.
	if c.subscriber != nil {
		if b := c.subscriber.Bridges(); len(b) > 0 {
			return b // Bridges() already returns a fresh copy
		}
	}
	c.mqttMu.RLock()
	defer c.mqttMu.RUnlock()
	// Return a copy: callers (e.g. the API bridge handler) sort and mutate the
	// result in place, which would otherwise race the poll goroutine that
	// reassigns c.mqttBridges.
	return slices.Clone(c.mqttBridges)
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
		m.startCollector(cctx, c, id, c.getEnv())
	}
	m.mu.Unlock()
}

// AddCluster builds and starts a new collector for a cluster. Idempotent: if
// the cluster is already tracked, it is a no-op.
func (m *Manager) AddCluster(cl store.Cluster) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.addClusterLocked(cl)
}

// addClusterLocked builds and starts a collector for cl. The caller must hold
// m.mu. Idempotent: a no-op if the cluster is already tracked.
func (m *Manager) addClusterLocked(cl store.Cluster) error {
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
		m.startCollector(cctx, c, cl.ID, env)
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
		// Not yet tracked — start fresh (we already hold m.mu).
		return m.addClusterLocked(cl)
	}

	oldEnv := existing.getEnv()
	newEnv := cl.ToEnvironment()

	// Only the display name changed if servers, TLS, and NATS conn all match.
	nameOnly := serversSame(oldEnv.Servers, newEnv.Servers) &&
		tlsSame(oldEnv.TLS, newEnv.TLS) &&
		natsConnSame(oldEnv.NATSConn, newEnv.NATSConn)

	if nameOnly {
		// Fast path: update the env label in-place, no goroutine restart.
		existing.setEnv(newEnv)
		return nil
	}

	// Connection-affecting change: tear down and rebuild from the new config.
	if cancel, ok := m.cancels[cl.ID]; ok {
		cancel()
		delete(m.cancels, cl.ID)
	}
	delete(m.collectors, cl.ID)

	return m.addClusterLocked(cl)
}

// startCollector launches the poll loop plus any NATS push goroutines for c.
// The NATS push fields (subscriber/sys) are assigned BEFORE the poll loop
// goroutine starts so that poll() observes a stable collection mode from its
// very first tick — assigning them after `go c.run` would be a data race and
// could make the first poll silently take the HTTP path. Must be called with
// m.mu held.
func (m *Manager) startCollector(cctx context.Context, c *Collector, id string, env config.Environment) {
	if env.NATSConn != nil {
		c.subscriber = newMQTTSubscriber()
		c.subscriber.log = c.log
		if env.NATSConn.SYSCollection {
			c.sys = newSYSCollector()
			c.sys.log = c.log
			if !natsConnHasAuth(env.NATSConn) {
				c.log.Warn("sys_collection is enabled but no NATS credentials are configured — $SYS server collection requires system-account credentials (username/password, token, nkey, or creds file); topology and server stats will stay empty until they are provided")
			}
		}
	}

	go c.run(cctx, id, m.onChange)
	if c.subscriber != nil {
		go c.subscriber.run(cctx, env.NATSConn)
	}
	if c.sys != nil {
		go c.sys.run(cctx, env.NATSConn)
	}
}

// natsConnHasAuth reports whether any authentication field is set on the NATS
// connection config. A connection with no auth can only reach the default
// account and will never receive $SYS events.
func natsConnHasAuth(n *config.NATSConnConfig) bool {
	return n.Username != "" || n.Password != "" || n.Token != "" ||
		n.NKey != "" || n.CredsFile != ""
}

// collector returns the collector for a cluster ID, or nil if not tracked.
func (m *Manager) collector(id string) *Collector {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.collectors[id]
}

// ClusterConfig returns a copy of the live environment config for a cluster ID,
// for use in MQTT proxy handlers that need token/bridge resolution.
func (m *Manager) ClusterConfig(id string) *config.Environment {
	c := m.collector(id)
	if c == nil {
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

// ClusterHealth returns the operational health for a single cluster ID, or nil
// if the cluster is unknown.
func (m *Manager) ClusterHealth(clusterID string) *ClusterHealth {
	c := m.collector(clusterID)
	if c == nil {
		return nil
	}
	h := c.Health()
	h.ID = clusterID
	return &h
}

// HealthReport returns operational health for every tracked cluster. Collectors
// are gathered under the manager lock, then queried outside it so a slow
// snapshot read never blocks cluster add/remove.
func (m *Manager) HealthReport() []ClusterHealth {
	m.mu.RLock()
	ids := make([]string, 0, len(m.collectors))
	cols := make([]*Collector, 0, len(m.collectors))
	for id, c := range m.collectors {
		ids = append(ids, id)
		cols = append(cols, c)
	}
	m.mu.RUnlock()

	report := make([]ClusterHealth, 0, len(cols))
	for i, c := range cols {
		h := c.Health()
		h.ID = ids[i]
		report = append(report, h)
	}
	return report
}

// Snapshot returns the latest snapshot for a cluster ID.
func (m *Manager) Snapshot(clusterID string) *Snapshot {
	c := m.collector(clusterID)
	if c == nil {
		return nil
	}
	return c.Snapshot()
}

// PrevSnapshot returns the previous snapshot for a cluster ID.
func (m *Manager) PrevSnapshot(clusterID string) *Snapshot {
	c := m.collector(clusterID)
	if c == nil {
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
	c := m.collector(clusterID)
	if c == nil {
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
	c := m.collector(clusterID)
	if c == nil {
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
	c := m.collector(clusterID)
	if c == nil {
		return nil
	}
	return c.fetcher
}

// EnvServers returns the configured server URLs for a cluster ID.
func (m *Manager) EnvServers(clusterID string) []string {
	c := m.collector(clusterID)
	if c == nil {
		return nil
	}
	env := c.getEnv()
	urls := make([]string, len(env.Servers))
	for i, s := range env.Servers {
		urls[i] = s.URL
	}
	return urls
}

// serversSame reports whether two server slices are structurally identical, so
// the name-only fast path can skip a rebuild.
func serversSame(a, b []config.Server) bool {
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

// tlsSame reports whether two TLS configs are structurally identical.
func tlsSame(a, b *config.TLSConfig) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	return a.CAFile == b.CAFile && a.Insecure == b.Insecure
}

// natsConnSame reports whether two NATSConnConfig values are equivalent for
// the purpose of deciding whether to rebuild the collector (and its subscriber).
func natsConnSame(a, b *config.NATSConnConfig) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	if len(a.URLs) != len(b.URLs) {
		return false
	}
	for i := range a.URLs {
		if a.URLs[i] != b.URLs[i] {
			return false
		}
	}
	return a.Username == b.Username &&
		a.Password == b.Password &&
		a.Token == b.Token &&
		a.NKey == b.NKey &&
		a.CredsFile == b.CredsFile &&
		a.SubjectPrefix == b.SubjectPrefix &&
		a.SYSCollection == b.SYSCollection
}
