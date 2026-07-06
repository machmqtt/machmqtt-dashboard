import { useState, useEffect } from 'react'
import { fetchWithTimeout } from '../utils/fetchWithTimeout'
import { useStore } from '../store/store'
import { TableSkeleton, CardSkeleton, NoClusterEmptyState } from '../components/Skeleton'
import { ChevronDown, ChevronRight, Server as ServerIcon, Network, Crown } from 'lucide-react'
import { formatBytes as fmtBytes } from '../utils/format'

interface ConsumerInfo {
  stream_name: string
  name: string
  config: { durable_name?: string; filter_subject?: string; deliver_policy: string; ack_policy: string }
  delivered: { consumer_seq: number; stream_seq: number }
  ack_floor: { consumer_seq: number; stream_seq: number }
  num_ack_pending: number
  num_redelivered: number
  num_waiting: number
  num_pending: number
}

// One RAFT peer in a stream or meta-cluster replica set.
interface PeerInfo {
  name: string
  current: boolean
  offline?: boolean
  lag?: number
}
interface ClusterInfo {
  name?: string
  leader?: string
  replicas?: PeerInfo[]
}

interface StreamDetail {
  name: string
  config: {
    subjects?: string[]
    retention: string
    storage: string
    num_replicas: number
    max_msgs: number
    max_bytes: number
    max_age: number // nanoseconds; 0 = unlimited
    discard: string
  }
  // consumer_count is NATS's jsz field name (not "consumers").
  state: { messages: number; bytes: number; consumer_count: number; first_seq: number; last_seq: number }
  cluster?: ClusterInfo // present only for replicated (R>1) streams
  consumer_detail?: ConsumerInfo[]
}

interface AccountDetail {
  name: string
  memory: number
  storage: number
  stream_detail?: StreamDetail[]
}

// MetaClusterInfo is the JetStream meta-group (RAFT) — present only on clustered
// deployments. `leader` names the meta leader; `replicas` are the other peers.
interface MetaClusterInfo {
  name?: string
  leader?: string
  cluster_size: number
  replicas?: PeerInfo[]
}

interface JSData {
  streams: number
  consumers: number
  messages: number
  bytes: number
  memory: number
  storage: number
  reserved_memory?: number
  reserved_storage?: number
  api?: { total: number; errors: number }
  meta_cluster?: MetaClusterInfo
  config?: { domain?: string; max_memory?: number; max_storage?: number }
  account_details?: AccountDetail[]
}

// A stream paired with the account it belongs to, grouped under its server.
interface StreamRow {
  account: string
  stream: StreamDetail
}
interface ServerGroup {
  serverId: string
  name: string
  domain?: string
  rows: StreamRow[]
  msgs: number
  bytes: number
}

// One stream as reported by one server. A replicated stream produces several of
// these (one per replica-holding server); dedupeLogicalStreams collapses them.
interface FlatStream {
  serverId: string
  account: string
  domain?: string
  stream: StreamDetail
}

