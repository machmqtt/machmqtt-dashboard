package api

import (
	"bufio"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/noodlebit/machmqtt-dashboard/internal/auth"
)

type responseCapture struct {
	http.ResponseWriter
	status int
	size   int
}

func (w *responseCapture) WriteHeader(status int) {
	if w.status != 0 {
		return
	}
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}

func (w *responseCapture) Write(body []byte) (int, error) {
	if w.status == 0 {
		w.WriteHeader(http.StatusOK)
	}
	n, err := w.ResponseWriter.Write(body)
	w.size += n
	return n, err
}

func (w *responseCapture) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func (w *responseCapture) Flush() {
	if flusher, ok := w.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

func (w *responseCapture) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	hijacker, ok := w.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, fmt.Errorf("response writer does not support hijacking")
	}
	return hijacker.Hijack()
}

type operationalMetrics struct {
	inFlight atomic.Int64
	panics   atomic.Uint64
	mu       sync.Mutex
	requests map[string]uint64
	duration map[string]time.Duration
}

func newOperationalMetrics() *operationalMetrics {
	return &operationalMetrics{requests: make(map[string]uint64), duration: make(map[string]time.Duration)}
}

func (m *operationalMetrics) record(route string, status int, duration time.Duration) {
	key := route + "\x00" + strconv.Itoa(status)
	m.mu.Lock()
	m.requests[key]++
	m.duration[route] += duration
	m.mu.Unlock()
}

// snapshot copies the counters so /metrics never formats to a network writer
// while holding the lock every request handler needs.
func (m *operationalMetrics) snapshot() (map[string]uint64, map[string]time.Duration) {
	m.mu.Lock()
	defer m.mu.Unlock()
	requests := make(map[string]uint64, len(m.requests))
	for key, value := range m.requests {
		requests[key] = value
	}
	durations := make(map[string]time.Duration, len(m.duration))
	for key, value := range m.duration {
		durations[key] = value
	}
	return requests, durations
}

func requestID(r *http.Request) string {
	if value := r.Header.Get("X-Request-ID"); value != "" && len(value) <= 128 {
		valid := true
		for _, ch := range value {
			if !validRequestIDCharacter(ch) {
				valid = false
				break
			}
		}
		if valid {
			return value
		}
	}
	bytes := make([]byte, 16)
	if _, err := rand.Read(bytes); err == nil {
		return hex.EncodeToString(bytes)
	}
	return strconv.FormatInt(time.Now().UnixNano(), 36)
}

func validRequestIDCharacter(ch rune) bool {
	if ch == '-' || ch == '_' || ch == '.' {
		return true
	}
	return ch >= '0' && ch <= '9' || ch >= 'A' && ch <= 'Z' || ch >= 'a' && ch <= 'z'
}

func clientClass(r *http.Request) string {
	ua := strings.ToLower(r.UserAgent())
	if strings.Contains(ua, "mozilla/") {
		return "browser"
	}
	if ua == "" {
		return "unknown"
	}
	return "api"
}

func (s *Server) observe(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started := time.Now()
		id := requestID(r)
		w.Header().Set("X-Request-ID", id)
		capture := &responseCapture{ResponseWriter: w}
		s.ops.inFlight.Add(1)
		defer func() {
			s.ops.inFlight.Add(-1)
			route := r.Pattern
			if route == "" {
				route = "unmatched"
			}
			if recovered := recover(); recovered != nil {
				s.ops.panics.Add(1)
				s.log.Error("HTTP panic", "request_id", id, "method", r.Method, "route", route, "panic", fmt.Sprint(recovered))
				if capture.status == 0 {
					writeError(capture, http.StatusInternalServerError, "internal server error")
				}
			}
			if capture.status == 0 {
				capture.status = http.StatusOK
			}
			duration := time.Since(started)
			s.ops.record(route, capture.status, duration)
			s.log.Log(r.Context(), slog.LevelInfo, "HTTP request",
				"request_id", id, "method", r.Method, "route", route,
				"status", capture.status, "duration_ms", duration.Milliseconds(),
				"response_bytes", capture.size, "client", clientClass(r))
		}()
		next.ServeHTTP(capture, r)
	})
}

