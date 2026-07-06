package collector

import (
	"strconv"
	"strings"
)

func parsePrometheusMetrics(body string) *MQTTMetrics {
	// ConsumerPendingMessages is absent when JetStream is unavailable; sentinel -1
	// lets the UI distinguish "absent" from "zero pending".
	m := &MQTTMetrics{ConsumerPendingMessages: -1}

	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		name, value := parseMetricLine(line)
		switch {
		// --- Connections ---
		case name == "machmqtt_connections_active":
			m.ConnectionsActive = parseInt(value)
		case name == "machmqtt_connections_total":
			m.ConnectionsTotal = parseInt(value)
		case name == "machmqtt_connections_rejected_total":
			m.ConnectionsRejected = parseInt(value)
		case name == "machmqtt_ws_connections_active":
			m.WSConnectionsActive = parseInt(value)
		case name == "machmqtt_ws_connections_total":
			m.WSConnectionsTotal = parseInt(value)

		// Raw transport sockets (pre-CONNECT; includes non-MQTT probes).
		case name == "machmqtt_sockets_open":
			m.SocketsOpen = parseInt(value)
		case name == "machmqtt_sockets_accepted_total":
			m.SocketsAccepted = parseInt(value)
		case name == "machmqtt_ws_sockets_open":
			m.WSSocketsOpen = parseInt(value)
		case name == "machmqtt_ws_sockets_accepted_total":
			m.WSSocketsAccepted = parseInt(value)

		// Connection rejections by reason.
		case name == "machmqtt_connections_rejected_by_reason_total":
			switch extractLabel(line, "reason") {
			case "max_conns":
				m.RejectedMaxConns = parseInt(value)
			case "license":
				m.RejectedLicense = parseInt(value)
			case "per_ip_conns":
				m.RejectedPerIPConns = parseInt(value)
			case "per_ip_accept":
				m.RejectedPerIPAccept = parseInt(value)
			case "pool_full":
				m.RejectedPoolFull = parseInt(value)
			case "connect_timeout":
				m.RejectedConnectTimeout = parseInt(value)
			case "auth_timeout":
				m.RejectedAuthTimeout = parseInt(value)
			case "worker_pool":
				m.RejectedWorkerPool = parseInt(value)
			}

		// Post-CONNECT CONNACK rejections, keyed by reason code (hex, e.g. "0x88").
		case name == "machmqtt_connack_rejected_by_reason_total":
			if r := extractLabel(line, "reason"); r != "" {
				if m.ConnackRejectedByReason == nil {
					m.ConnackRejectedByReason = map[string]int64{}
				}
				m.ConnackRejectedByReason[r] = parseInt(value)
			}

		// Server-initiated DISCONNECTs, keyed by reason code (hex, e.g. "0x8F").
		case name == "machmqtt_disconnects_sent_by_reason_total":
			if r := extractLabel(line, "reason"); r != "" {
				if m.DisconnectsSentByReason == nil {
					m.DisconnectsSentByReason = map[string]int64{}
				}
				m.DisconnectsSentByReason[r] = parseInt(value)
			}

		// Dispatch-pool saturation gauges.
		case name == "machmqtt_dispatch_slots_active":
			switch extractLabel(line, "pool") {
			case "tls":
				m.DispatchSlotsTLS = parseInt(value)
			case "websocket":
				m.DispatchSlotsWS = parseInt(value)
			}

		// --- Authentication ---
		case name == "machmqtt_auth_success_total":
			m.AuthSuccess = parseInt(value)
		case name == "machmqtt_auth_failure_total":
			// machmqtt emits only per-reason labeled series; sum them into the total
			// and also keep each reason distinct.
			v := parseInt(value)
			m.AuthFailure += v
			switch extractLabel(line, "reason") {
			case "bad_credentials":
				m.AuthFailBadCreds = v
			case "enhanced":
				m.AuthFailEnhanced = v
			case "locked":
				m.AuthFailLocked = v
			case "other":
				m.AuthFailOther = v
			case "license":
				m.AuthFailLicense = v
			case "token_expired":
				m.AuthFailTokenExpired = v
			case "bad_signature":
				m.AuthFailBadSignature = v
			case "claim_mismatch":
				m.AuthFailClaimMismatch = v
			case "jwks_unavailable":
				m.AuthFailJWKSUnavailable = v
			}
		case name == "machmqtt_scram_sessions_active":
			m.ScramSessionsActive = parseInt(value)
		case name == "machmqtt_nats_enforcement_fallback_total":
			m.NATSEnforcementFallback = parseInt(value)
		case name == "machmqtt_nats_enforcement_denied_total":
			m.NATSEnforcementDenied = parseInt(value)

		// --- License feature-gate rejections ---
		case name == "machmqtt_license_feature_rejected_total":
			switch extractLabel(line, "feature") {
			case "auth_method":
				m.LicenseRejectedAuthMethod = parseInt(value)
			case "retain":
				m.LicenseRejectedRetain = parseInt(value)
			case "proxy_protocol":
				m.LicenseRejectedProxyProtocol = parseInt(value)
			}

		// --- Client messages (MQTT client ↔ broker) ---
		case name == "machmqtt_client_messages_received_total":
			switch extractLabel(line, "qos") {
			case "0":
				m.MsgsRecvQoS0 = parseInt(value)
			case "1":
				m.MsgsRecvQoS1 = parseInt(value)
			case "2":
				m.MsgsRecvQoS2 = parseInt(value)
			}
		case name == "machmqtt_client_messages_sent_total":
			switch extractLabel(line, "qos") {
			case "0":
				m.MsgsSentQoS0 = parseInt(value)
			case "1":
				m.MsgsSentQoS1 = parseInt(value)
			case "2":
				m.MsgsSentQoS2 = parseInt(value)
			}
		case name == "machmqtt_client_messages_redelivered_total":
			m.MsgsRedelivered = parseInt(value)

		// --- Server messages (broker ↔ NATS) ---
		case name == "machmqtt_server_messages_published_total":
			switch extractLabel(line, "qos") {
			case "0":
				m.ServerPublishedQoS0 = parseInt(value)
			case "1":
				m.ServerPublishedQoS1 = parseInt(value)
			case "2":
				m.ServerPublishedQoS2 = parseInt(value)
			}
		case name == "machmqtt_server_messages_consumed_total":
			switch extractLabel(line, "qos") {
			case "0":
				m.ServerConsumedQoS0 = parseInt(value)
			case "1":
				m.ServerConsumedQoS1 = parseInt(value)
			case "2":
				m.ServerConsumedQoS2 = parseInt(value)
			}

		// --- Will (Last-Will-and-Testament) ---
		case name == "machmqtt_will_published_total":
			m.WillPublished = parseInt(value)
		case name == "machmqtt_will_dropped_total":
			switch extractLabel(line, "reason") {
			case "queue_full":
				m.WillDroppedQueueFull = parseInt(value)
			case "publish_error":
				m.WillDroppedPublishError = parseInt(value)
			case "invalid_topic":
				m.WillDroppedInvalidTopic = parseInt(value)
			case "shutdown":
				m.WillDroppedShutdown = parseInt(value)
			}
		case name == "machmqtt_will_suppressed_total":
			switch extractLabel(line, "reason") {
			case "reconnected":
				m.WillSuppressedReconnect = parseInt(value)
			case "shutdown":
				m.WillSuppressedShutdown = parseInt(value)
			}
		case name == "machmqtt_will_pending":
			m.WillPending = parseInt(value)
		case name == "machmqtt_will_retry_pending":
			m.WillRetryPending = parseInt(value)

		// --- Protocol ops ---
		case name == "machmqtt_subscriptions_total":
			m.Subscribes = parseInt(value)
		case name == "machmqtt_unsubscriptions_total":
			m.Unsubscribes = parseInt(value)
		case name == "machmqtt_keepalive_timeouts_total":
			m.KeepaliveTimeouts = parseInt(value)
		case name == "machmqtt_pingreq_rate_limited_total":
			m.PingreqRateLimited = parseInt(value)

		// --- NATS connection ---
		case name == "machmqtt_nats_disconnects_total":
			m.NATSDisconnects = parseInt(value)
		case name == "machmqtt_nats_reconnects_total":
			m.NATSReconnects = parseInt(value)
		case name == "machmqtt_nats_slow_consumer_total":
			m.NATSSlowConsumer = parseInt(value)

		// --- Reliability ---
		case name == "machmqtt_panics_recovered_total":
			m.PanicsRecovered = parseInt(value)
		case name == "machmqtt_tls_handshake_failures_total":
			m.TLSHandshakeFailures = parseInt(value)
		case name == "machmqtt_proxy_protocol_errors_total":
			m.ProxyProtocolErrors = parseInt(value)
		case name == "machmqtt_ws_upgrade_failures_total":
			m.WSUpgradeFailures = parseInt(value)
		case name == "machmqtt_flowcontrol_overflow_total":
			m.FlowcontrolOverflow = parseInt(value)

		// --- Durability / DLQ ---
		case name == "machmqtt_qos2_server_publish_failed_total":
			m.QoS2ServerPublishFailed = parseInt(value)
		case name == "machmqtt_qos1_client_send_failed_total":
			m.QoS1ClientSendFailed = parseInt(value)
		case name == "machmqtt_qos2_client_send_failed_total":
			m.QoS2ClientSendFailed = parseInt(value)
		case name == "machmqtt_server_publish_failed_total":
			switch extractLabel(line, "qos") {
			case "0":
				m.ServerPublishFailedQoS0 = parseInt(value)
			case "1":
				m.ServerPublishFailedQoS1 = parseInt(value)
			case "2":
				m.ServerPublishFailedQoS2 = parseInt(value)
			}
		case name == "machmqtt_qos0_messages_shed_total":
			m.QoS0MessagesShed = parseInt(value)
		case name == "machmqtt_oversized_dropped_total":
			m.OversizedDropped = parseInt(value)
		case name == "machmqtt_publish_outage_disconnects_total":
			m.PublishOutageDisconnects = parseInt(value)
		case name == "machmqtt_server_publish_dropped_total":
			m.ServerPublishDropped = parseInt(value)
		case name == "machmqtt_messages_dead_lettered_total":
			m.MessagesDeadLettered = parseInt(value)
		case name == "machmqtt_poison_messages_terminated_total":
			m.PoisonMessagesTerminated = parseInt(value)
		case name == "machmqtt_dead_letter_write_failed_total":
			m.DeadLetterWriteFailed = parseInt(value)
		case name == "machmqtt_outbound_queue_dropped_total":
			m.OutboundQueueDropped = parseInt(value)
		case name == "machmqtt_outbound_evictions_total":
			m.OutboundEvictions = parseInt(value)
		case name == "machmqtt_outbound_stall_evictions_total":
			m.OutboundStallEvictions = parseInt(value)
		case name == "machmqtt_outbound_stalled_connections":
			m.OutboundStalledConns = parseInt(value)
		case name == "machmqtt_outbound_bytes":
			m.OutboundBytes = parseInt(value)
		case name == "machmqtt_retained_verify_failures_total":
			m.RetainVerifyFailures = parseInt(value)

		// --- Capacity & memory gauges ---
		case name == "machmqtt_retained_messages":
			m.RetainedMessages = parseInt(value)
		case name == "machmqtt_inflight_out_messages":
			m.InflightOutMessages = parseInt(value)
		case name == "machmqtt_subscriptions_active":
			m.SubscriptionsActive = parseInt(value)

		// --- Bridge / pool health ---
		case name == "machmqtt_pool_slot_connected":
			m.PoolSlotConnected = parseInt(value)
		case name == "machmqtt_pool_slot_rebuilds_total":
			m.PoolSlotRebuilds = parseInt(value)
		case name == "machmqtt_bridge_primary_rebuilds_total":
			m.BridgePrimaryRebuilds = parseInt(value)
		case name == "machmqtt_bridge_rebuilds_degraded_total":
			m.BridgeRebuildsDegraded = parseInt(value)
		case name == "machmqtt_bridge_consumer_reattach_total":
			switch extractLabel(line, "result") {
			case "reattached":
				m.BridgeConsumerReattached = parseInt(value)
			case "force_disconnected":
				m.BridgeConsumerForceDisconnected = parseInt(value)
			case "push_force_disconnected":
				m.BridgeConsumerPushForceDisconnected = parseInt(value)
			}

		// --- Throttling & ACL ---
		case name == "machmqtt_aggregate_publish_limit_msgs_per_sec":
			m.AggregatePublishLimit = parseInt(value)
		case name == "machmqtt_publish_throttled_total":
			switch extractLabel(line, "scope") {
			case "per_client":
				m.PublishThrottledPerClient = parseInt(value)
			case "aggregate":
				m.PublishThrottledAggregate = parseInt(value)
			}
		case name == "machmqtt_acl_denied_total":
			switch extractLabel(line, "action") {
			case "publish":
				m.ACLDeniedPublish = parseInt(value)
			case "subscribe":
				m.ACLDeniedSubscribe = parseInt(value)
			}

		// --- Cluster counters (present only when clustering is enabled) ---
		case name == "machmqtt_cluster_inspect_timeouts_total":
			m.ClusterInspectTimeouts = parseInt(value)
		case name == "machmqtt_cluster_takeover_dropped_total":
			m.ClusterTakeoverDropped = parseInt(value)
		case name == "machmqtt_cluster_takeover_order_skew_total":
			m.ClusterTakeoverOrderSkew = parseInt(value)

		// --- Queue backpressure ---
		case name == "machmqtt_worker_pool_queue_depth":
			m.WorkerPoolQueueDepth = parseInt(value)
		case name == "machmqtt_op_queue_depth":
			m.OpQueueDepth = parseInt(value)
		case name == "machmqtt_op_queue_bytes":
			m.OpQueueBytes = parseInt(value)
		case name == "machmqtt_op_suspended_conns":
			m.OpSuspendedConns = parseInt(value)
		case name == "machmqtt_op_pool_queue_depth":
			m.OpPoolQueueDepth = parseInt(value)
		case name == "machmqtt_op_pool_rejected_total":
			m.OpPoolRejected = parseInt(value)

		// --- Session / consumer persistence ---
		case name == "machmqtt_consumer_seq_map_entries":
			m.ConsumerSeqMapEntries = parseInt(value)
		case name == "machmqtt_consumer_deletes_dropped_total":
			m.ConsumerDeletesDropped = parseInt(value)
		case name == "machmqtt_consumer_delete_races_total":
			m.ConsumerDeleteRaces = parseInt(value)
		case name == "machmqtt_session_deletes_dropped_total":
			m.SessionDeletesDropped = parseInt(value)
		case name == "machmqtt_session_persist_failed_total":
			switch extractLabel(line, "reason") {
			case "write_failed":
				m.SessionPersistFailedWriteFailed = parseInt(value)
			case "queue_full":
				m.SessionPersistFailedQueueFull = parseInt(value)
			}

		// --- Reliability extras ---
		case name == "machmqtt_tls_cert_reload_failures_total":
			m.TLSCertReloadFailures = parseInt(value)
		case name == "machmqtt_oauth2_jwks_fetch_failures_total":
			m.OAuth2JWKSFetchFailures = parseInt(value)

		// --- JetStream / consumer gauges ---
		case name == "machmqtt_session_write_behind_depth":
			m.SessionWriteBehindDepth = parseInt(value)
		case name == "machmqtt_consumer_pending_messages":
			m.ConsumerPendingMessages = parseInt(value)
		case name == "machmqtt_stalled_consumers":
			m.StalledConsumers = parseInt(value)

		// --- Histograms (count + sum only; _bucket lines are ignored) ---
		// Exact-name match avoids accidentally matching _bucket{le=...} lines.
		case name == "machmqtt_publish_latency_seconds_count":
			m.PublishLatencyCount = parseInt(value)
		case name == "machmqtt_publish_latency_seconds_sum":
			m.PublishLatencySumSeconds = parseFloat(value)
		case name == "machmqtt_auth_duration_seconds_count":
			m.AuthDurationCount = parseInt(value)
		case name == "machmqtt_auth_duration_seconds_sum":
			m.AuthDurationSumSeconds = parseFloat(value)
		case name == "machmqtt_jetstream_publish_duration_seconds_count":
			m.JSPublishDurationCount = parseInt(value)
		case name == "machmqtt_jetstream_publish_duration_seconds_sum":
			m.JSPublishDurationSumSeconds = parseFloat(value)
		case name == "machmqtt_subscribe_duration_seconds_count":
			m.SubscribeDurationCount = parseInt(value)
		case name == "machmqtt_subscribe_duration_seconds_sum":
			m.SubscribeDurationSumSeconds = parseFloat(value)
		case name == "machmqtt_dispatch_wait_seconds_count":
			m.DispatchWaitCount = parseInt(value)
		case name == "machmqtt_dispatch_wait_seconds_sum":
			m.DispatchWaitSumSeconds = parseFloat(value)
		case name == "machmqtt_tls_handshake_duration_seconds_count":
			m.TLSHandshakeDurationCount = parseInt(value)
		case name == "machmqtt_tls_handshake_duration_seconds_sum":
			m.TLSHandshakeDurationSumSeconds = parseFloat(value)

		// --- Go runtime ---
		case name == "machmqtt_go_goroutines":
			m.GoGoroutines = parseInt(value)
		case name == "machmqtt_go_heap_inuse_bytes":
			m.GoHeapInuseBytes = parseInt(value)
		case name == "machmqtt_go_gc_cycles_total":
			m.GoGCCycles = parseInt(value)
		case name == "machmqtt_go_gc_pause_ns_total":
			m.GoGCPauseNsTotal = parseInt(value)

		// --- Instance identity ---
		case name == "machmqtt_instance_info":
			m.InstanceID = extractLabel(line, "instance_id")

		// --- Operator drain state (emitted from admin.go, not metrics.go) ---
		case name == "machmqtt_drained":
			m.Drained = parseInt(value)
		}
	}
	return m
}

