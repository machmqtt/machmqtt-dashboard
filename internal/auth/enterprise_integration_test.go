//go:build enterprise_integration

package auth

import (
	"context"
	"encoding/json"
	"errors"
	"html"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/noodlebit/machmqtt-dashboard/internal/config"
	"github.com/noodlebit/machmqtt-dashboard/internal/store"
)

const (
	realLDAPAdminDN       = "cn=admin,dc=planetexpress,dc=com"
	realLDAPAdminPassword = "GoodNewsEveryone"
	realLDAPUserPassword  = "fry"
)

func TestRealOpenLDAPAuthenticationAndLocalFallback(t *testing.T) {
	ldapURL := requiredIntegrationEnvironment(t, "REAL_LDAP_URL")
	st := testStoreForAuth(t)
	localFry, err := st.CreateUser("fry", "local-password", store.RoleAdmin)
	if err != nil {
		t.Fatal(err)
	}
	localAdmin, err := st.CreateUser("local-admin", "break-glass-password", store.RoleAdmin)
	if err != nil {
		t.Fatal(err)
	}
	provider, err := NewLDAPProvider("openldap", config.AuthMatchConfig{}, realLDAPConfig(ldapURL), st)
	if err != nil {
		t.Fatal(err)
	}
	waitForLDAPAuthentication(t, provider)
	startTLSConfig := realLDAPConfig(ldapURL)
	startTLSConfig.StartTLS = true
	startTLSConfig.InsecureSkipVerify = true
	startTLSProvider, err := NewLDAPProvider("openldap-starttls", config.AuthMatchConfig{}, startTLSConfig, st)
	if err != nil {
		t.Fatal(err)
	}
	startTLSUser, result, err := startTLSProvider.Authenticate(context.Background(), "fry", realLDAPUserPassword)
	if err != nil || result != ProviderAuthenticated || startTLSUser.AuthProvider != "openldap-starttls" {
		t.Fatalf("OpenLDAP StartTLS user = %+v, result = %v, err = %v", startTLSUser, result, err)
	}

	a := NewWithProviders(st, "enterprise-integration-secret", false, []PasswordProvider{provider})
	t.Cleanup(func() {
		a.loginLimiter.Stop()
		a.localLoginLimiter.Stop()
	})

	externalFry, err := a.AuthenticatePassword(context.Background(), "fry", realLDAPUserPassword)
	if err != nil {
		t.Fatal(err)
	}
	if externalFry.AuthProvider != "openldap" || externalFry.ExternalSubject == "" || externalFry.Role != store.RoleAdmin {
		t.Fatalf("OpenLDAP user = %+v", externalFry)
	}
	if externalFry.ID == localFry.ID {
		t.Fatal("external LDAP identity collided with the same-named local identity")
	}
	if _, err := a.AuthenticatePassword(context.Background(), "fry", "wrong-password"); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("wrong LDAP password error = %v", err)
	}
	explicitLocal, err := a.AuthenticateLocal("fry", "local-password")
	if err != nil || explicitLocal.ID != localFry.ID {
		t.Fatalf("explicit local user = %+v, err = %v", explicitLocal, err)
	}
	fallback, err := a.AuthenticatePassword(context.Background(), "local-admin", "break-glass-password")
	if err != nil || fallback.ID != localAdmin.ID || fallback.AuthProvider != store.ProviderLocal {
		t.Fatalf("local fallback user = %+v, err = %v", fallback, err)
	}
}

func TestRealDexOIDCFlowBackedByOpenLDAP(t *testing.T) {
	issuer := requiredIntegrationEnvironment(t, "REAL_OIDC_ISSUER_URL")
	waitForHTTP(t, issuer+"/.well-known/openid-configuration")
	st := testStoreForAuth(t)
	provider := NewOIDCProvider("dex", "http://127.0.0.1:18080", config.OIDCAuthConfig{
		IssuerURL:        issuer,
		ClientID:         "dashboard-client",
		ClientSecret:     "dashboard-secret",
		Scopes:           []string{"groups"},
		UsernameClaim:    "preferred_username",
		DisplayNameClaim: "name",
		EmailClaim:       "email",
		GroupsClaim:      "groups",
		AdminGroups:      []string{"ship_crew"},
	}, st)
	a := NewWithProviderSet(st, "enterprise-integration-secret", false, ProviderSet{
		OIDC: []*OIDCProvider{provider},
		Info: []ProviderInfo{{Name: "dex", Type: "oidc"}},
	})
	t.Cleanup(func() {
		a.loginLimiter.Stop()
		a.localLoginLimiter.Stop()
	})

	user := completeRealDexLogin(t, a, provider)
	if user.AuthProvider != "dex" || user.Username != "fry" || user.Email != "fry@planetexpress.com" || user.Role != store.RoleAdmin || user.ExternalSubject == "" {
		t.Fatalf("Dex/OpenLDAP user = %+v", user)
	}
}

