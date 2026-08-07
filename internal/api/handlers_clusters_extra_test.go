package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/noodlebit/machmqtt-dashboard/internal/config"
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
	srv, s, _, _ := polledServer(t, natsMockConfig{})
	s.Close()
	w := httptest.NewRecorder()
	srv.handleListClusters(w, httptest.NewRequest(http.MethodGet, "/api/admin/clusters", nil))
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", w.Code)
	}
}

func TestHandleCreateClusterInvalidTLS(t *testing.T) {
	srv, _, token, _ := polledServer(t, natsMockConfig{})
	// ca_file names a path on the dashboard host, so the API refuses it outright
	// rather than reporting whether the file happens to exist.
	body := `{"name":"bad","servers":[{"url":"http://localhost:8222"}],"tls":{"ca_file":"/no/such/ca.pem"}}`
	w := do(t, srv, "POST", "/api/admin/clusters", token, body)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400, body=%s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "ca_pem") {
		t.Errorf("body = %s, want it to point at ca_pem", w.Body.String())
	}
	if strings.Contains(w.Body.String(), "no such file") {
		t.Errorf("body = %s, must not reveal whether the path exists", w.Body.String())
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
	if strings.Contains(w.Body.String(), "no such file") {
		t.Errorf("body = %s, must not reveal whether the path exists", w.Body.String())
	}
}

// --- Host paths are config-file-only (CodeQL go/path-injection, alert #2) ---
//
// ca_file and creds_file name files on the dashboard host. Accepting them from a
// request body let an admin-role caller probe the filesystem via the error text
// and stall or OOM the process by naming a device file.

func TestSanitizeHostPathsRejectsClientChosenPaths(t *testing.T) {
	tests := map[string]struct {
		req  clusterRequest
		want string
	}{
		"cluster ca_file": {
			req:  clusterRequest{TLS: &config.TLSConfig{CAFile: "/etc/shadow"}},
			want: "tls.ca_file",
		},
		"nats ca_file": {
			req:  clusterRequest{NATSConn: &config.NATSConnConfig{TLS: &config.TLSConfig{CAFile: "/etc/shadow"}}},
			want: "nats_conn.tls.ca_file",
		},
		"nats creds_file": {
			req:  clusterRequest{NATSConn: &config.NATSConnConfig{CredsFile: "/etc/shadow"}},
			want: "nats_conn.creds_file",
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			err := sanitizeHostPaths(&tc.req, nil)
			if err == nil {
				t.Fatalf("sanitizeHostPaths accepted %s", tc.want)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("err = %q, want it to name %s", err, tc.want)
			}
		})
	}
}

// A blank ca_file must be replaced by the stored one, not left empty, or every
// edit from the UI would silently drop the operator's configured CA.
func TestSanitizeHostPathsInheritsStoredPaths(t *testing.T) {
	prev := &store.Cluster{
		TLS: &config.TLSConfig{CAFile: "/etc/machmqtt/ca.pem"},
		NATSConn: &config.NATSConnConfig{
			TLS:       &config.TLSConfig{CAFile: "/etc/machmqtt/nats-ca.pem"},
			CredsFile: "/etc/machmqtt/nats.creds",
		},
	}
	req := clusterRequest{
		TLS:      &config.TLSConfig{},
		NATSConn: &config.NATSConnConfig{TLS: &config.TLSConfig{}},
	}

	if err := sanitizeHostPaths(&req, prev); err != nil {
		t.Fatalf("sanitizeHostPaths: %v", err)
	}
	if req.TLS.CAFile != "/etc/machmqtt/ca.pem" {
		t.Errorf("tls.ca_file = %q, want the stored path", req.TLS.CAFile)
	}
	if req.NATSConn.TLS.CAFile != "/etc/machmqtt/nats-ca.pem" {
		t.Errorf("nats_conn.tls.ca_file = %q, want the stored path", req.NATSConn.TLS.CAFile)
	}
	if req.NATSConn.CredsFile != "/etc/machmqtt/nats.creds" {
		t.Errorf("nats_conn.creds_file = %q, want the stored path", req.NATSConn.CredsFile)
	}
}

// Re-submitting the ca_file the UI was shown is a no-op edit, not an attack.
func TestSanitizeHostPathsAllowsUnchangedPath(t *testing.T) {
	prev := &store.Cluster{TLS: &config.TLSConfig{CAFile: "/etc/machmqtt/ca.pem"}}
	req := clusterRequest{TLS: &config.TLSConfig{CAFile: "/etc/machmqtt/ca.pem"}}

	if err := sanitizeHostPaths(&req, prev); err != nil {
		t.Fatalf("round-tripping the stored ca_file was rejected: %v", err)
	}

	// Changing it is still refused.
	req = clusterRequest{TLS: &config.TLSConfig{CAFile: "/etc/shadow"}}
	if err := sanitizeHostPaths(&req, prev); err == nil {
		t.Error("changing ca_file to a different path was accepted")
	}
}

// The inline PEM is the supported API route and must survive untouched.
func TestSanitizeHostPathsKeepsInlinePEM(t *testing.T) {
	req := clusterRequest{TLS: &config.TLSConfig{CAPem: "-----BEGIN CERTIFICATE-----\nx\n-----END CERTIFICATE-----\n"}}
	if err := sanitizeHostPaths(&req, nil); err != nil {
		t.Fatalf("inline ca_pem was rejected: %v", err)
	}
	if req.TLS.CAPem == "" {
		t.Error("ca_pem was cleared, want it preserved")
	}
}
