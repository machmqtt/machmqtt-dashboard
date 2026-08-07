package collector

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/noodlebit/machmqtt-dashboard/internal/config"
)

// newTestFetcher returns a Fetcher pointed at srv.URL and the server itself.
func newTestFetcher(t *testing.T, handler http.Handler) (*Fetcher, *httptest.Server) {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	f, err := NewFetcher(nil)
	if err != nil {
		t.Fatal(err)
	}
	return f, srv
}

func serveJSON(t *testing.T, v any) http.HandlerFunc {
	t.Helper()
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(v); err != nil {
			t.Errorf("encode test JSON: %v", err)
		}
	}
}

// --- NewFetcher ---

func TestNewFetcherNilTLS(t *testing.T) {
	f, err := NewFetcher(nil)
	if err != nil {
		t.Fatal(err)
	}
	if f == nil {
		t.Fatal("expected non-nil Fetcher")
	}
}

func TestNewFetcherInsecureTLS(t *testing.T) {
	f, err := NewFetcher(&config.TLSConfig{Insecure: true})
	if err != nil {
		t.Fatal(err)
	}
	if f == nil {
		t.Fatal("expected non-nil Fetcher with insecure TLS")
	}
}

func TestNewFetcherBadCAFile(t *testing.T) {
	_, err := NewFetcher(&config.TLSConfig{CAFile: "/nonexistent/ca.pem"})
	if err == nil {
		t.Error("expected error for missing CA file")
	}
}

// --- fetch error paths ---

func TestFetchNon200(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "internal error", http.StatusInternalServerError)
	}))
	t.Cleanup(srv.Close)

	f, _ := NewFetcher(nil)
	_, err := f.FetchVarz(context.Background(), srv.URL)
	if err == nil {
		t.Error("expected error for 500 response")
	}
}

func TestFetchBadJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("not json"))
	}))
	t.Cleanup(srv.Close)

	f, _ := NewFetcher(nil)
	_, err := f.FetchVarz(context.Background(), srv.URL)
	if err == nil {
		t.Error("expected error for bad JSON")
	}
}

func TestFetchUnreachableServer(t *testing.T) {
	f, _ := NewFetcher(nil)
	// Port 0 on loopback should refuse connections immediately.
	_, err := f.FetchVarz(context.Background(), "http://127.0.0.1:0")
	if err == nil {
		t.Error("expected error for unreachable server")
	}
}

// --- FetchVarz ---

func TestFetcherFetchVarz(t *testing.T) {
	want := Varz{ServerID: "srv-1", ServerName: "nats-1", Version: "2.10.0"}
	f, srv := newTestFetcher(t, serveJSON(t, want))

	got, err := f.FetchVarz(context.Background(), srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	if got.ServerID != want.ServerID {
		t.Errorf("ServerID = %q, want %q", got.ServerID, want.ServerID)
	}
	if got.ServerName != want.ServerName {
		t.Errorf("ServerName = %q, want %q", got.ServerName, want.ServerName)
	}
}

// --- FetchConnz ---

func TestFetcherFetchConnzNoParams(t *testing.T) {
	want := Connz{ServerID: "srv-1", NumConns: 5}
	f, srv := newTestFetcher(t, serveJSON(t, want))

	got, err := f.FetchConnz(context.Background(), srv.URL, 0, 0, "", "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if got.NumConns != want.NumConns {
		t.Errorf("NumConns = %d, want %d", got.NumConns, want.NumConns)
	}
}

func TestFetcherFetchConnzWithParams(t *testing.T) {
	var capturedQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedQuery = r.URL.RawQuery
		json.NewEncoder(w).Encode(Connz{ServerID: "s"})
	}))
	t.Cleanup(srv.Close)

	f, _ := NewFetcher(nil)
	f.FetchConnz(context.Background(), srv.URL, 100, 50, "cid", "acc1", "open", "foo.>")
	// All params should be present in the query string.
	for _, want := range []string{"limit=100", "offset=50", "sort=cid", "acc=acc1", "state=open", "filter_subject=foo"} {
		if !containsStr(capturedQuery, want) {
			t.Errorf("query %q missing %q", capturedQuery, want)
		}
	}
}

