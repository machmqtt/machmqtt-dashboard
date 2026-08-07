package collector

import (
	"strings"
	"testing"
	"time"
)

func TestBuildTopologyRoutes(t *testing.T) {
	snap := &Snapshot{
		Varz: map[string]*Varz{
			"A": {ServerName: "nats-1", Connections: 10, Cluster: ClusterOptsVarz{Name: "dc1"}},
			"B": {ServerName: "nats-2", Connections: 5, Cluster: ClusterOptsVarz{Name: "dc1"}},
		},
		Routez: map[string]*Routez{
			"A": {Routes: []RouteInfo{{RemoteID: "B", RemoteName: "nats-2"}}},
			"B": {Routes: []RouteInfo{{RemoteID: "A", RemoteName: "nats-1"}}},
		},
		Health: map[string]*HealthStatus{
			"A": {Status: "ok"},
			"B": {Status: "ok"},
		},
		Rates: map[string]*ServerRates{},
	}

	g := buildTopology(snap, nil)
	if len(g.Nodes) != 2 {
		t.Errorf("nodes = %d, want 2", len(g.Nodes))
	}
	// Routes are bidirectional, should be deduplicated to 1.
	if len(g.Links) != 1 {
		t.Errorf("links = %d, want 1 (deduplicated)", len(g.Links))
	}
	if g.Links[0].Type != "route" {
		t.Errorf("link type = %q, want route", g.Links[0].Type)
	}
}

func TestBuildTopologyHealthNotOK(t *testing.T) {
	snap := &Snapshot{
		Varz: map[string]*Varz{
			"A": {ServerName: "nats-1"},
		},
		Health: map[string]*HealthStatus{
			"A": {Status: "critical"},
		},
		Routez: map[string]*Routez{},
		Rates:  map[string]*ServerRates{},
	}
	g := buildTopology(snap, nil)
	if len(g.Nodes) != 1 {
		t.Fatalf("nodes = %d, want 1", len(g.Nodes))
	}
	if g.Nodes[0].Healthy {
		t.Error("expected Healthy=false for critical health status")
	}
}

func TestBuildTopologyLeafs(t *testing.T) {
	snap := &Snapshot{
		Varz: map[string]*Varz{
			"A": {ServerName: "hub"},
		},
		Leafz: map[string]*Leafz{
			"A": {Leafs: []LeafInfo{
				{Name: "leaf-1", InMsgs: 100, OutMsgs: 200},
			}},
		},
		Routez: map[string]*Routez{},
		Health: map[string]*HealthStatus{},
		Rates:  map[string]*ServerRates{},
	}
	g := buildTopology(snap, nil)
	// hub + leaf node
	if len(g.Nodes) != 2 {
		t.Errorf("nodes = %d, want 2 (hub + leaf)", len(g.Nodes))
	}
	if len(g.Links) != 1 {
		t.Errorf("links = %d, want 1", len(g.Links))
	}
	if g.Links[0].Type != "leaf" {
		t.Errorf("link type = %q, want leaf", g.Links[0].Type)
	}
}

func TestBuildTopologyLeafMatchesKnownServer(t *testing.T) {
	// When a leaf's Name matches a known server's ServerName, use that server's ID
	// rather than creating a new "leaf:" node.
	snap := &Snapshot{
		Varz: map[string]*Varz{
			"A": {ServerName: "hub"},
			"B": {ServerName: "spoke"},
		},
		Leafz: map[string]*Leafz{
			"A": {Leafs: []LeafInfo{{Name: "spoke"}}},
		},
		Routez: map[string]*Routez{},
		Health: map[string]*HealthStatus{},
		Rates:  map[string]*ServerRates{},
	}
	g := buildTopology(snap, nil)
	// Both A and B are server nodes; no extra "leaf:spoke" node should be created.
	if len(g.Nodes) != 2 {
		t.Errorf("nodes = %d, want 2 (no duplicate leaf node)", len(g.Nodes))
	}
}

