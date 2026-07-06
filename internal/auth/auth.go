package auth

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/noodlebit/machmqtt-dashboard/internal/store"
)

type contextKey int

const userKey contextKey = 0

type Claims struct {
	jwt.RegisteredClaims
	UserID   int64  `json:"uid"`
	Username string `json:"usr"`
	Role     string `json:"role"`
	// TokenVersion must match the user's current token_version in the DB, or the
	// session is treated as revoked.
	TokenVersion int64 `json:"tv"`
}

type Auth struct {
	store         *store.Store
	secret        []byte
	cookieTTL     time.Duration
	secureCookies bool
	trustProxy    bool
	log           *slog.Logger
	loginLimiter  *LoginRateLimiter
}

func New(s *store.Store, secret string, secureCookies, trustProxy bool, log *slog.Logger) *Auth {
	if log == nil {
		log = slog.Default()
	}
	return &Auth{
		store:         s,
		secret:        []byte(secret),
		cookieTTL:     24 * time.Hour,
		secureCookies: secureCookies,
		trustProxy:    trustProxy,
		log:           log,
		loginLimiter:  NewLoginRateLimiter(10, 5*time.Minute),
	}
}

func (a *Auth) IssueToken(user *store.User) (string, error) {
	now := time.Now()
	claims := Claims{
		RegisteredClaims: jwt.RegisteredClaims{
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(a.cookieTTL)),
		},
		UserID:       user.ID,
		Username:     user.Username,
		Role:         user.Role,
		TokenVersion: user.TokenVersion,
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString(a.secret)
}

func (a *Auth) ValidateToken(tokenStr string) (*Claims, error) {
	token, err := jwt.ParseWithClaims(tokenStr, &Claims{}, func(t *jwt.Token) (any, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", t.Header["alg"])
		}
		return a.secret, nil
	})
	if err != nil {
		return nil, err
	}
	claims, ok := token.Claims.(*Claims)
	if !ok {
		return nil, fmt.Errorf("invalid claims")
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
			http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
			return
		}
		claims, err := a.ValidateToken(cookie.Value)
		if err != nil {
			http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
			return
		}

		st, err := a.store.GetSessionState(claims.UserID)
		if errors.Is(err, sql.ErrNoRows) {
			// User was deleted; its token must stop working immediately.
			a.ClearSessionCookie(w)
			http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
			return
		}
		if err != nil {
			a.log.Warn("auth: session state lookup failed", "err", err)
			http.Error(w, `{"error":"internal error"}`, http.StatusInternalServerError)
			return
		}
		if st.TokenVersion != claims.TokenVersion {
			// Password changed / logged out elsewhere / session revoked.
			a.ClearSessionCookie(w)
			http.Error(w, `{"error":"session expired"}`, http.StatusUnauthorized)
			return
		}
		// Refresh role from the DB so demotions take effect without re-login.
		claims.Role = st.Role

		if st.MustChangePassword && !allowedDuringPasswordChange(r) {
			http.Error(w, `{"error":"password change required"}`, http.StatusForbidden)
			return
		}

		ctx := context.WithValue(r.Context(), userKey, claims)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// allowedDuringPasswordChange lists the only requests permitted while a user
// still has must_change_password set: viewing their own identity, changing
// their password, and logging out.
func allowedDuringPasswordChange(r *http.Request) bool {
	p := r.URL.Path
	switch {
	case r.Method == http.MethodGet && p == "/api/me":
		return true
	case r.Method == http.MethodPost && p == "/api/logout":
		return true
	case r.Method == http.MethodPut && strings.HasPrefix(p, "/api/users/") && strings.HasSuffix(p, "/password"):
		return true
	}
	return false
}

// AdminMiddleware rejects non-admin requests. Must be applied after Middleware.
func AdminMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		claims := UserFromContext(r.Context())
		if claims == nil || claims.Role != store.RoleAdmin {
			http.Error(w, `{"error":"forbidden: admin role required"}`, http.StatusForbidden)
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
