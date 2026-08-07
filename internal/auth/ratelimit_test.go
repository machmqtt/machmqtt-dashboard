package auth

import (
	"net"
	"net/http/httptest"
	"testing"
)

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