export function JetStreamPage() {
  const activeEnv = useStore((s) => s.activeEnv)
  const environments = useStore((s) => s.environments)
  const overview = useStore((s) => s.overview)
  const addToast = useStore((s) => s.addToast)
  const [data, setData] = useState<Record<string, JSData> | null>(null)
  const [loading, setLoading] = useState(true)
  const [expandedStream, setExpandedStream] = useState<string | null>(null)
  const [collapsedServers, setCollapsedServers] = useState<Set<string>>(new Set())
  const [filterAccount, setFilterAccount] = useState('')

  useEffect(() => {
    if (!activeEnv) return
    let cancelled = false
    const run = async () => {
      setLoading(true)
      try {
        const r = await fetchWithTimeout(`/api/environments/${activeEnv}/jsz`)
        if (!cancelled && r.ok) setData(await r.json())
      } catch {
        if (!cancelled) addToast('Failed to load JetStream data', 'error')
      }
      if (!cancelled) setLoading(false)
    }
    run()
    // Guard against a late response for a previous env clobbering the current one.
    return () => { cancelled = true }
  }, [activeEnv, addToast])

  const resolveServerName = (id: string): string =>
    overview?.servers?.find((s) => s.id === id)?.name || id.slice(0, 12)

  // Flatten every (server, account, stream) the cluster reports, and roll up the
  // genuinely per-server stats. Memory/file store and API load ARE per-server, so
  // summing them reflects real cluster-wide resource use (replication included).
  const accounts = new Set<string>()
  const flat: FlatStream[] = []
  let memUsed = 0, storeUsed = 0, memMax = 0, storeMax = 0, reservedMem = 0, reservedStore = 0
  let apiTotal = 0, apiErrors = 0
  // Aggregate-only fallback: a poll can land before per-stream detail does (e.g. a
  // $SYS fast poll). Sum the top-level per-server counts so the cards aren't blank
  // until the deduped logical counts are available.
  let aggStreams = 0, aggConsumers = 0, aggMsgs = 0, aggBytes = 0
  // The meta-group is cluster-wide; every server reports its own view. Keep the
  // richest one (most peers / a known leader) so the panel is fully populated.
  let meta: MetaClusterInfo | undefined
  if (data) {
    for (const [serverId, js] of Object.entries(data)) {
      memUsed += js.memory || 0; storeUsed += js.storage || 0
      reservedMem += js.reserved_memory || 0; reservedStore += js.reserved_storage || 0
      if ((js.config?.max_memory ?? 0) > 0) memMax += js.config!.max_memory!
      if ((js.config?.max_storage ?? 0) > 0) storeMax += js.config!.max_storage!
      apiTotal += js.api?.total || 0; apiErrors += js.api?.errors || 0
      aggStreams += js.streams; aggConsumers += js.consumers
      aggMsgs += js.messages; aggBytes += js.bytes
      if (js.meta_cluster && (js.meta_cluster.replicas?.length || 0) >= (meta?.replicas?.length || 0)) {
        meta = js.meta_cluster
      }
      for (const acc of js.account_details || []) {
        accounts.add(acc.name)
        for (const s of acc.stream_detail || []) {
          flat.push({ serverId, account: acc.name, domain: js.config?.domain, stream: s })
        }
      }
    }
  }

  // Collapse replica groups so a replicated (R>1) stream is counted ONCE — valued
  // from its leader (authoritative; followers can lag) and kept under that leader.
  // A non-replicated stream of the same name on another server is genuinely
  // distinct (e.g. MachMQTT's per-broker streams) and is NOT merged. See
  // streamLogicalKey for the rule.
  const logical = dedupeLogicalStreams(flat, resolveServerName)

  // Top cards show LOGICAL counts (each stream once) when detail is available,
  // else the per-server aggregate sums. Memory/file-store cards stay per-server
  // sums (physical resource use, where replication overhead is real).
  const hasDetail = flat.length > 0
  const totalStreams = hasDetail ? logical.length : aggStreams
  const totalConsumers = hasDetail ? logical.reduce((n, f) => n + f.stream.state.consumer_count, 0) : aggConsumers
  const totalMsgs = hasDetail ? logical.reduce((n, f) => n + f.stream.state.messages, 0) : aggMsgs
  const totalBytes = hasDetail ? logical.reduce((n, f) => n + f.stream.state.bytes, 0) : aggBytes

  // Group the deduped streams under their home server (the leader, for replicated
  // streams), honoring the account filter.
  const groups: ServerGroup[] = []
  {
    const byServer = new Map<string, FlatStream[]>()
    for (const f of logical) {
      if (filterAccount && f.account !== filterAccount) continue
      const arr = byServer.get(f.serverId)
      if (arr) arr.push(f)
      else byServer.set(f.serverId, [f])
    }
    for (const [serverId, items] of byServer) {
      items.sort((a, b) => a.stream.name.localeCompare(b.stream.name))
      groups.push({
        serverId,
        name: resolveServerName(serverId),
        domain: data?.[serverId]?.config?.domain,
        rows: items.map((f) => ({ account: f.account, stream: f.stream })),
        msgs: items.reduce((n, f) => n + f.stream.state.messages, 0),
        bytes: items.reduce((n, f) => n + f.stream.state.bytes, 0),
      })
    }
    groups.sort((a, b) => a.name.localeCompare(b.name))
  }

  const toggleServer = (id: string) =>
    setCollapsedServers((prev) => {
      const next = new Set(prev)
      if (next.has(id)) next.delete(id)
      else next.add(id)
      return next
    })

  if (environments.length === 0 || !activeEnv) {
    return (
      <NoClusterEmptyState
        title="JetStream"
        description="Add a NATS cluster to monitor JetStream streams, consumers, and storage."
      />
    )
  }

  return (
    <div>
      <h1 className="text-2xl font-semibold mb-4">JetStream</h1>

      {loading ? (
        <>
          <div className="grid grid-cols-2 lg:grid-cols-4 gap-4 mb-6">
            {Array.from({ length: 4 }).map((_, i) => <CardSkeleton key={i} />)}
          </div>
          <TableSkeleton rows={3} cols={5} />
        </>
      ) : (
        <>
          <div className="grid grid-cols-2 lg:grid-cols-4 gap-4 mb-4">
            <SC label="Streams" value={totalStreams.toString()} title="Logical streams — replicated streams counted once, not per replica" />
            <SC label="Consumers" value={totalConsumers.toString()} title="Consumers across logical streams (replicas not double-counted)" />
            <SC label="Messages" value={totalMsgs.toLocaleString()} title="Logical message count — a replicated stream's messages counted once" />
            <SC label="Stored Data" value={fmtBytes(totalBytes)} title="Logical data size; see File Store for physical disk including replication" />
          </div>

          <div className="grid grid-cols-2 lg:grid-cols-4 gap-4 mb-6">
            <SC
              label="Memory Store"
              value={fmtBytes(memUsed)}
              sub={memMax > 0 ? `of ${fmtBytes(memMax)}` : reservedMem > 0 ? `${fmtBytes(reservedMem)} reserved` : undefined}
            />
            <SC
              label="File Store"
              value={fmtBytes(storeUsed)}
              sub={storeMax > 0 ? `of ${fmtBytes(storeMax)}` : reservedStore > 0 ? `${fmtBytes(reservedStore)} reserved` : undefined}
              title="Physical file-store bytes summed across all servers — includes replication overhead"
            />
            <SC label="API Requests" value={apiTotal.toLocaleString()} />
            <SC label="API Errors" value={apiErrors.toLocaleString()} danger={apiErrors > 0} />
          </div>

          {meta && (
            <div className="bg-white dark:bg-gray-800 rounded-lg shadow p-4 mb-6">
              <div className="flex items-center gap-2 mb-3">
                <Network className="w-4 h-4 text-brand-blue shrink-0" />
                <h2 className="font-semibold">Meta Cluster{meta.name ? `: ${meta.name}` : ''}</h2>
                <span className="text-xs text-gray-400">{meta.cluster_size} node{meta.cluster_size === 1 ? '' : 's'}</span>
              </div>
              <div className="flex flex-wrap gap-2">
                {meta.leader && <PeerBadge p={{ name: meta.leader, current: true }} leader />}
                {(meta.replicas || []).filter((p) => p.name !== meta!.leader).map((p) => <PeerBadge key={p.name} p={p} />)}
              </div>
            </div>
          )}

          {accounts.size > 1 && (
            <div className="mb-4">
              <select
                value={filterAccount}
                onChange={(e) => setFilterAccount(e.target.value)}
                className="border dark:border-gray-600 dark:bg-gray-800 rounded px-3 py-1.5 text-sm"
              >
                <option value="">All accounts</option>
                {[...accounts].sort().map((a) => <option key={a} value={a}>{a}</option>)}
              </select>
            </div>
          )}

          {groups.length === 0 ? (
            <div className="text-gray-500 dark:text-gray-400 bg-white dark:bg-gray-800 rounded-lg shadow p-8 text-center">
              No JetStream streams found.
            </div>
          ) : (
            <div className="space-y-3">
              {groups.map((g) => {
                const collapsed = collapsedServers.has(g.serverId)
                return (
                  <div key={g.serverId} className="bg-white dark:bg-gray-800 rounded-lg shadow">
                    <button
                      onClick={() => toggleServer(g.serverId)}
                      className="w-full flex items-center justify-between px-4 py-3 text-left hover:bg-gray-50 dark:hover:bg-gray-700/50"
                    >
                      <div className="flex items-center gap-3 min-w-0">
                        {collapsed ? <ChevronRight className="w-4 h-4 shrink-0" /> : <ChevronDown className="w-4 h-4 shrink-0" />}
                        <ServerIcon className="w-4 h-4 text-brand-blue shrink-0" />
                        <span className="font-semibold truncate" title={g.name}>{g.name}</span>
                        {g.domain && (
                          <span className="text-xs text-gray-500 bg-gray-100 dark:bg-gray-700 rounded px-2 py-0.5 shrink-0">domain: {g.domain}</span>
                        )}
                        <span className="text-xs text-gray-400 shrink-0">{g.rows.length} streams</span>
                      </div>
                      <div className="flex gap-6 text-sm text-gray-500 dark:text-gray-400 shrink-0">
                        <span>{g.msgs.toLocaleString()} msgs</span>
                        <span>{fmtBytes(g.bytes)}</span>
                      </div>
                    </button>

                    {!collapsed && (
                      <div className="border-t dark:border-gray-700 divide-y divide-gray-100 dark:divide-gray-700">
                        {g.rows.map(({ account, stream }) => {
                          const key = `${g.serverId}/${account}/${stream.name}`
                          const isExpanded = expandedStream === key
                          return (
                            <div key={key}>
                              <button
                                onClick={() => setExpandedStream(isExpanded ? null : key)}
                                className="w-full flex items-center justify-between pl-10 pr-4 py-2.5 text-left hover:bg-gray-50 dark:hover:bg-gray-700/50"
                              >
                                <div className="flex items-center gap-3 min-w-0">
                                  {isExpanded ? <ChevronDown className="w-4 h-4 shrink-0" /> : <ChevronRight className="w-4 h-4 shrink-0" />}
                                  <span className="font-medium truncate" title={stream.name}>{stream.name}</span>
                                  <span className="text-xs text-gray-500 bg-gray-100 dark:bg-gray-700 rounded px-2 py-0.5 shrink-0">{stream.config.storage}</span>
                                  <span className="text-xs text-gray-500 bg-gray-100 dark:bg-gray-700 rounded px-2 py-0.5 shrink-0">Replicas {Math.max(1, stream.config.num_replicas)}</span>
                                </div>
                                <div className="flex gap-6 text-sm text-gray-500 dark:text-gray-400 shrink-0">
                                  <span>{stream.state.messages.toLocaleString()} msgs</span>
                                  <span>{fmtBytes(stream.state.bytes)}</span>
                                  <span>{stream.state.consumer_count.toLocaleString()} consumers</span>
                                </div>
                              </button>

                              {isExpanded && (
                                <div className="bg-gray-50/60 dark:bg-gray-900/30 pl-10 pr-4 py-3">
                                  <div className="grid grid-cols-2 md:grid-cols-4 gap-4 text-sm mb-4">
                                    <div className="md:col-span-2"><span className="text-gray-500 dark:text-gray-400">Subjects:</span> {stream.config.subjects?.join(', ') || '-'}</div>
                                    <div><span className="text-gray-500 dark:text-gray-400">Retention:</span> {stream.config.retention}{stream.config.discard ? ` / discard ${stream.config.discard}` : ''}</div>
                                    <div><span className="text-gray-500 dark:text-gray-400">Storage:</span> {stream.config.storage}</div>
                                    <div><span className="text-gray-500 dark:text-gray-400">Max Msgs:</span> {fmtLimit(stream.config.max_msgs)}</div>
                                    <div><span className="text-gray-500 dark:text-gray-400">Max Bytes:</span> {fmtByteLimit(stream.config.max_bytes)}</div>
                                    <div><span className="text-gray-500 dark:text-gray-400">Max Age:</span> {fmtMaxAge(stream.config.max_age)}</div>
                                    <div><span className="text-gray-500 dark:text-gray-400">Seq Range:</span> {stream.state.first_seq} - {stream.state.last_seq}</div>
                                  </div>

                                  {stream.cluster && (
                                    <div className="mb-4 text-sm">
                                      <span className="text-gray-500 dark:text-gray-400">Replication:</span>
                                      <span className="ml-2 inline-flex flex-wrap gap-2 align-middle">
                                        {stream.cluster.leader && <PeerBadge p={{ name: stream.cluster.leader, current: true }} leader />}
                                        {(stream.cluster.replicas || []).filter((p) => p.name !== stream.cluster!.leader).map((p) => <PeerBadge key={p.name} p={p} />)}
                                      </span>
                                    </div>
                                  )}

                                  {stream.consumer_detail && stream.consumer_detail.length > 0 ? (
                                    <div>
                                      <h3 className="font-medium text-sm mb-2">Consumers ({stream.consumer_detail.length})</h3>
                                      <div className="overflow-x-auto">
                                        <table className="w-full text-sm">
                                          <thead className="bg-gray-100 dark:bg-gray-700 text-left text-gray-500 dark:text-gray-400">
                                            <tr>
                                              <th className="px-3 py-2">Name</th>
                                              <th className="px-3 py-2">Filter</th>
                                              <th className="px-3 py-2">Policy</th>
                                              <th className="px-3 py-2">Delivered</th>
                                              <th className="px-3 py-2">Ack Floor</th>
                                              <th className="px-3 py-2">Ack Pending</th>
                                              <th className="px-3 py-2">Redelivered</th>
                                              <th className="px-3 py-2">Waiting</th>
                                              <th className="px-3 py-2">Pending</th>
                                            </tr>
                                          </thead>
                                          <tbody className="divide-y divide-gray-100 dark:divide-gray-700">
                                            {stream.consumer_detail.map((c) => (
                                              <tr key={c.name} className="hover:bg-gray-50 dark:hover:bg-gray-700/50">
                                                <td className="px-3 py-2 font-medium" title={c.config.durable_name ? `durable: ${c.config.durable_name}` : 'ephemeral'}>{c.name}</td>
                                                <td className="px-3 py-2 font-mono text-xs">{c.config.filter_subject || '*'}</td>
                                                <td className="px-3 py-2 text-xs text-gray-500 dark:text-gray-400">{c.config.deliver_policy}/{c.config.ack_policy}</td>
                                                <td className="px-3 py-2">{c.delivered.consumer_seq.toLocaleString()}</td>
                                                <td className="px-3 py-2">{c.ack_floor.consumer_seq.toLocaleString()}</td>
                                                <td className="px-3 py-2">{c.num_ack_pending.toLocaleString()}</td>
                                                <td className="px-3 py-2">{c.num_redelivered.toLocaleString()}</td>
                                                <td className="px-3 py-2">{(c.num_waiting ?? 0).toLocaleString()}</td>
                                                <td className="px-3 py-2">{c.num_pending.toLocaleString()}</td>
                                              </tr>
                                            ))}
                                          </tbody>
                                        </table>
                                      </div>
                                    </div>
                                  ) : (
                                    <div className="text-sm text-gray-400">No consumers.</div>
                                  )}
                                </div>
                              )}
                            </div>
                          )
                        })}
                      </div>
                    )}
                  </div>
                )
              })}
            </div>
          )}
        </>
      )}
    </div>
  )
}

