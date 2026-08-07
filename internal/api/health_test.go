package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHealthzOK(t *testing.T) {
	srv, _, _, _ := setupTestServerWithStore(t)

	w := httptest.NewRecorder()
	// /healthz is public — no auth cookie.
	srv.Handler().ServeHTTP(w, httptest.NewRequest("GET", "/healthz", nil))

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	var resp map[string]string
	json.NewDecoder(w.Body).Decode(&resp)
	if resp["status"] != "ok" {
		t.Errorf("status = %q, want ok", resp["status"])
	}
}

func TestHealthzDBDown(t *testing.T) {
	srv, st, _, _ := setupTestServerWithStore(t)
	st.Close() // close the DB so Ping fails (t.Cleanup's second Close is harmless)

	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, httptest.NewRequest("GET", "/healthz", nil))

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503 when DB is down", w.Code)
	}
}

func TestAdminHealthShape(t *testing.T) {
	srv, _, _, token := setupTestServerWithStore(t)

	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, authedReq("GET", "/api/admin/health", token, ""))

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body: %s", w.Code, w.Body.String())
	}
	var resp map[string]any
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	for _, k := range []string{"status", "ws_clients", "dropped_samples", "clusters"} {
		if _, ok := resp[k]; !ok {
			t.Errorf("response missing key %q: %v", k, resp)
		}
	}
}

func TestAdminHealthRequiresAdmin(t *testing.T) {
	srv, _, a, _ := setupTestServerWithStore(t)
	viewer, _ := a.Store().CreateUser("viewer", "pass", "viewer")
	viewerToken, _ := a.IssueToken(viewer)

	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, authedReq("GET", "/api/admin/health", viewerToken, ""))

	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 for a viewer", w.Code)
	}
}
