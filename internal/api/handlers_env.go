package api

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/noodlebit/machmqtt-dashboard/internal/collector"
	"github.com/noodlebit/machmqtt-dashboard/internal/store"
	"golang.org/x/sync/errgroup"
)

const (
	connzPageSize       = 1024
	connzMaxPerServer   = 50000
	connzMaxClusterRows = 100000
	connzFanoutLimit    = 4
	connzRequestTimeout = 20 * time.Second
)

type connzPageFetcher func(context.Context, string, int, int) (*collector.Connz, error)

type connzServerResult struct {
	conns     []collector.ConnInfo
	total     int
	serverID  string
	failed    bool
	truncated bool
}

// fetchConnzPages concurrently fetches complete, bounded per-server result sets.
// Results retain configuration order here and are sorted by callers before paging.
func fetchConnzPages(ctx context.Context, servers []string, fetch connzPageFetcher) []connzServerResult {
	results := make([]connzServerResult, len(servers))
	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(connzFanoutLimit)
	for i, serverURL := range servers {
		i, serverURL := i, serverURL
		g.Go(func() error {
			result := &results[i]
			for offset := 0; offset < connzMaxPerServer; {
				page, err := fetch(gctx, serverURL, connzPageSize, offset)
				if err != nil {
					result.failed = true
					return nil
				}
				if result.serverID == "" {
					result.serverID = page.ServerID
					result.total = page.Total
				}
				for j := range page.Conns {
					page.Conns[j].ServerID = page.ServerID
				}
				result.conns = append(result.conns, page.Conns...)
				next := offset + len(page.Conns)
				if len(page.Conns) == 0 || next <= offset || (page.Total > 0 && next >= page.Total) || len(page.Conns) < connzPageSize {
					break
				}
				offset = next
			}
			if result.total == 0 {
				result.total = len(result.conns)
			}
			if result.total > len(result.conns) || len(result.conns) >= connzMaxPerServer {
				result.truncated = true
			}
			return nil
		})
	}
	_ = g.Wait()
	return results
}

func flattenConnz(results []connzServerResult) (conns []collector.ConnInfo, reportedTotal, failedServers int, partial bool) {
	for _, result := range results {
		reportedTotal += result.total
		if result.failed {
			failedServers++
			partial = true
		}
		if result.truncated {
			partial = true
		}
		conns = append(conns, result.conns...)
	}
	sort.SliceStable(conns, func(i, j int) bool {
		if conns[i].ServerID != conns[j].ServerID {
			return conns[i].ServerID < conns[j].ServerID
		}
		if conns[i].Cid != conns[j].Cid {
			return conns[i].Cid < conns[j].Cid
		}
		if conns[i].IP != conns[j].IP {
			return conns[i].IP < conns[j].IP
		}
		return conns[i].Port < conns[j].Port
	})
	if len(conns) > connzMaxClusterRows {
		conns = conns[:connzMaxClusterRows]
		partial = true
	}
	return conns, reportedTotal, failedServers, partial
}

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
		DegradedReason     string  `json:"degraded_reason,omitempty"`
		CollectionMode     string  `json:"collection_mode"`
		LastPollAgeSeconds float64 `json:"last_poll_age_seconds"`
	}
	list := make([]clusterInfo, len(clusters))
	for i, c := range clusters {
		ci := clusterInfo{ID: c.ID, Name: c.Name}
		if h := s.manager.ClusterHealth(c.ID); h != nil {
			ci.Degraded = h.Degraded()
			ci.DegradedReason = h.DegradedReason()
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
		writeJSONError(w, `{"error":"environment not found"}`, http.StatusNotFound)
		return
	}
	writeJSON(w, overview)
}

func (s *Server) handleTopology(w http.ResponseWriter, r *http.Request) {
	env := r.PathValue("env")
	topo := s.manager.Topology(env)
	if topo == nil {
		writeJSONError(w, `{"error":"environment not found"}`, http.StatusNotFound)
		return
	}
	writeJSON(w, topo)
}

