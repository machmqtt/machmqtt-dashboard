package api

import (
	"encoding/json"
	"net/http"
	"net/url"
	"time"

	"github.com/noodlebit/machmqtt-dashboard/internal/collector"
	"github.com/noodlebit/machmqtt-dashboard/internal/config"
	"github.com/noodlebit/machmqtt-dashboard/internal/store"
)

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

	cl := &store.Cluster{
		Name:          req.Name,
		Servers:       req.Servers,
		MQTTBridges:   req.MQTTBridges,
		MQTTDiscovery: req.MQTTDiscovery,
		TLS:           req.TLS,
		AdminToken:    req.AdminToken,
		NATSConn:      req.NATSConn,
	}

	// Validate the TLS config (e.g. CA file must be readable) before persisting.
	env := cl.ToEnvironment()
	if _, err := collector.NewFetcher(env.TLS); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	if err := s.store.CreateCluster(cl); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	// Start the collector. If this fails, roll back the store row.
	if err := s.manager.AddCluster(*cl); err != nil {
		s.store.DeleteCluster(cl.ID)
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
	if prev, err := s.store.GetCluster(id); err == nil {
		mergeClusterSecrets(cl, prev)
	}

	// Validate TLS before persisting.
	env := cl.ToEnvironment()
	if _, err := collector.NewFetcher(env.TLS); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	if err := s.store.UpdateCluster(cl); err != nil {
		status := http.StatusBadRequest
		if err.Error() == "cluster not found" {
			status = http.StatusNotFound
		}
		writeError(w, status, err.Error())
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
		writeError(w, http.StatusNotFound, err.Error())
		return
	}

	writeJSON(w, map[string]any{"ok": true})
}
