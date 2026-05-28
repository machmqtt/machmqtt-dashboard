package api

import (
	"context"
	"encoding/json"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/noodlebit/nats-dashboard/internal/collector"
	"github.com/noodlebit/nats-dashboard/internal/store"
)

func (s *Server) handleVersion(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, map[string]string{"version": s.version})
}

func (s *Server) handleEnvironments(w http.ResponseWriter, r *http.Request) {
	envs := s.manager.Environments()
	writeJSON(w, map[string]any{"environments": envs})
}

func (s *Server) handleOverview(w http.ResponseWriter, r *http.Request) {
	env := r.PathValue("env")
	overview := s.manager.Overview(env)
	if overview == nil {
		http.Error(w, `{"error":"environment not found"}`, http.StatusNotFound)
		return
	}
	writeJSON(w, overview)
}

func (s *Server) handleTopology(w http.ResponseWriter, r *http.Request) {
	env := r.PathValue("env")
	topo := s.manager.Topology(env)
	if topo == nil {
		http.Error(w, `{"error":"environment not found"}`, http.StatusNotFound)
		return
	}
	writeJSON(w, topo)
}

func (s *Server) handleGetPositions(w http.ResponseWriter, r *http.Request) {
	env := r.PathValue("env")
	positions, err := s.store.GetTopologyPositions(env)
	if err != nil {
		http.Error(w, `{"error":"failed to load positions"}`, http.StatusInternalServerError)
		return
	}
	if positions == nil {
		positions = []store.NodePosition{}
	}
	resp := map[string]any{"positions": positions}
	cam, err := s.store.GetTopologyCamera(env)
	if err == nil && cam != nil {
		resp["camera"] = cam
	}
	writeJSON(w, resp)
}

