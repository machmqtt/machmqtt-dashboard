package collector

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"math/big"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	nats "github.com/nats-io/nats.go"
	"github.com/noodlebit/machmqtt-dashboard/internal/config"
	"github.com/noodlebit/machmqtt-dashboard/internal/store"
	"github.com/noodlebit/machmqtt-dashboard/internal/testutil/natstest"
)

// mockNATSServer serves minimal NATS monitoring JSON for each endpoint.
func mockNATSServer(serverID, serverName string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		now := time.Now()
		switch r.URL.Path {
		case "/varz":
			json.NewEncoder(w).Encode(Varz{
				ServerID:    serverID,
				ServerName:  serverName,
				Connections: 5,
				Now:         now,
				Start:       now.Add(-time.Hour),
			})
		case "/routez":
			json.NewEncoder(w).Encode(Routez{ServerID: serverID})
		case "/gatewayz":
			json.NewEncoder(w).Encode(Gatewayz{ServerID: serverID})
		case "/leafz":
			json.NewEncoder(w).Encode(Leafz{ServerID: serverID})
		case "/healthz":
			json.NewEncoder(w).Encode(HealthStatus{Status: "ok"})
		case "/connz":
			json.NewEncoder(w).Encode(Connz{ServerID: serverID, NumConns: 5})
		case "/subsz":
			json.NewEncoder(w).Encode(SubszResp{ServerID: serverID})
		case "/jsz":
			json.NewEncoder(w).Encode(JSInfo{ServerID: serverID})
		case "/accountz":
			json.NewEncoder(w).Encode(Accountz{})
		default:
			http.NotFound(w, r)
		}
	}
}

func newLiveCluster(t *testing.T, s *store.Store, name string, handler http.Handler) store.Cluster {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return addClusterToStore(t, s, name, srv.URL)
}

// --- Collector poll with real HTTP server ---

func TestCollectorFastPollPopulatesVarz(t *testing.T) {
	s := testStore(t)
	cl := newLiveCluster(t, s, "live", mockNATSServer("srv-1", "nats-1"))

	m, err := NewManager(testCfg(), nil, testLog(), s)
	if err != nil {
		t.Fatal(err)
	}

	m.mu.RLock()
	c := m.collectors[cl.ID]
	m.mu.RUnlock()

	c.poll(context.Background(), cl.ID, false)

	snap := c.Snapshot()
	if snap == nil {
		t.Fatal("snapshot is nil after poll")
	}
	if len(snap.Varz) == 0 {
		t.Error("Varz is empty after poll")
	}
	if _, ok := snap.Varz["srv-1"]; !ok {
		t.Error("expected srv-1 in Varz after poll")
	}
}

func TestCollectorSlowPollPopulatesConnz(t *testing.T) {
	s := testStore(t)
	cl := newLiveCluster(t, s, "live-slow", mockNATSServer("srv-2", "nats-2"))

	m, err := NewManager(testCfg(), nil, testLog(), s)
	if err != nil {
		t.Fatal(err)
	}

	m.mu.RLock()
	c := m.collectors[cl.ID]
	m.mu.RUnlock()

	c.poll(context.Background(), cl.ID, true)

	snap := c.Snapshot()
	if snap == nil {
		t.Fatal("snapshot is nil")
	}
	if len(snap.Connz) == 0 {
		t.Error("Connz is empty after slow poll")
	}
}

func TestCollectorComputesRatesAcrossPolls(t *testing.T) {
	s := testStore(t)
	cl := newLiveCluster(t, s, "rates", mockNATSServer("srv-r", "nats-r"))

	m, err := NewManager(testCfg(), nil, testLog(), s)
	if err != nil {
		t.Fatal(err)
	}

	m.mu.RLock()
	c := m.collectors[cl.ID]
	m.mu.RUnlock()

	c.poll(context.Background(), cl.ID, false)
	time.Sleep(10 * time.Millisecond)
	c.poll(context.Background(), cl.ID, false)

	snap := c.Snapshot()
	if snap == nil {
		t.Fatal("snapshot nil")
	}
	// prev should now be set after two polls.
	prev := c.PrevSnapshot()
	if prev == nil {
		t.Error("expected non-nil PrevSnapshot after two polls")
	}
}

// --- run() with onChange callback ---

