package auth

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	ldap "github.com/go-ldap/ldap/v3"
	"github.com/golang-jwt/jwt/v5"
	"github.com/noodlebit/machmqtt-dashboard/internal/config"
	"github.com/noodlebit/machmqtt-dashboard/internal/store"
)

func TestAuthConfigurationAndDefensivePaths(t *testing.T) {
	a, st := testAuth(t)
	if a.Store() != st {
		t.Fatal("Store did not return the configured store")
	}
	if err := a.SetTrustedProxyCIDRs([]string{"10.0.0.0/8", "2001:db8::/32"}); err != nil || len(a.trustedProxies) != 2 {
		t.Fatalf("trusted proxies = %v, err = %v", a.trustedProxies, err)
	}
	if err := a.SetTrustedProxyCIDRs([]string{"not-a-cidr"}); err == nil {
		t.Fatal("expected invalid trusted proxy CIDR to fail")
	}
	a.SetLogger(nil)

	badMethod, err := jwt.NewWithClaims(jwt.SigningMethodHS384, Claims{}).SignedString(a.secret)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.ValidateToken(badMethod); err == nil {
		t.Fatal("expected non-HS256 token to fail")
	}

	handler := a.Middleware(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("invalid token reached protected handler")
	}))
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.AddCookie(&http.Cookie{Name: "session", Value: "invalid"})
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("invalid token status = %d", w.Code)
	}
}

func TestPasswordProviderDefensiveResults(t *testing.T) {
	st := testStoreForAuth(t)
	for _, test := range []struct {
		name   string
		result ProviderResult
		user   *store.User
	}{
		{name: "authenticated without user", result: ProviderAuthenticated},
		{name: "invalid result", result: ProviderResult(99)},
	} {
		t.Run(test.name, func(t *testing.T) {
			calls := []string{}
			provider := fakePasswordProvider{name: "broken", calls: &calls, fn: func(_, _ string) (*store.User, ProviderResult, error) {
				return test.user, test.result, nil
			}}
			a := NewWithProviders(st, "test-secret", false, []PasswordProvider{provider})
			if _, err := a.AuthenticatePassword(context.Background(), "alice", "password"); !errors.Is(err, ErrProviderUnavailable) {
				t.Fatalf("error = %v", err)
			}
		})
	}
}

func TestOIDCHandlerErrorPaths(t *testing.T) {
	a, _, _ := handlerTestAuth(t)

	req := httptest.NewRequest(http.MethodGet, "/api/auth/oidc/missing/login", nil)
	req.SetPathValue("provider", "missing")
	w := httptest.NewRecorder()
	a.HandleOIDCLogin(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("missing login provider status = %d", w.Code)
	}

	req = httptest.NewRequest(http.MethodGet, "/api/auth/oidc/missing/callback", nil)
	req.SetPathValue("provider", "missing")
	w = httptest.NewRecorder()
	a.HandleOIDCCallback(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("missing callback provider status = %d", w.Code)
	}

	unavailable := NewOIDCProvider("unavailable", "https://dashboard.example.com", config.OIDCAuthConfig{
		IssuerURL: "http://127.0.0.1:1",
		ClientID:  "dashboard",
	}, a.store)
	a.oidcProviders[unavailable.Name()] = unavailable
	req = httptest.NewRequest(http.MethodGet, "/api/auth/oidc/unavailable/login", nil)
	req.SetPathValue("provider", unavailable.Name())
	w = httptest.NewRecorder()
	a.HandleOIDCLogin(w, req)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("unavailable provider status = %d", w.Code)
	}

	a.loginLimiter.Stop()
	a.loginLimiter = NewLoginRateLimiter(0, time.Minute)
	req = httptest.NewRequest(http.MethodGet, "/api/auth/oidc/unavailable/login", nil)
	req.SetPathValue("provider", unavailable.Name())
	w = httptest.NewRecorder()
	a.HandleOIDCLogin(w, req)
	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("rate limited OIDC status = %d", w.Code)
	}
}

func TestOIDCCallbackRejectsMissingBrowserBinding(t *testing.T) {
	upstream := startOIDCIntegrationServer(t)
	a, _ := newOIDCIntegrationAuth(t, upstream, true)
	state, _ := beginOIDCIntegrationFlow(t, a, upstream)
	req := httptest.NewRequest(http.MethodGet, "/api/auth/oidc/enterprise/callback?state="+state+"&code=integration-code", nil)
	req.SetPathValue("provider", "enterprise")
	w := httptest.NewRecorder()
	a.HandleOIDCCallback(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("missing flow cookie status = %d", w.Code)
	}
}

func TestClosedStoreHandlerFailure(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	a := New(st, "test-secret", false)
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	a.HandleListUsers(w, httptest.NewRequest(http.MethodGet, "/api/admin/users", nil))
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("closed store list status = %d", w.Code)
	}
}

