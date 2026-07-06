package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/noodlebit/machmqtt-dashboard/internal/api"
	"github.com/noodlebit/machmqtt-dashboard/internal/auth"
	"github.com/noodlebit/machmqtt-dashboard/internal/collector"
	"github.com/noodlebit/machmqtt-dashboard/internal/config"
	"github.com/noodlebit/machmqtt-dashboard/internal/logbuf"
	"github.com/noodlebit/machmqtt-dashboard/internal/store"
	"github.com/noodlebit/machmqtt-dashboard/internal/ws"
)

var version = "dev"

func main() {
	configPath := flag.String("config", "config.yaml", "path to config file")
	showVersion := flag.Bool("version", false, "print version and exit")
	exampleConfig := flag.Bool("example-config", false, "print an example config.yaml to stdout and exit")
	flag.Parse()

	if *showVersion {
		fmt.Println("machmqtt-dashboard", version)
		os.Exit(0)
	}

	if *exampleConfig {
		fmt.Print(config.ExampleYAML())
		os.Exit(0)
	}

	lb := logbuf.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}), logbuf.DefaultSize)
	log := slog.New(lb)
	// Make the buffered handler the process default so packages that log via the
	// slog package functions (store, auth, api helpers) also land in the in-UI
	// Server Logs buffer instead of bypassing it.
	slog.SetDefault(log)

	cfg, err := config.Load(*configPath)
	if err != nil {
		log.Error("load config", "err", err)
		os.Exit(1)
	}

	db, err := store.Open(cfg.DataDir)
	if err != nil {
		log.Error("open store", "err", err)
		os.Exit(1)
	}
	defer db.Close()

	// Create default admin/admin user on first startup if no users exist.
	defaultUser, err := db.EnsureDefaultAdmin()
	if err != nil {
		log.Error("ensure default admin", "err", err)
		os.Exit(1)
	}
	if defaultUser != nil {
		log.Info("created default admin user", "username", defaultUser.Username)
	}

	// Seed any clusters declared in the config file that don't already exist
	// (idempotent, matched by name). This lets a fresh `docker compose up` poll
	// the configured servers with no manual cluster-creation step; existing
	// clusters (managed via the admin UI) are never overwritten.
	if seeded, err := db.SeedClusters(cfg.Environments); err != nil {
		log.Error("seed clusters from config", "err", err)
		os.Exit(1)
	} else if seeded > 0 {
		log.Info("seeded clusters from config", "count", seeded)
	}

	a := auth.New(db, cfg.SessionSecret, cfg.SecureCookies, cfg.TrustProxyHeaders, log)
	if !cfg.SecureCookies {
		log.Warn("secure_cookies is disabled: session cookies will be sent over plain HTTP — set secure_cookies: true when the dashboard is served over HTTPS/TLS")
	}
	hub := ws.NewHub(log)

	metricsWriter := store.NewMetricsWriter(db, log, cfg.MetricsRetention)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var bg sync.WaitGroup
	bg.Add(1)
	go func() {
		defer bg.Done()
		metricsWriter.Run(ctx)
	}()

	var manager *collector.Manager
	manager, err = collector.NewManager(cfg, func(clusterID string) {
		overview := manager.Overview(clusterID)
		hub.Broadcast(clusterID, "overview", overview)
		hub.Broadcast(clusterID, "topology", manager.Topology(clusterID))
		hub.Broadcast(clusterID, "health", manager.Health(clusterID))

		// Submit a metrics sample for time-series storage.
		if sample := manager.BuildMetricSample(clusterID, time.Now(), overview); sample != nil {
			metricsWriter.Submit(*sample)
		}
	}, log, db)
	if err != nil {
		log.Error("create collector manager", "err", err)
		os.Exit(1)
	}
	manager.Start(ctx)

	hub.SetOnSubscribe(func(c *ws.Client, env string) {
		hub.SendTo(c, env, "overview", manager.Overview(env))
		hub.SendTo(c, env, "topology", manager.Topology(env))
		hub.SendTo(c, env, "health", manager.Health(env))
	})

	srv := api.NewServer(a, manager, hub, log, version, cfg, metricsWriter, db, lb)

	httpServer := &http.Server{
		Addr:           cfg.Listen,
		Handler:        srv.Handler(),
		MaxHeaderBytes: 1 << 20, // 1 MB
		ReadTimeout:    30 * time.Second,
		WriteTimeout:   60 * time.Second,
		IdleTimeout:    120 * time.Second,
	}

	// Graceful shutdown.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		log.Info("starting server", "addr", cfg.Listen)
		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Error("server error", "err", err)
			os.Exit(1)
		}
	}()

	<-sigCh
	log.Info("shutting down")
	cancel()

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()
	httpServer.Shutdown(shutdownCtx)

	// Join the collector and metrics-writer goroutines (bounded) so no write is
	// in flight when the deferred db.Close() runs. Prevents the "database is
	// closed" race and a dropped final metrics sample on shutdown.
	drained := make(chan struct{})
	go func() {
		manager.Wait()
		bg.Wait()
		close(drained)
	}()
	select {
	case <-drained:
	case <-time.After(5 * time.Second):
		log.Warn("shutdown: timed out waiting for background goroutines to drain")
	}
}
