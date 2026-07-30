package collector

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	nats "github.com/nats-io/nats.go"
	"github.com/noodlebit/machmqtt-dashboard/internal/config"
	"github.com/noodlebit/machmqtt-dashboard/internal/testutil/natstest"
)

// recordingHandler is a slog.Handler that captures emitted messages so tests can
// assert that a specific diagnostic/warning was logged.
type recordingHandler struct {
	mu   sync.Mutex
	msgs []string
}

func (h *recordingHandler) Enabled(context.Context, slog.Level) bool { return true }
func (h *recordingHandler) Handle(_ context.Context, r slog.Record) error {
	h.mu.Lock()
	h.msgs = append(h.msgs, r.Message)
	h.mu.Unlock()
	return nil
}
func (h *recordingHandler) WithAttrs([]slog.Attr) slog.Handler { return h }
func (h *recordingHandler) WithGroup(string) slog.Handler      { return h }

func (h *recordingHandler) waitForMessage(t *testing.T, substr string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		h.mu.Lock()
		for _, m := range h.msgs {
			if strings.Contains(m, substr) {
				h.mu.Unlock()
				return
			}
		}
		h.mu.Unlock()
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("no log message containing %q was emitted", substr)
}

func TestMQTTSubscriberRunSweepsExpired(t *testing.T) {
	s := natstest.New(t)
	sub := newMQTTSubscriber()
	sub.ttl = 60 * time.Millisecond // run-loop sweeper fires every ttl/3
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go sub.run(ctx, &config.NATSConnConfig{URLs: []string{s.ClientURL()}, SubjectPrefix: "$MQTT5"})
	waitSubscriberConnected(t, sub)

	nc, err := nats.Connect(s.ClientURL())
	if err != nil {
		t.Fatal(err)
	}
	defer nc.Close()
	data, _ := json.Marshal(BridgeMetricsMsg{V: 1, InstanceName: "ephemeral"})
	nc.Publish("$MQTT5.metrics.ephemeral", data)
	nc.Flush()

	waitForBridges(t, sub, 1)
	// No further publishes → the entry goes stale and the run-loop sweeper
	// removes it.
	waitForBridges(t, sub, 0)
}

func TestMQTTSubscriberDiagnosticWarnsNoData(t *testing.T) {
	s := natstest.New(t)
	rec := &recordingHandler{}
	sub := newMQTTSubscriber()
	sub.log = slog.New(rec)
	sub.diagDelay = 50 * time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go sub.run(ctx, &config.NATSConnConfig{URLs: []string{s.ClientURL()}, SubjectPrefix: "$MQTT5"})

	// No metrics published → the diagnostic fires after diagDelay.
	rec.waitForMessage(t, "no bridge metrics received")
}

// waitSubscriberConnected blocks until the subscriber reports Connected, or
// fails after 3s.
func waitSubscriberConnected(t *testing.T, sub *MQTTSubscriber) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if sub.Connected() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("subscriber never connected")
}

func TestMQTTSubscriberConnected(t *testing.T) {
	s := natstest.New(t)
	sub := newMQTTSubscriber()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go sub.run(ctx, &config.NATSConnConfig{URLs: []string{s.ClientURL()}, SubjectPrefix: "$MQTT5"})

	waitSubscriberConnected(t, sub) // asserts Connected() flips to true
}

func TestMQTTSubscriberSkipsNewerSchema(t *testing.T) {
	s := natstest.New(t)
	sub := newMQTTSubscriber()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go sub.run(ctx, &config.NATSConnConfig{URLs: []string{s.ClientURL()}, SubjectPrefix: "$MQTT5"})
	waitSubscriberConnected(t, sub)

	nc, err := nats.Connect(s.ClientURL())
	if err != nil {
		t.Fatal(err)
	}
	defer nc.Close()

	// A message from a newer schema version must be skipped, not misinterpreted.
	msg := BridgeMetricsMsg{V: bridgeMetricsSchemaV + 1, InstanceName: "future-bridge"}
	data, _ := json.Marshal(msg)
	nc.Publish("$MQTT5.metrics.future-bridge", data)
	nc.Flush()
	time.Sleep(150 * time.Millisecond)

	if got := len(sub.Bridges()); got != 0 {
		t.Errorf("bridges = %d, want 0 (newer-schema message skipped)", got)
	}
}

