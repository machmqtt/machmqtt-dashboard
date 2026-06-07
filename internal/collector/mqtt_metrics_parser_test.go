package collector

import "testing"

// sampleMetrics returns a representative /metrics body that closely mirrors
// what the broker emits today (connector/metrics/metrics.go WritePrometheusText
// + connector/admin.go for machmqtt_drained).
const sampleMetrics = `# HELP machmqtt_instance_info Static instance identity.
# TYPE machmqtt_instance_info gauge
machmqtt_instance_info{instance_id="broker-1"} 1
# TYPE machmqtt_connections_active gauge
machmqtt_connections_active 7
# TYPE machmqtt_connections_total counter
machmqtt_connections_total 100
# TYPE machmqtt_connections_rejected_total counter
machmqtt_connections_rejected_total 15
# TYPE machmqtt_connections_rejected_by_reason_total counter
machmqtt_connections_rejected_by_reason_total{reason="max_conns"} 5
machmqtt_connections_rejected_by_reason_total{reason="license"} 4
machmqtt_connections_rejected_by_reason_total{reason="per_ip_conns"} 3
machmqtt_connections_rejected_by_reason_total{reason="per_ip_accept"} 2
machmqtt_connections_rejected_by_reason_total{reason="pool_full"} 1
# TYPE machmqtt_license_feature_rejected_total counter
machmqtt_license_feature_rejected_total{feature="auth_method"} 11
machmqtt_license_feature_rejected_total{feature="retain"} 22
machmqtt_license_feature_rejected_total{feature="proxy_protocol"} 33
# TYPE machmqtt_dispatch_slots_active gauge
machmqtt_dispatch_slots_active{pool="tls"} 12
machmqtt_dispatch_slots_active{pool="websocket"} 8
# TYPE machmqtt_ws_connections_active gauge
machmqtt_ws_connections_active 3
# TYPE machmqtt_ws_connections_total counter
machmqtt_ws_connections_total 50
# TYPE machmqtt_auth_success_total counter
machmqtt_auth_success_total 42
# TYPE machmqtt_auth_failure_total counter
machmqtt_auth_failure_total{reason="bad_credentials"} 5
machmqtt_auth_failure_total{reason="enhanced"} 3
machmqtt_auth_failure_total{reason="locked"} 2
machmqtt_auth_failure_total{reason="other"} 1
# TYPE machmqtt_scram_sessions_active gauge
machmqtt_scram_sessions_active 2
# TYPE machmqtt_client_messages_received_total counter
machmqtt_client_messages_received_total{qos="0"} 10
machmqtt_client_messages_received_total{qos="1"} 20
machmqtt_client_messages_received_total{qos="2"} 30
# TYPE machmqtt_client_messages_sent_total counter
machmqtt_client_messages_sent_total{qos="0"} 11
machmqtt_client_messages_sent_total{qos="1"} 22
machmqtt_client_messages_sent_total{qos="2"} 33
# TYPE machmqtt_client_messages_redelivered_total counter
machmqtt_client_messages_redelivered_total 9
# TYPE machmqtt_server_messages_published_total counter
machmqtt_server_messages_published_total{qos="0"} 100
machmqtt_server_messages_published_total{qos="1"} 200
machmqtt_server_messages_published_total{qos="2"} 300
# TYPE machmqtt_server_messages_consumed_total counter
machmqtt_server_messages_consumed_total{qos="0"} 110
machmqtt_server_messages_consumed_total{qos="1"} 220
machmqtt_server_messages_consumed_total{qos="2"} 330
# TYPE machmqtt_will_published_total counter
machmqtt_will_published_total 7
# TYPE machmqtt_will_dropped_total counter
machmqtt_will_dropped_total{reason="queue_full"} 1
machmqtt_will_dropped_total{reason="publish_error"} 2
machmqtt_will_dropped_total{reason="invalid_topic"} 3
machmqtt_will_dropped_total{reason="shutdown"} 4
# TYPE machmqtt_will_suppressed_total counter
machmqtt_will_suppressed_total{reason="reconnected"} 6
# TYPE machmqtt_will_pending gauge
machmqtt_will_pending 5
# TYPE machmqtt_will_retry_pending gauge
machmqtt_will_retry_pending 8
# TYPE machmqtt_subscriptions_total counter
machmqtt_subscriptions_total 77
# TYPE machmqtt_unsubscriptions_total counter
machmqtt_unsubscriptions_total 13
# TYPE machmqtt_keepalive_timeouts_total counter
machmqtt_keepalive_timeouts_total 4
# TYPE machmqtt_pingreq_rate_limited_total counter
machmqtt_pingreq_rate_limited_total 99
# TYPE machmqtt_nats_disconnects_total counter
machmqtt_nats_disconnects_total 1
# TYPE machmqtt_nats_reconnects_total counter
machmqtt_nats_reconnects_total 2
# TYPE machmqtt_nats_slow_consumer_total counter
machmqtt_nats_slow_consumer_total 3
# TYPE machmqtt_panics_recovered_total counter
machmqtt_panics_recovered_total 0
# TYPE machmqtt_tls_handshake_failures_total counter
machmqtt_tls_handshake_failures_total 6
# TYPE machmqtt_proxy_protocol_errors_total counter
machmqtt_proxy_protocol_errors_total 7
# TYPE machmqtt_ws_upgrade_failures_total counter
machmqtt_ws_upgrade_failures_total 8
# TYPE machmqtt_flowcontrol_overflow_total counter
machmqtt_flowcontrol_overflow_total 9
# TYPE machmqtt_qos2_server_publish_failed_total counter
machmqtt_qos2_server_publish_failed_total 10
# TYPE machmqtt_qos1_client_send_failed_total counter
machmqtt_qos1_client_send_failed_total 11
# TYPE machmqtt_server_publish_dropped_total counter
machmqtt_server_publish_dropped_total 12
# TYPE machmqtt_messages_dead_lettered_total counter
machmqtt_messages_dead_lettered_total 13
# TYPE machmqtt_poison_messages_terminated_total counter
machmqtt_poison_messages_terminated_total 14
# TYPE machmqtt_dead_letter_write_failed_total counter
machmqtt_dead_letter_write_failed_total 15
# TYPE machmqtt_outbound_queue_dropped_total counter
machmqtt_outbound_queue_dropped_total 16
# TYPE machmqtt_session_write_behind_depth gauge
machmqtt_session_write_behind_depth 17
# TYPE machmqtt_consumer_pending_messages gauge
machmqtt_consumer_pending_messages 42
# TYPE machmqtt_stalled_consumers gauge
machmqtt_stalled_consumers 2
# TYPE machmqtt_publish_latency_seconds histogram
machmqtt_publish_latency_seconds_bucket{le="0.0005"} 90
machmqtt_publish_latency_seconds_bucket{le="+Inf"} 100
machmqtt_publish_latency_seconds_sum 0.05
machmqtt_publish_latency_seconds_count 100
# TYPE machmqtt_auth_duration_seconds histogram
machmqtt_auth_duration_seconds_bucket{le="0.001"} 10
machmqtt_auth_duration_seconds_bucket{le="+Inf"} 20
machmqtt_auth_duration_seconds_sum 0.02
machmqtt_auth_duration_seconds_count 20
# TYPE machmqtt_jetstream_publish_duration_seconds histogram
machmqtt_jetstream_publish_duration_seconds_bucket{le="+Inf"} 30
machmqtt_jetstream_publish_duration_seconds_sum 0.03
machmqtt_jetstream_publish_duration_seconds_count 30
# TYPE machmqtt_subscribe_duration_seconds histogram
machmqtt_subscribe_duration_seconds_bucket{le="+Inf"} 40
machmqtt_subscribe_duration_seconds_sum 0.04
machmqtt_subscribe_duration_seconds_count 40
# TYPE machmqtt_dispatch_wait_seconds histogram
machmqtt_dispatch_wait_seconds_bucket{le="+Inf"} 50
machmqtt_dispatch_wait_seconds_sum 0.005
machmqtt_dispatch_wait_seconds_count 50
# TYPE machmqtt_go_goroutines gauge
machmqtt_go_goroutines 88
# TYPE machmqtt_go_heap_inuse_bytes gauge
machmqtt_go_heap_inuse_bytes 1048576
# TYPE machmqtt_go_gc_cycles_total counter
machmqtt_go_gc_cycles_total 55
# TYPE machmqtt_go_gc_pause_ns_total counter
machmqtt_go_gc_pause_ns_total 999999
machmqtt_drained 1
`

