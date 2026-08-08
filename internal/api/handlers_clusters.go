package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/noodlebit/machmqtt-dashboard/internal/collector"
	"github.com/noodlebit/machmqtt-dashboard/internal/config"
	"github.com/noodlebit/machmqtt-dashboard/internal/store"
)

// writeClusterError maps a store cluster error to an HTTP status and a safe,
// non-leaking message. Not-found becomes 404, known validation errors become
// 400 with their (safe) message, and anything else is logged and returned as a
// generic 500 so raw DB/driver errors are never sent to the client.
func (s *Server) writeClusterError(w http.ResponseWriter, op string, err error) {
	switch {
	case errors.Is(err, store.ErrClusterNotFound):
		http.Error(w, `{"error":"cluster not found"}`, http.StatusNotFound)
	case errors.Is(err, store.ErrClusterNameRequired):
		writeError(w, http.StatusBadRequest, store.ErrClusterNameRequired.Error())
	case errors.Is(err, store.ErrClusterServersRequired):
		writeError(w, http.StatusBadRequest, store.ErrClusterServersRequired.Error())
	default:
		s.log.Warn(op, "err", err)
		http.Error(w, `{"error":"internal error"}`, http.StatusInternalServerError)
	}
}

// redactURLCreds strips any userinfo (user:pass@) from a URL so credentials
// embedded directly in a connection string don't leak in API responses. URLs
// without userinfo are returned unchanged (exact formatting preserved).
func redactURLCreds(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.User == nil {
		return raw
	}
	u.User = nil
	return u.String()
}

func serverURLs(servers []config.Server) []string {
	out := make([]string, len(servers))
	for i, s := range servers {
		out[i] = s.URL
	}
	return out
}

func bridgeURLs(bridges []config.MQTTBridge) []string {
	out := make([]string, len(bridges))
	for i, b := range bridges {
		out[i] = b.URL
	}
	return out
}

// restoreURLCreds grafts userinfo from a previously-stored URL with the same host
// back onto an incoming URL that has none — the write-side counterpart to
// redactURLCreds, so an edit that round-trips a redacted URL keeps its creds.
func restoreURLCreds(incoming string, prev []string) string {
	in, err := url.Parse(incoming)
	if err != nil || in.User != nil {
		return incoming // unparseable, or the admin supplied fresh credentials
	}
	for _, p := range prev {
		if pu, err := url.Parse(p); err == nil && pu.User != nil && pu.Host == in.Host {
			in.User = pu.User
			return in.String()
		}
	}
	return incoming
}

// clusterRequest is the JSON body for create and update cluster endpoints.
type clusterRequest struct {
	Name          string                      `json:"name"`
	Servers       []config.Server             `json:"servers"`
	MQTTBridges   []config.MQTTBridge         `json:"mqtt_bridges"`
	MQTTDiscovery *config.MQTTDiscoveryConfig `json:"mqtt_discovery,omitempty"`
	TLS           *config.TLSConfig           `json:"tls,omitempty"`
	AdminToken    string                      `json:"admin_token,omitempty"`
	NATSConn      *config.NATSConnConfig      `json:"nats_conn,omitempty"`
}

// --- Response shaping: secrets never leave the server ---
//
// Cluster config holds several secrets (admin bearer token, NATS auth, per-bridge
// bearer tokens). Even though these endpoints are admin-only, echoing the stored
// plaintext back into API responses puts it in browser memory, devtools, and any
// proxy/response logging. The *View types below replace every secret with a
// boolean ("is one set?") so the management UI can show "•••• set" without ever
// receiving the value. On write, a blank secret means "keep the stored one"
// (see mergeClusterSecrets), so the round-trip never needs the plaintext.

type natsConnView struct {
	URLs          []string          `json:"urls"`
	Username      string            `json:"username,omitempty"`
	SubjectPrefix string            `json:"subject_prefix,omitempty"`
	SYSCollection bool              `json:"sys_collection,omitempty"`
	TLS           *config.TLSConfig `json:"tls,omitempty"`
	HasPassword   bool              `json:"has_password"`
	HasToken      bool              `json:"has_token"`
	HasNKey       bool              `json:"has_nkey"`
	HasCreds      bool              `json:"has_creds"`
}