// streamLogicalKey identifies a logical stream for deduplication. A replicated
// stream (R>1, or one carrying RAFT cluster info) is a single entity across the
// servers holding its replicas, so it keys on (cluster, domain, account, name)
// and collapses them together — disambiguated by RAFT cluster name so two
// separate clusters that share a stream name don't merge. A non-replicated
// stream keeps its server in the key, so same-named per-server streams (e.g.
// MachMQTT's per-broker streams) stay distinct.
function streamLogicalKey(f: FlatStream): string {
  // Guard config: nats-server omits the config block unless config=true is
  // requested, and replica rows can lag a config refresh. The cluster field is
  // the reliable replication signal regardless.
  const replicated = (f.stream.config?.num_replicas || 1) > 1 || !!f.stream.cluster
  if (!replicated) return ['srv', f.serverId, f.domain || '', f.account, f.stream.name].join(' ')
  return ['rep', f.stream.cluster?.name || '', f.domain || '', f.account, f.stream.name].join(' ')
}

// isLeaderReplica is true when this stream's RAFT cluster names the reporting
// server as leader — i.e. this replica's numbers are authoritative.
function isLeaderReplica(f: FlatStream, serverName: (id: string) => string): boolean {
  return !!f.stream.cluster?.leader && f.stream.cluster.leader === serverName(f.serverId)
}

