package collector

import (
	"testing"
	"time"
)

// TestBuildTopologyNodeRatesFromRatesMap verifies that when a server has an
// entry in snap.Rates, the resulting topology node carries those msg rates.
// Covers the snap.Rates[id] branch in buildTopology (topology.go L62-65).
func TestBuildTopologyNodeRatesFromRatesMap(t *testing.T) {
	snap := &Snapshot{
		Varz: map[string]*Varz{
			"A": {ServerName: "nats-1"},
		},
		Rates: map[string]*ServerRates{
			"A": {InMsgsRate: 12.5, OutMsgsRate: 34.5},
		},
		Routez: map[string]*Routez{},
		Health: map[string]*HealthStatus{},
	}
	g := buildTopology(snap, nil)
	if len(g.Nodes) != 1 {
		t.Fatalf("nodes = %d, want 1", len(g.Nodes))
	}
	if g.Nodes[0].InMsgsRate != 12.5 {
		t.Errorf("node InMsgsRate = %f, want 12.5", g.Nodes[0].InMsgsRate)
	}
	if g.Nodes[0].OutMsgsRate != 34.5 {
		t.Errorf("node OutMsgsRate = %f, want 34.5", g.Nodes[0].OutMsgsRate)
	}
}

// TestBuildTopologyRouteRatePrevServerMissing verifies that when the previous
// snapshot has no Routez entry for the source server, the route's prev counters
// resolve to 0 and the link rate is computed against zero (cur/dt).
// Covers prevRouteMsg's "server not in prev.Routez" branch (topology.go L87-89).
func TestBuildTopologyRouteRatePrevServerMissing(t *testing.T) {
	now := time.Now()
	prev := &Snapshot{
		Timestamp: now.Add(-10 * time.Second),
		// prev has NO Routez for "A" at all.
		Routez: map[string]*Routez{},
	}
	snap := &Snapshot{
		Timestamp: now,
		Varz: map[string]*Varz{
			"A": {ServerName: "nats-1"},
		},
		Routez: map[string]*Routez{
			"A": {Routes: []RouteInfo{{RemoteID: "B", RemoteName: "nats-2", InMsgs: 200, OutMsgs: 400}}},
		},
		Health: map[string]*HealthStatus{},
		Rates:  map[string]*ServerRates{},
	}
	g := buildTopology(snap, prev)
	link := findLinkByType(t, g, "route")
	// prev=0 → rate = 200/10 = 20 (would be 10 if a matched prev of 100 were found).
	if link.InMsgsRate != 20 {
		t.Errorf("route InMsgsRate = %f, want 20 (prev counters should be 0)", link.InMsgsRate)
	}
	if link.OutMsgsRate != 40 {
		t.Errorf("route OutMsgsRate = %f, want 40 (prev counters should be 0)", link.OutMsgsRate)
	}
}

