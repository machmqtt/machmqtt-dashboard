package api

import (
	"encoding/json"
	"net/http"
	"testing"
)

func TestHandleVersion(t *testing.T) {
	srv, _, token, _ := polledServer(t, natsMockConfig{})
	w := do(t, srv, "GET", "/api/version", token, "")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	if decodeJSON(t, w)["version"] != "test" {
		t.Errorf("version = %v, want test", decodeJSON(t, w)["version"])
	}
}

func TestHandleOverview(t *testing.T) {
	srv, _, token, id := polledServer(t, natsMockConfig{})
	w := do(t, srv, "GET", "/api/environments/"+id+"/overview", token, "")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", w.Code, w.Body.String())
	}
	m := decodeJSON(t, w)
	if m["server_count"].(float64) != 1 {
		t.Errorf("server_count = %v, want 1", m["server_count"])
	}
	if m["connection_count"].(float64) != 2 {
		t.Errorf("connection_count = %v, want 2", m["connection_count"])
	}
}

func TestHandleOverviewNotFound(t *testing.T) {
	srv, _, token, _ := polledServer(t, natsMockConfig{})
	w := do(t, srv, "GET", "/api/environments/missing/overview", token, "")
	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", w.Code)
	}
}

func TestHandleTopology(t *testing.T) {
	srv, _, token, id := polledServer(t, natsMockConfig{})
	w := do(t, srv, "GET", "/api/environments/"+id+"/topology", token, "")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	if _, ok := decodeJSON(t, w)["nodes"]; !ok {
		t.Errorf("topology missing nodes field: %s", w.Body.String())
	}
}

func TestHandleTopologyNotFound(t *testing.T) {
	srv, _, token, _ := polledServer(t, natsMockConfig{})
	w := do(t, srv, "GET", "/api/environments/missing/topology", token, "")
	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", w.Code)
	}
}

// TestEnvSnapshotHandlers covers the family of handlers that simply return a
// slice of the snapshot (varz/routez/gatewayz/leafz/subsz/jsz/accountz), for
// both the populated and not-found cases.
func TestEnvSnapshotHandlers(t *testing.T) {
	srv, _, token, id := polledServer(t, natsMockConfig{})
	for _, path := range []string{"varz", "routez", "gatewayz", "leafz", "subsz", "jsz", "accountz"} {
		t.Run(path, func(t *testing.T) {
			w := do(t, srv, "GET", "/api/environments/"+id+"/"+path, token, "")
			if w.Code != http.StatusOK {
				t.Fatalf("%s status = %d", path, w.Code)
			}
			// All return a JSON object keyed by server ID.
			var m map[string]json.RawMessage
			if err := json.Unmarshal(w.Body.Bytes(), &m); err != nil {
				t.Fatalf("%s body not a JSON object: %s", path, w.Body.String())
			}
			if _, ok := m[fixtureServerID]; !ok {
				t.Errorf("%s missing %q key: %s", path, fixtureServerID, w.Body.String())
			}
		})
		t.Run(path+"_notfound", func(t *testing.T) {
			w := do(t, srv, "GET", "/api/environments/missing/"+path, token, "")
			if w.Code != http.StatusNotFound {
				t.Errorf("%s missing-env status = %d, want 404", path, w.Code)
			}
		})
	}
}

func TestHandleConnzBasic(t *testing.T) {
	srv, _, token, id := polledServer(t, natsMockConfig{subsInPlainConnz: true})
	w := do(t, srv, "GET", "/api/environments/"+id+"/connz", token, "")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	m := decodeJSON(t, w)
	if m["total"].(float64) != 2 {
		t.Errorf("total = %v, want 2", m["total"])
	}
	if len(m["connections"].([]any)) != 2 {
		t.Errorf("connections len = %d, want 2", len(m["connections"].([]any)))
	}
}

func TestHandleConnzPagination(t *testing.T) {
	srv, _, token, id := polledServer(t, natsMockConfig{})
	w := do(t, srv, "GET", "/api/environments/"+id+"/connz?limit=1&offset=1", token, "")
	m := decodeJSON(t, w)
	if m["total"].(float64) != 2 {
		t.Errorf("total = %v, want 2", m["total"])
	}
	if got := len(m["connections"].([]any)); got != 1 {
		t.Errorf("connections len = %d, want 1 (limit=1)", got)
	}
	if m["offset"].(float64) != 1 {
		t.Errorf("offset = %v, want 1", m["offset"])
	}
}

func TestHandleConnzAccountFilter(t *testing.T) {
	srv, _, token, id := polledServer(t, natsMockConfig{})
	w := do(t, srv, "GET", "/api/environments/"+id+"/connz?acc=ACC", token, "")
	m := decodeJSON(t, w)
	conns := m["connections"].([]any)
	if len(conns) != 1 {
		t.Fatalf("connections len = %d, want 1 (acc=ACC)", len(conns))
	}
	if conns[0].(map[string]any)["account"] != "ACC" {
		t.Errorf("filtered conn account = %v, want ACC", conns[0].(map[string]any)["account"])
	}
}

