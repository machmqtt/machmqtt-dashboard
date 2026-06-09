package collector

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	nats "github.com/nats-io/nats.go"
	"github.com/noodlebit/machmqtt-dashboard/internal/config"
)

// bridgeTTL is how long a bridge's last-seen entry survives without a new
// publish before it is considered gone. Set to 3× the expected publish interval.
const bridgeTTL = 45 * time.Second

// BridgeMetricsMsg is the JSON object published by MachMQTT bridges to
// <prefix>.metrics.<instance_name>. Schema version v=1.
// Field names and nesting match the publisher exactly; both sides must agree.
type BridgeMetricsMsg struct {
	V            int              `json:"v"`
	PublishedAt  time.Time        `json:"published_at"`
	InstanceID   string           `json:"instance_id"`   // ephemeral, matches cluster heartbeat id
	InstanceName string           `json:"instance_name"` // stable across restarts — dashboard's historical key
	Version      string           `json:"version,omitempty"`
	Drained      bool             `json:"drained,omitempty"`
	Connections  BridgeMsgConns   `json:"connections"`
	Messages     BridgeMsgMsgs    `json:"messages"`
	NATS         BridgeMsgNATS    `json:"nats"`
	Pool         BridgePool       `json:"pool"`

	ConsumerPendingMessages int64            `json:"consumer_pending_messages"`
	StalledConsumers        int64            `json:"stalled_consumers"`
	SessionWriteBehindDepth int64            `json:"session_write_behind_depth"`
	Account                 *BridgeMsgAccount `json:"account,omitempty"`
}

type BridgeMsgConns struct {
	Active   int64 `json:"active"`
	Total    int64 `json:"total"`
	Rejected int64 `json:"rejected"`
}

type BridgeMsgMsgs struct {
	RecvQoS0    int64 `json:"recv_qos0"`
	RecvQoS1    int64 `json:"recv_qos1"`
	RecvQoS2    int64 `json:"recv_qos2"`
	SentQoS0    int64 `json:"sent_qos0"`
	SentQoS1    int64 `json:"sent_qos1"`
	SentQoS2    int64 `json:"sent_qos2"`
	Redelivered int64 `json:"redelivered"`
}

type BridgeMsgNATS struct {
	Connected    bool     `json:"connected"`
	ServerID     string   `json:"server_id"`
	ServerName   string   `json:"server_name"`
	URL          string   `json:"url"`
	Servers      []string `json:"servers,omitempty"`
	RTT          string   `json:"rtt,omitempty"`
	Reconnects   uint64   `json:"reconnects"`
	Disconnects  int64    `json:"disconnects"`
	SlowConsumer int64    `json:"slow_consumer"`
}

// BridgeMsgAccount carries JetStream account info. Absent when JetStream is disabled.
type BridgeMsgAccount struct {
	Domain    string `json:"domain,omitempty"`
	Memory    uint64 `json:"memory_bytes"`
	Store     uint64 `json:"store_bytes"`
	Streams   int    `json:"streams"`
	Consumers int    `json:"consumers"`
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
// Cache is keyed by instance_name (stable across restarts), not instance_id.
type MQTTSubscriber struct {
	mu      sync.RWMutex
	bridges map[string]*cachedBridge // keyed by instance_name
	log     *slog.Logger             // optional; nil falls back to slog.Default()
}

func newMQTTSubscriber() *MQTTSubscriber {
	return &MQTTSubscriber{bridges: make(map[string]*cachedBridge)}
}

func (s *MQTTSubscriber) logger() *slog.Logger {
	if s.log != nil {
		return s.log
	}
	return slog.Default()
}

// run connects to NATS, subscribes to <prefix>.metrics.>, and maintains the
// bridge cache until ctx is cancelled. Intended to be started as a goroutine.
func (s *MQTTSubscriber) run(ctx context.Context, cfg *config.NATSConnConfig) {
	log := s.logger()
	prefix := cfg.SubjectPrefixOrDefault()
	subject := prefix + ".metrics.>"
	log.Info("mqtt metrics subscriber starting", "urls", cfg.URLs, "subject", subject)

	nc, err := connectNATS(cfg, log.With("conn", "mqtt-metrics"))
	if err != nil {
		log.Error("mqtt metrics subscriber: NATS connect failed", "err", err)
		return
	}
	defer nc.Close()

	var received atomic.Int64
	_, err = nc.Subscribe(subject, func(msg *nats.Msg) {
		var m BridgeMetricsMsg
		if json.Unmarshal(msg.Data, &m) != nil || m.InstanceName == "" {
			return
		}
		if received.Add(1) == 1 {
			log.Info("mqtt metrics subscriber: receiving bridge metrics", "instance", m.InstanceName)
		}
		s.mu.Lock()
		if m.Drained {
			delete(s.bridges, m.InstanceName)
		} else {
			s.bridges[m.InstanceName] = &cachedBridge{msg: &m, receivedAt: time.Now()}
		}
		s.mu.Unlock()
	})
	if err != nil {
		log.Error("mqtt metrics subscriber: subscribe failed", "subject", subject, "err", err)
		return
	}

	// Diagnostic: if no bridge metrics arrive shortly after connecting, the
	// MachMQTT bridges are not publishing to this subject. The MachMQTT Fleet
	// pages stay empty until they do.
	go func() {
		t := time.NewTimer(20 * time.Second)
		defer t.Stop()
		select {
		case <-ctx.Done():
		case <-t.C:
			if received.Load() == 0 {
				log.Warn("mqtt metrics subscriber: no bridge metrics received within 20s — no MachMQTT bridge is publishing to "+subject+"; the MachMQTT Fleet pages will stay empty until bridges publish metrics (or disable the NATS connection to use connz-scan discovery instead)")
			}
		}
	}()

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
	for name, cb := range s.bridges {
		if cb.receivedAt.Before(deadline) {
			delete(s.bridges, name)
		}
	}
}

// Bridges returns the current live bridge instances from the push cache.
func (s *MQTTSubscriber) Bridges() []MQTTBridgeInstance {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]MQTTBridgeInstance, 0, len(s.bridges))
	for name, cb := range s.bridges {
		out = append(out, bridgeMsgToInstance(name, cb.msg))
	}
	return out
}

