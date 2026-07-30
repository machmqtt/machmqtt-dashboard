package collector

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// newMQTTTestFetcher creates a MQTTBridgeFetcher pointing at srv.URL.
func newMQTTTestFetcher(srv *httptest.Server) *MQTTBridgeFetcher {
	return NewMQTTBridgeFetcher(srv.URL, "test-bridge", "")
}

// --- getWithStatus nil-out path ---

func TestMQTTGetWithStatusNilOut(t *testing.T) {
	// 200 + nil out → decode step is skipped, returns (200, nil).
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	f := newMQTTTestFetcher(srv)
	code, err := f.getWithStatus(context.Background(), "/anything", nil)
	if err != nil {
		t.Fatal(err)
	}
	if code != http.StatusOK {
		t.Errorf("code = %d, want 200", code)
	}
}

func TestNewMQTTBridgeFetcher(t *testing.T) {
	f := NewMQTTBridgeFetcher("http://host:8080", "bridge-1", "secret")
	if f == nil {
		t.Fatal("expected non-nil fetcher")
	}
	if f.baseURL != "http://host:8080" {
		t.Errorf("baseURL = %q", f.baseURL)
	}
	if f.bearerToken != "secret" {
		t.Errorf("bearerToken = %q", f.bearerToken)
	}
}

// --- fetch (internal) ---

func TestMQTTFetchSuccess(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(MQTTReadyz{Status: "ready"})
	}))
	defer srv.Close()

	f := newMQTTTestFetcher(srv)
	r, err := f.FetchReadyz(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if r.Status != "ready" {
		t.Errorf("Status = %q, want ready", r.Status)
	}
}

func TestMQTTFetchNon200(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "forbidden", http.StatusForbidden)
	}))
	defer srv.Close()

	f := newMQTTTestFetcher(srv)
	_, err := f.FetchReadyz(context.Background())
	if err == nil {
		t.Error("expected error for 403")
	}
}

func TestMQTTFetchBadJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("not-json"))
	}))
	defer srv.Close()

	f := newMQTTTestFetcher(srv)
	_, err := f.FetchReadyz(context.Background())
	if err == nil {
		t.Error("expected error for bad JSON")
	}
}

func TestMQTTFetchWithBearerToken(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		json.NewEncoder(w).Encode(MQTTReadyz{Status: "ready"})
	}))
	defer srv.Close()

	f := NewMQTTBridgeFetcher(srv.URL, "b", "my-token")
	f.FetchReadyz(context.Background())
	if gotAuth != "Bearer my-token" {
		t.Errorf("Authorization header = %q, want Bearer my-token", gotAuth)
	}
}

// --- getWithStatus ---

func TestMQTTGetWithStatus200(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(MQTTCluster{LocalInstanceID: "inst-1"})
	}))
	defer srv.Close()

	f := newMQTTTestFetcher(srv)
	cluster, code, err := f.FetchCluster(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if code != http.StatusOK {
		t.Errorf("code = %d, want 200", code)
	}
	if cluster.LocalInstanceID != "inst-1" {
		t.Errorf("LocalInstanceID = %q", cluster.LocalInstanceID)
	}
}

func TestMQTTGetWithStatus409(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusConflict) // 409 = clustering not enabled
	}))
	defer srv.Close()

	f := newMQTTTestFetcher(srv)
	cluster, code, err := f.FetchCluster(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if code != http.StatusConflict {
		t.Errorf("code = %d, want 409", code)
	}
	if cluster != nil {
		t.Error("expected nil cluster for non-200")
	}
}

func TestMQTTGetWithStatusBadJSONDecodeError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("bad json"))
	}))
	defer srv.Close()

	f := newMQTTTestFetcher(srv)
	// getWithStatus called via FetchCluster: 200 + bad JSON → decode error
	_, _, err := f.FetchCluster(context.Background())
	if err == nil {
		t.Error("expected decode error")
	}
}

// --- FetchClusterInspect ---