func TestRealProviderCombinationMatrix(t *testing.T) {
	ldapURL := requiredIntegrationEnvironment(t, "REAL_LDAP_URL")
	issuer := requiredIntegrationEnvironment(t, "REAL_OIDC_ISSUER_URL")
	waitForHTTP(t, issuer+"/.well-known/openid-configuration")

	t.Run("local only", func(t *testing.T) {
		a, st := newRealCombinationAuth(t, config.AuthenticationConfig{}, false)
		createRealLocalUser(t, st, "local-admin", "local-password")
		assertRealPasswordLogin(t, a, false, "local-admin", "local-password", store.ProviderLocal)
		assertRealPasswordLogin(t, a, true, "local-admin", "local-password", store.ProviderLocal)
		assertProviderInfo(t, a, nil)
	})

	t.Run("LDAP plus local", func(t *testing.T) {
		authConfig := realProviderConfiguration(ldapURL, issuer, true, false)
		a, st := newRealCombinationAuth(t, authConfig, true)
		createRealLocalUser(t, st, "local-admin", "local-password")
		createRealLocalUser(t, st, "fry", "local-fry-password")
		assertRealPasswordLogin(t, a, false, "fry", realLDAPUserPassword, "openldap")
		assertRealPasswordLogin(t, a, false, "local-admin", "local-password", store.ProviderLocal)
		assertRealPasswordLogin(t, a, true, "fry", "local-fry-password", store.ProviderLocal)
		assertProviderInfo(t, a, []ProviderInfo{{Name: "openldap", Type: "ldap"}})
	})

	t.Run("OIDC plus local", func(t *testing.T) {
		authConfig := realProviderConfiguration(ldapURL, issuer, false, true)
		a, st := newRealCombinationAuth(t, authConfig, false)
		createRealLocalUser(t, st, "local-admin", "local-password")
		assertRealPasswordLogin(t, a, false, "local-admin", "local-password", store.ProviderLocal)
		assertRealPasswordLogin(t, a, true, "local-admin", "local-password", store.ProviderLocal)
		assertProviderInfo(t, a, []ProviderInfo{{Name: "dex", Type: "oidc", LoginURL: "/api/auth/oidc/dex/login"}})
		oidcUser := completeRealDexLogin(t, a, a.oidcProviders["dex"])
		if oidcUser.AuthProvider != "dex" || oidcUser.Role != store.RoleAdmin {
			t.Fatalf("OIDC user = %+v", oidcUser)
		}
	})

	t.Run("LDAP plus OIDC plus local", func(t *testing.T) {
		authConfig := realProviderConfiguration(ldapURL, issuer, true, true)
		a, st := newRealCombinationAuth(t, authConfig, true)
		createRealLocalUser(t, st, "local-admin", "local-password")
		createRealLocalUser(t, st, "fry", "local-fry-password")
		assertProviderInfo(t, a, []ProviderInfo{
			{Name: "openldap", Type: "ldap"},
			{Name: "dex", Type: "oidc", LoginURL: "/api/auth/oidc/dex/login"},
		})
		assertRealPasswordLogin(t, a, false, "fry", realLDAPUserPassword, "openldap")
		assertRealPasswordLogin(t, a, false, "local-admin", "local-password", store.ProviderLocal)
		assertRealPasswordLogin(t, a, true, "fry", "local-fry-password", store.ProviderLocal)
		oidcUser := completeRealDexLogin(t, a, a.oidcProviders["dex"])
		if oidcUser.AuthProvider != "dex" || oidcUser.Role != store.RoleAdmin {
			t.Fatalf("combined OIDC user = %+v", oidcUser)
		}
		assertRealPasswordLogin(t, a, true, "local-admin", "local-password", store.ProviderLocal)
	})
}