// guardMetrics authorizes a Prometheus scrape by bearer token when one is
// configured, and otherwise falls back to a normal dashboard session so the
// endpoint is never reachable anonymously.
func (s *Server) guardMetrics(a *auth.Auth, next http.Handler) http.Handler {
	token := ""
	if s.cfg != nil {
		token = s.cfg.MetricsToken
	}
	if token == "" {
		return a.Middleware(next)
	}
	expected := []byte(token)
	session := a.Middleware(next)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The scheme is required: TrimPrefix alone would also accept the bare
		// token, widening what counts as a valid credential.
		presented, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		if ok && subtle.ConstantTimeCompare([]byte(presented), expected) == 1 {
			next.ServeHTTP(w, r)
			return
		}
		session.ServeHTTP(w, r)
	})
}

// Prometheus output is best-effort once streaming starts; a client disconnect
// cannot be recovered within an HTTP handler.
//
//nolint:errcheck
func (s *Server) handleMetrics(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/plain; version=0.0.4")
	fmt.Fprintf(w, "# TYPE nats_dashboard_http_in_flight gauge\nnats_dashboard_http_in_flight %d\n", s.ops.inFlight.Load())
	fmt.Fprintf(w, "# TYPE nats_dashboard_http_panics_total counter\nnats_dashboard_http_panics_total %d\n", s.ops.panics.Load())
	requests, durations := s.ops.snapshot()
	for key, count := range requests {
		parts := strings.SplitN(key, "\x00", 2)
		fmt.Fprintf(w, "nats_dashboard_http_requests_total{route=%q,status=%q} %d\n", parts[0], parts[1], count)
	}
	for route, duration := range durations {
		fmt.Fprintf(w, "nats_dashboard_http_request_duration_seconds_total{route=%q} %f\n", route, duration.Seconds())
	}
	if s.metrics != nil {
		stats := s.metrics.Stats()
		fmt.Fprintf(w, "nats_dashboard_metrics_queue_depth %d\n", stats.QueueDepth)
		fmt.Fprintf(w, "nats_dashboard_metrics_samples_dropped_total %d\n", stats.Dropped)
		fmt.Fprintf(w, "nats_dashboard_metrics_samples_written_total %d\n", stats.Written)
		fmt.Fprintf(w, "nats_dashboard_metrics_samples_failed_total %d\n", stats.Failed)
		fmt.Fprintf(w, "nats_dashboard_metrics_database_busy_total %d\n", stats.Busy)
		fmt.Fprintf(w, "nats_dashboard_metrics_query_errors_total %d\n", stats.QueryErrors)
		fmt.Fprintf(w, "nats_dashboard_metrics_last_write_duration_seconds %f\n", float64(stats.LastWriteNanos)/float64(time.Second))
		fmt.Fprintf(w, "nats_dashboard_metrics_query_duration_seconds_total %f\n", float64(stats.QueryNanos)/float64(time.Second))
		fmt.Fprintf(w, "nats_dashboard_metrics_cleanup_duration_seconds %f\n", float64(stats.CleanupNanos)/float64(time.Second))
		fmt.Fprintf(w, "nats_dashboard_metrics_last_batch_rows %d\n", stats.LastBatchRows)
		fmt.Fprintf(w, "nats_dashboard_metrics_oldest_queued_sample_seconds %f\n", float64(stats.OldestQueueAge)/float64(time.Second))
	}
	if s.store != nil {
		stats := s.store.DB().Stats()
		fmt.Fprintf(w, "nats_dashboard_database_open_connections %d\n", stats.OpenConnections)
		fmt.Fprintf(w, "nats_dashboard_database_in_use_connections %d\n", stats.InUse)
		fmt.Fprintf(w, "nats_dashboard_database_idle_connections %d\n", stats.Idle)
		fmt.Fprintf(w, "nats_dashboard_database_wait_count_total %d\n", stats.WaitCount)
		fmt.Fprintf(w, "nats_dashboard_database_wait_duration_seconds_total %f\n", stats.WaitDuration.Seconds())
		if walBytes, err := s.store.WALSize(); err == nil {
			fmt.Fprintf(w, "nats_dashboard_database_wal_bytes %d\n", walBytes)
		} else {
			s.log.Warn("inspect database WAL", "err", err)
		}
	}
	if s.hub != nil {
		stats := s.hub.Stats()
		fmt.Fprintf(w, "nats_dashboard_websocket_clients %d\n", stats.Connected)
		fmt.Fprintf(w, "nats_dashboard_websocket_messages_dropped_total %d\n", stats.Dropped)
		fmt.Fprintf(w, "nats_dashboard_websocket_subscriptions_total %d\n", stats.Subscriptions)
		fmt.Fprintf(w, "nats_dashboard_websocket_disconnects_total %d\n", stats.Disconnects)
		fmt.Fprintf(w, "nats_dashboard_websocket_write_failures_total %d\n", stats.WriteFailures)
		fmt.Fprintf(w, "nats_dashboard_websocket_send_queue_depth %d\n", stats.SendQueueDepth)
	}
	if s.auth != nil {
		for key, count := range s.auth.Metrics() {
			parts := strings.SplitN(key, "\x00", 2)
			fmt.Fprintf(w, "nats_dashboard_authentication_events_total{provider=%q,result=%q} %d\n", parts[0], parts[1], count)
		}
		for provider, nanos := range s.auth.ProviderDurations() {
			fmt.Fprintf(w, "nats_dashboard_authentication_provider_duration_seconds_total{provider=%q} %f\n", provider, float64(nanos)/float64(time.Second))
		}
		ordered, local := s.auth.RateLimiterMetrics()
		fmt.Fprintf(w, "nats_dashboard_authentication_rate_limiter_keys{route=%q} %d\n", "ordered", ordered.Keys)
		fmt.Fprintf(w, "nats_dashboard_authentication_rate_limiter_keys{route=%q} %d\n", "local", local.Keys)
		fmt.Fprintf(w, "nats_dashboard_authentication_rate_limited_total{route=%q} %d\n", "ordered", ordered.Rejected)
		fmt.Fprintf(w, "nats_dashboard_authentication_rate_limited_total{route=%q} %d\n", "local", local.Rejected)
		fmt.Fprintf(w, "nats_dashboard_authentication_rate_limiter_evictions_total{route=%q} %d\n", "ordered", ordered.Evicted)
		fmt.Fprintf(w, "nats_dashboard_authentication_rate_limiter_evictions_total{route=%q} %d\n", "local", local.Evicted)
		for provider, stats := range s.auth.OIDCFlowMetrics() {
			fmt.Fprintf(w, "nats_dashboard_oidc_flows_active{provider=%q} %d\n", provider, stats.Active)
			fmt.Fprintf(w, "nats_dashboard_oidc_flow_evictions_total{provider=%q} %d\n", provider, stats.Evictions)
		}
	}
	if s.manager != nil {
		for env, stats := range s.manager.OperationalStats() {
			fmt.Fprintf(w, "nats_dashboard_collector_polls_total{environment=%q} %d\n", env, stats.Polls)
			fmt.Fprintf(w, "nats_dashboard_collector_partial_polls_total{environment=%q} %d\n", env, stats.PartialPolls)
			fmt.Fprintf(w, "nats_dashboard_collector_last_poll_duration_seconds{environment=%q} %f\n", env, float64(stats.LastPollNanos)/float64(time.Second))
			fmt.Fprintf(w, "nats_dashboard_collector_last_success_timestamp_seconds{environment=%q} %d\n", env, stats.LastSuccessUnix)
			fmt.Fprintf(w, "nats_dashboard_collector_snapshot_age_seconds{environment=%q} %f\n", env, float64(stats.SnapshotAgeNanos)/float64(time.Second))
			fmt.Fprintf(w, "nats_dashboard_collector_discovery_skipped_total{environment=%q} %d\n", env, stats.DiscoverySkips)
			for endpoint, count := range stats.EndpointFailures {
				fmt.Fprintf(w, "nats_dashboard_collector_endpoint_failures_total{environment=%q,endpoint=%q} %d\n", env, endpoint, count)
			}
		}
	}
	fmt.Fprintf(w, "nats_dashboard_subscription_cache_hits_total %d\n", s.subsCacheHits.Load())
	fmt.Fprintf(w, "nats_dashboard_subscription_cache_misses_total %d\n", s.subsCacheMisses.Load())
	fmt.Fprintf(w, "nats_dashboard_subscription_cache_evictions_total %d\n", s.subsCacheEvictions.Load())
	s.subsCacheMu.Lock()
	subsEntries := len(s.subsCacheData)
	s.subsCacheMu.Unlock()
	fmt.Fprintf(w, "nats_dashboard_subscription_cache_entries %d\n", subsEntries)
	var memory runtime.MemStats
	runtime.ReadMemStats(&memory)
	fmt.Fprintf(w, "nats_dashboard_go_goroutines %d\n", runtime.NumGoroutine())
	fmt.Fprintf(w, "nats_dashboard_go_heap_alloc_bytes %d\n", memory.HeapAlloc)
}
