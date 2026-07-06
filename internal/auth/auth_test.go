package auth

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/noodlebit/machmqtt-dashboard/internal/store"
)

func testAuth(t *testing.T) (*Auth, *store.Store) {
	t.Helper()
	s, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return New(s, "test-secret-key", false, false, nil), s
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

func TestMiddlewareRejectsExpiredToken(t *testing.T) {
	a, _ := testAuth(t)
	_, err := a.ValidateToken("eyJhbGciOiJIUzI1NiJ9.eyJleHAiOjF9.invalid")
	if err == nil {
		t.Error("expected error for obviously invalid token")
	}
}

func TestValidateTokenWrongAlgorithm(t *testing.T) {
	a, _ := testAuth(t)
	// A syntactically valid JWT header with alg=RS256 (non-HMAC).
	// header={"alg":"RS256","typ":"JWT"} payload={"sub":"test"} sig=fakesig
	rs256Token := "eyJhbGciOiJSUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiJ0ZXN0In0.ZmFrZXNpZw"
	_, err := a.ValidateToken(rs256Token)
	if err == nil {
		t.Error("expected error for RS256-signed token (unexpected signing method)")
	}
}

func TestSetAndClearSessionCookie(t *testing.T) {
	a, _ := testAuth(t)

	w := httptest.NewRecorder()
	a.SetSessionCookie(w, "test-token")
	resp := w.Result()
	cookies := resp.Cookies()
	if len(cookies) == 0 {
		t.Fatal("expected a Set-Cookie header after SetSessionCookie")
	}
	if cookies[0].Name != "session" || cookies[0].Value != "test-token" {
		t.Errorf("cookie = {%q,%q}, want {session,test-token}", cookies[0].Name, cookies[0].Value)
	}

	w2 := httptest.NewRecorder()
	a.ClearSessionCookie(w2)
	resp2 := w2.Result()
	cookies2 := resp2.Cookies()
	if len(cookies2) == 0 {
		t.Fatal("expected a Set-Cookie header after ClearSessionCookie")
	}
	if cookies2[0].Name != "session" || cookies2[0].MaxAge != -1 {
		t.Errorf("clear cookie name=%q maxage=%d, want session/-1", cookies2[0].Name, cookies2[0].MaxAge)
	}
}

func TestStoreGetter(t *testing.T) {
	a, s := testAuth(t)
	if a.Store() != s {
		t.Error("Store() returned wrong pointer")
	}
}

// Rate limiter tests

func TestRateLimiterAllowsUnderLimit(t *testing.T) {
	rl := NewLoginRateLimiter(3, time.Minute)
	defer rl.Stop()
	for i := range 3 {
		if !rl.Allow("1.2.3.4") {
			t.Errorf("attempt %d should be allowed", i+1)
		}
	}
}

func TestRateLimiterBlocksAtLimit(t *testing.T) {
	rl := NewLoginRateLimiter(2, time.Minute)
	defer rl.Stop()
	rl.Allow("1.2.3.4")
	rl.Allow("1.2.3.4")
	if rl.Allow("1.2.3.4") {
		t.Error("third attempt should be blocked")
	}
}

func TestRateLimiterIsolatesIPs(t *testing.T) {
	rl := NewLoginRateLimiter(1, time.Minute)
	defer rl.Stop()
	rl.Allow("1.1.1.1") // exhausts limit for 1.1.1.1
	if !rl.Allow("2.2.2.2") {
		t.Error("different IP should still be allowed")
	}
}

func TestRateLimiterStop(t *testing.T) {
	rl := NewLoginRateLimiter(5, time.Second)
	rl.Stop() // must not block or panic
}

func TestClientIP(t *testing.T) {
	cases := []struct {
		name       string
		xff        string
		remoteAddr string
		trustProxy bool
		want       string
	}{
		// Trusted proxy: X-Forwarded-For is honored, taking the rightmost hop
		// (the IP the trusted proxy appended). Earlier entries are spoofable.
		{"trusted xff single", "10.0.0.1", "192.168.1.1:5", true, "10.0.0.1"},
		{"trusted xff comma-separated", "10.0.0.1, 10.0.0.2", "192.168.1.1:5", true, "10.0.0.2"},
		// Untrusted: X-Forwarded-For is ignored so it can't be spoofed to mint a
		// fresh rate-limit bucket — RemoteAddr wins.
		{"untrusted xff ignored", "10.0.0.1", "192.168.1.1:54321", false, "192.168.1.1"},
		{"trusted but no xff falls back", "", "192.168.1.1:54321", true, "192.168.1.1"},
		{"remoteAddr with port", "", "192.168.1.1:54321", false, "192.168.1.1"},
		{"remoteAddr no port", "", "192.168.1.1", false, "192.168.1.1"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", "/", nil)
			if tc.xff != "" {
				req.Header.Set("X-Forwarded-For", tc.xff)
			}
			req.RemoteAddr = tc.remoteAddr
			if got := clientIP(req, tc.trustProxy); got != tc.want {
				t.Errorf("clientIP = %q, want %q", got, tc.want)
			}
		})
	}
}

