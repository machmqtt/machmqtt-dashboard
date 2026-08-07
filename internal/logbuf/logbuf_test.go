package logbuf

import (
	"fmt"
	"io"
	"log/slog"
	"testing"
)

func newTestLogger() (*Handler, *slog.Logger) {
	h := New(slog.NewTextHandler(io.Discard, nil), 10)
	return h, slog.New(h)
}

func TestCapturesRecordAttrs(t *testing.T) {
	h, log := newTestLogger()
	log.Info("hello", "k", "v")

	es := h.Entries()
	if len(es) != 1 {
		t.Fatalf("entries = %d, want 1", len(es))
	}
	if es[0].Message != "hello" {
		t.Errorf("Message = %q, want hello", es[0].Message)
	}
	if es[0].Attrs["k"] != "v" {
		t.Errorf("Attrs[k] = %v, want v", es[0].Attrs["k"])
	}
}

// The collector logs almost everything via log.With("cluster", name); the in-UI
// buffer must keep that context, not just the record's direct attrs.
func TestCapturesWithAttrs(t *testing.T) {
	h, base := newTestLogger()
	log := base.With("cluster", "prod").With("conn", "sys")
	log.Info("connected", "url", "nats://x")

	es := h.Entries()
	if len(es) != 1 {
		t.Fatalf("entries = %d, want 1", len(es))
	}
	a := es[0].Attrs
	if a["cluster"] != "prod" {
		t.Errorf("Attrs[cluster] = %v, want prod (WithAttrs context dropped)", a["cluster"])
	}
	if a["conn"] != "sys" {
		t.Errorf("Attrs[conn] = %v, want sys", a["conn"])
	}
	if a["url"] != "nats://x" {
		t.Errorf("Attrs[url] = %v, want nats://x", a["url"])
	}
}

func TestWithGroupPrefixesKeys(t *testing.T) {
	h, base := newTestLogger()
	base.WithGroup("req").With("id", "123").Info("done")

	es := h.Entries()
	if len(es) != 1 {
		t.Fatalf("entries = %d, want 1", len(es))
	}
	if es[0].Attrs["req.id"] != "123" {
		t.Errorf("Attrs = %v, want req.id=123", es[0].Attrs)
	}
}

// TestWithGroupEmptyNameReturnsSameHandler verifies that WithGroup("") is a
// no-op that returns the receiver unchanged (matching slog.Handler semantics),
// rather than allocating a derived handler with a trailing-dot group prefix.
func TestWithGroupEmptyNameReturnsSameHandler(t *testing.T) {
	h, _ := newTestLogger()
	if got := h.WithGroup(""); got != slog.Handler(h) {
		t.Errorf("WithGroup(\"\") = %p, want same receiver %p", got, h)
	}
}

// TestNewNonPositiveSizeUsesDefault verifies that a size of 0 or negative falls
// back to DefaultSize so the ring buffer is always usable.
func TestNewNonPositiveSizeUsesDefault(t *testing.T) {
	for _, size := range []int{0, -1, -100} {
		h := New(slog.NewTextHandler(io.Discard, nil), size)
		if h.c.size != DefaultSize {
			t.Errorf("New(inner, %d).c.size = %d, want DefaultSize=%d", size, h.c.size, DefaultSize)
		}
		if len(h.c.buf) != DefaultSize {
			t.Errorf("New(inner, %d) buf len = %d, want %d", size, len(h.c.buf), DefaultSize)
		}
	}
}

// TestEntriesWrapAroundKeepsNewestInOrder writes more records than the buffer
// holds, forcing ring-buffer wrap-around, and verifies entries() returns the
// most recent `size` records in chronological (oldest-first) order.
func TestEntriesWrapAroundKeepsNewestInOrder(t *testing.T) {
	h, log := newTestLogger() // size = 10
	const total = 15
	for i := range total {
		log.Info(fmt.Sprintf("msg-%d", i))
	}

	es := h.Entries()
	if len(es) != 10 {
		t.Fatalf("entries = %d, want 10 (buffer size)", len(es))
	}
	// After 15 writes into a size-10 buffer, the oldest 5 (msg-0..msg-4) are
	// overwritten; the surviving window is msg-5..msg-14, oldest first.
	if es[0].Message != "msg-5" {
		t.Errorf("oldest entry = %q, want msg-5", es[0].Message)
	}
	if es[9].Message != "msg-14" {
		t.Errorf("newest entry = %q, want msg-14", es[9].Message)
	}
	for i, e := range es {
		want := fmt.Sprintf("msg-%d", i+5)
		if e.Message != want {
			t.Errorf("entries[%d] = %q, want %q (wrap-around order broken)", i, e.Message, want)
		}
	}
}
