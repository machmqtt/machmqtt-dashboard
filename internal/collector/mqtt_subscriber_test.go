package collector

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	nats "github.com/nats-io/nats.go"
	"github.com/noodlebit/machmqtt-dashboard/internal/config"
	"github.com/noodlebit/machmqtt-dashboard/internal/testutil/natstest"
)

func TestMQTTSubscriberReceivesBridgeMetrics(t *testing.T) {
	s := natstest.New(t)

	cfg := &config.NATSConnConfig{
		URLs:          []string{s.ClientURL()},
		SubjectPrefix: "$MQTT5",
	}

	sub := newMQTTSubscriber()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go sub.run(ctx, cfg)
	time.Sleep(100 * time.Millisecond)

	nc, err := nats.Connect(s.ClientURL())
	if err != nil {
		t.Fatal(err)
	}
	defer nc.Close()

	msg := BridgeMetricsMsg{
		V:            1,
		InstanceID:   "bridge-abc",
		InstanceName: "my-bridge",
		NATS:         BridgeMsgNATS{Connected: true},
		Metrics: &MQTTMetrics{
			ConnectionsActive:       3,
			MsgsRecvQoS1:            42,
			SubscriptionsActive:     7,
			ConsumerPendingMessages: -1,
			NATSEnforcementFallback: 9,
			NATSEnforcementDenied:   11,
			WillSuppressedShutdown:  13,
		},
		Pool: BridgePool{
			Size: 2,
			Slots: []BridgePoolSlot{
				{
					Index: 0, Connected: true, SubCount: 10, PubCount: 5,
					BufferedBytes: 2048, OutMsgs: 100, InMsgs: 90,
					OutBytes: 4096, InBytes: 3072, Reconnects: 2,
				},
				{Index: 1, Connected: true, SubCount: 8, PubCount: 3},
			},
		},
	}
	data, err := json.Marshal(msg)
	if err != nil {
		t.Fatal(err)
	}
	if err := nc.Publish("$MQTT5.metrics.my-bridge", data); err != nil {
		t.Fatal(err)
	}
	nc.Flush()
	time.Sleep(100 * time.Millisecond)

	bridges := sub.Bridges()
	if len(bridges) != 1 {
		t.Fatalf("got %d bridges, want 1", len(bridges))
	}
	b := bridges[0]
	if b.ConfiguredName != "my-bridge" {
		t.Errorf("ConfiguredName = %q, want my-bridge", b.ConfiguredName)
	}
	if !b.Reachable {
		t.Error("Reachable should be true")
	}
	if b.Status == nil {
		t.Fatal("Status is nil")
	}
	if !b.Status.NATSConnected {
		t.Error("Status.NATSConnected should be true")
	}
	if b.Status.Metrics == nil {
		t.Fatal("Metrics is nil")
	}
	if b.Status.Metrics.ConnectionsActive != 3 {
		t.Errorf("ConnectionsActive = %d, want 3", b.Status.Metrics.ConnectionsActive)
	}
	if b.Status.Metrics.SubscriptionsActive != 7 {
		t.Errorf("SubscriptionsActive = %d, want 7", b.Status.Metrics.SubscriptionsActive)
	}
	if b.Status.Metrics.MsgsRecvQoS1 != 42 {
		t.Errorf("MsgsRecvQoS1 = %d, want 42", b.Status.Metrics.MsgsRecvQoS1)
	}
	if b.Status.Metrics.ConsumerPendingMessages != -1 {
		t.Errorf("ConsumerPendingMessages = %d, want -1", b.Status.Metrics.ConsumerPendingMessages)
	}
	if b.Status.Metrics.NATSEnforcementFallback != 9 {
		t.Errorf("NATSEnforcementFallback = %d, want 9", b.Status.Metrics.NATSEnforcementFallback)
	}
	if b.Status.Metrics.NATSEnforcementDenied != 11 {
		t.Errorf("NATSEnforcementDenied = %d, want 11", b.Status.Metrics.NATSEnforcementDenied)
	}
	if b.Status.Metrics.WillSuppressedShutdown != 13 {
		t.Errorf("WillSuppressedShutdown = %d, want 13", b.Status.Metrics.WillSuppressedShutdown)
	}
	if b.Status.Connections != 3 {
		t.Errorf("Status.Connections = %d, want 3", b.Status.Connections)
	}
	if b.Status.Pool == nil {
		t.Fatal("Pool is nil")
	}
	if b.Status.Pool.Size != 2 {
		t.Errorf("Pool.Size = %d, want 2", b.Status.Pool.Size)
	}
	if len(b.Status.Pool.Slots) != 2 {
		t.Errorf("Pool.Slots len = %d, want 2", len(b.Status.Pool.Slots))
	}
	// Verify the per-slot backpressure/throughput fields survive the pub/sub
	// round-trip (published over NATS -> decoded by the subscriber -> mapped to
	// MQTTPoolSlot), not just the count of slots.
	slot0 := b.Status.Pool.Slots[0]
	if slot0.BufferedBytes != 2048 || slot0.OutMsgs != 100 || slot0.InMsgs != 90 ||
		slot0.OutBytes != 4096 || slot0.InBytes != 3072 || slot0.Reconnects != 2 {
		t.Errorf("Pool.Slots[0] backpressure fields not mapped from push message: %+v", slot0)
	}
}