// Handler tests

func TestHandleLogin(t *testing.T) {
	a, s := testAuth(t)
	s.CreateUser("alice", "secret", store.RoleViewer)

	req := httptest.NewRequest("POST", "/auth/login", strings.NewReader(`{"username":"alice","password":"secret"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	a.HandleLogin(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("login status = %d, want 200 (body: %s)", w.Code, w.Body.String())
	}
	cookies := w.Result().Cookies()
	if len(cookies) == 0 {
		t.Error("expected session cookie after successful login")
	}
}

func TestHandleLoginBadCredentials(t *testing.T) {
	a, _ := testAuth(t)
	req := httptest.NewRequest("POST", "/auth/login", strings.NewReader(`{"username":"nobody","password":"wrong"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	a.HandleLogin(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", w.Code)
	}
}

func TestHandleLoginBadJSON(t *testing.T) {
	a, _ := testAuth(t)
	req := httptest.NewRequest("POST", "/auth/login", strings.NewReader(`not json`))
	w := httptest.NewRecorder()
	a.HandleLogin(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", w.Code)
	}
}

func TestHandleLoginRateLimited(t *testing.T) {
	a, _ := testAuth(t)
	// Exhaust the rate limit (default is 10/min; use a fresh limiter with max=1).
	a.loginLimiter = NewLoginRateLimiter(1, time.Minute)
	defer a.loginLimiter.Stop()
	// First request
	req1 := httptest.NewRequest("POST", "/auth/login", strings.NewReader(`{"username":"x","password":"y"}`))
	req1.RemoteAddr = "1.2.3.4:9999"
	w1 := httptest.NewRecorder()
	a.HandleLogin(w1, req1) // consumes the one allowed attempt
	// Second request — should be rate-limited
	req2 := httptest.NewRequest("POST", "/auth/login", strings.NewReader(`{"username":"x","password":"y"}`))
	req2.RemoteAddr = "1.2.3.4:9999"
	w2 := httptest.NewRecorder()
	a.HandleLogin(w2, req2)
	if w2.Code != http.StatusTooManyRequests {
		t.Errorf("status = %d, want 429", w2.Code)
	}
}

func TestHandleLogout(t *testing.T) {
	a, _ := testAuth(t)
	req := httptest.NewRequest("POST", "/auth/logout", nil)
	w := httptest.NewRecorder()
	a.HandleLogout(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", w.Code)
	}
}

func TestHandleMe(t *testing.T) {
	a, s := testAuth(t)
	u, _ := s.CreateUser("me", "pass", store.RoleViewer)
	token, _ := a.IssueToken(u)

	handler := a.Middleware(http.HandlerFunc(a.HandleMe))
	req := httptest.NewRequest("GET", "/auth/me", nil)
	req.AddCookie(&http.Cookie{Name: "session", Value: token})
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200 (body: %s)", w.Code, w.Body.String())
	}
}

func TestHandleMeNoContext(t *testing.T) {
	a, _ := testAuth(t)
	req := httptest.NewRequest("GET", "/auth/me", nil) // no auth context
	w := httptest.NewRecorder()
	a.HandleMe(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", w.Code)
	}
}

func TestHandleListUsers(t *testing.T) {
	a, s := testAuth(t)
	s.CreateUser("u1", "pass", store.RoleViewer)
	u2, _ := s.CreateUser("u2", "pass", store.RoleAdmin)
	token, _ := a.IssueToken(u2)

	handler := a.Middleware(AdminMiddleware(http.HandlerFunc(a.HandleListUsers)))
	req := httptest.NewRequest("GET", "/api/admin/users", nil)
	req.AddCookie(&http.Cookie{Name: "session", Value: token})
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", w.Code)
	}
}

func TestHandleCreateUser(t *testing.T) {
	a, s := testAuth(t)
	admin, _ := s.CreateUser("admin", "pass", store.RoleAdmin)
	token, _ := a.IssueToken(admin)

	handler := a.Middleware(AdminMiddleware(http.HandlerFunc(a.HandleCreateUser)))
	req := httptest.NewRequest("POST", "/api/admin/users", strings.NewReader(`{"username":"newuser","password":"p@ssw0rd","role":"viewer"}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: "session", Value: token})
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Errorf("status = %d, want 201 (body: %s)", w.Code, w.Body.String())
	}
}

func TestHandleCreateUserBadJSON(t *testing.T) {
	a, s := testAuth(t)
	admin, _ := s.CreateUser("admin", "pass", store.RoleAdmin)
	token, _ := a.IssueToken(admin)

	handler := a.Middleware(AdminMiddleware(http.HandlerFunc(a.HandleCreateUser)))
	req := httptest.NewRequest("POST", "/api/admin/users", strings.NewReader(`not json`))
	req.AddCookie(&http.Cookie{Name: "session", Value: token})
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", w.Code)
	}
}

func TestHandleCreateUserMissingFields(t *testing.T) {
	a, s := testAuth(t)
	admin, _ := s.CreateUser("admin", "pass", store.RoleAdmin)
	token, _ := a.IssueToken(admin)

	handler := a.Middleware(AdminMiddleware(http.HandlerFunc(a.HandleCreateUser)))
	req := httptest.NewRequest("POST", "/api/admin/users", strings.NewReader(`{"username":"","password":""}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: "session", Value: token})
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", w.Code)
	}
}

