package config

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func loadConfigText(t *testing.T, text string) (*Config, error) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
	return Load(path)
}

const minimalConfig = `
session_secret: "test-secret-that-is-at-least-32-characters-long"
environments:
  - name: dev
    servers:
      - url: "http://localhost:8222"
`

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
	if cfg.MetricsRetention.Hours() != 24 {
		t.Errorf("metrics retention=%v", cfg.MetricsRetention)
	}
	if !cfg.Authentication.Local.Enabled || !cfg.Authentication.Local.BreakGlassLogin {
		t.Errorf("local break-glass defaults = %+v, want enabled", cfg.Authentication.Local)
	}
}

func TestStrictConfigurationAndEnvironmentValidation(t *testing.T) {
	tests := []struct{ name, config string }{
		{"unknown field", minimalConfig + "unknown_field: true\n"},
		{"invalid YAML", ":\n"},
		{"multiple documents", minimalConfig + "---\n{}\n"},
		{"short secret", strings.Replace(minimalConfig, "test-secret-that-is-at-least-32-characters-long", "short", 1)},
		{"poll too short", "poll_interval: 1ms\n" + minimalConfig},
		{"poll too long", "poll_interval: 11m\n" + minimalConfig},
		{"retention too short", "metrics_retention: 1m\n" + minimalConfig},
		{"retention too long", "metrics_retention: 9000h\n" + minimalConfig},
		{"invalid listen", "listen: bad\n" + minimalConfig},
		{"duplicate environments", minimalConfig + "  - name: dev\n    servers:\n      - url: http://localhost:8223\n"},
		{"missing environment name", strings.Replace(minimalConfig, "name: dev", "name: ''", 1)},
		{"missing servers", strings.Replace(minimalConfig, "    servers:\n      - url: \"http://localhost:8222\"", "    servers: []", 1)},
		{"bad server scheme", strings.Replace(minimalConfig, "http://localhost:8222", "nats://localhost:4222", 1)},
		{"server credentials", strings.Replace(minimalConfig, "http://localhost:8222", "http://user:pass@localhost:8222", 1)},
		{"server path", strings.Replace(minimalConfig, "http://localhost:8222", "http://localhost:8222/path", 1)},
		{"server query", strings.Replace(minimalConfig, "http://localhost:8222", "http://localhost:8222?q=x", 1)},
		{"duplicate server", strings.Replace(minimalConfig, "      - url: \"http://localhost:8222\"", "      - url: \"http://localhost:8222\"\n      - url: \"http://localhost:8222\"", 1)},
		{"invalid discovery port", minimalConfig + "    mqtt_discovery:\n      admin_ports: [70000]\n"},
		{"bridge missing name", minimalConfig + "    mqtt_bridges:\n      - url: http://localhost:8080\n"},
		{"bridge duplicate name", minimalConfig + "    mqtt_bridges:\n      - name: b\n        url: http://localhost:8080\n      - name: b\n        url: http://localhost:8081\n"},
		{"bad bridge URL", minimalConfig + "    mqtt_bridges:\n      - name: b\n        url: ftp://localhost/x\n"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := loadConfigText(t, tc.config); err == nil {
				t.Fatal("expected validation error")
			}
		})
	}
}

func TestLoadProviderAndRecoveryValidationEdges(t *testing.T) {
	if _, err := Load(filepath.Join(t.TempDir(), "missing.yaml")); err == nil {
		t.Fatal("missing file accepted")
	}
	providerBase := `
session_secret: "test-secret-that-is-at-least-32-characters-long"
authentication:
  providers:
    - name: corp
      type: ldap
      ldap:
        url: ldap://ldap.example.com
        allow_plaintext: true
        user_base_dn: dc=example,dc=com
        subject_attribute: entryUUID
        default_role: viewer
environments:
  - name: dev
    servers:
      - url: http://localhost:8222
`
	tests := map[string]string{
		"break glass disabled":  strings.Replace(minimalConfig, "environments:", "authentication:\n  local:\n    enabled: true\n    break_glass_login: false\nenvironments:", 1),
		"provider name missing": strings.Replace(providerBase, "name: corp", "name: ''", 1),
		"provider type unknown": strings.Replace(providerBase, "type: ldap", "type: saml", 1),
		"provider empty domain": strings.Replace(providerBase, "      type: ldap", "      type: ldap\n      match:\n        domains: ['']", 1),
		"LDAP missing config":   strings.Replace(providerBase, "      ldap:\n", "      oidc: {}\n", 1),
		"LDAP with OIDC":        strings.Replace(providerBase, "      ldap:\n", "      oidc: {}\n      ldap:\n", 1),
	}
	for name, candidate := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := loadConfigText(t, candidate); err == nil {
				t.Fatal("expected error")
			}
		})
	}
}

