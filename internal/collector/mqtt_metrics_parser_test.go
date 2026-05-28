package collector

import "testing"

func TestParsePrometheusMetrics_AuthFailureSumsLabeledReasons(t *testing.T) {
	// machmqtt emits auth failures only as per-reason labeled series with no
	// unlabeled aggregate; the parser must sum them.
	body := `# HELP machmqtt_auth_success_total Successful MQTT client authentications.
# TYPE machmqtt_auth_success_total counter
machmqtt_auth_success_total 42
# HELP machmqtt_auth_failure_total Failed MQTT client authentication attempts by reason.
# TYPE machmqtt_auth_failure_total counter
machmqtt_auth_failure_total{reason="bad_credentials"} 5
machmqtt_auth_failure_total{reason="enhanced"} 3
machmqtt_auth_failure_total{reason="locked"} 2
machmqtt_auth_failure_total{reason="other"} 1
`
	m := parsePrometheusMetrics(body)
	if m.AuthSuccess != 42 {
		t.Errorf("AuthSuccess = %d, want 42", m.AuthSuccess)
	}
	if m.AuthFailure != 11 {
		t.Errorf("AuthFailure = %d, want 11 (sum of all reason buckets)", m.AuthFailure)
	}
}

func TestParsePrometheusMetrics_QoSAndCounters(t *testing.T) {
	body := `machmqtt_connections_active 7
machmqtt_connections_total 100
machmqtt_connections_rejected_total 4
machmqtt_messages_received_total{qos="0"} 10
machmqtt_messages_received_total{qos="1"} 20
machmqtt_messages_received_total{qos="2"} 30
machmqtt_messages_sent_total{qos="0"} 11
machmqtt_messages_sent_total{qos="1"} 22
machmqtt_messages_sent_total{qos="2"} 33
machmqtt_nats_reconnects_total 2
`
	m := parsePrometheusMetrics(body)
	checks := map[string]struct{ got, want int64 }{
		"ConnectionsActive":   {m.ConnectionsActive, 7},
		"ConnectionsTotal":    {m.ConnectionsTotal, 100},
		"ConnectionsRejected": {m.ConnectionsRejected, 4},
		"MsgsRecvQoS0":        {m.MsgsRecvQoS0, 10},
		"MsgsRecvQoS1":        {m.MsgsRecvQoS1, 20},
		"MsgsRecvQoS2":        {m.MsgsRecvQoS2, 30},
		"MsgsSentQoS0":        {m.MsgsSentQoS0, 11},
		"MsgsSentQoS1":        {m.MsgsSentQoS1, 22},
		"MsgsSentQoS2":        {m.MsgsSentQoS2, 33},
		"NATSReconnects":      {m.NATSReconnects, 2},
	}
	for field, c := range checks {
		if c.got != c.want {
			t.Errorf("%s = %d, want %d", field, c.got, c.want)
		}
	}
}
