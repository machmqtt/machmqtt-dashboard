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

// defaultBridgeTTL is how long a bridge's last-seen entry survives without a new
// publish before it is considered gone. Set to 3× the expected publish interval.
const defaultBridgeTTL = 45 * time.Second

// defaultDiagnosticDelay is how long the $SYS / metrics subscribers wait for the
// first message before warning that none arrived.
const defaultDiagnosticDelay = 20 * time.Second

// bridgeMetricsSchemaV is the BridgeMetricsMsg schema version this build
// understands. Messages with a higher "v" are skipped (see the subscriber).
const bridgeMetricsSchemaV = 1

// BridgeMetricsMsg is the JSON object published by MachMQTT bridges to
// <prefix>.metrics.<instance_name>. Schema version v=1.
// Field names and nesting match the publisher exactly; both sides must agree.
type BridgeMetricsMsg struct {
	V int `json:"v"`
	// PublishedAt is the publisher's send time. Staleness is tracked via the
	// receive time (see cachedBridge.receivedAt); this is retained to document
	// the wire schema and is available for clock-skew diagnostics.
	PublishedAt  time.Time `json:"published_at"`
	InstanceID   string    `json:"instance_id"`   // ephemeral, matches cluster heartbeat id
	InstanceName string    `json:"instance_name"` // stable across restarts — dashboard's historical key
	Version      string    `json:"version,omitempty"`
	Drained      bool      `json:"drained,omitempty"`

	// Metrics carries the full MQTTMetrics counter set (the same struct the HTTP
	// /metrics parser fills). The broker now embeds instance_id and drained inside
	// this object too; normalizeBridgeMsg prefers those when present.
	Metrics *MQTTMetrics `json:"metrics,omitempty"`

	// NATS, Pool, and Account feed the connection/pool/JetStream diagnostics in
	// Status — these are NOT part of MQTTMetrics.
	NATS    BridgeMsgNATS     `json:"nats"`
	Pool    BridgePool        `json:"pool"`
	Account *BridgeMsgAccount `json:"account,omitempty"`
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
	Index         int   `json:"index"`
	Connected     bool  `json:"connected"`
	SubCount      int64 `json:"sub_count"`
	PubCount      int64 `json:"pub_count"`
	FlushCount    int64 `json:"flush_count"`
	BufferedBytes int64 `json:"buffered_bytes"`
	OutMsgs       int64 `json:"out_msgs"`
	InMsgs        int64 `json:"in_msgs"`
	OutBytes      int64 `json:"out_bytes"`
	InBytes       int64 `json:"in_bytes"`
	Reconnects    int64 `json:"reconnects"`
}

type cachedBridge struct {
	msg        *BridgeMetricsMsg
	receivedAt time.Time
	// NATS-side message rates, derived from the delta between successive metrics
	// publishes. Push messages carry only cumulative counters, so the subscriber
	// computes the rates the Fleet view and per-bridge trend charts display.
	inRate  float64
	outRate float64
}

// MQTTSubscriber maintains a live TTL-keyed cache of bridge metrics received
// via NATS pub/sub. It is the push-based replacement for connz-scan discovery.
// Cache is keyed by instance_name (stable across restarts), not instance_id.
type MQTTSubscriber struct {
	mu      sync.RWMutex
	bridges map[string]*cachedBridge // keyed by instance_name
	nc      *nats.Conn               // current NATS connection; nil when disconnected
	log     *slog.Logger             // optional; nil falls back to slog.Default()
	// ttl and diagDelay default to the package defaults; tests set them shorter
	// to exercise the sweep and no-data-diagnostic paths without a global race.
	ttl       time.Duration
	diagDelay time.Duration
}

func newMQTTSubscriber() *MQTTSubscriber {
	return &MQTTSubscriber{
		bridges:   make(map[string]*cachedBridge),
		ttl:       defaultBridgeTTL,
		diagDelay: defaultDiagnosticDelay,
	}
}

func (s *MQTTSubscriber) logger() *slog.Logger {
	if s.log != nil {
		return s.log
	}
	return slog.Default()
}

// Connected reports whether the metrics subscriber's NATS connection is up.
// The client retries indefinitely (see connectNATS), so a non-nil conn only
// means "configured": the link must also be currently established, or health
// would report the push path up while NATS is unreachable.
func (s *MQTTSubscriber) Connected() bool {
	s.mu.RLock()
	nc := s.nc
	s.mu.RUnlock()
	return nc != nil && nc.IsConnected()
}