func TestBuildProviderSetPreservesConfiguredOrder(t *testing.T) {
	ldapConfig := ldapIntegrationConfig("ldap://127.0.0.1:389", "")
	oidcConfig := config.OIDCAuthConfig{IssuerURL: "https://identity.example.com", ClientID: "dashboard"}
	cfg := config.AuthenticationConfig{
		PublicURL: "https://dashboard.example.com",
		Providers: []config.AuthProviderConfig{
			{Name: "directory", Type: "ldap", LDAP: &ldapConfig},
			{Name: "sso", Type: "oidc", OIDC: &oidcConfig},
		},
	}
	set, err := BuildProviderSet(cfg, testStoreForAuth(t))
	if err != nil {
		t.Fatal(err)
	}
	if len(set.Password) != 1 || len(set.OIDC) != 1 || len(set.Info) != 2 || set.Info[0].Name != "directory" || set.Info[1].LoginURL != "/api/auth/oidc/sso/login" {
		t.Fatalf("provider set = %+v", set)
	}

	missingCA := ldapConfig
	missingCA.CAFile = filepath.Join(t.TempDir(), "missing.pem")
	cfg.Providers = []config.AuthProviderConfig{{Name: "bad", Type: "ldap", LDAP: &missingCA}}
	if _, err := BuildProviderSet(cfg, testStoreForAuth(t)); err == nil {
		t.Fatal("expected provider construction error")
	}
}

