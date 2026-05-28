package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/noodlebit/nats-dashboard/internal/api"
	"github.com/noodlebit/nats-dashboard/internal/auth"
	"github.com/noodlebit/nats-dashboard/internal/collector"
	"github.com/noodlebit/nats-dashboard/internal/config"
	"github.com/noodlebit/nats-dashboard/internal/store"
	"github.com/noodlebit/nats-dashboard/internal/ws"
)

var version = "dev"

func main() {
	configPath := flag.String("config", "config.yaml", "path to config file")
	showVersion := flag.Bool("version", false, "print version and exit")
	flag.Parse()

	if *showVersion {
		fmt.Println("nats-dashboard", version)
		os.Exit(0)
	}

	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))

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

	a := auth.New(db, cfg.SessionSecret, cfg.SecureCookies)
	hub := ws.NewHub(log)

	metricsWriter := store.NewMetricsWriter(db.DB(), log)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go metricsWriter.Run(ctx)

	var manager *collector.Manager
	manager, err = collector.NewManager(cfg, func(envName string) {
		overview := manager.Overview(envName)
		hub.Broadcast(envName, "overview", overview)
		hub.Broadcast(envName, "topology", manager.Topology(envName))
		hub.Broadcast(envName, "health", manager.Health(envName))

		// Submit metrics sample for time-series storage.
		if overview != nil {
			sample := store.MetricSample{
				Timestamp:       time.Now(),
				Env:             envName,
				ServerCount:     overview.ServerCount,
				HealthyCount:    overview.HealthyCount,
				ConnectionCount: overview.ConnectionCount,
				InMsgsRate:      overview.InMsgsRate,
				OutMsgsRate:     overview.OutMsgsRate,
				InBytesRate:     overview.InBytesRate,
				OutBytesRate:    overview.OutBytesRate,
				Subscriptions:   overview.Subscriptions,
			}

			// Per-server metrics from the snapshot.
			snap := manager.Snapshot(envName)
			if snap != nil {
				for id, v := range snap.Varz {
					sm := store.ServerMetricSample{
						ServerID:      id,
						Connections:   v.Connections,
						InMsgs:        v.InMsgs,
						OutMsgs:       v.OutMsgs,
						InBytes:       v.InBytes,
						OutBytes:      v.OutBytes,
						CPU:           v.CPU,
						Mem:           v.Mem,
						Subscriptions: v.Subscriptions,
						SlowConsumers: v.SlowConsumers,
						Routes:        v.Routes,
						LeafNodes:     v.Leafs,
						Healthy:       true,
					}
					if h, ok := snap.Health[id]; ok {
						sm.Healthy = h.Status == "ok"
					}
					if r, ok := snap.Rates[id]; ok {
						sm.InMsgsRate = r.InMsgsRate
						sm.OutMsgsRate = r.OutMsgsRate
						sm.InBytesRate = r.InBytesRate
						sm.OutBytesRate = r.OutBytesRate
					}
					sample.Servers = append(sample.Servers, sm)
				}
			}

			// Per-MQTT bridge metrics.
			bridges := manager.MQTTBridges(envName)
			for _, b := range bridges {
				bm := store.MQTTBridgeMetricSample{
					BridgeID:     b.ConfiguredName,
					InMsgsRate:   b.InMsgsRate,
					OutMsgsRate:  b.OutMsgsRate,
					InBytesRate:  b.InBytesRate,
					OutBytesRate: b.OutBytesRate,
				}
				if bm.BridgeID == "" {
					bm.BridgeID = b.IP
				}
				if b.Status != nil && b.Status.Metrics != nil {
					bm.ConnectionsActive = b.Status.Metrics.ConnectionsActive
					bm.MsgsRecvQoS0 = b.Status.Metrics.MsgsRecvQoS0
					bm.MsgsRecvQoS1 = b.Status.Metrics.MsgsRecvQoS1
					bm.MsgsSentQoS0 = b.Status.Metrics.MsgsSentQoS0
					bm.MsgsSentQoS1 = b.Status.Metrics.MsgsSentQoS1
				}
				sample.MQTTBridges = append(sample.MQTTBridges, bm)
			}

			metricsWriter.Submit(sample)
		}
	}, log, db)
	if err != nil {
		log.Error("create collector manager", "err", err)
		os.Exit(1)
	}
	manager.Start(ctx)

	srv := api.NewServer(a, manager, hub, log, version, cfg, metricsWriter, db)

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
}