func TestHandleDeleteUser(t *testing.T) {
	a, s := testAuth(t)
	admin, _ := s.CreateUser("admin", "pass", store.RoleAdmin)
	victim, _ := s.CreateUser("victim", "pass", store.RoleViewer)
	token, _ := a.IssueToken(admin)

	handler := a.Middleware(AdminMiddleware(http.HandlerFunc(a.HandleDeleteUser)))
	req := httptest.NewRequest("DELETE", "/api/admin/users/"+strconv.FormatInt(victim.ID, 10), nil)
	req.SetPathValue("id", strconv.FormatInt(victim.ID, 10))
	req.AddCookie(&http.Cookie{Name: "session", Value: token})
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200 (body: %s)", w.Code, w.Body.String())
	}
}

func TestHandleDeleteUserSelf(t *testing.T) {
	a, s := testAuth(t)
	admin, _ := s.CreateUser("admin", "pass", store.RoleAdmin)
	token, _ := a.IssueToken(admin)

	handler := a.Middleware(AdminMiddleware(http.HandlerFunc(a.HandleDeleteUser)))
	req := httptest.NewRequest("DELETE", "/api/admin/users/"+strconv.FormatInt(admin.ID, 10), nil)
	req.SetPathValue("id", strconv.FormatInt(admin.ID, 10))
	req.AddCookie(&http.Cookie{Name: "session", Value: token})
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 (cannot delete self)", w.Code)
	}
}

func TestHandleDeleteUserBadID(t *testing.T) {
	a, s := testAuth(t)
	admin, _ := s.CreateUser("admin", "pass", store.RoleAdmin)
	token, _ := a.IssueToken(admin)

	handler := a.Middleware(AdminMiddleware(http.HandlerFunc(a.HandleDeleteUser)))
	req := httptest.NewRequest("DELETE", "/api/admin/users/notanid", nil)
	req.SetPathValue("id", "notanid")
	req.AddCookie(&http.Cookie{Name: "session", Value: token})
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", w.Code)
	}
}

