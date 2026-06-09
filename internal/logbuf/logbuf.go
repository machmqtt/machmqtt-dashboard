package logbuf

import (
	"context"
	"log/slog"
	"sync"
	"time"
)

const DefaultSize = 500

// Entry is a captured log record.
type Entry struct {
	Time    time.Time      `json:"time"`
	Level   string         `json:"level"`
	Message string         `json:"msg"`
	Attrs   map[string]any `json:"attrs,omitempty"`
}

// core holds the shared ring buffer state across derived handlers.
type core struct {
	mu    sync.Mutex
	buf   []Entry
	pos   int
	size  int
	count int
}

func (c *core) add(r slog.Record) {
	var attrs map[string]any
	r.Attrs(func(a slog.Attr) bool {
		if attrs == nil {
			attrs = make(map[string]any)
		}
		attrs[a.Key] = a.Value.Any()
		return true
	})
	entry := Entry{
		Time:    r.Time,
		Level:   r.Level.String(),
		Message: r.Message,
		Attrs:   attrs,
	}
	c.mu.Lock()
	c.buf[c.pos] = entry
	c.pos = (c.pos + 1) % c.size
	if c.count < c.size {
		c.count++
	}
	c.mu.Unlock()
}

func (c *core) entries() []Entry {
	c.mu.Lock()
	defer c.mu.Unlock()
	n := c.count
	result := make([]Entry, n)
	if c.count < c.size {
		copy(result, c.buf[:n])
	} else {
		// Wrap-around: oldest entries start at c.pos.
		first := c.buf[c.pos:]
		rest := c.buf[:c.pos]
		copy(result, first)
		copy(result[len(first):], rest)
	}
	return result
}

// Handler is an slog.Handler that captures records into a ring buffer while
// also forwarding them to an inner handler (e.g. slog.NewTextHandler to stderr).
type Handler struct {
	c     *core
	inner slog.Handler
}

// New creates a Handler wrapping inner, buffering up to size entries.
func New(inner slog.Handler, size int) *Handler {
	if size <= 0 {
		size = DefaultSize
	}
	return &Handler{
		c:     &core{buf: make([]Entry, size), size: size},
		inner: inner,
	}
}

// Entries returns all buffered log entries in chronological order (oldest first).
func (h *Handler) Entries() []Entry { return h.c.entries() }

func (h *Handler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.inner.Enabled(ctx, level)
}

func (h *Handler) Handle(ctx context.Context, r slog.Record) error {
	h.c.add(r)
	return h.inner.Handle(ctx, r)
}

func (h *Handler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &Handler{c: h.c, inner: h.inner.WithAttrs(attrs)}
}

func (h *Handler) WithGroup(name string) slog.Handler {
	return &Handler{c: h.c, inner: h.inner.WithGroup(name)}
}
