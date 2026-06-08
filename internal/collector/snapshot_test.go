package collector

import (
	"testing"
	"time"
)

func TestComputeRates(t *testing.T) {
	now := time.Now()
	prev := &Snapshot{
		Varz: map[string]*Varz{
			"srv1": {InMsgs: 100, OutMsgs: 200, InBytes: 1000, OutBytes: 2000, Now: now},
		},
	}
	cur := &Snapshot{
		Varz: map[string]*Varz{
			"srv1": {InMsgs: 200, OutMsgs: 400, InBytes: 3000, OutBytes: 6000, Now: now.Add(10 * time.Second)},
		},
	}

	rates := computeRates(prev, cur)
	if rates == nil {
		t.Fatal("rates is nil")
	}
	r, ok := rates["srv1"]
	if !ok {
		t.Fatal("no rates for srv1")
	}
	if r.InMsgsRate != 10 {
		t.Errorf("InMsgsRate = %f, want 10", r.InMsgsRate)
	}
	if r.OutMsgsRate != 20 {
		t.Errorf("OutMsgsRate = %f, want 20", r.OutMsgsRate)
	}
	if r.InBytesRate != 200 {
		t.Errorf("InBytesRate = %f, want 200", r.InBytesRate)
	}
}

func TestComputeRatesSameTimestamp(t *testing.T) {
	now := time.Now()
	prev := &Snapshot{
		Varz: map[string]*Varz{
			"srv1": {InMsgs: 100, Now: now},
		},
	}
	cur := &Snapshot{
		Varz: map[string]*Varz{
			// Same Now timestamp → dt == 0 → skip this server.
			"srv1": {InMsgs: 200, Now: now},
		},
	}
	rates := computeRates(prev, cur)
	if rates == nil {
		t.Fatal("rates map should be non-nil even if empty")
	}
	if _, ok := rates["srv1"]; ok {
		t.Error("expected no rate entry when dt == 0")
	}
}

func TestComputeRatesServerNotInPrev(t *testing.T) {
	now := time.Now()
	prev := &Snapshot{
		Varz: map[string]*Varz{
			"other": {InMsgs: 0, Now: now},
		},
	}
	cur := &Snapshot{
		Varz: map[string]*Varz{
			// "srv1" is new — not present in prev → no rate entry.
			"srv1": {InMsgs: 100, Now: now.Add(5 * time.Second)},
		},
	}
	rates := computeRates(prev, cur)
	if _, ok := rates["srv1"]; ok {
		t.Error("expected no rate entry for server not in prev")
	}
}

func TestComputeRatesNilPrev(t *testing.T) {
	cur := &Snapshot{
		Varz: map[string]*Varz{"srv1": {InMsgs: 100}},
	}
	rates := computeRates(nil, cur)
	if rates != nil {
		t.Error("expected nil rates for nil prev")
	}
}

func TestBuildOverview(t *testing.T) {
	snap := &Snapshot{
		Varz: map[string]*Varz{
			"srv1": {ServerName: "nats-1", Version: "2.11.0", Connections: 100, CPU: 12.5, Mem: 1024 * 1024, Subscriptions: 500, Uptime: "1h"},
			"srv2": {ServerName: "nats-2", Version: "2.11.0", Connections: 50, CPU: 8.0, Mem: 512 * 1024, Subscriptions: 300, Uptime: "2h"},
		},
		Health: map[string]*HealthStatus{
			"srv1": {Status: "ok"},
			"srv2": {Status: "ok"},
		},
		Rates: map[string]*ServerRates{
			"srv1": {InMsgsRate: 100, OutMsgsRate: 200},
			"srv2": {InMsgsRate: 50, OutMsgsRate: 100},
		},
		JSInfo: map[string]*JSInfo{
			"srv1": {Streams: 3, Consumers: 5, Messages: 1000, Bytes: 50000},
		},
	}

	o := buildOverview(snap)
	if o.ServerCount != 2 {
		t.Errorf("ServerCount = %d, want 2", o.ServerCount)
	}
	if o.HealthyCount != 2 {
		t.Errorf("HealthyCount = %d, want 2", o.HealthyCount)
	}
	if o.ConnectionCount != 150 {
		t.Errorf("ConnectionCount = %d, want 150", o.ConnectionCount)
	}
	if o.InMsgsRate != 150 {
		t.Errorf("InMsgsRate = %f, want 150", o.InMsgsRate)
	}
	if o.JSStreams != 3 {
		t.Errorf("JSStreams = %d, want 3", o.JSStreams)
	}
}
