package api

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"
)

func TestMetricsHandlersDisabled(t *testing.T) {
	// No withMetrics → s.metrics is nil → 503 for all three endpoints.
	srv, _, token, id := polledServer(t, natsMockConfig{})
	for _, path := range []string{"metrics/overview", "metrics/servers", "metrics/mqtt"} {
		w := do(t, srv, "GET", "/api/environments/"+id+"/"+path, token, "")
		if w.Code != http.StatusServiceUnavailable {
			t.Errorf("%s status = %d, want 503 when metrics disabled", path, w.Code)
		}
	}
}

func TestHandleEnvMetricsSuccess(t *testing.T) {
	srv, _, token, id := polledServer(t, natsMockConfig{}, withMetrics())
	seedMetric(t, srv, id)
	w := do(t, srv, "GET", "/api/environments/"+id+"/metrics/overview", token, "")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", w.Code, w.Body.String())
	}
	pts := decodeJSON(t, w)["points"].([]any)
	if len(pts) == 0 {
		t.Errorf("points empty, want at least the seeded sample")
	}
}

func TestHandleServerMetricsSuccess(t *testing.T) {
	srv, _, token, id := polledServer(t, natsMockConfig{}, withMetrics())
	seedMetric(t, srv, id)
	w := do(t, srv, "GET", "/api/environments/"+id+"/metrics/servers?server_id="+fixtureServerID, token, "")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	pts, ok := decodeJSON(t, w)["points"].([]any)
	if !ok || len(pts) == 0 {
		t.Errorf("points empty, want the seeded server sample: %s", w.Body.String())
	}
}

func TestHandleMQTTBridgeMetricsSuccess(t *testing.T) {
	srv, _, token, id := polledServer(t, natsMockConfig{}, withMetrics())
	seedMetric(t, srv, id)
	w := do(t, srv, "GET", "/api/environments/"+id+"/metrics/mqtt?bridge_id=bridge-a", token, "")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	pts, ok := decodeJSON(t, w)["points"].([]any)
	if !ok || len(pts) == 0 {
		t.Errorf("points empty, want the seeded bridge sample: %s", w.Body.String())
	}
}

func TestMetricsHandlersQueryError(t *testing.T) {
	srv, s, _, id := polledServer(t, natsMockConfig{}, withMetrics())
	s.Close() // queries now fail
	for _, tc := range []struct {
		path    string
		handler http.HandlerFunc
	}{
		{"metrics/overview", srv.handleEnvMetrics},
		{"metrics/servers?server_id=x", srv.handleServerMetrics},
		{"metrics/mqtt?bridge_id=x", srv.handleMQTTBridgeMetrics},
	} {
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodGet, "/api/environments/"+id+"/"+tc.path, nil)
		r.SetPathValue("env", id)
		tc.handler(w, r)
		if w.Code != http.StatusInternalServerError {
			t.Errorf("%s status = %d, want 500 on query error", tc.path, w.Code)
		}
	}
}

func TestParseTimeRange(t *testing.T) {
	now := time.Now().Unix()

	t.Run("defaults", func(t *testing.T) {
		r := httptest.NewRequest("GET", "/x", nil)
		from, to, step := parseTimeRange(r)
		if to < now-2 || to > now+2 {
			t.Errorf("to = %d, want ~now (%d)", to, now)
		}
		if from < now-3602 || from > now-3598 {
			t.Errorf("from = %d, want ~now-3600", from)
		}
		if step != 0 {
			t.Errorf("step = %d, want 0 (unset)", step)
		}
	})

	t.Run("valid values", func(t *testing.T) {
		from0 := now - 600
		to0 := now - 60
		r := httptest.NewRequest("GET",
			"/x?from="+itoa(from0)+"&to="+itoa(to0)+"&step=30", nil)
		from, to, step := parseTimeRange(r)
		if from != from0 || to != to0 || step != 30 {
			t.Errorf("got from=%d to=%d step=%d, want %d/%d/30", from, to, step, from0, to0)
		}
	})

	t.Run("out of range ignored", func(t *testing.T) {
		// from older than 30d, to far in the future, step too large → all ignored.
		r := httptest.NewRequest("GET",
			"/x?from="+itoa(now-40*24*3600)+"&to="+itoa(now+99999)+"&step=999999", nil)
		from, to, step := parseTimeRange(r)
		if from < now-3602 || from > now-3598 {
			t.Errorf("from = %d, want default (out-of-range ignored)", from)
		}
		if to < now-2 || to > now+2 {
			t.Errorf("to = %d, want default (out-of-range ignored)", to)
		}
		if step != 0 {
			t.Errorf("step = %d, want 0 (out-of-range ignored)", step)
		}
	})

	t.Run("non-numeric ignored", func(t *testing.T) {
		r := httptest.NewRequest("GET", "/x?from=abc&to=def&step=ghi", nil)
		from, to, step := parseTimeRange(r)
		if step != 0 || to < now-2 || to > now+2 || from < now-3602 || from > now-3598 {
			t.Errorf("non-numeric params should fall back to defaults, got from=%d to=%d step=%d", from, to, step)
		}
	})
}

func itoa(n int64) string {
	return strconv.FormatInt(n, 10)
}
