package collector

import (
	"context"
	"fmt"
	"net"
	"strings"
	"sync"
)

// MQTTBridgeInstance represents a discovered MQTT bridge instance.
type MQTTBridgeInstance struct {
	// Derived from NATS connz data.
	IP            string  `json:"ip"`
	ServerID      string  `json:"server_id"`
	ServerName    string  `json:"server_name"`
	PoolConns     int     `json:"pool_connections"`
	TotalSubs     int     `json:"total_subs"`
	TotalInMsgs   int64   `json:"total_in_msgs"`
	TotalOutMsgs  int64   `json:"total_out_msgs"`
	TotalInBytes  int64   `json:"total_in_bytes"`
	TotalOutBytes int64   `json:"total_out_bytes"`
	InMsgsRate    float64 `json:"in_msgs_rate"`
	OutMsgsRate   float64 `json:"out_msgs_rate"`
	InBytesRate   float64 `json:"in_bytes_rate"`
	OutBytesRate  float64 `json:"out_bytes_rate"`

	// From config or admin API.
	ConfiguredName string            `json:"configured_name,omitempty"`
	AdminURL       string            `json:"admin_url,omitempty"`
	Status         *MQTTBridgeStatus `json:"status,omitempty"`
	Reachable      bool              `json:"reachable"`
}

// DiscoverMQTTBridges finds MQTT bridge instances from NATS connection data.
// adminToken is the environment-level bearer token used to authenticate the
// admin-API probe against each discovered bridge ("" = send no token).
//
// trustedHosts is the set of hosts (lowercased, no port) the admin token may be
// sent to. A discovered bridge whose IP/host is neither loopback nor trusted is
// still probed, but without the token — this prevents a NATS client that merely
// names itself "machmqtt-bridge" from a rogue address from capturing the shared
// admin secret via the callback probe.
func DiscoverMQTTBridges(ctx context.Context, snap, prev *Snapshot, adminPorts []int, adminToken string, trustedHosts map[string]bool) []MQTTBridgeInstance {
	if snap == nil {
		return nil
	}
	if len(adminPorts) == 0 {
		adminPorts = []int{8080}
	}

	type bridgeKey struct {
		serverID string
		ip       string
	}
	type bridgeAccum struct {
		poolConns int
		totalSubs int
		inMsgs    int64
		outMsgs   int64
		inBytes   int64
		outBytes  int64
	}

	groups := make(map[bridgeKey]*bridgeAccum)
	prevGroups := make(map[bridgeKey]*bridgeAccum)

	serverName := func(id string) string {
		if v, ok := snap.Varz[id]; ok && v.ServerName != "" {
			return v.ServerName
		}
		return id
	}

	// Accumulate current snapshot.
	for srvID, connz := range snap.Connz {
		for _, c := range connz.Conns {
			if !isMQTTBridgeConn(c.Name) {
				continue
			}
			ip := resolveBridgeIP(srvID, c.IP, snap)
			key := bridgeKey{serverID: srvID, ip: ip}
			g := groups[key]
			if g == nil {
				g = &bridgeAccum{}
				groups[key] = g
			}
			g.poolConns++
			g.totalSubs += int(c.NumSubs)
			g.inMsgs += c.InMsgs
			g.outMsgs += c.OutMsgs
			g.inBytes += c.InBytes
			g.outBytes += c.OutBytes
		}
	}

	// Accumulate previous snapshot for rate calculation.
	if prev != nil {
		for srvID, connz := range prev.Connz {
			for _, c := range connz.Conns {
				if !isMQTTBridgeConn(c.Name) {
					continue
				}
				ip := resolveBridgeIP(srvID, c.IP, prev)
				key := bridgeKey{serverID: srvID, ip: ip}
				g := prevGroups[key]
				if g == nil {
					g = &bridgeAccum{}
					prevGroups[key] = g
				}
				g.inMsgs += c.InMsgs
				g.outMsgs += c.OutMsgs
				g.inBytes += c.InBytes
				g.outBytes += c.OutBytes
			}
		}
	}

	var dt float64
	if prev != nil && !prev.Timestamp.IsZero() && !snap.Timestamp.IsZero() {
		dt = snap.Timestamp.Sub(prev.Timestamp).Seconds()
	}

	rate := func(cur, prev int64) float64 {
		if dt <= 0 || cur <= prev {
			return 0
		}
		return float64(cur-prev) / dt
	}

	if len(groups) == 0 {
		return nil
	}

	var mu sync.Mutex
	var wg sync.WaitGroup
	var instances []MQTTBridgeInstance

	// Limit concurrent discovery probes to avoid resource exhaustion.
	sem := make(chan struct{}, 10)

	for key, g := range groups {
		pg := prevGroups[key]
		var prevIn, prevOut, prevInB, prevOutB int64
		if pg != nil {
			prevIn = pg.inMsgs
			prevOut = pg.outMsgs
			prevInB = pg.inBytes
			prevOutB = pg.outBytes
		}

		inst := MQTTBridgeInstance{
			IP:            key.ip,
			ServerID:      key.serverID,
			ServerName:    serverName(key.serverID),
			PoolConns:     g.poolConns,
			TotalSubs:     g.totalSubs,
			TotalInMsgs:   g.inMsgs,
			TotalOutMsgs:  g.outMsgs,
			TotalInBytes:  g.inBytes,
			TotalOutBytes: g.outBytes,
			InMsgsRate:    rate(g.inMsgs, prevIn),
			OutMsgsRate:   rate(g.outMsgs, prevOut),
			InBytesRate:   rate(g.inBytes, prevInB),
			OutBytesRate:  rate(g.outBytes, prevOutB),
		}

		wg.Add(1)
		go func(inst MQTTBridgeInstance) {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
				defer func() { <-sem }()
			case <-ctx.Done():
				return
			}
			// Only send the shared admin token to loopback or explicitly-trusted
			// hosts; withhold it from any other discovered address.
			probeToken := adminToken
			if adminToken != "" && !discoveryHostTrusted(inst.IP, trustedHosts) {
				probeToken = ""
			}
			for _, port := range adminPorts {
				host := inst.IP
				if net.ParseIP(host) != nil && strings.Contains(host, ":") {
					host = "[" + host + "]"
				}
				url := fmt.Sprintf("http://%s:%d", host, port)
				f := NewMQTTBridgeFetcher(url, inst.IP, probeToken)
				status := f.FetchStatus(ctx)
				if status.Error == "" {
					inst.AdminURL = url
					inst.Status = status
					inst.Reachable = true
					break
				}
			}
			mu.Lock()
			instances = append(instances, inst)
			mu.Unlock()
		}(inst)
	}

	wg.Wait()
	return instances
}

