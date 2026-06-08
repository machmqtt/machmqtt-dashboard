package collector

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	nats "github.com/nats-io/nats.go"
	"github.com/noodlebit/machmqtt-dashboard/internal/config"
)

// bridgeTTL is how long a bridge's last-seen entry survives without a new
// publish before it is considered gone. Set to 3× the expected publish interval.
const bridgeTTL = 45 * time.Second

// BridgeMetricsMsg is the wire format published by MachMQTT bridges to
// <prefix>.metrics.<instance_id>. Schema version v=1.
// This is the canonical contract between MachMQTT (publisher) and the
// dashboard subscribe-collector (consumer). Both sides must match field-for-field.
type BridgeMetricsMsg struct {
	V           int       `json:"v"`                     // schema version, currently 1
	InstanceID  string    `json:"instance_id"`           // stable per-instance identity
	Version     string    `json:"version,omitempty"`     // MachMQTT app version
	Name        string    `json:"name,omitempty"`        // configured bridge name
	PublishedAt time.Time `json:"published_at"`
	Drained     bool      `json:"drained,omitempty"` // graceful-shutdown marker

	// Connection counters (cumulative).
	ConnectionsActive   int64 `json:"connections_active"`
	ConnectionsTotal    int64 `json:"connections_total"`
	ConnectionsRejected int64 `json:"connections_rejected"`

	// MQTT message counters (cumulative), broken out by QoS direction.
	MsgsRecvQoS0    int64 `json:"msgs_recv_qos0"`
	MsgsRecvQoS1    int64 `json:"msgs_recv_qos1"`
	MsgsRecvQoS2    int64 `json:"msgs_recv_qos2"`
	MsgsSentQoS0    int64 `json:"msgs_sent_qos0"`
	MsgsSentQoS1    int64 `json:"msgs_sent_qos1"`
	MsgsSentQoS2    int64 `json:"msgs_sent_qos2"`
	MsgsRedelivered int64 `json:"msgs_redelivered"`

	// NATS link health.
	NATSConnected    bool   `json:"nats_connected"`
	NATSServerID     string `json:"nats_server_id,omitempty"`
	NATSServerName   string `json:"nats_server_name,omitempty"`
	NATSURL          string `json:"nats_url,omitempty"`
	NATSRTT          string `json:"nats_rtt,omitempty"`
	NATSReconnects   uint64 `json:"nats_reconnects"`
	NATSDisconnects  int64  `json:"nats_disconnects"`
	NATSSlowConsumer int64  `json:"nats_slow_consumer"`

	// NATS connection pool.
	Pool BridgePool `json:"pool"`

	// JetStream gauges. -1 means JetStream is absent/disabled (metric not emitted).
	ConsumerPendingMessages int64 `json:"consumer_pending_messages"`
	StalledConsumers        int64 `json:"stalled_consumers"`
	SessionWriteBehindDepth int64 `json:"session_write_behind_depth"`
}

// BridgePool is the wire representation of the NATS connection pool.
type BridgePool struct {
	Size  int              `json:"size"`
	Slots []BridgePoolSlot `json:"slots,omitempty"`
}

// BridgePoolSlot is one slot in the NATS connection pool.
type BridgePoolSlot struct {
	Index      int   `json:"index"`
	Connected  bool  `json:"connected"`
	SubCount   int64 `json:"sub_count"`
	PubCount   int64 `json:"pub_count"`
	FlushCount int64 `json:"flush_count"`
}

type cachedBridge struct {
	msg        *BridgeMetricsMsg
	receivedAt time.Time
}

// MQTTSubscriber maintains a live TTL-keyed cache of bridge metrics received
// via NATS pub/sub. It is the push-based replacement for connz-scan discovery.
type MQTTSubscriber struct {
	mu      sync.RWMutex
	bridges map[string]*cachedBridge // keyed by instance_id
}

func newMQTTSubscriber() *MQTTSubscriber {
	return &MQTTSubscriber{bridges: make(map[string]*cachedBridge)}
}

// run connects to NATS, subscribes to <prefix>.metrics.>, and maintains the
// bridge cache until ctx is cancelled. Intended to be started as a goroutine.
// Connection failures are retried silently; the cache simply stays empty until
// a server is reachable, degrading gracefully to the HTTP-poll path.
func (s *MQTTSubscriber) run(ctx context.Context, cfg *config.NATSConnConfig) {
	nc, err := connectNATS(cfg)
	if err != nil {
		return
	}
	defer nc.Close()

	prefix := cfg.SubjectPrefixOrDefault()
	_, err = nc.Subscribe(prefix+".metrics.>", func(msg *nats.Msg) {
		var m BridgeMetricsMsg
		if json.Unmarshal(msg.Data, &m) != nil || m.InstanceID == "" {
			return
		}
		s.mu.Lock()
		s.bridges[m.InstanceID] = &cachedBridge{msg: &m, receivedAt: time.Now()}
		s.mu.Unlock()
	})
	if err != nil {
		return
	}

	sweeper := time.NewTicker(bridgeTTL / 3)
	defer sweeper.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-sweeper.C:
			s.sweepExpired()
		}
	}
}

