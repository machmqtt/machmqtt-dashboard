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
	// persistence is affected. Only the HTTP poll path can observe it — push
	// snapshots carry no readyz status.
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
// drift. The Phase-0 pool/reactor backpressure metrics (machmqtt_pool_buffered_bytes,
// reactor_task_queue_depth, pool_slot_buffered_bytes, etc.) are deliberately NOT
// here: they are HTTP-only admin appends, absent from the push Snapshot, so adding
// them would break push/poll parity.
type MQTTMetrics struct {
	// --- Connections (established MQTT, post-CONNECT) ---
	// As of machmqtt's socket-split (commit 11d2ebc) these count connections that
	// completed the MQTT CONNECT handshake. Pre-CONNECT transport sockets (e.g.
	// load-balancer TCP probes) are NOT counted here — see Sockets* below.
	ConnectionsActive   int64 `json:"connections_active"`
	ConnectionsTotal    int64 `json:"connections_total"`
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
	RejectedMaxConns       int64 `json:"rejected_max_conns"`
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
	ScramSessionsActive     int64 `json:"scram_sessions_active"`

	// --- Per-client NATS enforcement (OAuth2 CONNECT flow) ---
	NATSEnforcementFallback int64 `json:"nats_enforcement_fallback"`
	NATSEnforcementDenied   int64 `json:"nats_enforcement_denied"`

	// --- License feature-gate rejections ---
	// The bridge snapshot also defines a packet_size feature counter, but it is
	// always 0 and is not emitted on the Prometheus (/metrics) path; it is omitted
	// here until it carries real data (add the field + a parser branch then).
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
	PanicsRecovered      int64 `json:"panics_recovered"`
	TLSHandshakeFailures int64 `json:"tls_handshake_failures"`
	ProxyProtocolErrors  int64 `json:"proxy_protocol_errors"`
	WSUpgradeFailures    int64 `json:"ws_upgrade_failures"`
	FlowcontrolOverflow  int64 `json:"flowcontrol_overflow"`

	// --- Durability / DLQ ---
	QoS2ServerPublishFailed int64 `json:"qos2_server_publish_failed"`
	QoS1ClientSendFailed    int64 `json:"qos1_client_send_failed"`
	QoS2ClientSendFailed    int64 `json:"qos2_client_send_failed"`
	ServerPublishDropped    int64 `json:"server_publish_dropped"`
	// ServerPublishFailedQoS* is the machmqtt_server_publish_failed_total{qos=...}
	// family (transient NATS publish failures); the qos="2" series mirrors
	// QoS2ServerPublishFailed so SUM over the family covers all QoS levels.
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

	// --- Capacity & memory gauges ---
	RetainedMessages    int64 `json:"retained_messages"`
	InflightOutMessages int64 `json:"inflight_out_messages"`
	SubscriptionsActive int64 `json:"subscriptions_active"`
	OutboundBytes       int64 `json:"outbound_bytes"`

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

	// --- Queue backpressure ---
	WorkerPoolQueueDepth int64 `json:"worker_pool_queue_depth"`
	OpQueueDepth         int64 `json:"op_queue_depth"`
	OpQueueBytes         int64 `json:"op_queue_bytes"`
	OpSuspendedConns     int64 `json:"op_suspended_conns"`
	OpPoolQueueDepth     int64 `json:"op_pool_queue_depth"`
	OpPoolRejected       int64 `json:"op_pool_rejected"`

	// --- Session / consumer persistence ---
	ConsumerSeqMapEntries           int64 `json:"consumer_seq_map_entries"`
	ConsumerDeletesDropped          int64 `json:"consumer_deletes_dropped"`
	ConsumerDeleteRaces             int64 `json:"consumer_delete_races"`
	SessionDeletesDropped           int64 `json:"session_deletes_dropped"`
	SessionPersistFailedWriteFailed int64 `json:"session_persist_failed_write_failed"`
	SessionPersistFailedQueueFull   int64 `json:"session_persist_failed_queue_full"`

	// --- Reliability extras ---
	TLSCertReloadFailures   int64 `json:"tls_cert_reload_failures"`
	OAuth2JWKSFetchFailures int64 `json:"oauth2_jwks_fetch_failures"`
	// AuditWriteFailures counts audit records lost because the underlying writer
	// rejected the write (audit writes never surface errors otherwise) — a
	// non-zero value means audit records are being dropped (disk/path problem).
	AuditWriteFailures int64 `json:"audit_write_failures"`

	// --- Sparse hex-coded families, keyed by MQTT reason code (e.g. "0x88") ---
	// Only non-zero reason codes are emitted, so these are maps; nil when absent.
	ConnackRejectedByReason map[string]int64 `json:"connack_rejected_by_reason,omitempty"`
	DisconnectsSentByReason map[string]int64 `json:"disconnects_sent_by_reason,omitempty"`

	// --- JetStream / consumer gauges ---
	SessionWriteBehindDepth int64 `json:"session_write_behind_depth"`
	// ConsumerPendingMessages is -1 when JetStream is unavailable (metric absent).
	ConsumerPendingMessages int64 `json:"consumer_pending_messages"`
	StalledConsumers        int64 `json:"stalled_consumers"`

	// --- Histograms: count + sum only; buckets deliberately skipped ---
	// Average latency = SumSeconds / Count (computed in UI).
	PublishLatencyCount            int64   `json:"publish_latency_count"`
	PublishLatencySumSeconds       float64 `json:"publish_latency_sum_seconds"`
	AuthDurationCount              int64   `json:"auth_duration_count"`
	AuthDurationSumSeconds         float64 `json:"auth_duration_sum_seconds"`
	JSPublishDurationCount         int64   `json:"jetstream_publish_duration_count"`
	JSPublishDurationSumSeconds    float64 `json:"jetstream_publish_duration_sum_seconds"`
	SubscribeDurationCount         int64   `json:"subscribe_duration_count"`
	SubscribeDurationSumSeconds    float64 `json:"subscribe_duration_sum_seconds"`
	DispatchWaitCount              int64   `json:"dispatch_wait_count"`
	DispatchWaitSumSeconds         float64 `json:"dispatch_wait_sum_seconds"`
	TLSHandshakeDurationCount      int64   `json:"tls_handshake_duration_count"`
	TLSHandshakeDurationSumSeconds float64 `json:"tls_handshake_duration_sum_seconds"`

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
