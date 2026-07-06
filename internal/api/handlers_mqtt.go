package api

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/noodlebit/machmqtt-dashboard/internal/collector"
	"github.com/noodlebit/machmqtt-dashboard/internal/config"
)

// bridgeStatusCache memoizes live MachMQTT bridge admin-API status lookups for
// configured-but-undiscovered bridges, so repeated fleet-listing requests don't
// each pay the bridge round-trip. Auto-discovered bridges are served from the
// collector's poll cache and never reach this path. Keys are configured bridge
// URLs (a small, bounded set), so the map does not grow unbounded.
type bridgeStatusCache struct {
	mu      sync.Mutex
	ttl     time.Duration
	entries map[string]bridgeStatusEntry
}

type bridgeStatusEntry struct {
	status    *collector.MQTTBridgeStatus
	fetchedAt time.Time
}

func newBridgeStatusCache(ttl time.Duration) *bridgeStatusCache {
	return &bridgeStatusCache{ttl: ttl, entries: make(map[string]bridgeStatusEntry)}
}

// get returns a fresh cached status, or calls fetch (without the lock held, so
// requests for different bridges don't serialize) and stores the result.
func (c *bridgeStatusCache) get(key string, fetch func() *collector.MQTTBridgeStatus) *collector.MQTTBridgeStatus {
	now := time.Now()
	c.mu.Lock()
	if e, ok := c.entries[key]; ok && now.Sub(e.fetchedAt) < c.ttl {
		c.mu.Unlock()
		return e.status
	}
	c.mu.Unlock()

	status := fetch()

	c.mu.Lock()
	c.entries[key] = bridgeStatusEntry{status: status, fetchedAt: now}
	c.mu.Unlock()
	return status
}

// bridgeRespCache memoizes the encoded JSON body of a live bridge read for a
// short TTL, so multiple browser sessions viewing the same bridge tab (and their
// auto-refresh ticks) collapse into a single admin-API round-trip within the
// window instead of each hitting the monitored bridge. Keys embed the bridge URL
// plus any query params, so distinct pages/filters don't collide. The map is
// pruned lazily on write, so it stays bounded by the number of distinct
// bridge+param combinations viewed within the TTL.
type bridgeRespCache struct {
	mu      sync.Mutex
	ttl     time.Duration
	entries map[string]bridgeRespEntry
}

type bridgeRespEntry struct {
	body      []byte
	fetchedAt time.Time
}

func newBridgeRespCache(ttl time.Duration) *bridgeRespCache {
	return &bridgeRespCache{ttl: ttl, entries: make(map[string]bridgeRespEntry)}
}

// get returns a fresh cached body, or nil on a miss/expiry.
func (c *bridgeRespCache) get(key string) []byte {
	c.mu.Lock()
	defer c.mu.Unlock()
	if e, ok := c.entries[key]; ok && time.Since(e.fetchedAt) < c.ttl {
		return e.body
	}
	return nil
}

// put stores body under key and evicts any entries older than the TTL.
func (c *bridgeRespCache) put(key string, body []byte) {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := time.Now()
	for k, e := range c.entries {
		if now.Sub(e.fetchedAt) >= c.ttl {
			delete(c.entries, k)
		}
	}
	c.entries[key] = bridgeRespEntry{body: body, fetchedAt: now}
}

