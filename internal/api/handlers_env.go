package api

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/noodlebit/machmqtt-dashboard/internal/collector"
	"github.com/noodlebit/machmqtt-dashboard/internal/store"
)

func (s *Server) handleVersion(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, map[string]string{"version": s.version})
}

func (s *Server) handleEnvironments(w http.ResponseWriter, r *http.Request) {
	clusters, err := s.store.ListClusters()
	if err != nil {
		http.Error(w, `{"error":"failed to list clusters"}`, http.StatusInternalServerError)
		return
	}
	// Lightweight per-cluster health for the sidebar badge. The full diagnostic
	// detail lives behind the admin-only /api/admin/health; these three fields are
	// safe for any authenticated user and reuse the call the UI already polls.
	type clusterInfo struct {
		ID                 string  `json:"id"`
		Name               string  `json:"name"`
		Degraded           bool    `json:"degraded"`
		CollectionMode     string  `json:"collection_mode"`
		LastPollAgeSeconds float64 `json:"last_poll_age_seconds"`
	}
	list := make([]clusterInfo, len(clusters))
	for i, c := range clusters {
		ci := clusterInfo{ID: c.ID, Name: c.Name}
		if h := s.manager.ClusterHealth(c.ID); h != nil {
			ci.Degraded = h.Degraded()
			ci.CollectionMode = h.CollectionMode
			ci.LastPollAgeSeconds = h.LastPollAgeSeconds
		}
		list[i] = ci
	}
	writeJSON(w, map[string]any{"environments": list})
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
	filterSubject := q.Get("filter_subject")

	snap := s.manager.Snapshot(env)
	if snap == nil {
		http.Error(w, `{"error":"environment not found"}`, http.StatusNotFound)
		return
	}

	// If filtering by subject, build a CID set from the subs cache (15s TTL).
	var subCIDs map[uint64]bool
	subsAvailable := true
	if filterSubject != "" {
		rows := s.getSubsRows(r.Context(), env)
		// No rows means either the subscription source is unavailable or the
		// cluster genuinely has no subscriptions. Either way the subject filter
		// matches nothing — surface subsAvailable=false so the client can tell
		// this apart from "there are simply no connections".
		subsAvailable = len(rows) > 0
		subCIDs = make(map[uint64]bool, len(rows))
		for _, row := range rows {
			if strings.Contains(row.Subject, filterSubject) {
				subCIDs[row.ConnCid] = true
			}
		}
	}

	var allConns []collector.ConnInfo
	for _, connz := range snap.Connz {
		for _, c := range connz.Conns {
			if acc != "" && c.Account != acc {
				continue
			}
			if subCIDs != nil && !subCIDs[c.Cid] {
				continue
			}
			allConns = append(allConns, c)
		}
	}

	total := len(allConns)
	if offset > total {
		offset = total
	}
	end := offset + limit
	if end > total {
		end = total
	}

	resp := map[string]any{
		"connections": allConns[offset:end],
		"total":       total,
		"limit":       limit,
		"offset":      offset,
	}
	if filterSubject != "" {
		resp["subs_available"] = subsAvailable
	}
	writeJSON(w, resp)
}

