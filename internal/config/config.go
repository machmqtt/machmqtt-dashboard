package config

import (
	_ "embed"
	"fmt"
	"os"
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
		Listen:       ":8080",
		PollInterval: 30 * time.Second,
		DataDir:      "./data",
	}

	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
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
