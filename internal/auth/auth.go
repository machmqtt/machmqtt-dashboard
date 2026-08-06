package auth

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/noodlebit/machmqtt-dashboard/internal/store"
)

const sessionIssuer = "machmqtt-dashboard"

func newTokenID() (string, error) {
	value := make([]byte, 16)
	if _, err := rand.Read(value); err != nil {
		return "", fmt.Errorf("generate session identifier: %w", err)
	}
	return hex.EncodeToString(value), nil
}

type contextKey int

const userKey contextKey = 0

type Claims struct {
	jwt.RegisteredClaims
	UserID         int64  `json:"uid"`
	Username       string `json:"usr"`
	Role           string `json:"role"`
	AuthProvider   string `json:"provider"`
	SessionVersion int64  `json:"session_version"`
}

type Auth struct {
	store             *store.Store
	secret            []byte
	cookieTTL         time.Duration
	secureCookies     bool
	loginLimiter      *LoginRateLimiter
	localLoginLimiter *LoginRateLimiter
	providers         []PasswordProvider
	oidcProviders     map[string]*OIDCProvider
	providerInfo      []ProviderInfo
	trustedProxies    []*net.IPNet
	log               *slog.Logger
	metricsMu         sync.Mutex
	authEvents        map[string]uint64
	providerNanos     map[string]uint64
}

func (a *Auth) SetLogger(log *slog.Logger) {
	if log != nil {
		a.log = log
	}
}

func (a *Auth) Close() {
	a.loginLimiter.Stop()
	if a.localLoginLimiter != a.loginLimiter {
		a.localLoginLimiter.Stop()
	}
}

func (a *Auth) SetTrustedProxyCIDRs(cidrs []string) error {
	a.trustedProxies = nil
	for _, cidr := range cidrs {
		_, network, err := net.ParseCIDR(cidr)
		if err != nil {
			return fmt.Errorf("parse trusted proxy CIDR %q: %w", cidr, err)
		}
		a.trustedProxies = append(a.trustedProxies, network)
	}
	return nil
}

func New(s *store.Store, secret string, secureCookies bool) *Auth {
	return NewWithProviders(s, secret, secureCookies, nil)
}

// NewWithProviders constructs authentication with external password providers
// in lookup order. Local authentication is always the final fallback.
func NewWithProviders(s *store.Store, secret string, secureCookies bool, providers []PasswordProvider) *Auth {
	set := ProviderSet{Password: providers}
	for _, provider := range providers {
		set.Info = append(set.Info, ProviderInfo{Name: provider.Name(), Type: provider.Type()})
	}
	return NewWithProviderSet(s, secret, secureCookies, set)
}

func NewWithProviderSet(s *store.Store, secret string, secureCookies bool, providers ProviderSet) *Auth {
	oidcProviders := make(map[string]*OIDCProvider, len(providers.OIDC))
	for _, provider := range providers.OIDC {
		oidcProviders[provider.Name()] = provider
	}
	return &Auth{
		store:             s,
		secret:            []byte(secret),
		cookieTTL:         24 * time.Hour,
		secureCookies:     secureCookies,
		loginLimiter:      NewLoginRateLimiter(10, 5*time.Minute),
		localLoginLimiter: NewLoginRateLimiter(10, 5*time.Minute),
		providers:         append([]PasswordProvider(nil), providers.Password...),
		oidcProviders:     oidcProviders,
		providerInfo:      append([]ProviderInfo(nil), providers.Info...),
		log:               slog.Default(),
		authEvents:        make(map[string]uint64),
		providerNanos:     make(map[string]uint64),
	}
}

func (a *Auth) recordProviderDuration(provider string, duration time.Duration) {
	a.metricsMu.Lock()
	a.providerNanos[provider] += uint64(duration.Nanoseconds())
	a.metricsMu.Unlock()
}

func (a *Auth) recordAuthEvent(provider, result string) {
	a.metricsMu.Lock()
	a.authEvents[provider+"\x00"+result]++
	a.metricsMu.Unlock()
}

func (a *Auth) Metrics() map[string]uint64 {
	a.metricsMu.Lock()
	defer a.metricsMu.Unlock()
	result := make(map[string]uint64, len(a.authEvents))
	for key, value := range a.authEvents {
		result[key] = value
	}
	return result
}

func (a *Auth) ProviderDurations() map[string]uint64 {
	a.metricsMu.Lock()
	defer a.metricsMu.Unlock()
	result := make(map[string]uint64, len(a.providerNanos))
	for key, value := range a.providerNanos {
		result[key] = value
	}
	return result
}