func TestMQTTSubscriberMultipleBridges(t *testing.T) {
	s := natstest.New(t)

	cfg := &config.NATSConnConfig{
		URLs:          []string{s.ClientURL()},
		SubjectPrefix: "$MQTT5",
	}

	sub := newMQTTSubscriber()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go sub.run(ctx, cfg)
	time.Sleep(100 * time.Millisecond)

	nc, err := nats.Connect(s.ClientURL())
	if err != nil {
		t.Fatal(err)
	}
	defer nc.Close()

	for i, name := range []string{"bridge-1", "bridge-2", "bridge-3"} {
		msg := BridgeMetricsMsg{
			V:            1,
			InstanceID:   name,
			InstanceName: name,
			Metrics:      &MQTTMetrics{ConnectionsActive: int64(i + 1)},
		}
		data, _ := json.Marshal(msg)
		nc.Publish("$MQTT5.metrics."+name, data)
	}
	nc.Flush()
	time.Sleep(100 * time.Millisecond)

	bridges := sub.Bridges()
	if len(bridges) != 3 {
		t.Fatalf("got %d bridges, want 3", len(bridges))
	}
}

func TestMQTTSubscriberIgnoresInvalidMessages(t *testing.T) {
	s := natstest.New(t)

	cfg := &config.NATSConnConfig{
		URLs:          []string{s.ClientURL()},
		SubjectPrefix: "test",
	}

	sub := newMQTTSubscriber()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go sub.run(ctx, cfg)
	time.Sleep(100 * time.Millisecond)

	nc, err := nats.Connect(s.ClientURL())
	if err != nil {
		t.Fatal(err)
	}
	defer nc.Close()

	nc.Publish("test.metrics.bad", []byte("not json"))
	nc.Publish("test.metrics.noname", []byte(`{"v":1,"instance_id":"x"}`)) // missing instance_name
	nc.Flush()
	time.Sleep(100 * time.Millisecond)

	if len(sub.Bridges()) != 0 {
		t.Errorf("expected 0 bridges for invalid messages, got %d", len(sub.Bridges()))
	}
}