func (s *Server) handleGetPositions(w http.ResponseWriter, r *http.Request) {
	env := r.PathValue("env")
	positions, err := s.store.GetTopologyPositions(env)
	if err != nil {
		writeJSONError(w, `{"error":"failed to load positions"}`, http.StatusInternalServerError)
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
		writeJSONError(w, `{"error":"invalid request body"}`, http.StatusBadRequest)
		return
	}
	if err := s.store.SaveTopologyPositions(env, body.Positions); err != nil {
		writeJSONError(w, `{"error":"failed to save positions"}`, http.StatusInternalServerError)
		return
	}
	if body.Camera != nil {
		if err := s.store.SaveTopologyCamera(env, *body.Camera); err != nil {
			writeJSONError(w, `{"error":"failed to save camera"}`, http.StatusInternalServerError)
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
		writeJSONError(w, `{"error":"environment not found"}`, http.StatusNotFound)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), connzRequestTimeout)
	defer cancel()
	results := fetchConnzPages(ctx, servers, func(ctx context.Context, serverURL string, pageLimit, pageOffset int) (*collector.Connz, error) {
		return fetcher.FetchConnz(ctx, serverURL, pageLimit, pageOffset, "", acc, state, filterSubject)
	})
	allConns, reportedTotal, failedServers, partial := flattenConnz(results)

	loadedTotal := len(allConns)
	if offset > loadedTotal {
		offset = loadedTotal
	}
	end := offset + limit
	if end > loadedTotal {
		end = loadedTotal
	}

	writeJSON(w, map[string]any{
		"connections":    allConns[offset:end],
		"total":          reportedTotal,
		"loaded_total":   loadedTotal,
		"limit":          limit,
		"offset":         offset,
		"partial":        partial,
		"failed_servers": failedServers,
	})
}

func (s *Server) handleConnzDetail(w http.ResponseWriter, r *http.Request) {
	env := r.PathValue("env")
	cidStr := r.PathValue("cid")
	cid, err := strconv.ParseUint(cidStr, 10, 64)
	if err != nil {
		writeJSONError(w, `{"error":"invalid cid"}`, http.StatusBadRequest)
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
		rows, _ := s.getSubsRows(r.Context(), env)
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
type subsCacheEntry struct {
	rows      []subRow
	truncated bool
	fetchedAt time.Time
}

const subsCacheTTL = 15 * time.Second
const subsCacheMaxEntries = 50

// getSubsRows returns the subscription rows and whether they are incomplete —
// true when a server reported more connections than the fetch returned, so some
// connections' subscriptions are missing from the table.
func (s *Server) getSubsRows(ctx context.Context, env string) ([]subRow, bool) {
	s.subsCacheMu.Lock()
	cached := s.subsCacheData[env]
	if cached != nil && time.Since(cached.fetchedAt) < subsCacheTTL {
		s.subsCacheHits.Add(1)
		rows := append([]subRow(nil), cached.rows...)
		truncated := cached.truncated
		s.subsCacheMu.Unlock()
		return rows, truncated
	}
	s.subsCacheMu.Unlock()
	s.subsCacheMisses.Add(1)
	value, _, _ := s.subsGroup.Do(env, func() (any, error) {
		rows, truncated := s.loadSubsRows(ctx, env)
		return &subsCacheEntry{rows: rows, truncated: truncated}, nil
	})
	if value == nil {
		return nil, false
	}
	result := value.(*subsCacheEntry)
	return append([]subRow(nil), result.rows...), result.truncated
}

func (s *Server) loadSubsRows(ctx context.Context, env string) ([]subRow, bool) {
	// A request may have populated the cache while this caller waited for the
	// single-flight lock.
	s.subsCacheMu.Lock()
	cached := s.subsCacheData[env]
	if cached != nil && time.Since(cached.fetchedAt) < subsCacheTTL {
		s.subsCacheHits.Add(1)
		rows := append([]subRow(nil), cached.rows...)
		s.subsCacheMu.Unlock()
		return rows, cached.truncated
	}
	s.subsCacheMu.Unlock()

	// Fast path: use snapshot Connz when sys_collection is active and the slow
	// poll has populated per-connection subscription detail via PING.CONNZ.
	if snap := s.manager.Snapshot(env); snap != nil {
		if rows, truncated := subsRowsFromConnz(snap); rows != nil {
			s.cacheSubsRows(env, rows, truncated)
			return rows, truncated
		}
	}

	// Fall back to HTTP (clusters without sys_collection=true).
	fetcher := s.manager.Fetcher(env)
	servers := s.manager.EnvServers(env)
	if fetcher == nil || len(servers) == 0 {
		return nil, false
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

	ctx, cancel := context.WithTimeout(ctx, connzRequestTimeout)
	defer cancel()
	results := fetchConnzPages(ctx, servers, func(ctx context.Context, serverURL string, pageLimit, pageOffset int) (*collector.Connz, error) {
		page, err := fetcher.FetchConnzSubsDetailPage(ctx, serverURL, pageLimit, pageOffset, "")
		if err != nil {
			// Older NATS versions may not support subs=detail.
			return fetcher.FetchConnzWithSubsPage(ctx, serverURL, pageLimit, pageOffset, "")
		}
		return page, nil
	})

	var all []subRow
	truncated := false
	for _, result := range results {
		if result.failed {
			truncated = true
			continue
		}
		truncated = truncated || result.truncated
		if len(all) >= maxRows {
			break
		}
		srvName := serverName(result.serverID)
		for _, c := range result.conns {
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
						ServerID: result.serverID, ServerName: srvName,
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
						ServerID: result.serverID, ServerName: srvName,
					})
					if len(all) >= maxRows {
						break
					}
				}
			}
		}
	}

	if len(all) >= maxRows {
		truncated = true
	}

	sort.Slice(all, func(i, j int) bool { return all[i].Subject < all[j].Subject })
	s.cacheSubsRows(env, all, truncated)
	return all, truncated
}

