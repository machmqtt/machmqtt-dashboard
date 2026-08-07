package collector

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

func BenchmarkParseMQTTMetrics(b *testing.B) {
	body := strings.Repeat("machmqtt_connections_active 100\nmachmqtt_connections_total 200\nmachmqtt_messages_received_total{qos=\"0\"} 300\n", 100)
	b.ReportAllocs()
	for index := 0; index < b.N; index++ {
		metrics := parsePrometheusMetrics(body)
		if metrics.ConnectionsActive != 100 || metrics.MsgsRecvQoS0 != 300 {
			b.Fatal("unexpected parsed metrics")
		}
	}
}

func BenchmarkBuildOverviewAndTopology(b *testing.B) {
	snapshot := &Snapshot{Timestamp: time.Now(), Varz: make(map[string]*Varz), Routez: make(map[string]*Routez), Gatewayz: make(map[string]*Gatewayz), Leafz: make(map[string]*Leafz), Health: make(map[string]*HealthStatus), Rates: make(map[string]*ServerRates)}
	for index := 0; index < 100; index++ {
		id := fmt.Sprintf("server-%03d", index)
		snapshot.Varz[id] = &Varz{ServerID: id, ServerName: id}
		snapshot.Rates[id] = &ServerRates{}
	}
	b.ReportAllocs()
	for index := 0; index < b.N; index++ {
		_ = buildOverview(snapshot)
		_ = buildTopology(snapshot, nil)
	}
}