// TestMQTTSubscriberDrainedKeepsBridgeDraining pins the drain semantics: a drain
// stops new connections but keeps existing sessions alive, so a draining instance
// must stay on the fleet page — labelled Draining, not Ready, with its still-live
// connection counts — for as long as it keeps publishing. Dropping it on the
// drained message would hide a broker that is still serving clients.
func TestMQTTSubscriberDrainedKeepsBridgeDraining(t *testing.T) {
	s := natstest.New(t)
	sub := newMQTTSubscriber() // default TTL: the sweeper must not interfere here
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go sub.run(ctx, &config.NATSConnConfig{URLs: []string{s.ClientURL()}, SubjectPrefix: "$MQTT5"})
	waitSubscriberConnected(t, sub)

	nc, err := nats.Connect(s.ClientURL())
	if err != nil {
		t.Fatal(err)
	}
	defer nc.Close()

	publish := func(m BridgeMetricsMsg) {
		data, _ := json.Marshal(m)
		nc.Publish("$MQTT5.metrics.b", data)
		nc.Flush()
	}

	// First a live metrics message → bridge present and ready.
	publish(BridgeMetricsMsg{
		V: 1, InstanceName: "b",
		NATS:    BridgeMsgNATS{Connected: true},
		Metrics: &MQTTMetrics{ConnectionsActive: 4},
	})
	waitForBridges(t, sub, 1)

	// Then a drained message with sessions still attached: still listed, now
	// Draining and not Ready, and its connection count is still reported.
	publish(BridgeMetricsMsg{
		V: 1, InstanceName: "b", Drained: true,
		NATS:    BridgeMsgNATS{Connected: true},
		Metrics: &MQTTMetrics{ConnectionsActive: 4},
	})
	waitForBridgeState(t, sub, "b", func(inst MQTTBridgeInstance) bool {
		return inst.Status != nil && inst.Status.Draining
	}, "Draining=true")

	bridges := sub.Bridges()
	if len(bridges) != 1 {
		t.Fatalf("bridges = %d, want 1 (a draining instance stays listed)", len(bridges))
	}
	st := bridges[0].Status
	if !st.Draining {
		t.Error("Status.Draining = false, want true")
	}
	if st.Ready {
		t.Error("Status.Ready = true, want false (a draining instance is not ready)")
	}
	if st.Connections != 4 {
		t.Errorf("Status.Connections = %d, want 4 (sessions survive a drain)", st.Connections)
	}
	if st.Metrics == nil || st.Metrics.Drained != 1 {
		t.Errorf("Metrics.Drained = %v, want 1", st.Metrics)
	}
}

// TestMQTTSubscriberDrainedBridgeExpiresAfterTTL is the other half: a drained
// instance is removed only once it stops publishing, by the same TTL sweep that
// removes any silent bridge.
func TestMQTTSubscriberDrainedBridgeExpiresAfterTTL(t *testing.T) {
	s := natstest.New(t)
	sub := newMQTTSubscriber()
	sub.ttl = 60 * time.Millisecond // run-loop sweeper fires every ttl/3
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go sub.run(ctx, &config.NATSConnConfig{URLs: []string{s.ClientURL()}, SubjectPrefix: "$MQTT5"})
	waitSubscriberConnected(t, sub)

	nc, err := nats.Connect(s.ClientURL())
	if err != nil {
		t.Fatal(err)
	}
	defer nc.Close()

	data, _ := json.Marshal(BridgeMetricsMsg{V: 1, InstanceName: "b", Drained: true})
	nc.Publish("$MQTT5.metrics.b", data)
	nc.Flush()

	// No further publishes → the drained entry goes stale and the sweeper drops it.
	waitForBridges(t, sub, 0)
}

