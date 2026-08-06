package auth

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/noodlebit/machmqtt-dashboard/internal/config"
	"github.com/noodlebit/machmqtt-dashboard/internal/store"
	"golang.org/x/oauth2"
)

var (
	ErrOIDCInvalidResponse = errors.New("invalid OIDC response")
	ErrOIDCForbidden       = errors.New("OIDC identity is not authorized")
)

type oidcFlow struct {
	nonce    string
	verifier string
	binding  string
	created  time.Time
}

type OIDCProvider struct {
	name      string
	publicURL string
	config    config.OIDCAuthConfig
	store     *store.Store

	runtimeMu sync.Mutex
	provider  *oidc.Provider
	verifier  *oidc.IDTokenVerifier
	oauth2    *oauth2.Config

	flowMu        sync.Mutex
	flows         map[string]oidcFlow
	flowEvictions atomic.Uint64
}

type OIDCFlowStats struct {
	Active    int
	Evictions uint64
}

func NewOIDCProvider(name, publicURL string, cfg config.OIDCAuthConfig, st *store.Store) *OIDCProvider {
	return &OIDCProvider{
		name:      name,
		publicURL: strings.TrimRight(publicURL, "/"),
		config:    cfg,
		store:     st,
		flows:     make(map[string]oidcFlow),
	}
}

func (p *OIDCProvider) Name() string { return p.name }

func (p *OIDCProvider) BeginLogin(w http.ResponseWriter, r *http.Request) error {
	if err := p.ensureRuntime(r.Context()); err != nil {
		return err
	}
	state, err := randomToken(32)
	if err != nil {
		return err
	}
	nonce, err := randomToken(32)
	if err != nil {
		return err
	}
	verifier := oauth2.GenerateVerifier()
	binding, err := randomToken(32)
	if err != nil {
		return err
	}
	p.storeFlow(state, oidcFlow{nonce: nonce, verifier: verifier, binding: binding, created: time.Now()})
	http.SetCookie(w, &http.Cookie{
		Name:     p.flowCookieName(),
		Value:    binding,
		Path:     p.callbackPath(),
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   600,
	})
	loginURL := p.oauth2.AuthCodeURL(
		state,
		oidc.Nonce(nonce),
		oauth2.S256ChallengeOption(verifier),
	)
	http.Redirect(w, r, loginURL, http.StatusFound)
	return nil
}

func (p *OIDCProvider) CompleteLogin(ctx context.Context, state, code, binding string) (*store.User, error) {
	flow, ok := p.consumeFlow(state)
	if !ok || code == "" || binding == "" || binding != flow.binding {
		return nil, ErrOIDCInvalidResponse
	}
	if err := p.ensureRuntime(ctx); err != nil {
		return nil, err
	}
	token, err := p.oauth2.Exchange(ctx, code, oauth2.VerifierOption(flow.verifier))
	if err != nil {
		return nil, fmt.Errorf("exchange OIDC code: %w", err)
	}
	rawIDToken, ok := token.Extra("id_token").(string)
	if !ok || rawIDToken == "" {
		return nil, ErrOIDCInvalidResponse
	}
	idToken, err := p.verifier.Verify(ctx, rawIDToken)
	if err != nil {
		return nil, fmt.Errorf("verify OIDC ID token: %w", err)
	}
	if idToken.Nonce != flow.nonce {
		return nil, ErrOIDCInvalidResponse
	}

	claims := make(map[string]any)
	if err := idToken.Claims(&claims); err != nil {
		return nil, fmt.Errorf("decode OIDC claims: %w", err)
	}
	groups := stringSliceClaim(claims[p.config.GroupsClaim])
	role := roleForExternalGroups(groups, p.config.AdminGroups, p.config.ViewerGroups, p.config.DefaultRole)
	if role == "" {
		return nil, ErrOIDCForbidden
	}
	username := stringClaim(claims[p.config.UsernameClaim])
	email := stringClaim(claims[p.config.EmailClaim])
	if username == "" {
		username = email
	}
	if username == "" {
		username = idToken.Subject
	}
	return p.store.UpsertExternalUser(
		p.name,
		idToken.Subject,
		username,
		stringClaim(claims[p.config.DisplayNameClaim]),
		email,
		role,
	)
}

