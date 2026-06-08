package collector

import (
	"context"
	"testing"
	"time"

	"github.com/noodlebit/machmqtt-dashboard/internal/config"
	"github.com/noodlebit/machmqtt-dashboard/internal/testutil/natstest"
)

func TestSYSCollectorSTATSZPopulatesVarz(t *testing.T) {
	s := natstest.NewWithSysAccount(t)

	cfg := &config.NATSConnConfig{
		URLs:          []string{s.ClientURL()},
		SYSCollection: true,
	}

	sc := newSYSCollector()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go sc.run(ctx, cfg)

	// Wait for NATS connection to be established.
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		sc.mu.RLock()
		connected := sc.nc != nil
		sc.mu.RUnlock()
		if connected {
			break
		}
		time.Sleep(25 * time.Millisecond)
	}

	sc.mu.RLock()
	nc := sc.nc
	sc.mu.RUnlock()
	if nc == nil {
		t.Fatal("SYSCollector did not connect within 3s")
	}

	// Bootstrap path: cache is empty, nc is connected → poll triggers PING.VARZ.
	snap := sc.poll(ctx, nil, false)
	if snap == nil {
		t.Fatal("poll returned nil on bootstrap")
	}
	if len(snap.Varz) == 0 {
		t.Fatal("Varz map is empty after bootstrap poll")
	}
	for id, v := range snap.Varz {
		if id == "" {
			t.Error("empty server ID in Varz map")
		}
		if v.ServerName == "" {
			t.Error("Varz.ServerName is empty")
		}
	}
}

func TestSYSCollectorSTATSZCachePath(t *testing.T) {
	s := natstest.NewWithSysAccount(t)

	cfg := &config.NATSConnConfig{
		URLs:          []string{s.ClientURL()},
		SYSCollection: true,
	}

	sc := newSYSCollector()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go sc.run(ctx, cfg)

	// Wait for at least one STATSZ to arrive (server emits quickly after connect).
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		sc.mu.RLock()
		n := len(sc.statsz)
		sc.mu.RUnlock()
		if n > 0 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	sc.mu.RLock()
	n := len(sc.statsz)
	sc.mu.RUnlock()
	if n == 0 {
		t.Fatal("no STATSZ received within 5s")
	}

	// Fast path: STATSZ cache is populated → poll builds Varz without PING.
	snap := sc.poll(ctx, nil, false)
	if snap == nil {
		t.Fatal("fast-path poll returned nil")
	}
	if len(snap.Varz) == 0 {
		t.Fatal("Varz map empty on fast-path poll")
	}
	for id, v := range snap.Varz {
		if v.ServerID == "" && id == "" {
			t.Error("both map key and Varz.ServerID are empty")
		}
		if v.Now.IsZero() {
			t.Errorf("Varz.Now is zero for server %s", id)
		}
		if v.Start.IsZero() {
			t.Errorf("Varz.Start is zero for server %s", id)
		}
	}
}

func TestSYSCollectorSlowPollPINGFanIn(t *testing.T) {
	s := natstest.NewWithSysAccount(t)

	cfg := &config.NATSConnConfig{
		URLs:          []string{s.ClientURL()},
		SYSCollection: true,
	}

	sc := newSYSCollector()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go sc.run(ctx, cfg)

	// Wait for nc to be set.
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		sc.mu.RLock()
		connected := sc.nc != nil
		sc.mu.RUnlock()
		if connected {
			break
		}
		time.Sleep(25 * time.Millisecond)
	}

	sc.mu.RLock()
	nc := sc.nc
	sc.mu.RUnlock()
	if nc == nil {
		t.Fatal("SYSCollector did not connect within 3s")
	}

	// Slow poll: triggers PING fan-in for all endpoint types.
	snap := sc.poll(ctx, nil, true)
	if snap == nil {
		t.Fatal("slow poll returned nil")
	}

	if len(snap.Varz) == 0 {
		t.Error("Varz empty after slow poll (PING.VARZ)")
	}
	if len(snap.Connz) == 0 {
		t.Error("Connz empty after slow poll (PING.CONNZ)")
	}
	if len(snap.Routez) == 0 {
		t.Error("Routez empty after slow poll (PING.ROUTEZ)")
	}
}

