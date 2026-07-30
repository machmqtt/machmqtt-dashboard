package collector

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"log/slog"
	"runtime/debug"
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
	// NATS-side message and byte rates, derived from the delta between successive
	// metrics publishes. Push messages carry only cumulative counters, so the
	// subscriber computes the rates the Fleet view and per-bridge trend charts
	// display.
	inRate       float64
	outRate      float64
	inBytesRate  float64
	outBytesRate float64
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
	_, err = subscribeWithRetry(ctx, nc, subject, log, defaultSubscribeRetries,
		guardedMsgHandler(log, subject, func(msg *nats.Msg) {
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
			now := time.Now()
			cb := &cachedBridge{msg: &m, receivedAt: now}
			s.mu.Lock()
			// Derive NATS-side msg and byte rates from the counter delta vs the prior
			// publish. A drained instance is cached like any other: draining keeps
			// existing sessions alive, so it stays on the fleet (as Draining, with its
			// live counters) until its publishes stop and the TTL sweeper removes it.
			if prev, ok := s.bridges[m.InstanceName]; ok && prev.msg != nil {
				dt := now.Sub(prev.receivedAt).Seconds()
				if dt > 0 {
					if prev.msg.Metrics != nil && m.Metrics != nil {
						cb.inRate = nonNegRate(natsInTotal(m.Metrics), natsInTotal(prev.msg.Metrics), dt)
						cb.outRate = nonNegRate(natsOutTotal(m.Metrics), natsOutTotal(prev.msg.Metrics), dt)
					}
					curTo, curFrom := natsByteTotals(&m)
					prevTo, prevFrom := natsByteTotals(prev.msg)
					cb.inBytesRate = nonNegRate(curTo, prevTo, dt)
					cb.outBytesRate = nonNegRate(curFrom, prevFrom, dt)
				} else {
					cb.inRate, cb.outRate = prev.inRate, prev.outRate
					cb.inBytesRate, cb.outBytesRate = prev.inBytesRate, prev.outBytesRate
				}
			}
			// Resolve the envelope fixups before the message becomes visible: a
			// cached message must be immutable once published, because Bridges()
			// hands its *MQTTMetrics to readers that marshal it concurrently.
			normalizeBridgeMsg(&m)
			s.bridges[m.InstanceName] = cb
			s.mu.Unlock()
		}))
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
		inst.InBytesRate = cb.inBytesRate
		inst.OutBytesRate = cb.outBytesRate
		inst.LastSeen = cb.receivedAt
		out = append(out, inst)
	}
	return out
}

// BridgeCount reports how many bridges the push cache currently holds. Callers
// that only need "is push data flowing" use this instead of Bridges(), which
// converts and copies every cached message.
func (s *MQTTSubscriber) BridgeCount() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.bridges)
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

// natsByteTotals sums the pool slots' cumulative NATS-side byte counters.
// toNATS is what the bridge wrote to NATS and fromNATS what it read back, which
// is the direction pairing the connz-scan path uses: a NATS server reports a
// bridge connection's in_bytes for what the bridge published to it. Both paths
// write the same InBytesRate/OutBytesRate fields and the same stored time
// series, so In must stay "bridge → NATS" on both or a bridge's history swaps
// direction whenever the source switches. The same pairing holds for the message
// rates: natsInTotal is server_published (bridge → NATS).
//
// A slot set can change between publishes, so the sum can regress; the callers'
// nonNegRate clamp turns that into 0 rather than a negative rate.
func natsByteTotals(m *BridgeMetricsMsg) (toNATS, fromNATS int64) {
	for _, sl := range m.Pool.Slots {
		toNATS += sl.OutBytes
		fromNATS += sl.InBytes
	}
	return toNATS, fromNATS
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
	// The broker embeds drained inside the metrics object and older publishers
	// carry it only at the envelope level, so either one marks the instance as
	// draining. A draining instance keeps serving its existing sessions, so it
	// stays listed with its live counters — it is just not Ready.
	drained := m.Drained || metrics.Drained != 0

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
		Ready:         m.NATS.Connected && !drained,
		Draining:      drained,
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

// guardedMsgHandler wraps a NATS subscription callback so a panic while handling
// one message is contained instead of killing the process: the callback runs on
// the client's dispatch goroutine, where an unrecovered panic is fatal and would
// take down monitoring for every cluster. Each recovered panic is logged with a
// running count for the subscription, so a poison payload is visible rather than
// silently swallowed.
func guardedMsgHandler(log *slog.Logger, subject string, h nats.MsgHandler) nats.MsgHandler {
	var panics atomic.Int64
	return func(msg *nats.Msg) {
		defer func() {
			if r := recover(); r != nil {
				log.Error("nats subscription callback panicked; message dropped",
					"subject", subject, "panics", panics.Add(1),
					"panic", r, "stack", string(debug.Stack()))
			}
		}()
		h(msg)
	}
}

// defaultSubscribeRetries are the bounded, increasing waits between failed
// subscribe attempts (one attempt, then one per delay). A subscribe can fail
// transiently while the client is re-establishing its connection, and the caller
// is a long-lived collector goroutine: giving up on the first error would leave
// that cluster permanently without push data until a restart. Passed in rather
// than read from a global so tests can shorten it without racing.
var defaultSubscribeRetries = []time.Duration{500 * time.Millisecond, 2 * time.Second, 5 * time.Second}

// subscribeWithRetry subscribes to subject, retrying on failure with the given
// waits and aborting as soon as ctx is done. It returns the last error when every
// attempt failed.
func subscribeWithRetry(ctx context.Context, nc *nats.Conn, subject string, log *slog.Logger, delays []time.Duration, h nats.MsgHandler) (*nats.Subscription, error) {
	var err error
	for attempt := 0; ; attempt++ {
		var sub *nats.Subscription
		sub, err = nc.Subscribe(subject, h)
		if err == nil {
			return sub, nil
		}
		if attempt >= len(delays) {
			return nil, err
		}
		log.Warn("nats subscribe failed; retrying", "subject", subject,
			"attempt", attempt+1, "retry_in", delays[attempt], "err", err)
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(delays[attempt]):
		}
	}
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
