package auth

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/noodlebit/machmqtt-dashboard/internal/store"
)

var (
	ErrInvalidCredentials  = errors.New("invalid credentials")
	ErrProviderUnavailable = errors.New("authentication provider unavailable")
)

// ProviderResult distinguishes a missing identity from rejected credentials.
// Only a missing identity is allowed to continue to the next provider.
type ProviderResult int

const (
	ProviderNoMatch ProviderResult = iota
	ProviderAuthenticated
	ProviderRejected
)

// PasswordProvider authenticates username/password identities such as LDAP or
// Active Directory. Providers are called in configuration order. OIDC uses a
// redirect flow and therefore does not implement this interface.
type PasswordProvider interface {
	Name() string
	Type() string
	Authenticate(ctx context.Context, username, password string) (*store.User, ProviderResult, error)
}

// AuthenticatePassword evaluates external password providers in order and
// falls back to the local store only when none of them owns the identity.
func (a *Auth) AuthenticatePassword(ctx context.Context, username, password string) (*store.User, error) {
	for _, provider := range a.providers {
		started := time.Now()
		user, result, err := provider.Authenticate(ctx, username, password)
		a.recordProviderDuration(provider.Name(), time.Since(started))
		if err != nil {
			a.recordAuthEvent(provider.Name(), "unavailable")
			return nil, fmt.Errorf("%w: %s: %v", ErrProviderUnavailable, provider.Name(), err)
		}
		switch result {
		case ProviderNoMatch:
			a.recordAuthEvent(provider.Name(), "no_match")
			continue
		case ProviderAuthenticated:
			if user == nil {
				return nil, fmt.Errorf("%w: %s returned no user", ErrProviderUnavailable, provider.Name())
			}
			a.recordAuthEvent(provider.Name(), "success")
			return user, nil
		case ProviderRejected:
			a.recordAuthEvent(provider.Name(), "rejected")
			return nil, ErrInvalidCredentials
		default:
			return nil, fmt.Errorf("%w: %s returned an invalid result", ErrProviderUnavailable, provider.Name())
		}
	}
	return a.AuthenticateLocal(username, password)
}

// AuthenticateLocal bypasses all external providers. It backs the explicit
// break-glass login route and is intentionally always available.
func (a *Auth) AuthenticateLocal(username, password string) (*store.User, error) {
	started := time.Now()
	defer func() { a.recordProviderDuration("local", time.Since(started)) }()
	user, err := a.store.Authenticate(username, password)
	if err != nil {
		return nil, ErrInvalidCredentials
	}
	return user, nil
}

type ProviderInfo struct {
	Name     string `json:"name"`
	Type     string `json:"type"`
	LoginURL string `json:"login_url,omitempty"`
}

func (a *Auth) ProviderInfo() []ProviderInfo {
	return append([]ProviderInfo(nil), a.providerInfo...)
}

type ProviderSet struct {
	Password []PasswordProvider
	OIDC     []*OIDCProvider
	Info     []ProviderInfo
}
