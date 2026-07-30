package store

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"strings"
	"sync/atomic"
	"time"
)

// MetricSample holds one poll's worth of data for metrics storage.
type MetricSample struct {
	Timestamp time.Time
	Env       string

	// Environment-level aggregates.
	ServerCount     int
	HealthyCount    int
	ConnectionCount int
	InMsgsRate      float64
	OutMsgsRate     float64
	InBytesRate     float64
	OutBytesRate    float64
	Subscriptions   uint32

	// Per-server metrics.
	Servers []ServerMetricSample

	// Per-MQTT bridge metrics.
	MQTTBridges []MQTTBridgeMetricSample
}

type ServerMetricSample struct {
	ServerID      string
	Connections   int
	InMsgs        int64
	OutMsgs       int64
	InBytes       int64
	OutBytes      int64
	CPU           float64
	Mem           int64
	Subscriptions uint32
	SlowConsumers int64
	Routes        int
	LeafNodes     int
	InMsgsRate    float64
	OutMsgsRate   float64
	InBytesRate   float64
	OutBytesRate  float64
	Healthy       bool
}

type MQTTBridgeMetricSample struct {
	BridgeID                string
	ConnectionsActive       int64
	InMsgsRate              float64
	OutMsgsRate             float64
	InBytesRate             float64
	OutBytesRate            float64
	MsgsRecvQoS0            int64
	MsgsRecvQoS1            int64
	MsgsSentQoS0            int64
	MsgsSentQoS1            int64
	MsgsRecvQoS2            int64
	MsgsSentQoS2            int64
	SessionWriteBehindDepth int64
	// ConsumerPendingMessages is nil when JetStream is unavailable (metric absent);
	// stored as NULL in SQLite so AVG() skips absent-JS samples correctly.
	ConsumerPendingMessages *int64
	StalledConsumers        int64

	// Trend-line gauges.
	SocketsOpen          int64
	InflightOutMessages  int64
	OpQueueDepth         int64
	OpSuspendedConns     int64
	WorkerPoolQueueDepth int64
	PoolSlotConnected    int64
	RetainedMessages     int64
	SubscriptionsActive  int64
	GoHeapInuseBytes     int64
	GoGoroutines         int64
	ScramSessionsActive  int64
}

// MetricPoint represents a single time-series data point returned by queries.
type MetricPoint map[string]any

// MetricsWriter buffers metric samples and writes them to SQLite in batches.
type MetricsWriter struct {
	db           *sql.DB
	ch           chan MetricSample
	log          *slog.Logger
	retention    time.Duration
	dropped      atomic.Uint64 // dropped since the last periodic report (reset by Run)
	droppedTotal atomic.Uint64 // cumulative dropped, never reset (for /api/admin/health)
	pruning      atomic.Bool   // guards against overlapping retention prunes
}

// Dropped returns the cumulative number of metric samples dropped because the
// writer buffer was full, since process start.
func (w *MetricsWriter) Dropped() uint64 { return w.droppedTotal.Load() }

// NewMetricsWriter creates a new metrics writer that keeps samples for the given
// retention (<=0 falls back to 24h). Call Run() to start the background goroutine.
// It reads the store's connection directly (same package) so the store need not
// expose its raw *sql.DB.
func NewMetricsWriter(s *Store, log *slog.Logger, retention time.Duration) *MetricsWriter {
	if retention <= 0 {
		retention = 24 * time.Hour
	}
	return &MetricsWriter{
		db:        s.db,
		ch:        make(chan MetricSample, 256),
		log:       log,
		retention: retention,
	}
}

// Submit sends a sample to the writer. Non-blocking; drops if buffer is full.
func (w *MetricsWriter) Submit(s MetricSample) {
	select {
	case w.ch <- s:
	default:
		// Drop sample — monitoring is best-effort. Counted and reported
		// periodically by Run so sustained loss isn't silent.
		w.dropped.Add(1)
		w.droppedTotal.Add(1)
	}
}

