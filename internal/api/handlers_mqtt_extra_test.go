package api

import (
	"net"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
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

// blackholeBridgeURL returns the URL of a TCP listener that accepts connections
// (the kernel completes the handshake from the backlog) but never answers, so a
// request to it hangs until the fetcher's own timeout — the shape of a bridge
// whose host is up but whose admin API is wedged.
func blackholeBridgeURL(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	return "http://" + ln.Addr().String()
}

// fleetBridgeList issues one fleet read and returns the decoded bridge entries.
func fleetBridgeList(t *testing.T, srv *Server, env, token string) []map[string]any {
	t.Helper()
	w := do(t, srv, "GET", "/api/environments/"+env+"/mqtt/bridges", token, "")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", w.Code, w.Body.String())
	}
	raw, ok := decodeJSON(t, w)["bridges"].([]any)
	if !ok {
		t.Fatalf("bridges missing from %s", w.Body.String())
	}
	out := make([]map[string]any, len(raw))
	for i, b := range raw {
		out[i], _ = b.(map[string]any)
	}
	return out
}

// TestHandleMQTTBridgesProbeOffRequestPath pins that a configured bridge's live
// probe never runs on the request path: a wedged bridge costs a full admin-read
// timeout, which would stall the fleet page for every viewer. The first read
// reports the bridge as pending and the probe continues in the background.
func TestHandleMQTTBridgesProbeOffRequestPath(t *testing.T) {
	srv, _, token, id := polledServer(t, natsMockConfig{},
		withBridges(config.MQTTBridge{Name: "stuck", URL: blackholeBridgeURL(t)}))
	// Disable the fleet body cache so both reads really rebuild the listing —
	// otherwise the second one would be served from the first one's bytes and
	// would prove nothing about the probe path.
	srv.bridgeJSON = newBridgeRespCache(time.Nanosecond)

	start := time.Now()
	bridges := fleetBridgeList(t, srv, id, token)
	first := time.Since(start)
	if first > time.Second {
		t.Errorf("first fleet read took %v — the probe is on the request path (one admin read alone times out after 5s)", first)
	}
	if len(bridges) != 1 {
		t.Fatalf("bridges = %d, want 1", len(bridges))
	}
	if bridges[0]["reachable"] != false {
		t.Errorf("reachable = %v, want false while the first probe is pending", bridges[0]["reachable"])
	}
	st, _ := bridges[0]["status"].(map[string]any)
	if st == nil || st["error"] != probePendingReason {
		t.Errorf("status.error = %v, want %q (pending shape on the first ever read)", st["error"], probePendingReason)
	}

	start = time.Now()
	fleetBridgeList(t, srv, id, token)
	if second := time.Since(start); second > 100*time.Millisecond {
		t.Errorf("second fleet read took %v, want < 100ms (last known result, refresh in background)", second)
	}
}

// TestHandleMQTTBridgesResponseCached pins that the fleet body is produced once
// per TTL and shared: every viewer's poll otherwise re-converted and re-marshalled
// every cached bridge.
func TestHandleMQTTBridgesResponseCached(t *testing.T) {
	srv, _, token, id := polledServer(t, natsMockConfig{})
	pushInstance := func(name string) collector.MQTTBridgeInstance {
		return collector.MQTTBridgeInstance{
			ConfiguredName: name, Reachable: true, LastSeen: time.Now(),
			Status: &collector.MQTTBridgeStatus{Name: name, Ready: true},
		}
	}
	srv.manager.SeedMQTTBridgesForTest(id, []collector.MQTTBridgeInstance{pushInstance("a")})

	if got := len(fleetBridgeList(t, srv, id, token)); got != 1 {
		t.Fatalf("bridges = %d, want 1", got)
	}

	// The underlying discovery cache changes; within the TTL the endpoint still
	// serves the body it already encoded.
	srv.manager.SeedMQTTBridgesForTest(id, []collector.MQTTBridgeInstance{pushInstance("a"), pushInstance("b")})
	if got := len(fleetBridgeList(t, srv, id, token)); got != 1 {
		t.Errorf("bridges = %d, want 1 (second read must be served from the cached body)", got)
	}

	// A cache without that entry rebuilds the listing, so the staleness above came
	// from the body cache and not from a stale manager read.
	srv.bridgeJSON = newBridgeRespCache(3 * time.Second)
	if got := len(fleetBridgeList(t, srv, id, token)); got != 2 {
		t.Errorf("bridges = %d, want 2 once the cached body is gone", got)
	}
}