func TestParsePrometheusMetrics_Connections(t *testing.T) {
	m := parsePrometheusMetrics(sampleMetrics)
	checks := map[string]struct{ got, want int64 }{
		"ConnectionsActive":   {m.ConnectionsActive, 7},
		"ConnectionsTotal":    {m.ConnectionsTotal, 100},
		"ConnectionsRejected": {m.ConnectionsRejected, 15},
		"WSConnectionsActive": {m.WSConnectionsActive, 3},
		"WSConnectionsTotal":  {m.WSConnectionsTotal, 50},
		"RejectedMaxConns":    {m.RejectedMaxConns, 5},
		"RejectedLicense":     {m.RejectedLicense, 4},
		"RejectedPerIPConns":  {m.RejectedPerIPConns, 3},
		"RejectedPerIPAccept": {m.RejectedPerIPAccept, 2},
		"RejectedPoolFull":    {m.RejectedPoolFull, 1},
		"DispatchSlotsTLS":    {m.DispatchSlotsTLS, 12},
		"DispatchSlotsWS":     {m.DispatchSlotsWS, 8},
	}
	for field, c := range checks {
		if c.got != c.want {
			t.Errorf("%s = %d, want %d", field, c.got, c.want)
		}
	}
}

func TestParsePrometheusMetrics_Auth(t *testing.T) {
	m := parsePrometheusMetrics(sampleMetrics)
	checks := map[string]struct{ got, want int64 }{
		"AuthSuccess":         {m.AuthSuccess, 42},
		"AuthFailure (sum)":   {m.AuthFailure, 11},
		"AuthFailBadCreds":    {m.AuthFailBadCreds, 5},
		"AuthFailEnhanced":    {m.AuthFailEnhanced, 3},
		"AuthFailLocked":      {m.AuthFailLocked, 2},
		"AuthFailOther":       {m.AuthFailOther, 1},
		"ScramSessionsActive": {m.ScramSessionsActive, 2},
	}
	for field, c := range checks {
		if c.got != c.want {
			t.Errorf("%s = %d, want %d", field, c.got, c.want)
		}
	}
}

