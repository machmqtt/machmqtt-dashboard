package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/noodlebit/machmqtt-dashboard/internal/collector"
	"github.com/noodlebit/machmqtt-dashboard/internal/config"
	"github.com/noodlebit/machmqtt-dashboard/internal/store"
)

func writeConfig(t *testing.T, dataDir, listen, serverURL string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	content := fmt.Sprintf(`listen: %q
poll_interval: 1s
metrics_retention: 1h
session_secret: %q
data_dir: %q
authentication:
  local:
    enabled: true
    break_glass_login: true
environments:
  - name: test
    mqtt_discovery:
      enabled: false
    servers:
      - url: %q
`, listen, strings.Repeat("s", 32), dataDir, serverURL)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestRunVersionFlagsAndConfigurationErrors(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if err := run([]string{"-version"}, os.Getenv, &stdout, &stderr, make(chan os.Signal)); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), "machmqtt-dashboard "+version) {
		t.Fatalf("version output=%q", stdout.String())
	}
	if err := run([]string{"-does-not-exist"}, os.Getenv, &stdout, &stderr, make(chan os.Signal)); err == nil {
		t.Fatal("unknown flag should fail")
	}
	if err := run([]string{"-config", filepath.Join(t.TempDir(), "missing.yaml")}, os.Getenv, &stdout, &stderr, make(chan os.Signal)); err == nil {
		t.Fatal("missing config should fail")
	}
}

func TestMainVersionPath(t *testing.T) {
	previous := os.Args
	os.Args = []string{"machmqtt-dashboard", "-version"}
	t.Cleanup(func() { os.Args = previous })
	main()
}

func TestMainErrorPath(t *testing.T) {
	previousArgs, previousExit := os.Args, exitProcess
	os.Args = []string{"machmqtt-dashboard", "-unknown"}
	exitCode := 0
	exitProcess = func(code int) { exitCode = code }
	t.Cleanup(func() {
		os.Args = previousArgs
		exitProcess = previousExit
	})
	main()
	if exitCode != 1 {
		t.Fatalf("exit code = %d", exitCode)
	}
}

func TestRunStartupBootstrapRestartAndGracefulShutdown(t *testing.T) {
	dataDir := t.TempDir()
	configPath := writeConfig(t, dataDir, "127.0.0.1:0", "http://127.0.0.1:1")
	getenv := func(key string) string {
		if key == "NATS_DASHBOARD_BOOTSTRAP_PASSWORD" {
			return "bootstrap-password"
		}
		return ""
	}
	for i := 0; i < 2; i++ {
		shutdown := make(chan os.Signal, 1)
		shutdown <- os.Interrupt
		var output bytes.Buffer
		if err := run([]string{"-config", configPath}, getenv, &output, &output, shutdown); err != nil {
			t.Fatalf("run %d: %v\n%s", i, err, output.String())
		}
		if !strings.Contains(output.String(), "shutting down") {
			t.Fatalf("shutdown log missing: %s", output.String())
		}
	}
}

func TestRunRequiresBootstrapAndReportsListenFailure(t *testing.T) {
	var output bytes.Buffer
	missingBootstrap := writeConfig(t, t.TempDir(), "127.0.0.1:0", "http://127.0.0.1:1")
	if err := run([]string{"-config", missingBootstrap}, func(string) string { return "" }, &output, &output, make(chan os.Signal)); err == nil {
		t.Fatal("fresh install without bootstrap should fail")
	}

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	occupied := writeConfig(t, t.TempDir(), listener.Addr().String(), "http://127.0.0.1:1")
	if err := run([]string{"-config", occupied}, func(string) string { return "bootstrap-password" }, &output, &output, make(chan os.Signal)); err == nil || !strings.Contains(err.Error(), "listen") {
		t.Fatalf("listen error=%v", err)
	}
}