type mqttBridgeView struct {
	Name           string `json:"name"`
	URL            string `json:"url"`
	HasBearerToken bool   `json:"has_bearer_token"`
}

type clusterView struct {
	ID            string                      `json:"id"`
	Name          string                      `json:"name"`
	Servers       []config.Server             `json:"servers"`
	MQTTBridges   []mqttBridgeView            `json:"mqtt_bridges"`
	MQTTDiscovery *config.MQTTDiscoveryConfig `json:"mqtt_discovery,omitempty"`
	TLS           *config.TLSConfig           `json:"tls,omitempty"`
	HasAdminToken bool                        `json:"has_admin_token"`
	NATSConn      *natsConnView               `json:"nats_conn,omitempty"`
	CreatedAt     time.Time                   `json:"created_at"`
}

// toClusterView redacts a stored cluster into its secret-free API response shape.
func toClusterView(c store.Cluster) clusterView {
	v := clusterView{
		ID:            c.ID,
		Name:          c.Name,
		MQTTDiscovery: c.MQTTDiscovery,
		TLS:           c.TLS,
		HasAdminToken: c.AdminToken != "",
		CreatedAt:     c.CreatedAt,
	}
	for _, s := range c.Servers {
		v.Servers = append(v.Servers, config.Server{URL: redactURLCreds(s.URL)})
	}
	for _, b := range c.MQTTBridges {
		v.MQTTBridges = append(v.MQTTBridges, mqttBridgeView{
			Name:           b.Name,
			URL:            redactURLCreds(b.URL),
			HasBearerToken: b.BearerToken != "",
		})
	}
	if n := c.NATSConn; n != nil {
		urls := make([]string, len(n.URLs))
		for i, u := range n.URLs {
			urls[i] = redactURLCreds(u)
		}
		v.NATSConn = &natsConnView{
			URLs:          urls,
			Username:      n.Username,
			SubjectPrefix: n.SubjectPrefix,
			SYSCollection: n.SYSCollection,
			TLS:           n.TLS,
			HasPassword:   n.Password != "",
			HasToken:      n.Token != "",
			HasNKey:       n.NKey != "",
			HasCreds:      n.CredsFile != "",
		}
	}
	return v
}

// sanitizeHostPaths keeps client-chosen filesystem paths out of a cluster.
//
// ca_file and creds_file name files on the dashboard host. Honouring them over
// HTTP would let the admin role enumerate that filesystem — the resulting error
// distinguishes "no such file" from "permission denied" from "is a directory" —
// and stall or exhaust the process by naming a device file such as /dev/zero.
// The admin role is frequently an LDAP/OIDC-mapped identity with no shell on the
// host, so that is a real privilege boundary.
//
// These fields are therefore config-file-only and the stored value always wins:
// prev supplies it on update, and there is nothing to inherit on create. An
// error is returned only when a client tries to *change* a path, so an edit that
// round-trips the value the UI was shown still succeeds.
func sanitizeHostPaths(req *clusterRequest, prev *store.Cluster) error {
	var (
		prevTLS   *config.TLSConfig
		prevNATS  *config.TLSConfig
		prevCreds string
	)
	if prev != nil {
		prevTLS = prev.TLS
		if prev.NATSConn != nil {
			prevNATS = prev.NATSConn.TLS
			prevCreds = prev.NATSConn.CredsFile
		}
	}

	tlsCfg, err := sanitizedTLS(req.TLS, prevTLS, "tls.ca_file")
	if err != nil {
		return err
	}
	req.TLS = tlsCfg

	if req.NATSConn == nil {
		return nil
	}
	natsTLS, err := sanitizedTLS(req.NATSConn.TLS, prevNATS, "nats_conn.tls.ca_file")
	if err != nil {
		return err
	}
	req.NATSConn.TLS = natsTLS
	// Unlike ca_file, creds_file is never echoed back (only has_creds is), so any
	// non-empty incoming value is an attempt to set it. A blank one means "keep
	// the stored value", which mergeClusterSecrets restores.
	if req.NATSConn.CredsFile != "" && req.NATSConn.CredsFile != prevCreds {
		return fmt.Errorf("nats_conn.creds_file cannot be set through the API; declare it in the config file")
	}
	req.NATSConn.CredsFile = prevCreds
	return nil
}

