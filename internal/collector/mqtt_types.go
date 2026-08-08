package collector

import "time"

// MQTTBridgeStatus is the aggregated status of one MQTT bridge instance.
// Ready, Draining and JetStreamDegraded are mutually exclusive: they mirror the
// bridge's /readyz status string, which reports exactly one state per response.
// A bridge that answers /readyz at all is reachable, whatever the state — only a
// transport failure or an unexpected HTTP status sets Error.
type MQTTBridgeStatus struct {
	Name     string `json:"name"`
	URL      string `json:"url"`
	Ready    bool   `json:"ready"`
	Draining bool   `json:"draining"`
	// JetStreamDegraded is the bridge's "jetstream-degraded" readyz state: MQTT
	// service is up but JetStream is currently unavailable, so QoS 1/2
	// persistence is affected. Both ingestion paths observe it — the poll path
	// from /readyz, the push path from the envelope's ready_state.
	//
	// This is the bridge's displayed state and is the field to read for it. The
	// broker also reports a jetstream_degraded METRIC on its snapshot, mirrored
	// as MQTTMetrics.JetStreamDegraded (int64, not bool); that one is a metric
	// sampled with the rest, not the readiness the UI badges.
	JetStreamDegraded bool          `json:"jetstream_degraded"`
	Connections       int           `json:"connections"`
	NATSConnected     bool          `json:"nats_connected"`
	ConnzAvailable    bool          `json:"connz_available"`
	TotalConnections  int64         `json:"total_connections"`
	NATS              *MQTTNATSDiag `json:"nats,omitempty"`
	Connz             *MQTTConnz    `json:"connz,omitempty"`
	Pool              *MQTTPool     `json:"pool,omitempty"`
	Metrics           *MQTTMetrics  `json:"metrics,omitempty"`
	Error             string        `json:"error,omitempty"`
}

