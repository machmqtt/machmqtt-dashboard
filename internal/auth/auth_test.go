package auth

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/noodlebit/nats-dashboard/internal/store"
)

func testAuth(t *testing.T) (*Auth, *store.Store) {
	t.Helper()
	s, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return New(s, "test-secret-key", false), s
}

func TestIssueAndValidate(t *testing.T) {
	a, s := testAuth(t)
	u, _ := s.CreateUser("testuser", "pass", store.RoleViewer)

	token, err := a.IssueToken(u)
	if err != nil {
		t.Fatal(err)
	}

	claims, err := a.ValidateToken(token)
	if err != nil {
		t.Fatal(err)
	}
	if claims.UserID != u.ID {
		t.Errorf("userID = %d, want %d", claims.UserID, u.ID)
	}
	if claims.Username != "testuser" {
		t.Errorf("username = %q, want testuser", claims.Username)
	}
	if claims.Role != store.RoleViewer {
		t.Errorf("role = %q, want %q", claims.Role, store.RoleViewer)
	}
}

func TestIssueTokenWithAdminRole(t *testing.T) {
	a, s := testAuth(t)
	u, _ := s.CreateUser("admin", "pass", store.RoleAdmin)

	token, _ := a.IssueToken(u)
	claims, _ := a.ValidateToken(token)

	if claims.Role != store.RoleAdmin {
		t.Errorf("role = %q, want %q", claims.Role, store.RoleAdmin)
	}
}

func TestValidateBadToken(t *testing.T) {
	a, _ := testAuth(t)
	_, err := a.ValidateToken("invalid-token")
	if err == nil {
		t.Fatal("expected error for bad token")
	}
}

func TestMiddlewareRejectsNoAuth(t *testing.T) {
	a, _ := testAuth(t)

	handler := a.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("GET", "/api/test", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", w.Code)
	}
}

func TestMiddlewareAcceptsValidToken(t *testing.T) {
	a, s := testAuth(t)
	u, _ := s.CreateUser("testuser", "pass", store.RoleViewer)
	token, _ := a.IssueToken(u)

	handler := a.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		claims := UserFromContext(r.Context())
		if claims == nil {
			t.Error("expected claims in context")
			return
		}
		if claims.Username != "testuser" {
			t.Errorf("username = %q, want testuser", claims.Username)
		}
		if claims.Role != store.RoleViewer {
			t.Errorf("role = %q, want viewer", claims.Role)
		}
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("GET", "/api/test", nil)
	req.AddCookie(&http.Cookie{Name: "session", Value: token})
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", w.Code)
	}
}

func TestAdminMiddlewareRejectsViewer(t *testing.T) {
	a, s := testAuth(t)
	u, _ := s.CreateUser("viewer", "pass", store.RoleViewer)
	token, _ := a.IssueToken(u)

	handler := a.Middleware(AdminMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})))

	req := httptest.NewRequest("GET", "/api/admin/users", nil)
	req.AddCookie(&http.Cookie{Name: "session", Value: token})
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403", w.Code)
	}
}

func TestAdminMiddlewareAcceptsAdmin(t *testing.T) {
	a, s := testAuth(t)
	u, _ := s.CreateUser("admin", "pass", store.RoleAdmin)
	token, _ := a.IssueToken(u)

	handler := a.Middleware(AdminMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})))

	req := httptest.NewRequest("GET", "/api/admin/users", nil)
	req.AddCookie(&http.Cookie{Name: "session", Value: token})
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", w.Code)
	}
}
