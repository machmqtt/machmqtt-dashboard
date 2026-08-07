package collector

import "time"

// Varz mirrors NATS /varz response (subset).
type Varz struct {
	ServerID         string          `json:"server_id"`
	ServerName       string          `json:"server_name"`
	Version          string          `json:"version"`
	Host             string          `json:"host"`
	Port             int             `json:"port"`
	MaxConn          int             `json:"max_connections"`
	Connections      int             `json:"connections"`
	TotalConnections uint64          `json:"total_connections"`
	Routes           int             `json:"routes"`
	Leafs            int             `json:"leafnodes"`
	InMsgs           int64           `json:"in_msgs"`
	OutMsgs          int64           `json:"out_msgs"`
	InBytes          int64           `json:"in_bytes"`
	OutBytes         int64           `json:"out_bytes"`
	Mem              int64           `json:"mem"`
	CPU              float64         `json:"cpu"`
	Cores            int             `json:"cores"`
	Subscriptions    uint32          `json:"subscriptions"`
	SlowConsumers    int64           `json:"slow_consumers"`
	Start            time.Time       `json:"start"`
	Now              time.Time       `json:"now"`
	Uptime           string          `json:"uptime"`
	Cluster          ClusterOptsVarz `json:"cluster,omitempty"`
	Gateway          GatewayOptsVarz `json:"gateway,omitempty"`
	JetStream        JetStreamVarz   `json:"jetstream,omitempty"`
	GoVersion        string          `json:"go"`
}

type ClusterOptsVarz struct {
	Name string   `json:"name,omitempty"`
	URLs []string `json:"urls,omitempty"`
}

type GatewayOptsVarz struct {
	Name string `json:"name,omitempty"`
}

type JetStreamVarz struct {
	Config JetStreamConfig `json:"config,omitempty"`
	Stats  JetStreamStats  `json:"stats,omitempty"`
}

type JetStreamConfig struct {
	MaxMemory int64  `json:"max_memory"`
	MaxStore  int64  `json:"max_storage"`
	Domain    string `json:"domain,omitempty"`
}

type JetStreamStats struct {
	Memory   uint64            `json:"memory"`
	Store    uint64            `json:"storage"`
	Accounts int               `json:"accounts"`
	API      JetStreamAPIStats `json:"api"`
}

type JetStreamAPIStats struct {
	Total  uint64 `json:"total"`
	Errors uint64 `json:"errors"`
}

// Connz mirrors NATS /connz response.
type Connz struct {
	ServerID string     `json:"server_id"`
	NumConns int        `json:"num_connections"`
	Total    int        `json:"total"`
	Offset   int        `json:"offset"`
	Limit    int        `json:"limit"`
	Conns    []ConnInfo `json:"connections"`
}

type ConnInfo struct {
	ServerID       string      `json:"server_id,omitempty"`
	Cid            uint64      `json:"cid"`
	Kind           string      `json:"kind,omitempty"`
	IP             string      `json:"ip"`
	Port           int         `json:"port"`
	Start          time.Time   `json:"start"`
	LastActivity   time.Time   `json:"last_activity"`
	RTT            string      `json:"rtt,omitempty"`
	Uptime         string      `json:"uptime"`
	Idle           string      `json:"idle"`
	Pending        int         `json:"pending_bytes"`
	InMsgs         int64       `json:"in_msgs"`
	OutMsgs        int64       `json:"out_msgs"`
	InBytes        int64       `json:"in_bytes"`
	OutBytes       int64       `json:"out_bytes"`
	NumSubs        uint32      `json:"subscriptions"`
	Name           string      `json:"name,omitempty"`
	Lang           string      `json:"lang,omitempty"`
	Version        string      `json:"version,omitempty"`
	TLSVersion     string      `json:"tls_version,omitempty"`
	TLSCipher      string      `json:"tls_cipher_suite,omitempty"`
	AuthorizedUser string      `json:"authorized_user,omitempty"`
	Account        string      `json:"account,omitempty"`
	Subs           []string    `json:"subscriptions_list,omitempty"`
	SubsDetail     []SubDetail `json:"subscriptions_list_detail,omitempty"`
	MQTTClient     string      `json:"mqtt_client,omitempty"`
}