// MQTTMetrics holds parsed Prometheus metrics from the bridge /metrics endpoint.
// JSON tags on existing fields are intentionally unchanged; UI and SQLite depend on them.
// ConsumerPendingMessages is initialised to -1 by the parser; -1 means the metric
// was absent (JetStream unavailable), 0 means the stream is present but empty.
//
// This struct mirrors machmqtt's connector/metrics.Snapshot, the single source for
// both the HTTP /metrics core and the NATS push payload, so push and poll cannot
// drift. The json tags here are an exact mirror of that Snapshot's tags — the
// pool/reactor backpressure families (machmqtt_pool_buffered_bytes,
// machmqtt_reactor_task_queue_depth, machmqtt_pool_slot_buffered_bytes, …) now
// arrive on BOTH paths: they live on the broker's Snapshot, so the push payload
// carries them as nested objects and the Prometheus exposition renders them from
// the same capture.
type MQTTMetrics struct {
	// --- Connections (established MQTT, post-CONNECT) ---
	// These count connections that completed the MQTT CONNECT handshake.
	// Pre-CONNECT transport sockets (e.g. load-balancer TCP probes) are NOT
	// counted here — see Sockets* below.
	ConnectionsActive int64 `json:"connections_active"`
	ConnectionsTotal  int64 `json:"connections_total"`
	// ConnectionsMax is the high-water mark of ConnectionsActive since broker start.
	ConnectionsMax      int64 `json:"connections_max"`
	ConnectionsRejected int64 `json:"connections_rejected"`
	WSConnectionsActive int64 `json:"ws_connections_active"`
	WSConnectionsTotal  int64 `json:"ws_connections_total"`

	// --- Sockets (raw transport accepts, pre-CONNECT) ---
	// These count every accepted transport socket including non-MQTT probes, and
	// SocketsOpen is the value enforced against mqtt.max_connections.
	SocketsOpen       int64 `json:"sockets_open"`
	SocketsAccepted   int64 `json:"sockets_accepted"`
	WSSocketsOpen     int64 `json:"ws_sockets_open"`
	WSSocketsAccepted int64 `json:"ws_sockets_accepted"`

	// Connection rejections broken out by remediation path
	// (machmqtt_connections_rejected_by_reason_total{reason=...}).
	RejectedMaxConns int64 `json:"rejected_max_conns"`
	// RejectedMemBudget is NOT part of the ConnectionsRejected umbrella above:
	// the broker sums only the other eight reasons into
	// machmqtt_connections_rejected_total, so this one must be read separately.
	RejectedMemBudget      int64 `json:"rejected_mem_budget"`
	RejectedLicense        int64 `json:"rejected_license"`
	RejectedPerIPConns     int64 `json:"rejected_per_ip_conns"`
	RejectedPerIPAccept    int64 `json:"rejected_per_ip_accept"`
	RejectedPoolFull       int64 `json:"rejected_pool_full"`
	RejectedConnectTimeout int64 `json:"rejected_connect_timeout"`
	RejectedAuthTimeout    int64 `json:"rejected_auth_timeout"`
	RejectedWorkerPool     int64 `json:"rejected_worker_pool"`

	// Dispatch-pool saturation (machmqtt_dispatch_slots_active{pool=...}).
	// Sustained proximity to the configured pool size precedes pool_full rejections.
	DispatchSlotsTLS int64 `json:"dispatch_slots_tls"`
	DispatchSlotsWS  int64 `json:"dispatch_slots_ws"`

	// --- Authentication ---
	AuthSuccess             int64 `json:"auth_success"`
	AuthFailure             int64 `json:"auth_failure"` // sum of all reasons
	AuthFailBadCreds        int64 `json:"auth_fail_bad_credentials"`
	AuthFailEnhanced        int64 `json:"auth_fail_enhanced"`
	AuthFailLocked          int64 `json:"auth_fail_locked"`
	AuthFailOther           int64 `json:"auth_fail_other"`
	AuthFailLicense         int64 `json:"auth_fail_license"`
	AuthFailTokenExpired    int64 `json:"auth_fail_token_expired"`
	AuthFailBadSignature    int64 `json:"auth_fail_bad_signature"`
	AuthFailClaimMismatch   int64 `json:"auth_fail_claim_mismatch"`
	AuthFailJWKSUnavailable int64 `json:"auth_fail_jwks_unavailable"`
	// AuthFailWebhookDenied/Unavailable are only non-zero when auth.type is http.
	AuthFailWebhookDenied      int64 `json:"auth_fail_webhook_denied"`
	AuthFailWebhookUnavailable int64 `json:"auth_fail_webhook_unavailable"`
	ScramSessionsActive        int64 `json:"scram_sessions_active"`
	// AuthFailureTrackerEntries is the live entry count of the bounded
	// credential-lockout tracker; a value pinned at the configured cap means
	// eviction pressure, not merely activity.
	AuthFailureTrackerEntries int64 `json:"auth_failure_tracker_entries"`

	// --- Auth webhook (auth.type "http"; zero for other auth types) ---
	AuthWebhookRequests          int64 `json:"auth_webhook_requests"`
	AuthWebhookTransportFailures int64 `json:"auth_webhook_transport_failures"`
	AuthWebhookInflightRejected  int64 `json:"auth_webhook_inflight_rejected"`

	// --- Per-client NATS enforcement (OAuth2 CONNECT flow) ---
	NATSEnforcementFallback int64 `json:"nats_enforcement_fallback"`
	NATSEnforcementDenied   int64 `json:"nats_enforcement_denied"`

	// --- License feature-gate rejections ---
	LicenseRejectedAuthMethod    int64 `json:"license_rejected_auth_method"`
	LicenseRejectedRetain        int64 `json:"license_rejected_retain"`
	LicenseRejectedProxyProtocol int64 `json:"license_rejected_proxy_protocol"`

	// --- Client messages (MQTT client ↔ broker) ---
	MsgsRecvQoS0    int64 `json:"msgs_recv_qos0"`
	MsgsRecvQoS1    int64 `json:"msgs_recv_qos1"`
	MsgsRecvQoS2    int64 `json:"msgs_recv_qos2"`
	MsgsSentQoS0    int64 `json:"msgs_sent_qos0"`
	MsgsSentQoS1    int64 `json:"msgs_sent_qos1"`
	MsgsSentQoS2    int64 `json:"msgs_sent_qos2"`
	MsgsRedelivered int64 `json:"msgs_redelivered"`

	// --- Server messages (broker ↔ NATS) ---
	ServerPublishedQoS0 int64 `json:"server_published_qos0"`
	ServerPublishedQoS1 int64 `json:"server_published_qos1"`
	ServerPublishedQoS2 int64 `json:"server_published_qos2"`
	ServerConsumedQoS0  int64 `json:"server_consumed_qos0"`
	ServerConsumedQoS1  int64 `json:"server_consumed_qos1"`
	ServerConsumedQoS2  int64 `json:"server_consumed_qos2"`

	// --- Will (Last-Will-and-Testament) ---
	WillPublished           int64 `json:"will_published"`
	WillDroppedQueueFull    int64 `json:"will_dropped_queue_full"`
	WillDroppedPublishError int64 `json:"will_dropped_publish_error"`
	WillDroppedInvalidTopic int64 `json:"will_dropped_invalid_topic"`
	WillDroppedShutdown     int64 `json:"will_dropped_shutdown"`
	WillSuppressedReconnect int64 `json:"will_suppressed_reconnected"`
	WillSuppressedShutdown  int64 `json:"will_suppressed_shutdown"`
	WillPending             int64 `json:"will_pending"`
	WillRetryPending        int64 `json:"will_retry_pending"`

	// --- Will / retain persistence failures ---
	// Non-zero WillPersistFailed* means a will's crash-durability write did not
	// land: it still fires from memory on this process, but is lost if the broker
	// crashes first. RetainPersistFailedPut means a retained message is served
	// from memory but is not durable; RetainPersistFailedDelete means a deleted
	// retained message's KV entry survives and resurrects on the next restart.
	WillPersistFailedWrite     int64 `json:"will_persist_failed_write"`
	WillPersistFailedQueueFull int64 `json:"will_persist_failed_queue_full"`
	RetainPersistFailedPut     int64 `json:"retain_persist_failed_put"`
	RetainPersistFailedDelete  int64 `json:"retain_persist_failed_delete"`

	// --- Protocol ops ---
	Subscribes         int64 `json:"subscribes"`
	Unsubscribes       int64 `json:"unsubscribes"`
	KeepaliveTimeouts  int64 `json:"keepalive_timeouts"`
	PingreqRateLimited int64 `json:"pingreq_rate_limited"`

	// --- NATS connection (link-level) ---
	NATSDisconnects  int64 `json:"nats_disconnects"`
	NATSReconnects   int64 `json:"nats_reconnects"`
	NATSSlowConsumer int64 `json:"nats_slow_consumer"`

	// --- Reliability ---
	PanicsRecovered int64 `json:"panics_recovered"`
	// HookPanics counts lifecycle-hook handler panics recovered by the hook
	// registry (the panicking handler counts as allow and the chain continues),
	// so non-zero means a registered hook handler is broken. HookVetoes counts
	// operations a hook deliberately denied — policy, not failure.
	HookPanics int64 `json:"hook_panics"`
	// SharedConsumerRecreated counts $share/ group durables the broker's periodic
	// probe found deleted and rebuilt. Each increment means at least one group
	// member had silently stopped taking its share, and that anything un-acked on
	// the deleted consumer is gone. ConsumerDeletedUnderConsume is the delivery
	// side of the same race (ConsumerDeleteRaces reports the bridge side): durable
	// consumers re-attach and resume, but a shared member does not, and un-acked
	// QoS 1/2 messages are unrecoverable either way.
	SharedConsumerRecreated     int64 `json:"shared_consumer_recreated"`
	ConsumerDeletedUnderConsume int64 `json:"consumer_deleted_under_consume"`
	HookVetoes                  int64 `json:"hook_vetoes"`
	// SysTreePublished is zero when observability.sys_tree is disabled.
	// SysPublishBlocked counts client PUBLISH/will packets to a $SYS topic
	// refused by the spoof-block, so non-zero means a client is attempting to
	// forge the broker stat tree.
	SysTreePublished  int64 `json:"sys_tree_published"`
	SysPublishBlocked int64 `json:"sys_publish_blocked"`
	// PublishRefusedTopic counts client PUBLISHes rejected with 0x90 (Topic Name
	// invalid) because the topic, while well-formed MQTT, contains a character
	// the broker cannot map onto a NATS subject ('*', '>', space, DEL, control).
	PublishRefusedTopic  int64 `json:"publish_refused_topic"`
	TLSHandshakeFailures int64 `json:"tls_handshake_failures"`
	ProxyProtocolErrors  int64 `json:"proxy_protocol_errors"`
	WSUpgradeFailures    int64 `json:"ws_upgrade_failures"`
	// WSProtocolViolations counts WebSocket framing-layer violations (RFC 6455);
	// the offending connection is closed, which is otherwise indistinguishable
	// from an ordinary disconnect.
	WSProtocolViolations int64 `json:"ws_protocol_violations"`
	FlowcontrolOverflow  int64 `json:"flowcontrol_overflow"`

	// --- Durability / DLQ ---
	// QoS2ServerPublishFailed is the PUBREL-stage forward-to-NATS failure only.
	// Store-time QoS 2 failures are counted elsewhere — transient ones in
	// ServerPublishFailedQoS2 and permanent ones in ServerPublishDropped — so
	// the two are distinct events, never the same increment seen twice.
	QoS2ServerPublishFailed int64 `json:"qos2_server_publish_failed"`
	QoS1ClientSendFailed    int64 `json:"qos1_client_send_failed"`
	QoS2ClientSendFailed    int64 `json:"qos2_client_send_failed"`
	// QoS2SyncPersistFailed counts qos2_sync_persist binding writes that failed;
	// the message was NOT sent, so there is no duplicate risk.
	QoS2SyncPersistFailed int64 `json:"qos2_sync_persist_failed"`
	ServerPublishDropped  int64 `json:"server_publish_dropped"`
	// ServerPublishFailedQoS* is the machmqtt_server_publish_failed_total{qos=...}
	// family: inbound PUBLISH that could not be stored/forwarded to NATS because
	// of a transient error, counted once per failed PUBLISH.
	ServerPublishFailedQoS0  int64 `json:"server_publish_failed_qos0"`
	ServerPublishFailedQoS1  int64 `json:"server_publish_failed_qos1"`
	ServerPublishFailedQoS2  int64 `json:"server_publish_failed_qos2"`
	QoS0MessagesShed         int64 `json:"qos0_messages_shed"`
	OversizedDropped         int64 `json:"oversized_dropped"`
	PublishOutageDisconnects int64 `json:"publish_outage_disconnects"`
	MessagesDeadLettered     int64 `json:"messages_dead_lettered"`
	PoisonMessagesTerminated int64 `json:"poison_messages_terminated"`
	DeadLetterWriteFailed    int64 `json:"dead_letter_write_failed"`
	OutboundQueueDropped     int64 `json:"outbound_queue_dropped"`
	OutboundEvictions        int64 `json:"outbound_evictions"`
	// OutboundStallEvictions counts connections evicted because their outbound
	// buffer stayed non-empty-and-undrainable past mqtt.write_timeout (a stuck
	// slow consumer); OutboundStalledConns is the gauge of connections currently
	// in that write-backpressure state.
	OutboundStallEvictions int64 `json:"outbound_stall_evictions"`
	OutboundStalledConns   int64 `json:"outbound_stalled_conns"`
	RetainVerifyFailures   int64 `json:"retained_verify_failures"`
	// WillVerifyFailures counts pending-will entries skipped because their
	// envelope did not verify (unsigned legacy record, client-id/key mismatch, or
	// HMAC mismatch). Those wills are NOT fired.
	WillVerifyFailures int64 `json:"will_verify_failures"`
	// SubscribeFlushFailures counts subscriptions whose registration with the
	// NATS server was unconfirmed when SUBACK was sent, so messages published
	// before the reconnect replay are not delivered and no error reaches the
	// client.
	SubscribeFlushFailures int64 `json:"subscribe_flush_failures"`

	// --- Capacity & memory gauges ---
	RetainedMessages    int64 `json:"retained_messages"`
	InflightOutMessages int64 `json:"inflight_out_messages"`
	SubscriptionsActive int64 `json:"subscriptions_active"`
	OutboundBytes       int64 `json:"outbound_bytes"`
	// InboundBytes is machmqtt_bytes_received_total: cumulative application
	// PUBLISH payload bytes received from clients.
	InboundBytes int64 `json:"inbound_bytes"`

	// --- Bridge / pool health ---
	PoolSlotConnected                   int64 `json:"pool_slot_connected"`
	PoolSlotRebuilds                    int64 `json:"pool_slot_rebuilds"`
	BridgePrimaryRebuilds               int64 `json:"bridge_primary_rebuilds"`
	BridgeRebuildsDegraded              int64 `json:"bridge_rebuilds_degraded"`
	BridgeConsumerReattached            int64 `json:"bridge_consumer_reattached"`
	BridgeConsumerForceDisconnected     int64 `json:"bridge_consumer_force_disconnected"`
	BridgeConsumerPushForceDisconnected int64 `json:"bridge_consumer_push_force_disconnected"`

	// --- Throttling & ACL ---
	AggregatePublishLimit     int64 `json:"aggregate_publish_limit_msgs_per_sec"`
	PublishThrottledPerClient int64 `json:"publish_throttled_per_client"`
	PublishThrottledAggregate int64 `json:"publish_throttled_aggregate"`
	ACLDeniedPublish          int64 `json:"acl_denied_publish"`
	ACLDeniedSubscribe        int64 `json:"acl_denied_subscribe"`

	// --- Cluster counters (emitted on /metrics only when clustering is enabled) ---
	ClusterInspectTimeouts   int64 `json:"cluster_inspect_timeouts"`
	ClusterTakeoverDropped   int64 `json:"cluster_takeover_dropped"`
	ClusterTakeoverOrderSkew int64 `json:"cluster_takeover_order_skew"`
	// ClusterHeartbeatPublishFailures counts cluster heartbeat publishes that
	// errored; while it rises, peers evict this instance from their peer tables
	// and cluster-wide operations skip it.
	ClusterHeartbeatPublishFailures int64 `json:"cluster_heartbeat_publish_failures,omitempty"`

	// --- Session-ownership lease (epoch-fenced cluster takeover) ---
	// Also emitted only when clustering is enabled, except SessionFencingRejected
	// which is server-sourced from s.sessions and always present (zero when no
	// fencing has occurred, which is the case outside a cluster).
	ClusterLeaseAcquired     int64 `json:"cluster_lease_acquired"`
	ClusterLeaseTransferred  int64 `json:"cluster_lease_transferred"`
	ClusterLeaseReclaimed    int64 `json:"cluster_lease_reclaimed"`
	ClusterLeaseConflicts    int64 `json:"cluster_lease_conflicts"`
	ClusterLeaseWatcherKicks int64 `json:"cluster_lease_watcher_kicks"`
	// ClusterLeaseRevisionRegressions counts lease acquisitions observed at a KV
	// revision below one already cached. Any non-zero value means epoch ordering
	// can no longer fence a stale owner on that instance.
	ClusterLeaseRevisionRegressions int64 `json:"cluster_lease_revision_regressions"`
	ClusterLeaseReleaseFailed       int64 `json:"cluster_lease_release_failed"`
	ClusterOwnedLeases              int64 `json:"cluster_owned_leases"`
	// SessionFencingRejected counts dirty-session KV writes dropped because the
	// session was fenced (deposed by a higher-epoch lease owner elsewhere in the
	// cluster); each increment is a clobber the fence prevented, not a failure.
	SessionFencingRejected int64 `json:"session_fencing_rejected"`

	// --- Queue backpressure ---
	WorkerPoolQueueDepth int64 `json:"worker_pool_queue_depth"`
	OpQueueDepth         int64 `json:"op_queue_depth"`
	OpQueueBytes         int64 `json:"op_queue_bytes"`
	OpSuspendedConns     int64 `json:"op_suspended_conns"`
	OpPoolQueueDepth     int64 `json:"op_pool_queue_depth"`
	OpPoolRejected       int64 `json:"op_pool_rejected"`

	// --- Session / consumer persistence ---
	ConsumerSeqMapEntries  int64 `json:"consumer_seq_map_entries"`
	ConsumerDeletesDropped int64 `json:"consumer_deletes_dropped"`
	ConsumerDeleteRaces    int64 `json:"consumer_delete_races"`
	// LegacyNamedConsumers is a gauge, not a counter: durable consumers still
	// carrying the pre-1.2.0 derived name. It should fall to zero as persistent
	// sessions reconnect after an upgrade, so a floor that never clears means
	// those sessions are not coming back or their consumers were orphaned.
	LegacyNamedConsumers int64 `json:"legacy_named_consumers"`
	// JetStreamAPIErrors and JetStreamAPITotal are the JetStream ACCOUNT's
	// cumulative counters as the NATS server reports them — account-wide, shared
	// by every broker on the account, and refreshed by the bridge health probe
	// every 30s rather than sampled per request. They are a ratio, not a rate:
	// errors climbing against a flat total means the API is rejecting work.
	JetStreamAPIErrors              int64 `json:"jetstream_api_errors"`
	JetStreamAPITotal               int64 `json:"jetstream_api_total"`
	JetStreamHealthProbeFailures    int64 `json:"jetstream_health_probe_failures"`
	SessionDeletesDropped           int64 `json:"session_deletes_dropped"`
	SessionPersistFailedWriteFailed int64 `json:"session_persist_failed_write_failed"`
	SessionPersistFailedQueueFull   int64 `json:"session_persist_failed_queue_full"`
	// SessionPersistPanics counts write-behind flushes that panicked and were
	// recovered per-session; it arrives under the same
	// machmqtt_session_persist_failed_total family with reason="panic".
	SessionPersistPanics int64 `json:"session_persist_panics"`

	// --- Reliability extras ---
	// CredentialExpiryDisconnects counts clients disconnected because their
	// authentication credential elapsed mid-session — distinct from a keepalive
	// timeout, and a spike means a broken token-refresh flow.
	CredentialExpiryDisconnects int64 `json:"credential_expiry_disconnects"`
	// MTLSIdentityFallback* count connections that fell back to the
	// CONNECT-supplied username while mqtt.tls.identity_source was configured.
	// NoCert is the security-relevant case: no certificate identity was verified
	// at all.
	MTLSIdentityFallbackLicense int64 `json:"mtls_identity_fallback_license"`
	MTLSIdentityFallbackNoMatch int64 `json:"mtls_identity_fallback_no_match"`
	MTLSIdentityFallbackNoCert  int64 `json:"mtls_identity_fallback_no_cert"`
	// OTelHistogramSkewClamped counts OTLP exports whose histogram +Inf/_count
	// was clamped up to the observed bucket total; a steady rate is a renderer
	// bug rather than a benign sampling race.
	OTelHistogramSkewClamped int64 `json:"otel_histogram_skew_clamped"`
	TLSCertReloadFailures    int64 `json:"tls_cert_reload_failures"`
	OAuth2JWKSFetchFailures  int64 `json:"oauth2_jwks_fetch_failures"`
	// OAuth2TokenCacheEvictions counts token-cache entries evicted at capacity;
	// sustained growth means CONNECTs are paying full JWT verification cost.
	OAuth2TokenCacheEvictions int64 `json:"oauth2_token_cache_evictions"`
	// AuditWriteFailures counts audit records lost because the underlying writer
	// rejected the write (audit writes never surface errors otherwise) — a
	// non-zero value means audit records are being dropped (disk/path problem).
	AuditWriteFailures int64 `json:"audit_write_failures"`

	// --- Sparse hex-coded families, keyed by MQTT reason code (e.g. "0x88") ---
	// Only non-zero reason codes are emitted, so these are maps; nil when absent.
	ConnackRejectedByReason map[string]int64 `json:"connack_rejected_by_reason,omitempty"`
	// SubackRejectedByReason is counted per rejected topic filter, so one
	// SUBSCRIBE packet can contribute several increments.
	SubackRejectedByReason  map[string]int64 `json:"suback_rejected_by_reason,omitempty"`
	DisconnectsSentByReason map[string]int64 `json:"disconnects_sent_by_reason,omitempty"`

	// --- JetStream / consumer gauges ---
	// SubscribeConsumerFailures isolates the SUBACK 0x80s caused by a JetStream
	// consumer create/update failing from the policy rejections that share the
	// same reason code. SubscribeConsumerRetries counts the creates that failed
	// once and then succeeded — subscribes rescued rather than lost.
	SubscribeConsumerFailures int64 `json:"subscribe_consumer_failures"`
	SubscribeConsumerRetries  int64 `json:"subscribe_consumer_retries"`

	SessionWriteBehindDepth int64 `json:"session_write_behind_depth"`
	// ConsumerPendingMessages is -1 when JetStream is unavailable (metric absent).
	ConsumerPendingMessages int64 `json:"consumer_pending_messages"`
	StalledConsumers        int64 `json:"stalled_consumers"`

	// --- Histograms: count + sum ---
	// Average latency = SumSeconds / Count (computed in UI).
	PublishLatencyCount               int64   `json:"publish_latency_count"`
	PublishLatencySumSeconds          float64 `json:"publish_latency_sum_seconds"`
	AuthDurationCount                 int64   `json:"auth_duration_count"`
	AuthDurationSumSeconds            float64 `json:"auth_duration_sum_seconds"`
	AuthWebhookDurationCount          int64   `json:"auth_webhook_duration_count"`
	AuthWebhookDurationSumSeconds     float64 `json:"auth_webhook_duration_sum_seconds"`
	JSPublishDurationCount            int64   `json:"jetstream_publish_duration_count"`
	JSPublishDurationSumSeconds       float64 `json:"jetstream_publish_duration_sum_seconds"`
	QoS2SyncPersistDurationCount      int64   `json:"qos2_sync_persist_duration_count"`
	QoS2SyncPersistDurationSumSeconds float64 `json:"qos2_sync_persist_duration_sum_seconds"`
	SubscribeDurationCount            int64   `json:"subscribe_duration_count"`
	SubscribeDurationSumSeconds       float64 `json:"subscribe_duration_sum_seconds"`
	DispatchWaitCount                 int64   `json:"dispatch_wait_count"`
	DispatchWaitSumSeconds            float64 `json:"dispatch_wait_sum_seconds"`
	TLSHandshakeDurationCount         int64   `json:"tls_handshake_duration_count"`
	TLSHandshakeDurationSumSeconds    float64 `json:"tls_handshake_duration_sum_seconds"`

	// --- Histogram buckets ---
	// RAW (non-cumulative) per-bucket observation counts, aligned with
	// MQTTHistogramBounds. The push payload carries these raw counts directly;
	// the Prometheus exposition renders them cumulatively, so the poll parser
	// differences the le= series back to raw. Observations above the last bound
	// appear only in the *Count total, so sum(buckets) <= Count by design.
	PublishLatencyBuckets          [MQTTHistogramBucketCount]int64 `json:"publish_latency_buckets"`
	AuthDurationBuckets            [MQTTHistogramBucketCount]int64 `json:"auth_duration_buckets"`
	AuthWebhookDurationBuckets     [MQTTHistogramBucketCount]int64 `json:"auth_webhook_duration_buckets"`
	JSPublishDurationBuckets       [MQTTHistogramBucketCount]int64 `json:"jetstream_publish_duration_buckets"`
	QoS2SyncPersistDurationBuckets [MQTTHistogramBucketCount]int64 `json:"qos2_sync_persist_duration_buckets"`
	SubscribeDurationBuckets       [MQTTHistogramBucketCount]int64 `json:"subscribe_duration_buckets"`
	DispatchWaitBuckets            [MQTTHistogramBucketCount]int64 `json:"dispatch_wait_buckets"`
	TLSHandshakeDurationBuckets    [MQTTHistogramBucketCount]int64 `json:"tls_handshake_duration_buckets"`

	// --- Go runtime ---
	GoGoroutines     int64 `json:"go_goroutines"`
	GoHeapInuseBytes int64 `json:"go_heap_inuse_bytes"`
	GoGCCycles       int64 `json:"go_gc_cycles"`
	GoGCPauseNsTotal int64 `json:"go_gc_pause_ns_total"`

	// --- Instance identity ---
	// InstanceID is the value of the instance_id label from machmqtt_instance_info.
	// Empty string when the metric is absent (broker has no InstanceID configured).
	InstanceID string `json:"instance_id,omitempty"`

	// Drained is 1 when the instance is operator-drained (machmqtt_drained).
	Drained int64 `json:"drained"`

	// --- Sub-objects for the family-gated metric groups ---
	// Nil when the corresponding subsystem is not configured, exactly as the
	// broker gates them: the push payload omits the object, and the Prometheus
	// exposition omits the whole family, so the poll parser leaves the pointer
	// nil unless it saw at least one of that group's families.
	License *MQTTMetricsLicense `json:"license,omitempty"`
	Reactor *MQTTMetricsReactor `json:"reactor,omitempty"`
	Pool    *MQTTMetricsPool    `json:"pool,omitempty"`

	// ClusterHMACFailures carries the per-source HMAC verification failure
	// counters, sorted by source ID. Nil when clustering is off or no failures
	// have been seen.
	ClusterHMACFailures []MQTTHMACFailure `json:"cluster_hmac_failures,omitempty"`

	// Family gates. On the push path these arrive as booleans; on the poll path
	// they are inferred from the presence of the families each one gates, since
	// the broker exposes no metric line for the gate itself.
	AuthWebhookActive bool `json:"auth_webhook_active,omitempty"`
	// --- Bridge and JetStream state ---
	// StreamEnsureRetries counts stream ensures that failed once and succeeded on
	// a retry — each one a JetStream API reply that went missing while the server
	// did the work. StreamEnsureStalls is the leading indicator: it increments
	// when the stall watchdog fires even if the create then succeeds, so a
	// cluster starting to drop replies shows here before a restart actually fails.
	StreamEnsureRetries int64 `json:"stream_ensure_retries"`
	StreamEnsureStalls  int64 `json:"stream_ensure_stalls"`
	// NATSConnected and JetStreamDegraded are 0/1 state gauges, so zero is a
	// reported value and not an absent one. JetStreamDegraded is 1 when JetStream
	// is unhealthy WHILE the NATS socket is still up — the failure mode with no
	// other live signal, since the disconnect counters stay flat throughout it.
	//
	// These are metrics, distinct from the same-named readiness fields on
	// MQTTBridgeStatus: those come from /readyz on the poll path and the
	// envelope's ready_state on the push path, and remain the source for the
	// bridge's displayed state. See MQTTBridgeStatus.JetStreamDegraded.
	NATSConnected     int64 `json:"nats_connected"`
	JetStreamDegraded int64 `json:"jetstream_degraded"`
	// ConsumersAwaitingReattach is how many clients have dead durable QoS 1/2
	// consumers pending re-attach after a degraded rebuild: non-zero means this
	// instance cannot deliver QoS 1/2 to that many sessions right now.
	// ReattachSweepDurationMs is integer MILLISECONDS (the broker keeps it an
	// int64 so the OTLP path can carry it), and reports the most recent sweep
	// only — rebuilds are rare, so the last one is the useful signal.
	ConsumersAwaitingReattach int64 `json:"consumers_awaiting_reattach"`
	ReattachSweepDurationMs   int64 `json:"reattach_sweep_duration_ms"`

	ClusterEnabled bool `json:"cluster_enabled,omitempty"`
	BridgeUp       bool `json:"bridge_up,omitempty"`
	SessionsUp     bool `json:"sessions_up,omitempty"`

	// --- Dashboard-local capture (NOT part of the broker snapshot mirror) ---
	// Uncurated holds broker metrics this build has no curated field for:
	// unknown machmqtt_* Prometheus families on the scrape path (keyed by the
	// series name including its label block) and unknown numeric top-level keys
	// in the pushed metrics object. A new broker metric is therefore visible —
	// raw, under its wire name — the moment the broker ships it, instead of
	// silently invisible until the next contract sync. The broker-side tag
	// parity check must treat these two fields as dashboard-local extras.
	Uncurated map[string]float64 `json:"uncurated,omitempty"`
	// UncuratedHelp carries the broker's own # HELP text for captured families
	// (scrape path only — the push payload has no help text), keyed by family
	// name without labels.
	UncuratedHelp map[string]string `json:"uncurated_help,omitempty"`
}

