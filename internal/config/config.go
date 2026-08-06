package config

import (
	"bytes"
	"crypto/x509"
	_ "embed"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

//go:embed config.example.yaml
var exampleYAML string

var authProviderNamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

// ExampleYAML returns the fully commented example configuration file.
func ExampleYAML() string { return exampleYAML }

type Config struct {
	Listen        string        `yaml:"listen"`
	PollInterval  time.Duration `yaml:"poll_interval"`
	SessionSecret string        `yaml:"session_secret"`
	SecureCookies bool          `yaml:"secure_cookies"`
	DataDir       string        `yaml:"data_dir"`
	// MetricsRetention is how long time-series samples are kept before the
	// cleanup pass deletes them. Defaults to 24h.
	MetricsRetention time.Duration `yaml:"metrics_retention"`
	// TrustProxyHeaders enables honoring X-Forwarded-For for client-IP
	// identification (login rate limiting). Leave false unless the dashboard
	// sits behind a trusted reverse proxy that sets the header — otherwise a
	// client can spoof it to evade the login rate limiter.
	TrustProxyHeaders bool                 `yaml:"trust_proxy_headers"`
	Authentication    AuthenticationConfig `yaml:"authentication"`
	// Environments are clusters seeded into the database on first startup, keyed
	// by name: an environment whose name is not already present is created; ones
	// that already exist are left untouched (so runtime edits via the admin UI are
	// never overwritten). This makes a zero-touch `docker compose up` poll the
	// configured servers without a manual cluster-creation step. After seeding,
	// clusters are managed in the DB via the admin UI/API.
	Environments []Environment `yaml:"environments,omitempty"`
}

// AuthenticationConfig controls authentication for dashboard users. Local
// authentication is mandatory so administrators retain a recovery path when
// every external identity provider is unavailable.
type AuthenticationConfig struct {
	PublicURL         string               `yaml:"public_url,omitempty"`
	TrustedProxyCIDRs []string             `yaml:"trusted_proxy_cidrs,omitempty"`
	Local             LocalAuthConfig      `yaml:"local"`
	Providers         []AuthProviderConfig `yaml:"providers,omitempty"`
}

type LocalAuthConfig struct {
	Enabled               bool   `yaml:"enabled"`
	BreakGlassLogin       bool   `yaml:"break_glass_login"`
	BootstrapPassword     string `yaml:"bootstrap_password,omitempty"`
	BootstrapPasswordFile string `yaml:"bootstrap_password_file,omitempty"`
}

// AuthProviderConfig describes one external provider. Array order is
// significant and the first password-provider identity match wins.
type AuthProviderConfig struct {
	Name  string          `yaml:"name"`
	Type  string          `yaml:"type"`
	Match AuthMatchConfig `yaml:"match,omitempty"`
	LDAP  *LDAPAuthConfig `yaml:"ldap,omitempty"`
	OIDC  *OIDCAuthConfig `yaml:"oidc,omitempty"`
}

type AuthMatchConfig struct {
	Domains []string `yaml:"domains,omitempty"`
}

type LDAPAuthConfig struct {
	URL                   string        `yaml:"url"`
	StartTLS              bool          `yaml:"start_tls,omitempty"`
	AllowPlaintext        bool          `yaml:"allow_plaintext,omitempty"`
	CAFile                string        `yaml:"ca_file,omitempty"`
	InsecureSkipVerify    bool          `yaml:"insecure_skip_verify,omitempty"`
	BindDN                string        `yaml:"bind_dn,omitempty"`
	BindPassword          string        `yaml:"bind_password,omitempty"`
	BindPasswordFile      string        `yaml:"bind_password_file,omitempty"`
	UserBaseDN            string        `yaml:"user_base_dn"`
	UserFilter            string        `yaml:"user_filter,omitempty"`
	UsernameAttribute     string        `yaml:"username_attribute,omitempty"`
	SubjectAttribute      string        `yaml:"subject_attribute"`
	DisplayNameAttribute  string        `yaml:"display_name_attribute,omitempty"`
	EmailAttribute        string        `yaml:"email_attribute,omitempty"`
	GroupAttribute        string        `yaml:"group_attribute,omitempty"`
	GroupBaseDN           string        `yaml:"group_base_dn,omitempty"`
	NestedActiveDirectory bool          `yaml:"nested_active_directory,omitempty"`
	AdminGroups           []string      `yaml:"admin_groups,omitempty"`
	ViewerGroups          []string      `yaml:"viewer_groups,omitempty"`
	DefaultRole           string        `yaml:"default_role,omitempty"`
	Timeout               time.Duration `yaml:"timeout,omitempty"`
}

type OIDCAuthConfig struct {
	IssuerURL        string   `yaml:"issuer_url"`
	ClientID         string   `yaml:"client_id"`
	ClientSecret     string   `yaml:"client_secret"`
	ClientSecretFile string   `yaml:"client_secret_file,omitempty"`
	Scopes           []string `yaml:"scopes,omitempty"`
	UsernameClaim    string   `yaml:"username_claim,omitempty"`
	DisplayNameClaim string   `yaml:"display_name_claim,omitempty"`
	EmailClaim       string   `yaml:"email_claim,omitempty"`
	GroupsClaim      string   `yaml:"groups_claim,omitempty"`
	AdminGroups      []string `yaml:"admin_groups,omitempty"`
	ViewerGroups     []string `yaml:"viewer_groups,omitempty"`
	DefaultRole      string   `yaml:"default_role,omitempty"`
}

type Environment struct {
	Name          string               `yaml:"name"`
	Servers       []Server             `yaml:"servers"`
	MQTTBridges   []MQTTBridge         `yaml:"mqtt_bridges,omitempty"`
	MQTTDiscovery *MQTTDiscoveryConfig `yaml:"mqtt_discovery,omitempty"`
	// AdminToken is the environment-level ("cluster") default bearer token used
	// to authenticate to every MachMQTT bridge admin API in this environment —
	// both auto-discovered instances and configured bridges without their own
	// token. A per-bridge MQTTBridge.BearerToken overrides it. Empty = send no
	// token (works against bridges whose admin API has no token configured).
	AdminToken string          `yaml:"admin_token,omitempty"`
	TLS        *TLSConfig      `yaml:"tls,omitempty"`
	NATSConn   *NATSConnConfig `yaml:"nats_conn,omitempty"`
}

// NATSConnConfig holds the NATS client connection parameters for push-based
// collection (2a MachMQTT metrics subject, 2b $SYS server collection).
// Nil means HTTP polling only. Exactly one auth field should be set when used.
type NATSConnConfig struct {
	// URLs are one or more nats:// seed server URLs.
	URLs []string `yaml:"urls" json:"urls"`
	// Auth fields — set exactly one.
	Username  string `yaml:"username,omitempty"   json:"username,omitempty"`
	Password  string `yaml:"password,omitempty"   json:"password,omitempty"`
	Token     string `yaml:"token,omitempty"      json:"token,omitempty"`
	NKey      string `yaml:"nkey,omitempty"       json:"nkey,omitempty"`
	CredsFile string `yaml:"creds_file,omitempty" json:"creds_file,omitempty"`
	// SubjectPrefix is the MachMQTT subject namespace. Must match the prefix
	// configured in MachMQTT for this cluster. Default is "$MQTT5".
	SubjectPrefix string `yaml:"subject_prefix,omitempty" json:"subject_prefix,omitempty"`
	// SYSCollection enables $SYS-based server collection (Tier 2b). Requires
	// system-account access on the NATS connection. When false, HTTP polling is
	// used for server stats regardless of whether NATS push (2a) is configured.
	SYSCollection bool       `yaml:"sys_collection,omitempty" json:"sys_collection,omitempty"`
	TLS           *TLSConfig `yaml:"tls,omitempty"            json:"tls,omitempty"`
}

// SubjectPrefixOrDefault returns the configured prefix or the "$MQTT5" default.
func (n *NATSConnConfig) SubjectPrefixOrDefault() string {
	if n == nil || n.SubjectPrefix == "" {
		return "$MQTT5"
	}
	return n.SubjectPrefix
}

type MQTTBridge struct {
	Name        string `yaml:"name"                   json:"name"`
	URL         string `yaml:"url"                    json:"url"`
	BearerToken string `yaml:"bearer_token,omitempty" json:"bearer_token,omitempty"`
}

type MQTTDiscoveryConfig struct {
	Enabled    *bool `yaml:"enabled,omitempty"      json:"enabled,omitempty"`     // nil = true (default on)
	AdminPorts []int `yaml:"admin_ports,omitempty"  json:"admin_ports,omitempty"` // default [8080]
	// TrustedHosts extends the set of hosts the environment's admin_token may be
	// sent to during auto-discovery. Loopback and any host already named in this
	// environment's server/bridge/nats_conn URLs are always trusted. A discovered
	// bridge whose IP/host is not trusted is still probed, but WITHOUT the admin
	// token — so a connection that merely names itself "machmqtt-bridge" from an
	// arbitrary address can never capture the shared secret.
	TrustedHosts []string `yaml:"trusted_hosts,omitempty" json:"trusted_hosts,omitempty"`
}

// MQTTDiscoveryEnabled returns whether auto-discovery is enabled for this environment.
func (e *Environment) MQTTDiscoveryEnabled() bool {
	if e.MQTTDiscovery == nil || e.MQTTDiscovery.Enabled == nil {
		return true // default: enabled
	}
	return *e.MQTTDiscovery.Enabled
}

// MQTTDiscoveryPorts returns the admin ports to probe for bridge discovery.
func (e *Environment) MQTTDiscoveryPorts() []int {
	if e.MQTTDiscovery != nil && len(e.MQTTDiscovery.AdminPorts) > 0 {
		return e.MQTTDiscovery.AdminPorts
	}
	return []int{8080}
}

// ResolveBridgeToken returns the admin bearer token to use for a bridge: the
// per-bridge override if set, otherwise the environment-level default.
func (e *Environment) ResolveBridgeToken(perBridge string) string {
	if perBridge != "" {
		return perBridge
	}
	return e.AdminToken
}

// DiscoveryTrustedHosts returns the set of hosts (lowercased) the environment's
// admin token may be sent to during auto-discovery: every host named in this
// environment's server, bridge, and nats_conn URLs, plus any operator-supplied
// trusted_hosts. Loopback is handled separately by the collector. Used to avoid
// leaking the shared admin token to an arbitrary discovered address.
func (e *Environment) DiscoveryTrustedHosts() map[string]bool {
	hosts := make(map[string]bool)
	add := func(h string) {
		if h != "" {
			hosts[strings.ToLower(h)] = true
		}
	}
	for _, s := range e.Servers {
		add(hostFromURL(s.URL))
	}
	for _, b := range e.MQTTBridges {
		add(hostFromURL(b.URL))
	}
	if e.NATSConn != nil {
		for _, u := range e.NATSConn.URLs {
			add(hostFromURL(u))
		}
	}
	if e.MQTTDiscovery != nil {
		for _, h := range e.MQTTDiscovery.TrustedHosts {
			add(h)
		}
	}
	return hosts
}

// hostFromURL extracts the host (without port) from a URL that may or may not
// carry a scheme (e.g. "http://h:8222", "nats://h:4222", "h:4222", "h").
func hostFromURL(raw string) string {
	s := raw
	if i := strings.Index(s, "://"); i >= 0 {
		s = s[i+3:]
	}
	// Strip any path/query.
	if i := strings.IndexAny(s, "/?"); i >= 0 {
		s = s[:i]
	}
	if h, _, err := net.SplitHostPort(s); err == nil {
		return h
	}
	return s
}

type Server struct {
	URL string `yaml:"url" json:"url"`
}

type TLSConfig struct {
	CAFile   string `yaml:"ca_file,omitempty" json:"ca_file,omitempty"`
	Insecure bool   `yaml:"insecure"          json:"insecure"`
}

func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}

	cfg := &Config{
		Listen:           ":8080",
		PollInterval:     30 * time.Second,
		DataDir:          "./data",
		MetricsRetention: 24 * time.Hour,
		Authentication: AuthenticationConfig{
			Local: LocalAuthConfig{Enabled: true, BreakGlassLogin: true},
		},
	}

	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)
	if err := decoder.Decode(cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return nil, fmt.Errorf("parse config: multiple YAML documents are not supported")
		}
		return nil, fmt.Errorf("parse config: %w", err)
	}

	if cfg.MetricsRetention <= 0 {
		cfg.MetricsRetention = 24 * time.Hour
	}

	// SESSION_SECRET overrides the config file so the secret can be injected
	// without writing it to disk (Docker, CI, secret managers).
	if v := os.Getenv("SESSION_SECRET"); v != "" {
		cfg.SessionSecret = v
	}

	if cfg.SessionSecret == "" {
		return nil, fmt.Errorf("session_secret is required: set it in the config file or the SESSION_SECRET env var; generate one with: openssl rand -hex 32")
	}
	if len(cfg.SessionSecret) < 32 {
		return nil, fmt.Errorf("session_secret must be at least 32 characters (got %d) — the shipped placeholder is intentionally too short so the app refuses to start until you set a real one; generate one with: openssl rand -hex 32", len(cfg.SessionSecret))
	}
	if cfg.PollInterval < time.Second || cfg.PollInterval > 10*time.Minute {
		return nil, fmt.Errorf("poll_interval must be between 1s and 10m")
	}
	if cfg.MetricsRetention < time.Hour || cfg.MetricsRetention > 365*24*time.Hour {
		return nil, fmt.Errorf("metrics_retention must be between 1h and 8760h")
	}
	if _, port, err := net.SplitHostPort(cfg.Listen); err != nil || port == "" {
		return nil, fmt.Errorf("listen must be a host:port address")
	}
	if !cfg.Authentication.Local.Enabled {
		return nil, fmt.Errorf("authentication.local.enabled must be true: local authentication is required for break-glass access")
	}
	if !cfg.Authentication.Local.BreakGlassLogin {
		return nil, fmt.Errorf("authentication.local.break_glass_login must be true")
	}
	bootstrapPassword, err := resolveSecret(cfg.Authentication.Local.BootstrapPassword, cfg.Authentication.Local.BootstrapPasswordFile)
	if err != nil {
		return nil, fmt.Errorf("authentication.local bootstrap password: %w", err)
	}
	if bootstrapPassword != "" && len(bootstrapPassword) < 12 {
		return nil, fmt.Errorf("authentication.local bootstrap password must be at least 12 characters")
	}
	cfg.Authentication.Local.BootstrapPassword = bootstrapPassword
	for _, cidr := range cfg.Authentication.TrustedProxyCIDRs {
		if _, _, err := net.ParseCIDR(cidr); err != nil {
			return nil, fmt.Errorf("authentication.trusted_proxy_cidrs: invalid CIDR %q", cidr)
		}
	}
	providerNames := make(map[string]struct{}, len(cfg.Authentication.Providers))
	for i := range cfg.Authentication.Providers {
		provider := &cfg.Authentication.Providers[i]
		if provider.Name == "" {
			return nil, fmt.Errorf("authentication provider %d: name is required", i)
		}
		if !authProviderNamePattern.MatchString(provider.Name) {
			return nil, fmt.Errorf("authentication provider %d: name must contain only letters, numbers, dots, underscores, and hyphens", i)
		}
		if _, exists := providerNames[provider.Name]; exists {
			return nil, fmt.Errorf("authentication provider %d: duplicate name %q", i, provider.Name)
		}
		providerNames[provider.Name] = struct{}{}
		for j := range provider.Match.Domains {
			provider.Match.Domains[j] = strings.ToLower(strings.TrimSpace(provider.Match.Domains[j]))
			if provider.Match.Domains[j] == "" {
				return nil, fmt.Errorf("authentication provider %q: match domains cannot be empty", provider.Name)
			}
		}
		switch provider.Type {
		case "ldap":
			if provider.OIDC != nil || provider.LDAP == nil {
				return nil, fmt.Errorf("authentication provider %q: exactly one ldap configuration is required", provider.Name)
			}
			if err := validateLDAPProvider(provider.Name, provider.LDAP); err != nil {
				return nil, err
			}
		case "oidc":
			if provider.LDAP != nil || provider.OIDC == nil {
				return nil, fmt.Errorf("authentication provider %q: exactly one oidc configuration is required", provider.Name)
			}
			if cfg.Authentication.PublicURL == "" {
				return nil, fmt.Errorf("authentication.public_url is required when OIDC is configured")
			}
			if !cfg.SecureCookies {
				return nil, fmt.Errorf("secure_cookies must be true when OIDC is configured")
			}
			publicURL, err := url.Parse(cfg.Authentication.PublicURL)
			if err != nil || publicURL.Scheme != "https" || publicURL.Host == "" || (publicURL.Path != "" && publicURL.Path != "/") || publicURL.RawQuery != "" || publicURL.Fragment != "" {
				return nil, fmt.Errorf("authentication.public_url must be an https URL when OIDC is configured")
			}
			cfg.Authentication.PublicURL = strings.TrimRight(cfg.Authentication.PublicURL, "/")
			if err := validateOIDCProvider(provider.Name, provider.OIDC); err != nil {
				return nil, err
			}
		default:
			return nil, fmt.Errorf("authentication provider %q: type must be ldap or oidc", provider.Name)
		}
	}
	if err := validateEnvironments(cfg.Environments); err != nil {
		return nil, err
	}

	return cfg, nil
}

