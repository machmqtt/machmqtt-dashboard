package auth

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/noodlebit/machmqtt-dashboard/internal/store"
)

func writeJSONError(w http.ResponseWriter, payload string, status int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write([]byte(payload + "\n"))
}

func (a *Auth) HandleLogin(w http.ResponseWriter, r *http.Request) {
	a.handlePasswordLogin(w, r, false)
}

// HandleLocalLogin is the explicit break-glass login. It never queries an
// external provider, even when an external identity has the same username.
func (a *Auth) HandleLocalLogin(w http.ResponseWriter, r *http.Request) {
	a.handlePasswordLogin(w, r, true)
}

func (a *Auth) handlePasswordLogin(w http.ResponseWriter, r *http.Request, localOnly bool) {
	limiter := a.loginLimiter
	if localOnly {
		limiter = a.localLoginLimiter
	}
	if !limiter.Allow(clientIP(r, a.trustedProxies)) {
		provider := "ordered"
		if localOnly {
			provider = "local"
		}
		a.recordAuthEvent(provider, "rate_limited")
		writeJSONError(w, `{"error":"too many login attempts, try again later"}`, http.StatusTooManyRequests)
		return
	}

	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSONError(w, `{"error":"invalid request"}`, http.StatusBadRequest)
		return
	}

	var user *store.User
	var err error
	if localOnly {
		user, err = a.AuthenticateLocal(req.Username, req.Password)
	} else {
		user, err = a.AuthenticatePassword(r.Context(), req.Username, req.Password)
	}
	if err != nil {
		if errors.Is(err, ErrProviderUnavailable) {
			a.recordAuthEvent("ordered", "unavailable")
			a.log.Warn("authentication provider unavailable", "username", req.Username, "reason", "provider_unavailable")
			writeJSONError(w, `{"error":"authentication provider unavailable"}`, http.StatusServiceUnavailable)
			return
		}
		a.log.Warn("login failed", "username", req.Username, "local_only", localOnly)
		provider := "ordered"
		if localOnly {
			provider = "local"
		}
		a.recordAuthEvent(provider, "failure")
		writeJSONError(w, `{"error":"invalid credentials"}`, http.StatusUnauthorized)
		return
	}

	token, err := a.IssueToken(user)
	if err != nil {
		writeJSONError(w, `{"error":"internal error"}`, http.StatusInternalServerError)
		return
	}

	a.SetSessionCookie(w, token)
	a.recordAuthEvent(user.AuthProvider, "success")
	a.log.Info("login succeeded", "username", user.Username, "provider", user.AuthProvider, "local_only", localOnly)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(user)
}

func (a *Auth) HandleProviders(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"providers":        a.ProviderInfo(),
		"local_login_path": "/login/local",
	})
}

func (a *Auth) HandleOIDCLogin(w http.ResponseWriter, r *http.Request) {
	if !a.loginLimiter.Allow(clientIP(r, a.trustedProxies)) {
		a.recordAuthEvent("oidc", "rate_limited")
		writeJSONError(w, `{"error":"too many login attempts, try again later"}`, http.StatusTooManyRequests)
		return
	}
	provider := a.oidcProviders[r.PathValue("provider")]
	if provider == nil {
		writeJSONError(w, `{"error":"authentication provider not found"}`, http.StatusNotFound)
		return
	}
	started := time.Now()
	err := provider.BeginLogin(w, r)
	a.recordProviderDuration(provider.Name(), time.Since(started))
	if err != nil {
		a.recordAuthEvent(provider.Name(), "unavailable")
		a.log.Warn("OIDC provider unavailable", "provider", provider.Name(), "reason", "provider_unavailable")
		writeJSONError(w, `{"error":"authentication provider unavailable"}`, http.StatusServiceUnavailable)
	}
}

