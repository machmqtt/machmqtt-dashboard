package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/noodlebit/machmqtt-dashboard/internal/collector"
	"github.com/noodlebit/machmqtt-dashboard/internal/config"
)

// bridgeConfig controls the status codes a mock bridge returns for its
// cluster/admin endpoints so the proxy handlers' switch arms can be exercised.
type bridgeConfig struct {
	clusterCode int // /admin/cluster (default 200)
	inspectCode int // /admin/cluster/inspect (default 200)
	adminCode   int // POST /admin/<action> (default 200)
}

// okBridgeHandler serves a MachMQTT bridge admin API with valid canned data.
func okBridgeHandler(cfg bridgeConfig) http.HandlerFunc {
	if cfg.clusterCode == 0 {
		cfg.clusterCode = http.StatusOK
	}
	if cfg.inspectCode == 0 {
		cfg.inspectCode = http.StatusOK
	}
	if cfg.adminCode == 0 {
		cfg.adminCode = http.StatusOK
	}
	return func(w http.ResponseWriter, r *http.Request) {
		enc := func(v any) { _ = json.NewEncoder(w).Encode(v) }
		path := r.URL.Path
		switch {
		case path == "/readyz":
			enc(collector.MQTTReadyz{Status: "ready", NATSConnected: true})
		case path == "/connz" && r.URL.Query().Get("mqtt_client") != "":
			enc(collector.MQTTConnz{NumConnections: 1, Total: 1, Connections: []collector.MQTTClientInfo{{MQTTClient: "c1"}}})
		case path == "/connz":
			enc(collector.MQTTConnz{NumConnections: 1, Total: 1, Connections: []collector.MQTTClientInfo{{MQTTClient: "c1"}}})
		case path == "/diag/nats":
			enc(collector.MQTTNATSDiag{Connection: collector.MQTTNATSConnection{Connected: true}})
		case path == "/diag":
			enc(collector.MQTTDiag{})
		case path == "/license":
			enc(collector.MQTTLicense{})
		case path == "/pool":
			enc(collector.MQTTPool{})
		case path == "/metrics":
			_, _ = w.Write([]byte("machmqtt_connections_active 3\n"))
		case path == "/admin/cluster":
			if cfg.clusterCode != http.StatusOK {
				w.WriteHeader(cfg.clusterCode)
				return
			}
			enc(collector.MQTTCluster{})
		case path == "/admin/cluster/inspect":
			if cfg.inspectCode != http.StatusOK {
				w.WriteHeader(cfg.inspectCode)
				return
			}
			enc(collector.MQTTClusterInspect{})
		case strings.HasPrefix(path, "/admin/"):
			w.WriteHeader(cfg.adminCode)
			_, _ = w.Write([]byte(`{"ok":true}`))
		default:
			http.NotFound(w, r)
		}
	}
}

// bridgeServer with the given config, returning a configured cluster bridge.
func bridgeServerWith(t *testing.T, cfg bridgeConfig) (*Server, string, string) {
	t.Helper()
	bSrv := bridgeMock(t, okBridgeHandler(cfg))
	srv, _, token, id := polledServer(t, natsMockConfig{},
		withBridges(config.MQTTBridge{Name: "b1", URL: bSrv.URL}))
	return srv, token, id
}

// deadBridgeURL returns the URL of an httptest server that has been closed, so
// any request to it fails at the transport layer.
func deadBridgeURL(t *testing.T) string {
	t.Helper()
	s := httptest.NewServer(http.NotFoundHandler())
	url := s.URL
	s.Close()
	return url
}

func mqttPath(id, bridge, suffix string) string {
	return "/api/environments/" + id + "/mqtt/" + bridge + suffix
}

// bridgePort extracts the numeric port from a bridge mock's URL.
func bridgePort(t *testing.T, rawURL string) int {
	t.Helper()
	u, err := url.Parse(rawURL)
	if err != nil {
		t.Fatal(err)
	}
	p, err := strconv.Atoi(u.Port())
	if err != nil {
		t.Fatalf("parse port from %q: %v", rawURL, err)
	}
	return p
}