func validateEnvironments(environments []Environment) error {
	environmentNames := make(map[string]struct{}, len(environments))
	for i, env := range environments {
		if env.Name == "" {
			return fmt.Errorf("environment %d: name is required", i)
		}
		if _, duplicate := environmentNames[env.Name]; duplicate {
			return fmt.Errorf("environment %d: duplicate name %q", i, env.Name)
		}
		environmentNames[env.Name] = struct{}{}
		if len(env.Servers) == 0 {
			return fmt.Errorf("environment %q: at least one server is required", env.Name)
		}
		serverURLs := make(map[string]struct{}, len(env.Servers))
		for j, server := range env.Servers {
			if err := validateHTTPURL(server.URL); err != nil {
				return fmt.Errorf("environment %q server %d: %w", env.Name, j, err)
			}
			if _, duplicate := serverURLs[server.URL]; duplicate {
				return fmt.Errorf("environment %q server %d: duplicate URL %q", env.Name, j, server.URL)
			}
			serverURLs[server.URL] = struct{}{}
		}
		bridgeNames := make(map[string]struct{}, len(env.MQTTBridges))
		for j, bridge := range env.MQTTBridges {
			if bridge.Name == "" {
				return fmt.Errorf("environment %q MQTT bridge %d: name is required", env.Name, j)
			}
			if _, duplicate := bridgeNames[bridge.Name]; duplicate {
				return fmt.Errorf("environment %q MQTT bridge %d: duplicate name %q", env.Name, j, bridge.Name)
			}
			bridgeNames[bridge.Name] = struct{}{}
			if err := validateHTTPURL(bridge.URL); err != nil {
				return fmt.Errorf("environment %q MQTT bridge %q: %w", env.Name, bridge.Name, err)
			}
		}
		for _, port := range env.MQTTDiscoveryPorts() {
			if port < 1 || port > 65535 {
				return fmt.Errorf("environment %q: MQTT discovery port %d is invalid", env.Name, port)
			}
		}
		if env.TLS != nil && env.TLS.CAFile != "" {
			if err := validateCAFile(env.TLS.CAFile); err != nil {
				return fmt.Errorf("environment %q TLS CA: %w", env.Name, err)
			}
		}
	}
	return nil
}

