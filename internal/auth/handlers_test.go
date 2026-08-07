package auth

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/noodlebit/machmqtt-dashboard/internal/store"
)

func handlerTestAuth(t *testing.T) (*Auth, *store.Store, *store.User) {
	t.Helper()
	st := testStoreForAuth(t)
	admin, err := st.CreateUser("admin", "admin-password", store.RoleAdmin)
	if err != nil {
		t.Fatal(err)
	}
	a := New(st, "test-secret-key", false)
	a.SetLogger(slog.New(slog.NewTextHandler(io.Discard, nil)))
	t.Cleanup(func() {
		a.loginLimiter.Stop()
		a.localLoginLimiter.Stop()
	})
	return a, st, admin
}

func requestWithClaims(method, target string, body io.Reader, claims *Claims) *http.Request {
	req := httptest.NewRequest(method, target, body)
	if claims != nil {
		req = req.WithContext(context.WithValue(req.Context(), userKey, claims))
	}
	return req
}

func TestPasswordLoginHandlers(t *testing.T) {
	t.Run("regular and local success", func(t *testing.T) {
		a, _, _ := handlerTestAuth(t)
		for _, localOnly := range []bool{false, true} {
			w := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, "/api/login", strings.NewReader(`{"username":"admin","password":"admin-password"}`))
			if localOnly {
				a.HandleLocalLogin(w, req)
			} else {
				a.HandleLogin(w, req)
			}
			if w.Code != http.StatusOK || len(w.Result().Cookies()) == 0 {
				t.Fatalf("local=%v status=%d body=%s", localOnly, w.Code, w.Body.String())
			}
		}
	})

	t.Run("invalid request and credentials", func(t *testing.T) {
		a, _, _ := handlerTestAuth(t)
		cases := []struct {
			body string
			want int
		}{
			{body: `{`, want: http.StatusBadRequest},
			{body: `{"username":"admin","password":"wrong"}`, want: http.StatusUnauthorized},
		}
		for _, test := range cases {
			w := httptest.NewRecorder()
			a.HandleLogin(w, httptest.NewRequest(http.MethodPost, "/api/login", strings.NewReader(test.body)))
			if w.Code != test.want {
				t.Errorf("body=%q status=%d want=%d", test.body, w.Code, test.want)
			}
		}
	})

	t.Run("provider unavailable", func(t *testing.T) {
		st := testStoreForAuth(t)
		calls := []string{}
		provider := fakePasswordProvider{name: "broken", calls: &calls, fn: func(_, _ string) (*store.User, ProviderResult, error) {
			return nil, ProviderNoMatch, errors.New("directory offline")
		}}
		a := NewWithProviders(st, "test-secret-key", false, []PasswordProvider{provider})
		a.SetLogger(slog.New(slog.NewTextHandler(io.Discard, nil)))
		w := httptest.NewRecorder()
		a.HandleLogin(w, httptest.NewRequest(http.MethodPost, "/api/login", strings.NewReader(`{"username":"alice","password":"password"}`)))
		if w.Code != http.StatusServiceUnavailable {
			t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
		}
	})

	t.Run("rate limited independently", func(t *testing.T) {
		a, _, _ := handlerTestAuth(t)
		a.loginLimiter.Stop()
		a.loginLimiter = NewLoginRateLimiter(0, time.Minute)
		w := httptest.NewRecorder()
		a.HandleLogin(w, httptest.NewRequest(http.MethodPost, "/api/login", strings.NewReader(`{}`)))
		if w.Code != http.StatusTooManyRequests {
			t.Fatalf("status=%d", w.Code)
		}
		w = httptest.NewRecorder()
		a.HandleLocalLogin(w, httptest.NewRequest(http.MethodPost, "/api/auth/local/login", strings.NewReader(`{"username":"admin","password":"admin-password"}`)))
		if w.Code != http.StatusOK {
			t.Fatalf("local status=%d", w.Code)
		}
	})
}

func TestAuthenticationLogsNeverContainCredentialsOrTokens(t *testing.T) {
	st := testStoreForAuth(t)
	const password = "do-not-log-password"
	const token = "do-not-log-token"
	calls := []string{}
	provider := fakePasswordProvider{name: "broken", calls: &calls, fn: func(_, _ string) (*store.User, ProviderResult, error) {
		return nil, ProviderNoMatch, errors.New("upstream leaked " + password + " " + token)
	}}
	a := NewWithProviders(st, "test-secret-key", false, []PasswordProvider{provider})
	t.Cleanup(a.Close)
	var logs bytes.Buffer
	a.SetLogger(slog.New(slog.NewJSONHandler(&logs, nil)))
	body := `{"username":"alice","password":"` + password + `"}`
	w := httptest.NewRecorder()
	a.HandleLogin(w, httptest.NewRequest(http.MethodPost, "/api/login", strings.NewReader(body)))
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	for _, secret := range []string{password, token, "test-secret-key"} {
		if strings.Contains(logs.String(), secret) {
			t.Fatalf("authentication log exposed secret %q: %s", secret, logs.String())
		}
	}
}

