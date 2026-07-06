package store

import (
	"testing"
	"time"

	"github.com/noodlebit/machmqtt-dashboard/internal/config"
)

// --- helpers ---

func makeCluster(name string, serverURLs ...string) *Cluster {
	servers := make([]config.Server, len(serverURLs))
	for i, u := range serverURLs {
		servers[i] = config.Server{URL: u}
	}
	return &Cluster{Name: name, Servers: servers}
}

func TestSeedClusters(t *testing.T) {
	s := testStore(t)

	envs := []config.Environment{
		{Name: "prod", Servers: []config.Server{{URL: "http://p:8222"}}},
		{Name: "staging", Servers: []config.Server{{URL: "http://s:8222"}}},
		{Name: "", Servers: []config.Server{{URL: "http://x:8222"}}}, // skipped (no name)
	}

	n, err := s.SeedClusters(envs)
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatalf("seeded = %d, want 2", n)
	}

	// Idempotent: a second call creates nothing.
	n, err = s.SeedClusters(envs)
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("second seed = %d, want 0", n)
	}

	// A runtime edit to an existing cluster must not be overwritten by re-seeding.
	clusters, _ := s.ListClusters()
	var prod *Cluster
	for i := range clusters {
		if clusters[i].Name == "prod" {
			prod = &clusters[i]
		}
	}
	if prod == nil {
		t.Fatal("seeded cluster 'prod' not found")
	}
	prod.Servers = []config.Server{{URL: "http://edited:8222"}}
	if err := s.UpdateCluster(prod); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SeedClusters(envs); err != nil {
		t.Fatal(err)
	}
	got, _ := s.GetCluster(prod.ID)
	if got.Servers[0].URL != "http://edited:8222" {
		t.Errorf("re-seed overwrote a runtime edit: %s", got.Servers[0].URL)
	}
}

// --- CRUD basics ---

