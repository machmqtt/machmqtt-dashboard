package collector

import (
	"context"
	"encoding/json"
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

func TestMQTTSubscriberDrainedRemovesBridge(t *testing.T) {
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

	publish := func(m BridgeMetricsMsg) {
		data, _ := json.Marshal(m)
		nc.Publish("$MQTT5.metrics.b", data)
		nc.Flush()
	}

	// First a live metrics message → bridge present.
	publish(BridgeMetricsMsg{V: 1, InstanceName: "b", Metrics: &MQTTMetrics{ConnectionsActive: 1}})
	waitForBridges(t, sub, 1)

	// Then a drained message → bridge removed.
	publish(BridgeMetricsMsg{V: 1, InstanceName: "b", Drained: true})
	waitForBridges(t, sub, 0)
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
