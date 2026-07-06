package collector

import "net/url"

// redactURLCreds strips any userinfo (user:pass@) from a URL so credentials
// embedded directly in a connection string don't leak into logs. URLs without
// userinfo (or that fail to parse) are returned unchanged.
func redactURLCreds(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.User == nil {
		return raw
	}
	u.User = nil
	return u.String()
}

// redactURLCredsAll redacts userinfo from each URL in a slice, returning a new
// slice safe to log. Used for NATS connection URLs, which may embed credentials.
func redactURLCredsAll(urls []string) []string {
	out := make([]string, len(urls))
	for i, u := range urls {
		out[i] = redactURLCreds(u)
	}
	return out
}
