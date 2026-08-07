package collector

import (
	"time"

	"github.com/noodlebit/machmqtt-dashboard/internal/store"
)

// BuildMetricSample assembles a time-series sample for a cluster from the given
// overview plus the latest snapshot and discovered MQTT bridges. It returns nil
// when there is no overview yet (nothing to record). The caller passes the
// already-computed overview (to avoid recomputing it) and the sample timestamp.
//
// This lives here, next to the data it reads, rather than inline in main's
// onChange closure.
func (m *Manager) BuildMetricSample(clusterID string, ts time.Time, overview *Overview) *store.MetricSample {
	if overview == nil {
		return nil
	}

	sample := store.MetricSample{
		Timestamp:       ts,
		Env:             clusterID,
		ServerCount:     overview.ServerCount,
		HealthyCount:    overview.HealthyCount,
		ConnectionCount: overview.ConnectionCount,
		InMsgsRate:      overview.InMsgsRate,
		OutMsgsRate:     overview.OutMsgsRate,
		InBytesRate:     overview.InBytesRate,
		OutBytesRate:    overview.OutBytesRate,
		Subscriptions:   overview.Subscriptions,
	}

	// Per-server metrics from the snapshot.
	if snap := m.Snapshot(clusterID); snap != nil {
		for id, v := range snap.Varz {
			sm := store.ServerMetricSample{
				ServerID:      id,
				Connections:   v.Connections,
				InMsgs:        v.InMsgs,
				OutMsgs:       v.OutMsgs,
				InBytes:       v.InBytes,
				OutBytes:      v.OutBytes,
				CPU:           v.CPU,
				Mem:           v.Mem,
				Subscriptions: v.Subscriptions,
				SlowConsumers: v.SlowConsumers,
				Routes:        v.Routes,
				LeafNodes:     v.Leafs,
				Healthy:       true,
			}
			if h, ok := snap.Health[id]; ok {
				sm.Healthy = h.Status == "ok"
			}
			if r, ok := snap.Rates[id]; ok {
				sm.InMsgsRate = r.InMsgsRate
				sm.OutMsgsRate = r.OutMsgsRate
				sm.InBytesRate = r.InBytesRate
				sm.OutBytesRate = r.OutBytesRate
			}
			sample.Servers = append(sample.Servers, sm)
		}
	}

	// Per-MQTT bridge metrics.
	for _, b := range m.MQTTBridges(clusterID) {
		bm := store.MQTTBridgeMetricSample{
			BridgeID:     b.ConfiguredName,
			InMsgsRate:   b.InMsgsRate,
			OutMsgsRate:  b.OutMsgsRate,
			InBytesRate:  b.InBytesRate,
			OutBytesRate: b.OutBytesRate,
		}
		if bm.BridgeID == "" {
			bm.BridgeID = b.IP
		}
		if b.Status != nil && b.Status.Metrics != nil {
			mx := b.Status.Metrics
			bm.ConnectionsActive = mx.ConnectionsActive
			bm.MsgsRecvQoS0 = mx.MsgsRecvQoS0
			bm.MsgsRecvQoS1 = mx.MsgsRecvQoS1
			bm.MsgsSentQoS0 = mx.MsgsSentQoS0
			bm.MsgsSentQoS1 = mx.MsgsSentQoS1
			bm.MsgsRecvQoS2 = mx.MsgsRecvQoS2
			bm.MsgsSentQoS2 = mx.MsgsSentQoS2
			bm.SessionWriteBehindDepth = mx.SessionWriteBehindDepth
			bm.StalledConsumers = mx.StalledConsumers
			if mx.ConsumerPendingMessages >= 0 {
				v := mx.ConsumerPendingMessages
				bm.ConsumerPendingMessages = &v
			}
			// Trend-line gauges.
			bm.SocketsOpen = mx.SocketsOpen
			bm.InflightOutMessages = mx.InflightOutMessages
			bm.OpQueueDepth = mx.OpQueueDepth
			bm.OpSuspendedConns = mx.OpSuspendedConns
			bm.WorkerPoolQueueDepth = mx.WorkerPoolQueueDepth
			bm.PoolSlotConnected = mx.PoolSlotConnected
			bm.RetainedMessages = mx.RetainedMessages
			bm.SubscriptionsActive = mx.SubscriptionsActive
			bm.GoHeapInuseBytes = mx.GoHeapInuseBytes
			bm.GoGoroutines = mx.GoGoroutines
			bm.ScramSessionsActive = mx.ScramSessionsActive
		}
		sample.MQTTBridges = append(sample.MQTTBridges, bm)
	}

	return &sample
}