func TestCollectorRunCallsOnChange(t *testing.T) {
	s := testStore(t)
	cl := newLiveCluster(t, s, "onchange", mockNATSServer("srv-4", "nats-4"))

	cfg := testCfg()
	cfg.PollInterval = 30 * time.Millisecond

	called := make(chan string, 10)
	onChange := func(id string) { called <- id }

	m, err := NewManager(cfg, onChange, testLog(), s)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	m.Start(ctx)

	// Wait for the initial onChange call.
	select {
	case id := <-called:
		if id != cl.ID {
			t.Errorf("onChange called with %q, want %q", id, cl.ID)
		}
	case <-time.After(2 * time.Second):
		t.Error("onChange not called within 2s")
	}
	cancel()
}

func TestCollectorRunTickerFiresOnChange(t *testing.T) {
	s := testStore(t)
	cl := newLiveCluster(t, s, "ticker", mockNATSServer("srv-5", "nats-5"))

	cfg := testCfg()
	cfg.PollInterval = 20 * time.Millisecond

	polls := make(chan struct{}, 20)
	onChange := func(id string) {
		if id == cl.ID {
			select {
			case polls <- struct{}{}:
			default:
			}
		}
	}

	m, err := NewManager(cfg, onChange, testLog(), s)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	m.Start(ctx)

	// Collect 3 polls (initial + 2 ticker fires) to exercise the ticker path.
	count := 0
	deadline := time.After(3 * time.Second)
	for count < 3 {
		select {
		case <-polls:
			count++
		case <-deadline:
			t.Errorf("only got %d polls, want ≥3", count)
			cancel()
			return
		}
	}
	cancel()
}

// --- UpdateCluster with untracked ID ---

func TestManagerUpdateClusterUntracked(t *testing.T) {
	s := testStore(t)
	m, _ := NewManager(testCfg(), nil, testLog(), s)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	m.Start(ctx)

	// CreateCluster so it's in the DB, but don't add it to the manager first.
	cl := &store.Cluster{
		Name:    "untracked",
		Servers: []config.Server{{URL: "http://untracked:8222"}},
	}
	if err := s.CreateCluster(cl); err != nil {
		t.Fatalf("create cluster: %v", err)
	}

	// Manually remove it from the manager's map to simulate "untracked".
	m.mu.Lock()
	delete(m.collectors, cl.ID)
	m.mu.Unlock()

	if err := m.UpdateCluster(*cl); err != nil {
		t.Fatal(err)
	}
	if m.ClusterConfig(cl.ID) == nil {
		t.Error("expected cluster to be tracked after UpdateCluster on untracked ID")
	}
}

// --- AddCluster with NATSConn ---

func TestManagerAddClusterWithNATSConn(t *testing.T) {
	s := testStore(t)
	m, _ := NewManager(testCfg(), nil, testLog(), s)

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	m.Start(ctx)

	cl := store.Cluster{
		Name:    "nats-cluster",
		Servers: []config.Server{{URL: "http://nats:8222"}},
		NATSConn: &config.NATSConnConfig{
			URLs:          []string{"nats://127.0.0.1:14299"},
			SubjectPrefix: "$MQTT5",
		},
	}
	if err := s.CreateCluster(&cl); err != nil {
		t.Fatalf("create cluster: %v", err)
	}
	if err := m.AddCluster(cl); err != nil {
		t.Fatal(err)
	}
	if m.ClusterConfig(cl.ID) == nil {
		t.Error("expected cluster to be tracked")
	}
}

// --- Manager Start with NATSConn (exercises subscriber startup in Start) ---

func TestManagerStartWithNATSConn(t *testing.T) {
	s := testStore(t)

	// Pre-load a cluster with NATSConn so NewManager loads it and Start spawns the subscriber.
	cl := &store.Cluster{
		Name:    "nats-pre",
		Servers: []config.Server{{URL: "http://nats:8222"}},
		NATSConn: &config.NATSConnConfig{
			URLs:          []string{"nats://127.0.0.1:14299"},
			SubjectPrefix: "$MQTT5",
		},
	}
	if err := s.CreateCluster(cl); err != nil {
		t.Fatalf("create cluster: %v", err)
	}

	m, err := NewManager(testCfg(), nil, testLog(), s)
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	m.Start(ctx) // should spawn subscriber goroutines without panic
	time.Sleep(20 * time.Millisecond)
}