func TestMQTTFetchClusterInspect(t *testing.T) {
	want := MQTTClusterInspect{InstanceID: "inst-1"}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("client_id") == "" {
			t.Error("expected client_id query param")
		}
		json.NewEncoder(w).Encode(want)
	}))
	defer srv.Close()

	f := newMQTTTestFetcher(srv)
	ins, code, err := f.FetchClusterInspect(context.Background(), "client-abc")
	if err != nil {
		t.Fatal(err)
	}
	if code != http.StatusOK {
		t.Errorf("code = %d, want 200", code)
	}
	if ins.InstanceID != want.InstanceID {
		t.Errorf("InstanceID = %q, want %q", ins.InstanceID, want.InstanceID)
	}
}

func TestMQTTFetchClusterInspect404(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	f := newMQTTTestFetcher(srv)
	ins, code, err := f.FetchClusterInspect(context.Background(), "missing")
	if err != nil {
		t.Fatal(err)
	}
	if code != http.StatusNotFound {
		t.Errorf("code = %d, want 404", code)
	}
	if ins != nil {
		t.Error("expected nil result for 404")
	}
}

// --- PostAdmin ---

func TestMQTTPostAdminWithBody(t *testing.T) {
	var gotBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %q, want POST", r.Method)
		}
		gotBody, _ = readAll(r.Body)
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	f := newMQTTTestFetcher(srv)
	code, body, err := f.PostAdmin(context.Background(), "/admin/drain", []byte(`{"timeout":30}`))
	if err != nil {
		t.Fatal(err)
	}
	if code != http.StatusOK {
		t.Errorf("code = %d, want 200", code)
	}
	if string(body) == "" {
		t.Error("expected non-empty response body")
	}
	if string(gotBody) == "" {
		t.Error("expected request body to be forwarded")
	}
}

func TestMQTTPostAdminNilBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	f := newMQTTTestFetcher(srv)
	code, _, err := f.PostAdmin(context.Background(), "/admin/action", nil)
	if err != nil {
		t.Fatal(err)
	}
	if code != http.StatusNoContent {
		t.Errorf("code = %d, want 204", code)
	}
}

func TestMQTTPostAdminWithBearerToken(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	f := NewMQTTBridgeFetcher(srv.URL, "b", "admin-token")
	f.PostAdmin(context.Background(), "/admin/test", nil)
	if gotAuth != "Bearer admin-token" {
		t.Errorf("Authorization = %q, want Bearer admin-token", gotAuth)
	}
}

// readAll reads all bytes from an io.Reader.
func readAll(r interface{ Read([]byte) (int, error) }) ([]byte, error) {
	buf := make([]byte, 0, 512)
	tmp := make([]byte, 256)
	for {
		n, err := r.Read(tmp)
		buf = append(buf, tmp[:n]...)
		if err != nil {
			break
		}
	}
	return buf, nil
}

// --- Individual fetch wrappers ---

func TestMQTTFetchConnz(t *testing.T) {
	want := MQTTConnz{Total: 42}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("limit") == "" {
			t.Error("expected limit param")
		}
		json.NewEncoder(w).Encode(want)
	}))
	defer srv.Close()

	f := newMQTTTestFetcher(srv)
	got, err := f.FetchConnz(context.Background(), 100, 0)
	if err != nil {
		t.Fatal(err)
	}
	if got.Total != want.Total {
		t.Errorf("Total = %d, want %d", got.Total, want.Total)
	}
}

func TestMQTTFetchConnzClient(t *testing.T) {
	var capturedPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedPath = r.URL.RawQuery
		json.NewEncoder(w).Encode(MQTTConnz{})
	}))
	defer srv.Close()

	f := newMQTTTestFetcher(srv)
	_, err := f.FetchConnzClient(context.Background(), "my-client")
	if err != nil {
		t.Fatal(err)
	}
	if !containsStr(capturedPath, "mqtt_client=my-client") {
		t.Errorf("query %q missing mqtt_client", capturedPath)
	}
}

