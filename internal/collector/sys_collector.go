package collector

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	nats "github.com/nats-io/nats.go"
	"github.com/noodlebit/machmqtt-dashboard/internal/config"
)

// statszTTL is how long a server's STATSZ entry survives without a new publish.
// Set to 3× the 10s heartbeat interval.
const statszTTL = 30 * time.Second

// Wire types for $SYS.SERVER.*.STATSZ messages.
// Field names and JSON tags match nats-server events.go ServerStatsMsg / ServerStats.

type sysServerInfo struct {
	ID      string    `json:"id"`
	Name    string    `json:"name"`
	Host    string    `json:"host"`
	Cluster string    `json:"cluster,omitempty"`
	Version string    `json:"ver"`
	Time    time.Time `json:"time"`
}

type sysMsgBytes struct {
	Msgs  int64 `json:"msgs"`
	Bytes int64 `json:"bytes"`
}

type sysDataStats struct {
	sysMsgBytes
}

type sysRouteStat struct{} // only need the count; discard content

type sysServerStats struct {
	Start            time.Time      `json:"start"`
	Mem              int64          `json:"mem"`
	Cores            int            `json:"cores"`
	CPU              float64        `json:"cpu"`
	Connections      int            `json:"connections"`
	TotalConnections uint64         `json:"total_connections"`
	NumSubs          uint32         `json:"subscriptions"` // JSON tag is "subscriptions"
	Sent             sysDataStats   `json:"sent"`
	Received         sysDataStats   `json:"received"`
	SlowConsumers    int64          `json:"slow_consumers"`
	Routes           []json.RawMessage `json:"routes,omitempty"`   // only len needed
	Gateways         []json.RawMessage `json:"gateways,omitempty"` // only len needed
	JetStream        *sysJSVarz     `json:"jetstream,omitempty"`
}

type sysJSVarz struct {
	Config struct {
		MaxMemory int64  `json:"max_memory"`
		MaxStore  int64  `json:"max_storage"`
		Domain    string `json:"domain,omitempty"`
	} `json:"config,omitempty"`
	Stats struct {
		Memory   uint64 `json:"memory"`
		Store    uint64 `json:"storage"`
		Accounts int    `json:"accounts"`
		API      struct {
			Total  uint64 `json:"total"`
			Errors uint64 `json:"errors"`
		} `json:"api"`
	} `json:"stats,omitempty"`
}

type sysStatsMsg struct {
	Server sysServerInfo `json:"server"`
	Stats  sysServerStats `json:"statsz"` // JSON key is "statsz", not "stats"
}

// pingEnvelope wraps a $SYS.REQ.SERVER.PING.* reply.
// Matches nats-server ServerAPIResponse{Server, Data, Error}.
type pingEnvelope struct {
	Server sysServerInfo   `json:"server"`
	Data   json.RawMessage `json:"data,omitempty"`
	Error  *struct {
		Description string `json:"description"`
	} `json:"error,omitempty"`
}

type statszEntry struct {
	server sysServerInfo
	stats  sysServerStats
	when   time.Time
}

// SYSCollector subscribes to $SYS.SERVER.*.STATSZ for continuous server
// stats and uses $SYS.REQ.SERVER.PING.* fan-in for slow-poll detail data.
// It feeds the same Snapshot shape as the HTTP poller.
type SYSCollector struct {
	mu     sync.RWMutex
	nc     *nats.Conn
	statsz map[string]*statszEntry // keyed by server ID
}

func newSYSCollector() *SYSCollector {
	return &SYSCollector{statsz: make(map[string]*statszEntry)}
}

// run connects to NATS, subscribes to $SYS.SERVER.*.STATSZ, and maintains the
// server cache until ctx is cancelled. Intended to be started as a goroutine.
func (sc *SYSCollector) run(ctx context.Context, cfg *config.NATSConnConfig) {
	nc, err := connectNATS(cfg)
	if err != nil {
		return
	}
	defer nc.Close()

	sc.mu.Lock()
	sc.nc = nc
	sc.mu.Unlock()

	defer func() {
		sc.mu.Lock()
		sc.nc = nil
		sc.mu.Unlock()
	}()

	_, err = nc.Subscribe("$SYS.SERVER.*.STATSZ", func(msg *nats.Msg) {
		var m sysStatsMsg
		if json.Unmarshal(msg.Data, &m) != nil || m.Server.ID == "" {
			return
		}
		sc.mu.Lock()
		sc.statsz[m.Server.ID] = &statszEntry{
			server: m.Server,
			stats:  m.Stats,
			when:   time.Now(),
		}
		sc.mu.Unlock()
	})
	if err != nil {
		return
	}

	sweeper := time.NewTicker(statszTTL / 3)
	defer sweeper.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-sweeper.C:
			sc.sweepExpired()
		}
	}
}

