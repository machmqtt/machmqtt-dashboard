package auth

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"fmt"
	"net"
	"net/url"
	"os"
	"strings"
	"unicode/utf8"

	ldap "github.com/go-ldap/ldap/v3"
	"github.com/noodlebit/machmqtt-dashboard/internal/config"
	"github.com/noodlebit/machmqtt-dashboard/internal/store"
)

type LDAPProvider struct {
	name    string
	match   config.AuthMatchConfig
	config  config.LDAPAuthConfig
	store   *store.Store
	tls     *tls.Config
	dialURL string
}

func NewLDAPProvider(name string, match config.AuthMatchConfig, cfg config.LDAPAuthConfig, st *store.Store) (*LDAPProvider, error) {
	u, err := url.Parse(cfg.URL)
	if err != nil {
		return nil, fmt.Errorf("parse LDAP URL: %w", err)
	}
	tlsConfig := &tls.Config{
		MinVersion:         tls.VersionTLS12,
		ServerName:         u.Hostname(),
		InsecureSkipVerify: cfg.InsecureSkipVerify, // explicitly configured for test environments
	}
	if cfg.CAFile != "" {
		pem, err := os.ReadFile(cfg.CAFile)
		if err != nil {
			return nil, fmt.Errorf("read LDAP CA file: %w", err)
		}
		roots, err := x509.SystemCertPool()
		if err != nil || roots == nil {
			roots = x509.NewCertPool()
		}
		if !roots.AppendCertsFromPEM(pem) {
			return nil, fmt.Errorf("LDAP CA file contains no certificates")
		}
		tlsConfig.RootCAs = roots
	}
	return &LDAPProvider{
		name:    name,
		match:   match,
		config:  cfg,
		store:   st,
		tls:     tlsConfig,
		dialURL: cfg.URL,
	}, nil
}

func (p *LDAPProvider) Name() string { return p.name }
func (p *LDAPProvider) Type() string { return "ldap" }