func TestMQTTFetchDiagNATS(t *testing.T) {
	want := MQTTNATSDiag{Connection: MQTTNATSConnection{Connected: true}}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(want)
	}))
	defer srv.Close()

	f := newMQTTTestFetcher(srv)
	got, err := f.FetchDiagNATS(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !got.Connection.Connected {
		t.Error("expected Connected=true")
	}
}

func TestMQTTFetchDiag(t *testing.T) {
	want := MQTTDiag{ConfigPath: "/etc/machmqtt/config.yaml"}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(want)
	}))
	defer srv.Close()

	f := newMQTTTestFetcher(srv)
	got, err := f.FetchDiag(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got.ConfigPath != want.ConfigPath {
		t.Errorf("ConfigPath = %q", got.ConfigPath)
	}
}

func TestMQTTFetchLicense(t *testing.T) {
	want := MQTTLicense{Status: "active", MaxConnections: 1000}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(want)
	}))
	defer srv.Close()

	f := newMQTTTestFetcher(srv)
	got, err := f.FetchLicense(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != "active" {
		t.Errorf("Status = %q, want active", got.Status)
	}
}

func TestMQTTFetchPool(t *testing.T) {
	want := MQTTPool{Size: 4, Slots: []MQTTPoolSlot{{Index: 0, Connected: true}}}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(want)
	}))
	defer srv.Close()

	f := newMQTTTestFetcher(srv)
	got, err := f.FetchPool(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got.Size != 4 {
		t.Errorf("Size = %d, want 4", got.Size)
	}
}

// --- FetchMetrics ---

func TestMQTTFetchMetricsSuccess(t *testing.T) {
	promText := "machmqtt_connections_active 42\n"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.Write([]byte(promText))
	}))
	defer srv.Close()

	f := newMQTTTestFetcher(srv)
	got, err := f.FetchMetrics(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got == nil {
		t.Fatal("expected non-nil metrics")
	}
	if got.ConnectionsActive != 42 {
		t.Errorf("ConnectionsActive = %d, want 42", got.ConnectionsActive)
	}
}

func TestMQTTFetchMetricsNon200(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "not found", http.StatusNotFound)
	}))
	defer srv.Close()

	f := newMQTTTestFetcher(srv)
	_, err := f.FetchMetrics(context.Background())
	if err == nil {
		t.Error("expected error for 404")
	}
}

func TestMQTTFetchMetricsWithBearerToken(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.Write([]byte(""))
	}))
	defer srv.Close()

	f := NewMQTTBridgeFetcher(srv.URL, "b", "metrics-token")
	f.FetchMetrics(context.Background())
	if gotAuth != "Bearer metrics-token" {
		t.Errorf("Authorization = %q, want Bearer metrics-token", gotAuth)
	}
}

// --- FetchStatus ---

func TestMQTTFetchStatusSuccess(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/readyz", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(MQTTReadyz{Status: "ready", NATSConnected: true})
	})
	mux.HandleFunc("/diag/nats", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(MQTTNATSDiag{Connection: MQTTNATSConnection{Connected: true}})
	})
	mux.HandleFunc("/pool", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(MQTTPool{Size: 2})
	})
	mux.HandleFunc("/metrics", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("machmqtt_connections_active 3\n"))
	})
	mux.HandleFunc("/connz", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(MQTTConnz{Total: 10})
	})

	srv := httptest.NewServer(mux)
	defer srv.Close()

	f := newMQTTTestFetcher(srv)
	status := f.FetchStatus(context.Background())
	if status.Error != "" {
		t.Fatalf("unexpected error: %s", status.Error)
	}
	if !status.Ready {
		t.Error("expected Ready=true")
	}
	if !status.NATSConnected {
		t.Error("expected NATSConnected=true")
	}
	// Connections now comes from the metrics snapshot (connections_active=3),
	// not /readyz which carries no connection count.
	if status.Connections != 3 {
		t.Errorf("Connections = %d, want 3", status.Connections)
	}
	if status.NATS == nil {
		t.Error("expected non-nil NATS diag")
	}
	if status.Pool == nil {
		t.Error("expected non-nil Pool")
	}
	if status.Metrics == nil {
		t.Error("expected non-nil Metrics")
	}
	if !status.ConnzAvailable {
		t.Error("expected ConnzAvailable=true")
	}
}

