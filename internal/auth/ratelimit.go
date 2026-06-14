package auth

import (
	"net"
	"net/http"
	"strings"
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

// clientIP extracts the client IP used to key the login rate limiter.
//
// X-Forwarded-For is only honored when trustProxy is true (the dashboard is
// behind a reverse proxy known to set the header). When it's false, the header
// is ignored entirely and RemoteAddr is used — otherwise any client could send
// a different X-Forwarded-For per request to mint a fresh rate-limit bucket and
// defeat the limiter.
func clientIP(r *http.Request, trustProxy bool) string {
	if trustProxy {
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			// Take the LAST entry. A trusted reverse proxy appends the client IP
			// it actually observed (e.g. nginx's $proxy_add_x_forwarded_for), so
			// the rightmost hop is the one the proxy vouches for. Earlier entries
			// are whatever the client sent and remain spoofable.
			if i := strings.LastIndexByte(xff, ','); i >= 0 {
				return strings.TrimSpace(xff[i+1:])
			}
			return strings.TrimSpace(xff)
		}
	}
	// Strip the port from RemoteAddr (host:port). Falls back to the raw value
	// for inputs without a port.
	if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		return host
	}
	return r.RemoteAddr
}