func completeRealDexLogin(t *testing.T, a *Auth, provider *OIDCProvider) *store.User {
	t.Helper()
	if provider == nil {
		t.Fatal("Dex provider is not configured")
	}
	loginRequest := httptest.NewRequest(http.MethodGet, "/api/auth/oidc/dex/login", nil)
	loginRequest.SetPathValue("provider", "dex")
	loginResponse := httptest.NewRecorder()
	a.HandleOIDCLogin(loginResponse, loginRequest)
	if loginResponse.Code != http.StatusFound {
		t.Fatalf("OIDC start status = %d, body: %s", loginResponse.Code, loginResponse.Body.String())
	}
	flowCookie := cookieNamed(loginResponse.Result().Cookies(), provider.flowCookieName())
	if flowCookie == nil {
		t.Fatal("OIDC login did not set a browser-binding cookie")
	}

	callbackURL := logIntoDex(t, loginResponse.Header().Get("Location"))
	callbackRequest := httptest.NewRequest(http.MethodGet, callbackURL.String(), nil)
	callbackRequest.SetPathValue("provider", "dex")
	callbackRequest.AddCookie(flowCookie)
	callbackResponse := httptest.NewRecorder()
	a.HandleOIDCCallback(callbackResponse, callbackRequest)
	if callbackResponse.Code != http.StatusSeeOther || callbackResponse.Header().Get("Location") != "/" {
		t.Fatalf("OIDC callback status = %d, location = %q, body: %s", callbackResponse.Code, callbackResponse.Header().Get("Location"), callbackResponse.Body.String())
	}

	session := cookieNamed(callbackResponse.Result().Cookies(), "session")
	if session == nil {
		t.Fatal("OIDC callback did not issue a dashboard session")
	}
	claims, err := a.ValidateToken(session.Value)
	if err != nil {
		t.Fatal(err)
	}
	user, err := a.store.GetUser(claims.UserID)
	if err != nil {
		t.Fatal(err)
	}
	return user
}

func realProviderConfiguration(ldapURL, issuer string, includeLDAP, includeOIDC bool) config.AuthenticationConfig {
	authConfig := config.AuthenticationConfig{PublicURL: "http://127.0.0.1:18080"}
	if includeLDAP {
		ldapConfig := realLDAPConfig(ldapURL)
		authConfig.Providers = append(authConfig.Providers, config.AuthProviderConfig{
			Name: "openldap",
			Type: "ldap",
			LDAP: &ldapConfig,
		})
	}
	if includeOIDC {
		oidcConfig := config.OIDCAuthConfig{
			IssuerURL:        issuer,
			ClientID:         "dashboard-client",
			ClientSecret:     "dashboard-secret",
			Scopes:           []string{"groups"},
			UsernameClaim:    "preferred_username",
			DisplayNameClaim: "name",
			EmailClaim:       "email",
			GroupsClaim:      "groups",
			AdminGroups:      []string{"ship_crew"},
		}
		authConfig.Providers = append(authConfig.Providers, config.AuthProviderConfig{
			Name: "dex",
			Type: "oidc",
			OIDC: &oidcConfig,
		})
	}
	return authConfig
}

func newRealCombinationAuth(t *testing.T, authConfig config.AuthenticationConfig, waitForLDAP bool) (*Auth, *store.Store) {
	t.Helper()
	st := testStoreForAuth(t)
	set, err := BuildProviderSet(authConfig, st)
	if err != nil {
		t.Fatal(err)
	}
	a := NewWithProviderSet(st, "enterprise-integration-secret", false, set)
	t.Cleanup(func() {
		a.loginLimiter.Stop()
		a.localLoginLimiter.Stop()
	})
	if waitForLDAP {
		provider, ok := set.Password[0].(*LDAPProvider)
		if !ok {
			t.Fatal("first password provider is not OpenLDAP")
		}
		waitForLDAPAuthentication(t, provider)
	}
	return a, st
}

func createRealLocalUser(t *testing.T, st *store.Store, username, password string) *store.User {
	t.Helper()
	user, err := st.CreateUser(username, password, store.RoleAdmin)
	if err != nil {
		t.Fatal(err)
	}
	return user
}

func assertRealPasswordLogin(t *testing.T, a *Auth, localOnly bool, username, password, wantProvider string) {
	t.Helper()
	target := "/api/login"
	if localOnly {
		target = "/api/auth/local/login"
	}
	body, err := json.Marshal(map[string]string{"username": username, "password": password})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, target, strings.NewReader(string(body)))
	w := httptest.NewRecorder()
	if localOnly {
		a.HandleLocalLogin(w, req)
	} else {
		a.HandleLogin(w, req)
	}
	if w.Code != http.StatusOK {
		t.Fatalf("local_only=%v username=%q status=%d body=%s", localOnly, username, w.Code, w.Body.String())
	}
	session := cookieNamed(w.Result().Cookies(), "session")
	if session == nil {
		t.Fatal("password login did not issue a session cookie")
	}
	claims, err := a.ValidateToken(session.Value)
	if err != nil {
		t.Fatal(err)
	}
	if claims.Username != username || claims.AuthProvider != wantProvider {
		t.Fatalf("claims = %+v, want username=%q provider=%q", claims, username, wantProvider)
	}
}