// parseMetricLine splits a Prometheus text line into the metric name (the part
// before the first '{', or the first whitespace-delimited token) and the sample
// value (the last whitespace-delimited token after the closing '}', or the
// second token for unlabelled lines).
func parseMetricLine(line string) (name, value string) {
	idx := strings.IndexByte(line, '{')
	if idx >= 0 {
		name = line[:idx]
		end := strings.LastIndexByte(line, '}')
		if end >= 0 && end+1 < len(line) {
			value = strings.TrimSpace(line[end+1:])
		}
	} else {
		parts := strings.Fields(line)
		if len(parts) >= 2 {
			name = parts[0]
			value = parts[1]
		}
	}
	return
}

// extractLabel returns the value of a single label key from the '{...}' portion
// of a Prometheus text line, e.g. extractLabel(`foo{reason="bar"} 1`, "reason")
// returns "bar".  Returns "" when the key is absent.
func extractLabel(line, key string) string {
	start := strings.Index(line, key+`="`)
	if start < 0 {
		return ""
	}
	start += len(key) + 2 // skip key="
	end := strings.Index(line[start:], `"`)
	if end < 0 {
		return ""
	}
	return line[start : start+end]
}

func parseInt(s string) int64 {
	v, _ := strconv.ParseInt(s, 10, 64)
	return v
}

func parseFloat(s string) float64 {
	v, _ := strconv.ParseFloat(s, 64)
	return v
}
