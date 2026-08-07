package store

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func benchmarkLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func benchmarkMetricSample(serverCount int) MetricSample {
	sample := MetricSample{Timestamp: time.Unix(1_700_000_000, 0), Env: "benchmark", ServerCount: serverCount}
	for index := 0; index < serverCount; index++ {
		sample.Servers = append(sample.Servers, ServerMetricSample{ServerID: fmt.Sprintf("server-%04d", index), Healthy: true})
	}
	return sample
}

func BenchmarkMetricsWriteBatch(b *testing.B) {
	for _, serverCount := range []int{1, 100} {
		b.Run(fmt.Sprintf("servers_%d", serverCount), func(b *testing.B) {
			dir := b.TempDir()
			st, err := Open(dir)
			if err != nil {
				b.Fatal(err)
			}
			defer st.Close()
			writer := NewMetricsWriter(st.DB(), benchmarkLogger())
			sample := benchmarkMetricSample(serverCount)
			b.ReportAllocs()
			b.ResetTimer()
			for index := 0; index < b.N; index++ {
				sample.Timestamp = sample.Timestamp.Add(time.Second)
				if err := writer.writeSample(sample); err != nil {
					b.Fatal(err)
				}
			}
			b.StopTimer()
			if _, err := st.DB().Exec(`PRAGMA wal_checkpoint(TRUNCATE)`); err != nil {
				b.Fatal(err)
			}
			if info, err := os.Stat(filepath.Join(dir, "dashboard.db")); err == nil {
				b.ReportMetric(float64(info.Size()), "db-bytes")
			}
		})
	}
}

func BenchmarkMetricsQueryWindow(b *testing.B) {
	st, err := Open(b.TempDir())
	if err != nil {
		b.Fatal(err)
	}
	defer st.Close()
	base := time.Unix(1_700_000_000, 0)
	tx, err := st.DB().Begin()
	if err != nil {
		b.Fatal(err)
	}
	stmt, err := tx.Prepare(`INSERT INTO env_metrics
		(ts, env, server_count, healthy_count, connection_count, in_msgs_rate,
		 out_msgs_rate, in_bytes_rate, out_bytes_rate, subscriptions)
		VALUES (?, 'benchmark', 3, 3, 50, 1, 1, 1, 1, 10)`)
	if err != nil {
		b.Fatal(err)
	}
	for index := 0; index < 10_000; index++ {
		if _, err := stmt.Exec(base.Add(time.Duration(index) * time.Second).Unix()); err != nil {
			b.Fatal(err)
		}
	}
	if err := stmt.Close(); err != nil {
		b.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		b.Fatal(err)
	}
	writer := NewMetricsWriter(st.DB(), benchmarkLogger())
	b.ReportAllocs()
	b.ResetTimer()
	for index := 0; index < b.N; index++ {
		if _, err := writer.QueryEnvMetrics(context.Background(), "benchmark", base.Unix(), base.Add(10_000*time.Second).Unix(), 60); err != nil {
			b.Fatal(err)
		}
	}
}
