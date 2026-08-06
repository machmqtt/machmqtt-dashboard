package api

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/noodlebit/machmqtt-dashboard/internal/auth"
	"github.com/noodlebit/machmqtt-dashboard/internal/collector"
	"github.com/noodlebit/machmqtt-dashboard/internal/config"
	"github.com/noodlebit/machmqtt-dashboard/internal/store"
	"github.com/noodlebit/machmqtt-dashboard/internal/ws"
)

// setupDefaultAdminServer builds a full API server whose only user is the
// break-glass admin, provisioned with an explicit secret and marked for rotation.
func setupDefaultAdminServer(t *testing.T) *Server {
	t.Helper()
	s, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	if _, err := s.EnsureBreakGlassAdmin(testBootstrapPassword); err != nil {
		t.Fatal(err)
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	a := auth.New(s, "test-secret-please-ignore", false)
	t.Cleanup(a.Close)
	cfg := &config.Config{PollInterval: 5e9}
	hub := ws.NewHub(log)
	mgr, _ := collector.NewManager(cfg, nil, log, s)
	return NewServer(a, mgr, hub, log, "test", cfg, nil, s, nil)
}

// sessionCookie returns the value of the "session" cookie the response set, or
// "" if it was cleared (MaxAge < 0) or absent.
func sessionCookie(w *httptest.ResponseRecorder) string {
	for _, c := range w.Result().Cookies() {
		if c.Name == "session" {
			if c.MaxAge < 0 {
				return ""
			}
			return c.Value
		}
	}
	return ""
}

func reqWithSession(method, path, session, body string) *http.Request {
	var r *http.Request
	if body != "" {
		r = httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
	} else {
		r = httptest.NewRequest(method, path, nil)
	}
	if session != "" {
		r.AddCookie(&http.Cookie{Name: "session", Value: session})
	}
	return r
}

// TestAuthFlowForcedChangeAndRevocation exercises the whole session lifecycle
// through the real middleware chain: bootstrap admin must change its password
// before it can use the API, the change re-issues the session cookie, the
// pre-change cookie is revoked, and logout invalidates the session everywhere.
func TestAuthFlowForcedChangeAndRevocation(t *testing.T) {
	srv := setupDefaultAdminServer(t)
	h := srv.Handler()

	// 1. Log in as the bootstrap admin.
	w := httptest.NewRecorder()
	h.ServeHTTP(w, reqWithSession("POST", "/api/login", "", `{"username":"admin","password":"bootstrap-password"}`))
	if w.Code != http.StatusOK {
		t.Fatalf("login status = %d, want 200", w.Code)
	}
	oldSession := sessionCookie(w)
	if oldSession == "" {
		t.Fatal("login did not set a session cookie")
	}

	// 2. While must_change_password is set, a normal endpoint is blocked...
	w = httptest.NewRecorder()
	h.ServeHTTP(w, reqWithSession("GET", "/api/environments", oldSession, ""))
	if w.Code != http.StatusForbidden {
		t.Fatalf("pre-change /api/environments status = %d, want 403", w.Code)
	}

	// ...but /api/me is allowed so the UI can discover the user + the flag.
	w = httptest.NewRecorder()
	h.ServeHTTP(w, reqWithSession("GET", "/api/me", oldSession, ""))
	if w.Code != http.StatusOK {
		t.Fatalf("pre-change /api/me status = %d, want 200", w.Code)
	}

	// 3. Change the password. This clears must_change and re-issues the cookie.
	w = httptest.NewRecorder()
	h.ServeHTTP(w, reqWithSession("PUT", "/api/users/1/password", oldSession,
		`{"old_password":"bootstrap-password","new_password":"newpassword123"}`))
	if w.Code != http.StatusOK {
		t.Fatalf("password change status = %d, want 200", w.Code)
	}
	newSession := sessionCookie(w)
	if newSession == "" || newSession == oldSession {
		t.Fatalf("password change did not re-issue a fresh session cookie")
	}

	// 4. The pre-change cookie is now revoked (token_version bumped).
	w = httptest.NewRecorder()
	h.ServeHTTP(w, reqWithSession("GET", "/api/environments", oldSession, ""))
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("stale-cookie /api/environments status = %d, want 401 (revoked)", w.Code)
	}

	// 5. The re-issued cookie works and is no longer blocked by must_change.
	w = httptest.NewRecorder()
	h.ServeHTTP(w, reqWithSession("GET", "/api/environments", newSession, ""))
	if w.Code != http.StatusOK {
		t.Fatalf("post-change /api/environments status = %d, want 200", w.Code)
	}

	// 6. Logout invalidates the session; the same cookie no longer authenticates.
	w = httptest.NewRecorder()
	h.ServeHTTP(w, reqWithSession("POST", "/api/logout", newSession, ""))
	if w.Code != http.StatusOK {
		t.Fatalf("logout status = %d, want 200", w.Code)
	}
	w = httptest.NewRecorder()
	h.ServeHTTP(w, reqWithSession("GET", "/api/environments", newSession, ""))
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("post-logout /api/environments status = %d, want 401", w.Code)
	}
}

// TestAuthFlowWeakPasswordRejected verifies the password policy is enforced at
// the change endpoint (not just at user creation).
func TestAuthFlowWeakPasswordRejected(t *testing.T) {
	srv := setupDefaultAdminServer(t)
	h := srv.Handler()

	w := httptest.NewRecorder()
	h.ServeHTTP(w, reqWithSession("POST", "/api/login", "", `{"username":"admin","password":"bootstrap-password"}`))
	session := sessionCookie(w)

	w = httptest.NewRecorder()
	h.ServeHTTP(w, reqWithSession("PUT", "/api/users/1/password", session,
		`{"old_password":"bootstrap-password","new_password":"short"}`))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("weak password status = %d, want 400", w.Code)
	}
}
