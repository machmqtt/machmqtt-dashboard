package api

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/noodlebit/machmqtt-dashboard/internal/auth"
	"github.com/noodlebit/machmqtt-dashboard/internal/collector"
	"github.com/noodlebit/machmqtt-dashboard/internal/config"
	"github.com/noodlebit/machmqtt-dashboard/internal/store"
	"github.com/noodlebit/machmqtt-dashboard/internal/ws"
)

func natsFixture(t *testing.T, exposeMQTTBridge bool) *httptest.Server {
	t.Helper()
	connectionName := "client"
	if exposeMQTTBridge {
		connectionName = "machmqtt-bridge"
	}
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/varz":
			fmt.Fprint(w, `{"server_id":"s1","server_name":"one","connections":1,"now":"2026-07-31T12:00:00Z"}`)
		case "/connz":
			if r.URL.Query().Get("subs") == "detail" {
				http.Error(w, "detail unsupported", http.StatusBadRequest)
				return
			}
			fmt.Fprintf(w, `{"server_id":"s1","total":1,"offset":0,"limit":1024,"connections":[{"cid":7,"ip":"127.0.0.1","port":4222,"name":%q,"account":"A","subscriptions_list":["foo"],"subscriptions_list_detail":[{"subject":"foo","sid":"1"}]}]}`, connectionName)
		case "/routez":
			fmt.Fprint(w, `{"server_id":"s1","num_routes":0,"routes":[]}`)
		case "/gatewayz":
			fmt.Fprint(w, `{"server_id":"s1","outbound_gateways":{},"inbound_gateways":{}}`)
		case "/leafz":
			fmt.Fprint(w, `{"server_id":"s1","leafnodes":1,"leafs":[{"id":1,"account":"A"}]}`)
		case "/subsz":
			fmt.Fprint(w, `{"server_id":"s1","num_subscriptions":1}`)
		case "/jsz":
			fmt.Fprint(w, `{"server_id":"s1","streams":0}`)
		case "/accountz":
			if r.URL.Query().Get("acc") != "" {
				if r.URL.Query().Get("acc") != "A" {
					fmt.Fprint(w, `{"server_id":"s1"}`)
					return
				}
				fmt.Fprint(w, `{"server_id":"s1","account_detail":{"account_name":"A","client_connections":1,"subscriptions":1}}`)
				return
			}
			fmt.Fprint(w, `{"server_id":"s1","accounts":["A"]}`)
		case "/healthz":
			fmt.Fprint(w, `{"status":"ok"}`)
		default:
			http.NotFound(w, r)
		}
	}))
}

func mqttFixture(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/readyz":
			fmt.Fprint(w, `{"status":"ready","connections":1,"nats_connected":true}`)
		case "/connz":
			fmt.Fprint(w, `{"total":1,"num_connections":1,"connections":[{"mqtt_client":"c1"}]}`)
		case "/diag/nats", "/diag", "/license", "/pool":
			fmt.Fprint(w, `{}`)
		case "/metrics":
			w.Header().Set("Content-Type", "text/plain")
			fmt.Fprint(w, "machmqtt_connections_active 1\n")
		default:
			http.NotFound(w, r)
		}
	}))
}

func setupLiveAPIServer(t *testing.T) (*Server, string) {
	return setupLiveAPIServerWithDiscovery(t, true)
}