func TestBuildTopologyLeafEmptyName(t *testing.T) {
	// Leaf with empty Name falls back to IP.
	snap := &Snapshot{
		Varz: map[string]*Varz{
			"A": {ServerName: "hub"},
		},
		Leafz: map[string]*Leafz{
			"A": {Leafs: []LeafInfo{{Name: "", IP: "10.0.0.2"}}},
		},
		Routez: map[string]*Routez{},
		Health: map[string]*HealthStatus{},
		Rates:  map[string]*ServerRates{},
	}
	g := buildTopology(snap, nil)
	found := false
	for _, n := range g.Nodes {
		if n.ID == "leaf:10.0.0.2" {
			found = true
		}
	}
	if !found {
		t.Error("expected leaf node with ID leaf:10.0.0.2")
	}
}

func TestBuildTopologyMQTTBridgesFromConnz(t *testing.T) {
	snap := &Snapshot{
		Varz: map[string]*Varz{
			"srv-1": {ServerName: "nats-1"},
		},
		Connz: map[string]*Connz{
			"srv-1": {Conns: []ConnInfo{
				{Name: "machmqtt-bridge", IP: "192.168.1.50", InMsgs: 500, OutMsgs: 250},
				{Name: "machmqtt-pool-0", IP: "192.168.1.50", InMsgs: 100, OutMsgs: 50},
			}},
		},
		Routez: map[string]*Routez{},
		Health: map[string]*HealthStatus{},
		Rates:  map[string]*ServerRates{},
	}
	g := buildTopology(snap, nil)
	// Server node + mqtt node
	if len(g.Nodes) != 2 {
		t.Errorf("nodes = %d, want 2 (server + mqtt)", len(g.Nodes))
	}
	mqttFound := false
	for _, n := range g.Nodes {
		if n.Type == "mqtt" {
			mqttFound = true
			if n.Connections != 2 {
				t.Errorf("mqtt node Connections = %d, want 2", n.Connections)
			}
		}
	}
	if !mqttFound {
		t.Error("expected mqtt node in topology")
	}
	if len(g.Links) != 1 || g.Links[0].Type != "mqtt" {
		t.Errorf("expected 1 mqtt link, got links=%v", g.Links)
	}
}

func TestBuildTopologyMQTTLoopbackResolution(t *testing.T) {
	snap := &Snapshot{
		Varz: map[string]*Varz{
			"srv-1": {ServerName: "nats-1", Host: "10.0.0.5"},
		},
		Connz: map[string]*Connz{
			"srv-1": {Conns: []ConnInfo{
				{Name: "machmqtt-bridge", IP: "127.0.0.1"},
			}},
		},
		ServerURLs: map[string]string{"srv-1": "10.0.0.5"},
		Routez:     map[string]*Routez{},
		Health:     map[string]*HealthStatus{},
		Rates:      map[string]*ServerRates{},
	}
	g := buildTopology(snap, nil)
	var found bool
	for _, n := range g.Nodes {
		if n.Type == "mqtt" {
			found = true
			if strings.Contains(n.ID, "127.0.0.1") {
				t.Errorf("loopback 127.0.0.1 should have resolved to 10.0.0.5; node ID = %q", n.ID)
			}
			if !strings.HasSuffix(n.ID, ":10.0.0.5") {
				t.Errorf("mqtt node ID should end with the resolved host :10.0.0.5; got %q", n.ID)
			}
		}
	}
	if !found {
		t.Fatal("no mqtt node built")
	}
}

func TestBuildTopologyLoopbackFallbackToVarz(t *testing.T) {
	snap := &Snapshot{
		Varz: map[string]*Varz{
			"srv-1": {ServerName: "nats-1", Host: "10.0.0.9"},
		},
		Connz: map[string]*Connz{
			"srv-1": {Conns: []ConnInfo{
				{Name: "machmqtt-bridge", IP: "::1"}, // IPv6 loopback
			}},
		},
		// No ServerURLs — fallback to Varz.Host.
		Routez: map[string]*Routez{},
		Health: map[string]*HealthStatus{},
		Rates:  map[string]*ServerRates{},
	}
	g := buildTopology(snap, nil)
	var found bool
	for _, n := range g.Nodes {
		if n.Type == "mqtt" {
			found = true
			if strings.Contains(n.ID, "::1") {
				t.Errorf("IPv6 loopback not resolved: node ID = %q", n.ID)
			}
			if !strings.HasSuffix(n.ID, ":10.0.0.9") {
				t.Errorf("mqtt node ID should end with the resolved host :10.0.0.9; got %q", n.ID)
			}
		}
	}
	if !found {
		t.Fatal("no mqtt node built")
	}
}