func TestRunReportsStoreOpenFailure(t *testing.T) {
	dataFile := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(dataFile, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	configPath := writeConfig(t, dataFile, "127.0.0.1:0", "http://127.0.0.1:1")
	var output bytes.Buffer
	err := run([]string{"-config", configPath}, func(string) string { return "bootstrap-password" }, &output, &output, make(chan os.Signal))
	if err == nil || !strings.Contains(err.Error(), "open store") {
		t.Fatalf("store error=%v", err)
	}
}

func TestRunOwnsSignalHandlerWhenNotInjected(t *testing.T) {
	configPath := writeConfig(t, t.TempDir(), "127.0.0.1:0", "http://127.0.0.1:1")
	output := &startupWriter{started: make(chan struct{})}
	go func() {
		<-output.started
		process, err := os.FindProcess(os.Getpid())
		if err == nil {
			_ = process.Signal(os.Interrupt)
		}
	}()
	if err := run([]string{"-config", configPath}, func(string) string { return "bootstrap-password" }, output, output, nil); err != nil {
		t.Fatal(err)
	}
}

type startupWriter struct {
	mu      sync.Mutex
	buffer  bytes.Buffer
	started chan struct{}
	once    sync.Once
}

func (w *startupWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if strings.Contains(string(p), "starting server") {
		w.once.Do(func() { close(w.started) })
	}
	return w.buffer.Write(p)
}

func TestRunPollsLiveNATSServerBeforeShutdown(t *testing.T) {
	nats := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/varz":
			fmt.Fprint(w, `{"server_id":"s1","server_name":"one","connections":2}`)
		case "/routez":
			fmt.Fprint(w, `{"server_id":"s1","routes":[]}`)
		case "/gatewayz":
			fmt.Fprint(w, `{"server_id":"s1","outbound_gateways":{},"inbound_gateways":{}}`)
		case "/leafz":
			fmt.Fprint(w, `{"server_id":"s1","leafs":[]}`)
		case "/healthz":
			fmt.Fprint(w, `{"status":"ok"}`)
		case "/connz":
			fmt.Fprint(w, `{"server_id":"s1","connections":[]}`)
		case "/subsz", "/jsz", "/accountz":
			fmt.Fprint(w, `{"server_id":"s1"}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer nats.Close()
	configPath := writeConfig(t, t.TempDir(), "127.0.0.1:0", nats.URL)
	shutdown := make(chan os.Signal, 1)
	shutdown <- os.Interrupt
	var output bytes.Buffer
	if err := run([]string{"-config", configPath}, func(string) string { return "bootstrap-password" }, &output, &output, shutdown); err != nil {
		t.Fatal(err)
	}
}

func TestBuildMetricSampleIncludesServerRatesHealthAndMQTT(t *testing.T) {
	mqtt := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/readyz":
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"status":"ready","name":"bridge-one","nats_connected":true}`)
		case "/metrics":
			fmt.Fprint(w, "machmqtt_connections_active 3\nmachmqtt_msgs_recv_qos0 4\n")
		default:
			http.NotFound(w, r)
		}
	}))
	defer mqtt.Close()
	_, portText, err := net.SplitHostPort(strings.TrimPrefix(mqtt.URL, "http://"))
	if err != nil {
		t.Fatal(err)
	}
	var mqttPort int
	if _, err := fmt.Sscan(portText, &mqttPort); err != nil {
		t.Fatal(err)
	}
	nats := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/varz":
			fmt.Fprint(w, `{"server_id":"s1","server_name":"one","connections":2,"in_msgs":20,"out_msgs":10}`)
		case "/routez":
			fmt.Fprint(w, `{"server_id":"s1","routes":[]}`)
		case "/gatewayz":
			fmt.Fprint(w, `{"server_id":"s1","outbound_gateways":{},"inbound_gateways":{}}`)
		case "/leafz":
			fmt.Fprint(w, `{"server_id":"s1","leafs":[]}`)
		case "/healthz":
			fmt.Fprint(w, `{"status":"error"}`)
		case "/connz":
			fmt.Fprint(w, `{"server_id":"s1","total":1,"connections":[{"cid":1,"name":"machmqtt-bridge","ip":"127.0.0.1"}]}`)
		case "/subsz", "/jsz", "/accountz":
			fmt.Fprint(w, `{"server_id":"s1"}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer nats.Close()
	enabled := true
	cfg := &config.Config{PollInterval: 20 * time.Millisecond, Environments: []config.Environment{{
		Name: "test", Servers: []config.Server{{URL: nats.URL}},
		MQTTDiscovery: &config.MQTTDiscoveryConfig{Enabled: &enabled, AdminPorts: []int{mqttPort}},
	}}}
	db, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.SeedClusters(cfg.Environments); err != nil {
		t.Fatal(err)
	}
	clusters, err := db.ListClusters()
	if err != nil || len(clusters) != 1 {
		t.Fatalf("seeded clusters=%v err=%v", clusters, err)
	}
	clusterID := clusters[0].ID
	updates := make(chan struct{}, 8)
	manager, err := collector.NewManager(cfg, func(string) { updates <- struct{}{} }, slog.New(slog.NewTextHandler(io.Discard, nil)), db)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	manager.Start(ctx)
	defer func() {
		cancel()
		manager.Wait()
	}()
	deadline := time.After(2 * time.Second)
	for len(manager.MQTTBridges(clusterID)) == 0 {
		select {
		case <-updates:
		case <-deadline:
			t.Fatal("collector did not discover MQTT bridge")
		}
	}
	time.Sleep(60 * time.Millisecond)
	overview := manager.Overview(clusterID)
	if overview == nil {
		t.Fatal("overview missing")
	}
	sample := manager.BuildMetricSample(clusterID, time.Now(), overview)
	if sample == nil || len(sample.Servers) != 1 || sample.Servers[0].Healthy {
		t.Fatalf("server metrics=%+v", sample.Servers)
	}
	if len(sample.MQTTBridges) != 1 || sample.MQTTBridges[0].BridgeID == "" || sample.MQTTBridges[0].ConnectionsActive != 3 {
		t.Fatalf("MQTT metrics=%+v", sample.MQTTBridges)
	}
}
