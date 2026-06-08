package config

import (
	"os"
	"path/filepath"
	"testing"
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