func TestBuildTopologyWithRates(t *testing.T) {
	now := time.Now()
	prev := &Snapshot{
		Timestamp: now.Add(-10 * time.Second),
		Varz: map[string]*Varz{
			"A": {ServerName: "nats-1"},
		},
		Routez: map[string]*Routez{
			"A": {Routes: []RouteInfo{{RemoteID: "B", RemoteName: "nats-2", InMsgs: 100, OutMsgs: 200}}},
		},
		Leafz: map[string]*Leafz{
			"A": {Leafs: []LeafInfo{{Name: "leaf-1", InMsgs: 50, OutMsgs: 100}}},
		},
	}
	snap := &Snapshot{
		Timestamp: now,
		Varz: map[string]*Varz{
			"A": {ServerName: "nats-1"},
		},
		Routez: map[string]*Routez{
			"A": {Routes: []RouteInfo{{RemoteID: "B", RemoteName: "nats-2", InMsgs: 200, OutMsgs: 400}}},
		},
		Leafz: map[string]*Leafz{
			"A": {Leafs: []LeafInfo{{Name: "leaf-1", InMsgs: 100, OutMsgs: 250}}},
		},
		Health: map[string]*HealthStatus{},
		Rates:  map[string]*ServerRates{},
	}
	g := buildTopology(snap, prev)
	if g == nil {
		t.Fatal("expected non-nil topology")
	}
	// Should have at least route and leaf links.
	if len(g.Links) == 0 {
		t.Error("expected links when prev snapshot is provided")
	}
}

func TestBuildTopologyGatewayWithConnection(t *testing.T) {
	conn := &ConnInfo{IP: "10.0.0.3"}
	snap := &Snapshot{
		Varz: map[string]*Varz{
			"A": {ServerName: "hub"},
		},
		Gatewayz: map[string]*Gatewayz{
			"A": {
				OutboundGateways: map[string]*RemoteGatewayz{
					"dc2": {IsConfigured: true, Connection: conn},
				},
			},
		},
		Health: map[string]*HealthStatus{"A": {Status: "ok"}},
		Rates:  map[string]*ServerRates{},
		Routez: map[string]*Routez{},
	}
	g := buildTopology(snap, nil)
	if len(g.Links) != 1 || g.Links[0].Type != "gateway" {
		t.Errorf("expected 1 gateway link, got %v", g.Links)
	}
}

func TestBuildTopologyMQTTBridgesWithPrevRates(t *testing.T) {
	prev := &Snapshot{
		Timestamp: time.Now().Add(-10 * time.Second),
		Connz: map[string]*Connz{
			"srv-1": {Conns: []ConnInfo{
				{Name: "machmqtt-bridge", IP: "192.168.1.10", InMsgs: 100, OutMsgs: 50},
			}},
		},
	}
	snap := &Snapshot{
		Timestamp: time.Now(),
		Varz: map[string]*Varz{
			"srv-1": {ServerName: "nats-1"},
		},
		Connz: map[string]*Connz{
			"srv-1": {Conns: []ConnInfo{
				{Name: "machmqtt-bridge", IP: "192.168.1.10", InMsgs: 200, OutMsgs: 100},
			}},
		},
		Routez: map[string]*Routez{},
		Health: map[string]*HealthStatus{},
		Rates:  map[string]*ServerRates{},
	}
	g := buildTopology(snap, prev)
	for _, n := range g.Nodes {
		if n.Type == "mqtt" {
			if n.InMsgsRate <= 0 {
				t.Errorf("expected positive InMsgsRate for mqtt node, got %f", n.InMsgsRate)
			}
		}
	}
}

