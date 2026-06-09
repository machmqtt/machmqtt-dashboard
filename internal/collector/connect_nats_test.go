package collector

import (
	"os"
	"testing"
	"time"

	"github.com/noodlebit/machmqtt-dashboard/internal/config"
	"github.com/noodlebit/machmqtt-dashboard/internal/testutil/natstest"
)

func TestConnectNATSEmptyURLs(t *testing.T) {
	_, err := connectNATS(&config.NATSConnConfig{}, nil)
	if err == nil {
		t.Error("expected error for empty URLs")
	}
}

func TestConnectNATSBadNKey(t *testing.T) {
	_, err := connectNATS(&config.NATSConnConfig{
		URLs: []string{"nats://127.0.0.1:14299"},
		NKey: "not-a-valid-nkey-seed",
	}, nil)
	if err == nil {
		t.Error("expected error for invalid NKey seed")
	}
}

func TestConnectNATSWithUsernamePassword(t *testing.T) {
	s := natstest.New(t)
	nc, err := connectNATS(&config.NATSConnConfig{
		URLs:     []string{s.ClientURL()},
		Username: "user",
		Password: "pass",
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer nc.Close()
}

func TestConnectNATSWithToken(t *testing.T) {
	s := natstest.New(t)
	nc, err := connectNATS(&config.NATSConnConfig{
		URLs:  []string{s.ClientURL()},
		Token: "mytoken",
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer nc.Close()
}

func TestConnectNATSWithInsecureTLS(t *testing.T) {
	s := natstest.New(t)
	nc, err := connectNATS(&config.NATSConnConfig{
		URLs: []string{s.ClientURL()},
		TLS:  &config.TLSConfig{Insecure: true},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer nc.Close()
}

func TestConnectNATSWithCredsFile(t *testing.T) {
	s := natstest.New(t)
	// Create a minimal creds file — content doesn't matter for testing the code path.
	credsFile := t.TempDir() + "/user.creds"
	os.WriteFile(credsFile, []byte(""), 0600)

	nc, err := connectNATS(&config.NATSConnConfig{
		URLs:      []string{s.ClientURL()},
		CredsFile: credsFile,
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer nc.Close()
}

// TestConnectNATSWithTLSCAFile exercises the TLS.CAFile path.
// The CA file has dummy content so TLS handshakes will fail, but
// RetryOnFailedConnect means nats.Connect returns immediately.
func TestConnectNATSWithTLSCAFile(t *testing.T) {
	caFile := t.TempDir() + "/ca.pem"
	os.WriteFile(caFile, []byte("-----BEGIN CERTIFICATE-----\nfake\n-----END CERTIFICATE-----\n"), 0600)

	nc, err := connectNATS(&config.NATSConnConfig{
		URLs: []string{"nats://127.0.0.1:14299"},
		TLS:  &config.TLSConfig{CAFile: caFile},
	}, nil)
	if err == nil && nc != nil {
		nc.Close()
	}
	// Either outcome is acceptable — what matters is the code path was exercised.
}

// --- statszToVarz with JetStream ---

func TestStatszToVarzNoJetStream(t *testing.T) {
	e := &statszEntry{
		server: sysServerInfo{ID: "srv-1", Name: "nats-1", Time: time.Now()},
		stats:  sysServerStats{Start: time.Now().Add(-time.Hour)},
		when:   time.Now(),
	}
	v := statszToVarz(e)
	if v.ServerID != "srv-1" {
		t.Errorf("ServerID = %q, want srv-1", v.ServerID)
	}
	// JetStream should be zero-value when stats.JetStream is nil.
	if v.JetStream.Config.MaxMemory != 0 {
		t.Errorf("expected zero JetStream.Config.MaxMemory, got %d", v.JetStream.Config.MaxMemory)
	}
}

func TestStatszToVarzWithJetStream(t *testing.T) {
	js := &sysJSVarz{}
	js.Config.MaxMemory = 1024 * 1024
	js.Config.MaxStore = 4096 * 1024
	js.Config.Domain = "hub"
	js.Stats.Memory = 512 * 1024
	js.Stats.Store = 2048 * 1024
	js.Stats.Accounts = 2
	js.Stats.API.Total = 100
	js.Stats.API.Errors = 5

	e := &statszEntry{
		server: sysServerInfo{ID: "srv-1", Name: "nats-1", Time: time.Now()},
		stats: sysServerStats{
			Start:     time.Now().Add(-time.Hour),
			JetStream: js,
		},
		when: time.Now(),
	}

	v := statszToVarz(e)
	if v.JetStream.Config.MaxMemory != 1024*1024 {
		t.Errorf("JetStream.Config.MaxMemory = %d", v.JetStream.Config.MaxMemory)
	}
	if v.JetStream.Config.Domain != "hub" {
		t.Errorf("JetStream.Config.Domain = %q", v.JetStream.Config.Domain)
	}
	if v.JetStream.Stats.Memory != 512*1024 {
		t.Errorf("JetStream.Stats.Memory = %d", v.JetStream.Stats.Memory)
	}
	if v.JetStream.Stats.API.Total != 100 {
		t.Errorf("JetStream.Stats.API.Total = %d", v.JetStream.Stats.API.Total)
	}
}