func TestClusterCreateAndGet(t *testing.T) {
	s := testStore(t)

	cl := &Cluster{
		Name:       "prod",
		Servers:    []config.Server{{URL: "http://nats:8222"}},
		AdminToken: "secret",
	}
	if err := s.CreateCluster(cl); err != nil {
		t.Fatal(err)
	}
	if cl.ID == "" {
		t.Error("ID should be set after create")
	}
	if cl.CreatedAt.IsZero() {
		t.Error("CreatedAt should be set after create")
	}

	got, err := s.GetCluster(cl.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Name != "prod" {
		t.Errorf("Name = %q, want prod", got.Name)
	}
	if got.AdminToken != "secret" {
		t.Errorf("AdminToken = %q, want secret", got.AdminToken)
	}
	if len(got.Servers) != 1 || got.Servers[0].URL != "http://nats:8222" {
		t.Errorf("Servers = %v, want [{http://nats:8222}]", got.Servers)
	}
}

func TestClusterCreateValidation(t *testing.T) {
	s := testStore(t)

	if err := s.CreateCluster(&Cluster{Servers: []config.Server{{URL: "http://x"}}}); err == nil {
		t.Error("expected error for empty name")
	}
	if err := s.CreateCluster(&Cluster{Name: "x"}); err == nil {
		t.Error("expected error for empty servers")
	}
}

func TestClusterIDIsUnique(t *testing.T) {
	s := testStore(t)
	seen := make(map[string]bool)
	for i := 0; i < 20; i++ {
		cl := makeCluster("c", "http://x")
		if err := s.CreateCluster(cl); err != nil {
			t.Fatal(err)
		}
		if seen[cl.ID] {
			t.Fatalf("duplicate cluster ID: %s", cl.ID)
		}
		seen[cl.ID] = true
		if len(cl.ID) != 12 {
			t.Errorf("ID length = %d, want 12", len(cl.ID))
		}
	}
}

func TestClusterGetNotFound(t *testing.T) {
	s := testStore(t)
	_, err := s.GetCluster("nope")
	if err == nil {
		t.Error("expected error for nonexistent ID")
	}
}

// --- List ordering ---

func TestClusterListOrdering(t *testing.T) {
	s := testStore(t)

	for _, name := range []string{"zebra", "alpha", "middle"} {
		s.CreateCluster(makeCluster(name, "http://x"))
	}

	clusters, err := s.ListClusters()
	if err != nil {
		t.Fatal(err)
	}
	if len(clusters) != 3 {
		t.Fatalf("len = %d, want 3", len(clusters))
	}
	if clusters[0].Name != "alpha" || clusters[1].Name != "middle" || clusters[2].Name != "zebra" {
		t.Errorf("order = %v %v %v, want alpha middle zebra",
			clusters[0].Name, clusters[1].Name, clusters[2].Name)
	}
}

func TestClusterListEmpty(t *testing.T) {
	s := testStore(t)
	clusters, err := s.ListClusters()
	if err != nil {
		t.Fatal(err)
	}
	if clusters != nil {
		t.Errorf("expected nil slice for empty table, got %v", clusters)
	}
}

// --- Update ---

func TestClusterUpdate(t *testing.T) {
	s := testStore(t)

	cl := makeCluster("original", "http://old:8222")
	s.CreateCluster(cl)

	cl.Name = "renamed"
	cl.Servers = []config.Server{{URL: "http://new:8222"}}
	if err := s.UpdateCluster(cl); err != nil {
		t.Fatalf("update: %v", err)
	}

	got, _ := s.GetCluster(cl.ID)
	if got.Name != "renamed" {
		t.Errorf("Name = %q, want renamed", got.Name)
	}
	if got.Servers[0].URL != "http://new:8222" {
		t.Errorf("Servers[0].URL = %q, want http://new:8222", got.Servers[0].URL)
	}
}

func TestClusterUpdateValidation(t *testing.T) {
	s := testStore(t)
	cl := makeCluster("x", "http://x")
	s.CreateCluster(cl)

	cl.Name = ""
	if err := s.UpdateCluster(cl); err == nil {
		t.Error("expected error for empty name")
	}
	cl.Name = "x"
	cl.Servers = nil
	if err := s.UpdateCluster(cl); err == nil {
		t.Error("expected error for empty servers")
	}
}

func TestClusterUpdateNotFound(t *testing.T) {
	s := testStore(t)
	err := s.UpdateCluster(&Cluster{ID: "nope", Name: "x", Servers: []config.Server{{URL: "http://x"}}})
	if err == nil {
		t.Error("expected error for nonexistent ID")
	}
}

// --- Delete and cascade ---

func TestClusterDeleteCascade(t *testing.T) {
	s := testStore(t)

	// Two clusters so we can verify the cascade only affects the deleted one.
	a := makeCluster("alpha", "http://a")
	b := makeCluster("beta", "http://b")
	s.CreateCluster(a)
	s.CreateCluster(b)

	// Seed dependent rows for cluster a.
	s.UpsertMQTTBridge(a.ID, "1.2.3.4", "srv-1", "http://1.2.3.4:8080")
	s.SaveTopologyPositions(a.ID, []NodePosition{{NodeID: "n1", X: 1, Y: 2}})
	s.SaveTopologyCamera(a.ID, CameraState{Zoom: 1.5})

	// Seed a row for cluster b so we can confirm it survives.
	s.UpsertMQTTBridge(b.ID, "5.6.7.8", "srv-2", "http://5.6.7.8:8080")

	if err := s.DeleteCluster(a.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}

	// Cluster a should be gone.
	if _, err := s.GetCluster(a.ID); err == nil {
		t.Error("expected cluster a to be deleted")
	}

	// Cluster a's dependent rows should be gone.
	bridges, _ := s.ListMQTTBridges(a.ID)
	if len(bridges) != 0 {
		t.Errorf("mqtt_bridges for a: got %d, want 0", len(bridges))
	}

	// Cluster b's rows must survive.
	bridges, _ = s.ListMQTTBridges(b.ID)
	if len(bridges) != 1 {
		t.Errorf("mqtt_bridges for b: got %d, want 1", len(bridges))
	}
}

func TestClusterDeleteNotFound(t *testing.T) {
	s := testStore(t)
	if err := s.DeleteCluster("nope"); err == nil {
		t.Error("expected error for nonexistent ID")
	}
}

// --- Count ---

func TestClusterCount(t *testing.T) {
	s := testStore(t)

	n, err := s.ClusterCount()
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("count = %d, want 0", n)
	}

	a := makeCluster("a", "http://x")
	b := makeCluster("b", "http://y")
	s.CreateCluster(a)
	s.CreateCluster(b)

	n, _ = s.ClusterCount()
	if n != 2 {
		t.Errorf("count = %d, want 2", n)
	}

	s.DeleteCluster(a.ID)
	n, _ = s.ClusterCount()
	if n != 1 {
		t.Errorf("count = %d, want 1 after delete", n)
	}
}

