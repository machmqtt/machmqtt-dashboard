package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/noodlebit/machmqtt-dashboard/internal/config"
	"github.com/noodlebit/machmqtt-dashboard/internal/store"
)

const validClusterBody = `{
	"name":"prod",
	"servers":[{"url":"http://nats:8222"}]
}`

func TestAdminListClustersEmpty(t *testing.T) {
	srv, _, token := setupTestServer(t)

	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, authedReq("GET", "/api/admin/clusters", token, ""))

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	var resp map[string][]any
	json.NewDecoder(w.Body).Decode(&resp)
	if len(resp["clusters"]) != 0 {
		t.Errorf("clusters = %d, want 0", len(resp["clusters"]))
	}
}

func TestAdminCreateCluster(t *testing.T) {
	srv, st, _, token := setupTestServerWithStore(t)

	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, authedReq("POST", "/api/admin/clusters", token, validClusterBody))

	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201, body: %s", w.Code, w.Body.String())
	}

	var got map[string]any
	json.NewDecoder(w.Body).Decode(&got)
	if got["id"] == "" || got["id"] == nil {
		t.Error("created cluster missing id")
	}
	if got["name"] != "prod" {
		t.Errorf("name = %v, want prod", got["name"])
	}

	// Should appear in the list.
	count, _ := st.ClusterCount()
	if count != 1 {
		t.Errorf("store cluster count = %d, want 1", count)
	}
}

func TestAdminCreateClusterMissingName(t *testing.T) {
	srv, _, token := setupTestServer(t)

	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, authedReq("POST", "/api/admin/clusters", token,
		`{"servers":[{"url":"http://nats:8222"}]}`))

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
}

func TestAdminCreateClusterMissingServers(t *testing.T) {
	srv, _, token := setupTestServer(t)

	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, authedReq("POST", "/api/admin/clusters", token,
		`{"name":"x"}`))

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
}

func TestAdminCreateClusterInvalidJSON(t *testing.T) {
	srv, _, token := setupTestServer(t)

	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, authedReq("POST", "/api/admin/clusters", token, `{bad json}`))

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
}

func TestAdminUpdateCluster(t *testing.T) {
	srv, st, _, token := setupTestServerWithStore(t)

	cl := &store.Cluster{
		Name:    "original",
		Servers: []config.Server{{URL: "http://nats:8222"}},
	}
	st.CreateCluster(cl)

	w := httptest.NewRecorder()
	body := `{"name":"renamed","servers":[{"url":"http://nats2:8222"}]}`
	srv.Handler().ServeHTTP(w, authedReq("PUT", "/api/admin/clusters/"+cl.ID, token, body))

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body: %s", w.Code, w.Body.String())
	}

	got, _ := st.GetCluster(cl.ID)
	if got.Name != "renamed" {
		t.Errorf("Name = %q, want renamed", got.Name)
	}
	if got.Servers[0].URL != "http://nats2:8222" {
		t.Errorf("Servers[0].URL = %q, want http://nats2:8222", got.Servers[0].URL)
	}
}

func TestAdminUpdateClusterNotFound(t *testing.T) {
	srv, _, token := setupTestServer(t)

	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, authedReq("PUT", "/api/admin/clusters/nope", token, validClusterBody))

	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", w.Code)
	}
}

func TestAdminDeleteCluster(t *testing.T) {
	srv, st, _, token := setupTestServerWithStore(t)

	cl := &store.Cluster{
		Name:    "doomed",
		Servers: []config.Server{{URL: "http://nats:8222"}},
	}
	st.CreateCluster(cl)

	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, authedReq("DELETE", "/api/admin/clusters/"+cl.ID, token, ""))

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body: %s", w.Code, w.Body.String())
	}

	count, _ := st.ClusterCount()
	if count != 0 {
		t.Errorf("cluster count = %d, want 0 after delete", count)
	}
}

func TestAdminDeleteClusterNotFound(t *testing.T) {
	srv, _, token := setupTestServer(t)

	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, authedReq("DELETE", "/api/admin/clusters/nope", token, ""))

	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", w.Code)
	}
}

func TestViewerCannotCreateCluster(t *testing.T) {
	srv, _, a, _ := setupTestServerWithStore(t)

	viewer, _ := a.Store().CreateUser("viewer", "pass", "viewer")
	viewerToken, _ := a.IssueToken(viewer)

	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, authedReq("POST", "/api/admin/clusters", viewerToken, validClusterBody))

	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", w.Code)
	}
}

func TestViewerCannotListAdminClusters(t *testing.T) {
	srv, _, a, _ := setupTestServerWithStore(t)

	viewer, _ := a.Store().CreateUser("viewer", "pass", "viewer")
	viewerToken, _ := a.IssueToken(viewer)

	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, authedReq("GET", "/api/admin/clusters", viewerToken, ""))

	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", w.Code)
	}
}

func TestAdminListClustersAfterCreate(t *testing.T) {
	srv, _, token := setupTestServer(t)

	srv.Handler().ServeHTTP(httptest.NewRecorder(), authedReq("POST", "/api/admin/clusters", token, validClusterBody))
	srv.Handler().ServeHTTP(httptest.NewRecorder(), authedReq("POST", "/api/admin/clusters", token,
		`{"name":"staging","servers":[{"url":"http://staging:8222"}]}`))

	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, authedReq("GET", "/api/admin/clusters", token, ""))

	var resp map[string][]any
	json.NewDecoder(w.Body).Decode(&resp)
	if len(resp["clusters"]) != 2 {
		t.Errorf("clusters = %d, want 2", len(resp["clusters"]))
	}
}