func TestBootstrapSecretFileAndValidation(t *testing.T) {
	dir := t.TempDir()
	secretPath := filepath.Join(dir, "bootstrap")
	if err := os.WriteFile(secretPath, []byte("a-strong-bootstrap-password\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	text := strings.Replace(minimalConfig, "environments:", "authentication:\n  local:\n    enabled: true\n    break_glass_login: true\n    bootstrap_password_file: "+secretPath+"\nenvironments:", 1)
	cfg, err := loadConfigText(t, text)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Authentication.Local.BootstrapPassword != "a-strong-bootstrap-password" {
		t.Fatal("bootstrap secret file not resolved")
	}
	for name, authBlock := range map[string]string{
		"short":        "bootstrap_password: short",
		"both":         "bootstrap_password: a-strong-bootstrap-password\n    bootstrap_password_file: " + secretPath,
		"missing file": "bootstrap_password_file: /definitely/missing",
	} {
		t.Run(name, func(t *testing.T) {
			candidate := strings.Replace(minimalConfig, "environments:", "authentication:\n  local:\n    enabled: true\n    break_glass_login: true\n    "+authBlock+"\nenvironments:", 1)
			if _, err := loadConfigText(t, candidate); err == nil {
				t.Fatal("expected error")
			}
		})
	}
}

func TestLDAPProviderValidationBoundaries(t *testing.T) {
	provider := `authentication:
  providers:
    - name: corp
      type: ldap
      ldap:
        url: ldap://ldap.example.com
        start_tls: true
        user_base_dn: dc=example,dc=com
        subject_attribute: entryUUID
        default_role: viewer
`
	valid := strings.Replace(minimalConfig, "environments:", provider+"environments:", 1)
	if _, err := loadConfigText(t, valid); err != nil {
		t.Fatal(err)
	}
	invalid := map[string]string{
		"bad name":              strings.Replace(valid, "name: corp", "name: 'bad name'", 1),
		"missing LDAP":          strings.Replace(valid, "      ldap:\n", "", 1),
		"plaintext not allowed": strings.Replace(valid, "        start_tls: true\n", "", 1),
		"ldaps starttls":        strings.Replace(valid, "url: ldap://", "url: ldaps://", 1),
		"missing base":          strings.Replace(valid, "        user_base_dn: dc=example,dc=com\n", "", 1),
		"missing subject":       strings.Replace(valid, "        subject_attribute: entryUUID\n", "", 1),
		"bad role":              strings.Replace(valid, "default_role: viewer", "default_role: owner", 1),
		"filter placeholder":    strings.Replace(valid, "        default_role: viewer", "        user_filter: '(uid=x)'\n        default_role: viewer", 1),
		"nested missing base":   strings.Replace(valid, "        default_role: viewer", "        nested_active_directory: true\n        default_role: viewer", 1),
	}
	for name, candidate := range invalid {
		t.Run(name, func(t *testing.T) {
			if _, err := loadConfigText(t, candidate); err == nil {
				t.Fatal("expected error")
			}
		})
	}
}

func TestOIDCAndProxyValidationBoundaries(t *testing.T) {
	base := `
session_secret: "test-secret-that-is-at-least-32-characters-long"
secure_cookies: true
authentication:
  public_url: https://dashboard.example.com
  trusted_proxy_cidrs: ["10.0.0.0/8"]
  providers:
    - name: corp
      type: oidc
      oidc:
        issuer_url: https://id.example.com/tenant
        client_id: dashboard
        client_secret: secret
        default_role: viewer
environments:
  - name: dev
    servers:
      - url: http://localhost:8222
`
	if _, err := loadConfigText(t, base); err != nil {
		t.Fatal(err)
	}
	invalid := map[string]string{
		"proxy CIDR":            strings.Replace(base, "10.0.0.0/8", "bad", 1),
		"public URL missing":    strings.Replace(base, "  public_url: https://dashboard.example.com\n", "", 1),
		"public URL http":       strings.Replace(base, "https://dashboard.example.com", "http://dashboard.example.com", 1),
		"insecure cookies":      strings.Replace(base, "secure_cookies: true", "secure_cookies: false", 1),
		"issuer http":           strings.Replace(base, "https://id.example.com", "http://id.example.com", 1),
		"issuer query":          strings.Replace(base, "/tenant", "/tenant?x=y", 1),
		"missing client":        strings.Replace(base, "        client_id: dashboard\n", "", 1),
		"missing authorization": strings.Replace(base, "        default_role: viewer\n", "", 1),
		"invalid role":          strings.Replace(base, "default_role: viewer", "default_role: owner", 1),
		"wrong settings":        strings.Replace(base, "      oidc:\n", "      ldap:\n", 1),
	}
	for name, candidate := range invalid {
		t.Run(name, func(t *testing.T) {
			if _, err := loadConfigText(t, candidate); err == nil {
				t.Fatal("expected error")
			}
		})
	}
}

func TestCAFileMustContainCertificate(t *testing.T) {
	ca := filepath.Join(t.TempDir(), "ca.pem")
	if err := os.WriteFile(ca, []byte("not a certificate"), 0o600); err != nil {
		t.Fatal(err)
	}
	candidate := strings.Replace(minimalConfig, "    servers:", "    tls:\n      ca_file: "+ca+"\n    servers:", 1)
	if _, err := loadConfigText(t, candidate); err == nil {
		t.Fatal("expected unusable CA rejection")
	}
}

func TestConfigurationHelperDefaultsAndValidCA(t *testing.T) {
	env := Environment{}
	if !env.MQTTDiscoveryEnabled() || len(env.MQTTDiscoveryPorts()) != 1 {
		t.Fatal("discovery defaults")
	}
	disabled := false
	env.MQTTDiscovery = &MQTTDiscoveryConfig{Enabled: &disabled, AdminPorts: []int{8081, 8082}}
	if env.MQTTDiscoveryEnabled() || len(env.MQTTDiscoveryPorts()) != 2 {
		t.Fatal("discovery override")
	}
	if err := validateHTTPURL("https://localhost:8443/"); err != nil {
		t.Fatal(err)
	}
	if err := validateHTTPURL("http://localhost:99999"); err == nil {
		t.Fatal("invalid port accepted")
	}

	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "test"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), IsCA: true, BasicConstraintsValid: true}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	ca := filepath.Join(t.TempDir(), "ca.pem")
	certificate := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	if err := os.WriteFile(ca, certificate, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := validateCAFile(ca); err != nil {
		t.Fatal(err)
	}
	if err := validateCAFile(filepath.Join(t.TempDir(), "missing.pem")); err == nil {
		t.Fatal("missing CA accepted")
	}

	ldap := &LDAPAuthConfig{URL: "ldaps://ldap.example.com", CAFile: ca, UserBaseDN: "dc=example,dc=com", SubjectAttribute: "entryUUID", DefaultRole: "viewer"}
	if err := validateLDAPProvider("corp", ldap); err != nil {
		t.Fatal(err)
	}
	if ldap.Timeout != 5*time.Second || ldap.UserFilter == "" || ldap.UsernameAttribute == "" || ldap.DisplayNameAttribute == "" || ldap.EmailAttribute == "" || ldap.GroupAttribute == "" {
		t.Fatalf("LDAP defaults=%+v", ldap)
	}
	for name, cfg := range map[string]*LDAPAuthConfig{
		"bad URL":        {URL: "bad", UserBaseDN: "x", SubjectAttribute: "id", DefaultRole: "viewer"},
		"bind mismatch":  {URL: "ldaps://ldap", BindDN: "cn=bind", UserBaseDN: "x", SubjectAttribute: "id", DefaultRole: "viewer"},
		"missing groups": {URL: "ldaps://ldap", UserBaseDN: "x", SubjectAttribute: "id"},
		"invalid CA":     {URL: "ldaps://ldap", CAFile: filepath.Join(t.TempDir(), "missing"), UserBaseDN: "x", SubjectAttribute: "id", DefaultRole: "viewer"},
	} {
		t.Run(name, func(t *testing.T) {
			if err := validateLDAPProvider("corp", cfg); err == nil {
				t.Fatal("expected error")
			}
		})
	}
}

func TestLoadRejectsDisabledLocalAuthentication(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "config.yaml")
	os.WriteFile(p, []byte(`
session_secret: "test-secret-that-is-at-least-32-characters-long"
secure_cookies: true
authentication:
  local:
    enabled: false
    break_glass_login: true
environments:
  - name: dev
    servers:
      - url: "http://localhost:8222"
`), 0o644)

	if _, err := Load(p); err == nil {
		t.Fatal("expected disabled local authentication to be rejected")
	}
}