// MQTTHistogramBucketCount is the number of explicit histogram buckets every
// machmqtt latency histogram uses; MQTTHistogramBounds holds their upper bounds
// in seconds. The +Inf bucket is not stored — it equals the histogram's *Count.
const MQTTHistogramBucketCount = 9

// MQTTHistogramBounds are the upper bounds (seconds) of the explicit buckets, in
// ascending order, matching the bridge's histogram configuration.
var MQTTHistogramBounds = [MQTTHistogramBucketCount]float64{
	0.0005, 0.001, 0.005, 0.01, 0.05, 0.1, 0.5, 1, 5,
}

// MQTTMetricsLicense is the license-manager gauge/counter group
// (machmqtt_license_*), absent entirely when no license manager is configured.
// It does not include the license feature-gate rejection counters, which are
// core metrics present in every deployment (LicenseRejected* above).
type MQTTMetricsLicense struct {
	Valid  int64 `json:"valid"`
	Status int64 `json:"status"`
	// HasExpiry is false for a license with no expiry, in which case
	// ExpiresTimestamp is absent and must not be read. On the poll path it is
	// inferred from the presence of machmqtt_license_expires_timestamp.
	HasExpiry        bool  `json:"has_expiry,omitempty"`
	ExpiresTimestamp int64 `json:"expires_timestamp,omitempty"`
	// ConnectionsLimit is 0 for unlimited.
	ConnectionsLimit  int64 `json:"connections_limit"`
	ConnectionsGlobal int64 `json:"connections_global"`
	MaxQoS            int64 `json:"max_qos"`
	Instances         int64 `json:"instances"`
	Degraded          int64 `json:"degraded"`
	BlockConfirmed    int64 `json:"block_confirmed"`
	// CapacityClamped is the operative alarm: new connections are being held at
	// ClampFloor.
	CapacityClamped          int64 `json:"capacity_clamped"`
	ClampFloor               int64 `json:"clamp_floor"`
	PeerDiscrepancy          int64 `json:"peer_discrepancy"`
	PermissionViolations     int64 `json:"permission_violations"`
	HeartbeatPublishFailures int64 `json:"heartbeat_publish_failures"`
}

