package api

import (
	"net/http"

	"github.com/noodlebit/machmqtt-dashboard/internal/auth"
)

func (s *Server) registerRoutes(a *auth.Auth) {
	mux := s.mux

	// Public routes.
	mux.HandleFunc("POST /api/login", a.HandleLogin)
	mux.HandleFunc("GET /api/auth/providers", a.HandleProviders)
	mux.HandleFunc("POST /api/auth/local/login", a.HandleLocalLogin)
	mux.HandleFunc("GET /api/auth/oidc/{provider}/login", a.HandleOIDCLogin)
	mux.HandleFunc("GET /api/auth/oidc/{provider}/callback", a.HandleOIDCCallback)
	mux.HandleFunc("GET /livez", func(w http.ResponseWriter, _ *http.Request) { writeJSON(w, map[string]string{"status": "ok"}) })
	mux.HandleFunc("GET /readyz", s.handleReadiness)
	// /metrics exposes environment names and runtime internals, so it is never
	// anonymous: a configured scrape token, or an authenticated dashboard session.
	mux.Handle("GET /metrics", s.guardMetrics(a, http.HandlerFunc(s.handleMetrics)))
	// Unauthenticated liveness/readiness probe (k8s / load balancer).
	mux.HandleFunc("GET /healthz", s.handleHealthz)

	// Protected routes (any authenticated user).
	protected := http.NewServeMux()
	protected.HandleFunc("POST /api/logout", a.HandleLogout)
	protected.HandleFunc("GET /api/me", a.HandleMe)
	protected.HandleFunc("PUT /api/users/{id}/password", a.HandleChangePassword)
	protected.HandleFunc("GET /api/version", s.handleVersion)

	protected.HandleFunc("GET /api/environments", s.handleEnvironments)
	protected.HandleFunc("GET /api/environments/{env}/overview", s.handleOverview)
	protected.HandleFunc("GET /api/environments/{env}/topology", s.handleTopology)
	protected.HandleFunc("GET /api/environments/{env}/varz", s.handleVarz)
	protected.HandleFunc("GET /api/environments/{env}/connz", s.handleConnz)
	protected.HandleFunc("GET /api/environments/{env}/connz/{cid}", s.handleConnzDetail)
	protected.HandleFunc("GET /api/environments/{env}/routez", s.handleRoutez)
	protected.HandleFunc("GET /api/environments/{env}/gatewayz", s.handleGatewayz)
	protected.HandleFunc("GET /api/environments/{env}/leafz", s.handleLeafz)
	protected.HandleFunc("GET /api/environments/{env}/subsz", s.handleSubsz)
	protected.HandleFunc("GET /api/environments/{env}/subsz/detail", s.handleSubsDetail)
	protected.HandleFunc("GET /api/environments/{env}/jsz", s.handleJSz)
	protected.HandleFunc("GET /api/environments/{env}/accountz", s.handleAccountz)
	protected.HandleFunc("GET /api/environments/{env}/accountz/{acc}", s.handleAccountDetail)

	// MQTT bridge routes.
	protected.HandleFunc("GET /api/environments/{env}/mqtt/bridges", s.handleMQTTBridges)
	protected.HandleFunc("GET /api/environments/{env}/mqtt/{bridge}/connz", s.handleMQTTConnz)
	protected.HandleFunc("GET /api/environments/{env}/mqtt/{bridge}/connz/{client}", s.handleMQTTClient)
	protected.HandleFunc("GET /api/environments/{env}/mqtt/{bridge}/diag", s.handleMQTTDiag)
	protected.HandleFunc("GET /api/environments/{env}/mqtt/{bridge}/diag/config", s.handleMQTTBridgeDiag)
	protected.HandleFunc("GET /api/environments/{env}/mqtt/{bridge}/license", s.handleMQTTLicense)
	protected.HandleFunc("GET /api/environments/{env}/mqtt/{bridge}/metrics", s.handleMQTTMetrics)
	protected.HandleFunc("GET /api/environments/{env}/mqtt/{bridge}/pool", s.handleMQTTPool)
	protected.HandleFunc("GET /api/environments/{env}/mqtt/{bridge}/readyz", s.handleMQTTReadyz)
	protected.HandleFunc("GET /api/environments/{env}/mqtt/{bridge}/cluster", s.handleMQTTCluster)
	protected.HandleFunc("GET /api/environments/{env}/mqtt/{bridge}/cluster/inspect", s.handleMQTTClusterInspect)

	// Topology position persistence.
	protected.HandleFunc("GET /api/environments/{env}/topology/positions", s.handleGetPositions)
	protected.HandleFunc("PUT /api/environments/{env}/topology/positions", s.handleSavePositions)

	// Time-series metrics routes.
	protected.HandleFunc("GET /api/environments/{env}/metrics/overview", s.handleEnvMetrics)
	protected.HandleFunc("GET /api/environments/{env}/metrics/servers", s.handleServerMetrics)
	protected.HandleFunc("GET /api/environments/{env}/metrics/mqtt", s.handleMQTTBridgeMetrics)

	protected.HandleFunc("GET /api/ws", s.handleWS)

	// MQTT bridge admin actions (state-changing) — admin role only.
	protected.Handle("POST /api/environments/{env}/mqtt/{bridge}/admin/{action}",
		auth.AdminMiddleware(http.HandlerFunc(s.handleMQTTAdminAction)))

	// Admin-only routes (wrapped with AdminMiddleware).
	admin := http.NewServeMux()
	admin.HandleFunc("GET /api/admin/users", a.HandleListUsers)
	admin.HandleFunc("POST /api/admin/users", a.HandleCreateUser)
	admin.HandleFunc("DELETE /api/admin/users/{id}", a.HandleDeleteUser)
	// Cluster management (create/update/delete are admin-only).
	admin.HandleFunc("GET /api/admin/clusters", s.handleListClusters)
	admin.HandleFunc("POST /api/admin/clusters", s.handleCreateCluster)
	admin.HandleFunc("PUT /api/admin/clusters/{id}", s.handleUpdateCluster)
	admin.HandleFunc("DELETE /api/admin/clusters/{id}", s.handleDeleteCluster)
	// Server logs.
	admin.HandleFunc("GET /api/admin/logs", s.handleAdminLogs)
	// Dashboard self-health (per-cluster collection state).
	admin.HandleFunc("GET /api/admin/health", s.handleAdminHealth)
	admin.HandleFunc("GET /api/admin/status", s.handleDependencyStatus)
	protected.Handle("/api/admin/", auth.AdminMiddleware(admin))

	mux.Handle("/api/", a.Middleware(protected))
}
