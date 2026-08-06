package api

import (
	"embed"
	"io/fs"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
	"github.com/noodlebit/machmqtt-dashboard/internal/auth"
	"github.com/noodlebit/machmqtt-dashboard/internal/collector"
	"github.com/noodlebit/machmqtt-dashboard/internal/config"
	"github.com/noodlebit/machmqtt-dashboard/internal/logbuf"
	"github.com/noodlebit/machmqtt-dashboard/internal/store"
	"github.com/noodlebit/machmqtt-dashboard/internal/ws"
	"golang.org/x/sync/singleflight"
)

//go:embed dist/*
var distFS embed.FS

// spaDistDir is the embedded directory served as the SPA root. It is a var
// rather than an inline literal so a test can point it at an invalid path to
// exercise the fs.Sub failure branch.
var spaDistDir = "dist"

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
	mux                *http.ServeMux
	manager            *collector.Manager
	hub                *ws.Hub
	log                *slog.Logger
	version            string
	cfg                *config.Config
	metrics            *store.MetricsWriter
	store              *store.Store
	auth               *auth.Auth
	logBuf             *logbuf.Handler
	bridgeStatus       *bridgeStatusCache
	bridgeJSON         *bridgeRespCache
	ops                *operationalMetrics
	ready              atomic.Bool
	subsCacheMu        sync.Mutex
	subsCacheData      map[string]*subsCacheEntry
	subsGroup          singleflight.Group
	subsCacheHits      atomic.Uint64
	subsCacheMisses    atomic.Uint64
	subsCacheEvictions atomic.Uint64
}

func NewServer(a *auth.Auth, manager *collector.Manager, hub *ws.Hub, log *slog.Logger, version string, cfg *config.Config, metrics *store.MetricsWriter, st *store.Store, lb *logbuf.Handler) *Server {
	s := &Server{
		mux:           http.NewServeMux(),
		manager:       manager,
		hub:           hub,
		log:           log,
		version:       version,
		metrics:       metrics,
		store:         st,
		cfg:           cfg,
		auth:          a,
		logBuf:        lb,
		bridgeStatus:  newBridgeStatusCache(5*time.Second, log),
		bridgeJSON:    newBridgeRespCache(3 * time.Second),
		ops:           newOperationalMetrics(),
		subsCacheData: make(map[string]*subsCacheEntry),
	}
	s.ready.Store(true)

	s.registerRoutes(a)
	s.serveSPA()

	return s
}

func (s *Server) Handler() http.Handler {
	return securityHeaders(s.observe(limitBody(s.mux)))
}

func (s *Server) SetReady(ready bool) { s.ready.Store(ready) }

func (s *Server) serveSPA() {
	sub, err := fs.Sub(distFS, spaDistDir)
	if err != nil {
		s.log.Error("embed fs", "err", err)
		return
	}

	// Read the app shell once so the SPA fallback can write it directly. Going
	// through http.FileServer for the fallback is wrong: it special-cases
	// "/index.html" with a redirect to "./", which resolves against the original
	// deep-link path — so /subscriptions 301s to / (losing the page on reload)
	// and /mqtt/<id>/detail 301s to /mqtt/<id>/ and loops. Serving the bytes
	// ourselves keeps client-side routing intact for bookmarks and reloads.
	index, err := fs.ReadFile(sub, "index.html")
	if err != nil {
		s.log.Error("embed index.html", "err", err)
		return
	}
	fileServer := http.FileServer(http.FS(sub))

	serveIndex := func(w http.ResponseWriter) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(index)
	}

	s.mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		clean := strings.TrimPrefix(r.URL.Path, "/")
		if clean == "" || clean == "index.html" {
			serveIndex(w)
			return
		}

		// Serve a real embedded asset when one exists (and isn't a directory),
		// otherwise fall through to the SPA shell so client-side routing handles
		// the path. We never hand a non-asset path to http.FileServer, to avoid
		// its directory/index.html redirect behavior.
		if f, err := sub.Open(clean); err == nil {
			info, statErr := f.Stat()
			f.Close()
			if statErr == nil && !info.IsDir() {
				fileServer.ServeHTTP(w, r)
				return
			}
		}

		// SPA deep link (e.g. /subscriptions, /mqtt/<id>/detail): serve the shell.
		serveIndex(w)
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
