package collector

import (
	"context"
	"io"
	"log/slog"
	"testing"

	"github.com/noodlebit/machmqtt-dashboard/internal/config"
	"github.com/noodlebit/machmqtt-dashboard/internal/store"
)

// testStore opens a temporary store using a temp dir.
func testStore(t *testing.T) *store.Store {
	t.Helper()
	s, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func testCfg() *config.Config {
	return &config.Config{PollInterval: 30e9}
}

func testLog() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// addClusterToStore is a helper that creates a cluster in the store and returns it.
func addClusterToStore(t *testing.T, s *store.Store, name, url string) store.Cluster {
	t.Helper()
	cl := &store.Cluster{
		Name:    name,
		Servers: []config.Server{{URL: url}},
	}
	if err := s.CreateCluster(cl); err != nil {
		t.Fatalf("create cluster %q: %v", name, err)
	}
	return *cl
}

// --- NewManager ---

func TestManagerEmptyOnStart(t *testing.T) {
	s := testStore(t)
	m, err := NewManager(testCfg(), nil, testLog(), s)
	if err != nil {
		t.Fatal(err)
	}
	if ids := m.ClusterIDs(); len(ids) != 0 {
		t.Errorf("ClusterIDs = %v, want empty", ids)
	}
}

func TestManagerLoadsFromDB(t *testing.T) {
	s := testStore(t)
	addClusterToStore(t, s, "alpha", "http://alpha:8222")
	addClusterToStore(t, s, "beta", "http://beta:8222")

	m, err := NewManager(testCfg(), nil, testLog(), s)
	if err != nil {
		t.Fatal(err)
	}

	ids := m.ClusterIDs()
	if len(ids) != 2 {
		t.Fatalf("ClusterIDs len = %d, want 2", len(ids))
	}
}

// --- ClusterConfig ---

func TestManagerClusterConfigUnknownID(t *testing.T) {
	s := testStore(t)
	m, _ := NewManager(testCfg(), nil, testLog(), s)
	if m.ClusterConfig("nope") != nil {
		t.Error("expected nil for unknown cluster ID")
	}
}

func TestManagerClusterConfigKnownID(t *testing.T) {
	s := testStore(t)
	cl := addClusterToStore(t, s, "prod", "http://prod:8222")

	m, _ := NewManager(testCfg(), nil, testLog(), s)
	env := m.ClusterConfig(cl.ID)
	if env == nil {
		t.Fatal("expected non-nil config for known cluster")
	}
	if env.Name != "prod" {
		t.Errorf("Name = %q, want prod", env.Name)
	}
}

// --- AddCluster / RemoveCluster ---

func TestManagerAddCluster(t *testing.T) {
	s := testStore(t)
	m, _ := NewManager(testCfg(), nil, testLog(), s)

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	m.Start(ctx)

	cl := addClusterToStore(t, s, "new", "http://new:8222")
	if err := m.AddCluster(cl); err != nil {
		t.Fatal(err)
	}

	ids := m.ClusterIDs()
	found := false
	for _, id := range ids {
		if id == cl.ID {
			found = true
		}
	}
	if !found {
		t.Errorf("cluster %q not in ClusterIDs after Add: %v", cl.ID, ids)
	}

	env := m.ClusterConfig(cl.ID)
	if env == nil || env.Name != "new" {
		t.Errorf("ClusterConfig after Add = %v", env)
	}
}

func TestManagerAddClusterIdempotent(t *testing.T) {
	s := testStore(t)
	m, _ := NewManager(testCfg(), nil, testLog(), s)

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	m.Start(ctx)

	cl := addClusterToStore(t, s, "x", "http://x:8222")
	m.AddCluster(cl)
	if err := m.AddCluster(cl); err != nil {
		t.Errorf("second AddCluster should be a no-op, got: %v", err)
	}
	if len(m.ClusterIDs()) != 1 {
		t.Errorf("expected 1 cluster after two Adds of the same id, got %d", len(m.ClusterIDs()))
	}
}

func TestManagerRemoveCluster(t *testing.T) {
	s := testStore(t)
	cl := addClusterToStore(t, s, "gone", "http://gone:8222")

	m, _ := NewManager(testCfg(), nil, testLog(), s)

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	m.Start(ctx)

	m.RemoveCluster(cl.ID)

	for _, id := range m.ClusterIDs() {
		if id == cl.ID {
			t.Errorf("cluster %q still in ClusterIDs after Remove", cl.ID)
		}
	}
	if m.ClusterConfig(cl.ID) != nil {
		t.Error("ClusterConfig should be nil after Remove")
	}
}

func TestManagerRemoveUnknownClusterIsNoOp(t *testing.T) {
	s := testStore(t)
	m, _ := NewManager(testCfg(), nil, testLog(), s)
	// Should not panic.
	m.RemoveCluster("nope")
}

// --- UpdateCluster ---

func TestManagerUpdateClusterNameOnly(t *testing.T) {
	s := testStore(t)
	cl := addClusterToStore(t, s, "old-name", "http://same:8222")

	m, _ := NewManager(testCfg(), nil, testLog(), s)

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	m.Start(ctx)

	// Same server URL, different name → fast-path: in-place label swap.
	renamed := cl
	renamed.Name = "new-name"
	if err := m.UpdateCluster(renamed); err != nil {
		t.Fatal(err)
	}

	env := m.ClusterConfig(cl.ID)
	if env == nil {
		t.Fatal("ClusterConfig nil after rename")
	}
	if env.Name != "new-name" {
		t.Errorf("Name = %q, want new-name", env.Name)
	}
}

func TestManagerUpdateClusterServerChange(t *testing.T) {
	s := testStore(t)
	cl := addClusterToStore(t, s, "x", "http://old:8222")

	m, _ := NewManager(testCfg(), nil, testLog(), s)

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	m.Start(ctx)

	// Different server URL → rebuild path.
	changed := cl
	changed.Servers = []config.Server{{URL: "http://new:8222"}}
	if err := m.UpdateCluster(changed); err != nil {
		t.Fatal(err)
	}

	env := m.ClusterConfig(cl.ID)
	if env == nil {
		t.Fatal("ClusterConfig nil after server change")
	}
	if env.Servers[0].URL != "http://new:8222" {
		t.Errorf("Server URL = %q, want http://new:8222", env.Servers[0].URL)
	}
}

// --- Race detector: concurrent access ---

// --- Snapshot / PrevSnapshot ---

func TestManagerSnapshotUnknownID(t *testing.T) {
	s := testStore(t)
	m, _ := NewManager(testCfg(), nil, testLog(), s)
	if m.Snapshot("nope") != nil {
		t.Error("expected nil Snapshot for unknown cluster")
	}
}

func TestManagerSnapshotKnownID(t *testing.T) {
	s := testStore(t)
	cl := addClusterToStore(t, s, "snap", "http://snap:8222")
	m, _ := NewManager(testCfg(), nil, testLog(), s)
	if m.Snapshot(cl.ID) == nil {
		t.Error("expected non-nil initial Snapshot for known cluster")
	}
}

func TestManagerPrevSnapshotUnknownID(t *testing.T) {
	s := testStore(t)
	m, _ := NewManager(testCfg(), nil, testLog(), s)
	if m.PrevSnapshot("nope") != nil {
		t.Error("expected nil PrevSnapshot for unknown cluster")
	}
}

func TestManagerPrevSnapshotKnownID(t *testing.T) {
	s := testStore(t)
	cl := addClusterToStore(t, s, "prev", "http://prev:8222")
	m, _ := NewManager(testCfg(), nil, testLog(), s)
	// prev is nil before any poll
	if snap := m.PrevSnapshot(cl.ID); snap != nil {
		t.Errorf("expected nil PrevSnapshot before first poll, got %v", snap)
	}
}

// --- Overview ---

func TestManagerOverviewUnknownID(t *testing.T) {
	s := testStore(t)
	m, _ := NewManager(testCfg(), nil, testLog(), s)
	if m.Overview("nope") != nil {
		t.Error("expected nil Overview for unknown cluster")
	}
}

func TestManagerOverviewKnownID(t *testing.T) {
	s := testStore(t)
	cl := addClusterToStore(t, s, "ov", "http://ov:8222")
	m, _ := NewManager(testCfg(), nil, testLog(), s)
	// buildOverview on an empty snapshot should not panic and may return non-nil.
	_ = m.Overview(cl.ID)
}

// --- Topology ---

func TestManagerTopologyUnknownID(t *testing.T) {
	s := testStore(t)
	m, _ := NewManager(testCfg(), nil, testLog(), s)
	if m.Topology("nope") != nil {
		t.Error("expected nil Topology for unknown cluster")
	}
}

func TestManagerTopologyKnownID(t *testing.T) {
	s := testStore(t)
	cl := addClusterToStore(t, s, "topo", "http://topo:8222")
	m, _ := NewManager(testCfg(), nil, testLog(), s)
	_ = m.Topology(cl.ID) // empty snapshot → should not panic
}

// --- Health ---

func TestManagerHealthUnknownID(t *testing.T) {
	s := testStore(t)
	m, _ := NewManager(testCfg(), nil, testLog(), s)
	if m.Health("nope") != nil {
		t.Error("expected nil Health for unknown cluster")
	}
}

func TestManagerHealthKnownID(t *testing.T) {
	s := testStore(t)
	cl := addClusterToStore(t, s, "hlth", "http://hlth:8222")
	m, _ := NewManager(testCfg(), nil, testLog(), s)
	if h := m.Health(cl.ID); h == nil {
		t.Error("expected non-nil Health map for known cluster (initial empty snapshot)")
	}
}

// --- MQTTBridges ---

func TestManagerMQTTBridgesUnknownID(t *testing.T) {
	s := testStore(t)
	m, _ := NewManager(testCfg(), nil, testLog(), s)
	if m.MQTTBridges("nope") != nil {
		t.Error("expected nil MQTTBridges for unknown cluster")
	}
}

func TestManagerMQTTBridgesKnownID(t *testing.T) {
	s := testStore(t)
	cl := addClusterToStore(t, s, "mqtt", "http://mqtt:8222")
	m, _ := NewManager(testCfg(), nil, testLog(), s)
	// No subscriber set → returns mqttBridges slice (nil initially, not a hard rule)
	_ = m.MQTTBridges(cl.ID)
}

// --- Environments ---

func TestManagerEnvironmentsMatchesClusterIDs(t *testing.T) {
	s := testStore(t)
	addClusterToStore(t, s, "e1", "http://e1:8222")
	addClusterToStore(t, s, "e2", "http://e2:8222")
	m, _ := NewManager(testCfg(), nil, testLog(), s)
	envs := m.Environments()
	ids := m.ClusterIDs()
	if len(envs) != len(ids) {
		t.Errorf("Environments() len %d != ClusterIDs() len %d", len(envs), len(ids))
	}
}

// --- Fetcher ---

func TestManagerFetcherUnknownID(t *testing.T) {
	s := testStore(t)
	m, _ := NewManager(testCfg(), nil, testLog(), s)
	if m.Fetcher("nope") != nil {
		t.Error("expected nil Fetcher for unknown cluster")
	}
}

func TestManagerFetcherKnownID(t *testing.T) {
	s := testStore(t)
	cl := addClusterToStore(t, s, "ftch", "http://ftch:8222")
	m, _ := NewManager(testCfg(), nil, testLog(), s)
	if m.Fetcher(cl.ID) == nil {
		t.Error("expected non-nil Fetcher for known cluster")
	}
}

// --- EnvServers ---

func TestManagerEnvServersUnknownID(t *testing.T) {
	s := testStore(t)
	m, _ := NewManager(testCfg(), nil, testLog(), s)
	if m.EnvServers("nope") != nil {
		t.Error("expected nil EnvServers for unknown cluster")
	}
}

func TestManagerEnvServersKnownID(t *testing.T) {
	s := testStore(t)
	cl := addClusterToStore(t, s, "esrv", "http://esrv:8222")
	m, _ := NewManager(testCfg(), nil, testLog(), s)
	urls := m.EnvServers(cl.ID)
	if len(urls) != 1 || urls[0] != "http://esrv:8222" {
		t.Errorf("EnvServers = %v, want [http://esrv:8222]", urls)
	}
}

// --- Pure functions: tlsSame, natsConnSame, isMQTTBridgeConn ---

func TestTLSEqual(t *testing.T) {
	if !tlsSame(nil, nil) {
		t.Error("both nil should be equal")
	}
	cfg := &config.TLSConfig{CAFile: "ca.pem", Insecure: false}
	if tlsSame(nil, cfg) {
		t.Error("nil vs non-nil should not be equal")
	}
	if tlsSame(cfg, nil) {
		t.Error("non-nil vs nil should not be equal")
	}
	if !tlsSame(cfg, &config.TLSConfig{CAFile: "ca.pem", Insecure: false}) {
		t.Error("identical configs should be equal")
	}
	if tlsSame(cfg, &config.TLSConfig{CAFile: "other.pem", Insecure: false}) {
		t.Error("different CAFile should not be equal")
	}
	if tlsSame(cfg, &config.TLSConfig{CAFile: "ca.pem", Insecure: true}) {
		t.Error("different Insecure should not be equal")
	}
}

func TestNATSConnEqual(t *testing.T) {
	if !natsConnSame(nil, nil) {
		t.Error("both nil should be equal")
	}
	a := &config.NATSConnConfig{URLs: []string{"nats://host:4222"}, SubjectPrefix: "$MQTT5"}
	if natsConnSame(nil, a) {
		t.Error("nil vs non-nil should not be equal")
	}
	if natsConnSame(a, nil) {
		t.Error("non-nil vs nil should not be equal")
	}
	b := &config.NATSConnConfig{URLs: []string{"nats://host:4222"}, SubjectPrefix: "$MQTT5"}
	if !natsConnSame(a, b) {
		t.Error("identical configs should be equal")
	}
	// Different URL count
	c := &config.NATSConnConfig{URLs: []string{"nats://a:4222", "nats://b:4222"}}
	if natsConnSame(a, c) {
		t.Error("different URL count should not be equal")
	}
	// Different URL value
	d := &config.NATSConnConfig{URLs: []string{"nats://other:4222"}, SubjectPrefix: "$MQTT5"}
	if natsConnSame(a, d) {
		t.Error("different URL value should not be equal")
	}
	// Different SubjectPrefix
	e := &config.NATSConnConfig{URLs: []string{"nats://host:4222"}, SubjectPrefix: "acme"}
	if natsConnSame(a, e) {
		t.Error("different SubjectPrefix should not be equal")
	}
	// Different SYSCollection
	f := &config.NATSConnConfig{URLs: []string{"nats://host:4222"}, SubjectPrefix: "$MQTT5", SYSCollection: true}
	if natsConnSame(a, f) {
		t.Error("different SYSCollection should not be equal")
	}
}

// --- serversSame ---

func TestServersEqualDifferentLength(t *testing.T) {
	a := []config.Server{{URL: "http://a:8222"}}
	b := []config.Server{{URL: "http://a:8222"}, {URL: "http://b:8222"}}
	if serversSame(a, b) {
		t.Error("different-length slices should not be equal")
	}
}

func TestServersEqualDifferentURL(t *testing.T) {
	a := []config.Server{{URL: "http://a:8222"}}
	b := []config.Server{{URL: "http://b:8222"}}
	if serversSame(a, b) {
		t.Error("different URLs should not be equal")
	}
}

func TestServersEqualIdentical(t *testing.T) {
	a := []config.Server{{URL: "http://a:8222"}, {URL: "http://b:8222"}}
	if !serversSame(a, a) {
		t.Error("identical slices should be equal")
	}
}

func TestIsMQTTBridgeConn(t *testing.T) {
	if !isMQTTBridgeConn("machmqtt-bridge") {
		t.Error("machmqtt-bridge should match")
	}
	if !isMQTTBridgeConn("machmqtt-pool-0") {
		t.Error("machmqtt-pool-0 should match")
	}
	if !isMQTTBridgeConn("machmqtt-pool-99") {
		t.Error("machmqtt-pool-99 should match")
	}
	if isMQTTBridgeConn("nats-client") {
		t.Error("nats-client should not match")
	}
	if isMQTTBridgeConn("") {
		t.Error("empty string should not match")
	}
}

func TestManagerConcurrentAccessNoRace(t *testing.T) {
	s := testStore(t)
	addClusterToStore(t, s, "a", "http://a:8222")
	addClusterToStore(t, s, "b", "http://b:8222")

	m, _ := NewManager(testCfg(), nil, testLog(), s)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	m.Start(ctx)

	const goroutines = 10
	done := make(chan struct{}, goroutines*3)

	for range goroutines {
		go func() {
			_ = m.ClusterIDs()
			done <- struct{}{}
		}()
		go func() {
			cl := addClusterToStore(t, s, "dyn", "http://dyn:8222")
			_ = m.AddCluster(cl)
			m.RemoveCluster(cl.ID)
			done <- struct{}{}
		}()
		go func() {
			_ = m.ClusterConfig("a")
			done <- struct{}{}
		}()
	}

	for range goroutines * 3 {
		<-done
	}
}
