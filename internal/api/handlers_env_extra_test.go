package api

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/noodlebit/machmqtt-dashboard/internal/collector"
)

func TestHandleEnvironmentsWithHealth(t *testing.T) {
	// A polled server has a live collector, so ClusterHealth is non-nil and the
	// sidebar health-badge fields are populated.
	srv, _, token, id := polledServer(t, natsMockConfig{})
	w := do(t, srv, "GET", "/api/environments", token, "")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	envs := decodeJSON(t, w)["environments"].([]any)
	if len(envs) != 1 {
		t.Fatalf("environments = %d, want 1", len(envs))
	}
	e := envs[0].(map[string]any)
	if e["id"] != id {
		t.Errorf("id = %v, want %s", e["id"], id)
	}
	if _, ok := e["collection_mode"]; !ok {
		t.Errorf("missing collection_mode (health badge not populated): %v", e)
	}
}

func TestHandleEnvironmentsStoreError(t *testing.T) {
	srv, s, token, _ := polledServer(t, natsMockConfig{})
	s.Close() // make ListClusters fail
	w := do(t, srv, "GET", "/api/environments", token, "")
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", w.Code)
	}
}

func TestHandleGetPositionsStoreError(t *testing.T) {
	srv, s, token, id := polledServer(t, natsMockConfig{})
	s.Close()
	w := do(t, srv, "GET", "/api/environments/"+id+"/topology/positions", token, "")
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", w.Code)
	}
}

func TestHandleSavePositionsStoreError(t *testing.T) {
	srv, s, token, id := polledServer(t, natsMockConfig{})
	s.Close()
	body := `{"positions":[{"node_id":"n1","x":1,"y":2}]}`
	w := do(t, srv, "PUT", "/api/environments/"+id+"/topology/positions", token, body)
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", w.Code)
	}
}

func TestHandleConnzOffsetBeyondTotal(t *testing.T) {
	srv, _, token, id := polledServer(t, natsMockConfig{})
	w := do(t, srv, "GET", "/api/environments/"+id+"/connz?offset=999", token, "")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	m := decodeJSON(t, w)
	if len(m["connections"].([]any)) != 0 {
		t.Errorf("connections = %d, want 0 when offset beyond total", len(m["connections"].([]any)))
	}
	if m["offset"].(float64) != 2 {
		t.Errorf("offset clamped to %v, want 2 (=total)", m["offset"])
	}
}

func TestHandleSubsDetailServerFilterNoMatchAndOffset(t *testing.T) {
	srv, _, token, id := polledServer(t, natsMockConfig{subsInPlainConnz: true})

	// Server filter that matches nothing → 0 rows.
	w := do(t, srv, "GET", "/api/environments/"+id+"/subsz/detail?server=nope", token, "")
	if got := int(decodeJSON(t, w)["total"].(float64)); got != 0 {
		t.Errorf("server=nope total = %d, want 0", got)
	}

	// Offset beyond total clamps to total → empty page but correct total.
	w2 := do(t, srv, "GET", "/api/environments/"+id+"/subsz/detail?offset=999", token, "")
	m := decodeJSON(t, w2)
	if m["total"].(float64) != 2 {
		t.Errorf("total = %v, want 2", m["total"])
	}
	if subs, _ := m["subscriptions"].([]any); len(subs) != 0 {
		t.Errorf("subscriptions = %d, want 0 (offset beyond total)", len(subs))
	}
}

// TestGetSubsRowsHTTPFallbackList drives the HTTP fallback where the subs-detail
// fetch fails and the handler retries via FetchConnzWithSubs, which returns
// subscriptions as a plain list (Subs []string) rather than detail.
func TestGetSubsRowsHTTPFallbackList(t *testing.T) {
	srv, _, token, id := polledServer(t, natsMockConfig{
		subsInPlainConnz: false, // snapshot has no subs → fallback to HTTP
		subsDetailErrors: true,  // subs=detail fails → retry with subs=true
		subsAsList:       true,  // subs=true returns Subs []string
	})
	w := do(t, srv, "GET", "/api/environments/"+id+"/subsz/detail", token, "")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", w.Code, w.Body.String())
	}
	if got := int(decodeJSON(t, w)["total"].(float64)); got != 2 {
		t.Errorf("total = %d, want 2 (subs list fallback)", got)
	}
}

