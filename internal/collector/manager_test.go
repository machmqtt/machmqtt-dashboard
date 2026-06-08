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
