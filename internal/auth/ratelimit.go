package auth

import (
	"net"
	"net/http"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// LoginRateLimiter limits login attempts per IP address.
type LoginRateLimiter struct {
	mu       sync.Mutex
	attempts map[string][]time.Time
	window   time.Duration
	max      int
	stop     chan struct{}
	stopOnce sync.Once
	maxKeys  int
	rejected atomic.Uint64
	evicted  atomic.Uint64
}

// NewLoginRateLimiter creates a rate limiter that allows max attempts per window per IP.
func NewLoginRateLimiter(max int, window time.Duration) *LoginRateLimiter {
	rl := &LoginRateLimiter{
		attempts: make(map[string][]time.Time),
		window:   window,
		max:      max,
		stop:     make(chan struct{}),
		maxKeys:  10000,
	}
	go rl.cleanup()
	return rl
}

// Stop terminates the background cleanup goroutine.
func (rl *LoginRateLimiter) Stop() {
	rl.stopOnce.Do(func() { close(rl.stop) })
}

// Allow checks whether the given IP is allowed to attempt a login.
func (rl *LoginRateLimiter) Allow(ip string) bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	now := time.Now()
	cutoff := now.Add(-rl.window)

	// Prune old attempts.
	recent := rl.attempts[ip]
	if len(recent) == 0 && len(rl.attempts) >= rl.maxKeys {
		// Reclaim capacity by discarding the least-recently-active sources rather
		// than rejecting the new one: refusing unseen keys would let anyone who
		// can vary the client address lock every other client out of logging in.
		rl.evictStaleLocked(cutoff)
	}
	start := 0
	for start < len(recent) && recent[start].Before(cutoff) {
		start++
	}
	recent = recent[start:]

	if len(recent) >= rl.max {
		rl.rejected.Add(1)
		rl.attempts[ip] = recent
		return false
	}

	rl.attempts[ip] = append(recent, now)
	return true
}

// evictStaleLocked frees room in a full attempt table. Fully expired keys go
// first. If that is not enough it sheds the least-recently-active keys, but
// never one that is currently at its limit — evicting a blocked key would reset
// its budget and turn eviction pressure into a lockout bypass. When every
// resident key is blocked there is nothing safe to drop, and the caller is
// admitted without the table growing past its purpose as a memory guard.
// Callers hold rl.mu.
func (rl *LoginRateLimiter) evictStaleLocked(cutoff time.Time) {
	for key, attempts := range rl.attempts {
		if len(attempts) == 0 || attempts[len(attempts)-1].Before(cutoff) {
			delete(rl.attempts, key)
			rl.evicted.Add(1)
		}
	}
	if len(rl.attempts) < rl.maxKeys {
		return
	}
	type aged struct {
		key  string
		last time.Time
	}
	// Only keys with budget left are eligible; a blocked key must outlive the
	// pressure that would otherwise clear it.
	evictable := make([]aged, 0, len(rl.attempts))
	for key, attempts := range rl.attempts {
		live := 0
		for _, at := range attempts {
			if !at.Before(cutoff) {
				live++
			}
		}
		if live >= rl.max {
			continue
		}
		evictable = append(evictable, aged{key: key, last: attempts[len(attempts)-1]})
	}
	sort.Slice(evictable, func(i, j int) bool { return evictable[i].last.Before(evictable[j].last) })
	drop := len(rl.attempts) - rl.maxKeys + rl.maxKeys/10 + 1
	for i := 0; i < drop && i < len(evictable); i++ {
		delete(rl.attempts, evictable[i].key)
		rl.evicted.Add(1)
	}
}

type RateLimiterStats struct {
	Keys     int
	Rejected uint64
	Evicted  uint64
}

func (rl *LoginRateLimiter) Stats() RateLimiterStats {
	return RateLimiterStats{Keys: rl.Size(), Rejected: rl.rejected.Load(), Evicted: rl.evicted.Load()}
}

func (rl *LoginRateLimiter) Size() int {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	return len(rl.attempts)
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
			rl.prune(time.Now())
		}
	}
}

func (rl *LoginRateLimiter) prune(now time.Time) {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	cutoff := now.Add(-rl.window)
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

// clientIP uses forwarded addresses only when the direct peer is trusted. It
// walks the chain from right to left so clients cannot spoof the leftmost XFF.
func clientIP(r *http.Request, trustedProxies []*net.IPNet) string {
	remote := remoteIP(r.RemoteAddr)
	if !ipInNetworks(remote, trustedProxies) {
		return remote
	}
	forwarded := strings.Split(r.Header.Get("X-Forwarded-For"), ",")
	for i := len(forwarded) - 1; i >= 0; i-- {
		candidate := strings.TrimSpace(forwarded[i])
		if net.ParseIP(candidate) == nil {
			continue
		}
		if !ipInNetworks(candidate, trustedProxies) {
			return candidate
		}
	}
	return remote
}

func remoteIP(address string) string {
	host, _, err := net.SplitHostPort(address)
	if err == nil {
		return host
	}
	return address
}

func ipInNetworks(value string, networks []*net.IPNet) bool {
	ip := net.ParseIP(value)
	if ip == nil {
		return false
	}
	for _, network := range networks {
		if network.Contains(ip) {
			return true
		}
	}
	return false
}
