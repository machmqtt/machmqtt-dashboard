package auth

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	jose "github.com/go-jose/go-jose/v4"
	josejwt "github.com/go-jose/go-jose/v4/jwt"
	"github.com/noodlebit/machmqtt-dashboard/internal/config"
	"github.com/noodlebit/machmqtt-dashboard/internal/store"
	"golang.org/x/oauth2"
)

func TestOIDCFlowIsOneTimeAndBrowserBound(t *testing.T) {
	provider := NewOIDCProvider("enterprise", "https://dashboard.example.com", config.OIDCAuthConfig{}, nil)
	provider.storeFlow("state", oidcFlow{
		nonce:    "nonce",
		verifier: "verifier",
		binding:  "browser-binding",
		created:  time.Now(),
	})
	flow, ok := provider.consumeFlow("state")
	if !ok || flow.binding != "browser-binding" {
		t.Fatalf("flow = %+v, ok = %v", flow, ok)
	}
	if _, ok := provider.consumeFlow("state"); ok {
		t.Fatal("OIDC state must be one-time")
	}
}

func TestOIDCExpiredFlowIsRejected(t *testing.T) {
	provider := NewOIDCProvider("enterprise", "https://dashboard.example.com", config.OIDCAuthConfig{}, nil)
	provider.storeFlow("expired", oidcFlow{created: time.Now().Add(-11 * time.Minute)})
	if _, ok := provider.consumeFlow("expired"); ok {
		t.Fatal("expired OIDC state was accepted")
	}
}

func TestOIDCFlowStats(t *testing.T) {
	provider := NewOIDCProvider("enterprise", "https://dashboard.example.com", config.OIDCAuthConfig{}, nil)
	provider.storeFlow("expired", oidcFlow{created: time.Now().Add(-11 * time.Minute)})
	provider.storeFlow("current", oidcFlow{created: time.Now()})
	stats := provider.FlowStats()
	if stats.Active != 1 || stats.Evictions != 1 {
		t.Fatalf("unexpected flow stats: %+v", stats)
	}
}

func TestAuthOIDCFlowMetrics(t *testing.T) {
	provider := NewOIDCProvider("enterprise", "https://dashboard.example.com", config.OIDCAuthConfig{}, nil)
	provider.storeFlow("current", oidcFlow{created: time.Now()})
	a := NewWithProviderSet(testStoreForAuth(t), "test-signing-secret", false, ProviderSet{OIDC: []*OIDCProvider{provider}})
	t.Cleanup(a.Close)
	stats := a.OIDCFlowMetrics()["enterprise"]
	if stats.Active != 1 || stats.Evictions != 0 {
		t.Fatalf("unexpected auth flow stats: %+v", stats)
	}
}

func TestExternalGroupRoleMapping(t *testing.T) {
	if role := roleForExternalGroups([]string{"Dashboard-Admins"}, []string{"dashboard-admins"}, nil, ""); role != store.RoleAdmin {
		t.Errorf("admin role = %q", role)
	}
	if role := roleForExternalGroups([]string{"CN=Viewers,OU=Groups,DC=example,DC=com"}, nil, []string{"viewers"}, ""); role != store.RoleViewer {
		t.Errorf("viewer role = %q", role)
	}
	if role := roleForExternalGroups(nil, nil, nil, store.RoleViewer); role != store.RoleViewer {
		t.Errorf("default role = %q", role)
	}
}

func TestStringSliceClaim(t *testing.T) {
	groups := stringSliceClaim([]any{"one", 2, "two"})
	if len(groups) != 2 || groups[0] != "one" || groups[1] != "two" {
		t.Fatalf("groups = %v", groups)
	}
}