func TestMQTTFetchStatusReadyzError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "internal error", http.StatusInternalServerError)
	}))
	defer srv.Close()

	f := newMQTTTestFetcher(srv)
	status := f.FetchStatus(context.Background())
	if status.Error == "" {
		t.Error("expected non-empty Error when readyz fails")
	}
}

func TestMQTTFetchStatusDrainingState(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/readyz", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(MQTTReadyz{Status: "draining", NATSConnected: true})
	})
	mux.HandleFunc("/diag/nats", func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "not found", http.StatusNotFound)
	})
	mux.HandleFunc("/pool", func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "not found", http.StatusNotFound)
	})
	mux.HandleFunc("/metrics", func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "not found", http.StatusNotFound)
	})
	mux.HandleFunc("/connz", func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "not found", http.StatusNotFound)
	})

	srv := httptest.NewServer(mux)
	defer srv.Close()

	f := newMQTTTestFetcher(srv)
	status := f.FetchStatus(context.Background())
	if status.Error != "" {
		t.Fatalf("unexpected error: %s", status.Error)
	}
	if !status.Draining {
		t.Error("expected Draining=true")
	}
	if status.Ready {
		t.Error("expected Ready=false for draining status")
	}
}

// --- /readyz non-200 states ---

// Verbatim MachMQTT v1.2 /readyz bodies. "ready" answers 200; every other state
// answers 503 with the state in the body.
const (
	readyzReady     = `{"status":"ready","nats_connected":true}`
	readyzDraining  = `{"status":"draining","nats_connected":true}`
	readyzJSDegrade = `{"status":"jetstream-degraded","nats_connected":true,"jetstream_ready":false}`
	readyzNotReady  = `{"status":"not ready","nats_connected":false}`
)

// mqttStatusMux serves every endpoint FetchStatus reads, with a caller-supplied
// /readyz response. The non-readyz endpoints always answer 200 so a non-ready
// state can be asserted without losing them — the whole point of decoding a 503
// readyz body is that the rest of the bridge data still populates.
func mqttStatusMux(readyzCode int, readyzBody string) *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("/readyz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(readyzCode)
		w.Write([]byte(readyzBody))
	})
	mux.HandleFunc("/diag/nats", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(MQTTNATSDiag{Connection: MQTTNATSConnection{Connected: true}})
	})
	mux.HandleFunc("/pool", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(MQTTPool{Size: 2})
	})
	mux.HandleFunc("/metrics", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("machmqtt_connections_active 3\n"))
	})
	mux.HandleFunc("/connz", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(MQTTConnz{Total: 10})
	})
	return mux
}

func TestMQTTFetchReadyzDecodes503Body(t *testing.T) {
	cases := []struct {
		name       string
		code       int
		body       string
		wantStatus string
		wantNATS   bool
	}{
		{"ready", http.StatusOK, readyzReady, "ready", true},
		{"draining", http.StatusServiceUnavailable, readyzDraining, "draining", true},
		{"jetstream degraded", http.StatusServiceUnavailable, readyzJSDegrade, "jetstream-degraded", true},
		{"not ready", http.StatusServiceUnavailable, readyzNotReady, "not ready", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(mqttStatusMux(tc.code, tc.body))
			defer srv.Close()

			r, err := newMQTTTestFetcher(srv).FetchReadyz(context.Background())
			if err != nil {
				t.Fatalf("unexpected error for %s: %v", tc.name, err)
			}
			if r.Status != tc.wantStatus {
				t.Errorf("Status = %q, want %q", r.Status, tc.wantStatus)
			}
			if r.NATSConnected != tc.wantNATS {
				t.Errorf("NATSConnected = %v, want %v", r.NATSConnected, tc.wantNATS)
			}
		})
	}
}