// MQTTMetricsReactor is the I/O-reactor diagnostic group (machmqtt_reactor_*).
type MQTTMetricsReactor struct {
	TaskQueueDepth    int64 `json:"task_queue_depth"`
	TaskQueueDepthMax int64 `json:"task_queue_depth_max"`
	LoopPanics        int64 `json:"loop_panics"`
	// ReadContinuations counts reads deferred to a later event-loop pass because
	// the connection hit its per-call read budget. Non-zero is normal under load;
	// it is the read-fairness mechanism engaging.
	ReadContinuations int64 `json:"read_continuations"`
	WriteBackpressure int64 `json:"write_backpressure"`
	// FeedWriteOverflows counts TLS/WS connections torn down because their cipher
	// write buffer hit its hard cap; expect ~0.
	FeedWriteOverflows int64 `json:"feed_write_overflows"`
	// FeedReadOverflows counts TLS/WS connections torn down because their feed
	// read buffer hit its cap — a peer sending bytes the TLS/WS stack will not
	// consume. Expect ~0; any value is a broken or abusive client.
	FeedReadOverflows int64 `json:"feed_read_overflows"`
	// LoopDeaths counts event loops that exited permanently on a fatal poller
	// error; their connections are force-closed and new ones steered elsewhere.
	LoopDeaths int64 `json:"loop_deaths"`
}

