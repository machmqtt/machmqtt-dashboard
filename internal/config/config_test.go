package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLoadValid(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "config.yaml")
	os.WriteFile(p, []byte(`
listen: ":9090"
poll_interval: 10s
session_secret: "test-secret-that-is-at-least-32-characters-long"
data_dir: "./testdata"
`), 0o644)

	cfg, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Listen != ":9090" {
		t.Errorf("listen = %q, want :9090", cfg.Listen)
	}
	if cfg.PollInterval.Seconds() != 10 {
		t.Errorf("poll_interval = %v, want 10s", cfg.PollInterval)
	}
}

func TestLoadMissingSecret(t *testing.T) {
	t.Setenv("SESSION_SECRET", "")
	dir := t.TempDir()
	p := filepath.Join(dir, "config.yaml")
	os.WriteFile(p, []byte(`
listen: ":8080"
`), 0o644)

	_, err := Load(p)
	if err == nil {
		t.Fatal("expected error for missing session_secret")
	}
}

func TestLoadShortSecretRejected(t *testing.T) {
	t.Setenv("SESSION_SECRET", "")
	dir := t.TempDir()
	p := filepath.Join(dir, "config.yaml")
	os.WriteFile(p, []byte(`
session_secret: "change-me"
`), 0o644)

	if _, err := Load(p); err == nil {
		t.Fatal("expected error for session_secret shorter than 32 characters")
	}
}

func TestLoadSessionSecretEnvOverride(t *testing.T) {
	const envSecret = "env-secret-that-is-at-least-32-characters-long"
	t.Setenv("SESSION_SECRET", envSecret)
	dir := t.TempDir()
	p := filepath.Join(dir, "config.yaml")
	// File ships a deliberately too-short placeholder; the env var overrides it.
	os.WriteFile(p, []byte(`
session_secret: "change-me"
`), 0o644)

	cfg, err := Load(p)
	if err != nil {
		t.Fatalf("expected SESSION_SECRET env to satisfy validation: %v", err)
	}
	if cfg.SessionSecret != envSecret {
		t.Errorf("session_secret = %q, want env-supplied value", cfg.SessionSecret)
	}
}

func TestLoadNoEnvironmentsIsNowValid(t *testing.T) {
	// Clusters are now managed via the admin UI; an empty config is valid.
	dir := t.TempDir()
	p := filepath.Join(dir, "config.yaml")
	os.WriteFile(p, []byte(`
session_secret: "test-secret-that-is-at-least-32-characters-long"
`), 0o644)

	_, err := Load(p)
	if err != nil {
		t.Fatalf("expected empty config to be valid: %v", err)
	}
}

func TestLoadDefaults(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "config.yaml")
	os.WriteFile(p, []byte(`
session_secret: "test-secret-that-is-at-least-32-characters-long"
`), 0o644)

	cfg, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Listen != ":8080" {
		t.Errorf("default listen = %q, want :8080", cfg.Listen)
	}
	if cfg.DataDir != "./data" {
		t.Errorf("default data_dir = %q, want ./data", cfg.DataDir)
	}
}

func TestExampleYAML(t *testing.T) {
	yaml := ExampleYAML()
	if yaml == "" {
		t.Error("ExampleYAML returned empty string")
	}
}

func TestSubjectPrefixOrDefault(t *testing.T) {
	// nil receiver
	var n *NATSConnConfig
	if got := n.SubjectPrefixOrDefault(); got != "$MQTT5" {
		t.Errorf("nil.SubjectPrefixOrDefault() = %q, want $MQTT5", got)
	}
	// empty prefix
	n = &NATSConnConfig{}
	if got := n.SubjectPrefixOrDefault(); got != "$MQTT5" {
		t.Errorf("empty SubjectPrefix = %q, want $MQTT5", got)
	}
	// configured prefix
	n = &NATSConnConfig{SubjectPrefix: "mymqtt"}
	if got := n.SubjectPrefixOrDefault(); got != "mymqtt" {
		t.Errorf("configured SubjectPrefix = %q, want mymqtt", got)
	}
}