type SubDetail struct {
	Account string `json:"account,omitempty"`
	Subject string `json:"subject"`
	Queue   string `json:"qgroup,omitempty"`
	Sid     string `json:"sid"`
	Msgs    int64  `json:"msgs"`
	Max     int64  `json:"max,omitempty"`
	Cid     uint64 `json:"cid"`
}

// Routez mirrors NATS /routez response.
type Routez struct {
	ServerID   string      `json:"server_id"`
	ServerName string      `json:"server_name"`
	NumRoutes  int         `json:"num_routes"`
	Routes     []RouteInfo `json:"routes"`
}

type RouteInfo struct {
	Rid          uint64    `json:"rid"`
	RemoteID     string    `json:"remote_id"`
	RemoteName   string    `json:"remote_name"`
	DidSolicit   bool      `json:"did_solicit"`
	IP           string    `json:"ip"`
	Port         int       `json:"port"`
	Start        time.Time `json:"start"`
	LastActivity time.Time `json:"last_activity"`
	RTT          string    `json:"rtt,omitempty"`
	Uptime       string    `json:"uptime"`
	InMsgs       int64     `json:"in_msgs"`
	OutMsgs      int64     `json:"out_msgs"`
	InBytes      int64     `json:"in_bytes"`
	OutBytes     int64     `json:"out_bytes"`
	NumSubs      uint32    `json:"subscriptions"`
}

// Gatewayz mirrors NATS /gatewayz response.
type Gatewayz struct {
	ServerID         string                       `json:"server_id"`
	Name             string                       `json:"name,omitempty"`
	Host             string                       `json:"host,omitempty"`
	Port             int                          `json:"port,omitempty"`
	OutboundGateways map[string]*RemoteGatewayz   `json:"outbound_gateways"`
	InboundGateways  map[string][]*RemoteGatewayz `json:"inbound_gateways"`
}

type RemoteGatewayz struct {
	IsConfigured bool      `json:"configured"`
	Connection   *ConnInfo `json:"connection,omitempty"`
}

// Leafz mirrors NATS /leafz response.
type Leafz struct {
	ServerID string     `json:"server_id"`
	NumLeafs int        `json:"leafnodes"`
	Leafs    []LeafInfo `json:"leafs"`
}

type LeafInfo struct {
	ID       uint64 `json:"id"`
	Name     string `json:"name"`
	IsSpoke  bool   `json:"is_spoke"`
	Account  string `json:"account"`
	IP       string `json:"ip"`
	Port     int    `json:"port"`
	RTT      string `json:"rtt,omitempty"`
	InMsgs   int64  `json:"in_msgs"`
	OutMsgs  int64  `json:"out_msgs"`
	InBytes  int64  `json:"in_bytes"`
	OutBytes int64  `json:"out_bytes"`
	NumSubs  uint32 `json:"subscriptions"`
}

// SubszResp mirrors NATS /subsz response.
type SubszResp struct {
	ServerID  string  `json:"server_id"`
	NumSubs   uint32  `json:"num_subscriptions"`
	NumCache  uint32  `json:"num_cache"`
	NumInsert uint64  `json:"num_inserts"`
	NumRemove uint64  `json:"num_removes"`
	NumMatch  uint64  `json:"num_matches"`
	CacheHit  float64 `json:"cache_hit_rate"`
	MaxFanout uint32  `json:"max_fanout"`
	AvgFanout float64 `json:"avg_fanout"`
}

// JSInfo mirrors NATS /jsz response.
type JSInfo struct {
	ServerID       string            `json:"server_id"`
	Disabled       bool              `json:"disabled,omitempty"`
	Config         JetStreamConfig   `json:"config,omitempty"`
	Memory         uint64            `json:"memory"`
	Store          uint64            `json:"storage"`
	ReservedMemory uint64            `json:"reserved_memory"`
	ReservedStore  uint64            `json:"reserved_storage"`
	Accounts       int               `json:"accounts"`
	API            JetStreamAPIStats `json:"api"`
	Streams        int               `json:"streams"`
	Consumers      int               `json:"consumers"`
	Messages       uint64            `json:"messages"`
	Bytes          uint64            `json:"bytes"`
	Meta           *MetaClusterInfo  `json:"meta_cluster,omitempty"`
	AccountDetails []AccountDetail   `json:"account_details,omitempty"`
}

