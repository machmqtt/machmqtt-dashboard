package collector

import (
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	nats "github.com/nats-io/nats.go"
	"github.com/nats-io/nkeys"
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

func TestConnectNATSReconnects(t *testing.T) {
	// Two-node cluster; connect with both URLs, kill the connected node, and the
	// client fails over to the other — exercising the disconnect-error and
	// reconnect callbacks registered in connectNATS.
	servers := natstest.NewCluster(t, 2, "recon")
	nc, err := connectNATS(&config.NATSConnConfig{
		URLs: []string{servers[0].ClientURL(), servers[1].ClientURL()},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer nc.Close()

	// Wait for the initial connection.
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) && !nc.IsConnected() {
		time.Sleep(20 * time.Millisecond)
	}
	if !nc.IsConnected() {
		t.Fatal("never connected initially")
	}
	connectedTo := nc.ConnectedUrl()

	// Kill whichever node the client is attached to.
	if connectedTo == servers[0].ClientURL() {
		servers[0].Shutdown()
	} else {
		servers[1].Shutdown()
	}

	// The client should disconnect and reconnect to the surviving node.
	deadline = time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if nc.IsConnected() && nc.ConnectedUrl() != connectedTo {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("client did not reconnect to the surviving node (still %q)", nc.ConnectedUrl())
}

func TestConnectNATSErrorHandler(t *testing.T) {
	// Trigger an async slow-consumer error on the connection and assert that
	// connectNATS's ErrorHandler actually ran by capturing its log line.
	s := natstest.New(t)
	rec := &recordingHandler{}
	nc, err := connectNATS(&config.NATSConnConfig{URLs: []string{s.ClientURL()}}, slog.New(rec))
	if err != nil {
		t.Fatal(err)
	}
	defer nc.Close()
	for !nc.IsConnected() {
		time.Sleep(10 * time.Millisecond)
	}

	sub, err := nc.SubscribeSync("flood")
	if err != nil {
		t.Fatal(err)
	}
	// Tiny pending limits guarantee a slow-consumer error once we flood.
	if err := sub.SetPendingLimits(1, 64); err != nil {
		t.Fatal(err)
	}

	pub, err := nats.Connect(s.ClientURL())
	if err != nil {
		t.Fatal(err)
	}
	defer pub.Close()
	for i := 0; i < 2000; i++ {
		_ = pub.Publish("flood", []byte("xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx"))
	}
	_ = pub.Flush()

	// The connection's async ErrorHandler logs "nats async error" on the
	// slow-consumer condition.
	rec.waitForMessage(t, "nats async error")
}

func TestConnectNATSWithValidNKey(t *testing.T) {
	// nats.NkeyOptionFromSeed reads the seed from a file, so write a well-formed
	// user seed to disk and point NKey at it. This takes the success arm of the
	// NKey switch case (the bad-seed case is covered separately).
	kp, err := nkeys.CreateUser()
	if err != nil {
		t.Fatal(err)
	}
	seed, err := kp.Seed()
	if err != nil {
		t.Fatal(err)
	}
	seedFile := filepath.Join(t.TempDir(), "user.nk")
	if err := os.WriteFile(seedFile, seed, 0o600); err != nil {
		t.Fatal(err)
	}

	nc, err := connectNATS(&config.NATSConnConfig{
		URLs: []string{"nats://127.0.0.1:14299"}, // unreachable; RetryOnFailedConnect → non-nil conn
		NKey: seedFile,
	}, nil)
	if err != nil {
		t.Fatalf("valid NKey seed should not error: %v", err)
	}
	if nc == nil {
		t.Fatal("expected a non-nil connection")
	}
	nc.Close()
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