func containsStr(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(s) > 0 && func() bool {
		for i := 0; i <= len(s)-len(sub); i++ {
			if s[i:i+len(sub)] == sub {
				return true
			}
		}
		return false
	}())
}

// --- FetchConnzWithSubs ---

func TestFetcherFetchConnzWithSubs(t *testing.T) {
	var capturedQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedQuery = r.URL.RawQuery
		json.NewEncoder(w).Encode(Connz{ServerID: "s"})
	}))
	t.Cleanup(srv.Close)

	f, _ := NewFetcher(nil)
	_, err := f.FetchConnzWithSubs(context.Background(), srv.URL, 200)
	if err != nil {
		t.Fatal(err)
	}
	if !containsStr(capturedQuery, "subs=true") {
		t.Errorf("query %q missing subs=true", capturedQuery)
	}
	if !containsStr(capturedQuery, "limit=200") {
		t.Errorf("query %q missing limit=200", capturedQuery)
	}
}

func TestFetcherFetchConnzWithSubsNoLimit(t *testing.T) {
	f, srv := newTestFetcher(t, serveJSON(t, Connz{ServerID: "s"}))
	_, err := f.FetchConnzWithSubs(context.Background(), srv.URL, 0)
	if err != nil {
		t.Fatal(err)
	}
}

// --- FetchConnzWithSubsFiltered ---

func TestFetcherFetchConnzWithSubsFiltered(t *testing.T) {
	var capturedQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedQuery = r.URL.RawQuery
		json.NewEncoder(w).Encode(Connz{ServerID: "s"})
	}))
	t.Cleanup(srv.Close)

	f, _ := NewFetcher(nil)
	_, err := f.FetchConnzWithSubsFiltered(context.Background(), srv.URL, 50, "foo.bar")
	if err != nil {
		t.Fatal(err)
	}
	if !containsStr(capturedQuery, "filter_subject=foo.bar") {
		t.Errorf("query %q missing filter_subject", capturedQuery)
	}
}

// --- FetchConnzSubsDetail / FetchConnzSubsDetailFiltered ---

func TestFetcherFetchConnzSubsDetail(t *testing.T) {
	var capturedQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedQuery = r.URL.RawQuery
		json.NewEncoder(w).Encode(Connz{ServerID: "s"})
	}))
	t.Cleanup(srv.Close)

	f, _ := NewFetcher(nil)
	_, err := f.FetchConnzSubsDetail(context.Background(), srv.URL, 10)
	if err != nil {
		t.Fatal(err)
	}
	if !containsStr(capturedQuery, "subs=detail") {
		t.Errorf("query %q missing subs=detail", capturedQuery)
	}
}

func TestFetcherFetchConnzSubsDetailFiltered(t *testing.T) {
	var capturedQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedQuery = r.URL.RawQuery
		json.NewEncoder(w).Encode(Connz{ServerID: "s"})
	}))
	t.Cleanup(srv.Close)

	f, _ := NewFetcher(nil)
	_, err := f.FetchConnzSubsDetailFiltered(context.Background(), srv.URL, 10, "my.subject")
	if err != nil {
		t.Fatal(err)
	}
	if !containsStr(capturedQuery, "subs=detail") {
		t.Errorf("query %q missing subs=detail", capturedQuery)
	}
	if !containsStr(capturedQuery, "filter_subject=my.subject") {
		t.Errorf("query %q missing filter_subject", capturedQuery)
	}
}

func TestFetcherFetchConnzSubsDetailNon200(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "bad", http.StatusBadRequest)
	}))
	t.Cleanup(srv.Close)

	f, _ := NewFetcher(nil)
	_, err := f.FetchConnzSubsDetail(context.Background(), srv.URL, 0)
	if err == nil {
		t.Error("expected error for 400 response")
	}
}

func TestFetcherFetchConnzSubsDetailBadJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("not json"))
	}))
	t.Cleanup(srv.Close)

	f, _ := NewFetcher(nil)
	_, err := f.FetchConnzSubsDetail(context.Background(), srv.URL, 0)
	if err == nil {
		t.Error("expected error for bad JSON")
	}
}