func TestManagerStartWithSYSCollection(t *testing.T) {
	s := testStore(t)

	cl := &store.Cluster{
		Name:    "sys-pre",
		Servers: []config.Server{{URL: "http://sys:8222"}},
		NATSConn: &config.NATSConnConfig{
			URLs:          []string{"nats://127.0.0.1:14299"},
			SubjectPrefix: "$MQTT5",
			SYSCollection: true,
		},
	}
	if err := s.CreateCluster(cl); err != nil {
		t.Fatalf("create cluster: %v", err)
	}

	m, err := NewManager(testCfg(), nil, testLog(), s)
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	m.Start(ctx)
	time.Sleep(20 * time.Millisecond)
}

// --- NewManager error paths ---

func TestNewManagerDBError(t *testing.T) {
	s := testStore(t)
	s.Close() // close the DB to force a ListClusters error

	_, err := NewManager(testCfg(), nil, testLog(), s)
	if err == nil {
		t.Error("expected error when DB is closed")
	}
}

func TestNewManagerFetcherError(t *testing.T) {
	s := testStore(t)
	// Cluster with a CA file that doesn't exist → NewFetcher returns error.
	cl := &store.Cluster{
		Name:    "bad-tls",
		Servers: []config.Server{{URL: "http://srv:8222"}},
		TLS:     &config.TLSConfig{CAFile: "/nonexistent/no-such-ca.pem"},
	}
	if err := s.CreateCluster(cl); err != nil {
		t.Fatalf("create cluster: %v", err)
	}

	_, err := NewManager(testCfg(), nil, testLog(), s)
	if err == nil {
		t.Error("expected error when cluster has invalid TLS CA file")
	}
}

// --- NewFetcher with valid CA file ---

// testCAPem returns a self-signed CA certificate in PEM form.
func testCAPem(t *testing.T) []byte {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "collector-test-ca"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
}

// rootCAs digs the configured root pool out of the fetcher's transport so the
// assertions below check the CA was actually installed, not merely that
// NewFetcher returned without error.
func rootCAs(t *testing.T, f *Fetcher) *x509.CertPool {
	t.Helper()
	transport, ok := f.client.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("transport is %T, want *http.Transport", f.client.Transport)
	}
	if transport.TLSClientConfig == nil {
		t.Fatal("TLSClientConfig is nil, want a configured root pool")
	}
	return transport.TLSClientConfig.RootCAs
}

func TestNewFetcherValidCAFile(t *testing.T) {
	caFile := filepath.Join(t.TempDir(), "ca.pem")
	if err := os.WriteFile(caFile, testCAPem(t), 0o600); err != nil {
		t.Fatal(err)
	}

	// The fetcher works from bytes, so the path is resolved first — exactly as
	// the config loader and the store do before a collector is ever built.
	cfg := &config.TLSConfig{CAFile: caFile}
	if err := cfg.ResolveCAFile(); err != nil {
		t.Fatal(err)
	}

	f, err := NewFetcher(cfg)
	if err != nil {
		t.Fatal(err)
	}
	pool := rootCAs(t, f)
	if pool == nil {
		t.Fatal("RootCAs is nil, want the configured CA")
	}
	if len(pool.Subjects()) != 1 { //nolint:staticcheck // Subjects is fine for a pool we built
		t.Errorf("pool has %d subjects, want 1", len(pool.Subjects())) //nolint:staticcheck
	}
}

// TestNewFetcherInlineCAPem covers the API-facing alternative to ca_file: the
// PEM arrives inline, so no filesystem path is involved.
func TestNewFetcherInlineCAPem(t *testing.T) {
	f, err := NewFetcher(&config.TLSConfig{CAPem: string(testCAPem(t))})
	if err != nil {
		t.Fatal(err)
	}
	if pool := rootCAs(t, f); pool == nil {
		t.Fatal("RootCAs is nil, want the inline CA")
	}
}

// TestNewFetcherRejectsUnusablePEM pins the fix for a CA bundle that parses to
// nothing. The pool used to be installed empty, so every TLS connection failed
// later with an opaque verification error instead of here.
func TestNewFetcherRejectsUnusablePEM(t *testing.T) {
	if _, err := NewFetcher(&config.TLSConfig{CAPem: "not-real-pem"}); err == nil {
		t.Error("expected an error for inline PEM containing no certificates")
	}
}