// serveCachedBridgeJSON serves a bridge read from bridgeJSON, collapsing repeated
// viewer requests within the TTL into one live fetch. produce performs the live
// fetch and returns the value to encode plus whether the result is cacheable
// (successful reads are cached; error/placeholder payloads are not).
func (s *Server) serveCachedBridgeJSON(w http.ResponseWriter, key string, produce func() (v any, cacheable bool)) {
	if body := s.bridgeJSON.get(key); body != nil {
		writeRawJSON(w, body)
		return
	}
	v, cacheable := produce()
	body, err := json.Marshal(v)
	if err != nil {
		http.Error(w, `{"error":"internal error"}`, http.StatusInternalServerError)
		return
	}
	if cacheable {
		s.bridgeJSON.put(key, body)
	}
	writeRawJSON(w, body)
}

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
	return s.manager.ClusterConfig(env)
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
	// Match an existing discovered bridge by admin URL OR by name, so a configured
	// bridge that names a push-discovered instance (which has no admin URL of its
	// own) merges into it — adopting the configured admin URL — instead of adding a
	// duplicate fleet card.
	for _, b := range envCfg.MQTTBridges {
		found := false
		for i := range discovered {
			if discovered[i].AdminURL == b.URL || (b.Name != "" && bridgeDisplayName(discovered[i]) == b.Name) {
				found = true
				if b.Name != "" {
					discovered[i].ConfiguredName = b.Name
				}
				if discovered[i].AdminURL == "" {
					discovered[i].AdminURL = b.URL
				}
				break
			}
		}
		if !found {
			// Memoized live probe: only configured bridges that NATS connz never
			// reported reach here, and the result is cached so the UI's frequent
			// fleet polls don't each pay the bridge round-trip.
			status := s.bridgeStatus.get(b.URL, func() *collector.MQTTBridgeStatus {
				f := collector.NewMQTTBridgeFetcher(b.URL, b.Name, envCfg.ResolveBridgeToken(b.BearerToken))
				return f.FetchStatus(r.Context())
			})
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
		if s.findBridgePush(env, bridgeName) != nil {
			writeJSON(w, map[string]any{
				"error":           "connz not available",
				"detail":          pushUnavailableReason,
				"connections":     []any{},
				"num_connections": 0,
				"total":           0,
			})
			return
		}
		http.Error(w, `{"error":"bridge not found"}`, http.StatusNotFound)
		return
	}

	key := "connz:" + bridge.URL + ":" + strconv.Itoa(limit) + ":" + strconv.Itoa(offset)
	s.serveCachedBridgeJSON(w, key, func() (any, bool) {
		f := collector.NewMQTTBridgeFetcher(bridge.URL, bridge.Name, bridge.BearerToken)
		connz, err := f.FetchConnz(r.Context(), limit, offset)
		if err != nil {
			return map[string]any{
				"error":           "connz not available",
				"detail":          "The bridge's /connz endpoint returned an error. Set clients_snapshot_interval in the bridge's admin config to enable it.",
				"connections":     []any{},
				"num_connections": 0,
				"total":           0,
			}, false // don't cache error placeholders
		}
		return connz, true
	})
}