func TestLoadPreservesProviderOrderAndRejectsDuplicates(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "config.yaml")
	os.WriteFile(p, []byte(`
session_secret: "test-secret-that-is-at-least-32-characters-long"
authentication:
  providers:
    - name: first
      type: ldap
      ldap:
        url: "ldap://ldap-one.example.com"
        allow_plaintext: true
        user_base_dn: "dc=example,dc=com"
        subject_attribute: "entryUUID"
        default_role: viewer
    - name: second
      type: ldap
      ldap:
        url: "ldap://ldap-two.example.com"
        allow_plaintext: true
        user_base_dn: "dc=example,dc=com"
        subject_attribute: "entryUUID"
        default_role: viewer
environments:
  - name: dev
    servers:
      - url: "http://localhost:8222"
`), 0o644)
	cfg, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Authentication.Providers[0].Name != "first" || cfg.Authentication.Providers[1].Name != "second" {
		t.Fatalf("provider order changed: %+v", cfg.Authentication.Providers)
	}

	os.WriteFile(p, []byte(`
session_secret: "test-secret-that-is-at-least-32-characters-long"
authentication:
  providers:
    - name: duplicate
      type: ldap
      ldap:
        url: "ldap://ldap-one.example.com"
        allow_plaintext: true
        user_base_dn: "dc=example,dc=com"
        subject_attribute: "entryUUID"
        default_role: viewer
    - name: duplicate
      type: ldap
      ldap:
        url: "ldap://ldap-two.example.com"
        allow_plaintext: true
        user_base_dn: "dc=example,dc=com"
        subject_attribute: "entryUUID"
        default_role: viewer
environments:
  - name: dev
    servers:
      - url: "http://localhost:8222"
`), 0o644)
	if _, err := Load(p); err == nil {
		t.Fatal("expected duplicate provider name to be rejected")
	}
}

