package collector

import "time"

// MQTTBridgeStatus is the aggregated status of one MQTT bridge instance.
type MQTTBridgeStatus struct {
	Name             string        `json:"name"`
	URL              string        `json:"url"`
	Ready            bool          `json:"ready"`
	Draining         bool          `json:"draining"`
	Connections      int           `json:"connections"`
	NATSConnected    bool          `json:"nats_connected"`
	ConnzAvailable   bool          `json:"connz_available"`
	TotalConnections int64         `json:"total_connections"`
	NATS             *MQTTNATSDiag `json:"nats,omitempty"`
	Connz            *MQTTConnz    `json:"connz,omitempty"`
	Pool             *MQTTPool     `json:"pool,omitempty"`
	Metrics          *MQTTMetrics  `json:"metrics,omitempty"`
	Error            string        `json:"error,omitempty"`
}

// MQTTMetrics holds parsed Prometheus metrics from the bridge /metrics endpoint.
// JSON tags on existing fields are intentionally unchanged; UI and SQLite depend on them.
// ConsumerPendingMessages is initialised to -1 by the parser; -1 means the metric
// was absent (JetStream unavailable), 0 means the stream is present but empty.
type MQTTMetrics struct {
	// --- Connections ---
	ConnectionsActive   int64 `json:"connections_active"`
	ConnectionsTotal    int64 `json:"connections_total"`
	ConnectionsRejected int64 `json:"connections_rejected"`
	WSConnectionsActive int64 `json:"ws_connections_active"`
	WSConnectionsTotal  int64 `json:"ws_connections_total"`

	// Connection rejections broken out by remediation path
	// (machmqtt_connections_rejected_by_reason_total{reason=...}).
	RejectedMaxConns    int64 `json:"rejected_max_conns"`
	RejectedLicense     int64 `json:"rejected_license"`
	RejectedPerIPConns  int64 `json:"rejected_per_ip_conns"`
	RejectedPerIPAccept int64 `json:"rejected_per_ip_accept"`
	RejectedPoolFull    int64 `json:"rejected_pool_full"`

	// Dispatch-pool saturation (machmqtt_dispatch_slots_active{pool=...}).
	// Sustained proximity to the configured pool size precedes pool_full rejections.
	DispatchSlotsTLS int64 `json:"dispatch_slots_tls"`
	DispatchSlotsWS  int64 `json:"dispatch_slots_ws"`

	// --- Authentication ---
	AuthSuccess          int64 `json:"auth_success"`
	AuthFailure          int64 `json:"auth_failure"` // sum of all reasons
	AuthFailBadCreds     int64 `json:"auth_fail_bad_credentials"`
	AuthFailEnhanced     int64 `json:"auth_fail_enhanced"`
	AuthFailLocked       int64 `json:"auth_fail_locked"`
	AuthFailOther        int64 `json:"auth_fail_other"`
	ScramSessionsActive  int64 `json:"scram_sessions_active"`

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
	WillPending             int64 `json:"will_pending"`
	WillRetryPending        int64 `json:"will_retry_pending"`

	// --- Protocol ops ---
	Subscribes        int64 `json:"subscribes"`
	Unsubscribes      int64 `json:"unsubscribes"`
	KeepaliveTimeouts int64 `json:"keepalive_timeouts"`
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
	QoS2ServerPublishFailed  int64 `json:"qos2_server_publish_failed"`
	QoS1ClientSendFailed     int64 `json:"qos1_client_send_failed"`
	ServerPublishDropped      int64 `json:"server_publish_dropped"`
	MessagesDeadLettered      int64 `json:"messages_dead_lettered"`
	PoisonMessagesTerminated  int64 `json:"poison_messages_terminated"`
	DeadLetterWriteFailed     int64 `json:"dead_letter_write_failed"`
	OutboundQueueDropped      int64 `json:"outbound_queue_dropped"`

	// --- JetStream / consumer gauges ---
	SessionWriteBehindDepth  int64 `json:"session_write_behind_depth"`
	// ConsumerPendingMessages is -1 when JetStream is unavailable (metric absent).
	ConsumerPendingMessages  int64 `json:"consumer_pending_messages"`
	StalledConsumers         int64 `json:"stalled_consumers"`

	// --- Histograms: count + sum only; buckets deliberately skipped ---
	// Average latency = SumSeconds / Count (computed in UI).
	PublishLatencyCount          int64   `json:"publish_latency_count"`
	PublishLatencySumSeconds     float64 `json:"publish_latency_sum_seconds"`
	AuthDurationCount            int64   `json:"auth_duration_count"`
	AuthDurationSumSeconds       float64 `json:"auth_duration_sum_seconds"`
	JSPublishDurationCount       int64   `json:"jetstream_publish_duration_count"`
	JSPublishDurationSumSeconds  float64 `json:"jetstream_publish_duration_sum_seconds"`
	SubscribeDurationCount       int64   `json:"subscribe_duration_count"`
	SubscribeDurationSumSeconds  float64 `json:"subscribe_duration_sum_seconds"`
	DispatchWaitCount            int64   `json:"dispatch_wait_count"`
	DispatchWaitSumSeconds       float64 `json:"dispatch_wait_sum_seconds"`

	// --- Go runtime ---
	GoGoroutines    int64 `json:"go_goroutines"`
	GoHeapInuseBytes int64 `json:"go_heap_inuse_bytes"`
	GoGCCycles      int64 `json:"go_gc_cycles"`
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
}

// MQTTReadyz mirrors the bridge /readyz response.
type MQTTReadyz struct {
	Status        string `json:"status"`
	Connections   int    `json:"connections"`
	NATSConnected bool   `json:"nats_connected"`
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
	PendingBytes   int       `json:"pending_bytes"`
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
}