func (a *Auth) RateLimiterMetrics() (ordered, local RateLimiterStats) {
	return a.loginLimiter.Stats(), a.localLoginLimiter.Stats()
}

func (a *Auth) OIDCFlowMetrics() map[string]OIDCFlowStats {
	result := make(map[string]OIDCFlowStats, len(a.oidcProviders))
	for name, provider := range a.oidcProviders {
		result[name] = provider.FlowStats()
	}
	return result
}

func (a *Auth) IssueToken(user *store.User) (string, error) {
	now := time.Now()
	tokenID, err := newTokenID()
	if err != nil {
		return "", err
	}
	claims := Claims{
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    sessionIssuer,
			Subject:   fmt.Sprintf("user:%d", user.ID),
			Audience:  jwt.ClaimStrings{sessionIssuer},
			ID:        tokenID,
			IssuedAt:  jwt.NewNumericDate(now),
			NotBefore: jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(a.cookieTTL)),
		},
		UserID:         user.ID,
		Username:       user.Username,
		Role:           user.Role,
		AuthProvider:   user.AuthProvider,
		SessionVersion: user.SessionVersion,
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString(a.secret)
}

func (a *Auth) ValidateToken(tokenStr string) (*Claims, error) {
	claims := &Claims{}
	_, err := jwt.ParseWithClaims(tokenStr, claims, func(t *jwt.Token) (any, error) {
		if t.Method.Alg() != jwt.SigningMethodHS256.Alg() {
			return nil, fmt.Errorf("unexpected signing method: %v", t.Header["alg"])
		}
		return a.secret, nil
	}, jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}), jwt.WithIssuer(sessionIssuer), jwt.WithAudience(sessionIssuer), jwt.WithExpirationRequired())
	if err != nil {
		return nil, err
	}
	return claims, nil
}

func (a *Auth) SetSessionCookie(w http.ResponseWriter, token string) {
	http.SetCookie(w, &http.Cookie{
		Name:     "session",
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		Secure:   a.secureCookies,
		SameSite: http.SameSiteStrictMode,
		MaxAge:   int(a.cookieTTL.Seconds()),
		Expires:  time.Now().Add(a.cookieTTL),
	})
}

func (a *Auth) ClearSessionCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     "session",
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		Secure:   a.secureCookies,
		SameSite: http.SameSiteStrictMode,
		MaxAge:   -1,
		Expires:  time.Unix(1, 0),
	})
}

// Middleware rejects unauthenticated requests. Beyond verifying the JWT
// signature it re-reads the user's authorization state from the database on
// every request, so a session is rejected the moment its user is deleted, its
// password changes, or it is explicitly logged out (all bump token_version),
// and role changes take effect immediately.
func (a *Auth) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie("session")
		if err != nil {
			writeJSONError(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
			return
		}
		claims, err := a.ValidateToken(cookie.Value)
		if err != nil {
			writeJSONError(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
			return
		}
		// Resolve the user on every request so deletion and local role changes
		// invalidate existing self-contained tokens immediately.
		user, err := a.store.GetUser(claims.UserID)
		if err != nil {
			writeJSONError(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
			return
		}
		if claims.SessionVersion != user.SessionVersion {
			writeJSONError(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
			return
		}
		claims.Username = user.Username
		claims.Role = user.Role
		claims.AuthProvider = user.AuthProvider
		claims.SessionVersion = user.SessionVersion
		ctx := context.WithValue(r.Context(), userKey, claims)
		if user.AuthProvider == store.ProviderLocal && user.MustChangePassword && !passwordRotationAllowed(r, user.ID) {
			writeJSONError(w, `{"error":"password change required"}`, http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func passwordRotationAllowed(r *http.Request, userID int64) bool {
	if r.Method == http.MethodGet && r.URL.Path == "/api/me" {
		return true
	}
	if r.Method == http.MethodPost && r.URL.Path == "/api/logout" {
		return true
	}
	return r.Method == http.MethodPut && r.URL.Path == fmt.Sprintf("/api/users/%d/password", userID)
}

// AdminMiddleware rejects non-admin requests. Must be applied after Middleware.
func AdminMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		claims := UserFromContext(r.Context())
		if claims == nil || claims.Role != store.RoleAdmin {
			writeJSONError(w, `{"error":"forbidden: admin role required"}`, http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func UserFromContext(ctx context.Context) *Claims {
	claims, _ := ctx.Value(userKey).(*Claims)
	return claims
}

func (a *Auth) Store() *store.Store {
	return a.store
}