// TestHandleMQTTBridgesLastSeen pins the staleness signal on the fleet response:
// a push instance reports when its last metrics publish arrived, and a configured
// bridge reports when it was last probed. Without it a viewer cannot tell live
// counters from ones frozen since a broker went quiet.
func TestHandleMQTTBridgesLastSeen(t *testing.T) {
	bSrv := bridgeMock(t, okBridgeHandler(bridgeConfig{}))
	srv, _, token, id := polledServer(t, natsMockConfig{},
		withBridges(config.MQTTBridge{Name: "probed", URL: bSrv.URL}))
	srv.bridgeJSON = newBridgeRespCache(time.Nanosecond) // read fresh listings

	pushSeen := time.Now().Add(-2 * time.Second).UTC().Truncate(time.Millisecond)
	srv.manager.SeedMQTTBridgesForTest(id, []collector.MQTTBridgeInstance{
		{
			ConfiguredName: "pushed", Reachable: true, LastSeen: pushSeen,
			Status: &collector.MQTTBridgeStatus{Name: "pushed", Ready: true},
		},
		{
			ConfiguredName: "never-seen", Reachable: true,
			Status: &collector.MQTTBridgeStatus{Name: "never-seen"},
		},
	})

	// The configured bridge's probe is asynchronous, so its time appears once the
	// first probe lands.
	var probedAt string
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		for _, b := range fleetBridgeList(t, srv, id, token) {
			if b["configured_name"] == "probed" {
				probedAt, _ = b["last_seen"].(string)
			}
		}
		if probedAt != "" {
			break
		}
		time.Sleep(25 * time.Millisecond)
	}
	if probedAt == "" {
		t.Fatal("configured bridge never reported a last_seen probe time")
	}
	ts, err := time.Parse(time.RFC3339Nano, probedAt)
	if err != nil {
		t.Fatalf("last_seen %q is not RFC3339: %v", probedAt, err)
	}
	if age := time.Since(ts); age < 0 || age > time.Minute {
		t.Errorf("probe last_seen is %v old, want a recent time", age)
	}

	for _, b := range fleetBridgeList(t, srv, id, token) {
		switch b["configured_name"] {
		case "pushed":
			got, _ := b["last_seen"].(string)
			ts, err := time.Parse(time.RFC3339Nano, got)
			if err != nil {
				t.Fatalf("push last_seen %q is not RFC3339: %v", got, err)
			}
			if !ts.Equal(pushSeen) {
				t.Errorf("push last_seen = %v, want %v (the publish receive time)", ts, pushSeen)
			}
		case "never-seen":
			// Nothing is known about this entry's age, so the field is absent
			// rather than reporting the zero time as a real timestamp.
			if v, ok := b["last_seen"]; ok {
				t.Errorf("last_seen = %v for an entry with no known time, want the field omitted", v)
			}
		}
	}
}

