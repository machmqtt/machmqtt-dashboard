package collector

import (
	"context"
	"testing"

	nats "github.com/nats-io/nats.go"
	"github.com/noodlebit/machmqtt-dashboard/internal/config"
	"github.com/noodlebit/machmqtt-dashboard/internal/testutil/natstest"
)

// These tests pin the dashboard's version-compatibility guarantees against
// MachMQTT: a newer broker (extra metrics, extra JSON fields, new label
// reasons) must degrade gracefully to "known fields parsed, unknown ignored",
// and an older broker (legacy v=0 envelope, sparse payloads) must still be
// accepted. Breaking wire changes on the broker side are signaled by bumping
// the envelope's "v", which the subscriber already skips with a warning
// (TestMQTTSubscriberSkipsNewerSchema).

// TestParsePrometheusMetricsIgnoresUnknownMetrics feeds the parser a scrape
// from a hypothetical newer broker: unknown metric families, an unknown
// rejection reason, and an unknown auth-failure reason. Known series must
// parse; unknown ones must be skipped without error, and unknown auth-failure
// reasons must still count toward the AuthFailure total so the headline
// number stays truthful even before the dashboard learns the new reason.
func TestParsePrometheusMetricsIgnoresUnknownMetrics(t *testing.T) {
	body := `# TYPE machmqtt_connections_active gauge
machmqtt_connections_active 7
# TYPE machmqtt_future_gauge_from_next_release gauge
machmqtt_future_gauge_from_next_release 123
# TYPE machmqtt_future_histogram_seconds histogram
machmqtt_future_histogram_seconds_bucket{le="0.1"} 4
machmqtt_future_histogram_seconds_count 4
machmqtt_future_histogram_seconds_sum 0.2
# TYPE machmqtt_connections_rejected_by_reason_total counter
machmqtt_connections_rejected_by_reason_total{reason="max_conns"} 5
machmqtt_connections_rejected_by_reason_total{reason="reason_added_in_future"} 9
# TYPE machmqtt_auth_failure_total counter
machmqtt_auth_failure_total{reason="bad_credentials"} 5
machmqtt_auth_failure_total{reason="reason_added_in_future"} 2
not_a_machmqtt_metric 1
garbage line without a value
`
	m := parsePrometheusMetrics(body)
	if m.ConnectionsActive != 7 {
		t.Errorf("ConnectionsActive = %d, want 7", m.ConnectionsActive)
	}
	if m.RejectedMaxConns != 5 {
		t.Errorf("RejectedMaxConns = %d, want 5", m.RejectedMaxConns)
	}
	if m.AuthFailBadCreds != 5 {
		t.Errorf("AuthFailBadCreds = %d, want 5", m.AuthFailBadCreds)
	}
	if m.AuthFailure != 7 {
		t.Errorf("AuthFailure = %d, want 7 (unknown reasons still count toward the total)", m.AuthFailure)
	}
	// The JS-absent sentinel must survive a body that never mentions the gauge.
	if m.ConsumerPendingMessages != -1 {
		t.Errorf("ConsumerPendingMessages = %d, want -1 (sentinel)", m.ConsumerPendingMessages)
	}
}

// TestMQTTSubscriberForwardCompatUnknownFields publishes a v=1 envelope with
// extra fields a newer broker might add — top-level, inside metrics, and
// inside nats — and asserts the message is cached with all known fields
// intact. encoding/json drops unknown fields, so additive broker changes
// must never require a schema bump.
func TestMQTTSubscriberForwardCompatUnknownFields(t *testing.T) {
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

	raw := `{
		"v": 1,
		"instance_name": "newer-bridge",
		"instance_id": "id-1",
		"field_added_in_future": {"nested": true},
		"nats": {"connected": true, "server_name": "n1", "future_nats_field": 3},
		"pool": {"size": 1, "slots": [{"index": 0, "connected": true, "future_slot_field": 9}]},
		"metrics": {
			"connections_active": 4,
			"consumer_pending_messages": 2,
			"counter_added_in_future": 999
		}
	}`
	nc.Publish("$MQTT5.metrics.newer-bridge", []byte(raw))
	nc.Flush()
	waitForBridges(t, sub, 1)

	inst := sub.Bridges()[0]
	if inst.ConfiguredName != "newer-bridge" {
		t.Errorf("ConfiguredName = %q, want newer-bridge", inst.ConfiguredName)
	}
	if inst.Status.Metrics.ConnectionsActive != 4 {
		t.Errorf("ConnectionsActive = %d, want 4", inst.Status.Metrics.ConnectionsActive)
	}
	if inst.Status.Metrics.ConsumerPendingMessages != 2 {
		t.Errorf("ConsumerPendingMessages = %d, want 2", inst.Status.Metrics.ConsumerPendingMessages)
	}
	if !inst.Status.NATSConnected {
		t.Error("NATSConnected should be true")
	}
	if inst.Status.Pool == nil || len(inst.Status.Pool.Slots) != 1 || !inst.Status.Pool.Slots[0].Connected {
		t.Error("pool slot from the extended payload was not preserved")
	}
}

// TestMQTTSubscriberAcceptsLegacyV0 covers backward compatibility with
// publishers that predate the "v" envelope field: the zero value must be
// treated as the legacy schema and accepted, not skipped.
func TestMQTTSubscriberAcceptsLegacyV0(t *testing.T) {
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

	// No "v" field at all, and a sparse payload with no metrics object — the
	// oldest shape a broker ever published.
	raw := `{"instance_name": "legacy-bridge", "nats": {"connected": true}}`
	nc.Publish("$MQTT5.metrics.legacy-bridge", []byte(raw))
	nc.Flush()
	waitForBridges(t, sub, 1)

	inst := sub.Bridges()[0]
	if inst.ConfiguredName != "legacy-bridge" {
		t.Errorf("ConfiguredName = %q, want legacy-bridge", inst.ConfiguredName)
	}
	// A payload without a metrics object must fall back to the JS-absent
	// sentinel rather than reporting pending=0.
	if inst.Status.Metrics.ConsumerPendingMessages != -1 {
		t.Errorf("ConsumerPendingMessages = %d, want -1 (sentinel)", inst.Status.Metrics.ConsumerPendingMessages)
	}
}