func validateOIDCProvider(name string, cfg *OIDCAuthConfig) error {
	issuer, err := url.Parse(cfg.IssuerURL)
	if err != nil || issuer.Scheme != "https" || issuer.Host == "" || issuer.User != nil || issuer.RawQuery != "" || issuer.Fragment != "" {
		return fmt.Errorf("authentication provider %q: oidc.issuer_url must be an https URL", name)
	}
	secret, err := resolveSecret(cfg.ClientSecret, cfg.ClientSecretFile)
	if err != nil {
		return fmt.Errorf("authentication provider %q: oidc client secret: %w", name, err)
	}
	cfg.ClientSecret = secret
	if cfg.ClientID == "" || cfg.ClientSecret == "" {
		return fmt.Errorf("authentication provider %q: oidc.client_id and a client secret are required", name)
	}
	if cfg.DefaultRole != "" && cfg.DefaultRole != "admin" && cfg.DefaultRole != "viewer" {
		return fmt.Errorf("authentication provider %q: oidc.default_role must be admin or viewer", name)
	}
	if len(cfg.AdminGroups) == 0 && len(cfg.ViewerGroups) == 0 && cfg.DefaultRole == "" {
		return fmt.Errorf("authentication provider %q: configure OIDC groups or default_role", name)
	}
	if cfg.UsernameClaim == "" {
		cfg.UsernameClaim = "preferred_username"
	}
	if cfg.DisplayNameClaim == "" {
		cfg.DisplayNameClaim = "name"
	}
	if cfg.EmailClaim == "" {
		cfg.EmailClaim = "email"
	}
	if cfg.GroupsClaim == "" {
		cfg.GroupsClaim = "groups"
	}
	return nil
}