// MQTTMetricsPool is the NATS connection-pool group (machmqtt_pool_*), including
// the pre-aggregated buffered-bytes gauges. A high BufferedBytesMax with an idle
// broker is the head-of-line signature: deliveries queued behind publishes on
// one slot's single TCP connection.
type MQTTMetricsPool struct {
	Size             int64                 `json:"size"`
	BufferedBytes    int64                 `json:"buffered_bytes"`
	BufferedBytesMax int64                 `json:"buffered_bytes_max"`
	Slots            []MQTTMetricsPoolSlot `json:"slots,omitempty"`
}

// MQTTMetricsPoolSlot is one NATS connection-pool slot's stats. OutMsgs/InMsgs
// are monotonic across slot rebuilds. This is the metrics-path per-slot view;
// MQTTPoolSlot is the richer /pool admin-endpoint view.
type MQTTMetricsPoolSlot struct {
	Slot          int64 `json:"slot"`
	BufferedBytes int64 `json:"buffered_bytes"`
	OutMsgs       int64 `json:"out_msgs"`
	InMsgs        int64 `json:"in_msgs"`
}

// MQTTHMACFailure is one source instance's cluster-HMAC verification failure
// count. Source IDs can embed client-derived strings, so on the poll path the
// label value arrives escaped and must be unescaped.
type MQTTHMACFailure struct {
	SourceInstanceID string `json:"source_instance_id"`
	Count            int64  `json:"count"`
}

