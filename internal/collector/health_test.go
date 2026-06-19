package collector

import (
	"testing"
	"time"

	"github.com/noodlebit/machmqtt-dashboard/internal/config"
)

func newTestCollector(t *testing.T, name string) *Collector {
	t.Helper()
	f, err := NewFetcher(nil)
	if err != nil {
		t.Fatalf("NewFetcher: %v", err)
	}
	return newCollector(config.Environment{Name: name}, f, 30*time.Second, testLog(), nil)
}

func TestCollectorHealthBaseline(t *testing.T) {
	c := newTestCollector(t, "prod")
	h := c.Health()

	if h.Name != "prod" {
		t.Errorf("Name = %q, want prod", h.Name)
	}
	if h.CollectionMode != "http" {
		t.Errorf("CollectionMode = %q, want http", h.CollectionMode)
	}
	if h.NATSPushConfigured {
		t.Error("NATSPushConfigured = true, want false for an HTTP-only collector")
	}
	if h.Stale {
		t.Error("a never-polled collector (zero timestamp) should not be marked stale")
	}
	if h.Degraded() {
		t.Error("a fresh HTTP collector should not be degraded")
	}
}

func TestCollectorHealthStale(t *testing.T) {
	c := newTestCollector(t, "prod")
	// Simulate a poll that happened well beyond 3× the interval ago.
	c.snapshot.Timestamp = time.Now().Add(-5 * time.Minute)
	c.snapshot.Varz["s1"] = &Varz{}
	c.snapshot.Health["s1"] = &HealthStatus{Status: "ok"}
	c.snapshot.Varz["s2"] = &Varz{}
	c.snapshot.Health["s2"] = &HealthStatus{Status: "error"}

	h := c.Health()
	if !h.Stale {
		t.Errorf("LastPollAge=%.0fs should be stale (interval 30s)", h.LastPollAgeSeconds)
	}
	if !h.Degraded() {
		t.Error("stale collector should be degraded")
	}
	if h.Servers != 2 {
		t.Errorf("Servers = %d, want 2", h.Servers)
	}
	if h.HealthyServers != 1 {
		t.Errorf("HealthyServers = %d, want 1", h.HealthyServers)
	}
}

func TestCollectorHealthSysFallback(t *testing.T) {
	c := newTestCollector(t, "prod")
	c.sys = newSYSCollector() // $SYS configured but never connected
	c.snapshot.Timestamp = time.Now()

	// Healthy $SYS path.
	if got := c.Health().CollectionMode; got != "sys" {
		t.Errorf("CollectionMode = %q, want sys", got)
	}
	if !c.Health().NATSPushConfigured {
		t.Error("NATSPushConfigured should be true when $SYS is configured")
	}
	if c.Health().NATSPushConnected {
		t.Error("NATSPushConnected should be false when $SYS never connected")
	}

	// Engage the HTTP fallback.
	c.sysFellBack.Store(true)
	h := c.Health()
	if h.CollectionMode != "sys-fallback" {
		t.Errorf("CollectionMode = %q, want sys-fallback", h.CollectionMode)
	}
	if !h.SysFallbackEngaged || !h.Degraded() {
		t.Error("fallback engaged should be degraded")
	}
}

func TestManagerHealthReport(t *testing.T) {
	s := testStore(t)
	addClusterToStore(t, s, "prod", "http://nats:8222")
	addClusterToStore(t, s, "staging", "http://staging:8222")

	m, err := NewManager(testCfg(), nil, testLog(), s)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}

	rep := m.HealthReport()
	if len(rep) != 2 {
		t.Fatalf("HealthReport len = %d, want 2", len(rep))
	}
	for _, h := range rep {
		if h.ID == "" {
			t.Error("HealthReport entry missing ID")
		}
		if h.Name == "" {
			t.Error("HealthReport entry missing Name")
		}
	}
}
