package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHandleHealthzOK(t *testing.T) {
	srv, _, _, _ := polledServer(t, natsMockConfig{})
	// /healthz is unauthenticated.
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, httptest.NewRequest("GET", "/healthz", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	if decodeJSON(t, w)["status"] != "ok" {
		t.Errorf("status = %v, want ok", decodeJSON(t, w)["status"])
	}
}

func TestHandleHealthzDBDown(t *testing.T) {
	srv, s, _, _ := polledServer(t, natsMockConfig{})
	s.Close() // Ping fails
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, httptest.NewRequest("GET", "/healthz", nil))
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", w.Code)
	}
	if decodeJSON(t, w)["status"] != "db_unavailable" {
		t.Errorf("status = %v, want db_unavailable", decodeJSON(t, w)["status"])
	}
}

func TestHandleAdminHealthOK(t *testing.T) {
	srv, _, token, _ := polledServer(t, natsMockConfig{}, withMetrics())
	w := do(t, srv, "GET", "/api/admin/health", token, "")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	m := decodeJSON(t, w)
	if m["status"] != "ok" {
		t.Errorf("status = %v, want ok (healthy cluster)", m["status"])
	}
	if _, ok := m["clusters"].([]any); !ok {
		t.Errorf("clusters missing/not array: %v", m["clusters"])
	}
	if _, ok := m["ws_clients"]; !ok {
		t.Errorf("ws_clients missing")
	}
	if _, ok := m["dropped_samples"]; !ok {
		t.Errorf("dropped_samples missing (metrics wired)")
	}
}

func TestHandleHealthzDBDownEncodeError(t *testing.T) {
	// Drive the encode-failure branch inside the 503 path by handing handleHealthz
	// a writer whose Write always fails.
	srv, s, _, _ := polledServer(t, natsMockConfig{})
	s.Close()
	fw := &failWriter{ResponseRecorder: httptest.NewRecorder()}
	srv.handleHealthz(fw, httptest.NewRequest("GET", "/healthz", nil))
	if fw.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", fw.Code)
	}
}

func TestHandleAdminHealthDegraded(t *testing.T) {
	// A cluster with a NATS push connection that can't connect reports Degraded,
	// so the overall status is "degraded".
	srv, _, token, _ := polledServer(t, natsMockConfig{}, withDeadNATSConn())
	w := do(t, srv, "GET", "/api/admin/health", token, "")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	if got := decodeJSON(t, w)["status"]; got != "degraded" {
		t.Errorf("status = %v, want degraded", got)
	}
}