// bridgeMsgToInstance converts a wire message into the MQTTBridgeInstance shape.
// name is instance_name (the stable cache key and store BridgeID).
func bridgeMsgToInstance(name string, m *BridgeMetricsMsg) MQTTBridgeInstance {
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
		ConnectionsActive:       m.Connections.Active,
		ConnectionsTotal:        m.Connections.Total,
		ConnectionsRejected:     m.Connections.Rejected,
		MsgsRecvQoS0:            m.Messages.RecvQoS0,
		MsgsRecvQoS1:            m.Messages.RecvQoS1,
		MsgsRecvQoS2:            m.Messages.RecvQoS2,
		MsgsSentQoS0:            m.Messages.SentQoS0,
		MsgsSentQoS1:            m.Messages.SentQoS1,
		MsgsSentQoS2:            m.Messages.SentQoS2,
		MsgsRedelivered:         m.Messages.Redelivered,
		NATSDisconnects:         m.NATS.Disconnects,
		NATSReconnects:          int64(m.NATS.Reconnects),
		NATSSlowConsumer:        m.NATS.SlowConsumer,
		ConsumerPendingMessages: m.ConsumerPendingMessages,
		StalledConsumers:        m.StalledConsumers,
		SessionWriteBehindDepth: m.SessionWriteBehindDepth,
		InstanceID:              m.InstanceID,
		Drained:                 boolToInt64(m.Drained),
	}

	natsConn := MQTTNATSConnection{
		Connected:  m.NATS.Connected,
		URL:        m.NATS.URL,
		Servers:    m.NATS.Servers,
		ServerID:   m.NATS.ServerID,
		ServerName: m.NATS.ServerName,
		RTT:        m.NATS.RTT,
		Reconnects: m.NATS.Reconnects,
	}

	natsDiag := &MQTTNATSDiag{Connection: natsConn}
	if m.Account != nil {
		natsDiag.Account = &MQTTNATSAccount{
			Domain:    m.Account.Domain,
			Memory:    m.Account.Memory,
			Store:     m.Account.Store,
			Streams:   m.Account.Streams,
			Consumers: m.Account.Consumers,
		}
	}

	status := &MQTTBridgeStatus{
		Name:          name,
		Ready:         m.NATS.Connected && !m.Drained,
		Draining:      m.Drained,
		Connections:   int(m.Connections.Active),
		NATSConnected: m.NATS.Connected,
		Pool:          pool,
		Metrics:       metrics,
		NATS:          natsDiag,
	}

	return MQTTBridgeInstance{
		ServerID:       m.NATS.ServerID,
		ServerName:     m.NATS.ServerName,
		ConfiguredName: name, // instance_name is the stable BridgeID for the store
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
// log, when non-nil, receives connection lifecycle events (connect, disconnect,
// reconnect, async errors) so the Server Logs page reflects NATS link health.
func connectNATS(cfg *config.NATSConnConfig, log *slog.Logger) (*nats.Conn, error) {
	if len(cfg.URLs) == 0 {
		return nil, fmt.Errorf("nats_conn: at least one URL is required")
	}
	if log == nil {
		log = slog.Default()
	}

	opts := []nats.Option{
		nats.RetryOnFailedConnect(true),
		nats.MaxReconnects(-1),
		nats.ConnectHandler(func(nc *nats.Conn) {
			log.Info("nats connected", "url", nc.ConnectedUrl())
		}),
		nats.DisconnectErrHandler(func(_ *nats.Conn, err error) {
			if err != nil {
				log.Warn("nats disconnected", "err", err)
			} else {
				log.Info("nats disconnected")
			}
		}),
		nats.ReconnectHandler(func(nc *nats.Conn) {
			log.Info("nats reconnected", "url", nc.ConnectedUrl())
		}),
		nats.ErrorHandler(func(_ *nats.Conn, sub *nats.Subscription, err error) {
			subject := ""
			if sub != nil {
				subject = sub.Subject
			}
			log.Warn("nats async error", "subject", subject, "err", err)
		}),
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
