package api

import (
	"net/http"
	"testing"
	"time"

	"github.com/noodlebit/machmqtt-dashboard/internal/collector"
	"github.com/noodlebit/machmqtt-dashboard/internal/config"
)

func TestBridgeDisplayNameAndMatch(t *testing.T) {
	cases := []struct {
		name    string
		b       collector.MQTTBridgeInstance
		want    string
		matches []string // queries that must match
		misses  []string // queries that must not match
	}{
		{
			name:    "configured name wins",
			b:       collector.MQTTBridgeInstance{ConfiguredName: "edge-1", IP: "10.0.0.5", AdminURL: "http://10.0.0.5:8081", Status: &collector.MQTTBridgeStatus{Name: "broker"}},
			want:    "edge-1",
			matches: []string{"edge-1", "10.0.0.5", "http://10.0.0.5:8081"},
			misses:  []string{"broker", "nope"},
		},
		{
			name:    "status name when no configured name",
			b:       collector.MQTTBridgeInstance{IP: "10.0.0.6", Status: &collector.MQTTBridgeStatus{Name: "broker-2"}},
			want:    "broker-2",
			matches: []string{"broker-2", "10.0.0.6"},
			misses:  []string{""},
		},
		{
			name:    "push-discovered bridge with no admin url still matches by name",
			b:       collector.MQTTBridgeInstance{ConfiguredName: "edge-broker-1", Status: &collector.MQTTBridgeStatus{Name: "edge-broker-1"}},
			want:    "edge-broker-1",
			matches: []string{"edge-broker-1"},
			misses:  []string{"http://example:8081"},
		},
		{
			name:    "ip fallback when nothing else set",
			b:       collector.MQTTBridgeInstance{IP: "10.0.0.7"},
			want:    "mqtt@10.0.0.7",
			matches: []string{"mqtt@10.0.0.7", "10.0.0.7"},
			misses:  []string{"mqtt@other"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := bridgeDisplayName(tc.b); got != tc.want {
				t.Errorf("bridgeDisplayName = %q, want %q", got, tc.want)
			}
			for _, q := range tc.matches {
				if !bridgeMatchesName(tc.b, q) {
					t.Errorf("bridgeMatchesName(%q) = false, want true", q)
				}
			}
			for _, q := range tc.misses {
				if bridgeMatchesName(tc.b, q) {
					t.Errorf("bridgeMatchesName(%q) = true, want false", q)
				}
			}
		})
	}
}

// TestMQTTPushFallback pins the headline behavior: a push-discovered bridge
// (no admin URL, but a Status snapshot from $MQTT5.metrics.>) serves the
// Metrics/Pool/NATS tabs from that snapshot, and the admin-only sub-resources
// return a clean reason (HTTP 200) instead of a bare 404 — so the detail page
// populates and never spam-toasts "failed to load".
func TestMQTTPushFallback(t *testing.T) {
	srv, _, token, id := polledServer(t, natsMockConfig{})

	push := collector.MQTTBridgeInstance{
		ConfiguredName: "push-bridge",
		Reachable:      true,
		Status: &collector.MQTTBridgeStatus{
			Name:    "push-bridge",
			Metrics: &collector.MQTTMetrics{ConnectionsActive: 7, ConsumerPendingMessages: -1},
			Pool:    &collector.MQTTPool{Size: 16},
			NATS:    &collector.MQTTNATSDiag{Connection: collector.MQTTNATSConnection{Connected: true, ServerName: "S1"}},
		},
	}
	srv.manager.SeedMQTTBridgesForTest(id, []collector.MQTTBridgeInstance{push})

	t.Run("metrics from push snapshot", func(t *testing.T) {
		w := do(t, srv, "GET", mqttPath(id, "push-bridge", "/metrics"), token, "")
		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, body=%s", w.Code, w.Body.String())
		}
		if got := decodeJSON(t, w)["connections_active"]; got != float64(7) {
			t.Errorf("connections_active = %v, want 7", got)
		}
	})

	t.Run("pool from push snapshot", func(t *testing.T) {
		w := do(t, srv, "GET", mqttPath(id, "push-bridge", "/pool"), token, "")
		if w.Code != http.StatusOK {
			t.Fatalf("status = %d", w.Code)
		}
		if got := decodeJSON(t, w)["size"]; got != float64(16) {
			t.Errorf("size = %v, want 16", got)
		}
	})

	t.Run("nats diag from push snapshot", func(t *testing.T) {
		w := do(t, srv, "GET", mqttPath(id, "push-bridge", "/diag"), token, "")
		if w.Code != http.StatusOK {
			t.Fatalf("status = %d", w.Code)
		}
		if _, ok := decodeJSON(t, w)["connection"]; !ok {
			t.Errorf("nats diag missing connection: %s", w.Body.String())
		}
	})

	// Admin-only sub-resources: clean reason (200 + available:false), not 404.
	for _, ep := range []string{"/license", "/diag/config", "/cluster"} {
		t.Run("reason for "+ep, func(t *testing.T) {
			w := do(t, srv, "GET", mqttPath(id, "push-bridge", ep), token, "")
			if w.Code != http.StatusOK {
				t.Fatalf("status = %d", w.Code)
			}
			m := decodeJSON(t, w)
			if m["available"] != false {
				t.Errorf("%s available = %v, want false", ep, m["available"])
			}
			if s, _ := m["reason"].(string); s == "" {
				t.Errorf("%s missing reason string: %s", ep, w.Body.String())
			}
		})
	}

	t.Run("connz returns push reason in detail", func(t *testing.T) {
		w := do(t, srv, "GET", mqttPath(id, "push-bridge", "/connz"), token, "")
		if w.Code != http.StatusOK {
			t.Fatalf("status = %d", w.Code)
		}
		if d, _ := decodeJSON(t, w)["detail"].(string); d == "" {
			t.Errorf("connz missing detail: %s", w.Body.String())
		}
	})

	t.Run("admin action is unavailable without an admin URL", func(t *testing.T) {
		w := do(t, srv, "POST", mqttPath(id, "push-bridge", "/admin/drain"), token, "")
		if w.Code != http.StatusServiceUnavailable {
			t.Errorf("status = %d, want 503", w.Code)
		}
	})
}