func TestParsePrometheusMetrics_License(t *testing.T) {
	m := parsePrometheusMetrics(sampleMetrics)
	checks := map[string]struct{ got, want int64 }{
		"LicenseRejectedAuthMethod":    {m.LicenseRejectedAuthMethod, 11},
		"LicenseRejectedRetain":        {m.LicenseRejectedRetain, 22},
		"LicenseRejectedProxyProtocol": {m.LicenseRejectedProxyProtocol, 33},
	}
	for field, c := range checks {
		if c.got != c.want {
			t.Errorf("%s = %d, want %d", field, c.got, c.want)
		}
	}
}

func TestParsePrometheusMetrics_ClientMessages(t *testing.T) {
	m := parsePrometheusMetrics(sampleMetrics)
	checks := map[string]struct{ got, want int64 }{
		"MsgsRecvQoS0":    {m.MsgsRecvQoS0, 10},
		"MsgsRecvQoS1":    {m.MsgsRecvQoS1, 20},
		"MsgsRecvQoS2":    {m.MsgsRecvQoS2, 30},
		"MsgsSentQoS0":    {m.MsgsSentQoS0, 11},
		"MsgsSentQoS1":    {m.MsgsSentQoS1, 22},
		"MsgsSentQoS2":    {m.MsgsSentQoS2, 33},
		"MsgsRedelivered": {m.MsgsRedelivered, 9},
	}
	for field, c := range checks {
		if c.got != c.want {
			t.Errorf("%s = %d, want %d", field, c.got, c.want)
		}
	}
}