func TestHandleConnzSubjectFilterSnapshotPath(t *testing.T) {
	// subsInPlainConnz=true means the snapshot carries subscription detail, so
	// the subject filter resolves CIDs via subsRowsFromConnz (no HTTP fallback).
	srv, _, token, id := polledServer(t, natsMockConfig{subsInPlainConnz: true})
	w := do(t, srv, "GET", "/api/environments/"+id+"/connz?filter_subject=foo.bar", token, "")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	m := decodeJSON(t, w)
	if m["subs_available"] != true {
		t.Errorf("subs_available = %v, want true", m["subs_available"])
	}
	conns := m["connections"].([]any)
	if len(conns) != 1 || conns[0].(map[string]any)["cid"].(float64) != 1 {
		t.Errorf("subject filter conns = %v, want only cid 1", conns)
	}
}

func TestHandleConnzSubjectFilterHTTPFallback(t *testing.T) {
	// subsInPlainConnz=false: the snapshot has no subs, so the subject filter
	// falls back to a /connz?subs=detail HTTP fetch.
	srv, _, token, id := polledServer(t, natsMockConfig{subsInPlainConnz: false})
	w := do(t, srv, "GET", "/api/environments/"+id+"/connz?filter_subject=foo.bar", token, "")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	m := decodeJSON(t, w)
	if m["subs_available"] != true {
		t.Errorf("subs_available = %v, want true (fallback fetched rows)", m["subs_available"])
	}
	conns := m["connections"].([]any)
	if len(conns) != 1 || conns[0].(map[string]any)["cid"].(float64) != 1 {
		t.Errorf("subject filter conns = %v, want only cid 1", conns)
	}
}

func TestHandleConnzNotFound(t *testing.T) {
	srv, _, token, _ := polledServer(t, natsMockConfig{})
	w := do(t, srv, "GET", "/api/environments/missing/connz", token, "")
	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", w.Code)
	}
}

func TestHandleConnzDetail(t *testing.T) {
	srv, _, token, id := polledServer(t, natsMockConfig{subsInPlainConnz: true})
	w := do(t, srv, "GET", "/api/environments/"+id+"/connz/1", token, "")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", w.Code, w.Body.String())
	}
	m := decodeJSON(t, w)
	if m["cid"].(float64) != 1 {
		t.Errorf("cid = %v, want 1", m["cid"])
	}
}

func TestHandleConnzDetailEnrichesSubsFromCache(t *testing.T) {
	// No subs in the snapshot conn → handler enriches SubsDetail from the subs
	// cache (HTTP fallback) for the requested cid.
	srv, _, token, id := polledServer(t, natsMockConfig{subsInPlainConnz: false})
	w := do(t, srv, "GET", "/api/environments/"+id+"/connz/1", token, "")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	m := decodeJSON(t, w)
	subs, ok := m["subscriptions_list_detail"].([]any)
	if !ok || len(subs) == 0 {
		t.Errorf("expected enriched subs detail, got %v", m["subscriptions_list_detail"])
	}
}

func TestHandleConnzDetailBadCID(t *testing.T) {
	srv, _, token, id := polledServer(t, natsMockConfig{})
	w := do(t, srv, "GET", "/api/environments/"+id+"/connz/notanumber", token, "")
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
}

func TestHandleConnzDetailNotFound(t *testing.T) {
	srv, _, token, id := polledServer(t, natsMockConfig{})
	w := do(t, srv, "GET", "/api/environments/"+id+"/connz/9999", token, "")
	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", w.Code)
	}
	// And a missing env.
	w2 := do(t, srv, "GET", "/api/environments/missing/connz/1", token, "")
	if w2.Code != http.StatusNotFound {
		t.Fatalf("missing-env status = %d, want 404", w2.Code)
	}
}

func TestHandleSubsDetail(t *testing.T) {
	srv, _, token, id := polledServer(t, natsMockConfig{subsInPlainConnz: true})
	w := do(t, srv, "GET", "/api/environments/"+id+"/subsz/detail", token, "")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", w.Code, w.Body.String())
	}
	m := decodeJSON(t, w)
	if m["total"].(float64) != 2 {
		t.Errorf("total = %v, want 2 subs", m["total"])
	}
}

func TestHandleSubsDetailFilters(t *testing.T) {
	srv, _, token, id := polledServer(t, natsMockConfig{subsInPlainConnz: true})

	cases := []struct {
		name  string
		query string
		want  int
	}{
		{"subject", "subject=foo", 1},
		{"account", "account=ACC", 1},
		{"server_by_name", "server=nats-1", 2},
		{"hide_system", "hide_system=true", 1}, // drops $SYS.SERVER.x
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := do(t, srv, "GET", "/api/environments/"+id+"/subsz/detail?"+tc.query, token, "")
			if w.Code != http.StatusOK {
				t.Fatalf("status = %d", w.Code)
			}
			if got := int(decodeJSON(t, w)["total"].(float64)); got != tc.want {
				t.Errorf("%s total = %d, want %d", tc.query, got, tc.want)
			}
		})
	}
}

