import { useState, useEffect, useCallback } from 'react'
import { fetchWithTimeout } from '../utils/fetchWithTimeout'
import { useStore } from '../store/store'
import { CardSkeleton, TableSkeleton } from '../components/Skeleton'
import { Link } from 'react-router-dom'
import { TimeSeriesChart } from '../components/TimeSeriesChart'
import { TimeRangeSelector } from '../components/TimeRangeSelector'
import { useMetrics } from '../hooks/useMetrics'

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

interface MQTTMetrics {
  connections_active: number
  connections_total: number
  connections_rejected: number
  ws_connections_active?: number
  ws_connections_total?: number
  auth_success: number
  auth_failure: number
  msgs_recv_qos0: number
  msgs_recv_qos1: number
  msgs_recv_qos2: number
  msgs_sent_qos0: number
  msgs_sent_qos1: number
  msgs_sent_qos2: number
  subscribes: number
  unsubscribes: number
  keepalive_timeouts: number
  pool_publishes?: number
  pool_subscribes?: number
  nats_disconnects: number
  nats_reconnects: number
}

interface BridgeStatus {
  name: string
  url: string
  ready: boolean
  draining?: boolean
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
}

const REFRESH_INTERVAL = 10_000

export function MQTTOverviewPage() {
  const activeEnv = useStore((s) => s.activeEnv)
  const [bridges, setBridges] = useState<BridgeInstance[] | null>(null)
  const [loading, setLoading] = useState(true)
  const mqttMetrics = useMetrics(activeEnv, 'metrics/mqtt')

  const fetchData = useCallback(async () => {
    if (!activeEnv) return
    setLoading(true)
    try {
      const res = await fetchWithTimeout(`/api/environments/${activeEnv}/mqtt/bridges`)
      if (res.ok) {
        const data = await res.json()
        setBridges(data.bridges || [])
      }
    } catch { /* ignore */ }
    setLoading(false)
  }, [activeEnv])

  useEffect(() => {
    fetchData() // eslint-disable-line react-hooks/set-state-in-effect -- fetch-on-mount is intentional
  }, [fetchData])
  useEffect(() => {
    if (!activeEnv) return
    const id = setInterval(fetchData, REFRESH_INTERVAL)
    return () => clearInterval(id)
  }, [activeEnv, fetchData])

  if (loading) {
    return (
      <div>
        <h1 className="text-2xl font-semibold mb-6">MachMQTT Bridges</h1>
        <div className="grid grid-cols-3 gap-4 mb-6">
          {[1,2,3].map(i => <CardSkeleton key={i} />)}
        </div>
        <TableSkeleton rows={3} cols={5} />
      </div>
    )
  }

  if (!bridges || bridges.length === 0) {
    return (
      <div>
        <h1 className="text-2xl font-semibold mb-6">MachMQTT Bridges</h1>
        <div className="bg-white dark:bg-gray-800 rounded-lg shadow p-8 text-center text-gray-500 dark:text-gray-400">
          No MQTT bridges configured. Add <code className="bg-gray-100 dark:bg-gray-700 px-1.5 py-0.5 rounded text-xs">mqtt_bridges</code> to your environment config.
        </div>
      </div>
    )
  }

  const totalConns = bridges.reduce((s, b) => s + (b.status?.connections ?? 0), 0)
  const healthyCount = bridges.filter(b => b.reachable && b.status?.ready && b.status?.nats_connected).length

  return (
    <div>
      <div className="flex items-center justify-between mb-6">
        <h1 className="text-2xl font-semibold">MachMQTT Bridges</h1>
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
            lines={[
              { key: 'connections_active', color: '#a855f7', label: 'Active' },
            ]}
            yFormatter={(v) => v.toFixed(0)}
          />
        </div>
        <div className="bg-white dark:bg-gray-800 rounded-lg shadow p-4">
          <h3 className="text-sm font-medium text-gray-500 dark:text-gray-400 mb-2">Message Rate</h3>
          <TimeSeriesChart
            data={mqttMetrics.data}
            lines={[
              { key: 'in_msgs_rate', color: '#22c55e', label: 'In msgs/s' },
              { key: 'out_msgs_rate', color: '#f97316', label: 'Out msgs/s' },
            ]}
            yFormatter={fmtRateAxis}
          />
        </div>
      </div>

      <div className="space-y-4">
        {bridges.map((b) => {
          const s = b.status
          const healthy = b.reachable && s?.ready && s?.nats_connected
          const displayName = b.configured_name || b.status?.name || `mqtt@${b.ip}`
          return (
          <div key={b.ip} className="bg-white dark:bg-gray-800 rounded-lg shadow">
            <div className="p-4">
              <div className="flex items-center justify-between mb-3">
                <div className="flex items-center gap-3">
                  <span className={`w-2.5 h-2.5 rounded-full ${healthy ? 'bg-healthy' : b.reachable ? 'bg-yellow-400' : 'bg-unhealthy'}`} />
                  <h2 className="font-semibold text-lg">{displayName}</h2>
                  {s?.draining && (
                    <span className="text-xs font-medium text-amber-700 bg-amber-100 dark:bg-amber-900/40 dark:text-amber-300 rounded px-2 py-0.5" title="Operator-drained: not accepting new connections">
                      Draining
                    </span>
                  )}
                  <span className="text-xs text-gray-400">on {b.server_name}</span>
                  {b.admin_url && <span className="text-xs text-gray-400 font-mono">{b.admin_url}</span>}
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

              {s?.error && (
                <div className="bg-red-50 dark:bg-red-900/20 border border-red-200 dark:border-red-800 rounded p-2 mb-3 text-sm text-red-600 dark:text-red-400">
                  {s.error}
                </div>
              )}

              {!b.reachable && (
                <div className="bg-yellow-50 dark:bg-yellow-900/20 border border-yellow-200 dark:border-yellow-800 rounded p-2 mb-3 text-sm text-yellow-700 dark:text-yellow-400">
                  Bridge admin API not reachable. Showing NATS-side data only.
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
                    <DI label="Pool Pub / Sub" value={`${fmtNum(s.metrics.pool_publishes ?? 0)} / ${fmtNum(s.metrics.pool_subscribes ?? 0)}`} />
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
    <div>
      <div className="text-gray-500 dark:text-gray-400 text-xs">{label}</div>
      <div className="font-medium">{value}</div>
    </div>
  )
}

function fmtNum(n: number): string {
  if (n >= 1e9) return (n / 1e9).toFixed(1) + 'B'
  if (n >= 1e6) return (n / 1e6).toFixed(1) + 'M'
  if (n >= 1e3) return (n / 1e3).toFixed(1) + 'K'
  return n.toLocaleString()
}

function fmtBytes(b: number): string {
  if (b >= 1e9) return (b / 1e9).toFixed(1) + ' GB'
  if (b >= 1e6) return (b / 1e6).toFixed(1) + ' MB'
  if (b >= 1e3) return (b / 1e3).toFixed(1) + ' KB'
  return b + ' B'
}

function fmtRate(r: number): string {
  if (r >= 1e6) return (r / 1e6).toFixed(1) + 'M/s'
  if (r >= 1e3) return (r / 1e3).toFixed(1) + 'K/s'
  if (r >= 1) return r.toFixed(0) + '/s'
  if (r > 0) return r.toFixed(1) + '/s'
  return '0/s'
}

function fmtRateAxis(r: number): string {
  if (r >= 1e6) return (r / 1e6).toFixed(1) + 'M'
  if (r >= 1e3) return (r / 1e3).toFixed(1) + 'K'
  if (r >= 1) return r.toFixed(0)
  if (r > 0) return r.toFixed(2)
  return '0'
}

function fmtBytesRate(b: number): string {
  if (b >= 1e9) return (b / 1e9).toFixed(1) + ' GB/s'
  if (b >= 1e6) return (b / 1e6).toFixed(1) + ' MB/s'
  if (b >= 1e3) return (b / 1e3).toFixed(1) + ' KB/s'
  if (b > 0) return b.toFixed(0) + ' B/s'
  return '0 B/s'
}