func TestParsePrometheusMetrics_ServerMessages(t *testing.T) {
	m := parsePrometheusMetrics(sampleMetrics)
	checks := map[string]struct{ got, want int64 }{
		"ServerPublishedQoS0": {m.ServerPublishedQoS0, 100},
		"ServerPublishedQoS1": {m.ServerPublishedQoS1, 200},
		"ServerPublishedQoS2": {m.ServerPublishedQoS2, 300},
		"ServerConsumedQoS0":  {m.ServerConsumedQoS0, 110},
		"ServerConsumedQoS1":  {m.ServerConsumedQoS1, 220},
		"ServerConsumedQoS2":  {m.ServerConsumedQoS2, 330},
	}
	for field, c := range checks {
		if c.got != c.want {
			t.Errorf("%s = %d, want %d", field, c.got, c.want)
		}
	}
}

func TestParsePrometheusMetrics_Will(t *testing.T) {
	m := parsePrometheusMetrics(sampleMetrics)
	checks := map[string]struct{ got, want int64 }{
		"WillPublished":           {m.WillPublished, 7},
		"WillDroppedQueueFull":    {m.WillDroppedQueueFull, 1},
		"WillDroppedPublishError": {m.WillDroppedPublishError, 2},
		"WillDroppedInvalidTopic": {m.WillDroppedInvalidTopic, 3},
		"WillDroppedShutdown":     {m.WillDroppedShutdown, 4},
		"WillSuppressedReconnect": {m.WillSuppressedReconnect, 6},
		"WillPending":             {m.WillPending, 5},
		"WillRetryPending":        {m.WillRetryPending, 8},
	}
	for field, c := range checks {
		if c.got != c.want {
			t.Errorf("%s = %d, want %d", field, c.got, c.want)
		}
	}
}

func TestParsePrometheusMetrics_ProtocolAndNATS(t *testing.T) {
	m := parsePrometheusMetrics(sampleMetrics)
	checks := map[string]struct{ got, want int64 }{
		"Subscribes":         {m.Subscribes, 77},
		"Unsubscribes":       {m.Unsubscribes, 13},
		"KeepaliveTimeouts":  {m.KeepaliveTimeouts, 4},
		"PingreqRateLimited": {m.PingreqRateLimited, 99},
		"NATSDisconnects":    {m.NATSDisconnects, 1},
		"NATSReconnects":     {m.NATSReconnects, 2},
		"NATSSlowConsumer":   {m.NATSSlowConsumer, 3},
	}
	for field, c := range checks {
		if c.got != c.want {
			t.Errorf("%s = %d, want %d", field, c.got, c.want)
		}
	}
}

func TestParsePrometheusMetrics_ReliabilityAndDurability(t *testing.T) {
	m := parsePrometheusMetrics(sampleMetrics)
	checks := map[string]struct{ got, want int64 }{
		"PanicsRecovered":         {m.PanicsRecovered, 0},
		"TLSHandshakeFailures":    {m.TLSHandshakeFailures, 6},
		"ProxyProtocolErrors":     {m.ProxyProtocolErrors, 7},
		"WSUpgradeFailures":       {m.WSUpgradeFailures, 8},
		"FlowcontrolOverflow":     {m.FlowcontrolOverflow, 9},
		"QoS2ServerPublishFailed": {m.QoS2ServerPublishFailed, 10},
		"QoS1ClientSendFailed":    {m.QoS1ClientSendFailed, 11},
		"ServerPublishDropped":    {m.ServerPublishDropped, 12},
		"MessagesDeadLettered":    {m.MessagesDeadLettered, 13},
		"PoisonMessagesTerminated": {m.PoisonMessagesTerminated, 14},
		"DeadLetterWriteFailed":   {m.DeadLetterWriteFailed, 15},
		"OutboundQueueDropped":    {m.OutboundQueueDropped, 16},
	}
	for field, c := range checks {
		if c.got != c.want {
			t.Errorf("%s = %d, want %d", field, c.got, c.want)
		}
	}
}