// dedupeLogicalStreams collapses each replica group to one representative,
// preferring the leader replica (never lagging) and otherwise the replica
// reporting the most messages (a lagging follower under-reports). The chosen
// representative carries the leader's serverId, so callers grouping by serverId
// file a replicated stream under its leader.
function dedupeLogicalStreams(flat: FlatStream[], serverName: (id: string) => string): FlatStream[] {
  const best = new Map<string, FlatStream>()
  for (const f of flat) {
    const key = streamLogicalKey(f)
    const cur = best.get(key)
    if (!cur) { best.set(key, f); continue }
    const fLead = isLeaderReplica(f, serverName)
    const curLead = isLeaderReplica(cur, serverName)
    if (fLead !== curLead) {
      if (fLead) best.set(key, f)
    } else if (f.stream.state.messages > cur.stream.state.messages) {
      best.set(key, f)
    }
  }
  return [...best.values()]
}

function SC({ label, value, sub, danger, title }: { label: string; value: string; sub?: string; danger?: boolean; title?: string }) {
  return (
    <div className="bg-white dark:bg-gray-800 rounded-lg shadow p-4" title={title}>
      <div className="text-sm text-gray-500 dark:text-gray-400 mb-1">{label}</div>
      <div className={`text-2xl font-semibold ${danger ? 'text-unhealthy' : ''}`}>{value}</div>
      {sub && <div className="text-xs text-gray-400 mt-0.5">{sub}</div>}
    </div>
  )
}