type MetaClusterInfo struct {
	Name     string     `json:"name,omitempty"`
	Leader   string     `json:"leader,omitempty"`
	Replicas []PeerInfo `json:"replicas,omitempty"`
	Size     int        `json:"cluster_size"`
}

type PeerInfo struct {
	Name    string        `json:"name"`
	Current bool          `json:"current"`
	Active  time.Duration `json:"active"`
	Offline bool          `json:"offline,omitempty"`
	Lag     uint64        `json:"lag,omitempty"`
}

type AccountDetail struct {
	Name    string         `json:"name"`
	ID      string         `json:"id"`
	Memory  uint64         `json:"memory"`
	Store   uint64         `json:"storage"`
	Streams []StreamDetail `json:"stream_detail,omitempty"`
}

type StreamDetail struct {
	Name      string         `json:"name"`
	Created   time.Time      `json:"created"`
	Config    StreamConfig   `json:"config,omitempty"`
	State     StreamState    `json:"state,omitempty"`
	Cluster   *ClusterInfo   `json:"cluster,omitempty"`
	Consumers []ConsumerInfo `json:"consumer_detail,omitempty"`
}

type StreamConfig struct {
	Name      string        `json:"name"`
	Subjects  []string      `json:"subjects,omitempty"`
	Retention string        `json:"retention"`
	MaxMsgs   int64         `json:"max_msgs"`
	MaxBytes  int64         `json:"max_bytes"`
	MaxAge    time.Duration `json:"max_age"`
	Storage   string        `json:"storage"`
	Replicas  int           `json:"num_replicas"`
	Discard   string        `json:"discard"`
}

type StreamState struct {
	Msgs      uint64    `json:"messages"`
	Bytes     uint64    `json:"bytes"`
	FirstSeq  uint64    `json:"first_seq"`
	LastSeq   uint64    `json:"last_seq"`
	Consumers int       `json:"consumer_count"`
	FirstTS   time.Time `json:"first_ts"`
	LastTS    time.Time `json:"last_ts"`
}

type ClusterInfo struct {
	Name     string     `json:"name,omitempty"`
	Leader   string     `json:"leader,omitempty"`
	Replicas []PeerInfo `json:"replicas,omitempty"`
}

type ConsumerInfo struct {
	StreamName     string         `json:"stream_name"`
	Name           string         `json:"name"`
	Created        time.Time      `json:"created"`
	Config         ConsumerConfig `json:"config"`
	Delivered      SequenceInfo   `json:"delivered"`
	AckFloor       SequenceInfo   `json:"ack_floor"`
	NumAckPending  int            `json:"num_ack_pending"`
	NumRedelivered int            `json:"num_redelivered"`
	NumWaiting     int            `json:"num_waiting"`
	NumPending     uint64         `json:"num_pending"`
	Cluster        *ClusterInfo   `json:"cluster,omitempty"`
}

type ConsumerConfig struct {
	Name          string `json:"name,omitempty"`
	DurableName   string `json:"durable_name,omitempty"`
	FilterSubject string `json:"filter_subject,omitempty"`
	DeliverPolicy string `json:"deliver_policy"`
	AckPolicy     string `json:"ack_policy"`
}

type SequenceInfo struct {
	Consumer uint64    `json:"consumer_seq"`
	Stream   uint64    `json:"stream_seq"`
	Last     time.Time `json:"last_active,omitempty"`
}

// Accountz mirrors NATS /accountz response.
type Accountz struct {
	ServerID      string       `json:"server_id"`
	SystemAccount string       `json:"system_account,omitempty"`
	Accounts      []string     `json:"accounts,omitempty"`
	Account       *AccountInfo `json:"account_detail,omitempty"`
}

type AccountInfo struct {
	AccountName string `json:"account_name"`
	IsSystem    bool   `json:"is_system,omitempty"`
	Expired     bool   `json:"expired"`
	JetStream   bool   `json:"jetstream_enabled"`
	LeafCnt     int    `json:"leafnode_connections"`
	ClientCnt   int    `json:"client_connections"`
	SubCnt      uint32 `json:"subscriptions"`
}

// HealthStatus mirrors NATS /healthz response.
type HealthStatus struct {
	Status string `json:"status"`
	Error  string `json:"error,omitempty"`
}
