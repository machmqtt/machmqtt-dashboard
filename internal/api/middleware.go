package api

import "net/http"

// securityHeaders adds standard browser security headers to all responses.
func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "strict-origin-when-cross-origin")
		// connect-src stays at 'self': it covers the same-origin ws://wss:// upgrade
		// the UI performs (it builds the URL from location.host), whereas bare ws:
		// wss: would authorize a WebSocket to any host on the network.
		w.Header().Set("Content-Security-Policy", "default-src 'self'; style-src 'self' 'unsafe-inline'; script-src 'self'; img-src 'self' data:; connect-src 'self'; font-src 'self'")
		next.ServeHTTP(w, r)
	})
}

// limitBody restricts the size of request bodies to prevent resource exhaustion.
func limitBody(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Body != nil {
			r.Body = http.MaxBytesReader(w, r.Body, 1<<20) // 1 MB
		}
		next.ServeHTTP(w, r)
	})
}