// MQTTDiag mirrors the bridge /diag response.
type MQTTDiag struct {
	ConfigPath string `json:"config_path"`
	Version    string `json:"version,omitempty"`
	Config     any    `json:"config"` // raw JSON — too many fields to type
}

// MQTTLicense mirrors the bridge /license response.
type MQTTLicense struct {
	Status            string `json:"status"`
	LicenseID         string `json:"license_id,omitempty"`
	Company           string `json:"company,omitempty"`
	Contact           string `json:"contact,omitempty"`
	Email             string `json:"email,omitempty"`
	Kind              string `json:"kind,omitempty"`
	Tier              string `json:"tier,omitempty"`
	MaxConnections    int    `json:"max_connections"`
	MaxQoS            int    `json:"max_qos"`
	ConnectionsLocal  int64  `json:"connections_local"`
	ConnectionsGlobal int64  `json:"connections_global"`
	Instances         int    `json:"instances"`
	ExpiresAt         string `json:"expires_at,omitempty"`
	GraceDays         int    `json:"grace_days,omitempty"`
	Degraded          bool   `json:"degraded,omitempty"`

	// Degraded/clamped license state reported by the broker's /license endpoint.
	// CapacityClamped is the operative alarm; the reason strings are
	// human-readable evidence. All fields are omitempty, so they are absent
	// (zero/false) on brokers that do not report this state.
	DegradedReason  string `json:"degraded_reason,omitempty"`
	BlockConfirmed  bool   `json:"block_confirmed,omitempty"`
	BlockReason     string `json:"block_reason,omitempty"`
	CapacityClamped bool   `json:"capacity_clamped,omitempty"`
	ClampFloor      int    `json:"clamp_floor,omitempty"`
	PeerDiscrepancy bool   `json:"peer_discrepancy,omitempty"`

	// Fleet-tier rate ceilings (omitted when 0 / not a Fleet license).
	AggregateMsgsPerSec          int `json:"aggregate_msgs_per_sec,omitempty"`
	AggregateBurstMsgsPerSec     int `json:"aggregate_burst_msgs_per_sec,omitempty"`
	AggregateBurstWindowSec      int `json:"aggregate_burst_window_sec,omitempty"`
	MaxClientMsgsPerSec          int `json:"max_client_msgs_per_sec,omitempty"`
	EffectiveAggregateMsgsPerSec int `json:"effective_aggregate_msgs_per_sec"`
}