func (s *Server) handleConnzDetail(w http.ResponseWriter, r *http.Request) {
	env := r.PathValue("env")
	cidStr := r.PathValue("cid")
	cid, err := strconv.ParseUint(cidStr, 10, 64)
	if err != nil {
		http.Error(w, `{"error":"invalid cid"}`, http.StatusBadRequest)
		return
	}

	snap := s.manager.Snapshot(env)
	if snap == nil {
		http.Error(w, `{"error":"environment not found"}`, http.StatusNotFound)
		return
	}

	var found *collector.ConnInfo
	for _, connz := range snap.Connz {
		for i := range connz.Conns {
			if connz.Conns[i].Cid == cid {
				cp := connz.Conns[i]
				found = &cp
				break
			}
		}
		if found != nil {
			break
		}
	}

	if found == nil {
		http.Error(w, `{"error":"connection not found"}`, http.StatusNotFound)
		return
	}

	// Enrich subs from the cache when the snapshot doesn't carry them.
	if len(found.SubsDetail) == 0 && len(found.Subs) == 0 {
		rows := s.getSubsRows(r.Context(), env)
		for _, row := range rows {
			if row.ConnCid == cid {
				found.SubsDetail = append(found.SubsDetail, collector.SubDetail{
					Subject: row.Subject,
					Queue:   row.Queue,
					Sid:     row.Sid,
					Msgs:    row.Msgs,
					Account: row.Account,
					Cid:     cid,
				})
			}
		}
	}

	writeJSON(w, found)
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

	// Fast path: use snapshot Connz when sys_collection is active and the slow
	// poll has populated per-connection subscription detail via PING.CONNZ.
	if snap := s.manager.Snapshot(env); snap != nil {
		if rows := subsRowsFromConnz(snap); rows != nil {
			s.cacheSubsRows(env, rows)
			return rows
		}
	}

	// Fall back to HTTP (clusters without sys_collection=true).
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

	const maxRows = 50000

	var all []subRow
	for _, url := range servers {
		if len(all) >= maxRows {
			break
		}
		connz, err := fetcher.FetchConnzSubsDetail(ctx, url, 1024)
		if err != nil {
			connz, err = fetcher.FetchConnzWithSubs(ctx, url, 1024)
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
	s.cacheSubsRows(env, all)
	return all
}

// subsRowsFromConnz builds the subscription row table from snapshot Connz entries
// that carry per-connection subscription detail (populated by PING.CONNZ with
// subscriptions_detail=true on slow polls when sys_collection=true). Returns nil
// when no Connz entry has subscription data, signalling a fall-through to HTTP.
func subsRowsFromConnz(snap *collector.Snapshot) []subRow {
	const maxRows = 50000
	var all []subRow
	for srvID, connz := range snap.Connz {
		srvName := srvID
		if v, ok := snap.Varz[srvID]; ok && v.ServerName != "" {
			srvName = v.ServerName
		}
		for _, c := range connz.Conns {
			if len(c.SubsDetail) == 0 && len(c.Subs) == 0 {
				continue
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
						ServerID: srvID, ServerName: srvName,
					})
					if len(all) >= maxRows {
						break
					}
				}
			} else {
				for i, sub := range c.Subs {
					all = append(all, subRow{
						Subject: sub, Sid: strconv.Itoa(i + 1),
						ConnCid: c.Cid, ConnName: c.Name,
						ConnIP: c.IP, Account: acct,
						ServerID: srvID, ServerName: srvName,
					})
					if len(all) >= maxRows {
						break
					}
				}
			}
		}
	}
	if len(all) == 0 {
		return nil
	}
	sort.Slice(all, func(i, j int) bool { return all[i].Subject < all[j].Subject })
	return all
}

func (s *Server) cacheSubsRows(env string, rows []subRow) {
	subsDetailCacheMu.Lock()
	subsDetailCacheData[env] = &struct {
		rows      []subRow
		fetchedAt time.Time
	}{rows: rows, fetchedAt: time.Now()}
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

	all := s.getSubsRows(r.Context(), env)
	if all == nil {
		http.Error(w, `{"error":"environment not found"}`, http.StatusNotFound)
		return
	}

	var filtered []subRow
	for _, row := range all {
		if hideSystem && isSystemSubject(row.Subject) {
			continue
		}
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

	snap := s.manager.Snapshot(env)
	if snap == nil {
		http.Error(w, `{"error":"environment not found"}`, http.StatusNotFound)
		return
	}

	// ClientCnt from snapshot connz.
	var clientCnt int
	for _, connz := range snap.Connz {
		for _, c := range connz.Conns {
			if c.Account == acc {
				clientCnt++
			}
		}
	}

	// LeafCnt from snapshot leafz.
	var leafCnt int
	for _, lz := range snap.Leafz {
		for _, l := range lz.Leafs {
			if l.Account == acc {
				leafCnt++
			}
		}
	}

	// SubCnt from the subs cache (15s TTL).
	var subCnt uint32
	for _, row := range s.getSubsRows(r.Context(), env) {
		if row.Account == acc {
			subCnt++
		}
	}

	// IsSystem: check whether this account is the system account on any server.
	var isSystem bool
	for _, az := range snap.Accountz {
		if az.SystemAccount == acc {
			isSystem = true
			break
		}
	}

	// Account existence: present in any server's account list, or has active connections.
	var knownAccount bool
	for _, az := range snap.Accountz {
		for _, name := range az.Accounts {
			if name == acc {
				knownAccount = true
				break
			}
		}
		if knownAccount {
			break
		}
	}
	if !knownAccount && clientCnt == 0 && leafCnt == 0 && subCnt == 0 && !isSystem {
		http.Error(w, `{"error":"account not found"}`, http.StatusNotFound)
		return
	}

	writeJSON(w, &collector.AccountInfo{
		AccountName: acc,
		IsSystem:    isSystem,
		LeafCnt:     leafCnt,
		ClientCnt:   clientCnt,
		SubCnt:      subCnt,
	})
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
	if err := json.NewEncoder(w).Encode(v); err != nil {
		// The status line is already committed, so we can't change it — but a
		// silent encode failure is otherwise invisible. Log it once here, the
		// single chokepoint every JSON handler funnels through.
		slog.Warn("writeJSON encode failed", "err", err)
	}
}

// writeError writes a JSON error body with the given status. It marshals the
// message so error text containing quotes/newlines can't corrupt the response
// (unlike hand-built `{"error":"..."}` string concatenation).
func writeError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(map[string]string{"error": msg}); err != nil {
		slog.Warn("writeError encode failed", "err", err)
	}
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