// TestHandleMQTTBridgesMergesProbedInstanceIdentity pins the double-count fix: a
// configured bridge whose name differs from the broker's own instance name is the
// same broker as the push-discovered instance reporting that identity, so it must
// merge into it. Listing both would show two fleet cards for one broker and
// double-count its connections and rates in the fleet totals.
func TestHandleMQTTBridgesMergesProbedInstanceIdentity(t *testing.T) {
	// identityBridge serves a bridge admin API whose /metrics reports the given
	// instance_id — the broker's own identity, independent of its configured name.
	identityBridge := func(t *testing.T, instanceID string) *httptest.Server {
		ok := okBridgeHandler(bridgeConfig{})
		return bridgeMock(t, func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/metrics" {
				_, _ = w.Write([]byte("machmqtt_instance_info{instance_id=\"" + instanceID + "\"} 1\n" +
					"machmqtt_connections_active 3\n"))
				return
			}
			ok(w, r)
		})
	}

	cases := []struct {
		name       string
		instanceID string                 // what the probed bridge reports
		push       *collector.MQTTMetrics // the push instance's metrics
	}{
		{
			// The broker's instance_id is its stable instance name, which is the
			// key the push cache lists it under.
			name:       "matches the push instance name",
			instanceID: "mqtt-a-prod",
			push:       &collector.MQTTMetrics{ConnectionsActive: 3},
		},
		{
			// The broker reports a separate ephemeral instance_id, which the push
			// snapshot carries too.
			name:       "matches the push instance id",
			instanceID: "inst-77",
			push:       &collector.MQTTMetrics{ConnectionsActive: 3, InstanceID: "inst-77"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			bSrv := identityBridge(t, tc.instanceID)
			srv, _, token, id := polledServer(t, natsMockConfig{},
				withBridges(config.MQTTBridge{Name: "mqtt-a", URL: bSrv.URL}))
			srv.bridgeJSON = newBridgeRespCache(time.Nanosecond)
			srv.manager.SeedMQTTBridgesForTest(id, []collector.MQTTBridgeInstance{{
				ConfiguredName: "mqtt-a-prod", Reachable: true, LastSeen: time.Now(),
				Status: &collector.MQTTBridgeStatus{
					Name: "mqtt-a-prod", Ready: true, Connections: 3, Metrics: tc.push,
				},
			}})

			// The probe is asynchronous, so the merge lands on a later read.
			var bridges []map[string]any
			deadline := time.Now().Add(5 * time.Second)
			for time.Now().Before(deadline) {
				bridges = fleetBridgeList(t, srv, id, token)
				if len(bridges) == 1 && bridges[0]["admin_url"] == bSrv.URL {
					break
				}
				time.Sleep(25 * time.Millisecond)
			}
			if len(bridges) != 1 {
				t.Fatalf("bridges = %d, want 1 (the configured bridge is the same broker as the push instance)", len(bridges))
			}
			b := bridges[0]
			if b["admin_url"] != bSrv.URL {
				t.Errorf("admin_url = %v, want %q (adopted from the configured bridge)", b["admin_url"], bSrv.URL)
			}
			// The broker's instance name is kept: it is the key its stored history
			// is written under, and the push snapshot stays the data source.
			if b["configured_name"] != "mqtt-a-prod" {
				t.Errorf("configured_name = %v, want mqtt-a-prod", b["configured_name"])
			}
			st, _ := b["status"].(map[string]any)
			if st == nil || st["connections"] != float64(3) {
				t.Errorf("status.connections = %v, want 3 counted once", st["connections"])
			}
		})
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

// TestBridgeStatusCacheAsyncSingleFlight pins the probe cache's contract: the
// first call returns nothing and starts one background probe, concurrent callers
// join it rather than starting their own, and later calls are served from the
// stored result without another probe.
func TestBridgeStatusCacheAsyncSingleFlight(t *testing.T) {
	c := newBridgeStatusCache(time.Minute, discardLogger())
	var calls atomic.Int64
	release := make(chan struct{})
	fetch := func() *collector.MQTTBridgeStatus {
		calls.Add(1)
		<-release // hold the probe open so the second call must not block on it
		return &collector.MQTTBridgeStatus{Name: "x"}
	}

	if st, at := c.getAsync("key", fetch); st != nil || !at.IsZero() {
		t.Fatalf("first call returned (%v, %v), want (nil, zero) while probing", st, at)
	}
	// A second call while the probe is in flight returns immediately and does not
	// start a second probe.
	if st, _ := c.getAsync("key", fetch); st != nil {
		t.Fatalf("second call returned %v, want nil (probe still in flight)", st)
	}
	close(release)

	st, at := waitForProbe(t, c, "key", fetch)
	if st.Name != "x" {
		t.Errorf("status.Name = %q, want x", st.Name)
	}
	if time.Since(at) > time.Minute {
		t.Errorf("fetchedAt = %v, want the probe's completion time", at)
	}
	if got := calls.Load(); got != 1 {
		t.Errorf("probes = %d, want 1 (single-flight, then cache hits)", got)
	}
}

func TestBridgeStatusCacheAsyncRefreshesAfterTTL(t *testing.T) {
	c := newBridgeStatusCache(time.Nanosecond, discardLogger()) // expires immediately
	var calls atomic.Int64
	fetch := func() *collector.MQTTBridgeStatus {
		calls.Add(1)
		return &collector.MQTTBridgeStatus{}
	}
	waitForProbe(t, c, "k", fetch)
	// The entry is already expired, so the next read serves it and starts a
	// refresh: the caller still never waits.
	c.getAsync("k", fetch)
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) && calls.Load() < 2 {
		time.Sleep(5 * time.Millisecond)
	}
	if got := calls.Load(); got != 2 {
		t.Errorf("probes = %d, want 2 (expired entry is refreshed in the background)", got)
	}
}

// TestBridgeStatusCacheProbePanicRecovers pins that a panicking probe cannot wedge
// a bridge: the in-flight flag must be cleared, or that bridge would never be
// probed again.
func TestBridgeStatusCacheProbePanicRecovers(t *testing.T) {
	c := newBridgeStatusCache(time.Minute, discardLogger())
	var calls atomic.Int64
	panicking := func() *collector.MQTTBridgeStatus {
		calls.Add(1)
		panic("probe blew up")
	}
	c.getAsync("k", panicking)
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		c.mu.Lock()
		inFlight := c.entries["k"].fetching
		c.mu.Unlock()
		if !inFlight {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	c.mu.Lock()
	inFlight := c.entries["k"].fetching
	c.mu.Unlock()
	if inFlight {
		t.Fatal("entry still marked in-flight after the probe panicked — it would never refresh")
	}
	// The next read starts a fresh probe instead of being stuck.
	c.getAsync("k", panicking)
	deadline = time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) && calls.Load() < 2 {
		time.Sleep(5 * time.Millisecond)
	}
	if got := calls.Load(); got != 2 {
		t.Errorf("probes = %d, want 2 (a panicked probe must not block later ones)", got)
	}
}

// waitForProbe polls getAsync until the background probe has stored a result.
func waitForProbe(t *testing.T, c *bridgeStatusCache, key string, fetch func() *collector.MQTTBridgeStatus) (*collector.MQTTBridgeStatus, time.Time) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if st, at := c.getAsync(key, fetch); st != nil {
			return st, at
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("probe for %q never completed", key)
	return nil, time.Time{}
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