func TestOIDCCallbackVerifiesTokenAndPersistsIdentity(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := jose.NewSigner(jose.SigningKey{Algorithm: jose.RS256, Key: key}, nil)
	if err != nil {
		t.Fatal(err)
	}
	var issuer string
	server := newIPv4TestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			http.Error(w, "bad form", http.StatusBadRequest)
			return
		}
		if r.Form.Get("code") != "valid-code" || r.Form.Get("code_verifier") != "pkce-verifier" {
			http.Error(w, "bad code", http.StatusUnauthorized)
			return
		}
		now := time.Now()
		rawToken, err := josejwt.Signed(signer).Claims(map[string]any{
			"iss":                issuer,
			"sub":                "immutable-subject",
			"aud":                "dashboard-client",
			"exp":                now.Add(time.Minute).Unix(),
			"iat":                now.Unix(),
			"nonce":              "expected-nonce",
			"preferred_username": "admin",
			"name":               "External Admin",
			"email":              "admin@example.com",
			"groups":             []string{"Dashboard Admins"},
		}).Serialize()
		if err != nil {
			http.Error(w, "sign token", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"access_token": "access-token",
			"token_type":   "Bearer",
			"expires_in":   60,
			"id_token":     rawToken,
		})
	}))
	defer server.Close()
	issuer = server.URL

	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	provider := NewOIDCProvider("enterprise", "https://dashboard.example.com", config.OIDCAuthConfig{
		IssuerURL:        issuer,
		ClientID:         "dashboard-client",
		ClientSecret:     "client-secret",
		UsernameClaim:    "preferred_username",
		DisplayNameClaim: "name",
		EmailClaim:       "email",
		GroupsClaim:      "groups",
		AdminGroups:      []string{"dashboard admins"},
	}, st)
	provider.provider = &oidc.Provider{}
	provider.verifier = oidc.NewVerifier(issuer, &oidc.StaticKeySet{PublicKeys: []crypto.PublicKey{&key.PublicKey}}, &oidc.Config{ClientID: "dashboard-client"})
	provider.oauth2 = &oauth2.Config{
		ClientID:     "dashboard-client",
		ClientSecret: "client-secret",
		Endpoint:     oauth2.Endpoint{TokenURL: server.URL},
		RedirectURL:  "https://dashboard.example.com/api/auth/oidc/enterprise/callback",
	}
	provider.storeFlow("valid-state", oidcFlow{
		nonce:    "expected-nonce",
		verifier: "pkce-verifier",
		binding:  "browser-binding",
		created:  time.Now(),
	})

	user, err := provider.CompleteLogin(context.Background(), "valid-state", "valid-code", "browser-binding")
	if err != nil {
		t.Fatal(err)
	}
	if user.AuthProvider != "enterprise" || user.ExternalSubject != "immutable-subject" || user.Role != store.RoleAdmin {
		t.Fatalf("OIDC user = %+v", user)
	}
}

type oidcIntegrationServer struct {
	server      *httptest.Server
	signer      jose.Signer
	publicKey   *rsa.PublicKey
	mu          sync.Mutex
	nonce       string
	groups      []string
	wrongNonce  bool
	tokenError  bool
	omitIDToken bool
}

func startOIDCIntegrationServer(t *testing.T) *oidcIntegrationServer {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := jose.NewSigner(
		jose.SigningKey{Algorithm: jose.RS256, Key: key},
		new(jose.SignerOptions).WithHeader(jose.HeaderKey("kid"), "integration-key"),
	)
	if err != nil {
		t.Fatal(err)
	}
	provider := &oidcIntegrationServer{
		signer:    signer,
		publicKey: &key.PublicKey,
		groups:    []string{"Dashboard Admins"},
	}
	provider.server = newIPv4TestServer(t, http.HandlerFunc(provider.handle))
	t.Cleanup(provider.server.Close)
	return provider
}

func newIPv4TestServer(t *testing.T, handler http.Handler) *httptest.Server {
	t.Helper()
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewUnstartedServer(handler)
	server.Listener = listener
	server.Start()
	return server
}

