package store

import (
	"context"
	"log/slog"
	"testing"
	"time"
)

// NOTE on clusters.go's remaining uncovered branches: generateClusterID's
// rand.Read error path (and its propagation in CreateCluster) is dead code under
// this Go toolchain — crypto/rand.Read no longer returns a non-nil error; it
// aborts the process via fatal() (golang.org/issue/66821). The json.Marshal
// error returns in marshalClusterFields are likewise unreachable: every cluster
// field is a concrete struct of strings/ints/bools/slices/pointers, none of
// which can fail to marshal. Neither is testable without a production seam.

// TestStorePing verifies Ping reports a healthy connection on an open store and
// an error once the underlying DB is closed.
func TestStorePing(t *testing.T) {
	s := testStore(t)
	if err := s.Ping(context.Background()); err != nil {
		t.Fatalf("Ping on open store = %v, want nil", err)
	}

	// After closing the DB, Ping must report the connection is unusable.
	s.db.Close()
	if err := s.Ping(context.Background()); err == nil {
		t.Error("Ping on closed store = nil, want error")
	}
}

// TestMetricsWriterDroppedCounter fills the writer's buffer so the next Submit is
// dropped, then asserts Dropped() reflects the lost sample. Deterministic: Run()
// is never started, so the buffer stays full and the counter is read after the
// overflow Submit returns.
func TestMetricsWriterDroppedCounter(t *testing.T) {
	s := testStore(t)
	// Do NOT start Run() — the channel must stay full so Submit drops.
	w := NewMetricsWriter(s, slog.Default(), 0)

	if got := w.Dropped(); got != 0 {
		t.Fatalf("Dropped() = %d before any drop, want 0", got)
	}

	sample := MetricSample{Timestamp: time.Now(), Env: "drop-env"}

	// Fill the buffer exactly to capacity; none of these should be dropped.
	capacity := cap(w.ch)
	for i := 0; i < capacity; i++ {
		w.Submit(sample)
	}
	if got := w.Dropped(); got != 0 {
		t.Fatalf("Dropped() = %d after filling buffer to capacity, want 0", got)
	}

	// The next Submit overflows the buffer and must be counted as dropped.
	w.Submit(sample)
	if got := w.Dropped(); got != 1 {
		t.Errorf("Dropped() = %d after one overflow Submit, want 1", got)
	}

	// A further overflow Submit increments the cumulative counter again.
	w.Submit(sample)
	if got := w.Dropped(); got != 2 {
		t.Errorf("Dropped() = %d after two overflow Submits, want 2", got)
	}
}