// --- Nullable fields round-trip ---

func TestClusterNullableFieldsNil(t *testing.T) {
	s := testStore(t)
	cl := makeCluster("x", "http://x")
	s.CreateCluster(cl)

	got, _ := s.GetCluster(cl.ID)
	if got.MQTTDiscovery != nil {
		t.Errorf("MQTTDiscovery = %v, want nil", got.MQTTDiscovery)
	}
	if got.TLS != nil {
		t.Errorf("TLS = %v, want nil", got.TLS)
	}
	if got.MQTTBridges == nil {
		t.Error("MQTTBridges should be non-nil (empty slice), got nil")
	}
	if len(got.MQTTBridges) != 0 {
		t.Errorf("MQTTBridges len = %d, want 0", len(got.MQTTBridges))
	}
}

func TestClusterNullableFieldsSet(t *testing.T) {
	s := testStore(t)

	enabledTrue := true
	ports := []int{8080, 8443}
	cl := &Cluster{
		Name:    "full",
		Servers: []config.Server{{URL: "http://x"}},
		MQTTBridges: []config.MQTTBridge{
			{Name: "bridge1", URL: "http://bridge:8080", BearerToken: "tok"},
		},
		MQTTDiscovery: &config.MQTTDiscoveryConfig{
			Enabled:    &enabledTrue,
			AdminPorts: ports,
		},
		TLS: &config.TLSConfig{
			Insecure: true,
		},
	}
	if err := s.CreateCluster(cl); err != nil {
		t.Fatal(err)
	}

	got, err := s.GetCluster(cl.ID)
	if err != nil {
		t.Fatal(err)
	}

	if got.MQTTDiscovery == nil {
		t.Fatal("MQTTDiscovery is nil, want non-nil")
	}
	if got.MQTTDiscovery.Enabled == nil || !*got.MQTTDiscovery.Enabled {
		t.Error("MQTTDiscovery.Enabled = false/nil, want true")
	}
	if len(got.MQTTDiscovery.AdminPorts) != 2 || got.MQTTDiscovery.AdminPorts[0] != 8080 {
		t.Errorf("MQTTDiscovery.AdminPorts = %v, want [8080 8443]", got.MQTTDiscovery.AdminPorts)
	}

	if got.TLS == nil {
		t.Fatal("TLS is nil, want non-nil")
	}
	if !got.TLS.Insecure {
		t.Error("TLS.Insecure = false, want true")
	}

	if len(got.MQTTBridges) != 1 || got.MQTTBridges[0].Name != "bridge1" {
		t.Errorf("MQTTBridges = %v, want [{bridge1 ...}]", got.MQTTBridges)
	}
}

// --- ToEnvironment ---

func TestClusterToEnvironment(t *testing.T) {
	cl := &Cluster{
		Name:       "prod",
		Servers:    []config.Server{{URL: "http://x"}},
		AdminToken: "tok",
	}
	env := cl.ToEnvironment()
	if env.Name != "prod" {
		t.Errorf("Name = %q, want prod", env.Name)
	}
	if env.AdminToken != "tok" {
		t.Errorf("AdminToken = %q, want tok", env.AdminToken)
	}
	if len(env.Servers) != 1 || env.Servers[0].URL != "http://x" {
		t.Errorf("Servers = %v", env.Servers)
	}
}

// --- CreatedAt is populated ---

func TestClusterCreatedAt(t *testing.T) {
	s := testStore(t)
	before := time.Now().Add(-time.Second)
	cl := makeCluster("x", "http://x")
	s.CreateCluster(cl)
	if cl.CreatedAt.Before(before) {
		t.Errorf("CreatedAt %v is before test start %v", cl.CreatedAt, before)
	}
}
