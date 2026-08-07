package api

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"time"
)

// handleHealthz is an unauthenticated liveness/readiness probe for k8s or a load
// balancer. It returns 200 when the database is reachable, 503 otherwise.
func (s *Server) handleHealthz(w http.ResponseWriter, r *http.Request) {
	if err := s.store.Ping(r.Context()); err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusServiceUnavailable)
		if err := json.NewEncoder(w).Encode(map[string]string{"status": "db_unavailable"}); err != nil {
			slog.Warn("healthz encode failed", "err", err)
		}
		return
	}
	writeJSON(w, map[string]string{"status": "ok"})
}

func (s *Server) handleReadiness(w http.ResponseWriter, r *http.Request) {
	if !s.ready.Load() || s.store == nil || s.store.DB().PingContext(r.Context()) != nil || s.manager == nil {
		writeError(w, http.StatusServiceUnavailable, "not ready")
		return
	}
	for _, stats := range s.manager.OperationalStats() {
		if stats.Polls == 0 {
			writeError(w, http.StatusServiceUnavailable, "not ready")
			return
		}
	}
	writeJSON(w, map[string]string{"status": "ready"})
}

func (s *Server) handleDependencyStatus(w http.ResponseWriter, r *http.Request) {
	databaseStatus := "ok"
	if s.store == nil || s.store.DB().PingContext(r.Context()) != nil {
		databaseStatus = "unavailable"
	}
	collectors := make(map[string]any)
	if s.manager != nil {
		staleAfter := 3 * s.cfg.PollInterval
		for environment, stats := range s.manager.OperationalStats() {
			state := "ok"
			if stats.LastSuccessUnix == 0 {
				state = "unavailable"
			} else if staleAfter > 0 && time.Duration(stats.SnapshotAgeNanos) > staleAfter {
				state = "stale"
			}
			collectors[environment] = map[string]any{
				"status": state, "last_success_unix": stats.LastSuccessUnix,
				"snapshot_age_seconds": time.Duration(stats.SnapshotAgeNanos).Seconds(),
				"partial_polls":        stats.PartialPolls,
			}
		}
	}
	writeJSON(w, map[string]any{
		"database": databaseStatus, "collectors": collectors,
		"identity_providers": s.auth.ProviderInfo(),
		"readiness_ignores_external_identity_provider_availability": true,
	})
}

// handleAdminHealth reports the dashboard's own operational health: per-cluster
// collection state (poll age, $SYS fallback, NATS-push connectivity, staleness)
// plus process-wide signals (WS clients, dropped metric samples). Admin-only.
// Overall status is "degraded" if any cluster is degraded, else "ok".
func (s *Server) handleAdminHealth(w http.ResponseWriter, r *http.Request) {
	clusters := s.manager.HealthReport()
	status := "ok"
	for _, c := range clusters {
		if c.Degraded() {
			status = "degraded"
			break
		}
	}
	var dropped uint64
	if s.metrics != nil {
		dropped = s.metrics.Dropped()
	}
	writeJSON(w, map[string]any{
		"status":           status,
		"ws_clients":       s.hub.ClientCount(),
		"ws_stale_clients": s.hub.StaleClientCount(),
		"ws_dropped_total": s.hub.DroppedTotal(),
		"dropped_samples":  dropped,
		"clusters":         clusters,
	})
}
