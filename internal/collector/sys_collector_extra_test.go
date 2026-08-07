package collector

import (
	"context"
	"encoding/json"
	"log/slog"
	"testing"
	"time"

	nats "github.com/nats-io/nats.go"
	"github.com/noodlebit/machmqtt-dashboard/internal/config"
	"github.com/noodlebit/machmqtt-dashboard/internal/testutil/natstest"
)

func TestSYSCollectorRunSweepsExpired(t *testing.T) {
	s := natstest.NewWithSysAccount(t)
	sc := newSYSCollector()
	sc.ttl = 60 * time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go sc.run(ctx, &config.NATSConnConfig{URLs: []string{s.ClientURL()}, SYSCollection: true})
	waitSysConnected(t, sc)

	// Inject a STATSZ entry with an old timestamp so the run-loop sweeper removes
	// it on its next tick.
	sc.mu.Lock()
	sc.statsz["stale-srv"] = &statszEntry{when: time.Now().Add(-time.Second)}
	sc.mu.Unlock()

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		sc.mu.RLock()
		_, ok := sc.statsz["stale-srv"]
		sc.mu.RUnlock()
		if !ok {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("run-loop sweeper never removed the stale STATSZ entry")
}

func TestSYSCollectorDiagnosticWarnsNoData(t *testing.T) {
	// A plain server (no system account) emits no STATSZ events, so the
	// diagnostic warning fires after diagDelay.
	s := natstest.New(t)
	rec := &recordingHandler{}
	sc := newSYSCollector()
	sc.log = slog.New(rec)
	sc.diagDelay = 50 * time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go sc.run(ctx, &config.NATSConnConfig{URLs: []string{s.ClientURL()}, SYSCollection: true})

	rec.waitForMessage(t, "no $SYS.SERVER")
}

func waitSysConnected(t *testing.T, sc *SYSCollector) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		sc.mu.RLock()
		connected := sc.nc != nil
		sc.mu.RUnlock()
		if connected {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("SYS collector never connected")
}

// TestSYSCollectorConnectedFalseWhenLinkDown mirrors the MQTT subscriber's
// contract: the client reconnects indefinitely, so the conn stays non-nil after
// the server disappears and Connected() must report the link state, not merely
// that a conn was created.
func TestSYSCollectorConnectedFalseWhenLinkDown(t *testing.T) {
	s := natstest.NewWithSysAccount(t)
	sc := newSYSCollector()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go sc.run(ctx, &config.NATSConnConfig{URLs: []string{s.ClientURL()}, SYSCollection: true})
	waitSysConnected(t, sc)
	if !sc.Connected() {
		t.Fatal("Connected() should be true while the server is up")
	}

	s.Shutdown()

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if !sc.Connected() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("Connected() stayed true after the NATS server went away")
}

// TestSYSCollectorRunHandlesSTATSZMessages drives the run() subscribe callback
// with a malformed message, an empty-server-ID message, and a valid one, so the
// branches inside the handler are exercised.
func TestSYSCollectorRunHandlesSTATSZMessages(t *testing.T) {
	s := natstest.NewWithSysAccount(t)
	sc := newSYSCollector()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go sc.run(ctx, &config.NATSConnConfig{URLs: []string{s.ClientURL()}, SYSCollection: true})
	waitSysConnected(t, sc)

	nc, err := nats.Connect(s.ClientURL())
	if err != nil {
		t.Fatal(err)
	}
	defer nc.Close()

	// Malformed JSON → warn-once branch, ignored.
	nc.Publish("$SYS.SERVER.bad.STATSZ", []byte("not json"))
	// Empty server ID → skipped.
	empty, _ := json.Marshal(sysStatsMsg{})
	nc.Publish("$SYS.SERVER.empty.STATSZ", empty)
	// Valid → cached.
	valid, _ := json.Marshal(sysStatsMsg{Server: sysServerInfo{ID: "srv-9", Name: "node-9"}})
	nc.Publish("$SYS.SERVER.srv-9.STATSZ", valid)
	nc.Flush()

	// Note: the in-process NATS server also emits its own STATSZ, so the cache
	// holds the real server plus our injected one — assert on our key directly,
	// and that the empty-ID message produced no entry.
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		sc.mu.RLock()
		_, ok := sc.statsz["srv-9"]
		_, emptyCached := sc.statsz[""]
		sc.mu.RUnlock()
		if ok {
			if emptyCached {
				t.Error("empty server-ID message should not have been cached")
			}
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("valid STATSZ message was never cached")
}

// TestJSZPingBody pins the slow/fast split for the PING.JSZ request body. Slow
// polls must request stream+consumer detail so the JetStream page can render
// streams under $SYS collection; fast polls must request nothing (they carry the
// previous slow poll's JSInfo forward). A regression here silently empties the
// JetStream page for every $SYS-collected cluster.
func TestJSZPingBody(t *testing.T) {
	if jszPingBody(false) != nil {
		t.Error("fast poll should send no JSZ detail body (relies on carry-forward)")
	}
	body := jszPingBody(true)
	if body == nil {
		t.Fatal("slow poll should request JSZ detail")
	}
	// Decode using the SAME json tags nats-server's JSzOptions declares — notably
	// `consumer` (singular), not `consumers`. The $SYS path unmarshals this body
	// straight into JSzOptions, so a plural key would silently bind to nothing and
	// drop consumer_detail. This struct mirrors the server contract so that footgun
	// can't regress unnoticed.
	var opts struct {
		Streams  bool `json:"streams"`
		Consumer bool `json:"consumer"`
		Config   bool `json:"config"`
	}
	if err := json.Unmarshal(body, &opts); err != nil {
		t.Fatalf("JSZ detail body is not valid JSON: %v", err)
	}
	if !opts.Streams {
		t.Errorf("JSZ detail body must request streams (streams=true auto-enables account_details), got %s", body)
	}
	if !opts.Consumer {
		t.Errorf("JSZ detail body must request consumer detail via the singular `consumer` key, got %s", body)
	}
	if !opts.Config {
		t.Errorf("JSZ detail body must request config (stream/consumer config blocks are null without it), got %s", body)
	}
}