// TestMQTTDiscoveredBridgeResolution drives real connz-scan discovery: a
// "machmqtt-bridge" connection is reported by NATS, and the dashboard probes the
// admin port (the mock bridge) to discover it. This exercises the discovered-
// bridge arms of handleMQTTBridges and findBridge that the configured-only tests
// can't reach.
func TestMQTTDiscoveredBridgeResolution(t *testing.T) {
	bSrv := bridgeMock(t, okBridgeHandler(bridgeConfig{}))
	port := bridgePort(t, bSrv.URL)

	// A configured bridge whose URL equals the discovered admin URL exercises the
	// discovered-vs-configured merge path in handleMQTTBridges.
	srv, _, token, id := polledServer(t, natsMockConfig{bridgeConn: true},
		withDiscovery(port),
		withBridges(config.MQTTBridge{Name: "named-bridge", URL: bSrv.URL}))

	// Discovery runs asynchronously after the poll; wait for it to land.
	waitForDiscovery(t, srv, id)

	t.Run("bridges list includes discovered bridge", func(t *testing.T) {
		w := do(t, srv, "GET", "/api/environments/"+id+"/mqtt/bridges", token, "")
		if w.Code != http.StatusOK {
			t.Fatalf("status = %d", w.Code)
		}
		bridges := decodeJSON(t, w)["bridges"].([]any)
		if len(bridges) == 0 {
			t.Fatalf("no bridges returned: %s", w.Body.String())
		}
		// The discovered bridge carries the admin URL and was merged with the
		// configured name.
		found := false
		for _, b := range bridges {
			bm := b.(map[string]any)
			if bm["admin_url"] == bSrv.URL {
				found = true
				if bm["configured_name"] != "named-bridge" {
					t.Errorf("configured_name = %v, want named-bridge (merge)", bm["configured_name"])
				}
			}
		}
		if !found {
			t.Errorf("discovered bridge with admin_url %q not in list", bSrv.URL)
		}
	})

	t.Run("findBridge resolves discovered bridge for proxying", func(t *testing.T) {
		// "named-bridge" resolves via the discovered admin URL, so a proxied connz
		// call reaches the mock bridge and succeeds.
		w := do(t, srv, "GET", mqttPath(id, "named-bridge", "/connz"), token, "")
		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, body=%s", w.Code, w.Body.String())
		}
	})
}

// waitForDiscovery polls until the manager reports at least one discovered MQTT
// bridge for the env, failing on timeout.
func waitForDiscovery(t *testing.T, srv *Server, env string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if len(srv.manager.MQTTBridges(env)) > 0 {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatal("discovery did not produce a bridge within 5s")
}

func TestHandleMQTTBridges(t *testing.T) {
	bSrv := bridgeMock(t, okBridgeHandler(bridgeConfig{}))
	srv, _, token, id := polledServer(t, natsMockConfig{},
		withBridges(config.MQTTBridge{Name: "b1", URL: bSrv.URL}))

	w := do(t, srv, "GET", "/api/environments/"+id+"/mqtt/bridges", token, "")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", w.Code, w.Body.String())
	}
	bridges := decodeJSON(t, w)["bridges"].([]any)
	if len(bridges) != 1 {
		t.Fatalf("bridges = %d, want 1", len(bridges))
	}
	b := bridges[0].(map[string]any)
	if b["configured_name"] != "b1" {
		t.Errorf("configured_name = %v, want b1", b["configured_name"])
	}
	if b["reachable"] != true {
		t.Errorf("reachable = %v, want true (readyz returned 200)", b["reachable"])
	}
}

func TestHandleMQTTBridgesUnknownEnv(t *testing.T) {
	srv, _, token, _ := polledServer(t, natsMockConfig{})
	w := do(t, srv, "GET", "/api/environments/missing/mqtt/bridges", token, "")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	if bridges := decodeJSON(t, w)["bridges"].([]any); len(bridges) != 0 {
		t.Errorf("bridges = %d, want 0 for unknown env", len(bridges))
	}
}

func TestHandleMQTTConnz(t *testing.T) {
	srv, token, id := bridgeServerWith(t, bridgeConfig{})
	w := do(t, srv, "GET", mqttPath(id, "b1", "/connz"), token, "")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	if decodeJSON(t, w)["num_connections"].(float64) != 1 {
		t.Errorf("num_connections = %v, want 1", decodeJSON(t, w)["num_connections"])
	}
}