func assertProviderInfo(t *testing.T, a *Auth, want []ProviderInfo) {
	t.Helper()
	got := a.ProviderInfo()
	if len(got) != len(want) {
		t.Fatalf("provider info = %+v, want %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("provider info[%d] = %+v, want %+v", i, got[i], want[i])
		}
	}
}

func realLDAPConfig(ldapURL string) config.LDAPAuthConfig {
	return config.LDAPAuthConfig{
		URL:                  ldapURL,
		AllowPlaintext:       true,
		BindDN:               realLDAPAdminDN,
		BindPassword:         realLDAPAdminPassword,
		UserBaseDN:           "ou=people,dc=planetexpress,dc=com",
		UserFilter:           "(uid={username})",
		UsernameAttribute:    "uid",
		SubjectAttribute:     "entryUUID",
		DisplayNameAttribute: "cn",
		EmailAttribute:       "mail",
		GroupAttribute:       "memberOf",
		AdminGroups:          []string{"ship_crew"},
		Timeout:              2 * time.Second,
	}
}

func waitForLDAPAuthentication(t *testing.T, provider *LDAPProvider) {
	t.Helper()
	deadline := time.Now().Add(60 * time.Second)
	var lastErr error
	for time.Now().Before(deadline) {
		_, result, err := provider.Authenticate(context.Background(), "fry", realLDAPUserPassword)
		if err == nil && result == ProviderAuthenticated {
			return
		}
		lastErr = err
		time.Sleep(250 * time.Millisecond)
	}
	t.Fatalf("OpenLDAP did not become ready: %v", lastErr)
}

func waitForHTTP(t *testing.T, endpoint string) {
	t.Helper()
	deadline := time.Now().Add(60 * time.Second)
	var lastErr error
	for time.Now().Before(deadline) {
		response, err := http.Get(endpoint)
		if err == nil {
			_ = response.Body.Close()
			if response.StatusCode == http.StatusOK {
				return
			}
			lastErr = errors.New(response.Status)
		} else {
			lastErr = err
		}
		time.Sleep(250 * time.Millisecond)
	}
	t.Fatalf("OIDC provider did not become ready: %v", lastErr)
}

func logIntoDex(t *testing.T, authorizationURL string) *url.URL {
	t.Helper()
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{
		Jar: jar,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if req.URL.Host == "127.0.0.1:18080" {
				return http.ErrUseLastResponse
			}
			if len(via) >= 20 {
				return errors.New("too many Dex redirects")
			}
			return nil
		},
	}
	loginPage, err := client.Get(authorizationURL)
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(loginPage.Body)
	_ = loginPage.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	if loginPage.StatusCode != http.StatusOK {
		t.Fatalf("Dex login page status = %d, body: %s", loginPage.StatusCode, body)
	}
	action := dexLoginFormAction(body)
	postURL, err := loginPage.Request.URL.Parse(action)
	if err != nil {
		t.Fatal(err)
	}
	form := url.Values{"login": {"fry"}, "password": {realLDAPUserPassword}}
	response, err := client.PostForm(postURL.String(), form)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode < 300 || response.StatusCode >= 400 {
		failedBody, _ := io.ReadAll(response.Body)
		t.Fatalf("Dex login status = %d, body: %s", response.StatusCode, failedBody)
	}
	location, err := response.Location()
	if err != nil {
		t.Fatal(err)
	}
	if location.Host != "127.0.0.1:18080" || location.Query().Get("code") == "" || location.Query().Get("state") == "" {
		t.Fatalf("Dex callback URL = %s", location)
	}
	return location
}

var dexFormActionPattern = regexp.MustCompile(`(?i)<form[^>]*action=["']([^"']+)["']`)

func dexLoginFormAction(body []byte) string {
	match := dexFormActionPattern.FindSubmatch(body)
	if len(match) != 2 {
		return ""
	}
	return html.UnescapeString(string(match[1]))
}

func cookieNamed(cookies []*http.Cookie, name string) *http.Cookie {
	for _, cookie := range cookies {
		if cookie.Name == name {
			return cookie
		}
	}
	return nil
}

func requiredIntegrationEnvironment(t *testing.T, name string) string {
	t.Helper()
	value := os.Getenv(name)
	if value == "" {
		t.Skipf("%s is not configured; use make test-enterprise-auth", name)
	}
	return value
}
