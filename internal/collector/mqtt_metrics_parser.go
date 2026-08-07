package collector

import (
	"cmp"
	"encoding/json"
	"math"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"sync"
)

// knownMetricTags is the set of top-level json tags MQTTMetrics decodes into a
// typed field, built once by reflection so it cannot drift from the struct.
// UnmarshalJSON uses it to spot payload keys this build has no field for.
var knownMetricTags = sync.OnceValue(func() map[string]bool {
	tags := make(map[string]bool)
	t := reflect.TypeFor[MQTTMetrics]()
	for i := 0; i < t.NumField(); i++ {
		tag, _, _ := strings.Cut(t.Field(i).Tag.Get("json"), ",")
		if tag != "" && tag != "-" {
			tags[tag] = true
		}
	}
	return tags
})

// UnmarshalJSON decodes the typed fields as usual, then captures unknown
// numeric top-level keys into Uncurated so a metric added by a newer broker is
// visible under its wire name instead of silently dropped. Non-numeric unknowns
// (a future nested object, say) are skipped: they have no single-value
// rendering, and the next contract sync gives them a typed home.
func (m *MQTTMetrics) UnmarshalJSON(b []byte) error {
	type plainMetrics MQTTMetrics
	if err := json.Unmarshal(b, (*plainMetrics)(m)); err != nil {
		return err
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(b, &raw); err != nil {
		return nil
	}
	known := knownMetricTags()
	for k, v := range raw {
		if known[k] {
			continue
		}
		var f float64
		if json.Unmarshal(v, &f) == nil {
			if m.Uncurated == nil {
				m.Uncurated = make(map[string]float64)
			}
			m.Uncurated[k] = f
		}
	}
	return nil
}

func parsePrometheusMetrics(body string) *MQTTMetrics {
	// ConsumerPendingMessages is absent when JetStream is unavailable; sentinel -1
	// lets the UI distinguish "absent" from "zero pending".
	m := &MQTTMetrics{ConsumerPendingMessages: -1}

	// The license, reactor and pool families are absent as a whole when their
	// subsystem is not configured, so their sub-objects stay nil unless at least
	// one of the group's families appears — the same gating the broker applies
	// when it renders (and when it fills the pushed payload).
	var (
		lic      MQTTMetricsLicense
		reactor  MQTTMetricsReactor
		pool     MQTTMetricsPool
		poolSeen bool
		slotIdx  = map[int64]int{}
	)

	// poolSlot returns the accumulator for one slot= label, allocating on first
	// sight; the three per-slot families each carry one field of the same slot.
	poolSlot := func(line string) *MQTTMetricsPoolSlot {
		slot := parseInt(extractLabel(line, "slot"))
		if i, ok := slotIdx[slot]; ok {
			return &pool.Slots[i]
		}
		slotIdx[slot] = len(pool.Slots)
		pool.Slots = append(pool.Slots, MQTTMetricsPoolSlot{Slot: slot})
		return &pool.Slots[len(pool.Slots)-1]
	}

	// help collects # HELP lines by family so a captured unknown family carries
	// the broker's own description (HELP always precedes the family's samples).
	help := map[string]string{}

	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimSpace(line)
		if h, ok := strings.CutPrefix(line, "# HELP "); ok {
			if fam, text, ok := strings.Cut(h, " "); ok {
				help[fam] = text
			}
			continue
		}
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
		case name == "machmqtt_connections_max":
			m.ConnectionsMax = parseInt(value)
		// The umbrella counter is read as the broker renders it — the sum of the
		// eight other rejection reasons. It deliberately excludes mem_budget, so
		// RejectedMemBudget below is read on its own and must not be folded in.
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
			case "mem_budget":
				m.RejectedMemBudget = parseInt(value)
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

		// SUBSCRIBE filters refused via a failure SUBACK, keyed by reason code
		// (hex, e.g. "0x87"), counted per rejected filter rather than per packet.
		case name == "machmqtt_suback_rejected_by_reason_total":
			if r := extractLabel(line, "reason"); r != "" {
				if m.SubackRejectedByReason == nil {
					m.SubackRejectedByReason = map[string]int64{}
				}
				m.SubackRejectedByReason[r] = parseInt(value)
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
			case "webhook_denied":
				m.AuthFailWebhookDenied = v
			case "webhook_unavailable":
				m.AuthFailWebhookUnavailable = v
			}
		case name == "machmqtt_scram_sessions_active":
			m.ScramSessionsActive = parseInt(value)
		case name == "machmqtt_auth_failure_tracker_entries":
			m.AuthFailureTrackerEntries = parseInt(value)

		// --- Auth webhook (the whole family is absent unless auth.type is http,
		// which is what AuthWebhookActive records) ---
		case name == "machmqtt_auth_webhook_requests_total":
			m.AuthWebhookRequests = parseInt(value)
			m.AuthWebhookActive = true
		case name == "machmqtt_auth_webhook_transport_failures_total":
			m.AuthWebhookTransportFailures = parseInt(value)
			m.AuthWebhookActive = true
		case name == "machmqtt_auth_webhook_inflight_rejected_total":
			m.AuthWebhookInflightRejected = parseInt(value)
			m.AuthWebhookActive = true

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
		case name == "machmqtt_will_persist_failed_total":
			switch extractLabel(line, "reason") {
			case "write_failed":
				m.WillPersistFailedWrite = parseInt(value)
			case "queue_full":
				m.WillPersistFailedQueueFull = parseInt(value)
			}
		case name == "machmqtt_retain_persist_failed_total":
			switch extractLabel(line, "op") {
			case "put":
				m.RetainPersistFailedPut = parseInt(value)
			case "delete":
				m.RetainPersistFailedDelete = parseInt(value)
			}

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
		case name == "machmqtt_hook_panics_total":
			m.HookPanics = parseInt(value)
		case name == "machmqtt_hook_vetoes_total":
			m.HookVetoes = parseInt(value)
		case name == "machmqtt_sys_tree_published_total":
			m.SysTreePublished = parseInt(value)
		case name == "machmqtt_sys_publish_blocked_total":
			m.SysPublishBlocked = parseInt(value)
		case name == "machmqtt_publish_refused_topic_total":
			m.PublishRefusedTopic = parseInt(value)
		case name == "machmqtt_tls_handshake_failures_total":
			m.TLSHandshakeFailures = parseInt(value)
		case name == "machmqtt_proxy_protocol_errors_total":
			m.ProxyProtocolErrors = parseInt(value)
		case name == "machmqtt_ws_upgrade_failures_total":
			m.WSUpgradeFailures = parseInt(value)
		case name == "machmqtt_ws_protocol_violations_total":
			m.WSProtocolViolations = parseInt(value)
		case name == "machmqtt_flowcontrol_overflow_total":
			m.FlowcontrolOverflow = parseInt(value)
		case name == "machmqtt_credential_expiry_disconnects_total":
			m.CredentialExpiryDisconnects = parseInt(value)
		case name == "machmqtt_mtls_identity_fallback_total":
			switch extractLabel(line, "reason") {
			case "license":
				m.MTLSIdentityFallbackLicense = parseInt(value)
			case "no_match":
				m.MTLSIdentityFallbackNoMatch = parseInt(value)
			case "no_cert":
				m.MTLSIdentityFallbackNoCert = parseInt(value)
			}
		case name == "machmqtt_otel_histogram_skew_clamped_total":
			m.OTelHistogramSkewClamped = parseInt(value)

		// --- Durability / DLQ ---
		// QoS2ServerPublishFailed is the PUBREL-stage failure only; store-time
		// QoS 2 failures land in server_publish_failed_total{qos="2"} and
		// server_publish_dropped_total, so the counters never double-count.
		case name == "machmqtt_qos2_server_publish_failed_total":
			m.QoS2ServerPublishFailed = parseInt(value)
		case name == "machmqtt_qos1_client_send_failed_total":
			m.QoS1ClientSendFailed = parseInt(value)
		case name == "machmqtt_qos2_client_send_failed_total":
			m.QoS2ClientSendFailed = parseInt(value)
		case name == "machmqtt_qos2_sync_persist_failed_total":
			m.QoS2SyncPersistFailed = parseInt(value)
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
		case name == "machmqtt_bytes_received_total":
			m.InboundBytes = parseInt(value)
		case name == "machmqtt_retained_verify_failures_total":
			m.RetainVerifyFailures = parseInt(value)
			m.BridgeUp = true
		case name == "machmqtt_will_verify_failures_total":
			m.WillVerifyFailures = parseInt(value)
			m.BridgeUp = true
		case name == "machmqtt_subscribe_flush_failures_total":
			m.SubscribeFlushFailures = parseInt(value)
			m.BridgeUp = true

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

		// --- NATS connection pool (machmqtt_pool_*) ---
		// The per-slot trio is emitted only while the pool has sampled slots, so
		// the aggregate gauges alone are enough to establish the sub-object.
		case name == "machmqtt_pool_size":
			pool.Size = parseInt(value)
			poolSeen = true
		case name == "machmqtt_pool_buffered_bytes":
			pool.BufferedBytes = parseInt(value)
			poolSeen = true
		case name == "machmqtt_pool_buffered_bytes_max":
			pool.BufferedBytesMax = parseInt(value)
			poolSeen = true
		case name == "machmqtt_pool_slot_buffered_bytes":
			poolSlot(line).BufferedBytes = parseInt(value)
			poolSeen = true
		case name == "machmqtt_pool_slot_out_msgs_total":
			poolSlot(line).OutMsgs = parseInt(value)
			poolSeen = true
		case name == "machmqtt_pool_slot_in_msgs_total":
			poolSlot(line).InMsgs = parseInt(value)
			poolSeen = true

		// --- I/O reactor diagnostics (machmqtt_reactor_*) ---
		case name == "machmqtt_reactor_task_queue_depth":
			reactor.TaskQueueDepth = parseInt(value)
			m.Reactor = &reactor
		case name == "machmqtt_reactor_task_queue_depth_max":
			reactor.TaskQueueDepthMax = parseInt(value)
			m.Reactor = &reactor
		case name == "machmqtt_reactor_loop_panics_total":
			reactor.LoopPanics = parseInt(value)
			m.Reactor = &reactor
		case name == "machmqtt_reactor_read_continuations_total":
			reactor.ReadContinuations = parseInt(value)
			m.Reactor = &reactor
		case name == "machmqtt_reactor_write_backpressure_total":
			reactor.WriteBackpressure = parseInt(value)
			m.Reactor = &reactor
		case name == "machmqtt_reactor_feed_write_overflows_total":
			reactor.FeedWriteOverflows = parseInt(value)
			m.Reactor = &reactor
		case name == "machmqtt_reactor_feed_read_overflows_total":
			reactor.FeedReadOverflows = parseInt(value)
			m.Reactor = &reactor
		case name == "machmqtt_reactor_loop_deaths_total":
			reactor.LoopDeaths = parseInt(value)
			m.Reactor = &reactor

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

		// --- Cluster counters (the whole group is absent unless clustering is
		// enabled, which is what ClusterEnabled records) ---
		case name == "machmqtt_cluster_inspect_timeouts_total":
			m.ClusterInspectTimeouts = parseInt(value)
			m.ClusterEnabled = true
		case name == "machmqtt_cluster_takeover_dropped_total":
			m.ClusterTakeoverDropped = parseInt(value)
			m.ClusterEnabled = true
		case name == "machmqtt_cluster_takeover_order_skew_total":
			m.ClusterTakeoverOrderSkew = parseInt(value)
			m.ClusterEnabled = true
		case name == "machmqtt_cluster_heartbeat_publish_failures_total":
			m.ClusterHeartbeatPublishFailures = parseInt(value)
			m.ClusterEnabled = true

		// Per-source cluster HMAC verification failures. Source IDs can embed
		// client-derived strings, so the label value arrives escaped.
		case name == "machmqtt_cluster_hmac_failures_total":
			if src := extractLabel(line, "source_instance_id"); src != "" {
				m.ClusterHMACFailures = append(m.ClusterHMACFailures,
					MQTTHMACFailure{SourceInstanceID: src, Count: parseInt(value)})
				m.ClusterEnabled = true
			}

		// --- Session-ownership lease (epoch-fenced cluster takeover) ---
		case name == "machmqtt_cluster_lease_acquired_total":
			m.ClusterLeaseAcquired = parseInt(value)
			m.ClusterEnabled = true
		case name == "machmqtt_cluster_lease_transferred_total":
			m.ClusterLeaseTransferred = parseInt(value)
			m.ClusterEnabled = true
		case name == "machmqtt_cluster_lease_reclaimed_total":
			m.ClusterLeaseReclaimed = parseInt(value)
			m.ClusterEnabled = true
		case name == "machmqtt_cluster_lease_conflicts_total":
			m.ClusterLeaseConflicts = parseInt(value)
			m.ClusterEnabled = true
		case name == "machmqtt_cluster_lease_watcher_kicks_total":
			m.ClusterLeaseWatcherKicks = parseInt(value)
			m.ClusterEnabled = true
		case name == "machmqtt_cluster_lease_revision_regressions_total":
			m.ClusterLeaseRevisionRegressions = parseInt(value)
			m.ClusterEnabled = true
		case name == "machmqtt_cluster_lease_release_failed_total":
			m.ClusterLeaseReleaseFailed = parseInt(value)
			m.ClusterEnabled = true
		case name == "machmqtt_cluster_owned_leases":
			m.ClusterOwnedLeases = parseInt(value)
			m.ClusterEnabled = true
		case name == "machmqtt_session_fencing_rejected_total":
			m.SessionFencingRejected = parseInt(value)
			m.SessionsUp = true

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

		// --- Session / consumer persistence (the four consumer/session families
		// below are absent unless the bridge is up, which is what BridgeUp
		// records; the persist-failure family gates on the session store) ---
		case name == "machmqtt_consumer_seq_map_entries":
			m.ConsumerSeqMapEntries = parseInt(value)
			m.BridgeUp = true
		case name == "machmqtt_consumer_deletes_dropped_total":
			m.ConsumerDeletesDropped = parseInt(value)
			m.BridgeUp = true
		case name == "machmqtt_consumer_delete_races_total":
			m.ConsumerDeleteRaces = parseInt(value)
			m.BridgeUp = true
		case name == "machmqtt_session_deletes_dropped_total":
			m.SessionDeletesDropped = parseInt(value)
			m.BridgeUp = true
		case name == "machmqtt_session_persist_failed_total":
			switch extractLabel(line, "reason") {
			case "write_failed":
				m.SessionPersistFailedWriteFailed = parseInt(value)
			case "queue_full":
				m.SessionPersistFailedQueueFull = parseInt(value)
			case "panic":
				m.SessionPersistPanics = parseInt(value)
			}
			m.SessionsUp = true

		// --- Reliability extras ---
		case name == "machmqtt_tls_cert_reload_failures_total":
			m.TLSCertReloadFailures = parseInt(value)
		case name == "machmqtt_oauth2_jwks_fetch_failures_total":
			m.OAuth2JWKSFetchFailures = parseInt(value)
		case name == "machmqtt_oauth2_token_cache_evictions_total":
			m.OAuth2TokenCacheEvictions = parseInt(value)
		case name == "machmqtt_audit_write_failures_total":
			m.AuditWriteFailures = parseInt(value)

		// --- License manager (machmqtt_license_*; absent as a group when no
		// license manager is configured). The feature-gate rejection counters
		// above are core metrics and are NOT part of this group. HasExpiry is
		// inferred from the expires_timestamp family, which the broker emits
		// only for a license that has an expiry. ---
		case name == "machmqtt_license_valid":
			lic.Valid = parseInt(value)
			m.License = &lic
		case name == "machmqtt_license_status":
			lic.Status = parseInt(value)
			m.License = &lic
		case name == "machmqtt_license_expires_timestamp":
			lic.ExpiresTimestamp = parseInt(value)
			lic.HasExpiry = true
			m.License = &lic
		case name == "machmqtt_license_connections_limit":
			lic.ConnectionsLimit = parseInt(value)
			m.License = &lic
		case name == "machmqtt_license_connections_global":
			lic.ConnectionsGlobal = parseInt(value)
			m.License = &lic
		case name == "machmqtt_license_max_qos":
			lic.MaxQoS = parseInt(value)
			m.License = &lic
		case name == "machmqtt_license_instances":
			lic.Instances = parseInt(value)
			m.License = &lic
		case name == "machmqtt_license_degraded":
			lic.Degraded = parseInt(value)
			m.License = &lic
		case name == "machmqtt_license_block_confirmed":
			lic.BlockConfirmed = parseInt(value)
			m.License = &lic
		case name == "machmqtt_license_capacity_clamped":
			lic.CapacityClamped = parseInt(value)
			m.License = &lic
		case name == "machmqtt_license_clamp_floor":
			lic.ClampFloor = parseInt(value)
			m.License = &lic
		case name == "machmqtt_license_peer_discrepancy":
			lic.PeerDiscrepancy = parseInt(value)
			m.License = &lic
		case name == "machmqtt_license_permission_violations_total":
			lic.PermissionViolations = parseInt(value)
			m.License = &lic
		case name == "machmqtt_license_heartbeat_publish_failures_total":
			lic.HeartbeatPublishFailures = parseInt(value)
			m.License = &lic

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
		case name == "machmqtt_auth_webhook_duration_seconds_count":
			m.AuthWebhookDurationCount = parseInt(value)
		case name == "machmqtt_auth_webhook_duration_seconds_sum":
			m.AuthWebhookDurationSumSeconds = parseFloat(value)
		case name == "machmqtt_jetstream_publish_duration_seconds_count":
			m.JSPublishDurationCount = parseInt(value)
		case name == "machmqtt_jetstream_publish_duration_seconds_sum":
			m.JSPublishDurationSumSeconds = parseFloat(value)
		case name == "machmqtt_qos2_sync_persist_duration_seconds_count":
			m.QoS2SyncPersistDurationCount = parseInt(value)
		case name == "machmqtt_qos2_sync_persist_duration_seconds_sum":
			m.QoS2SyncPersistDurationSumSeconds = parseFloat(value)
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

		// Histogram bucket series. The exposition renders CUMULATIVE counts, so
		// they are accumulated as scraped and differenced back to the raw
		// per-bucket counts the push path carries once the body is consumed.
		case strings.HasSuffix(name, "_bucket"):
			if arr := m.histBuckets(strings.TrimSuffix(name, "_bucket")); arr != nil {
				if i := histBucketIndex(extractLabel(line, "le")); i >= 0 {
					arr[i] = parseInt(value)
				}
			}

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

		// --- Operator drain state ---
		case name == "machmqtt_drained":
			m.Drained = parseInt(value)

		// Anything else in the broker's namespace is a metric this build has no
		// curated field for — capture it verbatim (name plus label block) so it
		// is visible immediately rather than silently dropped. Unknown histogram
		// bucket series were already swallowed by the _bucket case above, and
		// non-machmqtt families (a proxy's own metrics, say) stay ignored.
		default:
			if strings.HasPrefix(name, "machmqtt_") {
				if m.Uncurated == nil {
					m.Uncurated = make(map[string]float64)
				}
				key := name
				if i := strings.IndexByte(line, '{'); i >= 0 {
					if end := labelBlockEnd(line, i); end > i {
						key = name + line[i:end+1]
					}
				}
				m.Uncurated[key] = parseFloat(value)
				if h, ok := help[name]; ok {
					if m.UncuratedHelp == nil {
						m.UncuratedHelp = make(map[string]string)
					}
					m.UncuratedHelp[name] = h
				}
			}
		}
	}

	for _, family := range histogramFamilies {
		bucketsCumulativeToRaw(m.histBuckets(family))
	}

	if poolSeen {
		// Slots are keyed by their slot= label rather than by position, so sort
		// them to match the ascending order the push path carries.
		slices.SortFunc(pool.Slots, func(a, b MQTTMetricsPoolSlot) int {
			return cmp.Compare(a.Slot, b.Slot)
		})
		m.Pool = &pool
	}
	return m
}