func TestHandleSubsDetailNotFound(t *testing.T) {
	srv, _, token, _ := polledServer(t, natsMockConfig{})
	w := do(t, srv, "GET", "/api/environments/missing/subsz/detail", token, "")
	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", w.Code)
	}
}

func TestHandleAccountDetail(t *testing.T) {
	srv, _, token, id := polledServer(t, natsMockConfig{subsInPlainConnz: true})

	t.Run("regular account", func(t *testing.T) {
		w := do(t, srv, "GET", "/api/environments/"+id+"/accountz/ACC", token, "")
		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, body=%s", w.Code, w.Body.String())
		}
		m := decodeJSON(t, w)
		if m["account_name"] != "ACC" {
			t.Errorf("account_name = %v, want ACC", m["account_name"])
		}
		// is_system has omitempty, so a non-system account omits it entirely.
		if m["is_system"] == true {
			t.Errorf("is_system = true, want false/absent for a regular account")
		}
		if m["client_connections"].(float64) != 1 {
			t.Errorf("client_connections = %v, want 1", m["client_connections"])
		}
	})

	t.Run("system account", func(t *testing.T) {
		w := do(t, srv, "GET", "/api/environments/"+id+"/accountz/SYS", token, "")
		if w.Code != http.StatusOK {
			t.Fatalf("status = %d", w.Code)
		}
		if decodeJSON(t, w)["is_system"] != true {
			t.Errorf("is_system = %v, want true", decodeJSON(t, w)["is_system"])
		}
	})

	t.Run("unknown account", func(t *testing.T) {
		w := do(t, srv, "GET", "/api/environments/"+id+"/accountz/NOPE", token, "")
		if w.Code != http.StatusNotFound {
			t.Errorf("status = %d, want 404", w.Code)
		}
	})

	t.Run("missing env", func(t *testing.T) {
		w := do(t, srv, "GET", "/api/environments/missing/accountz/ACC", token, "")
		if w.Code != http.StatusNotFound {
			t.Errorf("status = %d, want 404", w.Code)
		}
	})
}

func TestHandleTopologyPositions(t *testing.T) {
	srv, _, token, id := polledServer(t, natsMockConfig{})

	// Initially empty.
	w := do(t, srv, "GET", "/api/environments/"+id+"/topology/positions", token, "")
	if w.Code != http.StatusOK {
		t.Fatalf("get status = %d", w.Code)
	}
	if pos, ok := decodeJSON(t, w)["positions"].([]any); !ok || len(pos) != 0 {
		t.Errorf("expected empty positions, got %v", decodeJSON(t, w)["positions"])
	}

	// Save positions + camera.
	body := `{"positions":[{"node_id":"srv-1","x":1.5,"y":2.5}],"camera":{"zoom":1.2,"center_x":10,"center_y":20}}`
	ws := do(t, srv, "PUT", "/api/environments/"+id+"/topology/positions", token, body)
	if ws.Code != http.StatusOK {
		t.Fatalf("save status = %d, body=%s", ws.Code, ws.Body.String())
	}

	// Read back: positions and camera persisted.
	w2 := do(t, srv, "GET", "/api/environments/"+id+"/topology/positions", token, "")
	m := decodeJSON(t, w2)
	pos := m["positions"].([]any)
	if len(pos) != 1 || pos[0].(map[string]any)["node_id"] != "srv-1" {
		t.Errorf("positions = %v, want one srv-1 entry", pos)
	}
	if _, ok := m["camera"]; !ok {
		t.Errorf("camera not persisted: %v", m)
	}
}

func TestHandleSavePositionsBadBody(t *testing.T) {
	srv, _, token, id := polledServer(t, natsMockConfig{})
	w := do(t, srv, "PUT", "/api/environments/"+id+"/topology/positions", token, `not json`)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
}

// clampInt is exercised indirectly above (limit/offset); this pins its edge
// cases directly.
func TestClampInt(t *testing.T) {
	cases := []struct {
		in       string
		def, max int
		want     int
	}{
		{"", 50, 100, 50},     // empty → default
		{"abc", 50, 100, 50},  // invalid → default
		{"-5", 50, 100, 50},   // negative → default
		{"0", 50, 100, 50},    // zero → default
		{"30", 50, 100, 30},   // in range
		{"500", 50, 100, 100}, // over max → capped
	}
	for _, tc := range cases {
		if got := clampInt(tc.in, tc.def, tc.max); got != tc.want {
			t.Errorf("clampInt(%q,%d,%d) = %d, want %d", tc.in, tc.def, tc.max, got, tc.want)
		}
	}
}

func TestIsSystemSubject(t *testing.T) {
	cases := map[string]bool{
		"":              false,
		"foo.bar":       false,
		"$SYS.SERVER.x": true,
		"_INBOX.abc":    true,
		"$MQTT5.foo":    false, // explicitly non-system prefix
	}
	for subj, want := range cases {
		if got := isSystemSubject(subj); got != want {
			t.Errorf("isSystemSubject(%q) = %v, want %v", subj, got, want)
		}
	}
}