// MQTTReadyz mirrors the bridge /readyz response. The endpoint reports
// readiness only; it carries no connection count (that comes from the metrics
// snapshot in FetchStatus).
//
// Status is one of "ready" (HTTP 200), "draining", "jetstream-degraded" or
// "not ready" (all HTTP 503) — the non-ready bodies are the state itself, so
// FetchReadyz decodes them instead of failing (see fetchAccepting).
type MQTTReadyz struct {
	Status        string `json:"status"`
	NATSConnected bool   `json:"nats_connected"`
	// JetStreamReady is only present on the "jetstream-degraded" body; it is
	// absent (and therefore false) on every other status, so never read it
	// without checking Status first.
	JetStreamReady bool `json:"jetstream_ready"`
}

// MQTTConnz mirrors the bridge /connz response.
type MQTTConnz struct {
	ServerID       string           `json:"server_id"`
	Now            time.Time        `json:"now"`
	NumConnections int              `json:"num_connections"`
	Total          int64            `json:"total"`
	Offset         int              `json:"offset"`
	Limit          int              `json:"limit"`
	Connections    []MQTTClientInfo `json:"connections"`
}

type MQTTClientInfo struct {
	CID            uint64    `json:"cid"`
	MQTTClient     string    `json:"mqtt_client"`
	Kind           string    `json:"kind"`
	Type           string    `json:"type"`
	IP             string    `json:"ip"`
	Port           int       `json:"port"`
	Start          time.Time `json:"start,omitempty"`
	LastActivity   time.Time `json:"last_activity,omitempty"`
	Uptime         string    `json:"uptime,omitempty"`
	Idle           string    `json:"idle,omitempty"`
	PendingBytes   int64     `json:"pending_bytes"`
	InMsgs         int64     `json:"in_msgs"`
	OutMsgs        int64     `json:"out_msgs"`
	InBytes        int64     `json:"in_bytes"`
	OutBytes       int64     `json:"out_bytes"`
	Subscriptions  int       `json:"subscriptions"`
	Lang           string    `json:"lang"`
	IsWebSocket    bool      `json:"is_websocket,omitempty"`
	CleanStart     bool      `json:"clean_start"`
	KeepAlive      int       `json:"keep_alive"`
	SessionExpiry  uint32    `json:"session_expiry_interval"`
	ReceiveMaximum int       `json:"receive_maximum"`
	InflightOut    int       `json:"inflight_out"`
	Username       string    `json:"username,omitempty"`
	State          string    `json:"state"`
	SlowConsumer   bool      `json:"slow_consumer,omitempty"`
}