// discoveryHostTrusted reports whether the environment admin token may be sent
// to a discovered bridge at host. Loopback is always trusted (a colocated
// bridge); otherwise the host must appear in the configured trusted set.
func discoveryHostTrusted(host string, trusted map[string]bool) bool {
	if ip := net.ParseIP(host); ip != nil && ip.IsLoopback() {
		return true
	}
	h := strings.ToLower(host)
	if h == "localhost" {
		return true
	}
	return trusted[h]
}

func isMQTTBridgeConn(name string) bool {
	return name == "machmqtt-bridge" ||
		strings.HasPrefix(name, "machmqtt-pool-")
}

// resolveBridgeIP maps a bridge connection's loopback IP back to a routable
// host. A bridge running on the same box as its NATS server shows up in connz
// as 127.0.0.1/::1; we prefer the server's config URL hostname, then its varz
// host, so bridges from different servers don't all collapse to "localhost".
// Non-loopback IPs are returned unchanged. Shared by discovery and topology.
func resolveBridgeIP(srvID, ip string, s *Snapshot) string {
	if ip == "127.0.0.1" || ip == "::1" {
		if s.ServerURLs != nil {
			if host, ok := s.ServerURLs[srvID]; ok && host != "" {
				return host
			}
		}
		if v, ok := s.Varz[srvID]; ok && v.Host != "" && v.Host != "0.0.0.0" {
			return v.Host
		}
	}
	return ip
}