// TestBuildTopologyRouteRatePrevRemoteIDMismatch verifies that when the prev
// snapshot has the source server but none of its routes match the current
// RemoteID, prev counters resolve to 0 (loop falls through to return 0,0).
// Covers prevRouteMsg's no-match fallthrough (topology.go L95).
func TestBuildTopologyRouteRatePrevRemoteIDMismatch(t *testing.T) {
	now := time.Now()
	prev := &Snapshot{
		Timestamp: now.Add(-10 * time.Second),
		Routez: map[string]*Routez{
			// Same source "A" but a DIFFERENT remote ("C"), so no match for "B".
			"A": {Routes: []RouteInfo{{RemoteID: "C", InMsgs: 9999, OutMsgs: 9999}}},
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
		Health: map[string]*HealthStatus{},
		Rates:  map[string]*ServerRates{},
	}
	g := buildTopology(snap, prev)
	link := findLinkByType(t, g, "route")
	// No matching prev route → prev=0 → 200/10 = 20.
	if link.InMsgsRate != 20 {
		t.Errorf("route InMsgsRate = %f, want 20 (no matching prev remote)", link.InMsgsRate)
	}
}

// TestBuildTopologyLeafRatePrevServerMissing verifies that when the prev
// snapshot has no Leafz entry for the source server, leaf prev counters resolve
// to 0 and the rate is cur/dt. Covers prevLeafMsg's "server not in prev.Leafz"
// branch (topology.go L103-105).
func TestBuildTopologyLeafRatePrevServerMissing(t *testing.T) {
	now := time.Now()
	prev := &Snapshot{
		Timestamp: now.Add(-10 * time.Second),
		Leafz:     map[string]*Leafz{}, // no entry for "A"
	}
	snap := &Snapshot{
		Timestamp: now,
		Varz: map[string]*Varz{
			"A": {ServerName: "hub"},
		},
		Leafz: map[string]*Leafz{
			"A": {Leafs: []LeafInfo{{Name: "leaf-1", InMsgs: 100, OutMsgs: 250}}},
		},
		Routez: map[string]*Routez{},
		Health: map[string]*HealthStatus{},
		Rates:  map[string]*ServerRates{},
	}
	g := buildTopology(snap, prev)
	link := findLinkByType(t, g, "leaf")
	if link.InMsgsRate != 10 {
		t.Errorf("leaf InMsgsRate = %f, want 10 (100/10, prev=0)", link.InMsgsRate)
	}
	if link.OutMsgsRate != 25 {
		t.Errorf("leaf OutMsgsRate = %f, want 25 (250/10, prev=0)", link.OutMsgsRate)
	}
}

// TestBuildTopologyLeafRatePrevEmptyNameFallbackNoMatch verifies the prev-leaf
// lookup: a prev leaf with an empty Name falls back to its IP for comparison,
// and when that doesn't match the current leaf name the loop returns 0,0.
// Covers prevLeafMsg's empty-name fallback (L108-110) and no-match return (L115).
func TestBuildTopologyLeafRatePrevEmptyNameFallbackNoMatch(t *testing.T) {
	now := time.Now()
	prev := &Snapshot{
		Timestamp: now.Add(-10 * time.Second),
		Leafz: map[string]*Leafz{
			// prev leaf has empty Name → falls back to IP "10.9.9.9", which does
			// NOT match the current leaf's name "leaf-1".
			"A": {Leafs: []LeafInfo{{Name: "", IP: "10.9.9.9", InMsgs: 7777, OutMsgs: 7777}}},
		},
	}
	snap := &Snapshot{
		Timestamp: now,
		Varz: map[string]*Varz{
			"A": {ServerName: "hub"},
		},
		Leafz: map[string]*Leafz{
			"A": {Leafs: []LeafInfo{{Name: "leaf-1", InMsgs: 100, OutMsgs: 250}}},
		},
		Routez: map[string]*Routez{},
		Health: map[string]*HealthStatus{},
		Rates:  map[string]*ServerRates{},
	}
	g := buildTopology(snap, prev)
	link := findLinkByType(t, g, "leaf")
	// prev fallback IP didn't match name → prev=0 → 100/10 = 10.
	if link.InMsgsRate != 10 {
		t.Errorf("leaf InMsgsRate = %f, want 10 (no matching prev leaf)", link.InMsgsRate)
	}
	if link.OutMsgsRate != 25 {
		t.Errorf("leaf OutMsgsRate = %f, want 25 (no matching prev leaf)", link.OutMsgsRate)
	}
}

// TestBuildTopologyNonBridgeConnsIgnored verifies that connz connections that
// are not MQTT bridge connections are skipped in both the current and previous
// snapshots, so no mqtt node or link is produced. Covers the isMQTTBridgeConn
// "continue" branches in the snap (L210-211) and prev (L238-239) loops.
func TestBuildTopologyNonBridgeConnsIgnored(t *testing.T) {
	prev := &Snapshot{
		Timestamp: time.Now().Add(-10 * time.Second),
		Connz: map[string]*Connz{
			"srv-1": {Conns: []ConnInfo{
				{Name: "regular-client", IP: "192.168.1.99", InMsgs: 5, OutMsgs: 5},
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
				{Name: "regular-client", IP: "192.168.1.99", InMsgs: 10, OutMsgs: 10},
			}},
		},
		Routez: map[string]*Routez{},
		Health: map[string]*HealthStatus{},
		Rates:  map[string]*ServerRates{},
	}
	g := buildTopology(snap, prev)
	for _, n := range g.Nodes {
		if n.Type == "mqtt" {
			t.Errorf("unexpected mqtt node for non-bridge conn: %+v", n)
		}
	}
	for _, l := range g.Links {
		if l.Type == "mqtt" {
			t.Errorf("unexpected mqtt link for non-bridge conn: %+v", l)
		}
	}
	// Only the single server node should exist.
	if len(g.Nodes) != 1 {
		t.Errorf("nodes = %d, want 1 (server only)", len(g.Nodes))
	}
}

// findLinkByType returns the single link of the given type, failing the test if
// zero or more than one is present.
func findLinkByType(t *testing.T, g *TopologyGraph, typ string) TopologyLink {
	t.Helper()
	var found []TopologyLink
	for _, l := range g.Links {
		if l.Type == typ {
			found = append(found, l)
		}
	}
	if len(found) != 1 {
		t.Fatalf("want exactly 1 %q link, got %d: %+v", typ, len(found), g.Links)
	}
	return found[0]
}