// sanitizedTLS returns a TLS config whose CA comes from the stored cluster
// rather than the request body, pointing the caller at the inline PEM
// alternative when they try to change the path.
//
// It deliberately builds a new value instead of clearing the field on the
// incoming one, and carries the stored CA forward as bytes (the store resolves
// ca_file into ca_pem on read), so a request-derived config never holds a path
// that anything downstream would open.
func sanitizedTLS(incoming, stored *config.TLSConfig, field string) (*config.TLSConfig, error) {
	if incoming == nil {
		return nil, nil
	}
	storedPath, storedPem := "", ""
	if stored != nil {
		storedPath, storedPem = stored.CAFile, stored.CAPem
	}
	if incoming.CAFile != "" && incoming.CAFile != storedPath {
		return nil, fmt.Errorf("%s cannot be set through the API; supply the certificate inline with %s, or declare the path in the config file",
			field, strings.Replace(field, "ca_file", "ca_pem", 1))
	}
	// A blank ca_pem means "keep what is configured", so the operator's CA
	// survives an edit that didn't touch it.
	caPem := incoming.CAPem
	if caPem == "" {
		caPem = storedPem
	}
	return &config.TLSConfig{
		CAFile:   storedPath,
		CAPem:    caPem,
		Insecure: incoming.Insecure,
	}, nil
}

// mergeClusterSecrets fills empty secret fields in cl from the previously-stored
// cluster, so the edit UI (which never receives plaintext) can submit a blank
// field to mean "leave unchanged". A non-empty incoming value overwrites.
func mergeClusterSecrets(cl, prev *store.Cluster) {
	if prev == nil {
		return
	}
	if cl.AdminToken == "" {
		cl.AdminToken = prev.AdminToken
	}
	// Restore any userinfo redacted out of connection URLs on read.
	for i := range cl.Servers {
		cl.Servers[i].URL = restoreURLCreds(cl.Servers[i].URL, serverURLs(prev.Servers))
	}
	for i := range cl.MQTTBridges {
		cl.MQTTBridges[i].URL = restoreURLCreds(cl.MQTTBridges[i].URL, bridgeURLs(prev.MQTTBridges))
	}
	if cl.NATSConn != nil && prev.NATSConn != nil {
		for i := range cl.NATSConn.URLs {
			cl.NATSConn.URLs[i] = restoreURLCreds(cl.NATSConn.URLs[i], prev.NATSConn.URLs)
		}
		if cl.NATSConn.Password == "" {
			cl.NATSConn.Password = prev.NATSConn.Password
		}
		if cl.NATSConn.Token == "" {
			cl.NATSConn.Token = prev.NATSConn.Token
		}
		if cl.NATSConn.NKey == "" {
			cl.NATSConn.NKey = prev.NATSConn.NKey
		}
		if cl.NATSConn.CredsFile == "" {
			cl.NATSConn.CredsFile = prev.NATSConn.CredsFile
		}
	}
	// Per-bridge bearer tokens, matched by the bridge name (the UI's stable key).
	if len(cl.MQTTBridges) > 0 && len(prev.MQTTBridges) > 0 {
		prevTokens := make(map[string]string, len(prev.MQTTBridges))
		for _, b := range prev.MQTTBridges {
			if b.BearerToken != "" {
				prevTokens[b.Name] = b.BearerToken
			}
		}
		for i := range cl.MQTTBridges {
			if cl.MQTTBridges[i].BearerToken == "" {
				if t, ok := prevTokens[cl.MQTTBridges[i].Name]; ok {
					cl.MQTTBridges[i].BearerToken = t
				}
			}
		}
	}
}