func TestMQTTSubscriberCustomPrefix(t *testing.T) {
	s := natstest.New(t)

	cfg := &config.NATSConnConfig{
		URLs:          []string{s.ClientURL()},
		SubjectPrefix: "acme",
	}

	sub := newMQTTSubscriber()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go sub.run(ctx, cfg)
	time.Sleep(100 * time.Millisecond)

	nc, err := nats.Connect(s.ClientURL())
	if err != nil {
		t.Fatal(err)
	}
	defer nc.Close()

	// Publish on default prefix — should NOT be received.
	wrongMsg := BridgeMetricsMsg{V: 1, InstanceID: "wrong", InstanceName: "wrong"}
	wrongData, _ := json.Marshal(wrongMsg)
	nc.Publish("$MQTT5.metrics.wrong", wrongData)

	// Publish on configured prefix — should be received.
	rightMsg := BridgeMetricsMsg{V: 1, InstanceID: "right", InstanceName: "right"}
	rightData, _ := json.Marshal(rightMsg)
	nc.Publish("acme.metrics.right", rightData)

	nc.Flush()
	time.Sleep(100 * time.Millisecond)

	bridges := sub.Bridges()
	if len(bridges) != 1 {
		t.Fatalf("got %d bridges, want 1 (only custom-prefix message)", len(bridges))
	}
	if bridges[0].Status == nil || bridges[0].Status.Metrics == nil {
		t.Fatal("bridge status/metrics nil")
	}
	// InstanceID in Metrics is the ephemeral instance_id from the payload.
	if bridges[0].Status.Metrics.InstanceID != "right" {
		t.Errorf("got instance_id %q, want right", bridges[0].Status.Metrics.InstanceID)
	}
}

func TestMQTTSubscriberAccountMapped(t *testing.T) {
	s := natstest.New(t)

	cfg := &config.NATSConnConfig{
		URLs:          []string{s.ClientURL()},
		SubjectPrefix: "$MQTT5",
	}

	sub := newMQTTSubscriber()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go sub.run(ctx, cfg)
	time.Sleep(100 * time.Millisecond)

	nc, err := nats.Connect(s.ClientURL())
	if err != nil {
		t.Fatal(err)
	}
	defer nc.Close()

	msg := BridgeMetricsMsg{
		V:            1,
		InstanceID:   "js-bridge",
		InstanceName: "js-bridge",
		Metrics:      &MQTTMetrics{ConnectionsActive: 1},
		Account: &BridgeMsgAccount{
			Domain:    "hub",
			Memory:    1024,
			Store:     4096,
			Streams:   3,
			Consumers: 5,
		},
	}
	data, _ := json.Marshal(msg)
	nc.Publish("$MQTT5.metrics.js-bridge", data)
	nc.Flush()
	time.Sleep(100 * time.Millisecond)

	bridges := sub.Bridges()
	if len(bridges) != 1 {
		t.Fatalf("got %d bridges, want 1", len(bridges))
	}
	b := bridges[0]
	if b.Status == nil || b.Status.NATS == nil {
		t.Fatal("Status.NATS is nil")
	}
	acct := b.Status.NATS.Account
	if acct == nil {
		t.Fatal("NATS.Account is nil — JetStream account not mapped")
	}
	if acct.Streams != 3 {
		t.Errorf("Account.Streams = %d, want 3", acct.Streams)
	}
	if acct.Memory != 1024 {
		t.Errorf("Account.Memory = %d, want 1024", acct.Memory)
	}
}

func TestMQTTSubscriberRunExitsOnConnectError(t *testing.T) {
	sub := newMQTTSubscriber()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Empty URLs → connectNATS returns error → run() exits cleanly.
	go sub.run(ctx, &config.NATSConnConfig{})
	time.Sleep(20 * time.Millisecond)
	// Should not panic; bridges should remain empty.
	if len(sub.Bridges()) != 0 {
		t.Errorf("expected 0 bridges after connect error, got %d", len(sub.Bridges()))
	}
}

func TestMQTTSubscriberSweepExpired(t *testing.T) {
	sub := newMQTTSubscriber()
	sub.bridges["live"] = &cachedBridge{receivedAt: time.Now()}
	sub.bridges["stale"] = &cachedBridge{receivedAt: time.Now().Add(-2 * defaultBridgeTTL)}
	sub.sweepExpired()
	if _, ok := sub.bridges["live"]; !ok {
		t.Error("live bridge was unexpectedly removed")
	}
	if _, ok := sub.bridges["stale"]; ok {
		t.Error("stale bridge was not removed")
	}
}

func TestBoolToInt64(t *testing.T) {
	if boolToInt64(true) != 1 {
		t.Error("boolToInt64(true) should be 1")
	}
	if boolToInt64(false) != 0 {
		t.Error("boolToInt64(false) should be 0")
	}
}
