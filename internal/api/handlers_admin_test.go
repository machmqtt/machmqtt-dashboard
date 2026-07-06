package api

import (
	"io"
	"log/slog"
	"net/http"
	"testing"

	"github.com/noodlebit/machmqtt-dashboard/internal/logbuf"
)

func TestHandleAdminLogsNilBuffer(t *testing.T) {
	// No log buffer wired → empty logs array, still 200.
	srv, _, token, _ := polledServer(t, natsMockConfig{})
	w := do(t, srv, "GET", "/api/admin/logs", token, "")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	logs, ok := decodeJSON(t, w)["logs"].([]any)
	if !ok || len(logs) != 0 {
		t.Errorf("logs = %v, want empty array", decodeJSON(t, w)["logs"])
	}
}

func TestHandleAdminLogsReturnsNewestFirst(t *testing.T) {
	lb := logbuf.New(slog.NewTextHandler(io.Discard, nil), 10)
	log := slog.New(lb)
	// Emit a few records so the buffer has ordered entries.
	log.Info("first")
	log.Info("second")
	log.Info("third")

	srv, _, token, _ := polledServer(t, natsMockConfig{}, withLogBuf(lb))
	w := do(t, srv, "GET", "/api/admin/logs", token, "")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	logs := decodeJSON(t, w)["logs"].([]any)
	if len(logs) < 3 {
		t.Fatalf("logs len = %d, want >= 3", len(logs))
	}
	// Newest-first: the first entry should be the most recent message ("third").
	first := logs[0].(map[string]any)
	if first["msg"] != "third" {
		t.Errorf("first log msg = %v, want third (newest-first)", first["msg"])
	}
}