func TestParsePrometheusMetrics_JetStreamAndGauges(t *testing.T) {
	m := parsePrometheusMetrics(sampleMetrics)
	checks := map[string]struct{ got, want int64 }{
		"SessionWriteBehindDepth":  {m.SessionWriteBehindDepth, 17},
		"ConsumerPendingMessages":  {m.ConsumerPendingMessages, 42},
		"StalledConsumers":         {m.StalledConsumers, 2},
	}
	for field, c := range checks {
		if c.got != c.want {
			t.Errorf("%s = %d, want %d", field, c.got, c.want)
		}
	}
}

// TestParsePrometheusMetrics_ConsumerPendingAbsent verifies that the sentinel
// (-1) is preserved when machmqtt_consumer_pending_messages is absent from the
// body — which happens when JetStream is unavailable.
func TestParsePrometheusMetrics_ConsumerPendingAbsent(t *testing.T) {
	body := `machmqtt_connections_active 1
machmqtt_stalled_consumers 0
`
	m := parsePrometheusMetrics(body)
	if m.ConsumerPendingMessages != -1 {
		t.Errorf("ConsumerPendingMessages = %d, want -1 (sentinel for absent metric)", m.ConsumerPendingMessages)
	}
}

func TestParsePrometheusMetrics_Histograms(t *testing.T) {
	m := parsePrometheusMetrics(sampleMetrics)
	// Buckets must NOT bleed into count or sum.
	intChecks := map[string]struct{ got, want int64 }{
		"PublishLatencyCount":    {m.PublishLatencyCount, 100},
		"AuthDurationCount":      {m.AuthDurationCount, 20},
		"JSPublishDurationCount": {m.JSPublishDurationCount, 30},
		"SubscribeDurationCount": {m.SubscribeDurationCount, 40},
		"DispatchWaitCount":      {m.DispatchWaitCount, 50},
	}
	for field, c := range intChecks {
		if c.got != c.want {
			t.Errorf("%s = %d, want %d", field, c.got, c.want)
		}
	}
	floatChecks := map[string]struct {
		got, want float64
	}{
		"PublishLatencySumSeconds":    {m.PublishLatencySumSeconds, 0.05},
		"AuthDurationSumSeconds":      {m.AuthDurationSumSeconds, 0.02},
		"JSPublishDurationSumSeconds": {m.JSPublishDurationSumSeconds, 0.03},
		"SubscribeDurationSumSeconds": {m.SubscribeDurationSumSeconds, 0.04},
		"DispatchWaitSumSeconds":      {m.DispatchWaitSumSeconds, 0.005},
	}
	for field, c := range floatChecks {
		if c.got != c.want {
			t.Errorf("%s = %g, want %g", field, c.got, c.want)
		}
	}
}

func TestParsePrometheusMetrics_RuntimeAndInstance(t *testing.T) {
	m := parsePrometheusMetrics(sampleMetrics)
	checks := map[string]struct{ got, want int64 }{
		"GoGoroutines":     {m.GoGoroutines, 88},
		"GoHeapInuseBytes": {m.GoHeapInuseBytes, 1048576},
		"GoGCCycles":       {m.GoGCCycles, 55},
		"GoGCPauseNsTotal": {m.GoGCPauseNsTotal, 999999},
		"Drained":          {m.Drained, 1},
	}
	for field, c := range checks {
		if c.got != c.want {
			t.Errorf("%s = %d, want %d", field, c.got, c.want)
		}
	}
	if m.InstanceID != "broker-1" {
		t.Errorf("InstanceID = %q, want %q", m.InstanceID, "broker-1")
	}
}

// TestParsePrometheusMetrics_InstanceAbsent verifies that InstanceID is empty
// when machmqtt_instance_info is absent (broker has no InstanceID configured).
func TestParsePrometheusMetrics_InstanceAbsent(t *testing.T) {
	body := `machmqtt_connections_active 1
`
	m := parsePrometheusMetrics(body)
	if m.InstanceID != "" {
		t.Errorf("InstanceID = %q, want empty string", m.InstanceID)
	}
}
