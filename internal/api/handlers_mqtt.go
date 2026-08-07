package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/noodlebit/machmqtt-dashboard/internal/collector"
	"github.com/noodlebit/machmqtt-dashboard/internal/config"
)

// bridgeStatusCache memoizes live MachMQTT bridge admin-API status lookups for
// configured-but-undiscovered bridges. Probes run in the background and requests
// are served the last known result, so a viewer never waits on a bridge — an
// unreachable one costs a full admin-read timeout per read. Auto-discovered
// bridges are served from the collector's poll cache and never reach this path.
// Keys are configured bridge URLs (a small, bounded set), so the map does not
// grow unbounded.
type bridgeStatusCache struct {
	mu      sync.Mutex
	ttl     time.Duration
	log     *slog.Logger
	entries map[string]bridgeStatusEntry
}

type bridgeStatusEntry struct {
	status    *collector.MQTTBridgeStatus
	fetchedAt time.Time
	// fetching is true while a background probe for this key is in flight, so
	// concurrent viewers collapse into one probe instead of one probe each.
	fetching bool
}

func newBridgeStatusCache(ttl time.Duration, log *slog.Logger) *bridgeStatusCache {
	return &bridgeStatusCache{ttl: ttl, log: log, entries: make(map[string]bridgeStatusEntry)}
}

// getAsync returns the last known status for key and the time it was obtained,
// starting a single background refresh when the entry is missing or older than
// the TTL. It never blocks on the bridge, so a dead bridge cannot stall the
// caller. A first-ever call returns (nil, zero) — the caller reports the bridge
// as pending and the refresh lands within the TTL.
func (c *bridgeStatusCache) getAsync(key string, fetch func() *collector.MQTTBridgeStatus) (*collector.MQTTBridgeStatus, time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e := c.entries[key]
	if !e.fetching && (e.status == nil || time.Since(e.fetchedAt) >= c.ttl) {
		e.fetching = true
		c.entries[key] = e
		go c.refresh(key, fetch)
	}
	return e.status, e.fetchedAt
}