func validateLDAPProvider(name string, cfg *LDAPAuthConfig) error {
	if cfg.URL == "" {
		return fmt.Errorf("authentication provider %q: ldap.url is required", name)
	}
	u, err := url.Parse(cfg.URL)
	if err != nil || u.Host == "" || (u.Scheme != "ldap" && u.Scheme != "ldaps") {
		return fmt.Errorf("authentication provider %q: ldap.url must use ldap:// or ldaps://", name)
	}
	if u.Scheme == "ldap" && !cfg.StartTLS && !cfg.AllowPlaintext {
		return fmt.Errorf("authentication provider %q: ldap:// requires start_tls or explicit allow_plaintext", name)
	}
	if u.Scheme == "ldaps" && cfg.StartTLS {
		return fmt.Errorf("authentication provider %q: start_tls cannot be used with ldaps://", name)
	}
	if cfg.CAFile != "" {
		if err := validateCAFile(cfg.CAFile); err != nil {
			return fmt.Errorf("authentication provider %q: ldap CA: %w", name, err)
		}
	}
	bindPassword, err := resolveSecret(cfg.BindPassword, cfg.BindPasswordFile)
	if err != nil {
		return fmt.Errorf("authentication provider %q: ldap bind password: %w", name, err)
	}
	cfg.BindPassword = bindPassword
	if (cfg.BindDN == "") != (cfg.BindPassword == "") {
		return fmt.Errorf("authentication provider %q: ldap.bind_dn and a bind password must be set together", name)
	}
	if cfg.UserBaseDN == "" {
		return fmt.Errorf("authentication provider %q: ldap.user_base_dn is required", name)
	}
	if cfg.SubjectAttribute == "" {
		return fmt.Errorf("authentication provider %q: ldap.subject_attribute is required", name)
	}
	if cfg.NestedActiveDirectory && cfg.GroupBaseDN == "" {
		return fmt.Errorf("authentication provider %q: ldap.group_base_dn is required for nested Active Directory groups", name)
	}
	if cfg.DefaultRole != "" && cfg.DefaultRole != "admin" && cfg.DefaultRole != "viewer" {
		return fmt.Errorf("authentication provider %q: ldap.default_role must be admin or viewer", name)
	}
	if len(cfg.AdminGroups) == 0 && len(cfg.ViewerGroups) == 0 && cfg.DefaultRole == "" {
		return fmt.Errorf("authentication provider %q: configure LDAP groups or default_role", name)
	}
	if cfg.UserFilter == "" {
		cfg.UserFilter = "(|(sAMAccountName={username})(userPrincipalName={username}))"
	}
	if !strings.Contains(cfg.UserFilter, "{username}") {
		return fmt.Errorf("authentication provider %q: ldap.user_filter must contain {username}", name)
	}
	if cfg.UsernameAttribute == "" {
		cfg.UsernameAttribute = "sAMAccountName"
	}
	if cfg.DisplayNameAttribute == "" {
		cfg.DisplayNameAttribute = "displayName"
	}
	if cfg.EmailAttribute == "" {
		cfg.EmailAttribute = "mail"
	}
	if cfg.GroupAttribute == "" {
		cfg.GroupAttribute = "memberOf"
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = 5 * time.Second
	}
	return nil
}

func validateHTTPURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return fmt.Errorf("URL must use http:// or https:// with no credentials, query, or fragment")
	}
	if u.Path != "" && u.Path != "/" {
		return fmt.Errorf("URL must not contain a path")
	}
	if port := u.Port(); port != "" {
		if parsed, err := net.LookupPort("tcp", port); err != nil || parsed < 1 || parsed > 65535 {
			return fmt.Errorf("URL contains an invalid port")
		}
	}
	return nil
}

func validateCAFile(path string) error {
	pem, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read %s: %w", path, err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(pem) {
		return fmt.Errorf("%s contains no usable PEM certificates", path)
	}
	return nil
}

func resolveSecret(inline, path string) (string, error) {
	if inline != "" && path != "" {
		return "", fmt.Errorf("configure either an inline value or a file, not both")
	}
	if path == "" {
		return inline, nil
	}
	value, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read %s: %w", path, err)
	}
	return strings.TrimRight(string(value), "\r\n"), nil
}