// A RAFT peer chip: green = caught up, amber = lagging, red = offline. The leader
// is flagged with a crown. Lag (messages behind) is shown when non-zero.
function PeerBadge({ p, leader }: { p: PeerInfo; leader?: boolean }) {
  const tone = p.offline
    ? 'bg-red-100 text-red-800 dark:bg-red-900/40 dark:text-red-300'
    : p.current
      ? 'bg-emerald-100 text-emerald-800 dark:bg-emerald-900/40 dark:text-emerald-300'
      : 'bg-amber-100 text-amber-800 dark:bg-amber-900/40 dark:text-amber-300'
  const status = p.offline ? 'offline' : p.current ? '' : 'lagging'
  return (
    <span className={`inline-flex items-center gap-1 text-xs font-medium rounded px-2 py-0.5 ${tone}`}>
      {leader && <Crown className="w-3 h-3 shrink-0" />}
      <span className="font-mono">{p.name}</span>
      {status && <span>· {status}</span>}
      {p.lag ? <span>· lag {p.lag.toLocaleString()}</span> : null}
    </span>
  )
}

// JetStream limits use 0 or -1 to mean "unlimited"; render that as ∞.
function fmtLimit(n: number): string {
  return n > 0 ? n.toLocaleString() : '∞'
}
function fmtByteLimit(n: number): string {
  return n > 0 ? fmtBytes(n) : '∞'
}
// max_age is a Go time.Duration serialized as nanoseconds; 0 = unlimited.
function fmtMaxAge(ns: number): string {
  if (!ns || ns <= 0) return '∞'
  const s = ns / 1e9
  if (s < 60) return `${s.toFixed(0)}s`
  const m = s / 60
  if (m < 60) return `${m.toFixed(0)}m`
  const h = m / 60
  if (h < 24) return `${h.toFixed(h < 10 ? 1 : 0)}h`
  const d = h / 24
  return `${d.toFixed(d < 10 ? 1 : 0)}d`
}