func (p *LDAPProvider) Authenticate(ctx context.Context, username, password string) (*store.User, ProviderResult, error) {
	if !p.matches(username) {
		return nil, ProviderNoMatch, nil
	}
	if password == "" {
		return nil, ProviderRejected, nil
	}
	if err := ctx.Err(); err != nil {
		return nil, ProviderNoMatch, err
	}

	conn, err := p.dial()
	if err != nil {
		return nil, ProviderNoMatch, err
	}
	defer func() { _ = conn.Close() }()

	if p.config.BindDN != "" {
		if err := conn.Bind(p.config.BindDN, p.config.BindPassword); err != nil {
			return nil, ProviderNoMatch, fmt.Errorf("LDAP service bind: %w", err)
		}
	}

	entry, err := p.findUser(conn, username)
	if err != nil {
		return nil, ProviderNoMatch, err
	}
	if entry == nil {
		return nil, ProviderNoMatch, nil
	}

	groups, err := p.groupsForUser(conn, entry)
	if err != nil {
		return nil, ProviderNoMatch, err
	}
	if err := conn.Bind(entry.DN, password); err != nil {
		if ldap.IsErrorWithCode(err, ldap.LDAPResultInvalidCredentials) {
			return nil, ProviderRejected, nil
		}
		return nil, ProviderNoMatch, fmt.Errorf("LDAP user bind: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return nil, ProviderNoMatch, err
	}

	role := p.roleForGroups(groups)
	if role == "" {
		return nil, ProviderRejected, nil
	}
	subject := ldapSubject(entry, p.config.SubjectAttribute)
	if subject == "" {
		return nil, ProviderNoMatch, fmt.Errorf("LDAP user is missing subject attribute %q", p.config.SubjectAttribute)
	}
	canonicalUsername := entry.GetAttributeValue(p.config.UsernameAttribute)
	if canonicalUsername == "" {
		canonicalUsername = username
	}
	user, err := p.store.UpsertExternalUser(
		p.name,
		subject,
		canonicalUsername,
		entry.GetAttributeValue(p.config.DisplayNameAttribute),
		entry.GetAttributeValue(p.config.EmailAttribute),
		role,
	)
	if err != nil {
		return nil, ProviderNoMatch, err
	}
	return user, ProviderAuthenticated, nil
}

func (p *LDAPProvider) matches(username string) bool {
	if len(p.match.Domains) == 0 {
		return true
	}
	at := strings.LastIndexByte(username, '@')
	if at < 0 {
		return true
	}
	domain := strings.ToLower(strings.TrimSpace(username[at+1:]))
	for _, allowed := range p.match.Domains {
		if domain == allowed {
			return true
		}
	}
	return false
}

func (p *LDAPProvider) dial() (*ldap.Conn, error) {
	dialer := &net.Dialer{Timeout: p.config.Timeout}
	options := []ldap.DialOpt{ldap.DialWithDialer(dialer)}
	if strings.HasPrefix(strings.ToLower(p.dialURL), "ldaps://") {
		options = append(options, ldap.DialWithTLSConfig(p.tls.Clone()))
	}
	conn, err := ldap.DialURL(p.dialURL, options...)
	if err != nil {
		return nil, fmt.Errorf("dial LDAP: %w", err)
	}
	conn.SetTimeout(p.config.Timeout)
	if p.config.StartTLS {
		if err := conn.StartTLS(p.tls.Clone()); err != nil {
			_ = conn.Close()
			return nil, fmt.Errorf("start LDAP TLS: %w", err)
		}
	}
	return conn, nil
}

func (p *LDAPProvider) findUser(conn *ldap.Conn, username string) (*ldap.Entry, error) {
	filter := strings.ReplaceAll(p.config.UserFilter, "{username}", ldap.EscapeFilter(username))
	attributes := []string{
		p.config.UsernameAttribute,
		p.config.SubjectAttribute,
		p.config.DisplayNameAttribute,
		p.config.EmailAttribute,
		p.config.GroupAttribute,
	}
	request := ldap.NewSearchRequest(
		p.config.UserBaseDN,
		ldap.ScopeWholeSubtree,
		ldap.NeverDerefAliases,
		2,
		int(p.config.Timeout.Seconds()),
		false,
		filter,
		attributes,
		nil,
	)
	result, err := conn.Search(request)
	if err != nil {
		return nil, fmt.Errorf("search LDAP user: %w", err)
	}
	switch len(result.Entries) {
	case 0:
		return nil, nil
	case 1:
		return result.Entries[0], nil
	default:
		return nil, fmt.Errorf("LDAP user filter returned multiple entries")
	}
}

func (p *LDAPProvider) groupsForUser(conn *ldap.Conn, user *ldap.Entry) ([]string, error) {
	groups := append([]string(nil), user.GetAttributeValues(p.config.GroupAttribute)...)
	if !p.config.NestedActiveDirectory {
		return groups, nil
	}
	filter := fmt.Sprintf(
		"(&(objectClass=group)(member:1.2.840.113556.1.4.1941:=%s))",
		ldap.EscapeFilter(user.DN),
	)
	request := ldap.NewSearchRequest(
		p.config.GroupBaseDN,
		ldap.ScopeWholeSubtree,
		ldap.NeverDerefAliases,
		0,
		int(p.config.Timeout.Seconds()),
		false,
		filter,
		[]string{"distinguishedName", "cn"},
		nil,
	)
	result, err := conn.Search(request)
	if err != nil {
		return nil, fmt.Errorf("search nested Active Directory groups: %w", err)
	}
	for _, entry := range result.Entries {
		groups = append(groups, entry.DN, entry.GetAttributeValue("cn"))
	}
	return groups, nil
}

func (p *LDAPProvider) roleForGroups(groups []string) string {
	if groupMatch(groups, p.config.AdminGroups) {
		return store.RoleAdmin
	}
	if groupMatch(groups, p.config.ViewerGroups) {
		return store.RoleViewer
	}
	return p.config.DefaultRole
}

func groupMatch(actual, configured []string) bool {
	wanted := make(map[string]struct{}, len(configured))
	for _, group := range configured {
		wanted[strings.ToLower(strings.TrimSpace(group))] = struct{}{}
	}
	for _, group := range actual {
		values := []string{strings.ToLower(strings.TrimSpace(group))}
		if dn, err := ldap.ParseDN(group); err == nil && len(dn.RDNs) > 0 && len(dn.RDNs[0].Attributes) > 0 {
			values = append(values, strings.ToLower(dn.RDNs[0].Attributes[0].Value))
		}
		for _, value := range values {
			if _, ok := wanted[value]; ok {
				return true
			}
		}
	}
	return false
}

func ldapSubject(entry *ldap.Entry, attribute string) string {
	raw := entry.GetRawAttributeValue(attribute)
	if len(raw) == 0 {
		return ""
	}
	if utf8.Valid(raw) {
		printable := true
		for _, r := range string(raw) {
			if r < 0x20 || r == 0x7f {
				printable = false
				break
			}
		}
		if printable {
			return string(raw)
		}
	}
	return base64.RawURLEncoding.EncodeToString(raw)
}

func BuildProviderSet(cfg config.AuthenticationConfig, st *store.Store) (ProviderSet, error) {
	providers := ProviderSet{}
	for _, providerCfg := range cfg.Providers {
		switch providerCfg.Type {
		case "ldap":
			provider, err := NewLDAPProvider(providerCfg.Name, providerCfg.Match, *providerCfg.LDAP, st)
			if err != nil {
				return ProviderSet{}, fmt.Errorf("authentication provider %q: %w", providerCfg.Name, err)
			}
			providers.Password = append(providers.Password, provider)
			providers.Info = append(providers.Info, ProviderInfo{Name: providerCfg.Name, Type: "ldap"})
		case "oidc":
			provider := NewOIDCProvider(providerCfg.Name, cfg.PublicURL, *providerCfg.OIDC, st)
			providers.OIDC = append(providers.OIDC, provider)
			providers.Info = append(providers.Info, ProviderInfo{
				Name:     providerCfg.Name,
				Type:     "oidc",
				LoginURL: "/api/auth/oidc/" + url.PathEscape(providerCfg.Name) + "/login",
			})
		}
	}
	return providers, nil
}