func (s *Server) handleMQTTClient(w http.ResponseWriter, r *http.Request) {
	env := r.PathValue("env")
	bridgeName := r.PathValue("bridge")
	clientID := r.PathValue("client")

	bridge := s.findBridge(env, bridgeName)
	if bridge == nil {
		if s.findBridgePush(env, bridgeName) != nil {
			writeJSON(w, map[string]any{"error": "client detail unavailable", "detail": pushUnavailableReason})
			return
		}
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
	push := s.findBridgePush(env, bridgeName)
	if bridge == nil && push == nil {
		http.Error(w, `{"error":"bridge not found"}`, http.StatusNotFound)
		return
	}

	// Prefer the cached NATS-push snapshot so a viewer's request never triggers a
	// live fetch against the monitored bridge. Fall back to a live HTTP fetch only
	// when no push data is available for this bridge.
	if push != nil && push.Status != nil && push.Status.NATS != nil {
		writeJSON(w, push.Status.NATS)
		return
	}
	if bridge != nil {
		f := collector.NewMQTTBridgeFetcher(bridge.URL, bridge.Name, bridge.BearerToken)
		diag, err := f.FetchDiagNATS(r.Context())
		if err == nil {
			writeJSON(w, diag)
			return
		}
		s.log.Warn("mqtt bridge request failed", "err", err)
	}
	http.Error(w, `{"error":"bridge request failed"}`, http.StatusBadGateway)
}

func (s *Server) handleMQTTPool(w http.ResponseWriter, r *http.Request) {
	env := r.PathValue("env")
	bridgeName := r.PathValue("bridge")

	bridge := s.findBridge(env, bridgeName)
	push := s.findBridgePush(env, bridgeName)
	if bridge == nil && push == nil {
		http.Error(w, `{"error":"bridge not found"}`, http.StatusNotFound)
		return
	}

	// Prefer the cached NATS-push snapshot; fall back to a live fetch only when no
	// push data is available for this bridge.
	if push != nil && push.Status != nil && push.Status.Pool != nil {
		writeJSON(w, push.Status.Pool)
		return
	}
	if bridge != nil {
		f := collector.NewMQTTBridgeFetcher(bridge.URL, bridge.Name, bridge.BearerToken)
		pool, err := f.FetchPool(r.Context())
		if err == nil {
			writeJSON(w, pool)
			return
		}
		s.log.Warn("mqtt bridge request failed", "err", err)
	}
	http.Error(w, `{"error":"bridge request failed"}`, http.StatusBadGateway)
}

func (s *Server) handleMQTTBridgeDiag(w http.ResponseWriter, r *http.Request) {
	env := r.PathValue("env")
	bridgeName := r.PathValue("bridge")

	bridge := s.findBridge(env, bridgeName)
	if bridge == nil {
		if s.findBridgePush(env, bridgeName) != nil {
			writeJSON(w, map[string]any{"available": false, "reason": pushUnavailableReason})
			return
		}
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
		if s.findBridgePush(env, bridgeName) != nil {
			writeJSON(w, map[string]any{"available": false, "reason": pushUnavailableReason})
			return
		}
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
	push := s.findBridgePush(env, bridgeName)
	if bridge == nil && push == nil {
		http.Error(w, `{"error":"bridge not found"}`, http.StatusNotFound)
		return
	}

	// Prefer the cached NATS-push snapshot; fall back to a live fetch only when no
	// push data is available for this bridge.
	if push != nil && push.Status != nil && push.Status.Metrics != nil {
		writeJSON(w, push.Status.Metrics)
		return
	}
	if bridge != nil {
		f := collector.NewMQTTBridgeFetcher(bridge.URL, bridge.Name, bridge.BearerToken)
		metrics, err := f.FetchMetrics(r.Context())
		if err == nil {
			writeJSON(w, metrics)
			return
		}
		s.log.Warn("mqtt bridge request failed", "err", err)
	}
	http.Error(w, `{"error":"bridge request failed"}`, http.StatusBadGateway)
}

// handleMQTTCluster proxies GET /admin/cluster, relaying the bridge's "feature
// unavailable" responses (409 not clustered, 404 unsupported on older bridges)
// as a clean {available:false, reason} payload instead of an error.
func (s *Server) handleMQTTCluster(w http.ResponseWriter, r *http.Request) {
	env := r.PathValue("env")
	bridgeName := r.PathValue("bridge")

	bridge := s.findBridge(env, bridgeName)
	if bridge == nil {
		if s.findBridgePush(env, bridgeName) != nil {
			writeJSON(w, map[string]any{"available": false, "reason": pushUnavailableReason})
			return
		}
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
		if s.findBridgePush(env, bridgeName) != nil {
			writeJSON(w, map[string]any{"found": false, "reason": pushUnavailableReason})
			return
		}
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
		if s.findBridgePush(env, bridgeName) != nil {
			http.Error(w, `{"error":"admin actions require the bridge's admin HTTP API, which is not configured for this push-discovered bridge"}`, http.StatusServiceUnavailable)
			return
		}
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

// findBridge resolves a bridge's admin HTTP endpoint by name, from both config
// and auto-discovered bridges, resolving the admin bearer token (per-bridge
// override, else the environment-level default) so authed reads and admin
// actions can reach it. It returns nil for push-discovered bridges that have no
// admin URL — those have no live admin API to talk to. Callers that can serve
// cached push data instead should fall back to findBridgePush.
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
		if bridgeMatchesName(b, name) && b.AdminURL != "" {
			return &resolvedBridge{URL: b.AdminURL, Name: bridgeDisplayName(b), BearerToken: resolveToken("")}
		}
	}

	return nil
}

// findBridgePush returns the cached push/poll snapshot for a discovered bridge,
// regardless of whether it exposes an admin URL. The snapshot's Status carries
// the full Metrics, Pool and NATS diagnostics the broker publishes over
// $MQTT5.metrics.>, so the detail page's Metrics/Pool/NATS tabs can be served
// without reaching the bridge's admin HTTP API. Returns nil if no such bridge
// has been discovered.
func (s *Server) findBridgePush(env, name string) *collector.MQTTBridgeInstance {
	for _, b := range s.manager.MQTTBridges(env) {
		if bridgeMatchesName(b, name) {
			inst := b
			return &inst
		}
	}
	return nil
}

// bridgeDisplayName derives the stable display name a discovered bridge is
// addressed by (configured name, else broker-reported name, else mqtt@IP).
func bridgeDisplayName(b collector.MQTTBridgeInstance) string {
	if b.ConfiguredName != "" {
		return b.ConfiguredName
	}
	if b.Status != nil && b.Status.Name != "" {
		return b.Status.Name
	}
	return "mqtt@" + b.IP
}

func bridgeMatchesName(b collector.MQTTBridgeInstance, name string) bool {
	if bridgeDisplayName(b) == name {
		return true
	}
	// Match by IP or admin URL only when set, so an empty query can't match a
	// bridge that happens to have an empty IP/AdminURL.
	return (b.IP != "" && b.IP == name) || (b.AdminURL != "" && b.AdminURL == name)
}

// pushUnavailableReason is returned (HTTP 200) by admin-only endpoints when the
// bridge was discovered over NATS push but exposes no admin HTTP API, so the UI
// can render a precise inline explanation instead of a bare 404.
const pushUnavailableReason = "This bridge was discovered over NATS push metrics and has no admin HTTP endpoint configured. " +
	"Live counters, pool and NATS diagnostics are available on the Metrics, Connection Pool and NATS Connection tabs, " +
	"but license, config, cluster and per-client connection details require the bridge's admin HTTP API " +
	"(set admin_url and a bearer token for this bridge in the environment config)."