func TestProviderAndSessionHandlers(t *testing.T) {
	a, st, admin := handlerTestAuth(t)

	w := httptest.NewRecorder()
	a.HandleProviders(w, httptest.NewRequest(http.MethodGet, "/api/auth/providers", nil))
	if w.Code != http.StatusOK {
		t.Fatal(w.Code)
	}
	var providers map[string]any
	if err := json.NewDecoder(w.Body).Decode(&providers); err != nil || providers["local_login_path"] != "/login/local" {
		t.Fatalf("providers=%v err=%v", providers, err)
	}

	w = httptest.NewRecorder()
	a.HandleLogout(w, httptest.NewRequest(http.MethodPost, "/api/logout", nil))
	cookies := w.Result().Cookies()
	if w.Code != http.StatusOK || len(cookies) != 1 || cookies[0].MaxAge != -1 {
		t.Fatalf("logout status=%d cookies=%v", w.Code, cookies)
	}

	w = httptest.NewRecorder()
	a.HandleMe(w, httptest.NewRequest(http.MethodGet, "/api/me", nil))
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("me without claims=%d", w.Code)
	}
	w = httptest.NewRecorder()
	a.HandleMe(w, requestWithClaims(http.MethodGet, "/api/me", nil, &Claims{UserID: admin.ID}))
	if w.Code != http.StatusOK {
		t.Fatalf("me status=%d", w.Code)
	}
	w = httptest.NewRecorder()
	a.HandleMe(w, requestWithClaims(http.MethodGet, "/api/me", nil, &Claims{UserID: 9999}))
	if w.Code != http.StatusNotFound {
		t.Fatalf("missing me status=%d", w.Code)
	}

	_ = st
}

func TestChangePasswordHandlerBranches(t *testing.T) {
	a, _, admin := handlerTestAuth(t)
	tests := []struct {
		name   string
		id     string
		claims *Claims
		body   string
		want   int
	}{
		{name: "bad id", id: "bad", claims: &Claims{UserID: admin.ID}, body: `{}`, want: http.StatusBadRequest},
		{name: "forbidden", id: "1", claims: nil, body: `{}`, want: http.StatusForbidden},
		{name: "bad JSON", id: "1", claims: &Claims{UserID: admin.ID}, body: `{`, want: http.StatusBadRequest},
		{name: "wrong old password", id: "1", claims: &Claims{UserID: admin.ID}, body: `{"old_password":"wrong","new_password":"new-password"}`, want: http.StatusBadRequest},
		{name: "success", id: "1", claims: &Claims{UserID: admin.ID}, body: `{"old_password":"admin-password","new_password":"new-password"}`, want: http.StatusOK},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			req := requestWithClaims(http.MethodPut, "/api/users/"+test.id+"/password", strings.NewReader(test.body), test.claims)
			req.SetPathValue("id", test.id)
			a.HandleChangePassword(w, req)
			if w.Code != test.want {
				t.Fatalf("status=%d want=%d body=%s", w.Code, test.want, w.Body.String())
			}
		})
	}
}

func TestUserAdministrationHandlerBranches(t *testing.T) {
	a, st, admin := handlerTestAuth(t)

	w := httptest.NewRecorder()
	a.HandleListUsers(w, httptest.NewRequest(http.MethodGet, "/api/admin/users", nil))
	if w.Code != http.StatusOK {
		t.Fatal(w.Code)
	}

	createCases := []struct {
		body string
		want int
	}{
		{body: `{`, want: http.StatusBadRequest},
		{body: `{"username":"","password":""}`, want: http.StatusBadRequest},
		{body: `{"username":"viewer","password":"password"}`, want: http.StatusCreated},
		{body: `{"username":"bad-role","password":"password","role":"owner"}`, want: http.StatusBadRequest},
	}
	for _, test := range createCases {
		w = httptest.NewRecorder()
		a.HandleCreateUser(w, httptest.NewRequest(http.MethodPost, "/api/admin/users", strings.NewReader(test.body)))
		if w.Code != test.want {
			t.Fatalf("create body=%q status=%d want=%d", test.body, w.Code, test.want)
		}
	}
	viewer, err := st.CreateUser("delete-me", "password", store.RoleViewer)
	if err != nil {
		t.Fatal(err)
	}
	deleteCases := []struct {
		name   string
		id     string
		claims *Claims
		want   int
	}{
		{name: "bad id", id: "bad", claims: &Claims{UserID: admin.ID}, want: http.StatusBadRequest},
		{name: "self", id: "1", claims: &Claims{UserID: admin.ID}, want: http.StatusBadRequest},
		{name: "missing", id: "9999", claims: &Claims{UserID: admin.ID}, want: http.StatusBadRequest},
		{name: "success", id: "3", claims: &Claims{UserID: admin.ID}, want: http.StatusOK},
	}
	for _, test := range deleteCases {
		t.Run(test.name, func(t *testing.T) {
			id := test.id
			if test.name == "success" {
				id = jsonNumber(viewer.ID)
			}
			w := httptest.NewRecorder()
			req := requestWithClaims(http.MethodDelete, "/api/admin/users/"+id, nil, test.claims)
			req.SetPathValue("id", id)
			a.HandleDeleteUser(w, req)
			if w.Code != test.want {
				t.Fatalf("status=%d want=%d body=%s", w.Code, test.want, w.Body.String())
			}
		})
	}
}