func TestBuildTopologyGateways(t *testing.T) {
	snap := &Snapshot{
		Varz: map[string]*Varz{
			"A": {ServerName: "nats-1"},
		},
		Gatewayz: map[string]*Gatewayz{
			"A": {
				OutboundGateways: map[string]*RemoteGatewayz{
					"dc2": {IsConfigured: true},
				},
			},
		},
		Health: map[string]*HealthStatus{"A": {Status: "ok"}},
		Rates:  map[string]*ServerRates{},
		Routez: map[string]*Routez{},
	}

	g := buildTopology(snap, nil)
	if len(g.Nodes) != 2 {
		t.Errorf("nodes = %d, want 2 (server + gateway)", len(g.Nodes))
	}
	if len(g.Links) != 1 {
		t.Errorf("links = %d, want 1", len(g.Links))
	}
}

func TestBuildTopologyLeafMQTTAndRateBranches(t *testing.T) {
	now := time.Now()
	prev := &Snapshot{
		Timestamp:  now.Add(-2 * time.Second),
		Routez:     map[string]*Routez{"A": {Routes: []RouteInfo{{RemoteID: "B", InMsgs: 1, OutMsgs: 2}}}},
		Leafz:      map[string]*Leafz{"A": {Leafs: []LeafInfo{{Name: "leaf-one", InMsgs: 2, OutMsgs: 3}}}},
		Connz:      map[string]*Connz{"A": {Conns: []ConnInfo{{Name: "machmqtt-bridge", IP: "127.0.0.1", InMsgs: 3, OutMsgs: 4}}}},
		Varz:       map[string]*Varz{"A": {Host: "host-a"}},
		ServerURLs: map[string]string{"A": "bridge-host"},
	}
	snap := &Snapshot{
		Timestamp: now,
		Varz: map[string]*Varz{
			"A": {ServerName: "nats-a", Host: "host-a", Connections: 2},
			"B": {ServerName: "leaf-known"},
		},
		Health:   map[string]*HealthStatus{"A": {Status: "error"}},
		Rates:    map[string]*ServerRates{"A": {InMsgsRate: 2, OutMsgsRate: 3}},
		Routez:   map[string]*Routez{"A": {Routes: []RouteInfo{{RemoteID: "B", RemoteName: "nats-b", InMsgs: 5, OutMsgs: 8}}}},
		Gatewayz: map[string]*Gatewayz{"A": {OutboundGateways: map[string]*RemoteGatewayz{"west": {Connection: &ConnInfo{Cid: 1}}}}},
		Leafz: map[string]*Leafz{"A": {Leafs: []LeafInfo{
			{Name: "leaf-one", InMsgs: 6, OutMsgs: 9},
			{Name: "leaf-known", InMsgs: 1, OutMsgs: 1},
			{IP: "10.0.0.2"},
		}}},
		Connz: map[string]*Connz{"A": {Conns: []ConnInfo{
			{Name: "machmqtt-bridge", IP: "127.0.0.1", NumSubs: 2, InMsgs: 9, OutMsgs: 12},
			{Name: "machmqtt-pool-2", IP: "127.0.0.1", NumSubs: 1, InMsgs: 1, OutMsgs: 1},
			{Name: "ordinary", IP: "10.0.0.3"},
		}}},
		ServerURLs: map[string]string{"A": "bridge-host"},
	}
	g := buildTopology(snap, prev)
	if len(g.Nodes) < 6 || len(g.Links) < 5 {
		t.Fatalf("topology=%+v", g)
	}
	var foundMQTT, foundLeafRate, foundRouteRate bool
	for _, link := range g.Links {
		if link.Type == "mqtt" && link.InMsgsRate > 0 {
			foundMQTT = true
		}
		if link.Type == "leaf" && link.Target == "leaf:leaf-one" && link.InMsgsRate == 2 {
			foundLeafRate = true
		}
		if link.Type == "route" && link.InMsgsRate == 2 {
			foundRouteRate = true
		}
	}
	if !foundMQTT || !foundLeafRate || !foundRouteRate {
		t.Fatalf("missing rate links: %+v", g.Links)
	}
	if routeLinkKey("b", "a") != "a<->b" || routeLinkKey("a", "b") != "a<->b" {
		t.Fatal("route key")
	}
}
