package auth

import (
	"net"
	"net/http/httptest"
	"testing"
	"time"
)

func TestEvictionReclaimsExpiredKeysBeforeSheddingLiveOnes(t *testing.T) {
	rl := NewLoginRateLimiter(5, time.Minute)
	defer rl.Stop()

	now := time.Now()
	stale := now.Add(-2 * time.Minute)
	rl.attempts["expired-a"] = []time.Time{stale}
	rl.attempts["expired-b"] = []time.Time{stale}
	rl.attempts["live"] = []time.Time{now}
	rl.maxKeys = len(rl.attempts)

	if !rl.Allow("new-source") {
		t.Fatal("limiter rejected an unseen key instead of reclaiming expired ones")
	}

	rl.mu.Lock()
	defer rl.mu.Unlock()
	for _, key := range []string{"expired-a", "expired-b"} {
		if _, ok := rl.attempts[key]; ok {
			t.Errorf("expired key %q survived eviction", key)
		}
	}
	// Reclaiming the expired keys freed enough room, so the still-active key
	// must not be shed as collateral.
	if _, ok := rl.attempts["live"]; !ok {
		t.Error("eviction dropped a key that still had recent attempts")
	}
	if _, ok := rl.attempts["new-source"]; !ok {
		t.Error("admitted key was not recorded")
	}
	if got := rl.evicted.Load(); got != 2 {
		t.Errorf("evicted = %d, want 2", got)
	}
}

func TestClientIPIgnoresForwardedHeaderFromUntrustedPeer(t *testing.T) {
	req := httptest.NewRequest("POST", "/api/login", nil)
	req.RemoteAddr = "192.0.2.10:1234"
	req.Header.Set("X-Forwarded-For", "203.0.113.50")
	if got := clientIP(req, nil); got != "192.0.2.10" {
		t.Errorf("client IP = %q", got)
	}
}

func TestClientIPWalksTrustedProxyChain(t *testing.T) {
	_, trusted, _ := net.ParseCIDR("10.0.0.0/8")
	req := httptest.NewRequest("POST", "/api/login", nil)
	req.RemoteAddr = "10.0.0.2:1234"
	req.Header.Set("X-Forwarded-For", "203.0.113.50, 10.0.0.1")
	if got := clientIP(req, []*net.IPNet{trusted}); got != "203.0.113.50" {
		t.Errorf("client IP = %q", got)
	}
}
