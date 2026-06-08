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
		json.NewEncoder(w).Encode(MQTTReadyz{Status: "ready", Connections: 5, NATSConnected: true})
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
	if status.Connections != 5 {
		t.Errorf("Connections = %d, want 5", status.Connections)
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
		json.NewEncoder(w).Encode(MQTTReadyz{Status: "draining", Connections: 1, NATSConnected: true})
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