func (sc *SYSCollector) sweepExpired() {
	deadline := time.Now().Add(-statszTTL)
	sc.mu.Lock()
	defer sc.mu.Unlock()
	for id, e := range sc.statsz {
		if e.when.Before(deadline) {
			delete(sc.statsz, id)
		}
	}
}

// poll returns a Snapshot from the STATSZ cache (fast path) or PING fan-in
// (slow path or bootstrap when cache is empty). Returns nil when the NATS
// connection is not yet established.
// carry is the most recent snapshot whose slow-polled data is reused on fast polls.
func (sc *SYSCollector) poll(ctx context.Context, carry *Snapshot, slow bool) *Snapshot {
	sc.mu.RLock()
	nc := sc.nc
	cacheLen := len(sc.statsz)
	sc.mu.RUnlock()

	if nc == nil {
		return nil
	}

	snap := &Snapshot{
		Timestamp: time.Now(),
		Varz:      make(map[string]*Varz),
		Routez:    make(map[string]*Routez),
		Gatewayz:  make(map[string]*Gatewayz),
		Leafz:     make(map[string]*Leafz),
		Health:    make(map[string]*HealthStatus),
		Connz:     make(map[string]*Connz),
		Subsz:     make(map[string]*SubszResp),
		JSInfo:    make(map[string]*JSInfo),
		Accountz:  make(map[string]*Accountz),
	}

	// Bootstrap: STATSZ cache is empty but we are connected → PING.VARZ immediately
	// so the first fast poll returns real data instead of an empty snapshot.
	bootstrap := cacheLen == 0 && nc.IsConnected()

	if bootstrap || slow {
		sc.fillFromPing(ctx, nc, snap)
		return snap
	}

	// Fast path: build Varz from the STATSZ cache.
	sc.mu.RLock()
	for id, e := range sc.statsz {
		snap.Varz[id] = statszToVarz(e)
	}
	sc.mu.RUnlock()

	if len(snap.Varz) == 0 {
		return nil
	}

	// Carry forward slow-polled data from the previous snapshot.
	if carry != nil {
		snap.Connz = carry.Connz
		snap.Subsz = carry.Subsz
		snap.JSInfo = carry.JSInfo
		snap.Accountz = carry.Accountz
		snap.ServerURLs = carry.ServerURLs
	}

	return snap
}

// fillFromPing fires all PING fan-in requests in parallel and populates snap.
func (sc *SYSCollector) fillFromPing(ctx context.Context, nc *nats.Conn, snap *Snapshot) {
	type endpoint struct {
		name string
		fill func(id string, data json.RawMessage)
	}

	var mu sync.Mutex
	endpoints := []endpoint{
		{"VARZ", func(id string, data json.RawMessage) {
			var v Varz
			if json.Unmarshal(data, &v) == nil {
				snap.Varz[id] = &v
			}
		}},
		{"CONNZ", func(id string, data json.RawMessage) {
			var v Connz
			if json.Unmarshal(data, &v) == nil {
				snap.Connz[id] = &v
			}
		}},
		{"ROUTEZ", func(id string, data json.RawMessage) {
			var v Routez
			if json.Unmarshal(data, &v) == nil {
				snap.Routez[id] = &v
			}
		}},
		{"GATEWAYZ", func(id string, data json.RawMessage) {
			var v Gatewayz
			if json.Unmarshal(data, &v) == nil {
				snap.Gatewayz[id] = &v
			}
		}},
		{"LEAFZ", func(id string, data json.RawMessage) {
			var v Leafz
			if json.Unmarshal(data, &v) == nil {
				snap.Leafz[id] = &v
			}
		}},
		{"SUBSZ", func(id string, data json.RawMessage) {
			var v SubszResp
			if json.Unmarshal(data, &v) == nil {
				snap.Subsz[id] = &v
			}
		}},
		{"JSZ", func(id string, data json.RawMessage) {
			var v JSInfo
			if json.Unmarshal(data, &v) == nil {
				snap.JSInfo[id] = &v
			}
		}},
		{"ACCOUNTZ", func(id string, data json.RawMessage) {
			var v Accountz
			if json.Unmarshal(data, &v) == nil {
				snap.Accountz[id] = &v
			}
		}},
		{"HEALTHZ", func(id string, data json.RawMessage) {
			var v HealthStatus
			if json.Unmarshal(data, &v) == nil {
				snap.Health[id] = &v
			}
		}},
	}

	var wg sync.WaitGroup
	for _, ep := range endpoints {
		ep := ep
		wg.Add(1)
		go func() {
			defer wg.Done()
			subject := fmt.Sprintf("$SYS.REQ.SERVER.PING.%s", ep.name)
			replies := fanIn(nc, subject, 2*time.Second)
			mu.Lock()
			for _, r := range replies {
				if r.Error != nil || len(r.Data) == 0 {
					continue
				}
				ep.fill(r.Server.ID, r.Data)
			}
			mu.Unlock()
		}()
	}
	wg.Wait()
}