func (p *OIDCProvider) ensureRuntime(ctx context.Context) error {
	p.runtimeMu.Lock()
	defer p.runtimeMu.Unlock()
	if p.provider != nil {
		return nil
	}
	provider, err := oidc.NewProvider(ctx, p.config.IssuerURL)
	if err != nil {
		return fmt.Errorf("discover OIDC provider: %w", err)
	}
	scopes := uniqueStrings(append([]string{oidc.ScopeOpenID, "profile", "email"}, p.config.Scopes...))
	p.provider = provider
	p.verifier = provider.Verifier(&oidc.Config{ClientID: p.config.ClientID})
	p.oauth2 = &oauth2.Config{
		ClientID:     p.config.ClientID,
		ClientSecret: p.config.ClientSecret,
		Endpoint:     provider.Endpoint(),
		RedirectURL:  p.publicURL + p.callbackPath(),
		Scopes:       scopes,
	}
	return nil
}

func (p *OIDCProvider) storeFlow(state string, flow oidcFlow) {
	p.flowMu.Lock()
	defer p.flowMu.Unlock()
	cutoff := time.Now().Add(-10 * time.Minute)
	for key, existing := range p.flows {
		if existing.created.Before(cutoff) {
			delete(p.flows, key)
			p.flowEvictions.Add(1)
		}
	}
	if len(p.flows) >= 10000 {
		var oldestKey string
		var oldestTime time.Time
		for key, existing := range p.flows {
			if oldestKey == "" || existing.created.Before(oldestTime) {
				oldestKey = key
				oldestTime = existing.created
			}
		}
		delete(p.flows, oldestKey)
		p.flowEvictions.Add(1)
	}
	p.flows[state] = flow
}

func (p *OIDCProvider) FlowStats() OIDCFlowStats {
	p.flowMu.Lock()
	defer p.flowMu.Unlock()
	return OIDCFlowStats{Active: len(p.flows), Evictions: p.flowEvictions.Load()}
}

func (p *OIDCProvider) callbackPath() string {
	return "/api/auth/oidc/" + p.name + "/callback"
}

func (p *OIDCProvider) flowCookieName() string {
	return "oidc_flow_" + p.name
}

func (p *OIDCProvider) consumeFlow(state string) (oidcFlow, bool) {
	p.flowMu.Lock()
	defer p.flowMu.Unlock()
	flow, ok := p.flows[state]
	delete(p.flows, state)
	if !ok || flow.created.Before(time.Now().Add(-10*time.Minute)) {
		return oidcFlow{}, false
	}
	return flow, true
}

func randomToken(size int) (string, error) {
	value := make([]byte, size)
	if _, err := rand.Read(value); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(value), nil
}

func stringClaim(value any) string {
	result, _ := value.(string)
	return result
}

func stringSliceClaim(value any) []string {
	switch typed := value.(type) {
	case string:
		return []string{typed}
	case []string:
		return typed
	case []any:
		values := make([]string, 0, len(typed))
		for _, value := range typed {
			if text, ok := value.(string); ok {
				values = append(values, text)
			}
		}
		return values
	case json.RawMessage:
		var values []string
		_ = json.Unmarshal(typed, &values)
		return values
	default:
		return nil
	}
}

func roleForExternalGroups(actual, admins, viewers []string, defaultRole string) string {
	if groupMatch(actual, admins) {
		return store.RoleAdmin
	}
	if groupMatch(actual, viewers) {
		return store.RoleViewer
	}
	return defaultRole
}

func uniqueStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return result
}