// refresh runs one background probe and stores its result. The in-flight flag is
// cleared even if fetch panics — otherwise that bridge would never be probed
// again for the process's lifetime.
func (c *bridgeStatusCache) refresh(key string, fetch func() *collector.MQTTBridgeStatus) {
	var status *collector.MQTTBridgeStatus
	defer func() {
		if r := recover(); r != nil && c.log != nil {
			c.log.Error("mqtt bridge probe panicked", "bridge", key, "panic", r)
		}
		c.mu.Lock()
		e := c.entries[key]
		e.fetching = false
		if status != nil {
			e.status, e.fetchedAt = status, time.Now()
		}
		c.entries[key] = e
		c.mu.Unlock()
	}()
	status = fetch()
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

// bridgeProbeTimeout bounds one background probe of a configured bridge.
// FetchStatus performs several sequential admin reads, each with its own
// timeout, so a bridge that answers the first and then hangs would otherwise
// hold a probe slot for the sum of them.
const bridgeProbeTimeout = 15 * time.Second

// probePendingReason is reported for a configured bridge whose first probe has
// not answered yet. The probe never runs on the request path, so the first ever
// fleet read reports the bridge as pending rather than waiting for it; the next
// read (the UI polls every few seconds) carries the real state.
const probePendingReason = "probing the bridge admin API"

func (s *Server) handleMQTTBridges(w http.ResponseWriter, r *http.Request) {
	env := r.PathValue("env")
	envCfg := s.envConfig(env)
	if envCfg == nil {
		writeJSON(w, map[string]any{"bridges": []any{}})
		return
	}

	// One cached body per env, shared by every viewer: the listing is derived from
	// the env path value alone — there are no query params, and since the
	// configured-bridge probes are asynchronous, nothing request-scoped feeds it —
	// so the same bytes are correct for every caller within the TTL. Without this,
	// each viewer's poll re-converted and re-marshalled every cached bridge. The
	// trade-off is that a state change (e.g. a drain) can take up to the TTL to
	// appear in the listing.
	s.serveCachedBridgeJSON(w, "bridges|"+env, func() (any, bool) {
		return map[string]any{"bridges": s.fleetBridges(env, envCfg)}, true
	})
}

// fleetBridges builds the fleet listing: the collector's cached discovery results
// (push or connz-scan) merged with the configured bridges discovery never
// reported, sorted by display name.
func (s *Server) fleetBridges(env string, envCfg *config.Environment) []collector.MQTTBridgeInstance {
	// Use cached discovery results from the collector poll loop.
	discovered := s.manager.MQTTBridges(env)
	if discovered == nil {
		discovered = []collector.MQTTBridgeInstance{}
	}

	// Also include manually configured bridges that weren't auto-discovered.
	for _, b := range envCfg.MQTTBridges {
		if i := indexOfConfiguredBridge(discovered, b); i >= 0 {
			mergeConfiguredBridge(&discovered[i], b)
			continue
		}
		// Only configured bridges that neither push metrics nor connz reported
		// reach here. The probe runs in the background and this read serves the
		// last known result, so an unreachable bridge can never stall the fleet.
		// Resolve the token here, not in the closure: the probe outlives the
		// request, and the environment config it comes from is replaced in place on
		// a config reload.
		token := envCfg.ResolveBridgeToken(b.BearerToken)
		status, probedAt := s.bridgeStatus.getAsync(b.URL, func() *collector.MQTTBridgeStatus {
			ctx, cancel := context.WithTimeout(context.Background(), bridgeProbeTimeout)
			defer cancel()
			f := collector.NewMQTTBridgeFetcher(b.URL, b.Name, token)
			return f.FetchStatus(ctx)
		})
		if status == nil {
			status = &collector.MQTTBridgeStatus{Name: b.Name, URL: b.URL, Error: probePendingReason}
		}
		// A bridge the broker reports under a different instance name than the one
		// it is configured as is the same broker: merge it rather than listing it
		// twice, which would also double-count its connections and rates in the
		// fleet totals.
		if i := findByInstanceIdentity(discovered, probedInstanceIdentity(status)); i >= 0 {
			mergeConfiguredBridge(&discovered[i], b)
			continue
		}
		discovered = append(discovered, collector.MQTTBridgeInstance{
			IP:             b.URL,
			AdminURL:       b.URL,
			ConfiguredName: b.Name,
			Status:         status,
			Reachable:      status.Error == "",
			LastSeen:       probedAt,
		})
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
	return discovered
}

// indexOfConfiguredBridge returns the index of the discovered instance a
// configured bridge names — by admin URL, or by name so a configured bridge that
// names a push-discovered instance (which has no admin URL of its own) merges
// into it instead of adding a duplicate fleet card. Returns -1 for no match.
func indexOfConfiguredBridge(discovered []collector.MQTTBridgeInstance, b config.MQTTBridge) int {
	for i := range discovered {
		if (b.URL != "" && discovered[i].AdminURL == b.URL) ||
			(b.Name != "" && bridgeDisplayName(discovered[i]) == b.Name) {
			return i
		}
	}
	return -1
}

// mergeConfiguredBridge folds a configured bridge's admin endpoint and name into
// the discovered instance it refers to. A discovered name is left alone: for a
// push instance it is the broker's instance name, which is the key its stored
// history is written under.
func mergeConfiguredBridge(inst *collector.MQTTBridgeInstance, b config.MQTTBridge) {
	if inst.ConfiguredName == "" && b.Name != "" {
		inst.ConfiguredName = b.Name
	}
	if inst.AdminURL == "" {
		inst.AdminURL = b.URL
	}
}

// probedInstanceIdentity is the broker-reported identity of a probed bridge: the
// instance_id its own metrics expose. A configured bridge's name is chosen by the
// operator and need not match it.
func probedInstanceIdentity(st *collector.MQTTBridgeStatus) string {
	if st == nil || st.Metrics == nil {
		return ""
	}
	return st.Metrics.InstanceID
}

// findByInstanceIdentity returns the index of the discovered instance that
// reports the given broker identity — matched against the identity a push
// snapshot carries, or against the instance name it is keyed by (deployments that
// set instance_id to the stable name). Returns -1 for no match, including for an
// empty identity, which must never match.
func findByInstanceIdentity(discovered []collector.MQTTBridgeInstance, identity string) int {
	if identity == "" {
		return -1
	}
	for i := range discovered {
		st := discovered[i].Status
		if st == nil {
			continue
		}
		if st.Metrics != nil && st.Metrics.InstanceID == identity {
			return i
		}
		if st.Name == identity {
			return i
		}
	}
	return -1
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
		writeJSONError(w, `{"error":"bridge request failed"}`, http.StatusBadGateway)
		return
	}
	if len(connz.Connections) == 0 {
		writeJSONError(w, `{"error":"client not found"}`, http.StatusNotFound)
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
		writeJSONError(w, `{"error":"bridge request failed"}`, http.StatusBadGateway)
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
		writeJSONError(w, `{"error":"bridge request failed"}`, http.StatusBadGateway)
		return
	}
	writeJSON(w, license)
}

// handleMQTTReadyz relays the bridge's readiness state so the detail page can
// label a draining or JetStream-degraded broker. The bridge answers 503 for
// those states, which the fetcher decodes rather than failing, so a non-ready
// answer is a successful read here. Push-only bridges carry no readyz state
// (the metrics snapshot doesn't include it), hence the "available:false" reason.
// A bridge that can't be reached at all reports the same way instead of a 502:
// unreachability is already surfaced by the fleet card and the detail page's
// load-error banner, and this read only decorates the page with a state label.
func (s *Server) handleMQTTReadyz(w http.ResponseWriter, r *http.Request) {
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

	s.serveCachedBridgeJSON(w, "readyz|"+bridge.URL, func() (any, bool) {
		f := collector.NewMQTTBridgeFetcher(bridge.URL, bridge.Name, bridge.BearerToken)
		readyz, err := f.FetchReadyz(r.Context())
		if err != nil {
			s.log.Warn("mqtt bridge request failed", "err", err)
			return map[string]any{"available": false, "reason": "bridge request failed"}, false
		}
		ready, draining, jsDegraded := collector.ReadyzState(readyz.Status)
		return map[string]any{
			"available":          true,
			"status":             readyz.Status,
			"ready":              ready,
			"draining":           draining,
			"jetstream_degraded": jsDegraded,
			"nats_connected":     readyz.NATSConnected,
		}, true
	})
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
		writeJSONError(w, `{"error":"bridge request failed"}`, http.StatusBadGateway)
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