func TestFindBridgeResolvesDiscoveredByIP(t *testing.T) {
	bSrv := bridgeMock(t, okBridgeHandler(bridgeConfig{}))
	port := bridgePort(t, bSrv.URL)
	// Discover a bridge but configure none, so resolution must go through the
	// discovered-bridge path in findBridge (matched by IP).
	srv, _, token, id := polledServer(t, natsMockConfig{bridgeConn: true}, withDiscovery(port))
	waitForDiscovery(t, srv, id)

	w := do(t, srv, "GET", mqttPath(id, "127.0.0.1", "/connz"), token, "")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", w.Code, w.Body.String())
	}
}

func TestHandleMQTTBridgesSort(t *testing.T) {
	// A discovered bridge has no configured name, so the bridge-list sort falls
	// back to its IP; two configured (dead) bridges sort by name. Mixing the two
	// exercises both IP-fallback arms of the sort comparator.
	bSrv := bridgeMock(t, okBridgeHandler(bridgeConfig{}))
	port := bridgePort(t, bSrv.URL)
	srv, _, token, id := polledServer(t,
		natsMockConfig{bridgeConn: true},
		withDiscovery(port),
		withBridges(
			config.MQTTBridge{Name: "bridge-a", URL: deadBridgeURL(t)},
			config.MQTTBridge{Name: "bridge-z", URL: deadBridgeURL(t)},
		))
	waitForDiscovery(t, srv, id)

	w := do(t, srv, "GET", "/api/environments/"+id+"/mqtt/bridges", token, "")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	bridges := decodeJSON(t, w)["bridges"].([]any)
	if len(bridges) != 3 {
		t.Fatalf("bridges = %d, want 3 (1 discovered + 2 configured)", len(bridges))
	}
	// The discovered bridge (no name) sorts by IP "127.0.0.1", ahead of the named
	// "bridge-a"/"bridge-z".
	names := make([]string, len(bridges))
	for i, b := range bridges {
		bm := b.(map[string]any)
		if n, _ := bm["configured_name"].(string); n != "" {
			names[i] = n
		} else {
			names[i], _ = bm["ip"].(string)
		}
	}
	if names[0] != "127.0.0.1" || names[1] != "bridge-a" || names[2] != "bridge-z" {
		t.Errorf("sort order = %v, want [127.0.0.1 bridge-a bridge-z]", names)
	}
}

func TestMQTTHandlerUnknownEnv(t *testing.T) {
	// An MQTT proxy request for an unknown env exercises mqttBridges' nil-config
	// branch and 404s.
	srv, token, _ := bridgeServerWith(t, bridgeConfig{})
	w := do(t, srv, "GET", "/api/environments/missing/mqtt/b1/connz", token, "")
	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", w.Code)
	}
}

func TestBridgeStatusCacheGet(t *testing.T) {
	c := newBridgeStatusCache(time.Minute)
	calls := 0
	fetch := func() *collector.MQTTBridgeStatus {
		calls++
		return &collector.MQTTBridgeStatus{Name: "x"}
	}
	s1 := c.get("key", fetch)
	s2 := c.get("key", fetch) // served from cache, fetch not called again
	if calls != 1 {
		t.Errorf("fetch called %d times, want 1 (second is a cache hit)", calls)
	}
	if s1 != s2 {
		t.Errorf("cache returned a different status on hit")
	}
}

func TestBridgeStatusCacheExpiry(t *testing.T) {
	c := newBridgeStatusCache(time.Nanosecond) // expires immediately
	calls := 0
	fetch := func() *collector.MQTTBridgeStatus {
		calls++
		return &collector.MQTTBridgeStatus{}
	}
	c.get("k", fetch)
	time.Sleep(time.Millisecond)
	c.get("k", fetch) // entry expired → refetched
	if calls != 2 {
		t.Errorf("fetch called %d times, want 2 (entry expired between calls)", calls)
	}
}

// TestMQTTBridgeNotFoundAcrossHandlers pins the bridge-not-found 404 for the
// handlers whose not-found arm wasn't otherwise exercised.
func TestMQTTBridgeNotFoundAcrossHandlers(t *testing.T) {
	srv, token, id := bridgeServerWith(t, bridgeConfig{})
	paths := []string{
		"/connz/c1",                     // handleMQTTClient
		"/cluster",                      // handleMQTTCluster
		"/cluster/inspect?client_id=c1", // handleMQTTClusterInspect
	}
	for _, p := range paths {
		w := do(t, srv, "GET", mqttPath(id, "ghost", p), token, "")
		if w.Code != http.StatusNotFound {
			t.Errorf("%s with unknown bridge = %d, want 404", p, w.Code)
		}
	}
}
