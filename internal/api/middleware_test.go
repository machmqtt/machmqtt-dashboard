package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSecurityHeadersCSPRestrictsWebSocketOrigin(t *testing.T) {
	w := httptest.NewRecorder()
	securityHeaders(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})).
		ServeHTTP(w, httptest.NewRequest("GET", "/", nil))

	csp := w.Header().Get("Content-Security-Policy")
	if csp == "" {
		t.Fatal("Content-Security-Policy header missing")
	}
	// A bare ws:/wss: scheme source in connect-src authorizes a WebSocket to any
	// host; the UI only ever connects to its own origin, which 'self' covers.
	if strings.Contains(csp, "ws:") {
		t.Errorf("connect-src still allows any WebSocket host: %q", csp)
	}
	if !strings.Contains(csp, "connect-src 'self'") {
		t.Errorf("connect-src 'self' missing from CSP: %q", csp)
	}
	for _, want := range []string{"default-src 'self'", "script-src 'self'", "font-src 'self'"} {
		if !strings.Contains(csp, want) {
			t.Errorf("CSP missing %q: %q", want, csp)
		}
	}
}

func TestSecurityHeadersBaseline(t *testing.T) {
	w := httptest.NewRecorder()
	securityHeaders(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})).
		ServeHTTP(w, httptest.NewRequest("GET", "/", nil))

	for header, want := range map[string]string{
		"X-Content-Type-Options": "nosniff",
		"X-Frame-Options":        "DENY",
		"Referrer-Policy":        "strict-origin-when-cross-origin",
	} {
		if got := w.Header().Get(header); got != want {
			t.Errorf("%s = %q, want %q", header, got, want)
		}
	}
}