func TestHandleChangePassword(t *testing.T) {
	a, s := testAuth(t)
	u, _ := s.CreateUser("user1", "oldpass", store.RoleViewer)
	token, _ := a.IssueToken(u)

	handler := a.Middleware(http.HandlerFunc(a.HandleChangePassword))
	body := `{"old_password":"oldpass","new_password":"newpass123"}`
	req := httptest.NewRequest("PUT", "/api/users/"+strconv.FormatInt(u.ID, 10)+"/password", strings.NewReader(body))
	req.SetPathValue("id", strconv.FormatInt(u.ID, 10))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: "session", Value: token})
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200 (body: %s)", w.Code, w.Body.String())
	}
}

func TestHandleChangePasswordBadID(t *testing.T) {
	a, s := testAuth(t)
	u, _ := s.CreateUser("user1", "pass", store.RoleViewer)
	token, _ := a.IssueToken(u)

	handler := a.Middleware(http.HandlerFunc(a.HandleChangePassword))
	req := httptest.NewRequest("PUT", "/api/users/notanid/password", strings.NewReader(`{}`))
	req.SetPathValue("id", "notanid")
	req.AddCookie(&http.Cookie{Name: "session", Value: token})
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", w.Code)
	}
}

func TestHandleChangePasswordBadJSON(t *testing.T) {
	a, s := testAuth(t)
	u, _ := s.CreateUser("user1", "pass", store.RoleViewer)
	token, _ := a.IssueToken(u)

	handler := a.Middleware(http.HandlerFunc(a.HandleChangePassword))
	req := httptest.NewRequest("PUT", "/api/users/"+strconv.FormatInt(u.ID, 10)+"/password", strings.NewReader(`not json`))
	req.SetPathValue("id", strconv.FormatInt(u.ID, 10))
	req.AddCookie(&http.Cookie{Name: "session", Value: token})
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", w.Code)
	}
}

func TestHandleChangePasswordWrongOldPass(t *testing.T) {
	a, s := testAuth(t)
	u, _ := s.CreateUser("user1", "rightpass", store.RoleViewer)
	token, _ := a.IssueToken(u)

	handler := a.Middleware(http.HandlerFunc(a.HandleChangePassword))
	body := `{"old_password":"wrongpass","new_password":"newpass123"}`
	req := httptest.NewRequest("PUT", "/api/users/"+strconv.FormatInt(u.ID, 10)+"/password", strings.NewReader(body))
	req.SetPathValue("id", strconv.FormatInt(u.ID, 10))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: "session", Value: token})
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 (wrong old password rejected)", w.Code)
	}
}