// --- FetchRoutez ---

func TestFetcherFetchRoutez(t *testing.T) {
	want := Routez{ServerID: "srv-1", NumRoutes: 2}
	f, srv := newTestFetcher(t, serveJSON(t, want))

	got, err := f.FetchRoutez(context.Background(), srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	if got.NumRoutes != want.NumRoutes {
		t.Errorf("NumRoutes = %d, want %d", got.NumRoutes, want.NumRoutes)
	}
}

// --- FetchGatewayz ---

func TestFetcherFetchGatewayz(t *testing.T) {
	want := Gatewayz{ServerID: "srv-1", Name: "hub"}
	f, srv := newTestFetcher(t, serveJSON(t, want))

	got, err := f.FetchGatewayz(context.Background(), srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != want.Name {
		t.Errorf("Name = %q, want %q", got.Name, want.Name)
	}
}

// --- FetchLeafz ---

func TestFetcherFetchLeafz(t *testing.T) {
	want := Leafz{ServerID: "srv-1", NumLeafs: 3}
	f, srv := newTestFetcher(t, serveJSON(t, want))

	got, err := f.FetchLeafz(context.Background(), srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	if got.NumLeafs != want.NumLeafs {
		t.Errorf("NumLeafs = %d, want %d", got.NumLeafs, want.NumLeafs)
	}
}

// --- FetchSubsz ---

func TestFetcherFetchSubsz(t *testing.T) {
	want := SubszResp{ServerID: "srv-1", NumSubs: 42}
	f, srv := newTestFetcher(t, serveJSON(t, want))

	got, err := f.FetchSubsz(context.Background(), srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	if got.NumSubs != want.NumSubs {
		t.Errorf("NumSubs = %d, want %d", got.NumSubs, want.NumSubs)
	}
}

// --- FetchJSInfo ---

func TestFetcherFetchJSInfo(t *testing.T) {
	var capturedQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedQuery = r.URL.RawQuery
		json.NewEncoder(w).Encode(JSInfo{ServerID: "srv-1"})
	}))
	t.Cleanup(srv.Close)

	f, _ := NewFetcher(nil)
	got, err := f.FetchJSInfo(context.Background(), srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	if got.ServerID != "srv-1" {
		t.Errorf("ServerID = %q, want srv-1", got.ServerID)
	}
	// All three are required: streams+consumers for the detail, and config for the
	// stream/consumer config blocks (storage, replicas, limits, policies). Without
	// config=true nats-server returns null config and the JetStream page can't show
	// stream settings, consumer policies, or detect replica count for dedupe.
	for _, want := range []string{"streams=true", "consumers=true", "config=true"} {
		if !containsStr(capturedQuery, want) {
			t.Errorf("query %q missing %s", capturedQuery, want)
		}
	}
}

// --- FetchAccountz ---

func TestFetcherFetchAccountz(t *testing.T) {
	f, srv := newTestFetcher(t, serveJSON(t, Accountz{}))
	_, err := f.FetchAccountz(context.Background(), srv.URL)
	if err != nil {
		t.Fatal(err)
	}
}

// --- FetchAccountDetail ---

func TestFetcherFetchAccountDetail(t *testing.T) {
	var capturedQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedQuery = r.URL.RawQuery
		json.NewEncoder(w).Encode(Accountz{})
	}))
	t.Cleanup(srv.Close)

	f, _ := NewFetcher(nil)
	_, err := f.FetchAccountDetail(context.Background(), srv.URL, "my-account")
	if err != nil {
		t.Fatal(err)
	}
	if !containsStr(capturedQuery, "acc=my-account") {
		t.Errorf("query %q missing acc=my-account", capturedQuery)
	}
}

// --- FetchHealthz ---

func TestFetcherFetchHealthz(t *testing.T) {
	want := HealthStatus{Status: "ok"}
	f, srv := newTestFetcher(t, serveJSON(t, want))

	got, err := f.FetchHealthz(context.Background(), srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != want.Status {
		t.Errorf("Status = %q, want %q", got.Status, want.Status)
	}
}
