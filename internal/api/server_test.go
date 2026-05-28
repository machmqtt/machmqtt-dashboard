package api

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/noodlebit/nats-dashboard/internal/auth"
	"github.com/noodlebit/nats-dashboard/internal/collector"
	"github.com/noodlebit/nats-dashboard/internal/config"
	"github.com/noodlebit/nats-dashboard/internal/store"
	"github.com/noodlebit/nats-dashboard/internal/ws"
)

func setupTestServer(t *testing.T) (*Server, *auth.Auth, string) {
	t.Helper()
	s, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })

	u, _ := s.CreateUser("admin", "pass", store.RoleAdmin)
	a := auth.New(s, "test-secret", false)
	token, _ := a.IssueToken(u)
	log := slog.New(slog.NewTextHandler(nil, nil))

	cfg := &config.Config{
		PollInterval: 5e9,
		Environments: []config.Environment{
			{Name: "test", Servers: []config.Server{{URL: "http://localhost:9999"}}},
		},
	}
	hub := ws.NewHub(log)
	mgr, _ := collector.NewManager(cfg, nil, log, s)

	srv := NewServer(a, mgr, hub, log, "test", cfg, nil, s)
	return srv, a, token
}

func authedReq(method, path, token string, body string) *http.Request {
	var r *http.Request
	if body != "" {
		r = httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
	} else {
		r = httptest.NewRequest(method, path, nil)
	}
	r.AddCookie(&http.Cookie{Name: "session", Value: token})
	return r
}

func TestLoginEndpoint(t *testing.T) {
	srv, _, _ := setupTestServer(t)

	req := httptest.NewRequest("POST", "/api/login", strings.NewReader(`{"username":"admin","password":"pass"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("login status = %d, want 200", w.Code)
	}

	// Check session cookie was set.
	cookies := w.Result().Cookies()
	found := false
	for _, c := range cookies {
		if c.Name == "session" && c.Value != "" {
			found = true
		}
	}
	if !found {
		t.Error("session cookie not set")
	}
}

func TestLoginBadCredentials(t *testing.T) {
	srv, _, _ := setupTestServer(t)

	req := httptest.NewRequest("POST", "/api/login", strings.NewReader(`{"username":"admin","password":"wrong"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", w.Code)
	}
}

func TestEnvironmentsEndpoint(t *testing.T) {
	srv, _, token := setupTestServer(t)

	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, authedReq("GET", "/api/environments", token, ""))

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}

	var resp map[string][]string
	json.NewDecoder(w.Body).Decode(&resp)
	envs := resp["environments"]
	if len(envs) != 1 || envs[0] != "test" {
		t.Errorf("environments = %v, want [test]", envs)
	}
}

func TestUnauthenticatedRequest(t *testing.T) {
	srv, _, _ := setupTestServer(t)

	req := httptest.NewRequest("GET", "/api/environments", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", w.Code)
	}
}

func TestOverviewNotFound(t *testing.T) {
	srv, _, token := setupTestServer(t)

	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, authedReq("GET", "/api/environments/nonexistent/overview", token, ""))

	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", w.Code)
	}
}

func TestMeEndpoint(t *testing.T) {
	srv, _, token := setupTestServer(t)

	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, authedReq("GET", "/api/me", token, ""))

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}

	var user map[string]any
	json.NewDecoder(w.Body).Decode(&user)
	if user["username"] != "admin" {
		t.Errorf("username = %v, want admin", user["username"])
	}
	if user["role"] != "admin" {
		t.Errorf("role = %v, want admin", user["role"])
	}
}

func TestAdminListUsers(t *testing.T) {
	srv, _, token := setupTestServer(t)

	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, authedReq("GET", "/api/admin/users", token, ""))

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}

	var resp map[string][]map[string]any
	json.NewDecoder(w.Body).Decode(&resp)
	users := resp["users"]
	if len(users) != 1 {
		t.Fatalf("users = %d, want 1", len(users))
	}
	if users[0]["username"] != "admin" {
		t.Errorf("username = %v, want admin", users[0]["username"])
	}
}

func TestAdminCreateUser(t *testing.T) {
	srv, _, token := setupTestServer(t)

	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, authedReq("POST", "/api/admin/users", token,
		`{"username":"newuser","password":"pass123","role":"viewer"}`))

	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201, body: %s", w.Code, w.Body.String())
	}

	var user map[string]any
	json.NewDecoder(w.Body).Decode(&user)
	if user["username"] != "newuser" {
		t.Errorf("username = %v, want newuser", user["username"])
	}
	if user["role"] != "viewer" {
		t.Errorf("role = %v, want viewer", user["role"])
	}
}

func TestAdminDeleteUser(t *testing.T) {
	srv, a, token := setupTestServer(t)

	// Create a user to delete.
	u, _ := a.Store().CreateUser("victim", "pass", "viewer")

	w := httptest.NewRecorder()
	path := fmt.Sprintf("/api/admin/users/%d", u.ID)
	srv.Handler().ServeHTTP(w, authedReq("DELETE", path, token, ""))

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body: %s", w.Code, w.Body.String())
	}
}

func TestAdminCannotDeleteSelf(t *testing.T) {
	srv, _, token := setupTestServer(t)

	// Admin user is ID 1 (first created).
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, authedReq("DELETE", "/api/admin/users/1", token, ""))

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
}

func TestViewerCannotAccessAdmin(t *testing.T) {
	srv, a, _ := setupTestServer(t)

	// Create a viewer and get their token.
	viewer, _ := a.Store().CreateUser("viewer", "pass", "viewer")
	viewerToken, _ := a.IssueToken(viewer)

	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, authedReq("GET", "/api/admin/users", viewerToken, ""))

	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", w.Code)
	}
}

func TestDefaultAdminMustChangePassword(t *testing.T) {
	// Use EnsureDefaultAdmin (the real startup path) instead of CreateUser.
	s, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })

	_, err = s.EnsureDefaultAdmin()
	if err != nil {
		t.Fatal(err)
	}

	a := auth.New(s, "test-secret", false)
	log := slog.New(slog.NewTextHandler(nil, nil))
	cfg := &config.Config{
		PollInterval: 5e9,
		Environments: []config.Environment{
			{Name: "test", Servers: []config.Server{{URL: "http://localhost:9999"}}},
		},
	}
	hub := ws.NewHub(log)
	mgr, _ := collector.NewManager(cfg, nil, log, s)
	srv := NewServer(a, mgr, hub, log, "test", cfg, nil, s)

	// Login as default admin.
	req := httptest.NewRequest("POST", "/api/login", strings.NewReader(`{"username":"admin","password":"admin"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("login status = %d, want 200, body: %s", w.Code, w.Body.String())
	}

	var loginResp map[string]any
	json.NewDecoder(w.Body).Decode(&loginResp)

	mcp, ok := loginResp["must_change_password"]
	if !ok {
		t.Fatalf("login response missing must_change_password field, got: %v", loginResp)
	}
	if mcp != true {
		t.Errorf("login must_change_password = %v, want true", mcp)
	}

	// Also check /api/me returns the flag.
	cookies := w.Result().Cookies()
	meReq := httptest.NewRequest("GET", "/api/me", nil)
	for _, c := range cookies {
		meReq.AddCookie(c)
	}
	w2 := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w2, meReq)

	if w2.Code != http.StatusOK {
		t.Fatalf("me status = %d, want 200", w2.Code)
	}

	var meResp map[string]any
	json.NewDecoder(w2.Body).Decode(&meResp)
	if meResp["must_change_password"] != true {
		t.Errorf("me must_change_password = %v, want true", meResp["must_change_password"])
	}
}