// subsRowsFromConnz builds the subscription row table from snapshot Connz entries
// that carry per-connection subscription detail (populated by PING.CONNZ with
// subscriptions_detail=true on slow polls when sys_collection=true). Returns nil
// when no Connz entry has subscription data, signalling a fall-through to HTTP.
// The bool reports whether some connections were left out of the rows.
func subsRowsFromConnz(snap *collector.Snapshot) ([]subRow, bool) {
	const maxRows = 50000
	var all []subRow
	truncated := false
	for srvID, connz := range snap.Connz {
		if len(connz.Conns) < connz.Total {
			truncated = true
		}
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
		return nil, false
	}
	if len(all) >= maxRows {
		truncated = true
	}
	sort.Slice(all, func(i, j int) bool { return all[i].Subject < all[j].Subject })
	return all, truncated
}

func (s *Server) cacheSubsRows(env string, rows []subRow, truncated bool) {
	s.subsCacheMu.Lock()
	s.subsCacheData[env] = &subsCacheEntry{
		rows: append([]subRow(nil), rows...), truncated: truncated, fetchedAt: time.Now(),
	}
	if len(s.subsCacheData) > subsCacheMaxEntries {
		var oldestKey string
		var oldestTime time.Time
		for k, v := range s.subsCacheData {
			if oldestKey == "" || v.fetchedAt.Before(oldestTime) {
				oldestKey = k
				oldestTime = v.fetchedAt
			}
		}
		if oldestKey != "" {
			delete(s.subsCacheData, oldestKey)
			s.subsCacheEvictions.Add(1)
		}
	}
	s.subsCacheMu.Unlock()
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

	all, truncated := s.getSubsRows(r.Context(), env)
	if all == nil {
		writeJSONError(w, `{"error":"environment not found"}`, http.StatusNotFound)
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
		// The source connz fetch is capped per server, so rows can be missing for
		// connections beyond the cap.
		"truncated": truncated,
		"limit":     limit,
		"offset":    offset,
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
	subsRows, _ := s.getSubsRows(r.Context(), env)
	for _, row := range subsRows {
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
		writeJSONError(w, `{"error":"environment not found"}`, http.StatusNotFound)
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

// writeRawJSON writes pre-encoded JSON bytes (e.g. a cached response body).
func writeRawJSON(w http.ResponseWriter, body []byte) {
	w.Header().Set("Content-Type", "application/json")
	if _, err := w.Write(body); err != nil {
		slog.Warn("writeRawJSON write failed", "err", err)
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

func writeJSONError(w http.ResponseWriter, payload string, status int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write([]byte(payload + "\n"))
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