// histogramFamilies lists every histogram family whose buckets the dashboard
// stores, so the cumulative-to-raw pass covers all of them.
var histogramFamilies = []string{
	"machmqtt_publish_latency_seconds",
	"machmqtt_auth_duration_seconds",
	"machmqtt_auth_webhook_duration_seconds",
	"machmqtt_jetstream_publish_duration_seconds",
	"machmqtt_qos2_sync_persist_duration_seconds",
	"machmqtt_subscribe_duration_seconds",
	"machmqtt_dispatch_wait_seconds",
	"machmqtt_tls_handshake_duration_seconds",
}

// parseMetricLine splits a Prometheus text line into the metric name (the part
// before the first '{', or the first whitespace-delimited token) and the sample
// value. A sample may carry an optional trailing timestamp, on labelled and
// unlabelled lines alike, so only the first token after the label block (or
// after the name) is the value.
func parseMetricLine(line string) (name, value string) {
	if idx := strings.IndexByte(line, '{'); idx >= 0 {
		name = line[:idx]
		end := labelBlockEnd(line, idx)
		if end < 0 {
			return name, ""
		}
		if fields := strings.Fields(line[end+1:]); len(fields) >= 1 {
			value = fields[0]
		}
		return name, value
	}
	if parts := strings.Fields(line); len(parts) >= 2 {
		return parts[0], parts[1]
	}
	return "", ""
}

