import { useState, useEffect, useCallback } from 'react'
import { fetchWithTimeout } from '../utils/fetchWithTimeout'
import { useStore } from '../store/store'
import { CardSkeleton, TableSkeleton } from '../components/Skeleton'
import { Link } from 'react-router-dom'
import { Radio } from 'lucide-react'
import { TimeSeriesChart, type LineDef } from '../components/TimeSeriesChart'
import { TimeRangeSelector } from '../components/TimeRangeSelector'
import { useMetrics } from '../hooks/useMetrics'
import { formatNumber as fmtNum, formatBytes as fmtBytes, formatRatePerSec as fmtRate, formatRate as fmtRateAxis, formatBytesPerSec as fmtBytesRate } from '../utils/format'

interface NATSConn {
  connected: boolean
  url: string
  server_name: string
  server_version: string
  cluster_name: string
  rtt: string
  in_msgs: number
  out_msgs: number
  in_bytes: number
  out_bytes: number
  reconnects: number
}

interface Stream {
  name: string
  messages: number
  bytes: number
  consumers: number
}

interface KVBucket {
  bucket: string
  values: number
  bytes: number
}

interface PoolSlot {
  index: number
  connected: boolean
  sub_count: number
  pub_count: number
  flush_count: number
}

// MQTTMetrics is typed as any to avoid drift with the broker's expanding
// /metrics output. Field access is guarded with optional chaining at the
// call sites below.
// eslint-disable-next-line @typescript-eslint/no-explicit-any
type MQTTMetrics = any

interface BridgeStatus {
  name: string
  url: string
  ready: boolean
  draining?: boolean
  // MQTT service up, JetStream currently unavailable (bridge readyz state
  // "jetstream-degraded"). Distinct from unreachable: the admin API answered.
  jetstream_degraded?: boolean
  connections: number
  nats_connected: boolean
  connz_available: boolean
  total_connections: number
  nats?: { connection: NATSConn; streams?: Stream[]; kv_buckets?: KVBucket[] }
  pool?: { size: number; slots: PoolSlot[] }
  metrics?: MQTTMetrics
  error?: string
}

interface BridgeInstance {
  ip: string
  server_id: string
  server_name: string
  pool_connections: number
  total_subs: number
  total_in_msgs: number
  total_out_msgs: number
  total_in_bytes: number
  total_out_bytes: number
  in_msgs_rate: number
  out_msgs_rate: number
  in_bytes_rate: number
  out_bytes_rate: number
  configured_name?: string
  admin_url: string
  status?: BridgeStatus
  reachable: boolean
  // RFC3339; absent when the backend has no freshness signal for this entry.
  last_seen?: string
}

// Mirrors probePendingReason in internal/api/handlers_mqtt.go: a configured
// bridge whose first background probe hasn't answered yet. Rendered as a
// neutral "Probing" state, not as an error.
const PROBE_PENDING = 'probing the bridge admin API'

// A push bridge republishes every ~10-15s and the backend expires entries at
// 45s; beyond this the card's counters are shown as stale rather than live.
const STALE_AFTER_MS = 90_000

// Served from the collector's cached bridge list (no upstream fetch), so a
// snappy refresh is cheap and surfaces newly discovered bridges quickly.
const REFRESH_INTERVAL = 3_000

// Hoisted so the array reference is stable across renders — an inline array
// would defeat TimeSeriesChart's memo and re-render recharts on every 3s poll.
const CONN_LINES: LineDef[] = [{ key: 'connections_active', color: '#a855f7', label: 'Active' }]
const MSG_RATE_LINES: LineDef[] = [
  { key: 'in_msgs_rate', color: '#22c55e', label: 'In msgs/s' },
  { key: 'out_msgs_rate', color: '#f97316', label: 'Out msgs/s' },
]