func setupLiveAPIServerWithDiscovery(t *testing.T, exposeMQTTBridge bool) (*Server, string) {
	t.Helper()
	nats := natsFixture(t, exposeMQTTBridge)
	mqtt := mqttFixture(t)
	t.Cleanup(nats.Close)
	t.Cleanup(mqtt.Close)
	db, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	user, err := db.CreateUser("admin", "password", store.RoleAdmin)
	if err != nil {
		t.Fatal(err)
	}
	a := auth.New(db, strings.Repeat("s", 32), false)
	t.Cleanup(a.Close)
	token, err := a.IssueToken(user)
	if err != nil {
		t.Fatal(err)
	}
	mqttURL, _ := url.Parse(mqtt.URL)
	mqttPort, _ := strconv.Atoi(mqttURL.Port())
	cfg := &config.Config{PollInterval: 20 * time.Millisecond, Environments: []config.Environment{{
		Name: "test", Servers: []config.Server{{URL: nats.URL}, {URL: nats.URL}}, MQTTBridges: []config.MQTTBridge{{Name: "bridge", URL: mqtt.URL}},
		MQTTDiscovery: &config.MQTTDiscoveryConfig{AdminPorts: []int{mqttPort}},
	}}}
	if _, err := db.SeedClusters(cfg.Environments); err != nil {
		t.Fatal(err)
	}
	// Keep the existing human-readable integration paths deterministic while
	// exercising the production seed-before-manager startup sequence.
	if _, err := db.DB().Exec("UPDATE clusters SET id = ? WHERE name = ?", "test", "test"); err != nil {
		t.Fatal(err)
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	manager, err := collector.NewManager(cfg, nil, log, db)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	manager.Start(ctx)
	t.Cleanup(func() {
		cancel()
		manager.Wait()
	})
	deadline := time.Now().Add(time.Second)
	for manager.Snapshot("test").Timestamp.IsZero() && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if exposeMQTTBridge {
		for len(manager.MQTTBridges("test")) == 0 && time.Now().Before(deadline) {
			time.Sleep(time.Millisecond)
		}
	}
	metrics := store.NewMetricsWriter(db.DB(), log)
	_ = metrics.Submit(store.MetricSample{Timestamp: time.Now(), Env: "test"})
	srv := NewServer(a, manager, ws.NewHub(log), log, "test-version", cfg, metrics, db, nil)
	return srv, token
}

func updateLiveCluster(t *testing.T, srv *Server, mutate func(*store.Cluster)) {
	t.Helper()
	cluster, err := srv.store.GetCluster("test")
	if err != nil {
		t.Fatal(err)
	}
	mutate(cluster)
	if err := srv.store.UpdateCluster(cluster); err != nil {
		t.Fatal(err)
	}
	if err := srv.manager.UpdateCluster(*cluster); err != nil {
		t.Fatal(err)
	}
	// Configuration changes must not reuse bodies cached under the prior config.
	srv.bridgeJSON = newBridgeRespCache(3 * time.Second)
}

func TestLiveAPIReadAndPersistenceRoutes(t *testing.T) {
	srv, token := setupLiveAPIServer(t)
	paths := []string{
		"/api/version", "/api/environments", "/api/environments/test/overview", "/api/environments/test/topology",
		"/api/environments/test/varz", "/api/environments/test/connz?limit=1", "/api/environments/test/connz/7",
		"/api/environments/test/routez", "/api/environments/test/gatewayz", "/api/environments/test/leafz",
		"/api/environments/test/subsz", "/api/environments/test/subsz/detail", "/api/environments/test/subsz/detail?subject=foo",
		"/api/environments/test/jsz", "/api/environments/test/accountz", "/api/environments/test/accountz/A",
		"/api/environments/test/topology/positions", "/api/environments/test/metrics/overview",
		"/api/environments/test/metrics/servers", "/api/environments/test/metrics/mqtt",
		"/api/environments/test/mqtt/bridges", "/api/environments/test/mqtt/bridge/connz",
		"/api/environments/test/mqtt/bridge/connz/c1", "/api/environments/test/mqtt/bridge/diag",
		"/api/environments/test/mqtt/bridge/diag/config", "/api/environments/test/mqtt/bridge/license",
		"/api/environments/test/mqtt/bridge/metrics", "/api/environments/test/mqtt/bridge/pool",
		"/api/admin/status",
	}
	for _, path := range paths {
		t.Run(path, func(t *testing.T) {
			w := httptest.NewRecorder()
			srv.Handler().ServeHTTP(w, authedReq(http.MethodGet, path, token, ""))
			if w.Code != http.StatusOK {
				t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
			}
			if w.Header().Get("X-Request-ID") == "" {
				t.Fatal("missing request ID")
			}
		})
	}
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, authedReq(http.MethodPut, "/api/environments/test/topology/positions", token, `{"positions":[{"node_id":"s1","x":1,"y":2}],"camera":{"zoom":2,"center_x":3,"center_y":4}}`))
	if w.Code != http.StatusOK {
		t.Fatalf("save positions status=%d body=%s", w.Code, w.Body.String())
	}
	w = httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, authedReq(http.MethodGet, "/api/environments/test/topology/positions", token, ""))
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"camera"`) {
		t.Fatalf("load saved camera status=%d body=%s", w.Code, w.Body.String())
	}
	w = httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, authedReq(http.MethodGet, "/api/environments/test/connz?offset=999&limit=999", token, ""))
	if w.Code != http.StatusOK {
		t.Fatalf("bounded connz status=%d body=%s", w.Code, w.Body.String())
	}
}

func TestOperationalAndHandlerFailureRoutes(t *testing.T) {
	srv, token := setupLiveAPIServer(t)
	if srv.envConfig("missing") != nil || srv.mqttBridges("missing") != nil {
		t.Fatal("missing environment config")
	}
	updateLiveCluster(t, srv, func(cluster *store.Cluster) { cluster.MQTTBridges = nil })
	resolved := srv.findBridge("test", "127.0.0.1")
	if resolved == nil || resolved.URL == "" {
		t.Fatalf("discovered bridge=%+v", resolved)
	}
	for _, path := range []string{"/livez", "/readyz", "/metrics"} {
		w := httptest.NewRecorder()
		srv.Handler().ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
		if w.Code != http.StatusOK {
			t.Fatalf("%s status=%d", path, w.Code)
		}
	}
	login := httptest.NewRecorder()
	srv.auth.HandleLocalLogin(login, httptest.NewRequest(http.MethodPost, "/api/auth/local/login", strings.NewReader(`{"username":"admin","password":"wrong"}`)))
	metrics := httptest.NewRecorder()
	srv.handleMetrics(metrics, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if !strings.Contains(metrics.Body.String(), "nats_dashboard_authentication_events_total") {
		t.Fatal("authentication metrics missing")
	}
	srv.SetReady(false)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("readiness status=%d", w.Code)
	}

	tests := []struct {
		path   string
		status int
	}{
		{"/api/environments/missing/overview", 404}, {"/api/environments/test/connz/bad", 400},
		{"/api/environments/test/connz/999", 404}, {"/api/environments/missing/connz", 404},
		{"/api/environments/test/mqtt/missing/connz", 404}, {"/api/environments/missing/topology/positions", 200},
	}
	for _, tc := range tests {
		w := httptest.NewRecorder()
		srv.Handler().ServeHTTP(w, authedReq(http.MethodGet, tc.path, token, ""))
		if w.Code != tc.status {
			t.Errorf("%s status=%d want=%d body=%s", tc.path, w.Code, tc.status, w.Body.String())
		}
	}
}

func TestAuthenticatedWebSocketRoute(t *testing.T) {
	srv, token := setupLiveAPIServer(t)
	httpServer := httptest.NewServer(srv.Handler())
	defer httpServer.Close()
	header := http.Header{}
	header.Set("Cookie", "session="+token)
	header.Set("Origin", httpServer.URL)
	conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(httpServer.URL, "http")+"/api/ws", header)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if err := conn.WriteJSON(map[string]string{"subscribe": "test"}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(10 * time.Millisecond)
	srv.hub.Broadcast("test", "overview", map[string]int{"servers": 1})
	_ = conn.SetReadDeadline(time.Now().Add(time.Second))
	if _, _, err := conn.ReadMessage(); err != nil {
		t.Fatal(err)
	}
}

func TestAPIUtilitiesOriginsSPAAndObservabilityBranches(t *testing.T) {
	for _, tc := range []struct {
		origin, host string
		want         bool
	}{
		{"", "example.com", true}, {"https://example.com", "example.com", true},
		{"https://evil.example", "example.com", false}, {"://bad", "example.com", false},
	} {
		r := httptest.NewRequest(http.MethodGet, "http://"+tc.host+"/", nil)
		r.Host = tc.host
		r.Header.Set("Origin", tc.origin)
		if got := checkSameOrigin(r); got != tc.want {
			t.Errorf("origin %q host %q=%v", tc.origin, tc.host, got)
		}
	}
	for _, tc := range []struct {
		value          string
		def, max, want int
	}{{"", 5, 10, 5}, {"bad", 5, 10, 5}, {"-1", 5, 10, 5}, {"0", 5, 10, 5}, {"99", 5, 10, 10}, {"7", 5, 10, 7}} {
		if got := clampInt(tc.value, tc.def, tc.max); got != tc.want {
			t.Errorf("clamp %q=%d", tc.value, got)
		}
	}
	for subject, want := range map[string]bool{"": false, "foo": false, "_SYS": true, "$SYS": true, "$MQTT5.foo": false} {
		if got := isSystemSubject(subject); got != want {
			t.Errorf("system subject %q=%v", subject, got)
		}
	}
	for ua, want := range map[string]string{"": "unknown", "Mozilla/5.0": "browser", "curl/8": "api"} {
		r := httptest.NewRequest(http.MethodGet, "/", nil)
		r.Header.Set("User-Agent", ua)
		if got := clientClass(r); got != want {
			t.Errorf("client class=%s", got)
		}
	}
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.Header.Set("X-Request-ID", "known-id")
	if requestID(r) != "known-id" {
		t.Fatal("valid request ID not preserved")
	}
	r.Header.Set("X-Request-ID", "bad id")
	if requestID(r) == "bad id" {
		t.Fatal("invalid request ID preserved")
	}
	now := time.Now().Unix()
	rangeRequest := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/?from=%d&to=%d&step=2", now-10, now), nil)
	from, to, step := parseTimeRange(rangeRequest)
	if from != now-10 || to != now || step != 2 {
		t.Fatalf("time range=%d %d %d", from, to, step)
	}
	from, to, step = parseTimeRange(httptest.NewRequest(http.MethodGet, "/?from=bad&to=bad&step=999999", nil))
	if from == 0 || to == 0 || step != 0 {
		t.Fatalf("default time range=%d %d %d", from, to, step)
	}
	jsonRecorder := httptest.NewRecorder()
	writeJSON(jsonRecorder, make(chan int))

	srv, _ := setupLiveAPIServer(t)
	for _, path := range []string{"/", "/index.html", "/does-not-exist"} {
		w := httptest.NewRecorder()
		srv.Handler().ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
		if w.Code != http.StatusOK && (path != "/index.html" || w.Code != http.StatusMovedPermanently) {
			t.Fatalf("SPA %s status=%d", path, w.Code)
		}
	}
}

func TestMetricsDisabledAndSubscriptionCacheBranches(t *testing.T) {
	srv, token := setupLiveAPIServer(t)
	for i := 0; i < 2; i++ {
		w := httptest.NewRecorder()
		srv.Handler().ServeHTTP(w, authedReq(http.MethodGet, "/api/environments/test/subsz/detail?account=A&server=one&hide_system=true", token, ""))
		if w.Code != http.StatusOK {
			t.Fatalf("cached subscriptions status=%d", w.Code)
		}
	}
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, authedReq(http.MethodGet, "/api/environments/missing/subsz/detail", token, ""))
	if w.Code != http.StatusNotFound {
		t.Fatalf("missing subscriptions status=%d", w.Code)
	}
	w = httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, authedReq(http.MethodGet, "/api/environments/test/accountz/missing", token, ""))
	if w.Code != http.StatusNotFound {
		t.Fatalf("missing account status=%d", w.Code)
	}

	srv.metrics = nil
	for _, handler := range []http.HandlerFunc{srv.handleEnvMetrics, srv.handleServerMetrics, srv.handleMQTTBridgeMetrics} {
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodGet, "/", nil)
		r.SetPathValue("env", "test")
		handler(w, r)
		if w.Code != http.StatusServiceUnavailable {
			t.Fatalf("disabled metrics status=%d", w.Code)
		}
	}
}

func TestHandlerDatabaseAndUpstreamFailureBranches(t *testing.T) {
	// Disable connz-based MQTT discovery so every detail response below must use
	// the configured bridge's live admin URL rather than a cached push snapshot.
	srv, token := setupLiveAPIServerWithDiscovery(t, false)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, authedReq(http.MethodPut, "/api/environments/test/topology/positions", token, `{`))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("bad positions status=%d", w.Code)
	}

	// A dead configured bridge exercises bounded upstream failure handling.
	updateLiveCluster(t, srv, func(cluster *store.Cluster) {
		cluster.MQTTBridges[0].URL = "http://127.0.0.1:1"
	})
	for _, tc := range []struct {
		path   string
		status int
	}{
		{"/api/environments/test/mqtt/bridge/connz", 200},
		{"/api/environments/test/mqtt/bridge/connz/c1", 502},
		{"/api/environments/test/mqtt/bridge/diag", 502},
		{"/api/environments/test/mqtt/bridge/diag/config", 502},
		{"/api/environments/test/mqtt/bridge/license", 502},
		{"/api/environments/test/mqtt/bridge/metrics", 502},
		{"/api/environments/test/mqtt/bridge/pool", 502},
	} {
		w := httptest.NewRecorder()
		srv.Handler().ServeHTTP(w, authedReq(http.MethodGet, tc.path, token, ""))
		if w.Code != tc.status {
			t.Errorf("%s status=%d want=%d", tc.path, w.Code, tc.status)
		}
	}

	if err := srv.store.DB().Close(); err != nil {
		t.Fatal(err)
	}
	status := httptest.NewRecorder()
	srv.handleDependencyStatus(status, httptest.NewRequest(http.MethodGet, "/api/admin/status", nil))
	if status.Code != http.StatusOK || !strings.Contains(status.Body.String(), `"database":"unavailable"`) {
		t.Fatalf("dependency status=%d body=%s", status.Code, status.Body.String())
	}
	for _, tc := range []struct {
		method, path, body string
		handler            http.HandlerFunc
	}{
		{http.MethodGet, "/api/environments/test/topology/positions", "", srv.handleGetPositions},
		{http.MethodPut, "/api/environments/test/topology/positions", `{"positions":[]}`, srv.handleSavePositions},
		{http.MethodGet, "/api/environments/test/metrics/overview", "", srv.handleEnvMetrics},
		{http.MethodGet, "/api/environments/test/metrics/servers", "", srv.handleServerMetrics},
		{http.MethodGet, "/api/environments/test/metrics/mqtt", "", srv.handleMQTTBridgeMetrics},
	} {
		w := httptest.NewRecorder()
		r := authedReq(tc.method, tc.path, token, tc.body)
		r.SetPathValue("env", "test")
		tc.handler.ServeHTTP(w, r)
		if w.Code != http.StatusInternalServerError {
			t.Errorf("%s status=%d", tc.path, w.Code)
		}
	}
}

func TestCameraPersistenceAndMissingEnvironmentFailures(t *testing.T) {
	srv, _ := setupLiveAPIServer(t)
	if _, err := srv.store.DB().Exec(`DROP TABLE topology_camera`); err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPut, "/", strings.NewReader(`{"positions":[],"camera":{"zoom":1}}`))
	r.SetPathValue("env", "test")
	srv.handleSavePositions(w, r)
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("camera failure status=%d body=%s", w.Code, w.Body.String())
	}

	for _, handler := range []http.HandlerFunc{srv.handleConnzDetail, srv.handleAccountDetail} {
		w = httptest.NewRecorder()
		r = httptest.NewRequest(http.MethodGet, "/", nil)
		r.SetPathValue("env", "missing")
		r.SetPathValue("cid", "1")
		r.SetPathValue("acc", "A")
		handler(w, r)
		if w.Code != http.StatusNotFound {
			t.Fatalf("missing environment status=%d body=%s", w.Code, w.Body.String())
		}
	}
}

func TestSnapshotNotFoundAndPanicRecovery(t *testing.T) {
	srv, token := setupLiveAPIServer(t)
	for _, suffix := range []string{"topology", "varz", "routez", "gatewayz", "leafz", "subsz", "jsz", "accountz"} {
		w := httptest.NewRecorder()
		srv.Handler().ServeHTTP(w, authedReq(http.MethodGet, "/api/environments/missing/"+suffix, token, ""))
		if w.Code != http.StatusNotFound {
			t.Errorf("%s status=%d", suffix, w.Code)
		}
	}
	w := httptest.NewRecorder()
	srv.observe(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { panic("test panic") })).ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/panic", nil))
	if w.Code != http.StatusInternalServerError || srv.ops.panics.Load() != 1 {
		t.Fatalf("panic status=%d count=%d", w.Code, srv.ops.panics.Load())
	}

	base := httptest.NewRecorder()
	capture := &responseCapture{ResponseWriter: base}
	if capture.Unwrap() != base {
		t.Fatal("unwrap")
	}
	capture.Flush()
	capture.WriteHeader(http.StatusCreated)
	capture.WriteHeader(http.StatusTeapot)
	if capture.status != http.StatusCreated {
		t.Fatalf("duplicate WriteHeader changed status to %d", capture.status)
	}
	if _, _, err := capture.Hijack(); err == nil {
		t.Fatal("recorder should not support hijacking")
	}
	upgrade := httptest.NewRecorder()
	srv.handleWS(upgrade, httptest.NewRequest(http.MethodGet, "/api/ws", nil))
	if upgrade.Code == http.StatusSwitchingProtocols {
		t.Fatal("invalid websocket upgrade unexpectedly succeeded")
	}
}

func TestMQTTHandlerEdgeCases(t *testing.T) {
	srv, token := setupLiveAPIServer(t)

	// A second configured bridge is not among the discovery results. This covers
	// the status probe, unreachable state, sorting, and missing-environment result.
	updateLiveCluster(t, srv, func(cluster *store.Cluster) {
		cluster.MQTTBridges = append(cluster.MQTTBridges,
			config.MQTTBridge{Name: "aardvark", URL: "http://127.0.0.1:1"})
	})
	for _, path := range []string{
		"/api/environments/test/mqtt/bridges",
		"/api/environments/missing/mqtt/bridges",
	} {
		w := httptest.NewRecorder()
		srv.Handler().ServeHTTP(w, authedReq(http.MethodGet, path, token, ""))
		if w.Code != http.StatusOK {
			t.Fatalf("%s status=%d body=%s", path, w.Code, w.Body.String())
		}
		if path == "/api/environments/test/mqtt/bridges" && !strings.Contains(w.Body.String(), `"name":"aardvark"`) {
			t.Fatalf("configured bridge absent from fleet response: %s", w.Body.String())
		}
	}

	// Every bridge-detail endpoint has its own not-found guard. Exercise each so
	// future routes cannot accidentally dereference a missing configuration.
	for _, suffix := range []string{
		"connz", "connz/client", "diag", "diag/config", "license", "metrics", "pool",
	} {
		path := "/api/environments/test/mqtt/does-not-exist/" + suffix
		w := httptest.NewRecorder()
		srv.Handler().ServeHTTP(w, authedReq(http.MethodGet, path, token, ""))
		if w.Code != http.StatusNotFound {
			t.Errorf("%s status=%d body=%s", path, w.Code, w.Body.String())
		}
	}

	empty := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/connz" {
			fmt.Fprint(w, `{"total":0,"num_connections":0,"connections":[]}`)
			return
		}
		http.NotFound(w, r)
	}))
	defer empty.Close()
	updateLiveCluster(t, srv, func(cluster *store.Cluster) {
		cluster.MQTTBridges = append(cluster.MQTTBridges,
			config.MQTTBridge{Name: "empty", URL: empty.URL})
	})
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, authedReq(http.MethodGet, "/api/environments/test/mqtt/empty/connz/client", token, ""))
	if w.Code != http.StatusNotFound || !strings.Contains(w.Body.String(), "client not found") {
		t.Fatalf("empty client status=%d body=%s", w.Code, w.Body.String())
	}
}

func TestSubscriptionFallbackFilteringAndCacheEviction(t *testing.T) {
	srv, token := setupLiveAPIServer(t)

	// Seed rows to cover all client-side filters without depending on a NATS
	// server's subject matching interpretation.
	srv.subsCacheData["test"] = &subsCacheEntry{fetchedAt: time.Now(), rows: []subRow{
		{Subject: "$SYS.internal", Account: "SYS", ServerID: "s1", ServerName: "one"},
		{Subject: "$MQTT5.client", Account: "A", ServerID: "s1", ServerName: "one"},
		{Subject: "orders.created", Account: "A", ServerID: "s1", ServerName: "one"},
		{Subject: "orders.deleted", Account: "B", ServerID: "s2", ServerName: "two"},
	}}
	for _, query := range []string{
		"?hide_system=true", "?account=A", "?server=s1", "?server=one", "?offset=999&limit=1",
	} {
		w := httptest.NewRecorder()
		srv.Handler().ServeHTTP(w, authedReq(http.MethodGet, "/api/environments/test/subsz/detail"+query, token, ""))
		if w.Code != http.StatusOK {
			t.Fatalf("filter %s status=%d body=%s", query, w.Code, w.Body.String())
		}
	}

	// Expired entries cause a reload; an oversized cache evicts its oldest item.
	oldest := time.Now().Add(-time.Hour)
	for i := 0; i <= subsCacheMaxEntries; i++ {
		srv.subsCacheData[fmt.Sprintf("old-%d", i)] = &subsCacheEntry{fetchedAt: oldest.Add(time.Duration(i) * time.Second)}
	}
	srv.subsCacheData["test"] = &subsCacheEntry{fetchedAt: time.Time{}}
	rows, _ := srv.getSubsRows(context.Background(), "test")
	if len(rows) == 0 {
		t.Fatal("expected subscription reload")
	}
	if len(srv.subsCacheData) > subsCacheMaxEntries+1 {
		t.Fatalf("cache entries=%d", len(srv.subsCacheData))
	}
	if cached, _ := srv.loadSubsRows(context.Background(), "test"); len(cached) == 0 {
		t.Fatal("expected direct cached subscription rows")
	}
	if rows, truncated := srv.loadSubsRows(context.Background(), "missing"); rows != nil || truncated {
		t.Fatalf("missing subscription rows=%v truncated=%v", rows, truncated)
	}
}
