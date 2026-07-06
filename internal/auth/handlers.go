package auth

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/noodlebit/machmqtt-dashboard/internal/store"
)

func (a *Auth) HandleLogin(w http.ResponseWriter, r *http.Request) {
	ip := clientIP(r, a.trustProxy)
	if !a.loginLimiter.Allow(ip) {
		a.log.Warn("login rate limited", "ip", ip)
		http.Error(w, `{"error":"too many login attempts, try again later"}`, http.StatusTooManyRequests)
		return
	}

	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid request"}`, http.StatusBadRequest)
		return
	}

	user, err := a.store.Authenticate(req.Username, req.Password)
	if err != nil {
		// Audit trail for failed logins / brute-force attempts (never log the password).
		a.log.Warn("login failed", "username", req.Username, "ip", ip)
		if errors.Is(err, store.ErrAccountLocked) {
			http.Error(w, `{"error":"account temporarily locked, try again later"}`, http.StatusTooManyRequests)
			return
		}
		http.Error(w, `{"error":"invalid credentials"}`, http.StatusUnauthorized)
		return
	}

	token, err := a.IssueToken(user)
	if err != nil {
		http.Error(w, `{"error":"internal error"}`, http.StatusInternalServerError)
		return
	}

	a.SetSessionCookie(w, token)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(user)
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
	w.Write([]byte(`{"ok":true}`))
}

func (a *Auth) HandleMe(w http.ResponseWriter, r *http.Request) {
	claims := UserFromContext(r.Context())
	if claims == nil {
		http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
		return
	}
	user, err := a.store.GetUser(claims.UserID)
	if err != nil {
		http.Error(w, `{"error":"user not found"}`, http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(user)
}

func (a *Auth) HandleChangePassword(w http.ResponseWriter, r *http.Request) {
	idStr := r.PathValue("id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		http.Error(w, `{"error":"invalid user id"}`, http.StatusBadRequest)
		return
	}

	claims := UserFromContext(r.Context())
	if claims == nil || claims.UserID != id {
		http.Error(w, `{"error":"forbidden"}`, http.StatusForbidden)
		return
	}

	var req struct {
		OldPassword string `json:"old_password"`
		NewPassword string `json:"new_password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid request"}`, http.StatusBadRequest)
		return
	}

	if len(req.NewPassword) < store.MinPasswordLength {
		http.Error(w, `{"error":"`+store.ErrPasswordTooShort.Error()+`"}`, http.StatusBadRequest)
		return
	}

	if err := a.store.ChangePassword(id, req.OldPassword, req.NewPassword); err != nil {
		http.Error(w, `{"error":"failed to change password"}`, http.StatusBadRequest)
		return
	}

	// ChangePassword bumped token_version, invalidating this request's own
	// session. Re-issue a fresh cookie so the user stays logged in.
	if user, err := a.store.GetUser(id); err == nil {
		if token, err := a.IssueToken(user); err == nil {
			a.SetSessionCookie(w, token)
		}
	}

	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(`{"ok":true}`))
}

// Admin-only handlers below.

func (a *Auth) HandleListUsers(w http.ResponseWriter, r *http.Request) {
	users, err := a.store.ListUsers()
	if err != nil {
		http.Error(w, `{"error":"internal error"}`, http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"users": users})
}

func (a *Auth) HandleCreateUser(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
		Role     string `json:"role"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid request"}`, http.StatusBadRequest)
		return
	}

	if req.Username == "" || req.Password == "" {
		http.Error(w, `{"error":"username and password are required"}`, http.StatusBadRequest)
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
		http.Error(w, `{"error":"failed to create user"}`, http.StatusBadRequest)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(user)
}

func (a *Auth) HandleDeleteUser(w http.ResponseWriter, r *http.Request) {
	idStr := r.PathValue("id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		http.Error(w, `{"error":"invalid user id"}`, http.StatusBadRequest)
		return
	}

	// Prevent deleting yourself.
	claims := UserFromContext(r.Context())
	if claims != nil && claims.UserID == id {
		http.Error(w, `{"error":"cannot delete your own account"}`, http.StatusBadRequest)
		return
	}

	if err := a.store.DeleteUser(id); err != nil {
		http.Error(w, `{"error":"failed to delete user"}`, http.StatusBadRequest)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(`{"ok":true}`))
}