// TestNewFetcherRejectsUnresolvedCAFile pins the other half of the
// path-injection fix: the fetcher never opens a path, so an unresolved ca_file
// must fail loudly rather than silently falling back to the system roots.
func TestNewFetcherRejectsUnresolvedCAFile(t *testing.T) {
	if _, err := NewFetcher(&config.TLSConfig{CAFile: "/etc/ssl/certs/ca.pem"}); err == nil {
		t.Error("expected an error for a CA file that was never loaded")
	}
}

// --- pollSYS path ---

func TestCollectorPollSYSFastPath(t *testing.T) {
	natsS := natstest.New(t)

	sStore := testStore(t)
	cl := addClusterToStore(t, sStore, "sys-fast", "http://sys:8222")

	m, err := NewManager(testCfg(), nil, testLog(), sStore)
	if err != nil {
		t.Fatal(err)
	}
	m.mu.RLock()
	c := m.collectors[cl.ID]
	m.mu.RUnlock()

	// Need a real NATS connection so sc.poll() doesn't return nil immediately.
	nc, err := nats.Connect(natsS.ClientURL())
	if err != nil {
		t.Fatal(err)
	}
	defer nc.Close()

	sc := newSYSCollector()
	sc.mu.Lock()
	sc.nc = nc
	sc.statsz["srv-1"] = &statszEntry{
		server: sysServerInfo{ID: "srv-1", Name: "nats-1", Time: time.Now()},
		stats:  sysServerStats{Start: time.Now().Add(-time.Hour)},
		when:   time.Now(),
	}
	sc.mu.Unlock()
	c.sys = sc

	c.poll(context.Background(), cl.ID, false)

	snap := c.Snapshot()
	if snap == nil {
		t.Fatal("snapshot is nil after pollSYS")
	}
	if len(snap.Varz) == 0 {
		t.Error("expected Varz after pollSYS fast path")
	}
}

// --- $SYS → HTTP fallback ---

// TestCollectorSYSFallbackToHTTP verifies that when $SYS collection produces no
// data past the grace period, poll() falls back to the HTTP path and populates
// the snapshot from the configured monitoring URL, then resumes $SYS when it
// recovers.
func TestCollectorSYSFallbackToHTTP(t *testing.T) {
	s := testStore(t)
	// A live HTTP monitoring endpoint to fall back to.
	cl := newLiveCluster(t, s, "sys-fallback", mockNATSServer("http-srv", "http-nats"))

	m, err := NewManager(testCfg(), nil, testLog(), s)
	if err != nil {
		t.Fatal(err)
	}
	m.mu.RLock()
	c := m.collectors[cl.ID]
	m.mu.RUnlock()

	// Attach a $SYS collector with NO NATS connection so sys.poll() returns nil
	// (no data). subscriber non-nil mirrors how Start() wires a NATSConn cluster.
	c.sys = newSYSCollector()
	c.subscriber = newMQTTSubscriber()

	// Cold start: $SYS has never produced data, so the first poll must fall back
	// to HTTP immediately (no grace period) and populate Varz from the mock
	// monitoring server.
	c.poll(context.Background(), cl.ID, false)
	if !c.sysFellBack.Load() {
		t.Fatal("expected immediate HTTP fallback on cold start (no $SYS data ever)")
	}
	if _, ok := c.Snapshot().Varz["http-srv"]; !ok {
		t.Error("expected http-srv in Varz after cold-start HTTP fallback")
	}

	// $SYS recovers: seed the STATSZ cache so sys.poll() returns a server. A real
	// NATS connection is needed for sys.poll's nc != nil check.
	natsS := natstest.New(t)
	nc, err := nats.Connect(natsS.ClientURL())
	if err != nil {
		t.Fatal(err)
	}
	defer nc.Close()
	c.sys.mu.Lock()
	c.sys.nc = nc
	c.sys.statsz["sys-srv"] = &statszEntry{
		server: sysServerInfo{ID: "sys-srv", Name: "sys-nats", Time: time.Now()},
		stats:  sysServerStats{Start: time.Now().Add(-time.Hour)},
		when:   time.Now(),
	}
	c.sys.mu.Unlock()

	c.poll(context.Background(), cl.ID, false)
	if c.sysFellBack.Load() {
		t.Error("expected $SYS to resume and fallback to disengage")
	}
	if !c.sysEverHealthy {
		t.Error("expected sysEverHealthy to be set after recovery")
	}
	if _, ok := c.Snapshot().Varz["sys-srv"]; !ok {
		t.Error("expected sys-srv in Varz after $SYS recovery")
	}

	// Post-healthy outage: clear the STATSZ cache so $SYS yields nothing again.
	// Because $SYS was healthy, the grace period now applies — the collector must
	// NOT immediately fall back; it keeps serving the last $SYS snapshot.
	c.sys.mu.Lock()
	delete(c.sys.statsz, "sys-srv")
	c.sys.mu.Unlock()

	c.poll(context.Background(), cl.ID, false)
	if c.sysFellBack.Load() {
		t.Error("expected grace period to hold off fallback after a healthy period")
	}

	// Once the outage exceeds the grace period, fall back to HTTP again.
	c.sysFirstFail = time.Now().Add(-2 * sysFallbackGrace)
	c.poll(context.Background(), cl.ID, false)
	if !c.sysFellBack.Load() {
		t.Error("expected HTTP fallback after the grace period elapses")
	}
}