func (s *MQTTSubscriber) sweepExpired() {
	deadline := time.Now().Add(-bridgeTTL)
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, cb := range s.bridges {
		if cb.receivedAt.Before(deadline) {
			delete(s.bridges, id)
		}
	}
}

// Bridges returns the current live bridge instances from the push cache.
func (s *MQTTSubscriber) Bridges() []MQTTBridgeInstance {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]MQTTBridgeInstance, 0, len(s.bridges))
	for id, cb := range s.bridges {
		out = append(out, bridgeMsgToInstance(id, cb.msg))
	}
	return out
}

// bridgeMsgToInstance converts a wire message into the existing MQTTBridgeInstance
// shape so that the overview/topology/WS broadcast code requires no changes.
func bridgeMsgToInstance(id string, m *BridgeMetricsMsg) MQTTBridgeInstance {
	pool := &MQTTPool{
		Size:  m.Pool.Size,
		Slots: make([]MQTTPoolSlot, 0, len(m.Pool.Slots)),
	}
	for _, sl := range m.Pool.Slots {
		pool.Slots = append(pool.Slots, MQTTPoolSlot{
			Index:      sl.Index,
			Connected:  sl.Connected,
			SubCount:   sl.SubCount,
			PubCount:   sl.PubCount,
			FlushCount: sl.FlushCount,
		})
	}

	metrics := &MQTTMetrics{
		ConnectionsActive:       m.ConnectionsActive,
		ConnectionsTotal:        m.ConnectionsTotal,
		ConnectionsRejected:     m.ConnectionsRejected,
		MsgsRecvQoS0:            m.MsgsRecvQoS0,
		MsgsRecvQoS1:            m.MsgsRecvQoS1,
		MsgsRecvQoS2:            m.MsgsRecvQoS2,
		MsgsSentQoS0:            m.MsgsSentQoS0,
		MsgsSentQoS1:            m.MsgsSentQoS1,
		MsgsSentQoS2:            m.MsgsSentQoS2,
		MsgsRedelivered:         m.MsgsRedelivered,
		NATSDisconnects:         m.NATSDisconnects,
		NATSReconnects:          int64(m.NATSReconnects),
		NATSSlowConsumer:        m.NATSSlowConsumer,
		ConsumerPendingMessages: m.ConsumerPendingMessages,
		StalledConsumers:        m.StalledConsumers,
		SessionWriteBehindDepth: m.SessionWriteBehindDepth,
		InstanceID:              id,
		Drained:                 boolToInt64(m.Drained),
	}

	natsConn := MQTTNATSConnection{
		Connected:  m.NATSConnected,
		URL:        m.NATSURL,
		ServerID:   m.NATSServerID,
		ServerName: m.NATSServerName,
		RTT:        m.NATSRTT,
		Reconnects: m.NATSReconnects,
	}

	status := &MQTTBridgeStatus{
		Name:          m.Name,
		Ready:         m.NATSConnected && !m.Drained,
		Draining:      m.Drained,
		Connections:   int(m.ConnectionsActive),
		NATSConnected: m.NATSConnected,
		Pool:          pool,
		Metrics:       metrics,
		NATS:          &MQTTNATSDiag{Connection: natsConn},
	}

	return MQTTBridgeInstance{
		ServerID:       m.NATSServerID,
		ServerName:     m.NATSServerName,
		ConfiguredName: m.Name,
		Reachable:      true,
		Status:         status,
	}
}

func boolToInt64(b bool) int64 {
	if b {
		return 1
	}
	return 0
}

// connectNATS builds a NATS connection from the cluster's NATSConnConfig.
// RetryOnFailedConnect and MaxReconnects(-1) ensure the call returns immediately
// even when the server is temporarily unavailable, and retries indefinitely.
func connectNATS(cfg *config.NATSConnConfig) (*nats.Conn, error) {
	if len(cfg.URLs) == 0 {
		return nil, fmt.Errorf("nats_conn: at least one URL is required")
	}

	opts := []nats.Option{
		nats.RetryOnFailedConnect(true),
		nats.MaxReconnects(-1),
	}

	switch {
	case cfg.Username != "":
		opts = append(opts, nats.UserInfo(cfg.Username, cfg.Password))
	case cfg.Token != "":
		opts = append(opts, nats.Token(cfg.Token))
	case cfg.NKey != "":
		opt, err := nats.NkeyOptionFromSeed(cfg.NKey)
		if err != nil {
			return nil, fmt.Errorf("nkey: %w", err)
		}
		opts = append(opts, opt)
	case cfg.CredsFile != "":
		opts = append(opts, nats.UserCredentials(cfg.CredsFile))
	}

	if cfg.TLS != nil {
		if cfg.TLS.Insecure {
			opts = append(opts, nats.Secure(&tls.Config{InsecureSkipVerify: true})) //nolint:gosec
		} else if cfg.TLS.CAFile != "" {
			opts = append(opts, nats.RootCAs(cfg.TLS.CAFile))
		}
	}

	return nats.Connect(strings.Join(cfg.URLs, ","), opts...)
}
