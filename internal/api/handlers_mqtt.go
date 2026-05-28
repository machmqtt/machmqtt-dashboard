package api

import (
	"net/http"
	"sort"

	"github.com/noodlebit/nats-dashboard/internal/collector"
	"github.com/noodlebit/nats-dashboard/internal/config"
)

func (s *Server) envConfig(env string) *config.Environment {
	for i := range s.cfg.Environments {
		if s.cfg.Environments[i].Name == env {
			return &s.cfg.Environments[i]
		}
	}
	return nil
}

func (s *Server) mqttBridges(env string) []config.MQTTBridge {
	e := s.envConfig(env)
	if e == nil {
		return nil
	}
	return e.MQTTBridges
}

func (s *Server) handleMQTTBridges(w http.ResponseWriter, r *http.Request) {
	env := r.PathValue("env")
	envCfg := s.envConfig(env)
	if envCfg == nil {
		writeJSON(w, map[string]any{"bridges": []any{}})
		return
	}

	// Use cached discovery results from the collector poll loop.
	discovered := s.manager.MQTTBridges(env)
	if discovered == nil {
		discovered = []collector.MQTTBridgeInstance{}
	}

	// Also include manually configured bridges that weren't auto-discovered.
	for _, b := range envCfg.MQTTBridges {
		found := false
		for i := range discovered {
			if discovered[i].AdminURL == b.URL {
				found = true
				if b.Name != "" {
					discovered[i].ConfiguredName = b.Name
				}
				break
			}
		}
		if !found {
			f := collector.NewMQTTBridgeFetcher(b.URL, b.Name, b.BearerToken)
			status := f.FetchStatus(r.Context())
			discovered = append(discovered, collector.MQTTBridgeInstance{
				IP:             b.URL,
				AdminURL:       b.URL,
				ConfiguredName: b.Name,
				Status:         status,
				Reachable:      status.Error == "",
			})
		}
	}

	sort.Slice(discovered, func(i, j int) bool {
		ni := discovered[i].ConfiguredName
		if ni == "" {
			ni = discovered[i].IP
		}
		nj := discovered[j].ConfiguredName
		if nj == "" {
			nj = discovered[j].IP
		}
		return ni < nj
	})

	writeJSON(w, map[string]any{"bridges": discovered})
}

func (s *Server) handleMQTTConnz(w http.ResponseWriter, r *http.Request) {
	env := r.PathValue("env")
	bridgeName := r.PathValue("bridge")
	q := r.URL.Query()
	limit := clampInt(q.Get("limit"), 50, 10000)
	offset := clampInt(q.Get("offset"), 0, 100000)

	bridge := s.findBridge(env, bridgeName)
	if bridge == nil {
		http.Error(w, `{"error":"bridge not found"}`, http.StatusNotFound)
		return
	}

	f := collector.NewMQTTBridgeFetcher(bridge.URL, bridge.Name, bridge.BearerToken)
	connz, err := f.FetchConnz(r.Context(), limit, offset)
	if err != nil {
		writeJSON(w, map[string]any{
			"error":           "connz not available",
			"detail":          "The bridge's /connz endpoint returned an error. Set clients_snapshot_interval in the bridge's admin config to enable it.",
			"connections":     []any{},
			"num_connections": 0,
			"total":           0,
		})
		return
	}
	writeJSON(w, connz)
}

func (s *Server) handleMQTTClient(w http.ResponseWriter, r *http.Request) {
	env := r.PathValue("env")
	bridgeName := r.PathValue("bridge")
	clientID := r.PathValue("client")

	bridge := s.findBridge(env, bridgeName)
	if bridge == nil {
		http.Error(w, `{"error":"bridge not found"}`, http.StatusNotFound)
		return
	}

	f := collector.NewMQTTBridgeFetcher(bridge.URL, bridge.Name, bridge.BearerToken)
	connz, err := f.FetchConnzClient(r.Context(), clientID)
	if err != nil {
		s.log.Warn("mqtt bridge request failed", "err", err)
		http.Error(w, `{"error":"bridge request failed"}`, http.StatusBadGateway)
		return
	}
	if len(connz.Connections) == 0 {
		http.Error(w, `{"error":"client not found"}`, http.StatusNotFound)
		return
	}
	writeJSON(w, connz.Connections[0])
}

