package api

import (
	"fmt"
	"io"
	"net/http"
	"sort"

	"github.com/noodlebit/machmqtt-dashboard/internal/collector"
	"github.com/noodlebit/machmqtt-dashboard/internal/config"
)

// mqttAdminActions maps a dashboard action name to the MachMQTT bridge admin
// endpoint path. The allowlist is the only way to reach a bridge admin POST,
// so unknown actions are rejected before any request is made.
var mqttAdminActions = map[string]string{
	"kick-all-clients":         "/admin/kick-all-clients",
	"cluster-kick-client":      "/admin/cluster/kick-client",
	"cluster-kick-by-username": "/admin/cluster/kick-by-username",
	"cluster-kick-all":         "/admin/cluster/kick-all",
	"drain":                    "/admin/drain",
	"undrain":                  "/admin/undrain",
	"reload":                   "/admin/reload",
}

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
			f := collector.NewMQTTBridgeFetcher(b.URL, b.Name, envCfg.ResolveBridgeToken(b.BearerToken))
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

// handleMQTTCluster proxies GET /admin/cluster, relaying the bridge's "feature
// unavailable" responses (409 not clustered, 404 unsupported on older bridges)
// as a clean {available:false, reason} payload instead of an error.
func (s *Server) handleMQTTCluster(w http.ResponseWriter, r *http.Request) {
	env := r.PathValue("env")
	bridgeName := r.PathValue("bridge")

	bridge := s.findBridge(env, bridgeName)
	if bridge == nil {
		http.Error(w, `{"error":"bridge not found"}`, http.StatusNotFound)
		return
	}

	f := collector.NewMQTTBridgeFetcher(bridge.URL, bridge.Name, bridge.BearerToken)
	cluster, code, err := f.FetchCluster(r.Context())
	if err != nil {
		s.log.Warn("mqtt bridge request failed", "err", err)
		http.Error(w, `{"error":"bridge request failed"}`, http.StatusBadGateway)
		return
	}
	switch code {
	case http.StatusOK:
		writeJSON(w, map[string]any{"available": true, "cluster": cluster})
	case http.StatusConflict:
		writeJSON(w, map[string]any{"available": false, "reason": "clustering is not enabled on this bridge (set cluster.enabled: true)"})
	case http.StatusNotFound:
		writeJSON(w, map[string]any{"available": false, "reason": "this bridge version does not expose the cluster API"})
	case http.StatusUnauthorized, http.StatusForbidden:
		writeJSON(w, map[string]any{"available": false, "reason": "admin authentication failed — set the bridge admin token in the environment config"})
	default:
		writeJSON(w, map[string]any{"available": false, "reason": fmt.Sprintf("bridge returned HTTP %d", code)})
	}
}

// handleMQTTClusterInspect proxies GET /admin/cluster/inspect?client_id=, used
// to locate a single client across the cluster.
func (s *Server) handleMQTTClusterInspect(w http.ResponseWriter, r *http.Request) {
	env := r.PathValue("env")
	bridgeName := r.PathValue("bridge")
	clientID := r.URL.Query().Get("client_id")
	if clientID == "" {
		http.Error(w, `{"error":"client_id query parameter is required"}`, http.StatusBadRequest)
		return
	}

	bridge := s.findBridge(env, bridgeName)
	if bridge == nil {
		http.Error(w, `{"error":"bridge not found"}`, http.StatusNotFound)
		return
	}

	f := collector.NewMQTTBridgeFetcher(bridge.URL, bridge.Name, bridge.BearerToken)
	ins, code, err := f.FetchClusterInspect(r.Context(), clientID)
	if err != nil {
		s.log.Warn("mqtt bridge request failed", "err", err)
		http.Error(w, `{"error":"bridge request failed"}`, http.StatusBadGateway)
		return
	}
	switch code {
	case http.StatusOK:
		writeJSON(w, map[string]any{"found": true, "inspect": ins})
	case http.StatusNotFound:
		writeJSON(w, map[string]any{"found": false, "reason": "client not found in the cluster"})
	case http.StatusConflict:
		writeJSON(w, map[string]any{"found": false, "reason": "clustering is not enabled on this bridge"})
	case http.StatusTooManyRequests:
		writeJSON(w, map[string]any{"found": false, "reason": "bridge busy with concurrent inspects — retry shortly"})
	case http.StatusUnauthorized, http.StatusForbidden:
		writeJSON(w, map[string]any{"found": false, "reason": "admin authentication failed — set the bridge admin token in the environment config"})
	default:
		writeJSON(w, map[string]any{"found": false, "reason": fmt.Sprintf("bridge returned HTTP %d", code)})
	}
}

// handleMQTTAdminAction proxies a state-changing POST to a bridge admin
// endpoint. Admin-role only (gated by AdminMiddleware on the route). It
// forwards the request body (for cluster-kick-client / -by-username) and
// relays the bridge's status + body, so the UI surfaces 403 (endpoint
// disabled), 409 (cluster not enabled) and 404 (unsupported) precisely.
func (s *Server) handleMQTTAdminAction(w http.ResponseWriter, r *http.Request) {
	env := r.PathValue("env")
	bridgeName := r.PathValue("bridge")
	action := r.PathValue("action")

	path, ok := mqttAdminActions[action]
	if !ok {
		http.Error(w, `{"error":"unknown admin action"}`, http.StatusBadRequest)
		return
	}

	bridge := s.findBridge(env, bridgeName)
	if bridge == nil {
		http.Error(w, `{"error":"bridge not found"}`, http.StatusNotFound)
		return
	}

	var body []byte
	if r.Body != nil {
		body, _ = io.ReadAll(io.LimitReader(r.Body, 4096))
	}

	f := collector.NewMQTTBridgeFetcher(bridge.URL, bridge.Name, bridge.BearerToken)
	code, respBody, err := f.PostAdmin(r.Context(), path, body)
	if err != nil {
		s.log.Warn("mqtt bridge admin action failed", "action", action, "err", err)
		http.Error(w, `{"error":"bridge request failed"}`, http.StatusBadGateway)
		return
	}
	s.log.Info("mqtt admin action proxied", "env", env, "bridge", bridgeName, "action", action, "status", code)

	if code == 0 {
		code = http.StatusBadGateway
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	if len(respBody) > 0 {
		_, _ = w.Write(respBody)
	}
}

// resolvedBridge holds the URL and auth info needed to talk to a bridge.
type resolvedBridge struct {
	URL         string
	Name        string
	BearerToken string
}

// findBridge looks up a bridge by name from both config and auto-discovered
// bridges, resolving the admin bearer token (per-bridge override, else the
// environment-level default) so authed reads and admin actions can reach it.
func (s *Server) findBridge(env, name string) *resolvedBridge {
	envCfg := s.envConfig(env)
	resolveToken := func(perBridge string) string {
		if envCfg == nil {
			return perBridge
		}
		return envCfg.ResolveBridgeToken(perBridge)
	}

	// Check manually configured bridges first.
	for _, b := range s.mqttBridges(env) {
		if b.Name == name {
			return &resolvedBridge{URL: b.URL, Name: b.Name, BearerToken: resolveToken(b.BearerToken)}
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
				return &resolvedBridge{URL: b.AdminURL, Name: displayName, BearerToken: resolveToken("")}
			}
		}
	}

	return nil
}