// Run starts the writer goroutine. Blocks until ctx is cancelled.
func (w *MetricsWriter) Run(ctx context.Context) {
	cleanup := time.NewTicker(10 * time.Minute)
	defer cleanup.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case s := <-w.ch:
			w.writeSample(s)
		case <-cleanup.C:
			// Prune on a separate goroutine (guarded so at most one runs at a
			// time) so a large or slow DELETE never blocks the sample-draining
			// loop above — otherwise the buffer would fill and live samples for
			// every cluster would be dropped while pruning ran.
			if w.pruning.CompareAndSwap(false, true) {
				go func() {
					defer w.pruning.Store(false)
					w.deleteOld()
				}()
			}
			if n := w.dropped.Swap(0); n > 0 {
				w.log.Warn("metrics samples dropped (writer buffer full)", "count", n, "window", "10m")
			}
		}
	}
}

func (w *MetricsWriter) writeSample(s MetricSample) {
	ts := s.Timestamp.Unix()

	tx, err := w.db.Begin()
	if err != nil {
		w.log.Warn("metrics tx begin", "err", err)
		return
	}
	defer tx.Rollback()

	// Insert env-level metrics.
	_, err = tx.Exec(`INSERT INTO env_metrics (ts, env, server_count, healthy_count, connection_count,
		in_msgs_rate, out_msgs_rate, in_bytes_rate, out_bytes_rate, subscriptions)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		ts, s.Env, s.ServerCount, s.HealthyCount, s.ConnectionCount,
		s.InMsgsRate, s.OutMsgsRate, s.InBytesRate, s.OutBytesRate, s.Subscriptions)
	if err != nil {
		w.log.Warn("metrics insert env", "err", err)
		return
	}

	// Insert per-server metrics.
	for _, srv := range s.Servers {
		healthy := 0
		if srv.Healthy {
			healthy = 1
		}
		_, err = tx.Exec(`INSERT INTO server_metrics (ts, env, server_id,
			connections, in_msgs, out_msgs, in_bytes, out_bytes,
			cpu, mem, subscriptions, slow_consumers, routes, leafnodes,
			in_msgs_rate, out_msgs_rate, in_bytes_rate, out_bytes_rate, healthy)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			ts, s.Env, srv.ServerID,
			srv.Connections, srv.InMsgs, srv.OutMsgs, srv.InBytes, srv.OutBytes,
			srv.CPU, srv.Mem, srv.Subscriptions, srv.SlowConsumers, srv.Routes, srv.LeafNodes,
			srv.InMsgsRate, srv.OutMsgsRate, srv.InBytesRate, srv.OutBytesRate, healthy)
		if err != nil {
			w.log.Warn("metrics insert server", "server", srv.ServerID, "err", err)
		}
	}

	// Insert per-MQTT bridge metrics.
	for _, b := range s.MQTTBridges {
		_, err = tx.Exec(`INSERT INTO mqtt_bridge_metrics (ts, env, bridge_id,
			connections_active, in_msgs_rate, out_msgs_rate, in_bytes_rate, out_bytes_rate,
			msgs_recv_qos0, msgs_recv_qos1, msgs_sent_qos0, msgs_sent_qos1,
			msgs_recv_qos2, msgs_sent_qos2,
			session_write_behind_depth, consumer_pending_messages, stalled_consumers,
			sockets_open, inflight_out_messages, op_queue_depth, op_suspended_conns,
			worker_pool_queue_depth, pool_slot_connected, retained_messages,
			subscriptions_active, go_heap_inuse_bytes, go_goroutines, scram_sessions_active)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			ts, s.Env, b.BridgeID,
			b.ConnectionsActive, b.InMsgsRate, b.OutMsgsRate, b.InBytesRate, b.OutBytesRate,
			b.MsgsRecvQoS0, b.MsgsRecvQoS1, b.MsgsSentQoS0, b.MsgsSentQoS1,
			b.MsgsRecvQoS2, b.MsgsSentQoS2,
			b.SessionWriteBehindDepth, b.ConsumerPendingMessages, b.StalledConsumers,
			b.SocketsOpen, b.InflightOutMessages, b.OpQueueDepth, b.OpSuspendedConns,
			b.WorkerPoolQueueDepth, b.PoolSlotConnected, b.RetainedMessages,
			b.SubscriptionsActive, b.GoHeapInuseBytes, b.GoGoroutines, b.ScramSessionsActive)
		if err != nil {
			w.log.Warn("metrics insert mqtt", "bridge", b.BridgeID, "err", err)
		}
	}

	if err := tx.Commit(); err != nil {
		w.log.Warn("metrics tx commit", "err", err)
	}
}

func (w *MetricsWriter) deleteOld() {
	cutoff := time.Now().Add(-w.retention).Unix()
	freed := false
	for _, table := range []string{"server_metrics", "env_metrics", "mqtt_bridge_metrics"} {
		res, err := w.db.Exec(fmt.Sprintf("DELETE FROM %s WHERE ts < ?", table), cutoff)
		if err != nil {
			w.log.Warn("metrics cleanup", "table", table, "err", err)
			continue
		}
		if n, _ := res.RowsAffected(); n > 0 {
			freed = true
		}
	}
	// Return the pages freed by the deletes to the OS. Incremental auto_vacuum
	// keeps them on a freelist (they'd otherwise be reused by future inserts, so
	// the file plateaus); reclaiming here shrinks the file after a large prune,
	// e.g. when retention is reduced. Only runs when rows were actually deleted.
	if freed {
		if _, err := w.db.Exec("PRAGMA incremental_vacuum"); err != nil {
			w.log.Warn("metrics incremental_vacuum", "err", err)
		}
	}
}

// buildMetricsQuery assembles the bucketed-average query shared by all three
// metric queries: a `(ts/step)*step` time bucket, an env+time-range predicate,
// an optional id (server_id/bridge_id) predicate, and GROUP BY/ORDER BY. The
// table, idCol, and aggCols fragments are built from constant literals in this
// package (never user input); env and idVal are bound as parameters.
func buildMetricsQuery(table, idCol, aggCols, env, idVal string, from, to, step int64) (string, []any) {
	q := "SELECT (ts / ? ) * ? AS bucket"
	if idCol != "" {
		q += ", " + idCol
	}
	q += ", " + aggCols + " FROM " + table + " WHERE env = ? AND ts >= ? AND ts <= ?"
	args := []any{step, step, env, from, to}
	if idCol != "" && idVal != "" {
		q += " AND " + idCol + " = ?"
		args = append(args, idVal)
	}
	q += " GROUP BY bucket"
	if idCol != "" {
		q += ", " + idCol
	}
	q += " ORDER BY bucket"
	return q, args
}

// mqttMetricCols lists the mqtt_bridge_metrics value columns exposed as time
// series, in the order QueryMQTTMetrics scans them. Both the per-bridge AVG
// fragment and the fleet-wide SUM wrapper are generated from this slice so the
// two can never drift out of sync. Constant literals, never user input.
var mqttMetricCols = []string{
	"connections_active",
	"in_msgs_rate", "out_msgs_rate", "in_bytes_rate", "out_bytes_rate",
	"msgs_recv_qos0", "msgs_recv_qos1", "msgs_sent_qos0", "msgs_sent_qos1",
	"msgs_recv_qos2", "msgs_sent_qos2",
	"session_write_behind_depth", "consumer_pending_messages", "stalled_consumers",
	"sockets_open", "inflight_out_messages", "op_queue_depth", "op_suspended_conns",
	"worker_pool_queue_depth", "pool_slot_connected", "retained_messages",
	"subscriptions_active", "go_heap_inuse_bytes", "go_goroutines", "scram_sessions_active",
}

// aggList renders `fn(col), ...` for the given columns. With alias, each term is
// aliased back to its column name so an enclosing aggregate can reference it.
func aggList(fn string, cols []string, alias bool) string {
	parts := make([]string, len(cols))
	for i, c := range cols {
		parts[i] = fn + "(" + c + ")"
		if alias {
			parts[i] += " AS " + c
		}
	}
	return strings.Join(parts, ", ")
}

// autoStep calculates a step size to return approximately targetPoints data points.
func autoStep(from, to int64, targetPoints int) int64 {
	duration := to - from
	if duration <= 0 {
		return 5
	}
	step := duration / int64(targetPoints)
	if step < 5 {
		step = 5
	}
	return step
}

// QueryEnvMetrics returns environment-level time series.
func (w *MetricsWriter) QueryEnvMetrics(ctx context.Context, env string, from, to, step int64) ([]MetricPoint, error) {
	if step <= 0 {
		step = autoStep(from, to, 200)
	}
	q, args := buildMetricsQuery("env_metrics", "", `AVG(server_count), AVG(healthy_count), AVG(connection_count),
		AVG(in_msgs_rate), AVG(out_msgs_rate), AVG(in_bytes_rate), AVG(out_bytes_rate),
		AVG(subscriptions)`, env, "", from, to, step)
	rows, err := w.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var points []MetricPoint
	for rows.Next() {
		var ts int64
		var serverCount, healthyCount, connCount float64
		var inMsgsRate, outMsgsRate, inBytesRate, outBytesRate float64
		var subs float64
		if err := rows.Scan(&ts, &serverCount, &healthyCount, &connCount,
			&inMsgsRate, &outMsgsRate, &inBytesRate, &outBytesRate, &subs); err != nil {
			return nil, err
		}
		points = append(points, MetricPoint{
			"ts":               ts,
			"server_count":     serverCount,
			"healthy_count":    healthyCount,
			"connection_count": connCount,
			"in_msgs_rate":     inMsgsRate,
			"out_msgs_rate":    outMsgsRate,
			"in_bytes_rate":    inBytesRate,
			"out_bytes_rate":   outBytesRate,
			"subscriptions":    subs,
		})
	}
	return points, rows.Err()
}

// QueryServerMetrics returns per-server time series.
func (w *MetricsWriter) QueryServerMetrics(ctx context.Context, env, serverID string, from, to, step int64) ([]MetricPoint, error) {
	if step <= 0 {
		step = autoStep(from, to, 200)
	}

	query, args := buildMetricsQuery("server_metrics", "server_id", `AVG(connections), AVG(cpu), AVG(mem),
		AVG(in_msgs_rate), AVG(out_msgs_rate), AVG(in_bytes_rate), AVG(out_bytes_rate),
		AVG(subscriptions), AVG(slow_consumers)`, env, serverID, from, to, step)

	rows, err := w.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var points []MetricPoint
	for rows.Next() {
		var ts int64
		var sid string
		var conns, cpu, mem float64
		var inMR, outMR, inBR, outBR float64
		var subs, slowC float64
		if err := rows.Scan(&ts, &sid, &conns, &cpu, &mem,
			&inMR, &outMR, &inBR, &outBR, &subs, &slowC); err != nil {
			return nil, err
		}
		points = append(points, MetricPoint{
			"ts":             ts,
			"server_id":      sid,
			"connections":    conns,
			"cpu":            cpu,
			"mem":            mem,
			"in_msgs_rate":   inMR,
			"out_msgs_rate":  outMR,
			"in_bytes_rate":  inBR,
			"out_bytes_rate": outBR,
			"subscriptions":  subs,
			"slow_consumers": slowC,
		})
	}
	return points, rows.Err()
}

// QueryMQTTMetrics returns a per-bridge time series when bridgeID is set, and a
// fleet-wide series (one point per bucket, values summed across bridges) when it
// is empty.
func (w *MetricsWriter) QueryMQTTMetrics(ctx context.Context, env, bridgeID string, from, to, step int64) ([]MetricPoint, error) {
	if step <= 0 {
		step = autoStep(from, to, 200)
	}

	query, args := buildMetricsQuery("mqtt_bridge_metrics", "bridge_id",
		aggList("AVG", mqttMetricCols, true), env, bridgeID, from, to, step)

	// With no bridge_id filter the inner query yields one row per (bucket,
	// bridge) — N rows sharing a timestamp, which a single-series chart draws as
	// a sawtooth between bridges. Sum the per-bridge bucket averages into one
	// fleet total per bucket instead. SUM skips NULLs, so a bridge that doesn't
	// report a metric doesn't drag the fleet value down, and a bucket where no
	// bridge reports it stays NULL (rendered as a gap, not a false zero).
	fleet := bridgeID == ""
	if fleet {
		query = "SELECT bucket, " + aggList("SUM", mqttMetricCols, false) +
			" FROM (" + query + ") GROUP BY bucket ORDER BY bucket"
	}

	rows, err := w.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var points []MetricPoint
	for rows.Next() {
		var ts int64
		var bid string
		var connActive float64
		var inMR, outMR, inBR, outBR float64
		var rQ0, rQ1, sQ0, sQ1 float64
		var rQ2, sQ2 float64
		var writeBehind, stalledC float64
		// consumer_pending_messages is NULL when JetStream was unavailable for the
		// entire bucket; AVG of NULLs returns NULL, so use a nullable scan target.
		var pendingMsg sql.NullFloat64
		// Trend-line gauges. These columns were added later, so pre-migration
		// buckets are entirely NULL → AVG returns NULL; scan as nullable and omit
		// the key when absent so the chart shows a gap rather than a false zero.
		var socketsOpen, inflightOut, opQ, opSusp, workerQ sql.NullFloat64
		var poolSlot, retained, subsActive, heap, goroutines, scram sql.NullFloat64
		// Fleet rows have no bridge_id column — they aggregate every bridge.
		dest := []any{&ts}
		if !fleet {
			dest = append(dest, &bid)
		}
		// Order must match mqttMetricCols.
		dest = append(dest, &connActive,
			&inMR, &outMR, &inBR, &outBR,
			&rQ0, &rQ1, &sQ0, &sQ1,
			&rQ2, &sQ2,
			&writeBehind, &pendingMsg, &stalledC,
			&socketsOpen, &inflightOut, &opQ, &opSusp,
			&workerQ, &poolSlot, &retained,
			&subsActive, &heap, &goroutines, &scram)
		if err := rows.Scan(dest...); err != nil {
			return nil, err
		}
		pt := MetricPoint{
			"ts":                         ts,
			"connections_active":         connActive,
			"in_msgs_rate":               inMR,
			"out_msgs_rate":              outMR,
			"in_bytes_rate":              inBR,
			"out_bytes_rate":             outBR,
			"msgs_recv_qos0":             rQ0,
			"msgs_recv_qos1":             rQ1,
			"msgs_sent_qos0":             sQ0,
			"msgs_sent_qos1":             sQ1,
			"msgs_recv_qos2":             rQ2,
			"msgs_sent_qos2":             sQ2,
			"session_write_behind_depth": writeBehind,
			"stalled_consumers":          stalledC,
		}
		if !fleet {
			pt["bridge_id"] = bid
		}
		if pendingMsg.Valid {
			pt["consumer_pending_messages"] = pendingMsg.Float64
		}
		for key, v := range map[string]sql.NullFloat64{
			"sockets_open":            socketsOpen,
			"inflight_out_messages":   inflightOut,
			"op_queue_depth":          opQ,
			"op_suspended_conns":      opSusp,
			"worker_pool_queue_depth": workerQ,
			"pool_slot_connected":     poolSlot,
			"retained_messages":       retained,
			"subscriptions_active":    subsActive,
			"go_heap_inuse_bytes":     heap,
			"go_goroutines":           goroutines,
			"scram_sessions_active":   scram,
		} {
			if v.Valid {
				pt[key] = v.Float64
			}
		}
		points = append(points, pt)
	}
	return points, rows.Err()
}