func TestMQTTDiscoveryEnabled(t *testing.T) {
	// nil MQTTDiscovery → default true
	e := &Environment{}
	if !e.MQTTDiscoveryEnabled() {
		t.Error("nil MQTTDiscovery should default to enabled")
	}
	// nil Enabled field → default true
	e.MQTTDiscovery = &MQTTDiscoveryConfig{}
	if !e.MQTTDiscoveryEnabled() {
		t.Error("nil Enabled field should default to true")
	}
	// explicitly true
	enabled := true
	e.MQTTDiscovery.Enabled = &enabled
	if !e.MQTTDiscoveryEnabled() {
		t.Error("Enabled=true should return true")
	}
	// explicitly false
	disabled := false
	e.MQTTDiscovery.Enabled = &disabled
	if e.MQTTDiscoveryEnabled() {
		t.Error("Enabled=false should return false")
	}
}

func TestMQTTDiscoveryPorts(t *testing.T) {
	// nil MQTTDiscovery → default [8080]
	e := &Environment{}
	if ports := e.MQTTDiscoveryPorts(); len(ports) != 1 || ports[0] != 8080 {
		t.Errorf("nil discovery ports = %v, want [8080]", ports)
	}
	// nil AdminPorts slice → default [8080]
	e.MQTTDiscovery = &MQTTDiscoveryConfig{}
	if ports := e.MQTTDiscoveryPorts(); len(ports) != 1 || ports[0] != 8080 {
		t.Errorf("nil AdminPorts = %v, want [8080]", ports)
	}
	// configured ports
	e.MQTTDiscovery.AdminPorts = []int{9090, 9091}
	ports := e.MQTTDiscoveryPorts()
	if len(ports) != 2 || ports[0] != 9090 || ports[1] != 9091 {
		t.Errorf("configured ports = %v, want [9090, 9091]", ports)
	}
}

func TestLoadFileNotFound(t *testing.T) {
	_, err := Load("/nonexistent/path/config.yaml")
	if err == nil {
		t.Error("expected error for missing file, got nil")
	}
}

// TestLoadInvalidYAML verifies that a syntactically malformed config file is
// rejected with a "parse config" error rather than yielding a partial Config.
func TestLoadInvalidYAML(t *testing.T) {
	t.Setenv("SESSION_SECRET", "")
	dir := t.TempDir()
	p := filepath.Join(dir, "config.yaml")
	// Unterminated/mismatched YAML that gopkg.in/yaml.v3 cannot parse.
	os.WriteFile(p, []byte("listen: \":8080\"\n  bad: : indent\n\t- nope"), 0o644)

	cfg, err := Load(p)
	if err == nil {
		t.Fatal("expected error for invalid YAML, got nil")
	}
	if cfg != nil {
		t.Errorf("expected nil Config on parse failure, got %+v", cfg)
	}
	if !strings.Contains(err.Error(), "parse config") {
		t.Errorf("error = %q, want it to wrap %q", err.Error(), "parse config")
	}
}

// TestLoadNonPositiveMetricsRetentionDefaults verifies that a non-positive
// metrics_retention in the file is reset to the 24h default. A negative value
// (rather than 0s) proves the reset branch ran, since the pre-unmarshal default
// would already have been overwritten by the file's negative value.
func TestLoadNonPositiveMetricsRetentionDefaults(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "config.yaml")
	os.WriteFile(p, []byte(`
session_secret: "test-secret-that-is-at-least-32-characters-long"
metrics_retention: -1s
`), 0o644)

	cfg, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.MetricsRetention != 24*time.Hour {
		t.Errorf("MetricsRetention = %v, want 24h (non-positive value should reset)", cfg.MetricsRetention)
	}
}

func TestResolveBridgeToken(t *testing.T) {
	cases := []struct {
		name       string
		envDefault string
		perBridge  string
		want       string
	}{
		{"per-bridge overrides env default", "env-tok", "bridge-tok", "bridge-tok"},
		{"falls back to env default", "env-tok", "", "env-tok"},
		{"empty when neither set", "", "", ""},
		{"per-bridge with no env default", "", "bridge-tok", "bridge-tok"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := &Environment{AdminToken: tc.envDefault}
			if got := e.ResolveBridgeToken(tc.perBridge); got != tc.want {
				t.Errorf("ResolveBridgeToken(%q) with AdminToken=%q = %q, want %q", tc.perBridge, tc.envDefault, got, tc.want)
			}
		})
	}
}