// labelBlockEnd returns the index of the '}' that closes the label block opening
// at open, ignoring braces inside quoted label values (source instance IDs and
// other client-derived label values can contain them).
func labelBlockEnd(line string, open int) int {
	inQuote := false
	for i := open + 1; i < len(line); i++ {
		switch line[i] {
		case '\\':
			if inQuote {
				i++ // an escaped byte is never a delimiter
			}
		case '"':
			inQuote = !inQuote
		case '}':
			if !inQuote {
				return i
			}
		}
	}
	return -1
}

// extractLabel returns the unescaped value of a single label key from the
// '{...}' portion of a Prometheus text line, e.g.
// extractLabel(`foo{reason="bar"} 1`, "reason") returns "bar". The key must
// match a whole label name — one starting right after '{' or after a separating
// ',' — so asking for "instance_id" never matches "source_instance_id".
// Returns "" when the key is absent or the label block is malformed.
func extractLabel(line, key string) string {
	i := strings.IndexByte(line, '{')
	if i < 0 {
		return ""
	}
	for i++; i < len(line) && line[i] != '}'; {
		eq := strings.IndexByte(line[i:], '=')
		if eq < 0 {
			return ""
		}
		name := strings.TrimSpace(line[i : i+eq])
		i += eq + 1
		if i >= len(line) || line[i] != '"' {
			return ""
		}
		val, next, ok := scanLabelValue(line, i+1)
		if !ok {
			return ""
		}
		if name == key {
			return val
		}
		for i = next; i < len(line) && (line[i] == ',' || line[i] == ' '); i++ {
		}
	}
	return ""
}

