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
	time.Sleep(100 * time.Millisecond) // allow subscriber to connect

	nc, err := nats.Connect(s.ClientURL())
	if err != nil {
		t.Fatal(err)
	}
	defer nc.Close()

	msg := BridgeMetricsMsg{
		V:                       1,
		InstanceID:              "bridge-abc",
		Name:                    "my-bridge",
		PublishedAt:             time.Now(),
		NATSConnected:           true,
		ConnectionsActive:       3,
		ConsumerPendingMessages: -1,
		Pool: BridgePool{
			Size: 2,
			Slots: []BridgePoolSlot{
				{Index: 0, Connected: true, SubCount: 10, PubCount: 5},
				{Index: 1, Connected: true, SubCount: 8, PubCount: 3},
			},
		},
	}
	data, err := json.Marshal(msg)
	if err != nil {
		t.Fatal(err)
	}
	if err := nc.Publish("$MQTT5.metrics.bridge-abc", data); err != nil {
		t.Fatal(err)
	}
	nc.Flush()
	time.Sleep(100 * time.Millisecond) // allow message delivery

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
	if b.Status.Metrics.ConsumerPendingMessages != -1 {
		t.Errorf("ConsumerPendingMessages = %d, want -1", b.Status.Metrics.ConsumerPendingMessages)
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

	for i, id := range []string{"bridge-1", "bridge-2", "bridge-3"} {
		msg := BridgeMetricsMsg{V: 1, InstanceID: id, ConnectionsActive: int64(i + 1)}
		data, _ := json.Marshal(msg)
		nc.Publish("$MQTT5.metrics."+id, data)
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
	nc.Publish("test.metrics.noinstance", []byte(`{"v":1,"name":"x"}`)) // missing instance_id
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
	wrongMsg := BridgeMetricsMsg{V: 1, InstanceID: "wrong"}
	wrongData, _ := json.Marshal(wrongMsg)
	nc.Publish("$MQTT5.metrics.wrong", wrongData)

	// Publish on configured prefix — should be received.
	rightMsg := BridgeMetricsMsg{V: 1, InstanceID: "right"}
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
	if bridges[0].Status.Metrics.InstanceID != "right" {
		t.Errorf("got instance_id %q, want right", bridges[0].Status.Metrics.InstanceID)
	}
}
