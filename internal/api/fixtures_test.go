package api

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/noodlebit/machmqtt-dashboard/internal/auth"
	"github.com/noodlebit/machmqtt-dashboard/internal/collector"
	"github.com/noodlebit/machmqtt-dashboard/internal/config"
	"github.com/noodlebit/machmqtt-dashboard/internal/logbuf"
	"github.com/noodlebit/machmqtt-dashboard/internal/store"
	"github.com/noodlebit/machmqtt-dashboard/internal/ws"
)

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

const fixtureServerID = "srv-1"

// natsMockConfig controls what the mock NATS monitoring server returns so a
// single fixture can exercise both the snapshot-subs and HTTP-fallback subs
// paths.
type natsMockConfig struct {
	// subsInPlainConnz controls whether a plain /connz (the collector's poll)
	// includes per-connection subscription detail. When true the snapshot carries
	// subs (subsRowsFromConnz path); when false the API falls back to a
	// /connz?subs=detail HTTP fetch.
	subsInPlainConnz bool
	// subsDetailErrors makes /connz?subs=detail return 500, forcing getSubsRows'
	// HTTP fallback to retry via FetchConnzWithSubs (/connz?subs=true).
	subsDetailErrors bool
	// subsAsList makes the non-detail subs fetch return Subs []string instead of
	// SubsDetail, exercising the subscriptions-list code path.
	subsAsList bool
	// bridgeConn adds a connection named "machmqtt-bridge" (IP 127.0.0.1) so
	// connz-scan MQTT bridge discovery has a candidate to find.
	bridgeConn bool
	// connzReportedTotal, when non-zero, makes /connz report that many total
	// connections while still returning only the fixture rows — the shape a real
	// server has when it holds more connections than the poll fetches.
	connzReportedTotal int
	// subsConnzReportedTotal does the same but only for the subscription fetches
	// (/connz?subs=...), leaving the poll's plain /connz complete. That isolates
	// truncation of the subscription source from truncation of the snapshot connz.
	subsConnzReportedTotal int
}

// fixtureConns returns the two base connections the mock reports.
func fixtureConns() []collector.ConnInfo {
	return []collector.ConnInfo{
		{Cid: 1, Account: "ACC", Name: "client-a", IP: "10.0.0.1", NumSubs: 1},
		{Cid: 2, Account: "SYS", Name: "client-b", IP: "10.0.0.2", NumSubs: 1},
	}
}

func attachSubsDetail(conns []collector.ConnInfo) {
	conns[0].SubsDetail = []collector.SubDetail{{Subject: "foo.bar", Sid: "1", Cid: 1, Account: "ACC", Msgs: 3}}
	conns[1].SubsDetail = []collector.SubDetail{{Subject: "$SYS.SERVER.x", Sid: "1", Cid: 2, Account: "SYS"}}
}

func attachSubsList(conns []collector.ConnInfo) {
	conns[0].Subs = []string{"foo.bar"}
	conns[1].Subs = []string{"$SYS.SERVER.x"}
}

// natsMock returns an httptest server speaking the NATS monitoring API with rich
// fixture data.
func natsMock(t *testing.T, cfg natsMockConfig) *httptest.Server {
	t.Helper()
	now := time.Now()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		enc := func(v any) { _ = json.NewEncoder(w).Encode(v) }
		switch r.URL.Path {
		case "/varz":
			enc(collector.Varz{
				ServerID: fixtureServerID, ServerName: "nats-1", Version: "2.14.0",
				Connections: 2, Subscriptions: 5, CPU: 3.5, Mem: 4096,
				InMsgs: 100, OutMsgs: 200, InBytes: 1000, OutBytes: 2000,
				Routes: 0, Leafs: 1, Uptime: "1h", Now: now, Start: now.Add(-time.Hour),
			})
		case "/connz":
			subsParam := r.URL.Query().Get("subs")
			if subsParam == "detail" && cfg.subsDetailErrors {
				http.Error(w, "subs detail unavailable", http.StatusInternalServerError)
				return
			}
			conns := fixtureConns()
			switch {
			case subsParam == "detail", subsParam == "" && cfg.subsInPlainConnz, subsParam == "true" && !cfg.subsAsList:
				attachSubsDetail(conns)
			case subsParam != "" && cfg.subsAsList:
				attachSubsList(conns)
			}
			if cfg.bridgeConn {
				conns = append(conns, collector.ConnInfo{
					Cid: 3, Name: "machmqtt-bridge", IP: "127.0.0.1",
					NumSubs: 2, InMsgs: 10, OutMsgs: 20,
				})
			}
			total := len(conns)
			if cfg.connzReportedTotal > 0 {
				total = cfg.connzReportedTotal
			}
			if subsParam != "" && cfg.subsConnzReportedTotal > 0 {
				total = cfg.subsConnzReportedTotal
			}
			enc(collector.Connz{
				ServerID: fixtureServerID, NumConns: len(conns), Total: total,
				Limit: 1024, Conns: conns,
			})
		case "/routez":
			enc(collector.Routez{ServerID: fixtureServerID})
		case "/gatewayz":
			enc(collector.Gatewayz{ServerID: fixtureServerID})
		case "/leafz":
			enc(collector.Leafz{ServerID: fixtureServerID, NumLeafs: 1, Leafs: []collector.LeafInfo{{Account: "ACC", Name: "leaf-1"}}})
		case "/subsz":
			enc(collector.SubszResp{ServerID: fixtureServerID})
		case "/jsz":
			enc(collector.JSInfo{ServerID: fixtureServerID, Streams: 1, Consumers: 2, Messages: 10, Bytes: 100})
		case "/accountz":
			enc(collector.Accountz{ServerID: fixtureServerID, SystemAccount: "SYS", Accounts: []string{"ACC", "SYS"}})
		case "/healthz":
			enc(collector.HealthStatus{Status: "ok"})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

// polledServer wires a full API server whose collector has polled the given
// NATS mock once, so snapshot-backed handlers have real data. It returns the
// server, store, an admin token, and the cluster ID.
func polledServer(t *testing.T, natsCfg natsMockConfig, opts ...func(*polledOpts)) (*Server, *store.Store, string, string) {
	t.Helper()
	o := &polledOpts{}
	for _, fn := range opts {
		fn(o)
	}

	s, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })

	u, _ := s.CreateUser("admin", "pass", store.RoleAdmin)
	a := auth.New(s, "test-secret", false)
	token, _ := a.IssueToken(u)
	log := discardLogger()

	natsSrv := natsMock(t, natsCfg)
	cl := &store.Cluster{
		Name:          "env1",
		Servers:       []config.Server{{URL: natsSrv.URL}},
		MQTTBridges:   o.bridges,
		NATSConn:      o.natsConn,
		MQTTDiscovery: o.discovery,
	}
	if err := s.CreateCluster(cl); err != nil {
		t.Fatal(err)
	}

	cfg := &config.Config{PollInterval: time.Hour} // only the initial poll fires
	hub := ws.NewHub(log)
	polled := make(chan string, 8)
	mgr, err := collector.NewManager(cfg, func(id string) {
		select {
		case polled <- id:
		default:
		}
	}, log, s)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	mgr.Start(ctx)

	select {
	case <-polled:
	case <-time.After(5 * time.Second):
		t.Fatal("collector did not complete its initial poll")
	}

	// The poll launches connz bridge discovery in a background goroutine that
	// outlives it and finishes by replacing the collector's cached bridge list.
	// Returning while it is still in flight lets it land on top of whatever a
	// test seeds next, so the bridge reads back as missing on a slow or loaded
	// machine. Wait for it to settle before handing the server over.
	waitForDiscoveryIdle(t, mgr, cl.ID)

	var metrics *store.MetricsWriter
	if o.withMetrics {
		metrics = store.NewMetricsWriter(s, log, 24*time.Hour)
		go metrics.Run(ctx) // drains submitted samples to the DB
	}
	srv := NewServer(a, mgr, hub, log, "test", cfg, metrics, s, o.logBuf)
	return srv, s, token, cl.ID
}