// TestShouldConnzScan verifies the "prefer push, else query connz-scan" gate:
// connz-scan runs while the push subscriber has no live bridges (HTTP-only or
// waiting for the first publish) and stops once push bridges arrive.
func TestShouldConnzScan(t *testing.T) {
	s := testStore(t)
	cl := addClusterToStore(t, s, "scan-gate", "http://scan:8222")
	m, err := NewManager(testCfg(), nil, testLog(), s)
	if err != nil {
		t.Fatal(err)
	}
	m.mu.RLock()
	c := m.collectors[cl.ID]
	m.mu.RUnlock()

	// HTTP only (no subscriber), discovery enabled by default → scan.
	if !c.shouldConnzScan() {
		t.Error("expected connz-scan when no push subscriber is configured")
	}

	// Subscriber configured but still empty (waiting for first publish) → scan.
	c.subscriber = newMQTTSubscriber()
	if !c.shouldConnzScan() {
		t.Error("expected connz-scan while the push subscriber has no bridges")
	}

	// Push bridge arrives → prefer push, stop connz-scan.
	c.subscriber.bridges["b1"] = &cachedBridge{
		msg:        &BridgeMetricsMsg{V: 1, InstanceName: "b1"},
		receivedAt: time.Now(),
	}
	if c.shouldConnzScan() {
		t.Error("expected no connz-scan once push bridges are present")
	}
}

// --- discoverMQTTBridges with store (exercises UpsertMQTTBridge / DeleteStaleMQTTBridges) ---

func TestDiscoverMQTTBridgesWithStore(t *testing.T) {
	// Start a real bridge admin server so the probe succeeds.
	srv := httptest.NewServer(bridgeAdminMux(t))
	defer srv.Close()
	port := portFromURL(srv.URL)

	enabled := true
	s := testStore(t)
	cl := &store.Cluster{
		Name:    "disc-store",
		Servers: []config.Server{{URL: "http://disc:8222"}},
		MQTTDiscovery: &config.MQTTDiscoveryConfig{
			Enabled:    &enabled,
			AdminPorts: []int{port},
		},
	}
	if err := s.CreateCluster(cl); err != nil {
		t.Fatalf("create cluster: %v", err)
	}

	m, err := NewManager(testCfg(), nil, testLog(), s)
	if err != nil {
		t.Fatal(err)
	}
	m.mu.RLock()
	c := m.collectors[cl.ID]
	m.mu.RUnlock()

	// Seed snapshot with a bridge connection on 127.0.0.1.
	c.snapMu.Lock()
	c.snapshot = &Snapshot{
		Timestamp: time.Now(),
		Varz:      map[string]*Varz{"srv-1": {ServerName: "nats-1"}},
		Connz: map[string]*Connz{
			"srv-1": {Conns: []ConnInfo{
				{Name: "machmqtt-bridge", IP: "127.0.0.1"},
			}},
		},
		ServerURLs: map[string]string{"srv-1": "127.0.0.1"},
		Health:     map[string]*HealthStatus{},
		Routez:     map[string]*Routez{},
		Rates:      map[string]*ServerRates{},
	}
	c.snapMu.Unlock()

	c.discoverMQTTBridges(context.Background(), cl.ID)

	// mqttBridges should be set (even if 0 due to probe result).
	c.mqttMu.RLock()
	bridges := c.mqttBridges
	c.mqttMu.RUnlock()
	_ = bridges // may be 1 or 0 depending on whether loopback is reachable
}

// --- collector.MQTTBridges path with subscriber ---

