package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"sync"
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
var exitProcess = os.Exit

func main() {
	if err := run(os.Args[1:], os.Getenv, os.Stdout, os.Stderr, nil); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, "machmqtt-dashboard:", err)
		exitProcess(1)
	}
}

// run owns the process lifecycle. Explicit inputs make startup, shutdown, and
// break-glass initialization integration-testable without installing signals.
func run(args []string, getenv func(string) string, stdout, stderr io.Writer, shutdown <-chan os.Signal) error {
	flags := flag.NewFlagSet("machmqtt-dashboard", flag.ContinueOnError)
	flags.SetOutput(stderr)
	configPath := flags.String("config", "config.yaml", "path to config file")
	showVersion := flags.Bool("version", false, "print version and exit")
	exampleConfig := flags.Bool("example-config", false, "print an example config.yaml and exit")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *showVersion {
		_, err := fmt.Fprintln(stdout, "machmqtt-dashboard", version)
		return err
	}
	if *exampleConfig {
		_, err := fmt.Fprint(stdout, config.ExampleYAML())
		return err
	}

	lb := logbuf.New(slog.NewTextHandler(stderr, &slog.HandlerOptions{Level: slog.LevelInfo}), logbuf.DefaultSize)
	log := slog.New(lb)
	slog.SetDefault(log)

	cfg, err := config.Load(*configPath)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	db, err := store.Open(cfg.DataDir)
	if err != nil {
		return fmt.Errorf("open store: %w", err)
	}
	defer func() {
		if err := db.Close(); err != nil {
			log.Warn("close store", "err", err)
		}
	}()

	bootstrapPassword := cfg.Authentication.Local.BootstrapPassword
	if bootstrapPassword == "" {
		bootstrapPassword = getenv("MACHMQTT_DASHBOARD_BOOTSTRAP_PASSWORD")
	}
	// Retain the historical variable for existing deployments while the binary
	// and module complete their machmqtt-dashboard rename.
	if bootstrapPassword == "" {
		bootstrapPassword = getenv("NATS_DASHBOARD_BOOTSTRAP_PASSWORD")
	}
	defaultUser, err := db.EnsureBreakGlassAdmin(bootstrapPassword)
	if err != nil {
		return fmt.Errorf("ensure local break-glass administrator: %w", err)
	}
	if defaultUser != nil {
		log.Info("created bootstrap local administrator", "username", defaultUser.Username, "password_change_required", true)
	}

	if seeded, err := db.SeedClusters(cfg.Environments); err != nil {
		return fmt.Errorf("seed clusters from config: %w", err)
	} else if seeded > 0 {
		log.Info("seeded clusters from config", "count", seeded)
	}

	providers, err := auth.BuildProviderSet(cfg.Authentication, db)
	if err != nil {
		return fmt.Errorf("configure authentication providers: %w", err)
	}
	a := auth.NewWithProviderSet(db, cfg.SessionSecret, cfg.SecureCookies, providers)
	defer a.Close()
	a.SetLogger(log)
	if err := a.SetTrustedProxyCIDRs(cfg.Authentication.TrustedProxyCIDRs); err != nil {
		return fmt.Errorf("configure trusted proxies: %w", err)
	}
	if !cfg.SecureCookies {
		log.Warn("secure_cookies is disabled; session cookies may traverse plain HTTP")
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	metricsWriter := store.NewMetricsWriter(db, log, cfg.MetricsRetention)
	var background sync.WaitGroup
	background.Add(1)
	go func() {
		defer background.Done()
		metricsWriter.Run(ctx)
	}()

	hub := ws.NewHub(log)
	var manager *collector.Manager
	manager, err = collector.NewManager(cfg, func(clusterID string) {
		overview := manager.Overview(clusterID)
		hub.Broadcast(clusterID, "overview", overview)
		hub.Broadcast(clusterID, "topology", manager.Topology(clusterID))
		hub.Broadcast(clusterID, "health", manager.Health(clusterID))
		if sample := manager.BuildMetricSample(clusterID, time.Now(), overview); sample != nil {
			metricsWriter.Submit(*sample)
		}
	}, log, db)
	if err != nil {
		cancel()
		return fmt.Errorf("create collector manager: %w", err)
	}
	manager.Start(ctx)
	hub.SetOnSubscribe(func(client *ws.Client, clusterID string) {
		hub.SendTo(client, clusterID, "overview", manager.Overview(clusterID))
		hub.SendTo(client, clusterID, "topology", manager.Topology(clusterID))
		hub.SendTo(client, clusterID, "health", manager.Health(clusterID))
	})

	srv := api.NewServer(a, manager, hub, log, version, cfg, metricsWriter, db, lb)
	httpServer := &http.Server{
		Addr:           cfg.Listen,
		Handler:        srv.Handler(),
		MaxHeaderBytes: 1 << 20,
		ReadTimeout:    30 * time.Second,
		WriteTimeout:   60 * time.Second,
		IdleTimeout:    120 * time.Second,
	}
	listener, err := net.Listen("tcp", cfg.Listen)
	if err != nil {
		cancel()
		return fmt.Errorf("listen on %s: %w", cfg.Listen, err)
	}

	var ownedSignals chan os.Signal
	if shutdown == nil {
		ownedSignals = make(chan os.Signal, 1)
		signal.Notify(ownedSignals, processSignals()...)
		defer signal.Stop(ownedSignals)
		shutdown = ownedSignals
	}
	serveErr := make(chan error, 1)
	go func() {
		log.Info("starting server", "addr", listener.Addr().String())
		serveErr <- httpServer.Serve(listener)
	}()

	serveStopped := false
	select {
	case <-shutdown:
		log.Info("shutting down")
	case err := <-serveErr:
		serveStopped = true
		if err != nil && err != http.ErrServerClosed {
			cancel()
			return fmt.Errorf("serve HTTP: %w", err)
		}
	}
	srv.SetReady(false)

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()
	if err := httpServer.Shutdown(shutdownCtx); err != nil {
		log.Error("HTTP shutdown", "err", err)
		if closeErr := httpServer.Close(); closeErr != nil {
			log.Error("force HTTP close", "err", closeErr)
		}
	}
	if !serveStopped {
		select {
		case err := <-serveErr:
			if err != nil && err != http.ErrServerClosed {
				log.Warn("HTTP server stopped unexpectedly", "err", err)
			}
		case <-shutdownCtx.Done():
			log.Warn("HTTP server goroutine did not stop", "err", shutdownCtx.Err())
		}
	}
	cancel()
	drained := make(chan struct{})
	go func() {
		manager.Wait()
		background.Wait()
		close(drained)
	}()
	select {
	case <-drained:
	case <-shutdownCtx.Done():
		log.Warn("background shutdown incomplete", "err", shutdownCtx.Err())
	}
	collector.CloseMQTTIdleConnections()
	return nil
}