// fanIn publishes a request to subject and collects all replies within timeout.
func fanIn(nc *nats.Conn, subject string, timeout time.Duration) []*pingEnvelope {
	inbox := nc.NewRespInbox()
	sub, err := nc.SubscribeSync(inbox)
	if err != nil {
		return nil
	}
	defer sub.Unsubscribe() //nolint:errcheck

	if err := nc.PublishRequest(subject, inbox, nil); err != nil {
		return nil
	}
	_ = nc.Flush()

	deadline := time.Now().Add(timeout)
	var out []*pingEnvelope
	for {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			break
		}
		msg, err := sub.NextMsg(remaining)
		if err != nil {
			break // timeout or subscription closed
		}
		var e pingEnvelope
		if json.Unmarshal(msg.Data, &e) == nil {
			out = append(out, &e)
		}
	}
	return out
}

// statszToVarz maps a STATSZ cache entry to the Varz shape used by the snapshot.
func statszToVarz(e *statszEntry) *Varz {
	v := &Varz{
		ServerID:         e.server.ID,
		ServerName:       e.server.Name,
		Host:             e.server.Host,
		Version:          e.server.Version,
		Connections:      e.stats.Connections,
		TotalConnections: e.stats.TotalConnections,
		Routes:           len(e.stats.Routes),
		Mem:              e.stats.Mem,
		CPU:              e.stats.CPU,
		Cores:            e.stats.Cores,
		Subscriptions:    e.stats.NumSubs,
		SlowConsumers:    e.stats.SlowConsumers,
		InMsgs:           e.stats.Received.Msgs,
		OutMsgs:          e.stats.Sent.Msgs,
		InBytes:          e.stats.Received.Bytes,
		OutBytes:         e.stats.Sent.Bytes,
		Start:            e.stats.Start,
		Now:              e.server.Time,
		Cluster:          ClusterOptsVarz{Name: e.server.Cluster},
	}
	v.Uptime = formatUptime(e.server.Time.Sub(e.stats.Start))

	if js := e.stats.JetStream; js != nil {
		v.JetStream = JetStreamVarz{
			Config: JetStreamConfig{
				MaxMemory: js.Config.MaxMemory,
				MaxStore:  js.Config.MaxStore,
				Domain:    js.Config.Domain,
			},
			Stats: JetStreamStats{
				Memory:   js.Stats.Memory,
				Store:    js.Stats.Store,
				Accounts: js.Stats.Accounts,
				API: JetStreamAPIStats{
					Total:  js.Stats.API.Total,
					Errors: js.Stats.API.Errors,
				},
			},
		}
	}
	return v
}

func formatUptime(d time.Duration) string {
	d = d.Truncate(time.Second)
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	if d < time.Hour {
		return fmt.Sprintf("%dm%ds", int(d.Minutes()), int(d.Seconds())%60)
	}
	h := int(d.Hours())
	if h < 24 {
		return fmt.Sprintf("%dh%dm%ds", h, int(d.Minutes())%60, int(d.Seconds())%60)
	}
	return fmt.Sprintf("%dd%dh%dm%ds", h/24, h%24, int(d.Minutes())%60, int(d.Seconds())%60)
}