func (s *Server) handleMQTTDiag(w http.ResponseWriter, r *http.Request) {
	env := r.PathValue("env")
	bridgeName := r.PathValue("bridge")

	bridge := s.findBridge(env, bridgeName)
	if bridge == nil {
		http.Error(w, `{"error":"bridge not found"}`, http.StatusNotFound)
		return
	}

	f := collector.NewMQTTBridgeFetcher(bridge.URL, bridge.Name, bridge.BearerToken)
	diag, err := f.FetchDiagNATS(r.Context())
	if err != nil {
		s.log.Warn("mqtt bridge request failed", "err", err)
		http.Error(w, `{"error":"bridge request failed"}`, http.StatusBadGateway)
		return
	}
	writeJSON(w, diag)
}

func (s *Server) handleMQTTPool(w http.ResponseWriter, r *http.Request) {
	env := r.PathValue("env")
	bridgeName := r.PathValue("bridge")

	bridge := s.findBridge(env, bridgeName)
	if bridge == nil {
		http.Error(w, `{"error":"bridge not found"}`, http.StatusNotFound)
		return
	}

	f := collector.NewMQTTBridgeFetcher(bridge.URL, bridge.Name, bridge.BearerToken)
	pool, err := f.FetchPool(r.Context())
	if err != nil {
		s.log.Warn("mqtt bridge request failed", "err", err)
		http.Error(w, `{"error":"bridge request failed"}`, http.StatusBadGateway)
		return
	}
	writeJSON(w, pool)
}

func (s *Server) handleMQTTBridgeDiag(w http.ResponseWriter, r *http.Request) {
	env := r.PathValue("env")
	bridgeName := r.PathValue("bridge")

	bridge := s.findBridge(env, bridgeName)
	if bridge == nil {
		http.Error(w, `{"error":"bridge not found"}`, http.StatusNotFound)
		return
	}

	f := collector.NewMQTTBridgeFetcher(bridge.URL, bridge.Name, bridge.BearerToken)
	diag, err := f.FetchDiag(r.Context())
	if err != nil {
		s.log.Warn("mqtt bridge request failed", "err", err)
		http.Error(w, `{"error":"bridge request failed"}`, http.StatusBadGateway)
		return
	}
	writeJSON(w, diag)
}

func (s *Server) handleMQTTLicense(w http.ResponseWriter, r *http.Request) {
	env := r.PathValue("env")
	bridgeName := r.PathValue("bridge")

	bridge := s.findBridge(env, bridgeName)
	if bridge == nil {
		http.Error(w, `{"error":"bridge not found"}`, http.StatusNotFound)
		return
	}

	f := collector.NewMQTTBridgeFetcher(bridge.URL, bridge.Name, bridge.BearerToken)
	license, err := f.FetchLicense(r.Context())
	if err != nil {
		s.log.Warn("mqtt bridge request failed", "err", err)
		http.Error(w, `{"error":"bridge request failed"}`, http.StatusBadGateway)
		return
	}
	writeJSON(w, license)
}

func (s *Server) handleMQTTMetrics(w http.ResponseWriter, r *http.Request) {
	env := r.PathValue("env")
	bridgeName := r.PathValue("bridge")

	bridge := s.findBridge(env, bridgeName)
	if bridge == nil {
		http.Error(w, `{"error":"bridge not found"}`, http.StatusNotFound)
		return
	}

	f := collector.NewMQTTBridgeFetcher(bridge.URL, bridge.Name, bridge.BearerToken)
	metrics, err := f.FetchMetrics(r.Context())
	if err != nil {
		s.log.Warn("mqtt bridge request failed", "err", err)
		http.Error(w, `{"error":"bridge request failed"}`, http.StatusBadGateway)
		return
	}
	writeJSON(w, metrics)
}

// resolvedBridge holds the URL and auth info needed to talk to a bridge.
type resolvedBridge struct {
	URL         string
	Name        string
	BearerToken string
}

// findBridge looks up a bridge by name from both config and auto-discovered bridges.
func (s *Server) findBridge(env, name string) *resolvedBridge {
	// Check manually configured bridges first.
	for _, b := range s.mqttBridges(env) {
		if b.Name == name {
			return &resolvedBridge{URL: b.URL, Name: b.Name, BearerToken: b.BearerToken}
		}
	}

	// Check auto-discovered bridges (match by configured name, IP, or admin URL).
	for _, b := range s.manager.MQTTBridges(env) {
		displayName := b.ConfiguredName
		if displayName == "" && b.Status != nil {
			displayName = b.Status.Name
		}
		if displayName == "" {
			displayName = "mqtt@" + b.IP
		}
		if displayName == name || b.IP == name || b.AdminURL == name {
			if b.AdminURL != "" {
				return &resolvedBridge{URL: b.AdminURL, Name: displayName}
			}
		}
	}

	return nil
}