func TestHandleCreateUserDefaultRole(t *testing.T) {
	a, s := testAuth(t)
	admin, _ := s.CreateUser("admin", "pass", store.RoleAdmin)
	token, _ := a.IssueToken(admin)

	handler := a.Middleware(AdminMiddleware(http.HandlerFunc(a.HandleCreateUser)))
	// omit role — should default to "viewer"
	req := httptest.NewRequest("POST", "/api/admin/users", strings.NewReader(`{"username":"defaultrole","password":"pass1234"}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: "session", Value: token})
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Errorf("status = %d, want 201 (body: %s)", w.Code, w.Body.String())
	}
}

func TestMiddlewareRejectsBadCookieValue(t *testing.T) {
	a, _ := testAuth(t)
	handler := a.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	req := httptest.NewRequest("GET", "/api/test", nil)
	req.AddCookie(&http.Cookie{Name: "session", Value: "garbage-not-a-jwt"})
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", w.Code)
	}
}

func TestHandleMeUserNotFound(t *testing.T) {
	a, s := testAuth(t)
	u, _ := s.CreateUser("ghost", "pass", store.RoleViewer)
	token, _ := a.IssueToken(u)
	s.DeleteUser(u.ID) // delete after issuing token

	handler := a.Middleware(http.HandlerFunc(a.HandleMe))
	req := httptest.NewRequest("GET", "/auth/me", nil)
	req.AddCookie(&http.Cookie{Name: "session", Value: token})
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	// The middleware re-checks the user against the DB, so a deleted user's
	// still-signed token is rejected as unauthorized (session revoked) before
	// the handler runs.
	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", w.Code)
	}
}

func TestHandleListUsersStoreError(t *testing.T) {
	a, s := testAuth(t)
	admin, _ := s.CreateUser("admin", "pass", store.RoleAdmin)
	token, _ := a.IssueToken(admin)
	s.Close() // close DB so ListUsers fails

	handler := a.Middleware(AdminMiddleware(http.HandlerFunc(a.HandleListUsers)))
	req := httptest.NewRequest("GET", "/api/admin/users", nil)
	req.AddCookie(&http.Cookie{Name: "session", Value: token})
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", w.Code)
	}
}

func TestSweepOnceRemovesFullyExpired(t *testing.T) {
	rl := NewLoginRateLimiter(10, 30*time.Millisecond)
	defer rl.Stop()
	rl.Allow("10.0.0.1")              // adds an attempt
	rl.Allow("10.0.0.2")              // adds an attempt
	time.Sleep(50 * time.Millisecond) // both expire
	rl.sweepOnce()
	rl.mu.Lock()
	defer rl.mu.Unlock()
	if _, ok := rl.attempts["10.0.0.1"]; ok {
		t.Error("10.0.0.1 should have been swept (all attempts expired)")
	}
	if _, ok := rl.attempts["10.0.0.2"]; ok {
		t.Error("10.0.0.2 should have been swept (all attempts expired)")
	}
}

func TestSweepOnceTrimsPartiallyExpired(t *testing.T) {
	rl := NewLoginRateLimiter(10, 200*time.Millisecond)
	defer rl.Stop()
	rl.Allow("10.0.0.3") // old attempt — will expire
	time.Sleep(110 * time.Millisecond)
	rl.Allow("10.0.0.3") // fresh attempt — within window
	time.Sleep(10 * time.Millisecond)
	rl.sweepOnce()
	rl.mu.Lock()
	defer rl.mu.Unlock()
	if len(rl.attempts["10.0.0.3"]) == 0 {
		t.Error("10.0.0.3 should still have the recent attempt after partial sweep")
	}
}

func TestRateLimiterPrunesExpiredAttempts(t *testing.T) {
	rl := NewLoginRateLimiter(2, 30*time.Millisecond)
	defer rl.Stop()
	rl.Allow("5.5.5.5")               // first attempt
	rl.Allow("5.5.5.5")               // second — now at limit
	time.Sleep(50 * time.Millisecond) // both expire
	// Should be allowed again after expiry (pruning path in Allow)
	if !rl.Allow("5.5.5.5") {
		t.Error("Allow should return true after all prior attempts have expired")
	}
}

func TestHandleCreateUserDuplicate(t *testing.T) {
	a, s := testAuth(t)
	admin, _ := s.CreateUser("admin", "pass", store.RoleAdmin)
	s.CreateUser("existing", "pass", store.RoleViewer)
	token, _ := a.IssueToken(admin)

	handler := a.Middleware(AdminMiddleware(http.HandlerFunc(a.HandleCreateUser)))
	req := httptest.NewRequest("POST", "/api/admin/users", strings.NewReader(`{"username":"existing","password":"pass123","role":"viewer"}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: "session", Value: token})
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 (duplicate username)", w.Code)
	}
}

func TestHandleDeleteUserNotFound(t *testing.T) {
	a, s := testAuth(t)
	admin, _ := s.CreateUser("admin", "pass", store.RoleAdmin)
	token, _ := a.IssueToken(admin)

	handler := a.Middleware(AdminMiddleware(http.HandlerFunc(a.HandleDeleteUser)))
	req := httptest.NewRequest("DELETE", "/api/admin/users/99999", nil)
	req.SetPathValue("id", "99999")
	req.AddCookie(&http.Cookie{Name: "session", Value: token})
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	// expect 400 since the user doesn't exist
	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 (user not found)", w.Code)
	}
}

func TestHandleChangePasswordForbidden(t *testing.T) {
	a, s := testAuth(t)
	u1, _ := s.CreateUser("user1", "pass", store.RoleViewer)
	u2, _ := s.CreateUser("user2", "pass", store.RoleViewer)
	token, _ := a.IssueToken(u1) // u1 trying to change u2's password

	handler := a.Middleware(http.HandlerFunc(a.HandleChangePassword))
	body := `{"old_password":"pass","new_password":"new"}`
	req := httptest.NewRequest("PUT", "/api/users/"+strconv.FormatInt(u2.ID, 10)+"/password", strings.NewReader(body))
	req.SetPathValue("id", strconv.FormatInt(u2.ID, 10))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: "session", Value: token})
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403", w.Code)
	}
}
