package auth

import (
	"net/http"
	"sync"
	"time"
)

// LoginRateLimiter limits login attempts per IP address.
type LoginRateLimiter struct {
	mu       sync.Mutex
	attempts map[string][]time.Time
	window   time.Duration
	max      int
	stop     chan struct{}
}

// NewLoginRateLimiter creates a rate limiter that allows max attempts per window per IP.
func NewLoginRateLimiter(max int, window time.Duration) *LoginRateLimiter {
	rl := &LoginRateLimiter{
		attempts: make(map[string][]time.Time),
		window:   window,
		max:      max,
		stop:     make(chan struct{}),
	}
	go rl.cleanup()
	return rl
}

// Stop terminates the background cleanup goroutine.
func (rl *LoginRateLimiter) Stop() {
	close(rl.stop)
}

// Allow checks whether the given IP is allowed to attempt a login.
func (rl *LoginRateLimiter) Allow(ip string) bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	now := time.Now()
	cutoff := now.Add(-rl.window)

	// Prune old attempts.
	recent := rl.attempts[ip]
	start := 0
	for start < len(recent) && recent[start].Before(cutoff) {
		start++
	}
	recent = recent[start:]

	if len(recent) >= rl.max {
		rl.attempts[ip] = recent
		return false
	}

	rl.attempts[ip] = append(recent, now)
	return true
}

// sweepOnce removes all per-IP attempt lists that have fully expired.
// IPs with at least one recent attempt are trimmed to drop only the stale prefix.
func (rl *LoginRateLimiter) sweepOnce() {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	cutoff := time.Now().Add(-rl.window)
	for ip, attempts := range rl.attempts {
		start := 0
		for start < len(attempts) && attempts[start].Before(cutoff) {
			start++
		}
		if start == len(attempts) {
			delete(rl.attempts, ip)
		} else {
			rl.attempts[ip] = attempts[start:]
		}
	}
}

// cleanup periodically removes stale entries to prevent unbounded memory growth.
func (rl *LoginRateLimiter) cleanup() {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-rl.stop:
			return
		case <-ticker.C:
			rl.sweepOnce()
		}
	}
}

// clientIP extracts the client IP from the request, preferring X-Forwarded-For
// for deployments behind a reverse proxy.
func clientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		// Take the first IP (the original client).
		for i := 0; i < len(xff); i++ {
			if xff[i] == ',' {
				return xff[:i]
			}
		}
		return xff
	}
	// Strip port from RemoteAddr.
	addr := r.RemoteAddr
	for i := len(addr) - 1; i >= 0; i-- {
		if addr[i] == ':' {
			return addr[:i]
		}
	}
	return addr
}