func TestLoadOIDCProvider(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "config.yaml")
	os.WriteFile(p, []byte(`
session_secret: "test-secret-that-is-at-least-32-characters-long"
secure_cookies: true
authentication:
  public_url: "https://dashboard.example.com/"
  providers:
    - name: entra
      type: oidc
      oidc:
        issuer_url: "https://login.example.com/tenant/v2.0"
        client_id: "dashboard"
        client_secret: "secret"
        admin_groups: ["Dashboard Admins"]
environments:
  - name: dev
    servers:
      - url: "http://localhost:8222"
`), 0o644)
	cfg, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	oidc := cfg.Authentication.Providers[0].OIDC
	if oidc.UsernameClaim != "preferred_username" || oidc.GroupsClaim != "groups" {
		t.Errorf("OIDC claim defaults = %+v", oidc)
	}
	if cfg.Authentication.PublicURL != "https://dashboard.example.com" {
		t.Errorf("public URL = %q", cfg.Authentication.PublicURL)
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

func TestDiscoveryTrustedHostsNormalizesEveryConfiguredSource(t *testing.T) {
	env := Environment{
		Servers:     []Server{{URL: "https://NATS.Example.COM:8222/varz"}, {URL: "http://"}},
		MQTTBridges: []MQTTBridge{{URL: "bridge.example.com:8080/readyz"}},
		NATSConn:    &NATSConnConfig{URLs: []string{"nats://[2001:db8::1]:4222", "nats://push.example.com:4222?x=1"}},
		MQTTDiscovery: &MQTTDiscoveryConfig{TrustedHosts: []string{
			"Explicit.Example.COM", "",
		}},
	}
	want := []string{"nats.example.com", "bridge.example.com", "2001:db8::1", "push.example.com", "explicit.example.com"}
	hosts := env.DiscoveryTrustedHosts()
	if len(hosts) != len(want) {
		t.Fatalf("trusted hosts = %v, want exactly %v", hosts, want)
	}
	for _, host := range want {
		if !hosts[host] {
			t.Errorf("trusted hosts missing %q: %v", host, hosts)
		}
	}
}

func TestHostFromURLVariants(t *testing.T) {
	for raw, want := range map[string]string{
		"https://host.example:8443/path?x=1": "host.example",
		"host.example:4222/path":             "host.example",
		"host.example/path":                  "host.example",
		"[2001:db8::2]:4222":                 "2001:db8::2",
		"plain-host":                         "plain-host",
	} {
		if got := hostFromURL(raw); got != want {
			t.Errorf("hostFromURL(%q) = %q, want %q", raw, got, want)
		}
	}
}