// The 503-with-body relaxation is scoped to /readyz: every other endpoint keeps
// the strict 200-only contract, so a 503 there is still a fetch error.
func TestMQTTFetchNonReadyzRejects503(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
		w.Write([]byte(`{"status":"unavailable"}`))
	}))
	defer srv.Close()

	f := newMQTTTestFetcher(srv)
	if _, err := f.FetchLicense(context.Background()); err == nil {
		t.Error("expected error for 503 on /license")
	}
	if _, err := f.FetchPool(context.Background()); err == nil {
		t.Error("expected error for 503 on /pool")
	}
}

// --- FetchStatus readiness-state mapping ---

func TestMQTTFetchStatusJetStreamDegraded(t *testing.T) {
	srv := httptest.NewServer(mqttStatusMux(http.StatusServiceUnavailable, readyzJSDegrade))
	defer srv.Close()

	status := newMQTTTestFetcher(srv).FetchStatus(context.Background())
	// A 503 readyz is an answer, not a failure: the bridge is reachable and the
	// rest of the status must still be filled in.
	if status.Error != "" {
		t.Fatalf("unexpected error: %s", status.Error)
	}
	if !status.JetStreamDegraded {
		t.Error("expected JetStreamDegraded=true")
	}
	if status.Ready {
		t.Error("expected Ready=false for jetstream-degraded status")
	}
	if status.Draining {
		t.Error("expected Draining=false for jetstream-degraded status")
	}
	if !status.NATSConnected {
		t.Error("expected NATSConnected=true (MQTT service is up)")
	}
	if !status.ConnzAvailable {
		t.Error("expected ConnzAvailable=true")
	}
	if status.Connections != 3 {
		t.Errorf("Connections = %d, want 3", status.Connections)
	}
	if status.NATS == nil || status.Pool == nil || status.Metrics == nil {
		t.Error("expected NATS, Pool and Metrics to be populated")
	}
}

func TestMQTTFetchStatusReadyzStateMapping(t *testing.T) {
	cases := []struct {
		name         string
		code         int
		body         string
		wantReady    bool
		wantDraining bool
		wantDegraded bool
	}{
		{"ready", http.StatusOK, readyzReady, true, false, false},
		{"draining", http.StatusServiceUnavailable, readyzDraining, false, true, false},
		{"jetstream degraded", http.StatusServiceUnavailable, readyzJSDegrade, false, false, true},
		{"not ready", http.StatusServiceUnavailable, readyzNotReady, false, false, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(mqttStatusMux(tc.code, tc.body))
			defer srv.Close()

			status := newMQTTTestFetcher(srv).FetchStatus(context.Background())
			if status.Error != "" {
				t.Fatalf("unexpected error: %s", status.Error)
			}
			if status.Ready != tc.wantReady {
				t.Errorf("Ready = %v, want %v", status.Ready, tc.wantReady)
			}
			if status.Draining != tc.wantDraining {
				t.Errorf("Draining = %v, want %v", status.Draining, tc.wantDraining)
			}
			if status.JetStreamDegraded != tc.wantDegraded {
				t.Errorf("JetStreamDegraded = %v, want %v", status.JetStreamDegraded, tc.wantDegraded)
			}
			// Every state above answered /readyz, so none of them is an error.
			if !status.ConnzAvailable {
				t.Error("expected ConnzAvailable=true")
			}
		})
	}
}

func TestMQTTFetchStatusConnectionRefused(t *testing.T) {
	srv := httptest.NewServer(mqttStatusMux(http.StatusOK, readyzReady))
	srv.Close() // nothing listening → transport error, the real unreachable case

	status := newMQTTTestFetcher(srv).FetchStatus(context.Background())
	if status.Error == "" {
		t.Fatal("expected non-empty Error when the bridge is not listening")
	}
	if status.Ready || status.Draining || status.JetStreamDegraded {
		t.Errorf("expected no readiness state for an unreachable bridge: %+v", status)
	}
	if status.ConnzAvailable {
		t.Error("expected ConnzAvailable=false for an unreachable bridge")
	}
}