func (s *Server) handleSavePositions(w http.ResponseWriter, r *http.Request) {
	env := r.PathValue("env")
	var body struct {
		Positions []store.NodePosition `json:"positions"`
		Camera    *store.CameraState   `json:"camera,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, `{"error":"invalid request body"}`, http.StatusBadRequest)
		return
	}
	if err := s.store.SaveTopologyPositions(env, body.Positions); err != nil {
		http.Error(w, `{"error":"failed to save positions"}`, http.StatusInternalServerError)
		return
	}
	if body.Camera != nil {
		if err := s.store.SaveTopologyCamera(env, *body.Camera); err != nil {
			http.Error(w, `{"error":"failed to save camera"}`, http.StatusInternalServerError)
			return
		}
	}
	writeJSON(w, map[string]string{"status": "ok"})
}

func (s *Server) handleVarz(w http.ResponseWriter, r *http.Request) {
	snap := s.envSnapshot(w, r)
	if snap == nil {
		return
	}
	writeJSON(w, snap.Varz)
}

func (s *Server) handleConnz(w http.ResponseWriter, r *http.Request) {
	env := r.PathValue("env")
	q := r.URL.Query()
	limit := clampInt(q.Get("limit"), 50, 10000)
	offset := clampInt(q.Get("offset"), 0, 100000)
	acc := q.Get("acc")
	state := q.Get("state")
	filterSubject := q.Get("filter_subject")

	fetcher := s.manager.Fetcher(env)
	servers := s.manager.EnvServers(env)
	if fetcher == nil || len(servers) == 0 {
		http.Error(w, `{"error":"environment not found"}`, http.StatusNotFound)
		return
	}

	var allConns []collector.ConnInfo
	for _, url := range servers {
		connz, err := fetcher.FetchConnz(r.Context(), url, 0, 0, "", acc, state, filterSubject)
		if err != nil {
			continue
		}
		allConns = append(allConns, connz.Conns...)
	}

	total := len(allConns)
	if offset > total {
		offset = total
	}
	end := offset + limit
	if end > total {
		end = total
	}

	writeJSON(w, map[string]any{
		"connections": allConns[offset:end],
		"total":       total,
		"limit":       limit,
		"offset":      offset,
	})
}

func (s *Server) handleConnzDetail(w http.ResponseWriter, r *http.Request) {
	env := r.PathValue("env")
	cidStr := r.PathValue("cid")
	cid, err := strconv.ParseUint(cidStr, 10, 64)
	if err != nil {
		http.Error(w, `{"error":"invalid cid"}`, http.StatusBadRequest)
		return
	}

	fetcher := s.manager.Fetcher(env)
	servers := s.manager.EnvServers(env)
	if fetcher == nil || len(servers) == 0 {
		http.Error(w, `{"error":"environment not found"}`, http.StatusNotFound)
		return
	}

	for _, url := range servers {
		connz, err := fetcher.FetchConnzWithSubs(r.Context(), url, 1024)
		if err != nil {
			continue
		}
		for _, c := range connz.Conns {
			if c.Cid == cid {
				writeJSON(w, c)
				return
			}
		}
	}
	http.Error(w, `{"error":"connection not found"}`, http.StatusNotFound)
}

func (s *Server) handleRoutez(w http.ResponseWriter, r *http.Request) {
	snap := s.envSnapshot(w, r)
	if snap == nil {
		return
	}
	writeJSON(w, snap.Routez)
}

func (s *Server) handleGatewayz(w http.ResponseWriter, r *http.Request) {
	snap := s.envSnapshot(w, r)
	if snap == nil {
		return
	}
	writeJSON(w, snap.Gatewayz)
}

func (s *Server) handleLeafz(w http.ResponseWriter, r *http.Request) {
	snap := s.envSnapshot(w, r)
	if snap == nil {
		return
	}
	writeJSON(w, snap.Leafz)
}

func (s *Server) handleSubsz(w http.ResponseWriter, r *http.Request) {
	snap := s.envSnapshot(w, r)
	if snap == nil {
		return
	}
	writeJSON(w, snap.Subsz)
}

// subRow is one subscription in the flat detail table.
type subRow struct {
	Subject    string `json:"subject"`
	Queue      string `json:"queue,omitempty"`
	Sid        string `json:"sid"`
	Msgs       int64  `json:"msgs"`
	ConnCid    uint64 `json:"conn_cid"`
	ConnName   string `json:"conn_name"`
	ConnIP     string `json:"conn_ip"`
	Account    string `json:"account,omitempty"`
	ServerID   string `json:"server_id"`
	ServerName string `json:"server_name"`
}

// subsDetailCache caches the expensive /connz?subs=detail fetch across all servers.
var subsDetailCacheMu sync.Mutex
var subsDetailCacheData = make(map[string]*struct {
	rows      []subRow
	fetchedAt time.Time
})

const subsCacheTTL = 15 * time.Second
const subsCacheMaxEntries = 50

func (s *Server) getSubsRows(ctx context.Context, env string) []subRow {
	subsDetailCacheMu.Lock()
	cached := subsDetailCacheData[env]
	if cached != nil && time.Since(cached.fetchedAt) < subsCacheTTL {
		rows := cached.rows
		subsDetailCacheMu.Unlock()
		return rows
	}
	subsDetailCacheMu.Unlock()

	fetcher := s.manager.Fetcher(env)
	servers := s.manager.EnvServers(env)
	if fetcher == nil || len(servers) == 0 {
		return nil
	}

	snap := s.manager.Snapshot(env)
	serverName := func(id string) string {
		if snap != nil {
			if v, ok := snap.Varz[id]; ok && v.ServerName != "" {
				return v.ServerName
			}
		}
		return id
	}

	const maxRows = 50000 // Hard cap to prevent OOM.

	var all []subRow
	for _, url := range servers {
		if len(all) >= maxRows {
			break
		}
		connz, err := fetcher.FetchConnzSubsDetail(ctx, url, 256)
		if err != nil {
			// Fallback to subs=true (string list) if subs=detail fails.
			connz, err = fetcher.FetchConnzWithSubs(ctx, url, 256)
			if err != nil {
				continue
			}
		}
		srvName := serverName(connz.ServerID)
		for _, c := range connz.Conns {
			if len(all) >= maxRows {
				break
			}
			acct := c.Account
			if len(c.SubsDetail) > 0 {
				for _, sd := range c.SubsDetail {
					a := sd.Account
					if a == "" {
						a = acct
					}
					all = append(all, subRow{
						Subject: sd.Subject, Queue: sd.Queue, Sid: sd.Sid,
						Msgs: sd.Msgs, ConnCid: c.Cid, ConnName: c.Name,
						ConnIP: c.IP, Account: a,
						ServerID: connz.ServerID, ServerName: srvName,
					})
					if len(all) >= maxRows {
						break
					}
				}
			} else if len(c.Subs) > 0 {
				for i, sub := range c.Subs {
					all = append(all, subRow{
						Subject: sub, Sid: strconv.Itoa(i + 1),
						ConnCid: c.Cid, ConnName: c.Name,
						ConnIP: c.IP, Account: acct,
						ServerID: connz.ServerID, ServerName: srvName,
					})
					if len(all) >= maxRows {
						break
					}
				}
			}
		}
	}

	sort.Slice(all, func(i, j int) bool { return all[i].Subject < all[j].Subject })

	subsDetailCacheMu.Lock()
	subsDetailCacheData[env] = &struct {
		rows      []subRow
		fetchedAt time.Time
	}{rows: all, fetchedAt: time.Now()}

	// Evict stale entries and enforce max cache size.
	if len(subsDetailCacheData) > subsCacheMaxEntries {
		var oldestKey string
		var oldestTime time.Time
		for k, v := range subsDetailCacheData {
			if oldestKey == "" || v.fetchedAt.Before(oldestTime) {
				oldestKey = k
				oldestTime = v.fetchedAt
			}
		}
		if oldestKey != "" {
			delete(subsDetailCacheData, oldestKey)
		}
	}
	subsDetailCacheMu.Unlock()

	return all
}

func (s *Server) handleSubsDetail(w http.ResponseWriter, r *http.Request) {
	env := r.PathValue("env")
	q := r.URL.Query()
	limit := clampInt(q.Get("limit"), 100, 10000)
	offset := clampInt(q.Get("offset"), 0, 100000)
	filterSubject := q.Get("subject")
	filterAccount := q.Get("account")
	filterServer := q.Get("server")
	hideSystem := q.Get("hide_system") == "true"

	var all []subRow

	if filterSubject != "" {
		// Targeted fetch: push filter_subject to NATS so it only returns
		// connections with matching subscriptions. Much faster than fetching
		// everything and filtering in-memory.
		all = s.fetchSubsFiltered(r.Context(), env, filterSubject)
	} else {
		// Unfiltered: use cache.
		all = s.getSubsRows(r.Context(), env)
	}

	if all == nil {
		http.Error(w, `{"error":"environment not found"}`, http.StatusNotFound)
		return
	}

	var filtered []subRow
	for _, row := range all {
		if hideSystem && isSystemSubject(row.Subject) {
			continue
		}
		// If we used a targeted NATS fetch, filter_subject was already applied
		// server-side. But NATS matches by subscription interest, not substring,
		// so still apply our substring filter for exact UI behavior.
		if filterSubject != "" && !strings.Contains(row.Subject, filterSubject) {
			continue
		}
		if filterAccount != "" && row.Account != filterAccount {
			continue
		}
		if filterServer != "" && row.ServerName != filterServer && row.ServerID != filterServer {
			continue
		}
		filtered = append(filtered, row)
	}

	total := len(filtered)
	if offset > total {
		offset = total
	}
	end := offset + limit
	if end > total {
		end = total
	}

	writeJSON(w, map[string]any{
		"subscriptions": filtered[offset:end],
		"total":         total,
		"limit":         limit,
		"offset":        offset,
	})
}

// fetchSubsFiltered uses subs=true + filter_subject for a fast, lightweight fetch.
// NATS returns only connections with matching subscriptions, and only the subject
// string list (no per-sub message counts), making this very fast even on large clusters.
func (s *Server) fetchSubsFiltered(ctx context.Context, env, filterSubject string) []subRow {
	fetcher := s.manager.Fetcher(env)
	servers := s.manager.EnvServers(env)
	if fetcher == nil || len(servers) == 0 {
		return nil
	}

	snap := s.manager.Snapshot(env)
	serverName := func(id string) string {
		if snap != nil {
			if v, ok := snap.Varz[id]; ok && v.ServerName != "" {
				return v.ServerName
			}
		}
		return id
	}

	var all []subRow
	for _, url := range servers {
		connz, err := fetcher.FetchConnzWithSubsFiltered(ctx, url, 1024, filterSubject)
		if err != nil {
			continue
		}
		srvName := serverName(connz.ServerID)
		for _, c := range connz.Conns {
			for i, sub := range c.Subs {
				all = append(all, subRow{
					Subject: sub, Sid: strconv.Itoa(i + 1),
					ConnCid: c.Cid, ConnName: c.Name,
					ConnIP: c.IP, Account: c.Account,
					ServerID: connz.ServerID, ServerName: srvName,
				})
			}
		}
	}

	sort.Slice(all, func(i, j int) bool { return all[i].Subject < all[j].Subject })
	return all
}

func (s *Server) handleJSz(w http.ResponseWriter, r *http.Request) {
	snap := s.envSnapshot(w, r)
	if snap == nil {
		return
	}
	writeJSON(w, snap.JSInfo)
}

func (s *Server) handleAccountz(w http.ResponseWriter, r *http.Request) {
	snap := s.envSnapshot(w, r)
	if snap == nil {
		return
	}
	writeJSON(w, snap.Accountz)
}

func (s *Server) handleAccountDetail(w http.ResponseWriter, r *http.Request) {
	env := r.PathValue("env")
	acc := r.PathValue("acc")

	fetcher := s.manager.Fetcher(env)
	servers := s.manager.EnvServers(env)
	if fetcher == nil || len(servers) == 0 {
		http.Error(w, `{"error":"environment not found"}`, http.StatusNotFound)
		return
	}

	// Aggregate account detail across all servers.
	var merged *collector.AccountInfo
	for _, url := range servers {
		detail, err := fetcher.FetchAccountDetail(r.Context(), url, acc)
		if err != nil || detail.Account == nil {
			continue
		}
		if merged == nil {
			copy := *detail.Account
			merged = &copy
		} else {
			merged.ClientCnt += detail.Account.ClientCnt
			merged.LeafCnt += detail.Account.LeafCnt
			merged.SubCnt += detail.Account.SubCnt
		}
	}

	if merged == nil {
		http.Error(w, `{"error":"account not found"}`, http.StatusNotFound)
		return
	}

	// Also count actual connections from connz for accuracy.
	var clientConns int
	var leafConns int
	for _, url := range servers {
		connz, err := fetcher.FetchConnz(r.Context(), url, 0, 0, "", acc, "", "")
		if err != nil {
			continue
		}
		clientConns += len(connz.Conns)
	}

	snap := s.manager.Snapshot(env)
	if snap != nil {
		for _, lz := range snap.Leafz {
			for _, l := range lz.Leafs {
				if l.Account == acc {
					leafConns++
				}
			}
		}
	}

	// Use the live-counted values.
	merged.ClientCnt = clientConns
	merged.LeafCnt = leafConns

	writeJSON(w, merged)
}

func (s *Server) envSnapshot(w http.ResponseWriter, r *http.Request) *collector.Snapshot {
	env := r.PathValue("env")
	snap := s.manager.Snapshot(env)
	if snap == nil {
		http.Error(w, `{"error":"environment not found"}`, http.StatusNotFound)
		return nil
	}
	return snap
}

// clampInt parses a query parameter as an integer, returning defaultVal if
// empty/invalid and capping at maxVal to prevent resource exhaustion.
func clampInt(s string, defaultVal, maxVal int) int {
	if s == "" {
		return defaultVal
	}
	n, err := strconv.Atoi(s)
	if err != nil || n < 0 {
		return defaultVal
	}
	if n == 0 {
		return defaultVal
	}
	if n > maxVal {
		return maxVal
	}
	return n
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}

var nonSystemPrefixes = []string{"$MQTT5"}

func isSystemSubject(subject string) bool {
	if len(subject) == 0 {
		return false
	}
	if subject[0] != '_' && subject[0] != '$' {
		return false
	}
	for _, p := range nonSystemPrefixes {
		if strings.HasPrefix(subject, p) {
			return false
		}
	}
	return true
}