// TestMQTTSubscriberLastSeenTracksPublishes pins the staleness signal: every
// cached bridge reports when its last metrics publish was received, and a
// re-publish moves it forward.
func TestMQTTSubscriberLastSeenTracksPublishes(t *testing.T) {
	s := natstest.New(t)
	sub := newMQTTSubscriber()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go sub.run(ctx, &config.NATSConnConfig{URLs: []string{s.ClientURL()}, SubjectPrefix: "$MQTT5"})
	waitSubscriberConnected(t, sub)

	nc, err := nats.Connect(s.ClientURL())
	if err != nil {
		t.Fatal(err)
	}
	defer nc.Close()

	publish := func() {
		data, _ := json.Marshal(BridgeMetricsMsg{
			V: 1, InstanceName: "b",
			Metrics: &MQTTMetrics{ConnectionsActive: 1},
		})
		nc.Publish("$MQTT5.metrics.b", data)
		nc.Flush()
	}

	before := time.Now()
	publish()
	waitForBridges(t, sub, 1)
	first := sub.Bridges()[0].LastSeen
	if first.Before(before) || first.After(time.Now()) {
		t.Fatalf("LastSeen = %v, want between %v and now", first, before)
	}

	// A later publish for the same instance moves LastSeen forward.
	time.Sleep(20 * time.Millisecond)
	publish()
	waitForBridgeState(t, sub, "b", func(inst MQTTBridgeInstance) bool {
		return inst.LastSeen.After(first)
	}, "LastSeen advanced")
}

// TestMQTTSubscriberByteRatesFromPoolSlots pins the push path's byte rates, which
// are summed from the pool slots' cumulative per-slot counters. The direction
// pairing is the assertion that matters: a slot's out_bytes is what the bridge
// wrote to NATS, which is the same flow the connz-scan path reports as the
// bridge's in_bytes — both paths write these same fields and the same stored
// series, so In must mean "bridge → NATS" on both.
func TestMQTTSubscriberByteRatesFromPoolSlots(t *testing.T) {
	s := natstest.New(t)
	sub := newMQTTSubscriber()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go sub.run(ctx, &config.NATSConnConfig{URLs: []string{s.ClientURL()}, SubjectPrefix: "$MQTT5"})
	waitSubscriberConnected(t, sub)

	nc, err := nats.Connect(s.ClientURL())
	if err != nil {
		t.Fatal(err)
	}
	defer nc.Close()

	publish := func(name string, slots ...BridgePoolSlot) {
		data, _ := json.Marshal(BridgeMetricsMsg{
			V: 1, InstanceName: name,
			Metrics: &MQTTMetrics{ConnectionsActive: 1},
			Pool:    BridgePool{Size: len(slots), Slots: slots},
		})
		nc.Publish("$MQTT5.metrics."+name, data)
		nc.Flush()
	}
	bridge := func(name string) MQTTBridgeInstance {
		t.Helper()
		for _, inst := range sub.Bridges() {
			if inst.ConfiguredName == name {
				return inst
			}
		}
		t.Fatalf("bridge %q not cached", name)
		return MQTTBridgeInstance{}
	}

	// Publishing to NATS only: slot out_bytes grows, in_bytes is flat.
	publish("up", BridgePoolSlot{Index: 0, OutBytes: 1000, InBytes: 500},
		BridgePoolSlot{Index: 1, OutBytes: 200, InBytes: 100})
	waitForBridges(t, sub, 1)
	publish("up", BridgePoolSlot{Index: 0, OutBytes: 3000, InBytes: 500},
		BridgePoolSlot{Index: 1, OutBytes: 400, InBytes: 100})
	waitForBridgeState(t, sub, "up", func(inst MQTTBridgeInstance) bool {
		return inst.InBytesRate > 0
	}, "InBytesRate > 0")
	if got := bridge("up"); got.OutBytesRate != 0 {
		t.Errorf("OutBytesRate = %v, want 0 (slot in_bytes did not change)", got.OutBytesRate)
	}

	// The mirror case: consuming from NATS only.
	publish("down", BridgePoolSlot{Index: 0, OutBytes: 10, InBytes: 1000})
	waitForBridges(t, sub, 2)
	publish("down", BridgePoolSlot{Index: 0, OutBytes: 10, InBytes: 9000})
	waitForBridgeState(t, sub, "down", func(inst MQTTBridgeInstance) bool {
		return inst.OutBytesRate > 0
	}, "OutBytesRate > 0")
	if got := bridge("down"); got.InBytesRate != 0 {
		t.Errorf("InBytesRate = %v, want 0 (slot out_bytes did not change)", got.InBytesRate)
	}

	// A slot vanishing on a pool rebuild makes the summed counters regress; the
	// rate must clamp to 0 rather than go negative.
	publish("up", BridgePoolSlot{Index: 0, OutBytes: 5, InBytes: 5})
	waitForBridgeState(t, sub, "up", func(inst MQTTBridgeInstance) bool {
		return inst.InBytesRate == 0 && inst.OutBytesRate == 0
	}, "byte rates clamped to 0 after a counter regression")
}

