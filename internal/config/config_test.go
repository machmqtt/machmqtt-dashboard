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
environments:
  - name: dev
    servers:
      - url: "http://localhost:8222"
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
	if len(cfg.Environments) != 1 {
		t.Fatalf("environments = %d, want 1", len(cfg.Environments))
	}
	if cfg.Environments[0].Name != "dev" {
		t.Errorf("env name = %q, want dev", cfg.Environments[0].Name)
	}
}

func TestLoadMissingSecret(t *testing.T) {
	t.Setenv("SESSION_SECRET", "")
	dir := t.TempDir()
	p := filepath.Join(dir, "config.yaml")
	os.WriteFile(p, []byte(`
environments:
  - name: dev
    servers:
      - url: "http://localhost:8222"
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
environments:
  - name: dev
    servers:
      - url: "http://localhost:8222"
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
environments:
  - name: dev
    servers:
      - url: "http://localhost:8222"
`), 0o644)

	cfg, err := Load(p)
	if err != nil {
		t.Fatalf("expected SESSION_SECRET env to satisfy validation: %v", err)
	}
	if cfg.SessionSecret != envSecret {
		t.Errorf("session_secret = %q, want env-supplied value", cfg.SessionSecret)
	}
}

func TestLoadNoEnvironments(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "config.yaml")
	os.WriteFile(p, []byte(`
session_secret: "test-secret-that-is-at-least-32-characters-long"
`), 0o644)

	_, err := Load(p)
	if err == nil {
		t.Fatal("expected error for no environments")
	}
}

func TestLoadDefaults(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "config.yaml")
	os.WriteFile(p, []byte(`
session_secret: "test-secret-that-is-at-least-32-characters-long"
environments:
  - name: dev
    servers:
      - url: "http://localhost:8222"
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