func TestSYSCollectorHEALTHZParsesIntoHealthStatus(t *testing.T) {
	s := natstest.NewWithSysAccount(t)

	cfg := &config.NATSConnConfig{
		URLs:          []string{s.ClientURL()},
		SYSCollection: true,
	}

	sc := newSYSCollector()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go sc.run(ctx, cfg)

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		sc.mu.RLock()
		connected := sc.nc != nil
		sc.mu.RUnlock()
		if connected {
			break
		}
		time.Sleep(25 * time.Millisecond)
	}

	sc.mu.RLock()
	nc := sc.nc
	sc.mu.RUnlock()
	if nc == nil {
		t.Fatal("SYSCollector did not connect within 3s")
	}

	snap := sc.poll(ctx, nil, true)
	if snap == nil {
		t.Fatal("poll returned nil")
	}

	if len(snap.Health) == 0 {
		t.Fatal("Health map empty — PING.HEALTHZ returned no replies")
	}
	for id, h := range snap.Health {
		if h.Status == "" {
			t.Errorf("HealthStatus.Status empty for server %s", id)
		}
	}
}

func TestSYSCollectorCarriesForwardSlowData(t *testing.T) {
	s := natstest.NewWithSysAccount(t)

	cfg := &config.NATSConnConfig{
		URLs:          []string{s.ClientURL()},
		SYSCollection: true,
	}

	sc := newSYSCollector()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go sc.run(ctx, cfg)

	// Wait for at least one STATSZ so the fast path is available.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		sc.mu.RLock()
		n := len(sc.statsz)
		sc.mu.RUnlock()
		if n > 0 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	// Slow poll to get Connz.
	slowSnap := sc.poll(ctx, nil, true)
	if slowSnap == nil || len(slowSnap.Connz) == 0 {
		t.Skip("slow poll returned no Connz — skipping carry-forward test")
	}

	// Fast poll with slow snap as carry: Connz should be carried forward.
	fastSnap := sc.poll(ctx, slowSnap, false)
	if fastSnap == nil {
		t.Fatal("fast poll returned nil")
	}
	if len(fastSnap.Connz) == 0 {
		t.Error("Connz not carried forward from previous slow snapshot")
	}
}

func TestFormatUptime(t *testing.T) {
	cases := []struct {
		d    time.Duration
		want string
	}{
		{5 * time.Second, "5s"},
		{90 * time.Second, "1m30s"},
		{2*time.Hour + 3*time.Minute + 4*time.Second, "2h3m4s"},
		{25*time.Hour + 6*time.Minute + 7*time.Second, "1d1h6m7s"},
	}
	for _, tc := range cases {
		if got := formatUptime(tc.d); got != tc.want {
			t.Errorf("formatUptime(%v) = %q, want %q", tc.d, got, tc.want)
		}
	}
}

func TestSYSCollectorSweepExpired(t *testing.T) {
	sc := newSYSCollector()
	sc.statsz["live"] = &statszEntry{when: time.Now()}
	sc.statsz["stale"] = &statszEntry{when: time.Now().Add(-2 * statszTTL)}
	sc.sweepExpired()
	if _, ok := sc.statsz["live"]; !ok {
		t.Error("live entry was unexpectedly removed")
	}
	if _, ok := sc.statsz["stale"]; ok {
		t.Error("stale entry was not removed")
	}
}

func TestSYSCollectorRunExitsOnConnectError(t *testing.T) {
	sc := newSYSCollector()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Empty URLs → connectNATS returns error → run() exits cleanly.
	go sc.run(ctx, &config.NATSConnConfig{})
	time.Sleep(20 * time.Millisecond)
	// nc should remain nil since connect failed.
	sc.mu.RLock()
	nc := sc.nc
	sc.mu.RUnlock()
	if nc != nil {
		t.Error("expected nc to remain nil after connect error")
	}
}

func TestSYSCollectorNilWhenNotConnected(t *testing.T) {
	// Use a URL that will not connect so nc stays nil.
	cfg := &config.NATSConnConfig{
		URLs:          []string{"nats://127.0.0.1:14299"}, // no server on this port
		SYSCollection: true,
	}

	sc := newSYSCollector()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go sc.run(ctx, cfg)
	time.Sleep(50 * time.Millisecond)

	// nc is nil → poll should return nil without panicking.
	snap := sc.poll(ctx, nil, false)
	if snap != nil {
		t.Errorf("expected nil snapshot when not connected, got non-nil")
	}
}
