package api

import (
	"net/http"
	"testing"

	"github.com/noodlebit/machmqtt-dashboard/internal/store"
)

func TestRestoreURLCreds(t *testing.T) {
	prev := []string{"nats://user:pass@host:4222"}

	// Incoming URL with no creds and a matching host gets the stored creds back.
	if got := restoreURLCreds("nats://host:4222", prev); got != "nats://user:pass@host:4222" {
		t.Errorf("restore = %q, want creds grafted back", got)
	}
	// Incoming URL that already has creds is returned unchanged.
	if got := restoreURLCreds("nats://other:secret@host:4222", prev); got != "nats://other:secret@host:4222" {
		t.Errorf("restore = %q, want unchanged (already has creds)", got)
	}
	// Unparseable incoming URL is returned unchanged.
	if got := restoreURLCreds(":bad", prev); got != ":bad" {
		t.Errorf("restore = %q, want unchanged for unparseable URL", got)
	}
	// No matching host → unchanged.
	if got := restoreURLCreds("nats://different:4222", prev); got != "nats://different:4222" {
		t.Errorf("restore = %q, want unchanged when no host matches", got)
	}
}

func TestMergeClusterSecretsNilPrev(t *testing.T) {
	cl := &store.Cluster{Name: "x", AdminToken: "keep"}
	mergeClusterSecrets(cl, nil) // must be a no-op, no panic
	if cl.AdminToken != "keep" {
		t.Errorf("AdminToken = %q, want unchanged", cl.AdminToken)
	}
}

func TestHandleListClustersStoreError(t *testing.T) {
	srv, s, token, _ := polledServer(t, natsMockConfig{})
	s.Close()
	w := do(t, srv, "GET", "/api/admin/clusters", token, "")
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", w.Code)
	}
}

func TestHandleCreateClusterInvalidTLS(t *testing.T) {
	srv, _, token, _ := polledServer(t, natsMockConfig{})
	// A TLS CA file that doesn't exist fails fetcher construction → 400.
	body := `{"name":"bad","servers":[{"url":"http://localhost:8222"}],"tls":{"ca_file":"/no/such/ca.pem"}}`
	w := do(t, srv, "POST", "/api/admin/clusters", token, body)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400, body=%s", w.Code, w.Body.String())
	}
}

func TestHandleUpdateClusterInvalidBody(t *testing.T) {
	srv, _, token, id := polledServer(t, natsMockConfig{})
	w := do(t, srv, "PUT", "/api/admin/clusters/"+id, token, `not json`)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
}

func TestHandleUpdateClusterInvalidTLS(t *testing.T) {
	srv, _, token, id := polledServer(t, natsMockConfig{})
	body := `{"name":"env1","servers":[{"url":"http://localhost:8222"}],"tls":{"ca_file":"/no/such/ca.pem"}}`
	w := do(t, srv, "PUT", "/api/admin/clusters/"+id, token, body)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400, body=%s", w.Code, w.Body.String())
	}
}
