package store

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"
)

func TestMetricsWriterPersistenceQueriesStatsAndRetention(t *testing.T) {
	s := testStore(t)
	w := NewMetricsWriter(s.DB(), slog.New(slog.NewTextHandler(io.Discard, nil)), time.Hour)
	now := time.Now().Truncate(time.Second)
	sample := MetricSample{
		Timestamp: now, Env: "prod", ServerCount: 1, HealthyCount: 1, ConnectionCount: 2,
		InMsgsRate: 3, OutMsgsRate: 4, InBytesRate: 5, OutBytesRate: 6, Subscriptions: 7,
		Servers:     []ServerMetricSample{{ServerID: "s1", Connections: 2, CPU: 1.5, Mem: 100, Healthy: true}},
		MQTTBridges: []MQTTBridgeMetricSample{{BridgeID: "b1", ConnectionsActive: 3, MsgsRecvQoS0: 4}},
	}
	if err := w.writeSample(sample); err != nil {
		t.Fatal(err)
	}
	from, to := now.Add(-time.Minute).Unix(), now.Add(time.Minute).Unix()
	if points, err := w.QueryEnvMetrics(context.Background(), "prod", from, to, 0); err != nil || len(points) != 1 {
		t.Fatalf("environment points=%v err=%v", points, err)
	}
	if points, err := w.QueryServerMetrics(context.Background(), "prod", "", from, to, 5); err != nil || len(points) != 1 {
		t.Fatalf("server points=%v err=%v", points, err)
	}
	if points, err := w.QueryServerMetrics(context.Background(), "prod", "s1", from, to, 5); err != nil || len(points) != 1 {
		t.Fatalf("filtered server points=%v err=%v", points, err)
	}
	if points, err := w.QueryMQTTMetrics(context.Background(), "prod", "", from, to, 5); err != nil || len(points) != 1 {
		t.Fatalf("MQTT points=%v err=%v", points, err)
	}
	if points, err := w.QueryMQTTMetrics(context.Background(), "prod", "b1", from, to, 5); err != nil || len(points) != 1 {
		t.Fatalf("filtered MQTT points=%v err=%v", points, err)
	}
	if autoStep(5, 1, 200) != 5 || autoStep(0, 100, 200) != 5 {
		t.Fatal("auto step minimum")
	}

	if _, err := s.db.Exec(`INSERT INTO env_metrics (ts, env) VALUES (?, ?)`, now.Add(-2*time.Hour).Unix(), "old"); err != nil {
		t.Fatal(err)
	}
	w.deleteOld()
	var old int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM env_metrics WHERE env='old'`).Scan(&old); err != nil || old != 0 {
		t.Fatalf("old rows=%d err=%v", old, err)
	}
}

func TestMetricsRetentionCleanupIsBoundedPerPass(t *testing.T) {
	s := testStore(t)
	old := time.Now().Add(-48 * time.Hour).Unix()
	if _, err := s.DB().Exec(`
		WITH RECURSIVE rows(n) AS (
			SELECT 1 UNION ALL SELECT n + 1 FROM rows WHERE n < 10001
		)
		INSERT INTO env_metrics (ts, env) SELECT ?, 'bounded-cleanup' FROM rows
	`, old); err != nil {
		t.Fatal(err)
	}

	w := NewMetricsWriter(s, slog.New(slog.NewTextHandler(io.Discard, nil)), time.Hour)
	w.deleteOld()
	var remaining int
	if err := s.DB().QueryRow(`SELECT COUNT(*) FROM env_metrics WHERE env = 'bounded-cleanup'`).Scan(&remaining); err != nil {
		t.Fatal(err)
	}
	if remaining != 1 {
		t.Fatalf("rows remaining after one cleanup pass = %d, want 1 (exactly 10,000 deleted)", remaining)
	}
}

func TestMetricsWriterOperationalTimingAndBusyAccounting(t *testing.T) {
	s := testStore(t)
	w := NewMetricsWriter(s.DB(), slog.New(slog.NewTextHandler(io.Discard, nil)))
	w.recordBusy(errors.New("database is locked"))
	w.recordBusy(errors.New("unrelated"))
	if !w.Submit(MetricSample{Timestamp: time.Now(), Env: "test"}) {
		t.Fatal("sample rejected")
	}
	if stats := w.Stats(); stats.QueueDepth != 1 || stats.OldestQueueAge < 0 || stats.Busy != 1 {
		t.Fatalf("queued stats=%+v", stats)
	}
	ctx, cancel := context.WithCancel(context.Background())
	go w.Run(ctx)
	deadline := time.Now().Add(time.Second)
	for w.Stats().Written == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	cancel()
	if err := w.Wait(context.Background()); err != nil {
		t.Fatal(err)
	}
	stats := w.Stats()
	if stats.Written != 1 || stats.LastWriteNanos <= 0 || stats.LastBatchRows != 1 || stats.QueueDepth != 0 {
		t.Fatalf("written stats=%+v", stats)
	}
}

func TestMetricsWriterQueueDropAndDrain(t *testing.T) {
	s := testStore(t)
	w := NewMetricsWriter(s.DB(), slog.New(slog.NewTextHandler(io.Discard, nil)))
	for i := 0; i < cap(w.ch); i++ {
		if !w.Submit(MetricSample{Timestamp: time.Now(), Env: "test"}) {
			t.Fatal("unexpected queue rejection")
		}
	}
	if w.Submit(MetricSample{Timestamp: time.Now(), Env: "dropped"}) {
		t.Fatal("expected full queue rejection")
	}
	if w.Stats().Dropped != 1 {
		t.Fatalf("stats=%+v", w.Stats())
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { w.Run(ctx); close(done) }()
	cancel()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("writer did not drain")
	}
	if err := w.Wait(context.Background()); err != nil {
		t.Fatal(err)
	}
	if w.Submit(MetricSample{Timestamp: time.Now(), Env: "late"}) {
		t.Fatal("accepted sample after shutdown")
	}
	stats := w.Stats()
	if stats.Written != uint64(cap(w.ch)) || stats.QueueDepth != 0 {
		t.Fatalf("stats=%+v", stats)
	}
}

func TestMetricsWriterAccountsForDatabaseFailure(t *testing.T) {
	s := testStore(t)
	w := NewMetricsWriter(s.DB(), slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err := s.db.Close(); err != nil {
		t.Fatal(err)
	}
	w.persist(MetricSample{Timestamp: time.Now(), Env: "test"})
	if w.Stats().Failed != 1 {
		t.Fatalf("stats=%+v", w.Stats())
	}
	if _, err := w.QueryEnvMetrics(context.Background(), "x", 0, 1, 1); err == nil {
		t.Fatal("expected closed DB query error")
	}
	if _, err := w.QueryServerMetrics(context.Background(), "x", "", 0, 1, 1); err == nil {
		t.Fatal("expected closed DB query error")
	}
	if _, err := w.QueryMQTTMetrics(context.Background(), "x", "", 0, 1, 1); err == nil {
		t.Fatal("expected closed DB query error")
	}
}
