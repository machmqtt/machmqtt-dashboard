package collector

import (
	"testing"
	"time"
)

// managerWithCollector builds a Manager holding a single collector keyed by a
// known cluster ID, returning both so tests can populate the snapshot/bridges
// directly (same-package access) and then exercise Manager methods.
func managerWithCollector(t *testing.T) (*Manager, *Collector, string) {
	t.Helper()
	s := testStore(t)
	cl := addClusterToStore(t, s, "metrics-env", "http://localhost:8222")
	m, err := NewManager(testCfg(), nil, testLog(), s)
	if err != nil {
		t.Fatal(err)
	}
	c := m.collector(cl.ID)
	if c == nil {
		t.Fatalf("collector for %q not found", cl.ID)
	}
	return m, c, cl.ID
}

func TestBuildMetricSampleNilOverview(t *testing.T) {
	m, _, id := managerWithCollector(t)
	if got := m.BuildMetricSample(id, time.Now(), nil); got != nil {
		t.Errorf("BuildMetricSample(nil overview) = %v, want nil", got)
	}
}

func TestBuildMetricSampleAggregatesOverviewAndServers(t *testing.T) {
	m, c, id := managerWithCollector(t)
	ts := time.Unix(1_700_000_000, 0)

	c.snapMu.Lock()
	c.snapshot.Varz = map[string]*Varz{
		"srv-1": {
			Connections:   7,
			InMsgs:        100,
			OutMsgs:       200,
			InBytes:       1000,
			OutBytes:      2000,
			CPU:           12.5,
			Mem:           4096,
			Subscriptions: 9,
			SlowConsumers: 1,
			Routes:        2,
			Leafs:         3,
		},
	}
	c.snapshot.Health = map[string]*HealthStatus{"srv-1": {Status: "error"}}
	c.snapshot.Rates = map[string]*ServerRates{"srv-1": {
		InMsgsRate: 1.5, OutMsgsRate: 2.5, InBytesRate: 10, OutBytesRate: 20,
	}}
	c.snapMu.Unlock()

	overview := &Overview{
		ServerCount:     1,
		HealthyCount:    0,
		ConnectionCount: 7,
		InMsgsRate:      1.5,
		OutMsgsRate:     2.5,
		InBytesRate:     10,
		OutBytesRate:    20,
		Subscriptions:   9,
	}

	sample := m.BuildMetricSample(id, ts, overview)
	if sample == nil {
		t.Fatal("sample is nil")
	}
	if !sample.Timestamp.Equal(ts) {
		t.Errorf("Timestamp = %v, want %v", sample.Timestamp, ts)
	}
	if sample.Env != id {
		t.Errorf("Env = %q, want %q", sample.Env, id)
	}
	if sample.ConnectionCount != 7 || sample.Subscriptions != 9 {
		t.Errorf("aggregate fields wrong: conns=%d subs=%d", sample.ConnectionCount, sample.Subscriptions)
	}
	if len(sample.Servers) != 1 {
		t.Fatalf("Servers len = %d, want 1", len(sample.Servers))
	}
	srv := sample.Servers[0]
	if srv.ServerID != "srv-1" {
		t.Errorf("ServerID = %q, want srv-1", srv.ServerID)
	}
	if srv.Connections != 7 || srv.InMsgs != 100 || srv.OutMsgs != 200 {
		t.Errorf("server counters wrong: %+v", srv)
	}
	if srv.Healthy {
		t.Error("server with Health status=error should be Healthy=false")
	}
	if srv.InMsgsRate != 1.5 || srv.OutBytesRate != 20 {
		t.Errorf("server rates not copied: %+v", srv)
	}
	if srv.Routes != 2 || srv.LeafNodes != 3 || srv.SlowConsumers != 1 {
		t.Errorf("server topo counters wrong: %+v", srv)
	}
}

func TestBuildMetricSampleHealthyServerDefault(t *testing.T) {
	// No Health entry for the server → Healthy defaults to true.
	m, c, id := managerWithCollector(t)
	c.snapMu.Lock()
	c.snapshot.Varz = map[string]*Varz{"s": {Connections: 1}}
	c.snapMu.Unlock()

	sample := m.BuildMetricSample(id, time.Now(), &Overview{ServerCount: 1})
	if len(sample.Servers) != 1 || !sample.Servers[0].Healthy {
		t.Fatalf("expected one healthy server, got %+v", sample.Servers)
	}
}

func TestBuildMetricSampleMQTTBridges(t *testing.T) {
	m, c, id := managerWithCollector(t)

	pending := int64(42)
	c.mqttMu.Lock()
	c.mqttBridges = []MQTTBridgeInstance{
		{
			ConfiguredName: "bridge-a",
			InMsgsRate:     5, OutMsgsRate: 6, InBytesRate: 7, OutBytesRate: 8,
			Status: &MQTTBridgeStatus{
				Metrics: &MQTTMetrics{
					ConnectionsActive:       3,
					MsgsRecvQoS0:            10,
					MsgsSentQoS1:            11,
					ConsumerPendingMessages: pending, // >= 0 → pointer set
					RetainedMessages:        99,
				},
			},
		},
		{
			// No ConfiguredName → BridgeID falls back to IP. No Status → metrics
			// block skipped.
			IP: "10.0.0.5",
		},
		{
			// ConsumerPendingMessages = -1 (JetStream absent) → pointer stays nil.
			ConfiguredName: "bridge-c",
			Status: &MQTTBridgeStatus{
				Metrics: &MQTTMetrics{ConsumerPendingMessages: -1},
			},
		},
	}
	c.mqttMu.Unlock()

	sample := m.BuildMetricSample(id, time.Now(), &Overview{ServerCount: 0})
	if sample == nil {
		t.Fatal("sample is nil")
	}
	if len(sample.MQTTBridges) != 3 {
		t.Fatalf("MQTTBridges len = %d, want 3", len(sample.MQTTBridges))
	}

	a := sample.MQTTBridges[0]
	if a.BridgeID != "bridge-a" {
		t.Errorf("bridge[0].BridgeID = %q, want bridge-a", a.BridgeID)
	}
	if a.InMsgsRate != 5 || a.OutBytesRate != 8 {
		t.Errorf("bridge[0] rates wrong: %+v", a)
	}
	if a.ConnectionsActive != 3 || a.RetainedMessages != 99 {
		t.Errorf("bridge[0] metrics not copied: %+v", a)
	}
	if a.ConsumerPendingMessages == nil || *a.ConsumerPendingMessages != 42 {
		t.Errorf("bridge[0] ConsumerPendingMessages = %v, want *42", a.ConsumerPendingMessages)
	}

	b := sample.MQTTBridges[1]
	if b.BridgeID != "10.0.0.5" {
		t.Errorf("bridge[1].BridgeID = %q, want IP fallback 10.0.0.5", b.BridgeID)
	}

	cc := sample.MQTTBridges[2]
	if cc.ConsumerPendingMessages != nil {
		t.Errorf("bridge[2] ConsumerPendingMessages = %v, want nil (-1 sentinel)", cc.ConsumerPendingMessages)
	}
}