// run connects to NATS, subscribes to <prefix>.metrics.>, and maintains the
// bridge cache until ctx is cancelled. Intended to be started as a goroutine.
func (s *MQTTSubscriber) run(ctx context.Context, cfg *config.NATSConnConfig) {
	log := s.logger()
	prefix := cfg.SubjectPrefixOrDefault()
	subject := prefix + ".metrics.>"
	log.Info("mqtt metrics subscriber starting", "urls", redactURLCredsAll(cfg.URLs), "subject", subject)

	nc, err := connectNATS(cfg, log.With("conn", "mqtt-metrics"))
	if err != nil {
		log.Error("mqtt metrics subscriber: NATS connect failed", "err", err)
		return
	}
	defer nc.Close()
	s.mu.Lock()
	s.nc = nc
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		s.nc = nil
		s.mu.Unlock()
	}()

	var received atomic.Int64
	var warnedNewerSchema atomic.Bool
	var warnedBadMsg atomic.Bool
	_, err = nc.Subscribe(subject, func(msg *nats.Msg) {
		var m BridgeMetricsMsg
		if err := json.Unmarshal(msg.Data, &m); err != nil {
			// Warn once so a schema mismatch is visible without per-message spam.
			if warnedBadMsg.CompareAndSwap(false, true) {
				log.Debug("mqtt metrics subscriber: ignoring malformed bridge message", "err", err)
			}
			return
		}
		if m.InstanceName == "" {
			return
		}
		// Accept the current schema and legacy publishers that omit "v" (v=0);
		// skip messages from a newer, possibly incompatible schema rather than
		// misinterpreting their fields. Warn once so the mismatch is visible.
		if m.V > bridgeMetricsSchemaV {
			if warnedNewerSchema.CompareAndSwap(false, true) {
				log.Warn("mqtt metrics subscriber: ignoring bridge message with newer schema version — upgrade the dashboard",
					"v", m.V, "supported", bridgeMetricsSchemaV, "instance", m.InstanceName)
			}
			return
		}
		if received.Add(1) == 1 {
			log.Info("mqtt metrics subscriber: receiving bridge metrics", "instance", m.InstanceName)
		}
		s.mu.Lock()
		if m.Drained {
			delete(s.bridges, m.InstanceName)
		} else {
			now := time.Now()
			cb := &cachedBridge{msg: &m, receivedAt: now}
			// Derive NATS-side msg rates from the counter delta vs the prior publish.
			if prev, ok := s.bridges[m.InstanceName]; ok && prev.msg != nil && prev.msg.Metrics != nil && m.Metrics != nil {
				dt := now.Sub(prev.receivedAt).Seconds()
				if dt > 0 {
					cb.inRate = nonNegRate(natsInTotal(m.Metrics), natsInTotal(prev.msg.Metrics), dt)
					cb.outRate = nonNegRate(natsOutTotal(m.Metrics), natsOutTotal(prev.msg.Metrics), dt)
				} else {
					cb.inRate, cb.outRate = prev.inRate, prev.outRate
				}
			}
			// Resolve the envelope fixups before the message becomes visible: a
			// cached message must be immutable once published, because Bridges()
			// hands its *MQTTMetrics to readers that marshal it concurrently.
			normalizeBridgeMsg(&m)
			s.bridges[m.InstanceName] = cb
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
		t := time.NewTimer(s.diagDelay)
		defer t.Stop()
		select {
		case <-ctx.Done():
		case <-t.C:
			if received.Load() == 0 {
				log.Warn("mqtt metrics subscriber: no bridge metrics received within 20s — no MachMQTT bridge is publishing to " + subject + "; the MachMQTT Fleet pages will stay empty until bridges publish metrics (or disable the NATS connection to use connz-scan discovery instead)")
			}
		}
	}()

	sweeper := time.NewTicker(s.ttl / 3)
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
	deadline := time.Now().Add(-s.ttl)
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
		inst := bridgeMsgToInstance(name, cb.msg)
		inst.InMsgsRate = cb.inRate
		inst.OutMsgsRate = cb.outRate
		out = append(out, inst)
	}
	return out
}

// natsInTotal / natsOutTotal are the bridge's cumulative NATS-side message
// counts: published-to-NATS and consumed-from-NATS across QoS levels. Their
// deltas drive the bridge's InMsgsRate/OutMsgsRate.
func natsInTotal(m *MQTTMetrics) int64 {
	return m.ServerPublishedQoS0 + m.ServerPublishedQoS1 + m.ServerPublishedQoS2
}
func natsOutTotal(m *MQTTMetrics) int64 {
	return m.ServerConsumedQoS0 + m.ServerConsumedQoS1 + m.ServerConsumedQoS2
}

func nonNegRate(cur, prev int64, dt float64) float64 {
	if dt <= 0 || cur < prev {
		return 0
	}
	return float64(cur-prev) / dt
}

// bridgeMetrics resolves the counter set for a message without writing through
// it. The full set arrives inside m.Metrics, which the subscriber normalizes at
// ingest (see normalizeBridgeMsg) — mutating it here would race with readers
// holding an earlier Bridges() result. When absent, a fresh struct carries the
// JS-absent sentinel the HTTP parser uses, plus the envelope's instance_id and
// drained, so the rest of the mapping is nil-safe.
func bridgeMetrics(m *BridgeMetricsMsg) *MQTTMetrics {
	if m.Metrics != nil {
		return m.Metrics
	}
	return &MQTTMetrics{
		ConsumerPendingMessages: -1,
		InstanceID:              m.InstanceID,
		Drained:                 boolToInt64(m.Drained),
	}
}

// normalizeBridgeMsg folds the envelope's instance_id and drained into the
// embedded metrics object. The broker embeds both inside Metrics, so those win;
// the top-level wire fields only fill them when unset. Call this once, before
// the message is cached — never afterwards.
func normalizeBridgeMsg(m *BridgeMetricsMsg) {
	if m.Metrics == nil {
		m.Metrics = bridgeMetrics(m)
		return
	}
	if m.Metrics.InstanceID == "" {
		m.Metrics.InstanceID = m.InstanceID
	}
	if m.Metrics.Drained == 0 {
		m.Metrics.Drained = boolToInt64(m.Drained)
	}
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
			Index:         sl.Index,
			Connected:     sl.Connected,
			SubCount:      sl.SubCount,
			PubCount:      sl.PubCount,
			FlushCount:    sl.FlushCount,
			BufferedBytes: sl.BufferedBytes,
			OutMsgs:       sl.OutMsgs,
			InMsgs:        sl.InMsgs,
			OutBytes:      sl.OutBytes,
			InBytes:       sl.InBytes,
			Reconnects:    sl.Reconnects,
		})
	}

	metrics := bridgeMetrics(m)

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
		Connections:   int(metrics.ConnectionsActive),
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
