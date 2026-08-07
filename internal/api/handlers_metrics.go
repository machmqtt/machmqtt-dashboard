package api

import (
	"net/http"
	"strconv"
	"time"
)

func (s *Server) handleEnvMetrics(w http.ResponseWriter, r *http.Request) {
	if s.metrics == nil {
		writeJSONError(w, `{"error":"metrics not enabled"}`, http.StatusServiceUnavailable)
		return
	}
	env := r.PathValue("env")
	from, to, step := parseTimeRange(r)

	points, err := s.metrics.QueryEnvMetrics(r.Context(), env, from, to, step)
	if err != nil {
		writeJSONError(w, `{"error":"query failed"}`, http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]any{"points": points})
}

func (s *Server) handleServerMetrics(w http.ResponseWriter, r *http.Request) {
	if s.metrics == nil {
		writeJSONError(w, `{"error":"metrics not enabled"}`, http.StatusServiceUnavailable)
		return
	}
	env := r.PathValue("env")
	serverID := r.URL.Query().Get("server_id")
	from, to, step := parseTimeRange(r)

	points, err := s.metrics.QueryServerMetrics(r.Context(), env, serverID, from, to, step)
	if err != nil {
		writeJSONError(w, `{"error":"query failed"}`, http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]any{"points": points})
}

func (s *Server) handleMQTTBridgeMetrics(w http.ResponseWriter, r *http.Request) {
	if s.metrics == nil {
		writeJSONError(w, `{"error":"metrics not enabled"}`, http.StatusServiceUnavailable)
		return
	}
	env := r.PathValue("env")
	bridgeID := r.URL.Query().Get("bridge_id")
	from, to, step := parseTimeRange(r)

	points, err := s.metrics.QueryMQTTMetrics(r.Context(), env, bridgeID, from, to, step)
	if err != nil {
		writeJSONError(w, `{"error":"query failed"}`, http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]any{"points": points})
}

func parseTimeRange(r *http.Request) (from, to, step int64) {
	now := time.Now().Unix()
	to = now
	from = now - 3600           // default 1 hour
	minFrom := now - 30*24*3600 // max 30 days of history

	if v := r.URL.Query().Get("from"); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil && n >= minFrom && n <= now {
			from = n
		}
	}
	if v := r.URL.Query().Get("to"); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil && n >= from && n <= now+60 {
			to = n
		}
	}
	if v := r.URL.Query().Get("step"); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil && n >= 1 && n <= 86400 {
			step = n
		}
	}
	// Clamp step up so a caller can't force an unbounded number of points
	// (e.g. step=1 over a 30-day window). At most maxMetricPoints are returned;
	// a step of <=0 leaves auto-bucketing to the store, which is already bounded.
	if step > 0 {
		if minStep := (to - from) / maxMetricPoints; step < minStep {
			step = minStep
		}
	}
	return
}

// maxMetricPoints bounds the number of time-series points any single query can
// return, regardless of the caller-supplied step.
const maxMetricPoints = 5000
