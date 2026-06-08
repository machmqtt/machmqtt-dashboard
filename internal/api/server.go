package api

import (
	"embed"
	"io/fs"
	"log/slog"
	"net/http"
	"net/url"
	"strings"

	"github.com/gorilla/websocket"
	"github.com/noodlebit/machmqtt-dashboard/internal/auth"
	"github.com/noodlebit/machmqtt-dashboard/internal/collector"
	"github.com/noodlebit/machmqtt-dashboard/internal/config"
	"github.com/noodlebit/machmqtt-dashboard/internal/store"
	"github.com/noodlebit/machmqtt-dashboard/internal/ws"
)

//go:embed dist/*
var distFS embed.FS

// checkSameOrigin validates that the Origin header matches the Host header,
// preventing cross-site WebSocket hijacking attacks.
func checkSameOrigin(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true // non-browser clients don't send Origin
	}
	u, err := url.Parse(origin)
	if err != nil {
		return false
	}
	return strings.EqualFold(u.Host, r.Host)
}

var upgrader = websocket.Upgrader{
	CheckOrigin: checkSameOrigin,
}

type Server struct {
	mux     *http.ServeMux
	manager *collector.Manager
	hub     *ws.Hub
	log     *slog.Logger
	version string
	metrics *store.MetricsWriter
	store   *store.Store
}

func NewServer(a *auth.Auth, manager *collector.Manager, hub *ws.Hub, log *slog.Logger, version string, cfg *config.Config, metrics *store.MetricsWriter, st *store.Store) *Server {
	s := &Server{
		mux:     http.NewServeMux(),
		manager: manager,
		hub:     hub,
		log:     log,
		version: version,
		metrics: metrics,
		store:   st,
	}

	s.registerRoutes(a)
	s.serveSPA()

	return s
}

func (s *Server) Handler() http.Handler {
	return securityHeaders(limitBody(s.mux))
}

func (s *Server) serveSPA() {
	sub, err := fs.Sub(distFS, "dist")
	if err != nil {
		s.log.Error("embed fs", "err", err)
		return
	}

	fileServer := http.FileServer(http.FS(sub))

	s.mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		// Serve static files if they exist, otherwise fall back to index.html.
		path := r.URL.Path
		if path == "/" {
			path = "/index.html"
		}

		// Try to open the file.
		f, err := sub.Open(strings.TrimPrefix(path, "/"))
		if err == nil {
			f.Close()
			fileServer.ServeHTTP(w, r)
			return
		}

		// Fall back to index.html for client-side routing.
		r.URL.Path = "/"
		fileServer.ServeHTTP(w, r)
	})
}

func (s *Server) handleWS(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		s.log.Warn("ws upgrade", "err", err)
		return
	}

	client := ws.NewClient(s.hub, conn, s.log)
	go client.Run()
}