func TestHandleMQTTConnzBridgeNotFound(t *testing.T) {
	srv, token, id := bridgeServerWith(t, bridgeConfig{})
	w := do(t, srv, "GET", mqttPath(id, "nope", "/connz"), token, "")
	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", w.Code)
	}
}

func TestHandleMQTTConnzUnavailable(t *testing.T) {
	// Dead bridge → FetchConnz errors → handler returns a 200 fallback payload.
	srv, _, token, id := polledServer(t, natsMockConfig{},
		withBridges(config.MQTTBridge{Name: "b1", URL: deadBridgeURL(t)}))
	w := do(t, srv, "GET", mqttPath(id, "b1", "/connz"), token, "")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 fallback", w.Code)
	}
	if decodeJSON(t, w)["error"] != "connz not available" {
		t.Errorf("error = %v, want 'connz not available'", decodeJSON(t, w)["error"])
	}
}

func TestHandleMQTTClient(t *testing.T) {
	srv, token, id := bridgeServerWith(t, bridgeConfig{})
	w := do(t, srv, "GET", mqttPath(id, "b1", "/connz/c1"), token, "")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", w.Code, w.Body.String())
	}
	if decodeJSON(t, w)["mqtt_client"] != "c1" {
		t.Errorf("mqtt_client = %v, want c1", decodeJSON(t, w)["mqtt_client"])
	}
}

func TestHandleMQTTClientNotFound(t *testing.T) {
	// Bridge returns an empty connection list for the client → 404.
	emptyBridge := bridgeMock(t, func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(collector.MQTTConnz{Connections: nil})
	})
	srv, _, token, id := polledServer(t, natsMockConfig{},
		withBridges(config.MQTTBridge{Name: "b1", URL: emptyBridge.URL}))
	w := do(t, srv, "GET", mqttPath(id, "b1", "/connz/missing"), token, "")
	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", w.Code)
	}
}

func TestHandleMQTTClientError(t *testing.T) {
	srv, _, token, id := polledServer(t, natsMockConfig{},
		withBridges(config.MQTTBridge{Name: "b1", URL: deadBridgeURL(t)}))
	w := do(t, srv, "GET", mqttPath(id, "b1", "/connz/c1"), token, "")
	if w.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502", w.Code)
	}
}

// TestMQTTSimpleProxyHandlers covers the diag/pool/bridgeDiag/license/metrics
// handlers, which share the same shape: 200 on success, 502 on bridge error.
func TestMQTTSimpleProxyHandlers(t *testing.T) {
	suffixes := []string{"/diag", "/pool", "/diag/config", "/license", "/metrics"}

	t.Run("success", func(t *testing.T) {
		srv, token, id := bridgeServerWith(t, bridgeConfig{})
		for _, sfx := range suffixes {
			w := do(t, srv, "GET", mqttPath(id, "b1", sfx), token, "")
			if w.Code != http.StatusOK {
				t.Errorf("%s status = %d, want 200", sfx, w.Code)
			}
		}
	})

	t.Run("bridge error", func(t *testing.T) {
		srv, _, token, id := polledServer(t, natsMockConfig{},
			withBridges(config.MQTTBridge{Name: "b1", URL: deadBridgeURL(t)}))
		for _, sfx := range suffixes {
			w := do(t, srv, "GET", mqttPath(id, "b1", sfx), token, "")
			if w.Code != http.StatusBadGateway {
				t.Errorf("%s status = %d, want 502", sfx, w.Code)
			}
		}
	})

	t.Run("bridge not found", func(t *testing.T) {
		srv, token, id := bridgeServerWith(t, bridgeConfig{})
		for _, sfx := range suffixes {
			w := do(t, srv, "GET", mqttPath(id, "nope", sfx), token, "")
			if w.Code != http.StatusNotFound {
				t.Errorf("%s status = %d, want 404", sfx, w.Code)
			}
		}
	})
}