// scanLabelValue reads a quoted label value starting at i (the first byte past
// the opening quote), resolving the three escapes the Prometheus exposition
// format defines (\\, \" and \n), and returns the value together with the index
// just past the closing quote. A single left-to-right pass is required: chained
// replacements would turn the two-character sequence \\n into a newline.
func scanLabelValue(line string, i int) (value string, next int, ok bool) {
	var b strings.Builder
	for i < len(line) {
		switch c := line[i]; c {
		case '"':
			return b.String(), i + 1, true
		case '\\':
			i++
			if i >= len(line) {
				return "", i, false
			}
			switch line[i] {
			case 'n':
				b.WriteByte('\n')
			case '\\':
				b.WriteByte('\\')
			case '"':
				b.WriteByte('"')
			default:
				// Not an escape this format defines; keep both bytes verbatim.
				b.WriteByte('\\')
				b.WriteByte(line[i])
			}
			i++
		default:
			b.WriteByte(c)
			i++
		}
	}
	return "", i, false
}

// parseInt reads an integral sample value. The exposition format permits any
// float form even for counters ("1e+06", "1.5"), so parse as a float and
// convert; an unparseable or NaN sample reads zero, and values beyond int64
// saturate rather than taking Go's undefined out-of-range conversion.
func parseInt(s string) int64 {
	f, err := strconv.ParseFloat(s, 64)
	if err != nil || math.IsNaN(f) {
		return 0
	}
	switch {
	case f >= math.MaxInt64:
		return math.MaxInt64
	case f <= math.MinInt64:
		return math.MinInt64
	}
	return int64(f)
}