// MQTTNATSDiag mirrors the bridge /diag/nats response.
type MQTTNATSDiag struct {
	Connection MQTTNATSConnection `json:"connection"`
	Account    *MQTTNATSAccount   `json:"account,omitempty"`
	Streams    []MQTTNATSStream   `json:"streams,omitempty"`
	KVBuckets  []MQTTNATSKVBucket `json:"kv_buckets,omitempty"`
}

type MQTTNATSConnection struct {
	Connected     bool     `json:"connected"`
	Reconnecting  bool     `json:"reconnecting"`
	Draining      bool     `json:"draining"`
	URL           string   `json:"url"`
	ServerID      string   `json:"server_id"`
	ServerName    string   `json:"server_name"`
	ServerVersion string   `json:"server_version"`
	ClusterName   string   `json:"cluster_name,omitempty"`
	Servers       []string `json:"servers"`
	MaxPayload    int64    `json:"max_payload"`
	Subscriptions int      `json:"subscriptions"`
	RTT           string   `json:"rtt,omitempty"`
	InMsgs        uint64   `json:"in_msgs"`
	OutMsgs       uint64   `json:"out_msgs"`
	InBytes       uint64   `json:"in_bytes"`
	OutBytes      uint64   `json:"out_bytes"`
	Reconnects    uint64   `json:"reconnects"`
}

type MQTTNATSAccount struct {
	Domain    string `json:"domain,omitempty"`
	Memory    uint64 `json:"memory_bytes"`
	Store     uint64 `json:"store_bytes"`
	Streams   int    `json:"streams"`
	Consumers int    `json:"consumers"`
}

type MQTTNATSStream struct {
	Name        string    `json:"name"`
	Messages    uint64    `json:"messages"`
	Bytes       uint64    `json:"bytes"`
	Consumers   int       `json:"consumers"`
	FirstSeq    uint64    `json:"first_seq"`
	LastSeq     uint64    `json:"last_seq"`
	NumSubjects uint64    `json:"num_subjects"`
	Created     time.Time `json:"created"`
	Error       string    `json:"error,omitempty"`
}

type MQTTNATSKVBucket struct {
	Bucket string `json:"bucket"`
	Values uint64 `json:"values"`
	Bytes  uint64 `json:"bytes"`
	TTL    string `json:"ttl,omitempty"`
	Error  string `json:"error,omitempty"`
}

// MQTTCluster mirrors the bridge GET /admin/cluster response: a read-only view
// of cluster members (from the heartbeat map) plus per-source HMAC failures.
type MQTTCluster struct {
	LocalInstanceID  string                `json:"local_instance_id"`
	LocalConnections int64                 `json:"local_connections"`
	Instances        []MQTTClusterInstance `json:"instances"`
	HMACFailures     map[string]int64      `json:"hmac_failures,omitempty"`
	// TakeoverOrderSkew counts cross-node takeovers where local/remote connect
	// timestamps differed by less than the clock-skew window; a rising value
	// signals inter-node clock divergence.
	TakeoverOrderSkew int64 `json:"takeover_order_skew"`
}

type MQTTClusterInstance struct {
	InstanceID string `json:"instance_id"`
	Addr       string `json:"addr"`
	Clients    int64  `json:"clients"`
	StartedAt  string `json:"started_at"`
	UpdatedAt  string `json:"updated_at"`
	LastSeenMs int64  `json:"last_seen_ms"`
	Self       bool   `json:"self"`
}

// MQTTClusterInspect mirrors GET /admin/cluster/inspect. Client is kept as a
// raw object so the dashboard tolerates field drift across bridge versions.
type MQTTClusterInspect struct {
	InstanceID string `json:"instance_id"`
	Client     any    `json:"client"`
}

// MQTTPool mirrors the bridge /pool response.
type MQTTPool struct {
	Size  int            `json:"size"`
	Slots []MQTTPoolSlot `json:"slots"`
}

type MQTTPoolSlot struct {
	Index      int   `json:"index"`
	Connected  bool  `json:"connected"`
	SubCount   int64 `json:"sub_count"`
	PubCount   int64 `json:"pub_count"`
	FlushCount int64 `json:"flush_count"`
	// BufferedBytes is the direct publish-side backpressure signal; the rest are
	// per-slot throughput/reconnect counters. Absent (zero) on older bridges.
	BufferedBytes int64 `json:"buffered_bytes"`
	OutMsgs       int64 `json:"out_msgs"`
	InMsgs        int64 `json:"in_msgs"`
	OutBytes      int64 `json:"out_bytes"`
	InBytes       int64 `json:"in_bytes"`
	Reconnects    int64 `json:"reconnects"`
}
