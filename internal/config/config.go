package config

import (
	_ "embed"
	"fmt"
	"net"
	"os"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

//go:embed config.example.yaml
var exampleYAML string

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
	TrustProxyHeaders bool `yaml:"trust_proxy_headers"`
	// Environments are clusters seeded into the database on first startup, keyed
	// by name: an environment whose name is not already present is created; ones
	// that already exist are left untouched (so runtime edits via the admin UI are
	// never overwritten). This makes a zero-touch `docker compose up` poll the
	// configured servers without a manual cluster-creation step. After seeding,
	// clusters are managed in the DB via the admin UI/API.
	Environments []Environment `yaml:"environments,omitempty"`
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
	}

	if err := yaml.Unmarshal(data, cfg); err != nil {
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

	return cfg, nil
}