// --- Direct unit tests for the snapshot→rows helpers ---

func TestSubsRowsFromConnz(t *testing.T) {
	t.Run("nil when no subs", func(t *testing.T) {
		snap := &collector.Snapshot{
			Connz: map[string]*collector.Connz{"s": {Conns: []collector.ConnInfo{{Cid: 1}}}},
		}
		if rows := subsRowsFromConnz(snap); rows != nil {
			t.Errorf("rows = %v, want nil when conns carry no subs", rows)
		}
	})

	t.Run("detail and list paths", func(t *testing.T) {
		snap := &collector.Snapshot{
			Varz: map[string]*collector.Varz{"s": {ServerName: "srvname"}},
			Connz: map[string]*collector.Connz{"s": {Conns: []collector.ConnInfo{
				{Cid: 1, Account: "A", SubsDetail: []collector.SubDetail{{Subject: "d.one", Sid: "1"}}},
				{Cid: 2, Account: "B", Subs: []string{"l.one", "l.two"}},
				{Cid: 3}, // no subs → skipped
			}}},
		}
		rows := subsRowsFromConnz(snap)
		if len(rows) != 3 {
			t.Fatalf("rows = %d, want 3 (1 detail + 2 list)", len(rows))
		}
		// Server name is resolved from Varz, and rows are sorted by subject.
		for _, r := range rows {
			if r.ServerName != "srvname" {
				t.Errorf("ServerName = %q, want srvname", r.ServerName)
			}
		}
		if rows[0].Subject != "d.one" {
			t.Errorf("first subject = %q, want d.one (sorted)", rows[0].Subject)
		}
	})
}

func TestCacheSubsRowsEviction(t *testing.T) {
	srv, _, _, _ := polledServer(t, natsMockConfig{})
	// Fill the cache past its max so the oldest entry is evicted.
	for i := 0; i < subsCacheMaxEntries+5; i++ {
		srv.cacheSubsRows(rowKey(i), []subRow{{Subject: "s"}})
	}
	subsDetailCacheMu.Lock()
	n := len(subsDetailCacheData)
	subsDetailCacheMu.Unlock()
	if n > subsCacheMaxEntries+1 {
		t.Errorf("cache size = %d, want bounded near %d (eviction not running)", n, subsCacheMaxEntries)
	}
}

func rowKey(i int) string {
	return "evict-env-" + string(rune('A'+i%26)) + string(rune('0'+i/26))
}

func TestWriteJSONEncodeError(t *testing.T) {
	// A channel can't be JSON-encoded; writeJSON must not panic and must log.
	w := httptest.NewRecorder()
	writeJSON(w, make(chan int))
	// Header was set even though encoding failed.
	if ct := w.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("content-type = %q, want application/json", ct)
	}
}

func TestWriteErrorEncodeError(t *testing.T) {
	w := httptest.NewRecorder()
	// writeError marshals map[string]string{"error": msg}; that always encodes,
	// so to hit the encode-error branch we wrap the writer to fail writes.
	fw := &failWriter{ResponseRecorder: w}
	writeError(fw, http.StatusBadRequest, "boom")
	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", w.Code)
	}
}

// failWriter is an http.ResponseWriter whose Write always errors, used to drive
// the encode-failure branch of writeError.
type failWriter struct {
	*httptest.ResponseRecorder
}

func (f *failWriter) Write([]byte) (int, error) {
	return 0, http.ErrHandlerTimeout
}