func TestLDAPProviderConstructionFailures(t *testing.T) {
	st := testStoreForAuth(t)
	if _, err := NewLDAPProvider("bad-url", config.AuthMatchConfig{}, config.LDAPAuthConfig{URL: "%"}, st); err == nil {
		t.Fatal("expected malformed URL error")
	}
	if _, err := NewLDAPProvider("missing-ca", config.AuthMatchConfig{}, config.LDAPAuthConfig{
		URL: "ldaps://localhost:636", CAFile: filepath.Join(t.TempDir(), "missing.pem"),
	}, st); err == nil {
		t.Fatal("expected missing CA error")
	}
	invalidCA := filepath.Join(t.TempDir(), "invalid.pem")
	if err := os.WriteFile(invalidCA, []byte("not a certificate"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := NewLDAPProvider("invalid-ca", config.AuthMatchConfig{}, config.LDAPAuthConfig{
		URL: "ldaps://localhost:636", CAFile: invalidCA,
	}, st); err == nil {
		t.Fatal("expected invalid CA error")
	}
}

func TestLDAPAuthenticationFailureAndMappingPaths(t *testing.T) {
	t.Run("request rejected before connection", func(t *testing.T) {
		provider, err := NewLDAPProvider("directory", config.AuthMatchConfig{Domains: []string{"example.com"}}, config.LDAPAuthConfig{
			URL: "ldap://127.0.0.1:1", Timeout: 20 * time.Millisecond,
		}, testStoreForAuth(t))
		if err != nil {
			t.Fatal(err)
		}
		if _, result, err := provider.Authenticate(context.Background(), "alice@other.test", "password"); err != nil || result != ProviderNoMatch {
			t.Fatalf("domain result = %v, err = %v", result, err)
		}
		if _, result, err := provider.Authenticate(context.Background(), "alice@example.com", ""); err != nil || result != ProviderRejected {
			t.Fatalf("empty password result = %v, err = %v", result, err)
		}
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if _, _, err := provider.Authenticate(ctx, "alice@example.com", "password"); !errors.Is(err, context.Canceled) {
			t.Fatalf("canceled error = %v", err)
		}
		if _, _, err := provider.Authenticate(context.Background(), "alice@example.com", "password"); err == nil {
			t.Fatal("expected LDAP dial error")
		}
	})

	t.Run("service bind failure and missing identity", func(t *testing.T) {
		_, ldapURL, _ := startLDAPIntegrationServer(t, false)
		cfg := ldapIntegrationConfig(ldapURL, "")
		cfg.BindPassword = "wrong-service-password"
		provider, err := NewLDAPProvider("directory", config.AuthMatchConfig{}, cfg, testStoreForAuth(t))
		if err != nil {
			t.Fatal(err)
		}
		if _, _, err := provider.Authenticate(context.Background(), "alice", "password"); err == nil {
			t.Fatal("expected service bind error")
		}

		cfg.BindPassword = "service-password"
		provider, _ = NewLDAPProvider("directory", config.AuthMatchConfig{}, cfg, testStoreForAuth(t))
		if _, result, err := provider.Authenticate(context.Background(), "bob", "password"); err != nil || result != ProviderNoMatch {
			t.Fatalf("missing identity result = %v, err = %v", result, err)
		}
	})

	t.Run("nested group failure", func(t *testing.T) {
		server, ldapURL, _ := startLDAPIntegrationServer(t, false)
		server.configure(func(s *ldapIntegrationServer) { s.groupSearchError = true })
		cfg := ldapIntegrationConfig(ldapURL, "")
		cfg.NestedActiveDirectory = true
		cfg.GroupBaseDN = "OU=Groups,DC=example,DC=com"
		provider, _ := NewLDAPProvider("directory", config.AuthMatchConfig{}, cfg, testStoreForAuth(t))
		if _, _, err := provider.Authenticate(context.Background(), "alice", "directory-password"); err == nil {
			t.Fatal("expected nested group search error")
		}
	})

	t.Run("unauthorized group", func(t *testing.T) {
		_, ldapURL, _ := startLDAPIntegrationServer(t, false)
		cfg := ldapIntegrationConfig(ldapURL, "")
		cfg.ViewerGroups = []string{"Different Group"}
		provider, _ := NewLDAPProvider("directory", config.AuthMatchConfig{}, cfg, testStoreForAuth(t))
		if _, result, err := provider.Authenticate(context.Background(), "alice", "directory-password"); err != nil || result != ProviderRejected {
			t.Fatalf("unauthorized group result = %v, err = %v", result, err)
		}
	})

	t.Run("missing subject", func(t *testing.T) {
		server, ldapURL, _ := startLDAPIntegrationServer(t, false)
		server.configure(func(s *ldapIntegrationServer) { s.omitSubject = true })
		provider, _ := NewLDAPProvider("directory", config.AuthMatchConfig{}, ldapIntegrationConfig(ldapURL, ""), testStoreForAuth(t))
		if _, _, err := provider.Authenticate(context.Background(), "alice", "directory-password"); err == nil {
			t.Fatal("expected missing subject error")
		}
	})

	t.Run("username fallback and default role", func(t *testing.T) {
		server, ldapURL, _ := startLDAPIntegrationServer(t, false)
		server.configure(func(s *ldapIntegrationServer) { s.omitUsername = true })
		cfg := ldapIntegrationConfig(ldapURL, "")
		cfg.ViewerGroups = nil
		cfg.DefaultRole = store.RoleViewer
		provider, _ := NewLDAPProvider("directory", config.AuthMatchConfig{}, cfg, testStoreForAuth(t))
		user, result, err := provider.Authenticate(context.Background(), "alice", "directory-password")
		if err != nil || result != ProviderAuthenticated || user.Username != "alice" || user.Role != store.RoleViewer {
			t.Fatalf("user = %+v, result = %v, err = %v", user, result, err)
		}
	})

	t.Run("store failure", func(t *testing.T) {
		_, ldapURL, _ := startLDAPIntegrationServer(t, false)
		st, err := store.Open(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		provider, _ := NewLDAPProvider("directory", config.AuthMatchConfig{}, ldapIntegrationConfig(ldapURL, ""), st)
		if err := st.Close(); err != nil {
			t.Fatal(err)
		}
		if _, _, err := provider.Authenticate(context.Background(), "alice", "directory-password"); err == nil {
			t.Fatal("expected persistence error")
		}
	})

	t.Run("StartTLS failure", func(t *testing.T) {
		_, ldapURL, _ := startLDAPIntegrationServer(t, false)
		cfg := ldapIntegrationConfig(ldapURL, "")
		cfg.StartTLS = true
		provider, _ := NewLDAPProvider("directory", config.AuthMatchConfig{}, cfg, testStoreForAuth(t))
		if _, _, err := provider.Authenticate(context.Background(), "alice", "directory-password"); err == nil {
			t.Fatal("expected StartTLS failure")
		}
	})
}

func TestLDAPHelpersCoverEmptyAndControlSubjects(t *testing.T) {
	if got := ldapSubject(&ldap.Entry{}, "entryUUID"); got != "" {
		t.Fatalf("empty subject = %q", got)
	}
	entry := &ldap.Entry{Attributes: []*ldap.EntryAttribute{{Name: "entryUUID", ByteValues: [][]byte{[]byte("has\x00control")}}}}
	if got := ldapSubject(entry, "entryUUID"); got != "aGFzAGNvbnRyb2w" {
		t.Fatalf("control subject = %q", got)
	}
}

func TestOIDCFlowAndClaimHelpers(t *testing.T) {
	p := NewOIDCProvider("enterprise", "https://dashboard.example.com/", config.OIDCAuthConfig{}, nil)
	if p.publicURL != "https://dashboard.example.com" {
		t.Fatalf("public URL = %q", p.publicURL)
	}
	p.flows["expired"] = oidcFlow{created: time.Now().Add(-11 * time.Minute)}
	for i := 0; i < 10000; i++ {
		p.flows[jsonNumber(int64(i))] = oidcFlow{created: time.Now().Add(time.Duration(i) * time.Nanosecond)}
	}
	p.storeFlow("new", oidcFlow{created: time.Now()})
	if _, ok := p.flows["expired"]; ok || len(p.flows) != 10000 {
		t.Fatalf("flow eviction failed: expired=%v count=%d", ok, len(p.flows))
	}
	if stats := p.FlowStats(); stats.Active != 10000 || stats.Evictions != 2 {
		t.Fatalf("flow stats = %+v", stats)
	}

	tests := []struct {
		value any
		want  []string
	}{
		{value: "one", want: []string{"one"}},
		{value: []string{"one", "two"}, want: []string{"one", "two"}},
		{value: json.RawMessage(`["one","two"]`), want: []string{"one", "two"}},
		{value: json.RawMessage(`not-json`), want: nil},
		{value: 42, want: nil},
	}
	for _, test := range tests {
		got := stringSliceClaim(test.value)
		if strings.Join(got, ",") != strings.Join(test.want, ",") {
			t.Errorf("stringSliceClaim(%T) = %v", test.value, got)
		}
	}
	if got := uniqueStrings([]string{"openid", "email", "openid"}); strings.Join(got, ",") != "openid,email" {
		t.Fatalf("unique strings = %v", got)
	}
	if token, err := randomToken(8); err != nil || token == "" {
		t.Fatalf("random token = %q, err = %v", token, err)
	}

	if _, err := p.CompleteLogin(context.Background(), "missing", "code", "binding"); !errors.Is(err, ErrOIDCInvalidResponse) {
		t.Fatalf("missing flow error = %v", err)
	}
}

func TestRateLimiterPruningAndProxyEdges(t *testing.T) {
	rl := NewLoginRateLimiter(2, time.Minute)
	t.Cleanup(rl.Stop)
	now := time.Now()
	rl.attempts["all-old"] = []time.Time{now.Add(-2 * time.Minute)}
	rl.attempts["mixed"] = []time.Time{now.Add(-2 * time.Minute), now}
	rl.prune(now)
	if _, ok := rl.attempts["all-old"]; ok || len(rl.attempts["mixed"]) != 1 {
		t.Fatalf("attempts after prune = %v", rl.attempts)
	}
	rl.attempts["stale-on-allow"] = []time.Time{now.Add(-2 * time.Minute), now.Add(-90 * time.Second)}
	if !rl.Allow("stale-on-allow") {
		t.Fatal("stale attempts should not block login")
	}

	_, trusted, _ := net.ParseCIDR("10.0.0.0/8")
	req := httptest.NewRequest(http.MethodPost, "/api/login", nil)
	req.RemoteAddr = "10.0.0.2"
	req.Header.Set("X-Forwarded-For", "invalid, 10.0.0.1")
	if got := clientIP(req, []*net.IPNet{trusted}); got != "10.0.0.2" {
		t.Fatalf("fully trusted proxy chain = %q", got)
	}
	if remoteIP("not-a-host-port") != "not-a-host-port" {
		t.Fatal("remoteIP should preserve a bare address")
	}
	if ipInNetworks("invalid", []*net.IPNet{trusted}) || ipInNetworks("192.0.2.1", []*net.IPNet{trusted}) {
		t.Fatal("invalid or untrusted IP matched trusted network")
	}
	rl.maxKeys = len(rl.attempts)
	if rl.Allow("new-source") {
		t.Fatal("limiter must fail closed at its key bound")
	}
	if rl.Size() != len(rl.attempts) {
		t.Fatal("limiter size changed beyond bound")
	}
}

func TestOperationalAuthenticationMetricsAreDefensiveAndBounded(t *testing.T) {
	a, st := testAuth(t)
	if _, err := st.CreateUser("local-metrics", "password", store.RoleAdmin); err != nil {
		t.Fatal(err)
	}
	if _, err := a.AuthenticateLocal("local-metrics", "password"); err != nil {
		t.Fatal(err)
	}
	a.recordAuthEvent("local", "success")
	events := a.Metrics()
	durations := a.ProviderDurations()
	if events["local\x00success"] != 1 || durations["local"] == 0 {
		t.Fatalf("events=%v durations=%v", events, durations)
	}
	events["local\x00success"] = 99
	durations["local"] = 0
	if a.Metrics()["local\x00success"] != 1 || a.ProviderDurations()["local"] == 0 {
		t.Fatal("metric snapshots must be defensive copies")
	}
	a.loginLimiter.Stop()
	a.loginLimiter = NewLoginRateLimiter(0, time.Minute)
	if a.loginLimiter.Allow("192.0.2.1") {
		t.Fatal("zero-capacity limiter accepted request")
	}
	ordered, local := a.RateLimiterMetrics()
	if ordered.Rejected != 1 || local.Keys < 0 {
		t.Fatalf("ordered=%+v local=%+v", ordered, local)
	}
}
