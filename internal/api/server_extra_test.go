package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gorilla/websocket"
)

func TestCheckSameOrigin(t *testing.T) {
	mk := func(origin, host string) *http.Request {
		r := httptest.NewRequest("GET", "/", nil)
		r.Host = host
		if origin != "" {
			r.Header["Origin"] = []string{origin}
		}
		return r
	}
	cases := []struct {
		name   string
		origin string
		host   string
		want   bool
	}{
		{"no origin (non-browser)", "", "example.com", true},
		{"matching origin", "http://example.com", "example.com", true},
		{"matching case-insensitive", "http://EXAMPLE.com", "example.com", true},
		{"mismatched origin", "http://evil.com", "example.com", false},
		{"unparseable origin", ":bad", "example.com", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := checkSameOrigin(mk(tc.origin, tc.host)); got != tc.want {
				t.Errorf("checkSameOrigin(%q, host=%q) = %v, want %v", tc.origin, tc.host, got, tc.want)
			}
		})
	}
}

func TestServeSPA(t *testing.T) {
	srv, _, _, _ := polledServer(t, natsMockConfig{})

	t.Run("root serves index", func(t *testing.T) {
		w := httptest.NewRecorder()
		srv.Handler().ServeHTTP(w, httptest.NewRequest("GET", "/", nil))
		if w.Code != http.StatusOK {
			t.Fatalf("status = %d", w.Code)
		}
		if !strings.Contains(strings.ToLower(w.Body.String()), "<!doctype html") &&
			!strings.Contains(strings.ToLower(w.Body.String()), "<html") {
			t.Errorf("root did not return HTML: %.80q", w.Body.String())
		}
	})

	t.Run("static asset served", func(t *testing.T) {
		w := httptest.NewRecorder()
		srv.Handler().ServeHTTP(w, httptest.NewRequest("GET", "/favicon.svg", nil))
		if w.Code != http.StatusOK {
			t.Errorf("favicon status = %d, want 200", w.Code)
		}
	})

	t.Run("deep links serve the app shell directly (no redirect)", func(t *testing.T) {
		// A client-side route must return the index HTML with 200 — never a 301.
		// The old fallback rewrote the path to /index.html and let http.FileServer
		// canonicalize it to "./", which resolved against the original deep path:
		// /subscriptions 301'd to / (losing the page on reload) and nested paths
		// like /mqtt/<id>/detail 301'd to /mqtt/<id>/ and redirect-looped.
		for _, path := range []string{"/subscriptions", "/mqtt/edge-broker-1/detail", "/clusters/abc/topology"} {
			w := httptest.NewRecorder()
			srv.Handler().ServeHTTP(w, httptest.NewRequest("GET", path, nil))
			if w.Code != http.StatusOK {
				t.Fatalf("deep link %q status = %d, want 200 (no redirect)", path, w.Code)
			}
			if body := strings.ToLower(w.Body.String()); !strings.Contains(body, "<!doctype html") && !strings.Contains(body, "<html") {
				t.Errorf("deep link %q did not return the app shell HTML: %.80q", path, w.Body.String())
			}
		}
	})
}

func TestHandleWSUpgrade(t *testing.T) {
	srv, _, token, _ := polledServer(t, natsMockConfig{})
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	url := "ws" + strings.TrimPrefix(ts.URL, "http") + "/api/ws"
	header := http.Header{}
	header.Set("Cookie", "session="+token)
	conn, resp, err := websocket.DefaultDialer.Dial(url, header)
	if err != nil {
		t.Fatalf("ws dial failed: %v (status %v)", err, resp.Status)
	}
	t.Cleanup(func() { conn.Close() })

	// The upgrade succeeded; exercise a round-trip subscribe so the client's
	// read pump runs.
	if err := conn.WriteMessage(websocket.TextMessage, []byte(`{"subscribe":"x"}`)); err != nil {
		t.Fatalf("write subscribe: %v", err)
	}
}

func TestServeSPAEmbedError(t *testing.T) {
	// An invalid embed sub-path makes fs.Sub fail; serveSPA logs and returns
	// without registering the SPA route.
	orig := spaDistDir
	spaDistDir = "" // not a valid fs path → fs.Sub returns an error
	t.Cleanup(func() { spaDistDir = orig })

	s := &Server{mux: http.NewServeMux(), log: discardLogger()}
	s.serveSPA()

	w := httptest.NewRecorder()
	s.mux.ServeHTTP(w, httptest.NewRequest("GET", "/", nil))
	if w.Code != http.StatusNotFound {
		t.Errorf("root status = %d, want 404 (route not registered after fs.Sub error)", w.Code)
	}
}

func TestHandleWSUpgradeFailure(t *testing.T) {
	// An authenticated but non-WebSocket GET reaches handleWS and fails the
	// upgrade, exercising the upgrade-error branch.
	srv, _, token, _ := polledServer(t, natsMockConfig{})
	w := do(t, srv, "GET", "/api/ws", token, "")
	if w.Code != http.StatusBadRequest {
		t.Fatalf("non-websocket GET to /api/ws = %d, want 400 (failed upgrade)", w.Code)
	}
}

func TestHandleWSRejectsUnauthenticated(t *testing.T) {
	srv, _, _, _ := polledServer(t, natsMockConfig{})
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	url := "ws" + strings.TrimPrefix(ts.URL, "http") + "/api/ws"
	_, resp, err := websocket.DefaultDialer.Dial(url, nil)
	if err == nil {
		t.Fatal("expected ws dial to fail without auth")
	}
	if resp == nil || resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status = %v, want 401", resp)
	}
}