func TestCollectorMQTTBridgesFromSubscriber(t *testing.T) {
	s := testStore(t)
	cl := addClusterToStore(t, s, "sub-mqtt", "http://sub:8222")

	m, err := NewManager(testCfg(), nil, testLog(), s)
	if err != nil {
		t.Fatal(err)
	}
	m.mu.RLock()
	c := m.collectors[cl.ID]
	m.mu.RUnlock()

	// Attach a subscriber with a pre-loaded bridge.
	sub := newMQTTSubscriber()
	sub.bridges["my-bridge"] = &cachedBridge{
		msg: &BridgeMetricsMsg{
			V: 1, InstanceID: "id-1", InstanceName: "my-bridge",
		},
		receivedAt: time.Now(),
	}
	c.subscriber = sub

	bridges := m.MQTTBridges(cl.ID)
	if len(bridges) != 1 {
		t.Errorf("expected 1 bridge from subscriber, got %d", len(bridges))
	}
}

// --- UpdateCluster with NATSConn rebuild ---

func TestManagerUpdateClusterWithNATSConnRebuild(t *testing.T) {
	s := testStore(t)
	cl := addClusterToStore(t, s, "rebuild", "http://rebuild:8222")
	m, _ := NewManager(testCfg(), nil, testLog(), s)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	m.Start(ctx)

	// Change the cluster config to include a NATSConn — triggers rebuild path.
	changed := cl
	changed.Servers = []config.Server{{URL: "http://rebuild-new:8222"}}
	changed.NATSConn = &config.NATSConnConfig{
		URLs:          []string{"nats://127.0.0.1:14299"},
		SubjectPrefix: "$MQTT5",
	}
	if err := m.UpdateCluster(changed); err != nil {
		t.Fatal(err)
	}
	if m.ClusterConfig(cl.ID) == nil {
		t.Error("expected cluster after UpdateCluster with NATSConn rebuild")
	}
}

func TestManagerUpdateClusterWithSYSCollectionRebuild(t *testing.T) {
	s := testStore(t)
	cl := addClusterToStore(t, s, "sysrebuild", "http://sysrebuild:8222")
	m, _ := NewManager(testCfg(), nil, testLog(), s)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	m.Start(ctx)

	changed := cl
	changed.Servers = []config.Server{{URL: "http://sysrebuild-new:8222"}}
	changed.NATSConn = &config.NATSConnConfig{
		URLs:          []string{"nats://127.0.0.1:14299"},
		SubjectPrefix: "$MQTT5",
		SYSCollection: true,
	}
	if err := m.UpdateCluster(changed); err != nil {
		t.Fatal(err)
	}
}

// --- AddCluster with SYSCollection ---

func TestManagerAddClusterWithSYSCollection(t *testing.T) {
	s := testStore(t)
	m, _ := NewManager(testCfg(), nil, testLog(), s)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	m.Start(ctx)

	cl := store.Cluster{
		Name:    "sys-add",
		Servers: []config.Server{{URL: "http://sysadd:8222"}},
		NATSConn: &config.NATSConnConfig{
			URLs:          []string{"nats://127.0.0.1:14299"},
			SubjectPrefix: "$MQTT5",
			SYSCollection: true,
		},
	}
	if err := s.CreateCluster(&cl); err != nil {
		t.Fatalf("create cluster: %v", err)
	}
	if err := m.AddCluster(cl); err != nil {
		t.Fatal(err)
	}
}

// --- Pre-cancelled context: getWithStatus, PostAdmin, FetchMetrics transport errors ---

func TestMQTTGetWithStatusTransportError(t *testing.T) {
	f := NewMQTTBridgeFetcher("http://127.0.0.1:0", "b", "")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	code, err := f.getWithStatus(ctx, "/anything", nil)
	if err == nil {
		t.Errorf("expected transport error, code=%d", code)
	}
}

func TestMQTTPostAdminTransportError(t *testing.T) {
	f := NewMQTTBridgeFetcher("http://127.0.0.1:0", "b", "")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	code, _, err := f.PostAdmin(ctx, "/admin/action", nil)
	if err == nil {
		t.Errorf("expected transport error, code=%d", code)
	}
}

func TestMQTTFetchMetricsTransportError(t *testing.T) {
	f := NewMQTTBridgeFetcher("http://127.0.0.1:0", "b", "")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := f.FetchMetrics(ctx)
	if err == nil {
		t.Error("expected transport error for cancelled context")
	}
}