func (a *Auth) HandleOIDCCallback(w http.ResponseWriter, r *http.Request) {
	provider := a.oidcProviders[r.PathValue("provider")]
	if provider == nil {
		writeJSONError(w, `{"error":"authentication provider not found"}`, http.StatusNotFound)
		return
	}
	flowCookie, _ := r.Cookie(provider.flowCookieName())
	binding := ""
	if flowCookie != nil {
		binding = flowCookie.Value
	}
	http.SetCookie(w, &http.Cookie{
		Name:     provider.flowCookieName(),
		Value:    "",
		Path:     provider.callbackPath(),
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   -1,
	})
	started := time.Now()
	user, err := provider.CompleteLogin(r.Context(), r.URL.Query().Get("state"), r.URL.Query().Get("code"), binding)
	a.recordProviderDuration(provider.Name(), time.Since(started))
	if err != nil {
		a.recordAuthEvent(provider.Name(), "failure")
		reason := "invalid_callback"
		if errors.Is(err, ErrOIDCForbidden) {
			reason = "forbidden"
		}
		a.log.Warn("OIDC login failed", "provider", provider.Name(), "reason", reason)
		status := http.StatusUnauthorized
		if errors.Is(err, ErrOIDCForbidden) {
			status = http.StatusForbidden
		}
		writeJSONError(w, `{"error":"single sign-on failed"}`, status)
		return
	}
	token, err := a.IssueToken(user)
	if err != nil {
		writeJSONError(w, `{"error":"internal error"}`, http.StatusInternalServerError)
		return
	}
	a.SetSessionCookie(w, token)
	a.recordAuthEvent(user.AuthProvider, "success")
	a.log.Info("OIDC login succeeded", "username", user.Username, "provider", user.AuthProvider)
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (a *Auth) HandleLogout(w http.ResponseWriter, r *http.Request) {
	// Bump token_version so the just-cleared cookie can't be replayed (signs the
	// user out of any other active sessions too).
	if claims := UserFromContext(r.Context()); claims != nil {
		if err := a.store.BumpTokenVersion(claims.UserID); err != nil {
			a.log.Warn("logout: token invalidation failed", "err", err)
		}
	}
	a.ClearSessionCookie(w)
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(`{"ok":true}`))
}

func (a *Auth) HandleMe(w http.ResponseWriter, r *http.Request) {
	claims := UserFromContext(r.Context())
	if claims == nil {
		writeJSONError(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
		return
	}
	user, err := a.store.GetUser(claims.UserID)
	if err != nil {
		writeJSONError(w, `{"error":"user not found"}`, http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(user)
}

func (a *Auth) HandleChangePassword(w http.ResponseWriter, r *http.Request) {
	idStr := r.PathValue("id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		writeJSONError(w, `{"error":"invalid user id"}`, http.StatusBadRequest)
		return
	}

	claims := UserFromContext(r.Context())
	if claims == nil || claims.UserID != id {
		writeJSONError(w, `{"error":"forbidden"}`, http.StatusForbidden)
		return
	}

	var req struct {
		OldPassword string `json:"old_password"`
		NewPassword string `json:"new_password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSONError(w, `{"error":"invalid request"}`, http.StatusBadRequest)
		return
	}

	if len(req.NewPassword) < store.MinPasswordLength {
		http.Error(w, `{"error":"`+store.ErrPasswordTooShort.Error()+`"}`, http.StatusBadRequest)
		return
	}

	if err := a.store.ChangePassword(id, req.OldPassword, req.NewPassword); err != nil {
		writeJSONError(w, `{"error":"failed to change password"}`, http.StatusBadRequest)
		return
	}
	// ChangePassword bumped token_version, invalidating this request's own
	// session. Re-issue a fresh cookie so the user stays logged in.
	user, err := a.store.GetUser(id)
	if err != nil {
		writeJSONError(w, `{"error":"internal error"}`, http.StatusInternalServerError)
		return
	}
	token, err := a.IssueToken(user)
	if err != nil {
		writeJSONError(w, `{"error":"internal error"}`, http.StatusInternalServerError)
		return
	}
	a.SetSessionCookie(w, token)

	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(`{"ok":true}`))
}

// Admin-only handlers below.

func (a *Auth) HandleListUsers(w http.ResponseWriter, r *http.Request) {
	users, err := a.store.ListUsers()
	if err != nil {
		writeJSONError(w, `{"error":"internal error"}`, http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"users": users})
}

func (a *Auth) HandleCreateUser(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
		Role     string `json:"role"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSONError(w, `{"error":"invalid request"}`, http.StatusBadRequest)
		return
	}

	if req.Username == "" || req.Password == "" {
		writeJSONError(w, `{"error":"username and password are required"}`, http.StatusBadRequest)
		return
	}
	if len(req.Password) < store.MinPasswordLength {
		http.Error(w, `{"error":"`+store.ErrPasswordTooShort.Error()+`"}`, http.StatusBadRequest)
		return
	}
	if req.Role == "" {
		req.Role = store.RoleViewer
	}

	user, err := a.store.CreateUser(req.Username, req.Password, req.Role)
	if err != nil {
		writeJSONError(w, `{"error":"failed to create user"}`, http.StatusBadRequest)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(user)
}

func (a *Auth) HandleDeleteUser(w http.ResponseWriter, r *http.Request) {
	idStr := r.PathValue("id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		writeJSONError(w, `{"error":"invalid user id"}`, http.StatusBadRequest)
		return
	}

	// Prevent deleting yourself.
	claims := UserFromContext(r.Context())
	if claims != nil && claims.UserID == id {
		writeJSONError(w, `{"error":"cannot delete your own account"}`, http.StatusBadRequest)
		return
	}

	if err := a.store.DeleteUser(id); err != nil {
		writeJSONError(w, `{"error":"failed to delete user"}`, http.StatusBadRequest)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(`{"ok":true}`))
}