export function MQTTOverviewPage() {
  const activeEnv = useStore((s) => s.activeEnv)
  const environments = useStore((s) => s.environments)
  // bridges === null means "not loaded yet" → show the skeleton. Background
  // refreshes update it in place, so the fleet doesn't strobe a skeleton (and
  // lose the user's scroll position) on every poll.
  const [bridges, setBridges] = useState<BridgeInstance[] | null>(null)
  // Captured when the fleet data lands, so staleness is computed against a
  // stable timestamp instead of calling Date.now() during render (which the
  // React compiler's purity rule rejects); the 3s poll keeps it fresh.
  const [fetchedAt, setFetchedAt] = useState(0)
  const mqttMetrics = useMetrics(activeEnv, 'metrics/mqtt')

  const fetchData = useCallback(async () => {
    if (!activeEnv) return
    try {
      const res = await fetchWithTimeout(`/api/environments/${activeEnv}/mqtt/bridges`)
      if (res.ok) {
        const data = await res.json()
        setBridges(data.bridges || [])
        setFetchedAt(Date.now())
      }
    } catch { /* ignore */ }
  }, [activeEnv])

  // Clear the fleet when switching environments so the previous env's cards don't
  // linger while the new env loads (bridges is local state, unlike the store-backed
  // overview which setActiveEnv resets).
  useEffect(() => {
    setBridges(null) // eslint-disable-line react-hooks/set-state-in-effect -- intentional reset on env change
  }, [activeEnv])

  useEffect(() => {
    fetchData() // eslint-disable-line react-hooks/set-state-in-effect -- fetch-on-mount is intentional
  }, [fetchData])
  useEffect(() => {
    if (!activeEnv) return
    const id = setInterval(fetchData, REFRESH_INTERVAL)
    return () => clearInterval(id)
  }, [activeEnv, fetchData])

  if (environments.length === 0 || !activeEnv) {
    return (
      <div>
        <h1 className="text-2xl font-semibold mb-6">MachMQTT Fleet</h1>
        <div className="bg-white dark:bg-gray-800 rounded-lg shadow p-12 text-center">
          <Radio className="w-12 h-12 text-gray-300 dark:text-gray-600 mx-auto mb-4" />
          <h2 className="text-lg font-semibold text-gray-700 dark:text-gray-200 mb-2">No clusters configured</h2>
          <p className="text-sm text-gray-500 dark:text-gray-400 mb-6 max-w-sm mx-auto">
            Add a NATS cluster to start monitoring MachMQTT bridge instances.
          </p>
          <Link
            to="/admin/clusters"
            className="inline-flex items-center gap-2 bg-brand-blue text-white rounded-lg px-5 py-2 text-sm font-medium hover:opacity-90 transition-opacity"
          >
            <Radio className="w-4 h-4" />
            Go to Cluster Management
          </Link>
        </div>
      </div>
    )
  }

  if (!bridges) {
    return (
      <div>
        <h1 className="text-2xl font-semibold mb-6">MachMQTT Fleet</h1>
        <div className="grid grid-cols-3 gap-4 mb-6">
          {[1,2,3].map(i => <CardSkeleton key={i} />)}
        </div>
        <TableSkeleton rows={3} cols={5} />
      </div>
    )
  }

  if (bridges.length === 0) {
    return (
      <div>
        <h1 className="text-2xl font-semibold mb-6">MachMQTT Fleet</h1>
        <div className="bg-white dark:bg-gray-800 rounded-lg shadow p-10 text-center">
          <Radio className="w-10 h-10 text-gray-300 dark:text-gray-600 mx-auto mb-3" />
          <p className="text-gray-500 dark:text-gray-400 text-sm">
            No MachMQTT bridges discovered yet. Configure MachMQTT discovery or add bridges manually in{' '}
            <Link to="/admin/clusters" className="text-brand-blue hover:underline">Cluster Management</Link>.
          </p>
        </div>
      </div>
    )
  }

  const totalConns = bridges.reduce((s, b) => s + (b.status?.connections ?? 0), 0)
  const healthyCount = bridges.filter(b => b.reachable && b.status?.ready && b.status?.nats_connected).length

  return (
    <div>
      <div className="flex items-center justify-between mb-6">
        <h1 className="text-2xl font-semibold">MachMQTT Fleet</h1>
        <span className="text-xs text-gray-400">Auto-refreshes every {REFRESH_INTERVAL / 1000}s</span>
      </div>

      <div className="grid grid-cols-2 lg:grid-cols-3 xl:grid-cols-6 gap-4 mb-6">
        <SC label="Bridges" value={`${healthyCount}/${bridges.length}`} sub="healthy" />
        <SC label="MQTT Connections" value={totalConns.toLocaleString()} />
        <SC label="Msgs/sec In" value={fmtRate(bridges.reduce((s, b) => s + b.in_msgs_rate, 0))} sub="From NATS connection data" />
        <SC label="Msgs/sec Out" value={fmtRate(bridges.reduce((s, b) => s + b.out_msgs_rate, 0))} sub="From NATS connection data" />
        <SC label="Bytes/sec In" value={fmtBytesRate(bridges.reduce((s, b) => s + b.in_bytes_rate, 0))} />
        <SC label="Bytes/sec Out" value={fmtBytesRate(bridges.reduce((s, b) => s + b.out_bytes_rate, 0))} />
      </div>

      <div className="flex items-center justify-between mb-3">
        <h2 className="text-lg font-semibold">Trends</h2>
        <TimeRangeSelector value={mqttMetrics.range} onChange={mqttMetrics.setRange} />
      </div>
      <div className="grid grid-cols-1 lg:grid-cols-2 gap-4 mb-6">
        <div className="bg-white dark:bg-gray-800 rounded-lg shadow p-4">
          <h3 className="text-sm font-medium text-gray-500 dark:text-gray-400 mb-2">MQTT Connections</h3>
          <TimeSeriesChart
            data={mqttMetrics.data}
            lines={CONN_LINES}
            yFormatter={(v) => v.toFixed(0)}
          />
        </div>
        <div className="bg-white dark:bg-gray-800 rounded-lg shadow p-4">
          <h3 className="text-sm font-medium text-gray-500 dark:text-gray-400 mb-2">Message Rate</h3>
          <TimeSeriesChart
            data={mqttMetrics.data}
            lines={MSG_RATE_LINES}
            yFormatter={fmtRateAxis}
          />
        </div>
      </div>

      <div className="space-y-4">
        {bridges.map((b) => {
          const s = b.status
          const healthy = b.reachable && s?.ready && s?.nats_connected
          // The display name is the cache key server-side, so it is unique per
          // card; b.ip is empty for push-discovered bridges and would collide.
          const displayName = b.configured_name || b.status?.name || `mqtt@${b.ip}`
          const probing = s?.error === PROBE_PENDING
          const staleMs = b.last_seen ? fetchedAt - Date.parse(b.last_seen) : 0
          const stale = staleMs > STALE_AFTER_MS
          return (
          <div key={displayName} className={`bg-white dark:bg-gray-800 rounded-lg shadow${stale ? ' opacity-60' : ''}`}>
            <div className="p-4">
              <div className="flex items-center justify-between gap-3 mb-3">
                <div className="flex items-center gap-3 min-w-0">
                  <span className={`shrink-0 w-2.5 h-2.5 rounded-full ${healthy ? 'bg-healthy' : b.reachable ? 'bg-yellow-400' : 'bg-unhealthy'}`} />
                  <h2 className="font-semibold text-lg shrink-0">{displayName}</h2>
                  {s?.draining && (
                    <span className="shrink-0 text-xs font-medium text-amber-700 bg-amber-100 dark:bg-amber-900/40 dark:text-amber-300 rounded px-2 py-0.5" title="Operator-drained: not accepting new connections">
                      Draining
                    </span>
                  )}
                  {s?.jetstream_degraded && (
                    <span className="shrink-0 text-xs font-medium text-amber-700 bg-amber-100 dark:bg-amber-900/40 dark:text-amber-300 rounded px-2 py-0.5" title="MQTT service is up; JetStream is currently unavailable">
                      JS Degraded
                    </span>
                  )}
                  {/* Reachable but in none of the named states (e.g. still starting,
                      or NATS down): label it rather than leaving only a yellow dot. */}
                  {b.reachable && s && !s.ready && !s.draining && !s.jetstream_degraded && (
                    <span className="shrink-0 text-xs font-medium text-amber-700 bg-amber-100 dark:bg-amber-900/40 dark:text-amber-300 rounded px-2 py-0.5" title="The bridge reports it is not ready to accept MQTT connections">
                      Not Ready
                    </span>
                  )}
                  {probing && (
                    <span className="shrink-0 text-xs font-medium text-gray-600 bg-gray-100 dark:bg-gray-700 dark:text-gray-300 rounded px-2 py-0.5" title="First contact with the bridge admin API is in progress">
                      Probing…
                    </span>
                  )}
                  {stale && (
                    <span className="shrink-0 text-xs text-gray-400" title="No fresh data from this bridge; counters below are its last report">
                      last seen {Math.round(staleMs / 1000)}s ago
                    </span>
                  )}
                  <span className="text-xs text-gray-400 truncate" title={b.server_name}>on {b.server_name}</span>
                  {b.admin_url && <span className="shrink-0 text-xs text-gray-400 font-mono">{b.admin_url}</span>}
                </div>
                <div className="flex items-center gap-4">
                  {s?.connz_available && (
                    <Link to={`/mqtt/${encodeURIComponent(displayName)}/connections`} className="text-brand-blue text-sm hover:underline">
                      Connections ({s.connections})
                    </Link>
                  )}
                  {b.reachable && (
                    <Link to={`/mqtt/${encodeURIComponent(displayName)}/detail`} className="text-brand-blue text-sm hover:underline">
                      Details
                    </Link>
                  )}
                  {!s?.connz_available && (
                    <span className="text-sm text-gray-400">{s?.connections ?? b.pool_connections} connections</span>
                  )}
                </div>
              </div>

              {s?.error && !probing && (
                <div className="bg-red-50 dark:bg-red-900/20 border border-red-200 dark:border-red-800 rounded p-2 mb-3 text-sm text-red-600 dark:text-red-400">
                  {s.error}
                </div>
              )}

              {!b.reachable && !probing && (
                <div className="bg-yellow-50 dark:bg-yellow-900/20 border border-yellow-200 dark:border-yellow-800 rounded p-2 mb-3 text-sm text-yellow-700 dark:text-yellow-400">
                  Bridge admin API not reachable. Showing NATS-side data only.
                </div>
              )}

              {s?.jetstream_degraded && (
                <div className="bg-amber-50 dark:bg-amber-900/20 border border-amber-200 dark:border-amber-800 rounded p-2 mb-3 text-sm text-amber-700 dark:text-amber-400">
                  JetStream unavailable — MQTT is still serving clients; QoS 1/2 persistence is affected until JetStream recovers.
                </div>
              )}

              <div className="grid grid-cols-2 md:grid-cols-4 lg:grid-cols-6 gap-4 text-sm">
                <DI label="MQTT Clients" value={(s?.connections ?? 0).toLocaleString()} />
                <DI label="Pool Conns" value={b.pool_connections.toLocaleString()} />
                <DI label="NATS Subs" value={b.total_subs.toLocaleString()} />
                <DI label="Msgs/sec In" value={fmtRate(b.in_msgs_rate)} />
                <DI label="Msgs/sec Out" value={fmtRate(b.out_msgs_rate)} />
                <DI label="Bytes/sec In" value={fmtBytesRate(b.in_bytes_rate)} />
                <DI label="Bytes/sec Out" value={fmtBytesRate(b.out_bytes_rate)} />
                <DI label="NATS Connected" value={s?.nats_connected ? 'Yes' : b.reachable ? 'No' : '-'} />
                {s?.nats && (
                  <>
                    <DI label="Connected To" value={s.nats.connection.server_name || s.nats.connection.url} />
                    <DI label="Cluster" value={s.nats.connection.cluster_name || '-'} />
                    <DI label="RTT" value={s.nats.connection.rtt || '-'} />
                    <DI label="Reconnects" value={s.nats.connection.reconnects.toLocaleString()} />
                  </>
                )}
                {s?.metrics && (
                  <>
                    <DI label="Total Accepted" value={fmtNum(s.metrics.connections_total)} />
                    <DI label="Rejected" value={fmtNum(s.metrics.connections_rejected)} />
                    <DI label="WS Active" value={fmtNum(s.metrics.ws_connections_active ?? 0)} />
                    <DI label="WS Total" value={fmtNum(s.metrics.ws_connections_total ?? 0)} />
                    <DI label="Auth OK / Fail" value={`${fmtNum(s.metrics.auth_success)} / ${fmtNum(s.metrics.auth_failure)}`} />
                    <DI label="Recv QoS 0/1/2" value={`${fmtNum(s.metrics.msgs_recv_qos0)} / ${fmtNum(s.metrics.msgs_recv_qos1)} / ${fmtNum(s.metrics.msgs_recv_qos2)}`} />
                    <DI label="Sent QoS 0/1/2" value={`${fmtNum(s.metrics.msgs_sent_qos0)} / ${fmtNum(s.metrics.msgs_sent_qos1)} / ${fmtNum(s.metrics.msgs_sent_qos2)}`} />
                    <DI label="Sub / Unsub" value={`${fmtNum(s.metrics.subscribes)} / ${fmtNum(s.metrics.unsubscribes)}`} />
                    <DI label="Keepalive Timeouts" value={fmtNum(s.metrics.keepalive_timeouts)} />
                    <DI label="NATS Disconn / Reconn" value={`${fmtNum(s.metrics.nats_disconnects)} / ${fmtNum(s.metrics.nats_reconnects)}`} />
                  </>
                )}
              </div>

              {s?.nats?.streams && s.nats.streams.length > 0 && (
                <div className="mt-4">
                  <h3 className="font-medium text-sm mb-2">JetStream Streams</h3>
                  <div className="overflow-x-auto">
                    <table className="w-full text-sm">
                      <thead className="bg-gray-50 dark:bg-gray-700 text-left text-gray-500 dark:text-gray-400">
                        <tr>
                          <th className="px-3 py-1.5">Stream</th>
                          <th className="px-3 py-1.5">Messages</th>
                          <th className="px-3 py-1.5">Bytes</th>
                          <th className="px-3 py-1.5">Consumers</th>
                        </tr>
                      </thead>
                      <tbody className="divide-y divide-gray-100 dark:divide-gray-700">
                        {s.nats.streams.map((st) => (
                          <tr key={st.name}>
                            <td className="px-3 py-1.5 font-mono text-xs">{st.name}</td>
                            <td className="px-3 py-1.5">{st.messages.toLocaleString()}</td>
                            <td className="px-3 py-1.5">{fmtBytes(st.bytes)}</td>
                            <td className="px-3 py-1.5">{st.consumers}</td>
                          </tr>
                        ))}
                      </tbody>
                    </table>
                  </div>
                </div>
              )}

              {s?.nats?.kv_buckets && s.nats.kv_buckets.length > 0 && (
                <div className="mt-3">
                  <h3 className="font-medium text-sm mb-2">KV Buckets</h3>
                  <div className="flex gap-4">
                    {s.nats.kv_buckets.map((kv) => (
                      <div key={kv.bucket} className="bg-gray-50 dark:bg-gray-700 rounded px-3 py-2 text-sm">
                        <span className="font-mono text-xs">{kv.bucket}</span>
                        <span className="text-gray-400 mx-2">|</span>
                        {kv.values.toLocaleString()} values
                        <span className="text-gray-400 mx-1">/</span>
                        {fmtBytes(kv.bytes)}
                      </div>
                    ))}
                  </div>
                </div>
              )}

              {s?.pool && s.pool.size > 0 && (() => {
                const slots = s.pool.slots
                const totalSubs = slots.reduce((a: number, sl: PoolSlot) => a + sl.sub_count, 0)
                const totalPubs = slots.reduce((a: number, sl: PoolSlot) => a + sl.pub_count, 0)
                const totalFlush = slots.reduce((a: number, sl: PoolSlot) => a + sl.flush_count, 0)
                const maxSubs = Math.max(...slots.map((sl: PoolSlot) => sl.sub_count), 1)
                const connected = slots.filter((sl: PoolSlot) => sl.connected).length
                return (
                <div className="mt-3">
                  <h3 className="font-medium text-sm mb-2">
                    Connection Pool — {connected}/{s.pool.size} connected
                    <span className="font-normal text-gray-400 ml-3">
                      {fmtNum(totalSubs)} subs | {fmtNum(totalPubs)} pubs | {fmtNum(totalFlush)} flushes
                    </span>
                  </h3>
                  <div className="flex items-end gap-px h-12">
                    {slots.map((slot: PoolSlot) => {
                      const pct = (slot.sub_count / maxSubs) * 100
                      return (
                        <div
                          key={slot.index}
                          className="flex-1 min-w-[3px] rounded-t-sm cursor-default"
                          style={{
                            height: `${Math.max(4, pct)}%`,
                            backgroundColor: slot.connected ? '#27aae1' : '#ef4444',
                            opacity: slot.connected ? 0.5 + (pct / 200) : 1,
                          }}
                          title={`Slot ${slot.index}: ${slot.sub_count.toLocaleString()} subs, ${slot.pub_count.toLocaleString()} pubs, ${slot.flush_count.toLocaleString()} flushes${slot.connected ? '' : ' (disconnected)'}`}
                        />
                      )
                    })}
                  </div>
                  <div className="flex justify-between text-[10px] text-gray-400 mt-1">
                    <span>Slot 0</span>
                    <span>Subscription distribution across {s.pool.size} slots</span>
                    <span>Slot {s.pool.size - 1}</span>
                  </div>
                </div>
                )
              })()}
            </div>
          </div>
          )
        })}
      </div>
    </div>
  )
}

function SC({ label, value, sub }: { label: string; value: string; sub?: string }) {
  return (
    <div className="bg-white dark:bg-gray-800 rounded-lg shadow p-4">
      <div className="text-sm text-gray-500 dark:text-gray-400 mb-1">{label}</div>
      <div className="text-2xl font-semibold">{value}</div>
      {sub && <div className="text-xs text-gray-400">{sub}</div>}
    </div>
  )
}

function DI({ label, value }: { label: string; value: string }) {
  return (
    <div className="min-w-0">
      <div className="text-gray-500 dark:text-gray-400 text-xs">{label}</div>
      <div className="font-medium truncate" title={value}>{value}</div>
    </div>
  )
}