func (s *oidcIntegrationServer) handle(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case "/.well-known/openid-configuration":
		writeJSON(w, map[string]any{
			"issuer":                                s.server.URL,
			"authorization_endpoint":                s.server.URL + "/authorize",
			"token_endpoint":                        s.server.URL + "/token",
			"jwks_uri":                              s.server.URL + "/keys",
			"id_token_signing_alg_values_supported": []string{"RS256"},
		})
	case "/keys":
		writeJSON(w, map[string]any{"keys": []jose.JSONWebKey{{
			Key:       s.publicKey,
			KeyID:     "integration-key",
			Algorithm: "RS256",
			Use:       "sig",
		}}})
	case "/token":
		s.mu.Lock()
		nonce, groups, wrongNonce, tokenError, omitIDToken := s.nonce, append([]string(nil), s.groups...), s.wrongNonce, s.tokenError, s.omitIDToken
		s.mu.Unlock()
		if tokenError {
			http.Error(w, `{"error":"invalid_grant"}`, http.StatusBadRequest)
			return
		}
		if err := r.ParseForm(); err != nil || r.Form.Get("code") != "integration-code" || r.Form.Get("code_verifier") == "" {
			http.Error(w, `{"error":"invalid_request"}`, http.StatusBadRequest)
			return
		}
		response := map[string]any{"access_token": "access", "token_type": "Bearer", "expires_in": 60}
		if !omitIDToken {
			if wrongNonce {
				nonce = "wrong-nonce"
			}
			now := time.Now()
			raw, err := josejwt.Signed(s.signer).Claims(map[string]any{
				"iss":                s.server.URL,
				"sub":                "oidc-subject",
				"aud":                "dashboard-client",
				"exp":                now.Add(time.Minute).Unix(),
				"iat":                now.Unix(),
				"nonce":              nonce,
				"preferred_username": "oidc-admin",
				"name":               "OIDC Admin",
				"email":              "oidc-admin@example.com",
				"groups":             groups,
			}).Serialize()
			if err != nil {
				http.Error(w, "sign token", http.StatusInternalServerError)
				return
			}
			response["id_token"] = raw
		}
		writeJSON(w, response)
	default:
		http.NotFound(w, r)
	}
}

func (s *oidcIntegrationServer) setNonce(nonce string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.nonce = nonce
}

func writeJSON(w http.ResponseWriter, value any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(value)
}

func newOIDCIntegrationAuth(t *testing.T, upstream *oidcIntegrationServer, groups bool) (*Auth, *store.Store) {
	t.Helper()
	st := testStoreForAuth(t)
	cfg := config.OIDCAuthConfig{
		IssuerURL:        upstream.server.URL,
		ClientID:         "dashboard-client",
		ClientSecret:     "dashboard-secret",
		UsernameClaim:    "preferred_username",
		DisplayNameClaim: "name",
		EmailClaim:       "email",
		GroupsClaim:      "groups",
	}
	if groups {
		cfg.AdminGroups = []string{"Dashboard Admins"}
	}
	provider := NewOIDCProvider("enterprise", "https://dashboard.example.com", cfg, st)
	a := NewWithProviderSet(st, "test-secret-key", true, ProviderSet{
		OIDC: []*OIDCProvider{provider},
		Info: []ProviderInfo{{Name: "enterprise", Type: "oidc", LoginURL: "/api/auth/oidc/enterprise/login"}},
	})
	a.SetLogger(slog.New(slog.NewTextHandler(io.Discard, nil)))
	return a, st
}