// handleListClusters returns all clusters with their full configuration (for
// the admin management UI).
func (s *Server) handleListClusters(w http.ResponseWriter, r *http.Request) {
	clusters, err := s.store.ListClusters()
	if err != nil {
		http.Error(w, `{"error":"failed to list clusters"}`, http.StatusInternalServerError)
		return
	}
	views := make([]clusterView, len(clusters))
	for i, c := range clusters {
		views[i] = toClusterView(c)
	}
	writeJSON(w, map[string]any{"clusters": views})
}

// handleCreateCluster creates a new cluster, starts its collector, and returns
// the created cluster with HTTP 201.
func (s *Server) handleCreateCluster(w http.ResponseWriter, r *http.Request) {
	var req clusterRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid request body"}`, http.StatusBadRequest)
		return
	}

	// A new cluster has no stored paths to inherit, so any supplied one is a
	// client-chosen host path and is refused.
	if err := sanitizeHostPaths(&req, nil); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	cl := &store.Cluster{
		Name:          req.Name,
		Servers:       req.Servers,
		MQTTBridges:   req.MQTTBridges,
		MQTTDiscovery: req.MQTTDiscovery,
		TLS:           req.TLS,
		AdminToken:    req.AdminToken,
		NATSConn:      req.NATSConn,
	}

	// Validate the TLS config (e.g. the inline CA must parse) before persisting.
	env := cl.ToEnvironment()
	if _, err := collector.NewFetcher(env.TLS); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	if err := s.store.CreateCluster(cl); err != nil {
		s.writeClusterError(w, "create cluster", err)
		return
	}

	// Start the collector. If this fails, roll back the store row.
	if err := s.manager.AddCluster(*cl); err != nil {
		if rollbackErr := s.store.DeleteCluster(cl.ID); rollbackErr != nil {
			s.log.Error("rollback failed cluster creation", "id", cl.ID, "err", rollbackErr)
		}
		http.Error(w, `{"error":"failed to start cluster collector"}`, http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusCreated)
	writeJSON(w, toClusterView(*cl))
}

// handleUpdateCluster updates an existing cluster's config and applies it live.
func (s *Server) handleUpdateCluster(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	var req clusterRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid request body"}`, http.StatusBadRequest)
		return
	}

	// The stored cluster supplies both the config-file-only host paths and any
	// secret the edit UI never received, so it has to be loaded before the
	// request body is turned into a cluster.
	prev, err := s.store.GetCluster(id)
	if err != nil {
		prev = nil
	}

	if err := sanitizeHostPaths(&req, prev); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	cl := &store.Cluster{
		ID:            id,
		Name:          req.Name,
		Servers:       req.Servers,
		MQTTBridges:   req.MQTTBridges,
		MQTTDiscovery: req.MQTTDiscovery,
		TLS:           req.TLS,
		AdminToken:    req.AdminToken,
		NATSConn:      req.NATSConn,
	}

	// Blank secrets mean "keep the stored value" — the edit UI never receives the
	// plaintext (see toClusterView), so preserve secrets it didn't resend.
	if prev != nil {
		mergeClusterSecrets(cl, prev)
	}

	// Validate TLS before persisting.
	env := cl.ToEnvironment()
	if _, err := collector.NewFetcher(env.TLS); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	if err := s.store.UpdateCluster(cl); err != nil {
		s.writeClusterError(w, "update cluster", err)
		return
	}

	if err := s.manager.UpdateCluster(*cl); err != nil {
		s.log.Warn("update cluster collector", "id", id, "err", err)
		// Non-fatal: store is updated; collector will be refreshed on next restart.
	}

	writeJSON(w, toClusterView(*cl))
}

// handleDeleteCluster removes a cluster, stops its collector, and cascades all
// associated data rows.
func (s *Server) handleDeleteCluster(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	// Stop the live collector first so it doesn't write new data while we delete.
	s.manager.RemoveCluster(id)

	if err := s.store.DeleteCluster(id); err != nil {
		s.writeClusterError(w, "delete cluster", err)
		return
	}

	writeJSON(w, map[string]any{"ok": true})
}