func jsonNumber(value int64) string {
	encoded, _ := json.Marshal(value)
	return string(encoded)
}

func TestHandleLogoutRevokesSessionsOnOtherDevices(t *testing.T) {
	a, st, admin := handlerTestAuth(t)

	token, err := a.IssueToken(admin)
	if err != nil {
		t.Fatal(err)
	}
	claims, err := a.ValidateToken(token)
	if err != nil {
		t.Fatal(err)
	}

	protected := a.Middleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	probe := func() int {
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/api/me", nil)
		req.AddCookie(&http.Cookie{Name: "session", Value: token})
		protected.ServeHTTP(w, req)
		return w.Code
	}

	if code := probe(); code != http.StatusOK {
		t.Fatalf("session rejected before logout: status=%d", code)
	}

	w := httptest.NewRecorder()
	a.HandleLogout(w, requestWithClaims(http.MethodPost, "/api/logout", nil, claims))
	if w.Code != http.StatusOK {
		t.Fatalf("logout status=%d want=200 body=%s", w.Code, w.Body.String())
	}

	// The cookie this request cleared is only half the job; a copy of the same
	// token held elsewhere must stop working too.
	if code := probe(); code != http.StatusUnauthorized {
		t.Fatalf("token still accepted after logout: status=%d", code)
	}

	updated, err := st.GetUser(admin.ID)
	if err != nil {
		t.Fatal(err)
	}
	if updated.SessionVersion == admin.SessionVersion {
		t.Fatalf("session version unchanged at %d", updated.SessionVersion)
	}
}

func TestHandleLogoutClearsCookieWhenRevocationFails(t *testing.T) {
	a, st, admin := handlerTestAuth(t)
	claims := &Claims{UserID: admin.ID, Username: admin.Username, Role: admin.Role}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}

	w := httptest.NewRecorder()
	a.HandleLogout(w, requestWithClaims(http.MethodPost, "/api/logout", nil, claims))
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d want=200 body=%s", w.Code, w.Body.String())
	}

	var cleared bool
	for _, cookie := range w.Result().Cookies() {
		if cookie.Name == "session" && cookie.Value == "" && cookie.MaxAge < 0 {
			cleared = true
		}
	}
	if !cleared {
		t.Fatalf("session cookie not cleared: %v", w.Result().Cookies())
	}
}

func TestHandleChangePasswordEnforcesMinimumLength(t *testing.T) {
	a, _, admin := handlerTestAuth(t)

	short := strings.Repeat("a", store.MinPasswordLength-1)
	body, err := json.Marshal(map[string]string{"old_password": "admin-password", "new_password": short})
	if err != nil {
		t.Fatal(err)
	}

	id := jsonNumber(admin.ID)
	w := httptest.NewRecorder()
	req := requestWithClaims(http.MethodPut, "/api/users/"+id+"/password", bytes.NewReader(body), &Claims{UserID: admin.ID})
	req.SetPathValue("id", id)
	a.HandleChangePassword(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status=%d want=400 body=%s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), store.ErrPasswordTooShort.Error()) {
		t.Errorf("response does not explain the rejection: %s", w.Body.String())
	}
	// The rejection must happen before the store is touched, so the old
	// credential still works and no session was re-issued.
	if _, err := a.AuthenticateLocal(admin.Username, "admin-password"); err != nil {
		t.Errorf("original password no longer valid: %v", err)
	}
	if _, err := a.AuthenticateLocal(admin.Username, short); err == nil {
		t.Error("short password was accepted by the store")
	}
	if len(w.Result().Cookies()) != 0 {
		t.Errorf("unexpected cookie on a rejected change: %v", w.Result().Cookies())
	}
}