func TestNATSByteTotals(t *testing.T) {
	m := &BridgeMetricsMsg{Pool: BridgePool{Slots: []BridgePoolSlot{
		{Index: 0, OutBytes: 100, InBytes: 7},
		{Index: 1, OutBytes: 20, InBytes: 3},
	}}}
	toNATS, fromNATS := natsByteTotals(m)
	if toNATS != 120 {
		t.Errorf("toNATS = %d, want 120 (sum of slot out_bytes)", toNATS)
	}
	if fromNATS != 10 {
		t.Errorf("fromNATS = %d, want 10 (sum of slot in_bytes)", fromNATS)
	}
	// No slots at all → zero totals, not a panic.
	if to, from := natsByteTotals(&BridgeMetricsMsg{}); to != 0 || from != 0 {
		t.Errorf("empty pool totals = (%d, %d), want (0, 0)", to, from)
	}
}

// TestMQTTSubscriberBridgeCount covers the cheap liveness probe the poll loop
// uses to decide whether to run connz-scan discovery: it must agree with
// Bridges() without paying for the conversion.
func TestMQTTSubscriberBridgeCount(t *testing.T) {
	s := natstest.New(t)
	sub := newMQTTSubscriber()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go sub.run(ctx, &config.NATSConnConfig{URLs: []string{s.ClientURL()}, SubjectPrefix: "$MQTT5"})
	waitSubscriberConnected(t, sub)

	if got := sub.BridgeCount(); got != 0 {
		t.Fatalf("BridgeCount = %d, want 0 before any publish", got)
	}

	nc, err := nats.Connect(s.ClientURL())
	if err != nil {
		t.Fatal(err)
	}
	defer nc.Close()
	for _, name := range []string{"b1", "b2"} {
		data, _ := json.Marshal(BridgeMetricsMsg{V: 1, InstanceName: name})
		nc.Publish("$MQTT5.metrics."+name, data)
	}
	nc.Flush()
	waitForBridges(t, sub, 2)

	if got := sub.BridgeCount(); got != 2 {
		t.Errorf("BridgeCount = %d, want 2", got)
	}
}

// TestGuardedMsgHandlerContainsPanic covers the subscription-callback guard: a
// NATS callback runs on the client's dispatch goroutine, so a panic on one
// malformed payload would otherwise take the whole process down.
func TestGuardedMsgHandlerContainsPanic(t *testing.T) {
	rec := &recordingHandler{}
	calls := 0
	h := guardedMsgHandler(slog.New(rec), "test.subject", func(*nats.Msg) {
		calls++
		panic("bad payload")
	})

	// Two panicking messages: neither escapes, and the handler keeps being called.
	h(&nats.Msg{Subject: "test.subject"})
	h(&nats.Msg{Subject: "test.subject"})
	if calls != 2 {
		t.Errorf("handler called %d times, want 2 (a panic must not stop delivery)", calls)
	}
	rec.mu.Lock()
	defer rec.mu.Unlock()
	got := 0
	for _, m := range rec.msgs {
		if strings.Contains(m, "callback panicked") {
			got++
		}
	}
	if got != 2 {
		t.Errorf("logged %d panic reports, want 2 (each recovered panic is counted)", got)
	}
}

func TestGuardedMsgHandlerPassesMessagesThrough(t *testing.T) {
	var seen *nats.Msg
	h := guardedMsgHandler(slog.New(&recordingHandler{}), "s", func(m *nats.Msg) { seen = m })
	msg := &nats.Msg{Subject: "s", Data: []byte("x")}
	h(msg)
	if seen != msg {
		t.Error("wrapped handler did not receive the message")
	}
}