func parseFloat(s string) float64 {
	v, _ := strconv.ParseFloat(s, 64)
	return v
}

// histBuckets returns the bucket array for a histogram family name (the metric
// name without its _bucket suffix), or nil for a family the dashboard does not
// track.
func (m *MQTTMetrics) histBuckets(family string) *[MQTTHistogramBucketCount]int64 {
	switch family {
	case "machmqtt_publish_latency_seconds":
		return &m.PublishLatencyBuckets
	case "machmqtt_auth_duration_seconds":
		return &m.AuthDurationBuckets
	case "machmqtt_auth_webhook_duration_seconds":
		return &m.AuthWebhookDurationBuckets
	case "machmqtt_jetstream_publish_duration_seconds":
		return &m.JSPublishDurationBuckets
	case "machmqtt_qos2_sync_persist_duration_seconds":
		return &m.QoS2SyncPersistDurationBuckets
	case "machmqtt_subscribe_duration_seconds":
		return &m.SubscribeDurationBuckets
	case "machmqtt_dispatch_wait_seconds":
		return &m.DispatchWaitBuckets
	case "machmqtt_tls_handshake_duration_seconds":
		return &m.TLSHandshakeDurationBuckets
	}
	return nil
}

// histBucketIndex maps an le= label to its index in MQTTHistogramBounds. Bounds
// are rendered with %g ("1", not "1.0"), so compare parsed floats rather than
// strings. Returns -1 for "+Inf" — that series is the histogram's _count, not a
// stored bucket — and for any bound the dashboard does not know.
func histBucketIndex(le string) int {
	b, err := strconv.ParseFloat(le, 64)
	if err != nil || math.IsInf(b, 0) || math.IsNaN(b) {
		return -1
	}
	for i, bound := range MQTTHistogramBounds {
		if b == bound {
			return i
		}
	}
	return -1
}

// bucketsCumulativeToRaw differences an in-place cumulative bucket series into
// the raw per-bucket counts the push path carries. Observations above the last
// bound live only in the histogram's _count, in both representations, so the
// last bucket is never back-filled from the +Inf series. A non-monotonic series
// (an inconsistent scrape) clamps to zero rather than yielding a negative count.
func bucketsCumulativeToRaw(cum *[MQTTHistogramBucketCount]int64) {
	for i := MQTTHistogramBucketCount - 1; i > 0; i-- {
		cum[i] -= cum[i-1]
		if cum[i] < 0 {
			cum[i] = 0
		}
	}
}