func withMetrics() func(*polledOpts) {
	return func(o *polledOpts) { o.withMetrics = true }
}

// waitForDiscoveryIdle blocks until no MQTT bridge discovery is running for the
// cluster. Discovery is started in the background by a poll and replaces the
// collector's whole bridge list when it completes, so anything that seeds or
// asserts on bridges has to let it finish first.
func waitForDiscoveryIdle(t *testing.T, mgr *collector.Manager, clusterID string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		if !mgr.OperationalStats()[clusterID].Discovering {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("mqtt bridge discovery did not settle")
		}
		time.Sleep(2 * time.Millisecond)
	}
}

func withLogBuf(lb *logbuf.Handler) func(*polledOpts) {
	return func(o *polledOpts) { o.logBuf = lb }
}

// seedMetric submits one sample for env and blocks until it is queryable (the
// writer drains asynchronously), failing the test on timeout.
func seedMetric(t *testing.T, srv *Server, env string) {
	t.Helper()
	now := time.Now().Unix()
	srv.metrics.Submit(store.MetricSample{
		Timestamp:       time.Unix(now, 0),
		Env:             env,
		ServerCount:     1,
		ConnectionCount: 5,
		Servers:         []store.ServerMetricSample{{ServerID: fixtureServerID, Connections: 5}},
		MQTTBridges:     []store.MQTTBridgeMetricSample{{BridgeID: "bridge-a", ConnectionsActive: 2}},
	})
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		pts, err := srv.metrics.QueryEnvMetrics(context.Background(), env, now-60, now+60, 1)
		if err == nil && len(pts) > 0 {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("seeded metric never became queryable")
}

type polledOpts struct {
	bridges     []config.MQTTBridge
	withMetrics bool
	logBuf      *logbuf.Handler
	natsConn    *config.NATSConnConfig
	discovery   *config.MQTTDiscoveryConfig
}

// withDiscovery enables connz-scan MQTT bridge discovery against the given admin
// ports (typically a mock bridge's port).
func withDiscovery(ports ...int) func(*polledOpts) {
	return func(o *polledOpts) {
		o.discovery = &config.MQTTDiscoveryConfig{AdminPorts: ports}
	}
}

func withBridges(b ...config.MQTTBridge) func(*polledOpts) {
	return func(o *polledOpts) { o.bridges = b }
}

// withDeadNATSConn configures a NATS push connection with an invalid NKey seed
// so the subscriber's connect fails outright (rather than entering nats.go's
// background-retry state, which would still report "connected"). The subscriber
// is created but never connects, so the cluster reports Degraded.
func withDeadNATSConn() func(*polledOpts) {
	return func(o *polledOpts) {
		o.natsConn = &config.NATSConnConfig{
			URLs: []string{"nats://127.0.0.1:14444"},
			NKey: "not-a-valid-nkey-seed",
		}
	}
}

// do executes a request against the server and returns the recorder.
func do(t *testing.T, srv *Server, method, path, token, body string) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, authedReq(method, path, token, body))
	return w
}

// decodeJSON decodes a recorder body into a generic map.
func decodeJSON(t *testing.T, w *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &m); err != nil {
		t.Fatalf("decode body %q: %v", w.Body.String(), err)
	}
	return m
}

// bridgeMock returns an httptest server speaking the MachMQTT bridge admin API.
// The handler is supplied by the caller so each test controls the responses.
func bridgeMock(t *testing.T, handler http.HandlerFunc) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return srv
}