// TestSubscribeWithRetry covers the bounded, context-aware retry: a subscribe can
// fail transiently while the client re-establishes its connection, and the caller
// is a long-lived collector goroutine that would otherwise lose push data for the
// process's lifetime.
func TestSubscribeWithRetry(t *testing.T) {
	s := natstest.New(t)
	nc, err := nats.Connect(s.ClientURL())
	if err != nil {
		t.Fatal(err)
	}
	defer nc.Close()

	t.Run("succeeds on a live connection", func(t *testing.T) {
		rec := &recordingHandler{}
		sub, err := subscribeWithRetry(context.Background(), nc, "a.b", slog.New(rec),
			[]time.Duration{time.Millisecond}, func(*nats.Msg) {})
		if err != nil {
			t.Fatalf("subscribe failed: %v", err)
		}
		defer sub.Unsubscribe()
		rec.mu.Lock()
		defer rec.mu.Unlock()
		if len(rec.msgs) != 0 {
			t.Errorf("logged %v, want nothing on a first-attempt success", rec.msgs)
		}
	})

	t.Run("gives up after the bounded retries", func(t *testing.T) {
		closed, err := nats.Connect(s.ClientURL())
		if err != nil {
			t.Fatal(err)
		}
		closed.Close() // subscribing on a closed connection always fails

		rec := &recordingHandler{}
		delays := []time.Duration{time.Millisecond, time.Millisecond}
		if _, err := subscribeWithRetry(context.Background(), closed, "a.b", slog.New(rec), delays, func(*nats.Msg) {}); err == nil {
			t.Fatal("subscribe on a closed connection returned no error")
		}
		rec.mu.Lock()
		defer rec.mu.Unlock()
		retries := 0
		for _, m := range rec.msgs {
			if strings.Contains(m, "subscribe failed; retrying") {
				retries++
			}
		}
		if retries != len(delays) {
			t.Errorf("logged %d retries, want %d (one per configured delay)", retries, len(delays))
		}
	})

	t.Run("aborts when the context is cancelled", func(t *testing.T) {
		closed, err := nats.Connect(s.ClientURL())
		if err != nil {
			t.Fatal(err)
		}
		closed.Close()

		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		_, err = subscribeWithRetry(ctx, closed, "a.b", slog.New(&recordingHandler{}),
			[]time.Duration{time.Minute}, func(*nats.Msg) {})
		if !errors.Is(err, context.Canceled) {
			t.Errorf("err = %v, want context.Canceled (no waiting out the backoff)", err)
		}
	})
}