func beginOIDCIntegrationFlow(t *testing.T, a *Auth, upstream *oidcIntegrationServer) (string, *http.Cookie) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/auth/oidc/enterprise/login", nil)
	req.SetPathValue("provider", "enterprise")
	w := httptest.NewRecorder()
	a.HandleOIDCLogin(w, req)
	if w.Code != http.StatusFound {
		t.Fatalf("begin status = %d, body: %s", w.Code, w.Body.String())
	}
	location, err := url.Parse(w.Header().Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	if location.Path != "/authorize" || location.Query().Get("code_challenge") == "" || location.Query().Get("code_challenge_method") != "S256" {
		t.Fatalf("authorization URL = %s", location.String())
	}
	upstream.setNonce(location.Query().Get("nonce"))
	var flowCookie *http.Cookie
	for _, cookie := range w.Result().Cookies() {
		if strings.HasPrefix(cookie.Name, "oidc_flow_") {
			flowCookie = cookie
		}
	}
	if flowCookie == nil || !flowCookie.HttpOnly || !flowCookie.Secure || flowCookie.SameSite != http.SameSiteLaxMode {
		t.Fatalf("flow cookie = %+v", flowCookie)
	}
	return location.Query().Get("state"), flowCookie
}

func completeOIDCIntegrationFlow(a *Auth, state string, flowCookie *http.Cookie) *httptest.ResponseRecorder {
	callback := httptest.NewRequest(http.MethodGet, "/api/auth/oidc/enterprise/callback?state="+url.QueryEscape(state)+"&code=integration-code", nil)
	callback.SetPathValue("provider", "enterprise")
	callback.AddCookie(flowCookie)
	w := httptest.NewRecorder()
	a.HandleOIDCCallback(w, callback)
	return w
}

func TestOIDCDiscoveryAuthorizationAndCallbackIntegration(t *testing.T) {
	upstream := startOIDCIntegrationServer(t)
	a, st := newOIDCIntegrationAuth(t, upstream, true)
	state, flowCookie := beginOIDCIntegrationFlow(t, a, upstream)
	w := completeOIDCIntegrationFlow(a, state, flowCookie)
	if w.Code != http.StatusSeeOther || w.Header().Get("Location") != "/" {
		t.Fatalf("callback status = %d, location = %q, body: %s", w.Code, w.Header().Get("Location"), w.Body.String())
	}
	var session *http.Cookie
	for _, cookie := range w.Result().Cookies() {
		if cookie.Name == "session" {
			session = cookie
		}
	}
	if session == nil || !session.HttpOnly || !session.Secure {
		t.Fatalf("session cookie = %+v", session)
	}
	claims, err := a.ValidateToken(session.Value)
	if err != nil || claims.AuthProvider != "enterprise" || claims.Role != store.RoleAdmin {
		t.Fatalf("claims = %+v, err = %v", claims, err)
	}
	user, err := st.GetUser(claims.UserID)
	if err != nil || user.ExternalSubject != "oidc-subject" || user.Email != "oidc-admin@example.com" {
		t.Fatalf("OIDC user = %+v, err = %v", user, err)
	}
}

func TestOIDCIntegrationFailures(t *testing.T) {
	tests := []struct {
		name       string
		configure  func(*oidcIntegrationServer)
		withGroups bool
		wantStatus int
	}{
		{name: "nonce mismatch", configure: func(s *oidcIntegrationServer) { s.wrongNonce = true }, withGroups: true, wantStatus: http.StatusUnauthorized},
		{name: "token endpoint error", configure: func(s *oidcIntegrationServer) { s.tokenError = true }, withGroups: true, wantStatus: http.StatusUnauthorized},
		{name: "missing ID token", configure: func(s *oidcIntegrationServer) { s.omitIDToken = true }, withGroups: true, wantStatus: http.StatusUnauthorized},
		{name: "missing authorized group", configure: func(s *oidcIntegrationServer) { s.groups = []string{"Other"} }, withGroups: true, wantStatus: http.StatusForbidden},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			upstream := startOIDCIntegrationServer(t)
			test.configure(upstream)
			a, _ := newOIDCIntegrationAuth(t, upstream, test.withGroups)
			state, cookie := beginOIDCIntegrationFlow(t, a, upstream)
			w := completeOIDCIntegrationFlow(a, state, cookie)
			if w.Code != test.wantStatus {
				t.Fatalf("status = %d, want %d, body: %s", w.Code, test.wantStatus, w.Body.String())
			}
		})
	}
}
