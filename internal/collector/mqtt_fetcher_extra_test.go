package collector

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// badURLFetcher returns a fetcher whose baseURL contains a control character so
// that http.NewRequestWithContext fails at URL parsing before any network I/O.
func badURLFetcher(token string) *MQTTBridgeFetcher {
	return NewMQTTBridgeFetcher("http://\x7f", "bad", token)
}

// TestMQTTFetchRequestBuildError verifies fetch returns the request-build error
// when the URL cannot be parsed (control char), without reaching client.Do.
// Covers the NewRequestWithContext error branch in fetch (mqtt_fetcher.go L49-51).
func TestMQTTFetchRequestBuildError(t *testing.T) {
	f := badURLFetcher("")
	_, err := f.FetchReadyz(context.Background())
	if err == nil {
		t.Fatal("expected request-build error for malformed URL")
	}
}

// TestMQTTGetWithStatusRequestBuildError verifies getWithStatus returns (0, err)
// when the request cannot be built. Covers the NewRequestWithContext error
// branch in getWithStatus (mqtt_fetcher.go L79-81).
func TestMQTTGetWithStatusRequestBuildError(t *testing.T) {
	f := badURLFetcher("")
	_, code, err := f.FetchCluster(context.Background())
	if err == nil {
		t.Fatal("expected request-build error for malformed URL")
	}
	if code != 0 {
		t.Errorf("code = %d, want 0 on request-build error", code)
	}
}

// TestMQTTGetWithStatusSetsBearerToken verifies getWithStatus attaches the
// Authorization header when a bearer token is configured. Covers the
// bearerToken-present branch in getWithStatus (mqtt_fetcher.go L82-84).
func TestMQTTGetWithStatusSetsBearerToken(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		json.NewEncoder(w).Encode(MQTTCluster{LocalInstanceID: "inst-1"})
	}))
	defer srv.Close()

	f := NewMQTTBridgeFetcher(srv.URL, "b", "cluster-token")
	_, code, err := f.FetchCluster(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if code != http.StatusOK {
		t.Errorf("code = %d, want 200", code)
	}
	if gotAuth != "Bearer cluster-token" {
		t.Errorf("Authorization = %q, want Bearer cluster-token", gotAuth)
	}
}

// TestMQTTPostAdminRequestBuildError verifies PostAdmin returns (0, nil, err)
// when the request cannot be built. Covers the NewRequestWithContext error
// branch in PostAdmin (mqtt_fetcher.go L117-119).
func TestMQTTPostAdminRequestBuildError(t *testing.T) {
	f := badURLFetcher("")
	code, body, err := f.PostAdmin(context.Background(), "/admin/x", []byte(`{}`))
	if err == nil {
		t.Fatal("expected request-build error for malformed URL")
	}
	if code != 0 {
		t.Errorf("code = %d, want 0 on request-build error", code)
	}
	if body != nil {
		t.Errorf("body = %v, want nil on request-build error", body)
	}
}

// TestMQTTFetchMetricsRequestBuildError verifies FetchMetrics returns the
// request-build error when the URL cannot be parsed. Covers the
// NewRequestWithContext error branch in FetchMetrics (mqtt_fetcher.go L201-203).
func TestMQTTFetchMetricsRequestBuildError(t *testing.T) {
	f := badURLFetcher("")
	_, err := f.FetchMetrics(context.Background())
	if err == nil {
		t.Fatal("expected request-build error for malformed URL")
	}
}

// TestMQTTFetchMetricsBodyReadError verifies FetchMetrics surfaces a body-read
// error: the server sends a 200 with a Content-Length larger than the bytes it
// actually writes, so io.ReadAll hits an unexpected EOF. Covers the io.ReadAll
// error branch in FetchMetrics (mqtt_fetcher.go L219-221).
func TestMQTTFetchMetricsBodyReadError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hj, ok := w.(http.Hijacker)
		if !ok {
			t.Fatal("response writer does not support hijacking")
		}
		conn, _, err := hj.Hijack()
		if err != nil {
			t.Fatalf("hijack: %v", err)
		}
		// Promise 50 bytes but send only 5, then close → client reads 200 OK
		// (passing the status check) but io.ReadAll fails with unexpected EOF.
		conn.Write([]byte("HTTP/1.1 200 OK\r\nContent-Length: 50\r\n\r\nshort"))
		conn.Close()
	}))
	defer srv.Close()

	f := newMQTTTestFetcher(srv)
	_, err := f.FetchMetrics(context.Background())
	if err == nil {
		t.Fatal("expected body-read error from truncated response")
	}
}
