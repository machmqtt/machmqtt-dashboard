package api

import (
	"encoding/json"
	"net/http"

	"github.com/noodlebit/machmqtt-dashboard/internal/collector"
	"github.com/noodlebit/machmqtt-dashboard/internal/config"
	"github.com/noodlebit/machmqtt-dashboard/internal/store"
)

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

// handleListClusters returns all clusters with their full configuration (for
// the admin management UI).
func (s *Server) handleListClusters(w http.ResponseWriter, r *http.Request) {
	clusters, err := s.store.ListClusters()
	if err != nil {
		http.Error(w, `{"error":"failed to list clusters"}`, http.StatusInternalServerError)
		return
	}
	if clusters == nil {
		clusters = []store.Cluster{}
	}
	writeJSON(w, map[string]any{"clusters": clusters})
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
	writeJSON(w, cl)
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

	writeJSON(w, cl)
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
