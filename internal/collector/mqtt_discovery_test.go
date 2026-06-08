package collector

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"
)

// bridgeAdminMux builds a minimal MachMQTT admin handler that FetchStatus needs.
func bridgeAdminMux(t *testing.T) *http.ServeMux {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/readyz", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(MQTTReadyz{Status: "ready", Connections: 3, NATSConnected: true})
	})
	mux.HandleFunc("/diag/nats", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(MQTTNATSDiag{Connection: MQTTNATSConnection{Connected: true}})
	})
	mux.HandleFunc("/pool", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(MQTTPool{Size: 2})
	})
	mux.HandleFunc("/metrics", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("machmqtt_connections_active 3\n"))
	})
	mux.HandleFunc("/connz", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(MQTTConnz{Total: 5})
	})
	return mux
}

// portFromURL extracts the port number from a URL like http://127.0.0.1:56789.
func portFromURL(u string) int {
	parts := strings.Split(u, ":")
	if len(parts) < 3 {
		return 0
	}
	p, _ := strconv.Atoi(parts[len(parts)-1])
	return p
}

func TestDiscoverMQTTBridgesNilSnap(t *testing.T) {
	result := DiscoverMQTTBridges(context.Background(), nil, nil, nil, "")
	if result != nil {
		t.Errorf("expected nil for nil snapshot, got %v", result)
	}
}

func TestDiscoverMQTTBridgesEmptyConnz(t *testing.T) {
	snap := &Snapshot{
		Varz:  map[string]*Varz{"srv-1": {ServerName: "nats-1"}},
		Connz: map[string]*Connz{},
	}
	result := DiscoverMQTTBridges(context.Background(), snap, nil, nil, "")
	if result != nil {
		t.Errorf("expected nil when no bridge connections found, got %v", result)
	}
}

func TestDiscoverMQTTBridgesNoMQTTConns(t *testing.T) {
	snap := &Snapshot{
		Varz: map[string]*Varz{"srv-1": {ServerName: "nats-1"}},
		Connz: map[string]*Connz{
			"srv-1": {Conns: []ConnInfo{
				{Name: "plain-client", IP: "10.0.0.1"},
			}},
		},
	}
	result := DiscoverMQTTBridges(context.Background(), snap, nil, nil, "")
	if result != nil {
		t.Errorf("expected nil when no bridge-named connections, got %v", result)
	}
}

func TestDiscoverMQTTBridgesProbeSuccess(t *testing.T) {
	srv := httptest.NewServer(bridgeAdminMux(t))
	defer srv.Close()
	port := portFromURL(srv.URL)

	snap := &Snapshot{
		Varz: map[string]*Varz{"srv-1": {ServerName: "nats-1"}},
		Connz: map[string]*Connz{
			"srv-1": {Conns: []ConnInfo{
				{Name: "machmqtt-bridge", IP: "127.0.0.1", InMsgs: 100, OutMsgs: 50},
				{Name: "machmqtt-pool-0", IP: "127.0.0.1", InMsgs: 20, OutMsgs: 10},
			}},
		},
	}
	instances := DiscoverMQTTBridges(context.Background(), snap, nil, []int{port}, "")
	if len(instances) != 1 {
		t.Fatalf("got %d instances, want 1", len(instances))
	}
	if !instances[0].Reachable {
		t.Error("expected Reachable=true after successful probe")
	}
	if instances[0].PoolConns != 2 {
		t.Errorf("PoolConns = %d, want 2", instances[0].PoolConns)
	}
}

func TestDiscoverMQTTBridgesProbeFailure(t *testing.T) {
	snap := &Snapshot{
		Varz: map[string]*Varz{"srv-1": {ServerName: "nats-1"}},
		Connz: map[string]*Connz{
			"srv-1": {Conns: []ConnInfo{
				{Name: "machmqtt-bridge", IP: "192.0.2.1"}, // unreachable IP
			}},
		},
	}
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	instances := DiscoverMQTTBridges(ctx, snap, nil, []int{18080}, "")
	if len(instances) != 1 {
		t.Fatalf("got %d instances, want 1 (unreachable bridge still appears)", len(instances))
	}
	if instances[0].Reachable {
		t.Error("expected Reachable=false for unreachable bridge")
	}
}

func TestDiscoverMQTTBridgesDefaultPort(t *testing.T) {
	// adminPorts nil → defaults to []int{8080}; just check it doesn't panic/deadlock.
	snap := &Snapshot{
		Varz: map[string]*Varz{"srv-1": {ServerName: "nats-1"}},
		Connz: map[string]*Connz{
			"srv-1": {Conns: []ConnInfo{
				{Name: "machmqtt-bridge", IP: "192.0.2.2"},
			}},
		},
	}
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	instances := DiscoverMQTTBridges(ctx, snap, nil, nil, "") // nil ports → default 8080
	if len(instances) != 1 {
		t.Fatalf("got %d instances, want 1", len(instances))
	}
}

func TestDiscoverMQTTBridgesWithPrevSnapshot(t *testing.T) {
	srv := httptest.NewServer(bridgeAdminMux(t))
	defer srv.Close()
	port := portFromURL(srv.URL)

	prev := &Snapshot{
		Timestamp: time.Now().Add(-10 * time.Second),
		Connz: map[string]*Connz{
			"srv-1": {Conns: []ConnInfo{
				{Name: "machmqtt-bridge", IP: "127.0.0.1", InMsgs: 50, OutMsgs: 25},
			}},
		},
	}
	snap := &Snapshot{
		Timestamp: time.Now(),
		Varz:      map[string]*Varz{"srv-1": {ServerName: "nats-1"}},
		Connz: map[string]*Connz{
			"srv-1": {Conns: []ConnInfo{
				{Name: "machmqtt-bridge", IP: "127.0.0.1", InMsgs: 100, OutMsgs: 50},
			}},
		},
	}
	instances := DiscoverMQTTBridges(context.Background(), snap, prev, []int{port}, "")
	if len(instances) != 1 {
		t.Fatalf("got %d instances, want 1", len(instances))
	}
	if instances[0].InMsgsRate <= 0 {
		t.Errorf("expected positive InMsgsRate, got %f", instances[0].InMsgsRate)
	}
}

func TestDiscoverMQTTBridgesIPv6Brackets(t *testing.T) {
	srv := httptest.NewServer(bridgeAdminMux(t))
	defer srv.Close()
	port := portFromURL(srv.URL)

	snap := &Snapshot{
		Varz: map[string]*Varz{"srv-1": {ServerName: "nats-1"}},
		Connz: map[string]*Connz{
			"srv-1": {Conns: []ConnInfo{
				// IPv6 address with colons → should be wrapped in brackets for URL.
				{Name: "machmqtt-bridge", IP: "::1"},
			}},
		},
		// ServerURLs maps ::1 to localhost for loopback resolution.
		ServerURLs: map[string]string{"srv-1": "127.0.0.1"},
	}
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	instances := DiscoverMQTTBridges(ctx, snap, nil, []int{port}, "")
	// Should not panic; reachability depends on whether loopback was resolved.
	_ = instances
}

func TestDiscoverMQTTBridgesContextCancelled(t *testing.T) {
	snap := &Snapshot{
		Varz: map[string]*Varz{"srv-1": {ServerName: "nats-1"}},
		Connz: map[string]*Connz{
			"srv-1": {Conns: []ConnInfo{
				{Name: "machmqtt-bridge", IP: "192.0.2.3"},
			}},
		},
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // pre-cancel

	// Should return without panicking even with a cancelled context.
	instances := DiscoverMQTTBridges(ctx, snap, nil, []int{18081}, "")
	_ = instances
}
