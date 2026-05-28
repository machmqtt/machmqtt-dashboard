package config

import (
	"fmt"
	"os"
	"time"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Listen        string        `yaml:"listen"`
	PollInterval  time.Duration `yaml:"poll_interval"`
	SessionSecret string        `yaml:"session_secret"`
	SecureCookies bool          `yaml:"secure_cookies"`
	DataDir       string        `yaml:"data_dir"`
	Environments  []Environment `yaml:"environments"`
}

type Environment struct {
	Name          string               `yaml:"name"`
	Servers       []Server             `yaml:"servers"`
	MQTTBridges   []MQTTBridge         `yaml:"mqtt_bridges,omitempty"`
	MQTTDiscovery *MQTTDiscoveryConfig `yaml:"mqtt_discovery,omitempty"`
	TLS           *TLSConfig           `yaml:"tls,omitempty"`
}

type MQTTBridge struct {
	Name        string `yaml:"name"`
	URL         string `yaml:"url"`
	BearerToken string `yaml:"bearer_token,omitempty"`
}

type MQTTDiscoveryConfig struct {
	Enabled    *bool `yaml:"enabled,omitempty"`     // nil = true (default on)
	AdminPorts []int `yaml:"admin_ports,omitempty"` // default [8080]
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

type Server struct {
	URL string `yaml:"url"`
}

type TLSConfig struct {
	CAFile   string `yaml:"ca_file,omitempty"`
	Insecure bool   `yaml:"insecure"`
}

func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}

	cfg := &Config{
		Listen:       ":8080",
		PollInterval: 5 * time.Second,
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
	if len(cfg.Environments) == 0 {
		return nil, fmt.Errorf("at least one environment is required")
	}
	for i, env := range cfg.Environments {
		if env.Name == "" {
			return nil, fmt.Errorf("environment %d: name is required", i)
		}
		if len(env.Servers) == 0 {
			return nil, fmt.Errorf("environment %q: at least one server is required", env.Name)
		}
	}

	return cfg, nil
}
