package logbuf

import (
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