func TestHandleMQTTCluster(t *testing.T) {
	cases := []struct {
		code      int
		available bool
	}{
		{http.StatusOK, true},
		{http.StatusConflict, false},
		{http.StatusNotFound, false},
		{http.StatusUnauthorized, false},
		{http.StatusTeapot, false}, // default arm
	}
	for _, tc := range cases {
		t.Run(http.StatusText(tc.code), func(t *testing.T) {
			srv, token, id := bridgeServerWith(t, bridgeConfig{clusterCode: tc.code})
			w := do(t, srv, "GET", mqttPath(id, "b1", "/cluster"), token, "")
			if w.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200", w.Code)
			}
			if decodeJSON(t, w)["available"] != tc.available {
				t.Errorf("available = %v, want %v for bridge code %d",
					decodeJSON(t, w)["available"], tc.available, tc.code)
			}
		})
	}
}

func TestHandleMQTTClusterError(t *testing.T) {
	srv, _, token, id := polledServer(t, natsMockConfig{},
		withBridges(config.MQTTBridge{Name: "b1", URL: deadBridgeURL(t)}))
	w := do(t, srv, "GET", mqttPath(id, "b1", "/cluster"), token, "")
	if w.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502", w.Code)
	}
}

func TestHandleMQTTClusterInspect(t *testing.T) {
	cases := []struct {
		code  int
		found bool
	}{
		{http.StatusOK, true},
		{http.StatusNotFound, false},
		{http.StatusConflict, false},
		{http.StatusTooManyRequests, false},
		{http.StatusForbidden, false},
		{http.StatusTeapot, false}, // default arm
	}
	for _, tc := range cases {
		t.Run(http.StatusText(tc.code), func(t *testing.T) {
			srv, token, id := bridgeServerWith(t, bridgeConfig{inspectCode: tc.code})
			w := do(t, srv, "GET", mqttPath(id, "b1", "/cluster/inspect?client_id=c1"), token, "")
			if w.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200", w.Code)
			}
			if decodeJSON(t, w)["found"] != tc.found {
				t.Errorf("found = %v, want %v for code %d", decodeJSON(t, w)["found"], tc.found, tc.code)
			}
		})
	}
}

func TestHandleMQTTClusterInspectMissingClientID(t *testing.T) {
	srv, token, id := bridgeServerWith(t, bridgeConfig{})
	w := do(t, srv, "GET", mqttPath(id, "b1", "/cluster/inspect"), token, "")
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (client_id required)", w.Code)
	}
}

func TestHandleMQTTClusterInspectError(t *testing.T) {
	srv, _, token, id := polledServer(t, natsMockConfig{},
		withBridges(config.MQTTBridge{Name: "b1", URL: deadBridgeURL(t)}))
	w := do(t, srv, "GET", mqttPath(id, "b1", "/cluster/inspect?client_id=c1"), token, "")
	if w.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502", w.Code)
	}
}

func TestHandleMQTTAdminActionSuccess(t *testing.T) {
	srv, token, id := bridgeServerWith(t, bridgeConfig{adminCode: http.StatusOK})
	w := do(t, srv, "POST", mqttPath(id, "b1", "/admin/drain"), token, `{"x":1}`)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", w.Code, w.Body.String())
	}
	if decodeJSON(t, w)["ok"] != true {
		t.Errorf("body = %s, want forwarded {ok:true}", w.Body.String())
	}
}

func TestHandleMQTTAdminActionForwardsBridgeStatus(t *testing.T) {
	// The bridge replies 409; the handler relays that status verbatim.
	srv, token, id := bridgeServerWith(t, bridgeConfig{adminCode: http.StatusConflict})
	w := do(t, srv, "POST", mqttPath(id, "b1", "/admin/reload"), token, "")
	if w.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409 (relayed)", w.Code)
	}
}

func TestHandleMQTTAdminActionBridgeNotFound(t *testing.T) {
	srv, token, id := bridgeServerWith(t, bridgeConfig{})
	w := do(t, srv, "POST", mqttPath(id, "nope", "/admin/drain"), token, "")
	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", w.Code)
	}
}

func TestHandleMQTTAdminActionBridgeError(t *testing.T) {
	srv, _, token, id := polledServer(t, natsMockConfig{},
		withBridges(config.MQTTBridge{Name: "b1", URL: deadBridgeURL(t)}))
	w := do(t, srv, "POST", mqttPath(id, "b1", "/admin/drain"), token, "")
	if w.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502", w.Code)
	}
}