// waitForBridgeState blocks until the named bridge satisfies cond, failing with
// what was expected. Used where the assertion is about a cached bridge's content
// rather than the cache's size.
func waitForBridgeState(t *testing.T, sub *MQTTSubscriber, name string, cond func(MQTTBridgeInstance) bool, want string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		for _, inst := range sub.Bridges() {
			if inst.ConfiguredName == name && cond(inst) {
				return
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("bridge %q never reached: %s", name, want)
}

// TestBridgeMsgToInstanceNilMetrics covers the m.Metrics == nil sentinel branch:
// a message with no embedded Metrics yields the JS-absent sentinel (-1) and fills
// InstanceID/Drained from the top-level wire fields.
func TestBridgeMsgToInstanceNilMetrics(t *testing.T) {
	m := &BridgeMetricsMsg{
		V:            1,
		InstanceID:   "top-id",
		InstanceName: "b",
		Drained:      true,
	}
	inst := bridgeMsgToInstance("b", m)
	if inst.Status == nil || inst.Status.Metrics == nil {
		t.Fatal("Status.Metrics is nil")
	}
	if inst.Status.Metrics.ConsumerPendingMessages != -1 {
		t.Errorf("ConsumerPendingMessages = %d, want -1 (sentinel)", inst.Status.Metrics.ConsumerPendingMessages)
	}
	if inst.Status.Metrics.InstanceID != "top-id" {
		t.Errorf("InstanceID = %q, want top-id (filled from top-level)", inst.Status.Metrics.InstanceID)
	}
	if inst.Status.Metrics.Drained != 1 {
		t.Errorf("Drained = %d, want 1 (filled from top-level)", inst.Status.Metrics.Drained)
	}
	if !inst.Status.Draining {
		t.Error("Draining should be true (top-level m.Drained)")
	}
}

// TestBridgeMsgToInstancePrefersEmbedded covers the populated branch and the
// "prefer embedded values" logic: when Metrics already carries instance_id and
// drained, the top-level fields must NOT override them.
func TestBridgeMsgToInstancePrefersEmbedded(t *testing.T) {
	m := &BridgeMetricsMsg{
		V:            1,
		InstanceID:   "top-id",
		InstanceName: "b",
		Drained:      false,
		Metrics: &MQTTMetrics{
			ConnectionsActive:       4,
			SubscriptionsActive:     7,
			ConsumerPendingMessages: 12,
			InstanceID:              "embedded-id",
			Drained:                 1,
		},
	}
	inst := bridgeMsgToInstance("b", m)
	if inst.Status.Metrics.InstanceID != "embedded-id" {
		t.Errorf("InstanceID = %q, want embedded-id (embedded wins)", inst.Status.Metrics.InstanceID)
	}
	if inst.Status.Metrics.Drained != 1 {
		t.Errorf("Drained = %d, want 1 (embedded preserved)", inst.Status.Metrics.Drained)
	}
	if inst.Status.Metrics.SubscriptionsActive != 7 {
		t.Errorf("SubscriptionsActive = %d, want 7", inst.Status.Metrics.SubscriptionsActive)
	}
	if inst.Status.Metrics.ConsumerPendingMessages != 12 {
		t.Errorf("ConsumerPendingMessages = %d, want 12 (embedded value, not sentinel)", inst.Status.Metrics.ConsumerPendingMessages)
	}
	if inst.Status.Connections != 4 {
		t.Errorf("Status.Connections = %d, want 4 (from metrics.ConnectionsActive)", inst.Status.Connections)
	}
	// Drained arrives only inside the metrics object here, which is where the
	// broker puts it: the instance is still draining, so it must not read as Ready.
	if !inst.Status.Draining {
		t.Error("Status.Draining = false, want true (metrics.Drained is set)")
	}
	if inst.Status.Ready {
		t.Error("Status.Ready = true, want false for a draining instance")
	}
}

// TestBridgesConcurrentReadersNoRace pins the cache's immutability contract:
// Bridges() takes only a read lock, so anything it writes through the cached
// message is an unsynchronized write shared with every other reader — including
// the API handlers, which JSON-marshal the very same *MQTTMetrics the cache
// holds. Run under -race.
func TestBridgesConcurrentReadersNoRace(t *testing.T) {
	s := natstest.New(t)
	sub := newMQTTSubscriber()
	// Default TTL: a short one would let the sweeper empty the cache mid-test,
	// leaving nothing for the readers to contend over.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go sub.run(ctx, &config.NATSConnConfig{URLs: []string{s.ClientURL()}, SubjectPrefix: "$MQTT5"})
	waitSubscriberConnected(t, sub)

	nc, err := nats.Connect(s.ClientURL())
	if err != nil {
		t.Fatal(err)
	}
	defer nc.Close()

	// Live (non-drained) bridges carrying an embedded metrics object: that is the
	// struct the cache shares with readers. instance_id lives only at the top
	// level, the shape that invites an envelope→metrics fixup on read.
	for _, name := range []string{"b1", "b2"} {
		data, _ := json.Marshal(BridgeMetricsMsg{
			V: 1, InstanceName: name, InstanceID: "id-" + name,
			Metrics: &MQTTMetrics{ConnectionsActive: 3, ConsumerPendingMessages: 5},
			NATS:    BridgeMsgNATS{Connected: true, ServerName: "n1"},
		})
		nc.Publish("$MQTT5.metrics."+name, data)
	}
	nc.Flush()
	waitForBridges(t, sub, 2)

	const iterations = 200
	var wg sync.WaitGroup
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range iterations {
				for _, inst := range sub.Bridges() {
					if inst.Status.Metrics.InstanceID == "" {
						t.Error("InstanceID was not resolved at ingest")
					}
				}
			}
		}()
	}
	// Stands in for the API handler serving push.Status.Metrics, which aliases
	// the cached struct rather than copying it.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for range iterations {
			for _, inst := range sub.Bridges() {
				if _, err := json.Marshal(inst.Status.Metrics); err != nil {
					t.Error(err)
				}
			}
		}
	}()
	wg.Wait()
}

// TestMQTTSubscriberConnectedFalseWhenLinkDown covers the health signal behind
// the dashboard's "push connected" badge: the client reconnects indefinitely, so
// the conn stays non-nil after the server disappears and only the link state
// distinguishes healthy from unreachable.
func TestMQTTSubscriberConnectedFalseWhenLinkDown(t *testing.T) {
	s := natstest.New(t)
	sub := newMQTTSubscriber()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go sub.run(ctx, &config.NATSConnConfig{URLs: []string{s.ClientURL()}, SubjectPrefix: "$MQTT5"})
	waitSubscriberConnected(t, sub)

	s.Shutdown()

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if !sub.Connected() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("Connected() stayed true after the NATS server went away")
}

func waitForBridges(t *testing.T, sub *MQTTSubscriber, want int) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if len(sub.Bridges()) == want {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("bridge count never reached %d (got %d)", want, len(sub.Bridges()))
}
