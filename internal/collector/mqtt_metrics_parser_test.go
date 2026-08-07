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
machmqtt_auth_failure_total{reason="webhook_denied"} 61
machmqtt_auth_failure_total{reason="webhook_unavailable"} 62
# TYPE machmqtt_auth_webhook_requests_total counter
machmqtt_auth_webhook_requests_total 63
machmqtt_auth_webhook_transport_failures_total 64
machmqtt_auth_webhook_inflight_rejected_total 65
# TYPE machmqtt_scram_sessions_active gauge
machmqtt_scram_sessions_active 2
# TYPE machmqtt_nats_enforcement_fallback_total counter
machmqtt_nats_enforcement_fallback_total 14
# TYPE machmqtt_nats_enforcement_denied_total counter
machmqtt_nats_enforcement_denied_total 15
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
machmqtt_will_suppressed_total{reason="shutdown"} 16
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
# TYPE machmqtt_auth_webhook_duration_seconds histogram
machmqtt_auth_webhook_duration_seconds_bucket{le="+Inf"} 25
machmqtt_auth_webhook_duration_seconds_sum 0.025
machmqtt_auth_webhook_duration_seconds_count 25
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
# --- socket-level counters (post socket-split) ---
machmqtt_sockets_open 9
machmqtt_sockets_accepted_total 200
machmqtt_ws_sockets_open 4
machmqtt_ws_sockets_accepted_total 60
# --- new rejection reasons ---
machmqtt_connections_rejected_by_reason_total{reason="connect_timeout"} 6
machmqtt_connections_rejected_by_reason_total{reason="auth_timeout"} 7
machmqtt_connections_rejected_by_reason_total{reason="worker_pool"} 8
# --- new auth-failure reasons ---
machmqtt_auth_failure_total{reason="license"} 4
machmqtt_auth_failure_total{reason="token_expired"} 5
machmqtt_auth_failure_total{reason="bad_signature"} 6
machmqtt_auth_failure_total{reason="claim_mismatch"} 7
machmqtt_auth_failure_total{reason="jwks_unavailable"} 8
# --- durability extras ---
machmqtt_qos2_client_send_failed_total 18
machmqtt_server_publish_failed_total{qos="0"} 1
machmqtt_server_publish_failed_total{qos="1"} 2
machmqtt_server_publish_failed_total{qos="2"} 3
machmqtt_qos0_messages_shed_total 19
machmqtt_oversized_dropped_total 20
machmqtt_publish_outage_disconnects_total 21
machmqtt_outbound_evictions_total 22
machmqtt_outbound_stall_evictions_total 7
machmqtt_outbound_stalled_connections 3
machmqtt_outbound_bytes 4096
machmqtt_retained_verify_failures_total 23
# --- capacity & memory gauges ---
machmqtt_retained_messages 500
machmqtt_inflight_out_messages 24
machmqtt_subscriptions_active 600
# --- bridge / pool health ---
machmqtt_pool_slot_connected 16
machmqtt_pool_slot_rebuilds_total 25
machmqtt_bridge_primary_rebuilds_total 26
machmqtt_bridge_rebuilds_degraded_total 27
machmqtt_bridge_consumer_reattach_total{result="reattached"} 28
machmqtt_bridge_consumer_reattach_total{result="force_disconnected"} 29
machmqtt_bridge_consumer_reattach_total{result="push_force_disconnected"} 30
# --- throttling & ACL ---
machmqtt_aggregate_publish_limit_msgs_per_sec 10000
machmqtt_publish_throttled_total{scope="per_client"} 31
machmqtt_publish_throttled_total{scope="aggregate"} 32
machmqtt_acl_denied_total{action="publish"} 33
machmqtt_acl_denied_total{action="subscribe"} 34
# --- cluster counters ---
machmqtt_cluster_inspect_timeouts_total 35
machmqtt_cluster_takeover_dropped_total 36
machmqtt_cluster_takeover_order_skew_total 37
# --- session-ownership lease (v1.1) ---
machmqtt_cluster_lease_acquired_total 66
machmqtt_cluster_lease_transferred_total 67
machmqtt_cluster_lease_reclaimed_total 68
machmqtt_cluster_lease_conflicts_total 69
machmqtt_cluster_lease_watcher_kicks_total 70
machmqtt_cluster_lease_release_failed_total 71
machmqtt_cluster_owned_leases 72
machmqtt_session_fencing_rejected_total 73
# --- queue backpressure ---
machmqtt_worker_pool_queue_depth 38
machmqtt_op_queue_depth 39
machmqtt_op_queue_bytes 8192
machmqtt_op_suspended_conns 40
machmqtt_op_pool_queue_depth 41
machmqtt_op_pool_rejected_total 42
# --- session / consumer persistence ---
machmqtt_consumer_seq_map_entries 43
machmqtt_consumer_deletes_dropped_total 44
machmqtt_consumer_delete_races_total 45
machmqtt_legacy_named_consumers 145
machmqtt_shared_consumer_recreated_total 146
machmqtt_consumer_deleted_under_consume_total 147
machmqtt_session_deletes_dropped_total 46
machmqtt_session_persist_failed_total{reason="write_failed"} 47
machmqtt_session_persist_failed_total{reason="queue_full"} 48
# --- reliability extras ---
machmqtt_tls_cert_reload_failures_total 49
machmqtt_oauth2_jwks_fetch_failures_total 50
machmqtt_audit_write_failures_total 51
# --- TLS handshake duration histogram ---
machmqtt_tls_handshake_duration_seconds_bucket{le="+Inf"} 60
machmqtt_tls_handshake_duration_seconds_sum 0.06
machmqtt_tls_handshake_duration_seconds_count 60
# --- sparse hex-coded families ---
machmqtt_connack_rejected_by_reason_total{reason="0x88"} 3
machmqtt_connack_rejected_by_reason_total{reason="0x81"} 1
machmqtt_disconnects_sent_by_reason_total{reason="0x8F"} 2
`

func TestParsePrometheusMetrics_Connections(t *testing.T) {
	m := parsePrometheusMetrics(sampleMetrics)
	checks := map[string]struct{ got, want int64 }{
		"ConnectionsActive":      {m.ConnectionsActive, 7},
		"ConnectionsTotal":       {m.ConnectionsTotal, 100},
		"ConnectionsRejected":    {m.ConnectionsRejected, 15},
		"WSConnectionsActive":    {m.WSConnectionsActive, 3},
		"WSConnectionsTotal":     {m.WSConnectionsTotal, 50},
		"RejectedMaxConns":       {m.RejectedMaxConns, 5},
		"RejectedLicense":        {m.RejectedLicense, 4},
		"RejectedPerIPConns":     {m.RejectedPerIPConns, 3},
		"RejectedPerIPAccept":    {m.RejectedPerIPAccept, 2},
		"RejectedPoolFull":       {m.RejectedPoolFull, 1},
		"RejectedConnectTimeout": {m.RejectedConnectTimeout, 6},
		"RejectedAuthTimeout":    {m.RejectedAuthTimeout, 7},
		"RejectedWorkerPool":     {m.RejectedWorkerPool, 8},
		"DispatchSlotsTLS":       {m.DispatchSlotsTLS, 12},
		"DispatchSlotsWS":        {m.DispatchSlotsWS, 8},
		"SocketsOpen":            {m.SocketsOpen, 9},
		"SocketsAccepted":        {m.SocketsAccepted, 200},
		"WSSocketsOpen":          {m.WSSocketsOpen, 4},
		"WSSocketsAccepted":      {m.WSSocketsAccepted, 60},
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
		"AuthSuccess":                  {m.AuthSuccess, 42},
		"AuthFailure (sum)":            {m.AuthFailure, 164}, // 5+3+2+1 + 4+5+6+7+8 + 61+62
		"AuthFailBadCreds":             {m.AuthFailBadCreds, 5},
		"AuthFailEnhanced":             {m.AuthFailEnhanced, 3},
		"AuthFailLocked":               {m.AuthFailLocked, 2},
		"AuthFailOther":                {m.AuthFailOther, 1},
		"AuthFailLicense":              {m.AuthFailLicense, 4},
		"AuthFailTokenExpired":         {m.AuthFailTokenExpired, 5},
		"AuthFailBadSignature":         {m.AuthFailBadSignature, 6},
		"AuthFailClaimMismatch":        {m.AuthFailClaimMismatch, 7},
		"AuthFailJWKSUnavailable":      {m.AuthFailJWKSUnavailable, 8},
		"AuthFailWebhookDenied":        {m.AuthFailWebhookDenied, 61},
		"AuthFailWebhookUnavailable":   {m.AuthFailWebhookUnavailable, 62},
		"ScramSessionsActive":          {m.ScramSessionsActive, 2},
		"NATSEnforcementFallback":      {m.NATSEnforcementFallback, 14},
		"NATSEnforcementDenied":        {m.NATSEnforcementDenied, 15},
		"AuthWebhookRequests":          {m.AuthWebhookRequests, 63},
		"AuthWebhookTransportFailures": {m.AuthWebhookTransportFailures, 64},
		"AuthWebhookInflightRejected":  {m.AuthWebhookInflightRejected, 65},
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
		"WillSuppressedShutdown":  {m.WillSuppressedShutdown, 16},
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
		"PanicsRecovered":          {m.PanicsRecovered, 0},
		"TLSHandshakeFailures":     {m.TLSHandshakeFailures, 6},
		"ProxyProtocolErrors":      {m.ProxyProtocolErrors, 7},
		"WSUpgradeFailures":        {m.WSUpgradeFailures, 8},
		"FlowcontrolOverflow":      {m.FlowcontrolOverflow, 9},
		"QoS2ServerPublishFailed":  {m.QoS2ServerPublishFailed, 10},
		"QoS1ClientSendFailed":     {m.QoS1ClientSendFailed, 11},
		"ServerPublishDropped":     {m.ServerPublishDropped, 12},
		"MessagesDeadLettered":     {m.MessagesDeadLettered, 13},
		"PoisonMessagesTerminated": {m.PoisonMessagesTerminated, 14},
		"DeadLetterWriteFailed":    {m.DeadLetterWriteFailed, 15},
		"OutboundQueueDropped":     {m.OutboundQueueDropped, 16},
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
		"SessionWriteBehindDepth": {m.SessionWriteBehindDepth, 17},
		"ConsumerPendingMessages": {m.ConsumerPendingMessages, 42},
		"StalledConsumers":        {m.StalledConsumers, 2},
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
		"PublishLatencyCount":      {m.PublishLatencyCount, 100},
		"AuthDurationCount":        {m.AuthDurationCount, 20},
		"AuthWebhookDurationCount": {m.AuthWebhookDurationCount, 25},
		"JSPublishDurationCount":   {m.JSPublishDurationCount, 30},
		"SubscribeDurationCount":   {m.SubscribeDurationCount, 40},
		"DispatchWaitCount":        {m.DispatchWaitCount, 50},
	}
	for field, c := range intChecks {
		if c.got != c.want {
			t.Errorf("%s = %d, want %d", field, c.got, c.want)
		}
	}
	floatChecks := map[string]struct {
		got, want float64
	}{
		"PublishLatencySumSeconds":      {m.PublishLatencySumSeconds, 0.05},
		"AuthDurationSumSeconds":        {m.AuthDurationSumSeconds, 0.02},
		"AuthWebhookDurationSumSeconds": {m.AuthWebhookDurationSumSeconds, 0.025},
		"JSPublishDurationSumSeconds":   {m.JSPublishDurationSumSeconds, 0.03},
		"SubscribeDurationSumSeconds":   {m.SubscribeDurationSumSeconds, 0.04},
		"DispatchWaitSumSeconds":        {m.DispatchWaitSumSeconds, 0.005},
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

// TestParsePrometheusMetrics_NewObservability covers the scalar metrics added to
// match machmqtt's recent observability work: durability extras, capacity gauges,
// bridge/pool health, throttling/ACL, cluster counters, queue backpressure,
// persistence, and reliability extras.
func TestParsePrometheusMetrics_NewObservability(t *testing.T) {
	m := parsePrometheusMetrics(sampleMetrics)
	checks := map[string]struct{ got, want int64 }{
		// Durability extras
		"QoS2ClientSendFailed":     {m.QoS2ClientSendFailed, 18},
		"ServerPublishFailedQoS0":  {m.ServerPublishFailedQoS0, 1},
		"ServerPublishFailedQoS1":  {m.ServerPublishFailedQoS1, 2},
		"ServerPublishFailedQoS2":  {m.ServerPublishFailedQoS2, 3},
		"QoS0MessagesShed":         {m.QoS0MessagesShed, 19},
		"OversizedDropped":         {m.OversizedDropped, 20},
		"PublishOutageDisconnects": {m.PublishOutageDisconnects, 21},
		"OutboundEvictions":        {m.OutboundEvictions, 22},
		"OutboundStallEvictions":   {m.OutboundStallEvictions, 7},
		"OutboundStalledConns":     {m.OutboundStalledConns, 3},
		"OutboundBytes":            {m.OutboundBytes, 4096},
		"RetainVerifyFailures":     {m.RetainVerifyFailures, 23},
		// Capacity & memory
		"RetainedMessages":    {m.RetainedMessages, 500},
		"InflightOutMessages": {m.InflightOutMessages, 24},
		"SubscriptionsActive": {m.SubscriptionsActive, 600},
		// Bridge / pool
		"PoolSlotConnected":                   {m.PoolSlotConnected, 16},
		"PoolSlotRebuilds":                    {m.PoolSlotRebuilds, 25},
		"BridgePrimaryRebuilds":               {m.BridgePrimaryRebuilds, 26},
		"BridgeRebuildsDegraded":              {m.BridgeRebuildsDegraded, 27},
		"BridgeConsumerReattached":            {m.BridgeConsumerReattached, 28},
		"BridgeConsumerForceDisconnected":     {m.BridgeConsumerForceDisconnected, 29},
		"BridgeConsumerPushForceDisconnected": {m.BridgeConsumerPushForceDisconnected, 30},
		// Throttling & ACL
		"AggregatePublishLimit":     {m.AggregatePublishLimit, 10000},
		"PublishThrottledPerClient": {m.PublishThrottledPerClient, 31},
		"PublishThrottledAggregate": {m.PublishThrottledAggregate, 32},
		"ACLDeniedPublish":          {m.ACLDeniedPublish, 33},
		"ACLDeniedSubscribe":        {m.ACLDeniedSubscribe, 34},
		// Cluster counters
		"ClusterInspectTimeouts":   {m.ClusterInspectTimeouts, 35},
		"ClusterTakeoverDropped":   {m.ClusterTakeoverDropped, 36},
		"ClusterTakeoverOrderSkew": {m.ClusterTakeoverOrderSkew, 37},
		// Session-ownership lease (v1.1)
		"ClusterLeaseAcquired":      {m.ClusterLeaseAcquired, 66},
		"ClusterLeaseTransferred":   {m.ClusterLeaseTransferred, 67},
		"ClusterLeaseReclaimed":     {m.ClusterLeaseReclaimed, 68},
		"ClusterLeaseConflicts":     {m.ClusterLeaseConflicts, 69},
		"ClusterLeaseWatcherKicks":  {m.ClusterLeaseWatcherKicks, 70},
		"ClusterLeaseReleaseFailed": {m.ClusterLeaseReleaseFailed, 71},
		"ClusterOwnedLeases":        {m.ClusterOwnedLeases, 72},
		"SessionFencingRejected":    {m.SessionFencingRejected, 73},
		// Queue backpressure
		"WorkerPoolQueueDepth": {m.WorkerPoolQueueDepth, 38},
		"OpQueueDepth":         {m.OpQueueDepth, 39},
		"OpQueueBytes":         {m.OpQueueBytes, 8192},
		"OpSuspendedConns":     {m.OpSuspendedConns, 40},
		"OpPoolQueueDepth":     {m.OpPoolQueueDepth, 41},
		"OpPoolRejected":       {m.OpPoolRejected, 42},
		// Persistence
		"ConsumerSeqMapEntries":           {m.ConsumerSeqMapEntries, 43},
		"ConsumerDeletesDropped":          {m.ConsumerDeletesDropped, 44},
		"ConsumerDeleteRaces":             {m.ConsumerDeleteRaces, 45},
		"LegacyNamedConsumers":            {m.LegacyNamedConsumers, 145},
		"SharedConsumerRecreated":         {m.SharedConsumerRecreated, 146},
		"ConsumerDeletedUnderConsume":     {m.ConsumerDeletedUnderConsume, 147},
		"SessionDeletesDropped":           {m.SessionDeletesDropped, 46},
		"SessionPersistFailedWriteFailed": {m.SessionPersistFailedWriteFailed, 47},
		"SessionPersistFailedQueueFull":   {m.SessionPersistFailedQueueFull, 48},
		// Reliability extras
		"TLSCertReloadFailures":   {m.TLSCertReloadFailures, 49},
		"OAuth2JWKSFetchFailures": {m.OAuth2JWKSFetchFailures, 50},
		"AuditWriteFailures":      {m.AuditWriteFailures, 51},
		// TLS handshake histogram (count; sum checked below)
		"TLSHandshakeDurationCount": {m.TLSHandshakeDurationCount, 60},
	}
	for field, c := range checks {
		if c.got != c.want {
			t.Errorf("%s = %d, want %d", field, c.got, c.want)
		}
	}
	if m.TLSHandshakeDurationSumSeconds != 0.06 {
		t.Errorf("TLSHandshakeDurationSumSeconds = %g, want 0.06", m.TLSHandshakeDurationSumSeconds)
	}
}

// TestParsePrometheusMetrics_HexFamilies covers the sparse, reason-code-keyed
// families. Only emitted codes should be present; absent codes stay out of the map.
func TestParsePrometheusMetrics_HexFamilies(t *testing.T) {
	m := parsePrometheusMetrics(sampleMetrics)
	if got := m.ConnackRejectedByReason["0x88"]; got != 3 {
		t.Errorf(`ConnackRejectedByReason["0x88"] = %d, want 3`, got)
	}
	if got := m.ConnackRejectedByReason["0x81"]; got != 1 {
		t.Errorf(`ConnackRejectedByReason["0x81"] = %d, want 1`, got)
	}
	if _, ok := m.ConnackRejectedByReason["0x99"]; ok {
		t.Errorf("ConnackRejectedByReason should not contain unemitted code 0x99")
	}
	if got := m.DisconnectsSentByReason["0x8F"]; got != 2 {
		t.Errorf(`DisconnectsSentByReason["0x8F"] = %d, want 2`, got)
	}
}

// TestParsePrometheusMetrics_HexFamiliesAbsent verifies the maps stay nil (and so
// marshal away via omitempty) when no codes are emitted.
func TestParsePrometheusMetrics_HexFamiliesAbsent(t *testing.T) {
	m := parsePrometheusMetrics(`machmqtt_connections_active 1
`)
	if m.ConnackRejectedByReason != nil {
		t.Errorf("ConnackRejectedByReason = %v, want nil when absent", m.ConnackRejectedByReason)
	}
	if m.DisconnectsSentByReason != nil {
		t.Errorf("DisconnectsSentByReason = %v, want nil when absent", m.DisconnectsSentByReason)
	}
}

func TestExtractLabelFound(t *testing.T) {
	line := `machmqtt_connections_rejected_by_reason_total{reason="max_conns"} 5`
	got := extractLabel(line, "reason")
	if got != "max_conns" {
		t.Errorf("extractLabel = %q, want max_conns", got)
	}
}

func TestExtractLabelNotFound(t *testing.T) {
	line := `machmqtt_connections_active 7`
	got := extractLabel(line, "reason")
	if got != "" {
		t.Errorf("extractLabel = %q, want empty", got)
	}
}

func TestExtractLabelMalformedNoClosingQuote(t *testing.T) {
	// Malformed label: opening quote but no closing quote.
	line := `foo{reason="unclosed`
	got := extractLabel(line, "reason")
	if got != "" {
		t.Errorf("extractLabel on malformed line = %q, want empty", got)
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
