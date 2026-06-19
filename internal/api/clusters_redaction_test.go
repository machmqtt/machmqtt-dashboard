package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/noodlebit/machmqtt-dashboard/internal/config"
	"github.com/noodlebit/machmqtt-dashboard/internal/store"
)

func TestToClusterViewRedactsSecrets(t *testing.T) {
	c := store.Cluster{
		Name:        "prod",
		AdminToken:  "super-secret-admin",
		NATSConn:    &config.NATSConnConfig{URLs: []string{"nats://x"}, Username: "u", Password: "pw", Token: "tk"},
		MQTTBridges: []config.MQTTBridge{{Name: "b1", URL: "http://b1", BearerToken: "bt"}},
	}
	v := toClusterView(c)

	if !v.HasAdminToken {
		t.Error("HasAdminToken = false, want true")
	}
	if v.NATSConn == nil || !v.NATSConn.HasPassword || !v.NATSConn.HasToken {
		t.Error("NATS password/token not flagged as set")
	}
	if v.NATSConn.Username != "u" {
		t.Errorf("Username = %q, want u (non-secret, should be preserved)", v.NATSConn.Username)
	}
	if len(v.MQTTBridges) != 1 || !v.MQTTBridges[0].HasBearerToken {
		t.Error("bridge bearer token not flagged as set")
	}

	// The marshaled view must not contain any plaintext secret.
	b, _ := json.Marshal(v)
	for _, secret := range []string{"super-secret-admin", "pw", "tk", "bt"} {
		if strings.Contains(string(b), secret) {
			t.Errorf("clusterView leaks secret %q: %s", secret, b)
		}
	}
}

func TestMergeClusterSecretsKeepsExistingOnEmpty(t *testing.T) {
	prev := &store.Cluster{
		AdminToken:  "keep-admin",
		NATSConn:    &config.NATSConnConfig{Password: "keep-pw", Token: "keep-tk"},
		MQTTBridges: []config.MQTTBridge{{Name: "b1", BearerToken: "keep-bt"}},
	}
	incoming := &store.Cluster{
		AdminToken:  "",
		NATSConn:    &config.NATSConnConfig{Password: "", Token: ""},
		MQTTBridges: []config.MQTTBridge{{Name: "b1", BearerToken: ""}},
	}
	mergeClusterSecrets(incoming, prev)

	if incoming.AdminToken != "keep-admin" {
		t.Errorf("AdminToken = %q, want preserved keep-admin", incoming.AdminToken)
	}
	if incoming.NATSConn.Password != "keep-pw" || incoming.NATSConn.Token != "keep-tk" {
		t.Error("NATS secrets not preserved on blank input")
	}
	if incoming.MQTTBridges[0].BearerToken != "keep-bt" {
		t.Errorf("bearer token = %q, want preserved keep-bt", incoming.MQTTBridges[0].BearerToken)
	}
}

func TestMergeClusterSecretsOverwritesWhenProvided(t *testing.T) {
	prev := &store.Cluster{AdminToken: "old"}
	incoming := &store.Cluster{AdminToken: "new"}
	mergeClusterSecrets(incoming, prev)
	if incoming.AdminToken != "new" {
		t.Errorf("AdminToken = %q, want new (a provided value overwrites)", incoming.AdminToken)
	}
}

func TestToClusterViewRedactsURLCreds(t *testing.T) {
	c := store.Cluster{
		Name:        "prod",
		Servers:     []config.Server{{URL: "http://muser:mpass@mon:8222"}},
		NATSConn:    &config.NATSConnConfig{URLs: []string{"nats://nuser:npass@host:4222"}},
		MQTTBridges: []config.MQTTBridge{{Name: "b", URL: "http://buser:bpass@bridge:8080"}},
	}
	b, _ := json.Marshal(toClusterView(c))
	for _, secret := range []string{"mpass", "npass", "bpass"} {
		if strings.Contains(string(b), secret) {
			t.Errorf("view leaks credential embedded in a URL (%q): %s", secret, b)
		}
	}
	// Only the userinfo is stripped — the host must remain.
	if !strings.Contains(string(b), "host:4222") {
		t.Errorf("expected host preserved in redacted NATS URL: %s", b)
	}
}

// End-to-end: the edit UI gets a redacted cluster, PUTs it back with blank
// structured secrets and a userinfo-stripped URL, and the stored secrets must
// survive while the response stays redacted.
func TestUpdateClusterRoundTripPreservesSecrets(t *testing.T) {
	srv, st, _, token := setupTestServerWithStore(t)
	cl := &store.Cluster{
		Name:       "prod",
		Servers:    []config.Server{{URL: "http://nats:8222"}},
		AdminToken: "admin-secret",
		NATSConn: &config.NATSConnConfig{
			URLs:     []string{"nats://user:urlpass@host:4222"},
			Password: "struct-pass",
		},
	}
	st.CreateCluster(cl)

	body := `{
		"name":"prod-renamed",
		"servers":[{"url":"http://nats:8222"}],
		"admin_token":"",
		"nats_conn":{"urls":["nats://host:4222"],"password":""}
	}`
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, authedReq("PUT", "/api/admin/clusters/"+cl.ID, token, body))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body: %s", w.Code, w.Body.String())
	}

	respBody := w.Body.String()
	for _, secret := range []string{"admin-secret", "struct-pass", "urlpass"} {
		if strings.Contains(respBody, secret) {
			t.Errorf("update response leaked secret %q: %s", secret, respBody)
		}
	}

	got, _ := st.GetCluster(cl.ID)
	if got.Name != "prod-renamed" {
		t.Errorf("Name = %q, want prod-renamed", got.Name)
	}
	if got.AdminToken != "admin-secret" {
		t.Errorf("AdminToken = %q, want preserved", got.AdminToken)
	}
	if got.NATSConn.Password != "struct-pass" {
		t.Errorf("NATSConn.Password = %q, want preserved", got.NATSConn.Password)
	}
	if got.NATSConn.URLs[0] != "nats://user:urlpass@host:4222" {
		t.Errorf("NATSConn.URLs[0] = %q, want URL creds preserved", got.NATSConn.URLs[0])
	}
}

func TestListClustersResponseHasNoSecrets(t *testing.T) {
	srv, st, _, token := setupTestServerWithStore(t)
	st.CreateCluster(&store.Cluster{
		Name:       "prod",
		Servers:    []config.Server{{URL: "http://nats:8222"}},
		AdminToken: "leaky-secret-token",
		NATSConn:   &config.NATSConnConfig{URLs: []string{"nats://x"}, Password: "leaky-pw"},
	})

	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, authedReq("GET", "/api/admin/clusters", token, ""))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}

	body := w.Body.String()
	for _, secret := range []string{"leaky-secret-token", "leaky-pw"} {
		if strings.Contains(body, secret) {
			t.Errorf("cluster list leaked secret %q: %s", secret, body)
		}
	}
	if !strings.Contains(body, "has_admin_token") || !strings.Contains(body, "has_password") {
		t.Errorf("expected has_* booleans in response: %s", body)
	}
}
