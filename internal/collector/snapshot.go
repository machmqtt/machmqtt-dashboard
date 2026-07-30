package collector

import (
	"slices"
	"strings"
	"time"
)

// Snapshot holds a point-in-time view of one NATS environment.
type Snapshot struct {
	Timestamp time.Time `json:"timestamp"`

	// Per-server data (keyed by server ID).
	Varz     map[string]*Varz         `json:"varz,omitempty"`
	Routez   map[string]*Routez       `json:"routez,omitempty"`
	Gatewayz map[string]*Gatewayz     `json:"gatewayz,omitempty"`
	Leafz    map[string]*Leafz        `json:"leafz,omitempty"`
	Health   map[string]*HealthStatus `json:"health,omitempty"`

	// Slow-polled (updated less frequently).
	Connz    map[string]*Connz     `json:"connz,omitempty"`
	Subsz    map[string]*SubszResp `json:"subsz,omitempty"`
	JSInfo   map[string]*JSInfo    `json:"jsinfo,omitempty"`
	Accountz map[string]*Accountz  `json:"accountz,omitempty"`

	// Computed rates (msgs/sec, bytes/sec per server).
	Rates map[string]*ServerRates `json:"rates,omitempty"`

	// ServerURLs maps server ID → config URL hostname (for resolving 127.0.0.1).
	ServerURLs map[string]string `json:"-"`
}

type ServerRates struct {
	InMsgsRate   float64 `json:"in_msgs_rate"`
	OutMsgsRate  float64 `json:"out_msgs_rate"`
	InBytesRate  float64 `json:"in_bytes_rate"`
	OutBytesRate float64 `json:"out_bytes_rate"`
}

// Overview is a summary for the overview page.
type Overview struct {
	ServerCount     int             `json:"server_count"`
	HealthyCount    int             `json:"healthy_count"`
	ConnectionCount int             `json:"connection_count"`
	InMsgsRate      float64         `json:"in_msgs_rate"`
	OutMsgsRate     float64         `json:"out_msgs_rate"`
	InBytesRate     float64         `json:"in_bytes_rate"`
	OutBytesRate    float64         `json:"out_bytes_rate"`
	Subscriptions   uint32          `json:"subscriptions"`
	JSStreams       int             `json:"js_streams"`
	JSConsumers     int             `json:"js_consumers"`
	JSMessages      uint64          `json:"js_messages"`
	JSBytes         uint64          `json:"js_bytes"`
	Servers         []ServerSummary `json:"servers"`
}

type ServerSummary struct {
	ID          string  `json:"id"`
	Name        string  `json:"name"`
	Version     string  `json:"version"`
	Connections int     `json:"connections"`
	CPU         float64 `json:"cpu"`
	Mem         int64   `json:"mem"`
	InMsgsRate  float64 `json:"in_msgs_rate"`
	OutMsgsRate float64 `json:"out_msgs_rate"`
	Healthy     bool    `json:"healthy"`
	Uptime      string  `json:"uptime"`
}

func computeRates(prev, cur *Snapshot) map[string]*ServerRates {
	if prev == nil {
		return nil
	}
	rates := make(map[string]*ServerRates)
	for id, cv := range cur.Varz {
		pv, ok := prev.Varz[id]
		if !ok {
			continue
		}
		dt := cv.Now.Sub(pv.Now).Seconds()
		if dt <= 0 {
			continue
		}
		// These are cumulative counters that reset when a server restarts. An
		// unclamped delta would then be a large negative rate, which is charted,
		// summed into the overview and persisted into the metrics history.
		rates[id] = &ServerRates{
			InMsgsRate:   nonNegRate(cv.InMsgs, pv.InMsgs, dt),
			OutMsgsRate:  nonNegRate(cv.OutMsgs, pv.OutMsgs, dt),
			InBytesRate:  nonNegRate(cv.InBytes, pv.InBytes, dt),
			OutBytesRate: nonNegRate(cv.OutBytes, pv.OutBytes, dt),
		}
	}
	return rates
}

func buildOverview(snap *Snapshot) *Overview {
	o := &Overview{}
	for id, v := range snap.Varz {
		o.ServerCount++
		o.ConnectionCount += v.Connections
		o.Subscriptions += v.Subscriptions

		ss := ServerSummary{
			ID:          id,
			Name:        v.ServerName,
			Version:     v.Version,
			Connections: v.Connections,
			CPU:         v.CPU,
			Mem:         v.Mem,
			Healthy:     true,
			Uptime:      v.Uptime,
		}

		if h, ok := snap.Health[id]; ok {
			ss.Healthy = h.Status == "ok"
		}
		if ss.Healthy {
			o.HealthyCount++
		}

		if r, ok := snap.Rates[id]; ok {
			ss.InMsgsRate = r.InMsgsRate
			ss.OutMsgsRate = r.OutMsgsRate
			o.InMsgsRate += r.InMsgsRate
			o.OutMsgsRate += r.OutMsgsRate
			o.InBytesRate += r.InBytesRate
			o.OutBytesRate += r.OutBytesRate
		}

		o.Servers = append(o.Servers, ss)
	}

	for _, js := range snap.JSInfo {
		o.JSStreams += js.Streams
		o.JSConsumers += js.Consumers
		o.JSMessages += js.Messages
		o.JSBytes += js.Bytes
	}

	slices.SortFunc(o.Servers, func(a, b ServerSummary) int {
		return strings.Compare(a.Name, b.Name)
	})

	return o
}
