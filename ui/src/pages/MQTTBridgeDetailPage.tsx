import { useState, useEffect, useCallback, useMemo, useRef } from 'react'
import { fetchWithTimeout } from '../utils/fetchWithTimeout'
import { useParams, Link } from 'react-router-dom'
import { useStore } from '../store/store'
import { TableSkeleton } from '../components/Skeleton'
import { ArrowLeft } from 'lucide-react'
import { TimeSeriesChart, type LineDef } from '../components/TimeSeriesChart'
import { TimeRangeSelector } from '../components/TimeRangeSelector'
import { useMetrics } from '../hooks/useMetrics'
import { formatNumber as fmtNum, formatBytes as fmtBytes, formatRate as fmtRateAxis } from '../utils/format'

type Tab = 'nats' | 'metrics' | 'pool' | 'cluster' | 'license' | 'config' | 'admin'

// Each refresh issues live admin-API fetches to the bridge, so keep it moderate.
const REFRESH_INTERVAL = 5_000

// Hoisted so the array references are stable across renders — inline arrays would
// defeat TimeSeriesChart's memo and re-render recharts on every 5s poll.
const CONN_LINES: LineDef[] = [
  { key: 'connections_active', color: '#a855f7', label: 'MQTT Active' },
  { key: 'sockets_open', color: '#64748b', label: 'Sockets Open' },
]
const MSG_RATE_LINES: LineDef[] = [
  { key: 'in_msgs_rate', color: '#22c55e', label: 'In msgs/s' },
  { key: 'out_msgs_rate', color: '#f97316', label: 'Out msgs/s' },
]
const QOS_LINES: LineDef[] = [
  { key: 'msgs_recv_qos0', color: '#22c55e', label: 'Recv QoS0' },
  { key: 'msgs_recv_qos1', color: '#16a34a', label: 'Recv QoS1' },
  { key: 'msgs_recv_qos2', color: '#15803d', label: 'Recv QoS2' },
  { key: 'msgs_sent_qos0', color: '#f97316', label: 'Sent QoS0' },
  { key: 'msgs_sent_qos1', color: '#ea580c', label: 'Sent QoS1' },
  { key: 'msgs_sent_qos2', color: '#c2410c', label: 'Sent QoS2' },
]
const JS_HEALTH_LINES: LineDef[] = [
  { key: 'consumer_pending_messages', color: '#f59e0b', label: 'Pending msgs' },
  { key: 'session_write_behind_depth', color: '#6366f1', label: 'Write-behind depth' },
  { key: 'stalled_consumers', color: '#ef4444', label: 'Stalled consumers' },
  { key: 'inflight_out_messages', color: '#0ea5e9', label: 'Inflight out' },
]
const BACKPRESSURE_LINES: LineDef[] = [
  { key: 'op_queue_depth', color: '#6366f1', label: 'Op queue depth' },
  { key: 'op_suspended_conns', color: '#ef4444', label: 'Suspended conns' },
  { key: 'worker_pool_queue_depth', color: '#f59e0b', label: 'Worker pool queue' },
]
const POOL_LINES: LineDef[] = [{ key: 'pool_slot_connected', color: '#27aae1', label: 'Slots connected' }]
const CAPACITY_LINES: LineDef[] = [
  { key: 'retained_messages', color: '#8b5cf6', label: 'Retained msgs' },
  { key: 'subscriptions_active', color: '#10b981', label: 'Active subs' },
]
const MEMORY_LINES: LineDef[] = [{ key: 'go_heap_inuse_bytes', color: '#ec4899', label: 'Heap in-use' }]
const RUNTIME_LINES: LineDef[] = [
  { key: 'go_goroutines', color: '#14b8a6', label: 'Goroutines' },
  { key: 'scram_sessions_active', color: '#f97316', label: 'SCRAM sessions' },
]

export function MQTTBridgeDetailPage({ role }: { role?: string }) {
  const { bridge } = useParams<{ bridge: string }>()
  const activeEnv = useStore((s) => s.activeEnv)
  const fetchSeq = useRef(0)
  const loadedOnce = useRef(false)
  const [tab, setTab] = useState<Tab>('nats')
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  const [nats, setNats] = useState<any>(null)
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  const [metrics, setMetrics] = useState<any>(null)
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  const [pool, setPool] = useState<any>(null)
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  const [license, setLicense] = useState<any>(null)
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  const [diag, setDiag] = useState<any>(null)
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  const [cluster, setCluster] = useState<any>(null)
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  const [readyz, setReadyz] = useState<any>(null)
  const [loading, setLoading] = useState(true)
  // Inline error banner shown when NO source (admin HTTP or push) returns data —
  // replaces a toast that previously fired on every 5s poll.
  const [loadError, setLoadError] = useState<string | null>(null)

  const bridgeMetricsParams = useMemo(() => (bridge ? { bridge_id: bridge } : undefined), [bridge])
  const bridgeMetrics = useMetrics(activeEnv, 'metrics/mqtt', bridgeMetricsParams)

  const fetchAll = useCallback(async () => {
    if (!activeEnv || !bridge) return
    const seq = ++fetchSeq.current
    // Show the skeleton only before the first result lands; later polls refresh
    // in place so the page doesn't strobe a skeleton every interval.
    if (!loadedOnce.current) setLoading(true)
    const b = encodeURIComponent(bridge)
    const base = `/api/environments/${activeEnv}/mqtt/${b}`
    const val = (r: PromiseSettledResult<unknown>) => (r.status === 'fulfilled' ? r.value : null)
    const results = await Promise.allSettled([
      fetchWithTimeout(`${base}/diag`).then(r => r.ok ? r.json() : null),
      fetchWithTimeout(`${base}/metrics`).then(r => r.ok ? r.json() : null),
      fetchWithTimeout(`${base}/pool`).then(r => r.ok ? r.json() : null),
      fetchWithTimeout(`${base}/license`).then(r => r.ok ? r.json() : null),
      fetchWithTimeout(`${base}/diag/config`).then(r => r.ok ? r.json() : null),
      fetchWithTimeout(`${base}/cluster`).then(r => r.ok ? r.json() : null),
      // Readiness state (draining / JetStream-degraded) for the header label.
      // Deliberately last: it is excluded from the all-sources-failed check
      // below, since it decorates the page rather than populating a tab.
      fetchWithTimeout(`${base}/readyz`).then(r => r.ok ? r.json() : null),
    ])
    // Ignore a late response if the bridge changed or a newer refresh started.
    if (seq !== fetchSeq.current) return
    setNats(val(results[0]))
    setMetrics(val(results[1]))
    setPool(val(results[2]))
    setLicense(val(results[3]))
    setDiag(val(results[4]))
    setCluster(val(results[5]))
    setReadyz(val(results[6]))
    loadedOnce.current = true
    setLoading(false)
    // Banner only when every source failed; clears as soon as one recovers.
    setLoadError(results.slice(0, 6).every((r) => val(r) == null)
      ? 'Could not load any details for this bridge. Its admin HTTP API is unreachable and no NATS push metrics are currently available — check that the bridge is running and either reachable on its admin port or publishing metrics to $MQTT5.metrics.>.'
      : null)
  }, [activeEnv, bridge])

  // Reset to the default tab and re-arm the first-load skeleton when navigating
  // between bridges.
  useEffect(() => {
    setTab('nats') // eslint-disable-line react-hooks/set-state-in-effect -- intentional reset on bridge change
    loadedOnce.current = false
  }, [bridge])

  useEffect(() => {
    fetchAll() // eslint-disable-line react-hooks/set-state-in-effect -- fetch-on-mount is intentional
  }, [fetchAll])
  useEffect(() => {
    if (!activeEnv || !bridge) return
    const id = setInterval(fetchAll, REFRESH_INTERVAL)
    return () => clearInterval(id)
  }, [activeEnv, bridge, fetchAll])

  const tabs: { id: Tab; label: string }[] = [
    { id: 'nats', label: 'NATS Connection' },
    { id: 'metrics', label: 'Metrics' },
    { id: 'pool', label: 'Connection Pool' },
    { id: 'cluster', label: 'Cluster' },
    { id: 'license', label: 'License' },
    { id: 'config', label: 'Config' },
    ...(role === 'admin' ? [{ id: 'admin' as Tab, label: 'Admin' }] : []),
  ]

  return (
    <div>
      <div className="flex items-center gap-3 mb-4">
        <Link to="/mqtt" className="text-gray-400 hover:text-gray-600 dark:hover:text-gray-200">
          <ArrowLeft className="w-5 h-5" />
        </Link>
        <h1 className="text-2xl font-semibold">{bridge}</h1>
        {diag?.version && <span className="text-xs text-gray-400 bg-gray-100 dark:bg-gray-700 rounded px-2 py-0.5">{diag.version}</span>}
        {metrics?.drained === 1 && (
          <span className="text-xs font-medium text-amber-700 bg-amber-100 dark:bg-amber-900/40 dark:text-amber-300 rounded px-2 py-0.5" title="Operator-drained: not accepting new connections (POST /admin/drain)">
            Draining
          </span>
        )}
        {readyz?.jetstream_degraded && (
          <span className="text-xs font-medium text-amber-700 bg-amber-100 dark:bg-amber-900/40 dark:text-amber-300 rounded px-2 py-0.5" title="MQTT service is up; JetStream is currently unavailable, so QoS 1/2 persistence is affected">
            JS Degraded
          </span>
        )}
      </div>

      {loadError && (
        <div className="mb-4 rounded border border-red-300 bg-red-50 px-3 py-2 text-sm text-red-800 dark:border-red-800 dark:bg-red-900/30 dark:text-red-200">
          {loadError}
        </div>
      )}

      <div className="flex gap-1 mb-4 border-b dark:border-gray-700">
        {tabs.map(t => (
          <button
            key={t.id}
            onClick={() => setTab(t.id)}
            className={`px-4 py-2 text-sm font-medium border-b-2 transition-colors ${
              tab === t.id
                ? 'border-brand-blue text-brand-blue'
                : 'border-transparent text-gray-500 hover:text-gray-700 dark:hover:text-gray-300'
            }`}
          >
            {t.label}
          </button>
        ))}
      </div>

      {loading ? <TableSkeleton rows={5} cols={4} /> : (
        <>
          {tab === 'nats' && <NATSTab data={nats} />}
          {tab === 'metrics' && <MetricsTab data={metrics} tsMetrics={bridgeMetrics} />}
          {tab === 'pool' && <PoolTab data={pool} />}
          {tab === 'cluster' && <ClusterTab data={cluster} env={activeEnv} bridge={bridge} />}
          {tab === 'license' && <LicenseTab data={license} />}
          {tab === 'config' && <ConfigTab data={diag} />}
          {tab === 'admin' && role === 'admin' && (
            <AdminTab env={activeEnv} bridge={bridge} clusterEnabled={cluster?.available === true} onChanged={fetchAll} />
          )}
        </>
      )}
    </div>
  )
}

// eslint-disable-next-line @typescript-eslint/no-explicit-any
function NATSTab({ data }: { data: any }) {
  if (!data) return <Empty msg="NATS diagnostics not available" />
  const c = data.connection
  const minimalMode = c?.connected && !data.account
  return (
    <div className="space-y-6">
      {minimalMode && (
        <div className="rounded border border-amber-300 bg-amber-50 px-3 py-2 text-sm text-amber-800 dark:border-amber-800 dark:bg-amber-900/30 dark:text-amber-300">
          JetStream unavailable — bridge is running in minimal mode. QoS 1/2 and persistent sessions are disabled (QoS capped at 0).
        </div>
      )}
      <Section title="Connection">
        <Grid>
          <DI label="Connected" value={c?.connected ? 'Yes' : 'No'} />
          <DI label="Reconnecting" value={c?.reconnecting ? 'Yes' : 'No'} />
          <DI label="Draining" value={c?.draining ? 'Yes' : 'No'} />
          <DI label="URL" value={c?.url} />
          <DI label="Server Name" value={c?.server_name} />
          <DI label="Server Version" value={c?.server_version} />
          <DI label="Cluster" value={c?.cluster_name || '-'} />
          <DI label="RTT" value={c?.rtt || '-'} />
          <DI label="Max Payload" value={fmtBytes(c?.max_payload || 0)} />
          <DI label="Subscriptions" value={(c?.subscriptions || 0).toLocaleString()} />
          <DI label="Reconnects" value={(c?.reconnects || 0).toLocaleString()} />
          <DI label="Msgs In" value={fmtNum(c?.in_msgs || 0)} />
          <DI label="Msgs Out" value={fmtNum(c?.out_msgs || 0)} />
          <DI label="Bytes In" value={fmtBytes(c?.in_bytes || 0)} />
          <DI label="Bytes Out" value={fmtBytes(c?.out_bytes || 0)} />
        </Grid>
        {c?.server_id && (
          <div className="mt-3">
            <div className="text-xs text-gray-500 dark:text-gray-400 mb-1">Server ID</div>
            <div className="font-mono text-xs bg-gray-100 dark:bg-gray-700 rounded px-2 py-1 break-all">{c.server_id}</div>
          </div>
        )}
        {c?.servers && c.servers.length > 0 && (
          <div className="mt-3">
            <div className="text-xs text-gray-500 dark:text-gray-400 mb-1">Known Servers</div>
            <div className="flex flex-wrap gap-2">
              {c.servers.map((s: string, i: number) => (
                <span key={i} className="font-mono text-xs bg-gray-100 dark:bg-gray-700 rounded px-2 py-0.5">{s}</span>
              ))}
            </div>
          </div>
        )}
      </Section>

      {data.account && (
        <Section title="JetStream Account">
          <Grid>
            <DI label="Domain" value={data.account.domain || '-'} />
            <DI label="Memory" value={fmtBytes(data.account.memory_bytes || 0)} />
            <DI label="Storage" value={fmtBytes(data.account.store_bytes || 0)} />
            <DI label="Streams" value={(data.account.streams || 0).toString()} />
            <DI label="Consumers" value={(data.account.consumers || 0).toString()} />
          </Grid>
        </Section>
      )}

      {data.streams && data.streams.length > 0 && (
        <Section title="Streams">
          <Table
            headers={['Name', 'Messages', 'Bytes', 'Consumers', 'Subjects', 'First Seq', 'Last Seq', 'Created', 'Error']}
            // eslint-disable-next-line @typescript-eslint/no-explicit-any
            rows={data.streams.map((s: any) => [
              s.name, fmtNum(s.messages), fmtBytes(s.bytes), s.consumers,
              s.num_subjects || 0, s.first_seq, s.last_seq,
              s.created ? new Date(s.created).toLocaleString() : '-',
              s.error || '-',
            ])}
          />
        </Section>
      )}

      {data.kv_buckets && data.kv_buckets.length > 0 && (
        <Section title="KV Buckets">
          <Table
            headers={['Bucket', 'Values', 'Bytes', 'TTL', 'Error']}
            // eslint-disable-next-line @typescript-eslint/no-explicit-any
            rows={data.kv_buckets.map((kv: any) => [
              kv.bucket, fmtNum(kv.values), fmtBytes(kv.bytes),
              kv.ttl || '-', kv.error || '-',
            ])}
          />
        </Section>
      )}
    </div>
  )
}

// fmtMs formats average latency from histogram sum/count into milliseconds.
// Returns '—' when count is zero to avoid division-by-zero.
function fmtMs(sumSeconds: number, count: number): string {
  if (!count) return '—'
  const ms = (sumSeconds / count) * 1000
  if (ms >= 1000) return (ms / 1000).toFixed(2) + ' s'
  if (ms >= 1) return ms.toFixed(2) + ' ms'
  return (ms * 1000).toFixed(0) + ' µs'
}

// fmtPending renders consumer_pending_messages.
// -1 means JetStream is unavailable and the metric was absent.
function fmtPending(v: number): string {
  if (v < 0) return 'n/a (JS unavailable)'
  return fmtNum(v)
}

// eslint-disable-next-line @typescript-eslint/no-explicit-any
function MetricsTab({ data, tsMetrics }: { data: any; tsMetrics: ReturnType<typeof useMetrics> }) {
  if (!data) return <Empty msg="Metrics not available" />
  return (
    <div className="space-y-6">
      <div className="flex items-center justify-between">
        <h3 className="font-medium text-sm">Trends</h3>
        <TimeRangeSelector value={tsMetrics.range} onChange={tsMetrics.setRange} />
      </div>
      <div className="grid grid-cols-1 lg:grid-cols-2 gap-4">
        <div className="bg-white dark:bg-gray-800 rounded-lg shadow p-4">
          <h3 className="text-sm font-medium text-gray-500 dark:text-gray-400 mb-2">Connections</h3>
          <TimeSeriesChart
            data={tsMetrics.data}
            lines={CONN_LINES}
            yFormatter={(v) => v.toFixed(0)}
          />
        </div>
        <div className="bg-white dark:bg-gray-800 rounded-lg shadow p-4">
          <h3 className="text-sm font-medium text-gray-500 dark:text-gray-400 mb-2">Message Rate</h3>
          <TimeSeriesChart
            data={tsMetrics.data}
            lines={MSG_RATE_LINES}
            yFormatter={fmtRateAxis}
          />
        </div>
        <div className="bg-white dark:bg-gray-800 rounded-lg shadow p-4">
          <h3 className="text-sm font-medium text-gray-500 dark:text-gray-400 mb-2">QoS Message Totals</h3>
          <TimeSeriesChart
            data={tsMetrics.data}
            lines={QOS_LINES}
            yFormatter={fmtRateAxis}
          />
        </div>
        <div className="bg-white dark:bg-gray-800 rounded-lg shadow p-4">
          <h3 className="text-sm font-medium text-gray-500 dark:text-gray-400 mb-2">JetStream Health</h3>
          <TimeSeriesChart
            data={tsMetrics.data}
            lines={JS_HEALTH_LINES}
            yFormatter={(v) => v.toFixed(0)}
          />
        </div>
        <div className="bg-white dark:bg-gray-800 rounded-lg shadow p-4">
          <h3 className="text-sm font-medium text-gray-500 dark:text-gray-400 mb-2">Backpressure</h3>
          <TimeSeriesChart
            data={tsMetrics.data}
            lines={BACKPRESSURE_LINES}
            yFormatter={(v) => v.toFixed(0)}
          />
        </div>
        <div className="bg-white dark:bg-gray-800 rounded-lg shadow p-4">
          <h3 className="text-sm font-medium text-gray-500 dark:text-gray-400 mb-2">Connection Pool</h3>
          <TimeSeriesChart
            data={tsMetrics.data}
            lines={POOL_LINES}
            yFormatter={(v) => v.toFixed(0)}
          />
        </div>
        <div className="bg-white dark:bg-gray-800 rounded-lg shadow p-4">
          <h3 className="text-sm font-medium text-gray-500 dark:text-gray-400 mb-2">Capacity</h3>
          <TimeSeriesChart
            data={tsMetrics.data}
            lines={CAPACITY_LINES}
            yFormatter={(v) => v.toFixed(0)}
          />
        </div>
        <div className="bg-white dark:bg-gray-800 rounded-lg shadow p-4">
          <h3 className="text-sm font-medium text-gray-500 dark:text-gray-400 mb-2">Heap Memory</h3>
          <TimeSeriesChart
            data={tsMetrics.data}
            lines={MEMORY_LINES}
            yFormatter={(v) => fmtBytes(v)}
          />
        </div>
        <div className="bg-white dark:bg-gray-800 rounded-lg shadow p-4">
          <h3 className="text-sm font-medium text-gray-500 dark:text-gray-400 mb-2">Goroutines &amp; SCRAM Sessions</h3>
          <TimeSeriesChart
            data={tsMetrics.data}
            lines={RUNTIME_LINES}
            yFormatter={(v) => v.toFixed(0)}
          />
        </div>
      </div>

      <Section title="Connections (established MQTT)">
        <Grid>
          <DI label="Active" value={fmtNum(data.connections_active)} />
          <DI label="Peak Active" value={fmtNum(data.connections_max)} />
          <DI label="Total CONNECTs" value={fmtNum(data.connections_total)} />
          <DI label="Rejected" value={fmtNum(data.connections_rejected)} />
          <DI label="WS Active" value={fmtNum(data.ws_connections_active)} />
          <DI label="WS Total CONNECTs" value={fmtNum(data.ws_connections_total)} />
        </Grid>
        <p className="text-xs text-gray-400 mt-2">Counted once the CONNECT handshake completes; pre-CONNECT probes (e.g. load-balancer TCP checks) are excluded — see Sockets below.</p>
      </Section>
      <Section title="Sockets (raw transport accepts)">
        <Grid>
          <DI label="Open" value={fmtNum(data.sockets_open)} />
          <DI label="Accepted (total)" value={fmtNum(data.sockets_accepted)} />
          <DI label="WS Open" value={fmtNum(data.ws_sockets_open)} />
          <DI label="WS Accepted (total)" value={fmtNum(data.ws_sockets_accepted)} />
        </Grid>
        <p className="text-xs text-gray-400 mt-2">Every accepted transport socket, including non-MQTT probes. <span className="font-mono">Open</span> is the value gated against <span className="font-mono">max_connections</span>.</p>
      </Section>
      <Section title="Rejections by Reason">
        <Grid>
          <DI label="Max Conns" value={fmtNum(data.rejected_max_conns)} />
          <DI label="License" value={fmtNum(data.rejected_license)} />
          <DI label="Per-IP Conns" value={fmtNum(data.rejected_per_ip_conns)} />
          <DI label="Per-IP Accept" value={fmtNum(data.rejected_per_ip_accept)} />
          <DI label="Pool Full" value={fmtNum(data.rejected_pool_full)} />
          <DI label="Connect Timeout" value={fmtNum(data.rejected_connect_timeout)} />
          <DI label="Auth Timeout" value={fmtNum(data.rejected_auth_timeout)} />
          <DI label="Worker Pool" value={fmtNum(data.rejected_worker_pool)} />
          <DI label="Memory Budget" value={fmtNum(data.rejected_mem_budget)} />
        </Grid>
        <p className="text-xs text-gray-400 mt-2">The headline <em>Rejected</em> total above excludes <span className="font-mono">Memory Budget</span> (the broker's deprecated umbrella counter predates it); this breakdown is complete.</p>
      </Section>
      {data.connack_rejected_by_reason && Object.keys(data.connack_rejected_by_reason).length > 0 && (
        <Section title="CONNACK Rejections by Reason Code">
          <Grid>
            {Object.entries(data.connack_rejected_by_reason).map(([code, n]) => (
              <DI key={code} label={code} value={fmtNum(n as number)} mono />
            ))}
          </Grid>
          <p className="text-xs text-gray-400 mt-2">Post-CONNECT failures by MQTT reason code (e.g. <span className="font-mono">0x88</span> ServerUnavailable, <span className="font-mono">0x8C</span> BadAuthenticationMethod).</p>
        </Section>
      )}
      {data.suback_rejected_by_reason && Object.keys(data.suback_rejected_by_reason).length > 0 && (
        <Section title="SUBACK Rejections by Reason Code">
          <Grid>
            {Object.entries(data.suback_rejected_by_reason).map(([code, n]) => (
              <DI key={code} label={code} value={fmtNum(n as number)} mono />
            ))}
          </Grid>
          <p className="text-xs text-gray-400 mt-2">Per-filter SUBSCRIBE rejections by MQTT reason code (e.g. <span className="font-mono">0x87</span> NotAuthorized, <span className="font-mono">0x8F</span> TopicFilterInvalid).</p>
        </Section>
      )}
      <Section title="Dispatch Pool Saturation">
        <Grid>
          <DI label="TLS Slots Active" value={fmtNum(data.dispatch_slots_tls)} />
          <DI label="WebSocket Slots Active" value={fmtNum(data.dispatch_slots_ws)} />
        </Grid>
        <p className="text-xs text-gray-400 mt-2">Sustained proximity to the configured handshake pool size precedes <span className="font-mono">pool_full</span> rejections.</p>
      </Section>
      <Section title="Authentication">
        <Grid>
          <DI label="Auth Success" value={fmtNum(data.auth_success)} />
          <DI label="Auth Failure (total)" value={fmtNum(data.auth_failure)} />
          <DI label="Bad Credentials" value={fmtNum(data.auth_fail_bad_credentials)} />
          <DI label="Enhanced (SCRAM)" value={fmtNum(data.auth_fail_enhanced)} />
          <DI label="Account Locked" value={fmtNum(data.auth_fail_locked)} />
          <DI label="License" value={fmtNum(data.auth_fail_license)} />
          <DI label="Token Expired" value={fmtNum(data.auth_fail_token_expired)} />
          <DI label="Bad Signature" value={fmtNum(data.auth_fail_bad_signature)} />
          <DI label="Claim Mismatch" value={fmtNum(data.auth_fail_claim_mismatch)} />
          <DI label="JWKS Unavailable" value={fmtNum(data.auth_fail_jwks_unavailable)} />
          <DI label="Webhook Denied" value={fmtNum(data.auth_fail_webhook_denied)} />
          <DI label="Webhook Unavailable" value={fmtNum(data.auth_fail_webhook_unavailable)} />
          <DI label="Other" value={fmtNum(data.auth_fail_other)} />
          <DI label="SCRAM Sessions Active" value={fmtNum(data.scram_sessions_active)} />
          <DI label="NATS Enforcement Fallback" value={fmtNum(data.nats_enforcement_fallback)} />
          <DI label="NATS Enforcement Denied" value={fmtNum(data.nats_enforcement_denied)} />
          <DI label="Webhook Requests" value={fmtNum(data.auth_webhook_requests)} />
          <DI label="Webhook Transport Failures" value={fmtNum(data.auth_webhook_transport_failures)} />
          <DI label="Webhook Inflight Rejected" value={fmtNum(data.auth_webhook_inflight_rejected)} />
          <DI label="Failure Tracker Entries" value={fmtNum(data.auth_failure_tracker_entries)} />
          <DI label="Credential Expiry Disconnects" value={fmtNum(data.credential_expiry_disconnects)} />
          <DI label="mTLS Fallback: License" value={fmtNum(data.mtls_identity_fallback_license)} />
          <DI label="mTLS Fallback: No Match" value={fmtNum(data.mtls_identity_fallback_no_match)} />
          <DI label="mTLS Fallback: No Cert" value={fmtNum(data.mtls_identity_fallback_no_cert)} />
        </Grid>
        <p className="text-xs text-gray-400 mt-2">Webhook fields are populated only when <span className="font-mono">auth.type</span> is <span className="font-mono">http</span>.</p>
      </Section>
      <Section title="License Rejections">
        <Grid>
          <DI label="Auth Method" value={fmtNum(data.license_rejected_auth_method)} />
          <DI label="Retain" value={fmtNum(data.license_rejected_retain)} />
          <DI label="Proxy Protocol" value={fmtNum(data.license_rejected_proxy_protocol)} />
        </Grid>
      </Section>
      <Section title="Client Messages (MQTT ↔ Broker)">
        <Grid>
          <DI label="Recv QoS 0" value={fmtNum(data.msgs_recv_qos0)} />
          <DI label="Recv QoS 1" value={fmtNum(data.msgs_recv_qos1)} />
          <DI label="Recv QoS 2" value={fmtNum(data.msgs_recv_qos2)} />
          <DI label="Sent QoS 0" value={fmtNum(data.msgs_sent_qos0)} />
          <DI label="Sent QoS 1" value={fmtNum(data.msgs_sent_qos1)} />
          <DI label="Sent QoS 2" value={fmtNum(data.msgs_sent_qos2)} />
          <DI label="Redelivered" value={fmtNum(data.msgs_redelivered)} />
        </Grid>
      </Section>
      <Section title="Server Messages (Broker ↔ NATS)">
        <Grid>
          <DI label="Published QoS 0" value={fmtNum(data.server_published_qos0)} />
          <DI label="Published QoS 1" value={fmtNum(data.server_published_qos1)} />
          <DI label="Published QoS 2" value={fmtNum(data.server_published_qos2)} />
          <DI label="Consumed QoS 0" value={fmtNum(data.server_consumed_qos0)} />
          <DI label="Consumed QoS 1" value={fmtNum(data.server_consumed_qos1)} />
          <DI label="Consumed QoS 2" value={fmtNum(data.server_consumed_qos2)} />
        </Grid>
      </Section>
      <Section title="Will Messages">
        <Grid>
          <DI label="Published" value={fmtNum(data.will_published)} />
          <DI label="Dropped: Queue Full" value={fmtNum(data.will_dropped_queue_full)} />
          <DI label="Dropped: Publish Error" value={fmtNum(data.will_dropped_publish_error)} />
          <DI label="Dropped: Invalid Topic" value={fmtNum(data.will_dropped_invalid_topic)} />
          <DI label="Dropped: Shutdown" value={fmtNum(data.will_dropped_shutdown)} />
          <DI label="Suppressed (Reconnected)" value={fmtNum(data.will_suppressed_reconnected)} />
          <DI label="Suppressed (Shutdown)" value={fmtNum(data.will_suppressed_shutdown)} />
          <DI label="Pending" value={fmtNum(data.will_pending)} />
          <DI label="Retry Pending" value={fmtNum(data.will_retry_pending)} />
          <DI label="Verify Failures" value={fmtNum(data.will_verify_failures)} />
          <DI label="Persist Failed: Write" value={fmtNum(data.will_persist_failed_write)} />
          <DI label="Persist Failed: Queue Full" value={fmtNum(data.will_persist_failed_queue_full)} />
        </Grid>
        <p className="text-xs text-gray-400 mt-2">A one-time burst of <em>Verify Failures</em> is expected after upgrading the broker across the signed-wills boundary; a sustained rise means stored wills are failing verification and not firing.</p>
      </Section>
      <Section title="Protocol">
        <Grid>
          <DI label="Subscribes" value={fmtNum(data.subscribes)} />
          <DI label="Unsubscribes" value={fmtNum(data.unsubscribes)} />
          <DI label="Subscribe Flush Failures" value={fmtNum(data.subscribe_flush_failures)} />
          <DI label="Keepalive Timeouts" value={fmtNum(data.keepalive_timeouts)} />
          <DI label="PINGREQ Rate-Limited" value={fmtNum(data.pingreq_rate_limited)} />
        </Grid>
      </Section>
      <Section title="NATS">
        <Grid>
          <DI label="Disconnects" value={fmtNum(data.nats_disconnects)} />
          <DI label="Reconnects" value={fmtNum(data.nats_reconnects)} />
          <DI label="Slow Consumer Events" value={fmtNum(data.nats_slow_consumer)} />
        </Grid>
      </Section>
      <Section title="Errors">
        <Grid>
          <DI label="Panics Recovered" value={fmtNum(data.panics_recovered)} />
          <DI label="TLS Handshake Failures" value={fmtNum(data.tls_handshake_failures)} />
          <DI label="TLS Cert Reload Failures" value={fmtNum(data.tls_cert_reload_failures)} />
          <DI label="OAuth2 JWKS Fetch Failures" value={fmtNum(data.oauth2_jwks_fetch_failures)} />
          <DI label="Audit Write Failures" value={fmtNum(data.audit_write_failures)} />
          <DI label="Proxy Protocol Errors" value={fmtNum(data.proxy_protocol_errors)} />
          <DI label="WS Upgrade Failures" value={fmtNum(data.ws_upgrade_failures)} />
          <DI label="WS Protocol Violations" value={fmtNum(data.ws_protocol_violations)} />
          <DI label="Publish Refused (Topic)" value={fmtNum(data.publish_refused_topic)} />
          <DI label="Flow-Control Overflow" value={fmtNum(data.flowcontrol_overflow)} />
          <DI label="OAuth2 Token Cache Evictions" value={fmtNum(data.oauth2_token_cache_evictions)} />
        </Grid>
      </Section>
      {data.disconnects_sent_by_reason && Object.keys(data.disconnects_sent_by_reason).length > 0 && (
        <Section title="Server DISCONNECTs by Reason Code">
          <Grid>
            {Object.entries(data.disconnects_sent_by_reason).map(([code, n]) => (
              <DI key={code} label={code} value={fmtNum(n as number)} mono />
            ))}
          </Grid>
          <p className="text-xs text-gray-400 mt-2">Server-initiated DISCONNECTs by MQTT reason code (e.g. <span className="font-mono">0x8F</span> SessionTakenOver, <span className="font-mono">0x93</span> ReceiveMaximumExceeded).</p>
        </Section>
      )}
      <Section title="Durability & DLQ">
        <Grid>
          <DI label="QoS 2 Publish Failed (to NATS)" value={fmtNum(data.qos2_server_publish_failed)} />
          <DI label="QoS 1 Client Send Failed" value={fmtNum(data.qos1_client_send_failed)} />
          <DI label="QoS 2 Client Send Failed" value={fmtNum(data.qos2_client_send_failed)} />
          <DI label="Publish Failed QoS 0" value={fmtNum(data.server_publish_failed_qos0)} />
          <DI label="Publish Failed QoS 1" value={fmtNum(data.server_publish_failed_qos1)} />
          <DI label="Publish Failed QoS 2" value={fmtNum(data.server_publish_failed_qos2)} />
          <DI label="QoS 0 Shed" value={fmtNum(data.qos0_messages_shed)} />
          <DI label="Oversized Dropped" value={fmtNum(data.oversized_dropped)} />
          <DI label="Publish-Outage Disconnects" value={fmtNum(data.publish_outage_disconnects)} />
          <DI label="Server Publish Dropped" value={fmtNum(data.server_publish_dropped)} />
          <DI label="Dead Lettered" value={fmtNum(data.messages_dead_lettered)} />
          <DI label="Poison Terminated" value={fmtNum(data.poison_messages_terminated)} />
          <DI label="DLQ Write Failed" value={fmtNum(data.dead_letter_write_failed)} />
          <DI label="Outbound Queue Dropped" value={fmtNum(data.outbound_queue_dropped)} />
          <DI label="Outbound Evictions" value={fmtNum(data.outbound_evictions)} />
          <DI label="Outbound Stall Evictions" value={fmtNum(data.outbound_stall_evictions)} />
          <DI label="Outbound Stalled Conns" value={fmtNum(data.outbound_stalled_conns)} />
          <DI label="Retained Verify Failures" value={fmtNum(data.retained_verify_failures)} />
          <DI label="Retain Persist Failed: Put" value={fmtNum(data.retain_persist_failed_put)} />
          <DI label="Retain Persist Failed: Delete" value={fmtNum(data.retain_persist_failed_delete)} />
          <DI label="QoS 2 Sync-Persist Failed" value={fmtNum(data.qos2_sync_persist_failed)} />
        </Grid>
        <p className="text-xs text-gray-400 mt-2">A failed QoS 2 sync-persist write defers the delivery until the write succeeds; the counter rising means JetStream writes are failing, not that messages were lost.</p>
      </Section>
      <Section title="Capacity & Memory">
        <Grid>
          <DI label="Retained Messages" value={fmtNum(data.retained_messages)} />
          <DI label="Inflight Out Messages" value={fmtNum(data.inflight_out_messages)} />
          <DI label="Active Subscriptions" value={fmtNum(data.subscriptions_active)} />
          <DI label="Outbound Queue Bytes" value={fmtBytes(data.outbound_bytes)} />
          <DI label="Inbound Buffer Bytes" value={fmtBytes(data.inbound_bytes)} />
        </Grid>
      </Section>
      <Section title="Hooks & $SYS Tree">
        <Grid>
          <DI label="Hook Vetoes" value={fmtNum(data.hook_vetoes)} />
          <DI label="Hook Panics" value={fmtNum(data.hook_panics)} />
          <DI label="$SYS Messages Published" value={fmtNum(data.sys_tree_published)} />
          <DI label="$SYS Publishes Blocked" value={fmtNum(data.sys_publish_blocked)} />
        </Grid>
        <p className="text-xs text-gray-400 mt-2">Hook counters stay zero unless the broker binary registers lifecycle hooks. <em>$SYS Publishes Blocked</em> counts client attempts to write into the broker-owned <span className="font-mono">$SYS/</span> tree — a rise is a spoofing attempt or a misconfigured client.</p>
      </Section>
      <Section title="Bridge & Pool Health">
        <Grid>
          <DI label="Pool Slots Connected" value={fmtNum(data.pool_slot_connected)} />
          <DI label="Pool Slot Rebuilds" value={fmtNum(data.pool_slot_rebuilds)} />
          {data.pool && (
            <>
              <DI label="Pool Publish Backlog" value={fmtBytes(data.pool.buffered_bytes)} />
              <DI label="Pool Backlog Peak" value={fmtBytes(data.pool.buffered_bytes_max)} />
            </>
          )}
          <DI label="Primary Rebuilds" value={fmtNum(data.bridge_primary_rebuilds)} />
          <DI label="Rebuilds Degraded (no JS)" value={fmtNum(data.bridge_rebuilds_degraded)} />
          <DI label="Consumer Reattached" value={fmtNum(data.bridge_consumer_reattached)} />
          <DI label="Consumer Force-Disconnected" value={fmtNum(data.bridge_consumer_force_disconnected)} />
          <DI label="Push Force-Disconnected" value={fmtNum(data.bridge_consumer_push_force_disconnected)} />
        </Grid>
      </Section>
      <Section title="Throttling & ACL">
        <Grid>
          <DI label="Aggregate Limit (msg/s)" value={fmtNum(data.aggregate_publish_limit_msgs_per_sec)} />
          <DI label="Throttled (per-client)" value={fmtNum(data.publish_throttled_per_client)} />
          <DI label="Throttled (aggregate)" value={fmtNum(data.publish_throttled_aggregate)} />
          <DI label="ACL Denied: Publish" value={fmtNum(data.acl_denied_publish)} />
          <DI label="ACL Denied: Subscribe" value={fmtNum(data.acl_denied_subscribe)} />
        </Grid>
      </Section>
      {data.reactor && (
        <Section title="I/O Reactor">
          <Grid>
            <DI label="Task Queue Depth" value={fmtNum(data.reactor.task_queue_depth)} />
            <DI label="Task Queue Peak" value={fmtNum(data.reactor.task_queue_depth_max)} />
            <DI label="Read Continuations" value={fmtNum(data.reactor.read_continuations)} />
            <DI label="Write Backpressure" value={fmtNum(data.reactor.write_backpressure)} />
            <DI label="Feed Write Overflows" value={fmtNum(data.reactor.feed_write_overflows)} />
            <DI label="Feed Read Overflows" value={fmtNum(data.reactor.feed_read_overflows)} />
            <DI label="Loop Panics" value={fmtNum(data.reactor.loop_panics)} />
            <DI label="Loop Deaths" value={fmtNum(data.reactor.loop_deaths)} />
          </Grid>
          <p className="text-xs text-gray-400 mt-2">Non-zero <em>Read Continuations</em> under load is normal read-fairness throttling; a sharp rise correlated with latency points at a flooding connection. Any <em>Loop Deaths</em> means an event loop died and force-closed its connections.</p>
        </Section>
      )}
      <Section title="Queue Backpressure">
        <Grid>
          <DI label="Worker Pool Queue" value={fmtNum(data.worker_pool_queue_depth)} />
          <DI label="Op Queue Depth" value={fmtNum(data.op_queue_depth)} />
          <DI label="Op Queue Bytes" value={fmtBytes(data.op_queue_bytes)} />
          <DI label="Op Suspended Conns" value={fmtNum(data.op_suspended_conns)} />
          <DI label="Op Pool Queue" value={fmtNum(data.op_pool_queue_depth)} />
          <DI label="Op Pool Rejected" value={fmtNum(data.op_pool_rejected)} />
        </Grid>
      </Section>
      <Section title="Session & Consumer Persistence">
        <Grid>
          <DI label="Consumer Seq-Map Entries" value={fmtNum(data.consumer_seq_map_entries)} />
          <DI label="Consumer Deletes Dropped" value={fmtNum(data.consumer_deletes_dropped)} />
          <DI label="Consumer Delete Races" value={fmtNum(data.consumer_delete_races)} />
          <DI label="Session Deletes Dropped" value={fmtNum(data.session_deletes_dropped)} />
          <DI label="Persist Failed: Write" value={fmtNum(data.session_persist_failed_write_failed)} />
          <DI label="Persist Failed: Queue Full" value={fmtNum(data.session_persist_failed_queue_full)} />
          <DI label="Persist Failed: Panic" value={fmtNum(data.session_persist_panics)} />
        </Grid>
      </Section>
      <Section title="Cluster">
        <Grid>
          <DI label="Inspect Timeouts" value={fmtNum(data.cluster_inspect_timeouts)} />
          <DI label="Takeover Dropped" value={fmtNum(data.cluster_takeover_dropped)} />
          <DI label="Takeover Order Skew" value={fmtNum(data.cluster_takeover_order_skew)} />
          <DI label="Owned Leases" value={fmtNum(data.cluster_owned_leases)} />
          <DI label="Lease Acquired" value={fmtNum(data.cluster_lease_acquired)} />
          <DI label="Lease Transferred" value={fmtNum(data.cluster_lease_transferred)} />
          <DI label="Lease Reclaimed" value={fmtNum(data.cluster_lease_reclaimed)} />
          <DI label="Lease Conflicts" value={fmtNum(data.cluster_lease_conflicts)} />
          <DI label="Lease Watcher Kicks" value={fmtNum(data.cluster_lease_watcher_kicks)} />
          <DI label="Lease Release Failed" value={fmtNum(data.cluster_lease_release_failed)} />
          <DI label="Lease Revision Regressions" value={fmtNum(data.cluster_lease_revision_regressions)} />
          <DI label="Heartbeat Publish Failures" value={fmtNum(data.cluster_heartbeat_publish_failures)} />
          <DI label="Session Fencing Rejected" value={fmtNum(data.session_fencing_rejected)} />
        </Grid>
        <p className="text-xs text-gray-400 mt-2">Cluster counters and owned/lease fields are populated only when clustering is enabled. Rising <span className="font-mono">order skew</span> signals inter-node clock divergence; rising <span className="font-mono">lease conflicts</span> or <span className="font-mono">watcher kicks</span> indicate contested session ownership across instances. Any non-zero <span className="font-mono">revision regressions</span> means the session-ownership bucket was rebuilt and stale owners can no longer be fenced — restart the affected instances.</p>
      </Section>
      <Section title="JetStream Health">
        <Grid>
          <DI label="Session Write-Behind Depth" value={fmtNum(data.session_write_behind_depth)} />
          <DI label="Consumer Pending Messages" value={fmtPending(data.consumer_pending_messages ?? -1)} />
          <DI label="Stalled Consumers" value={fmtNum(data.stalled_consumers)} />
        </Grid>
        <p className="text-xs text-gray-400 mt-2">Consumer Pending shows the QoS 1/2 backlog in the MQTT5_msgs stream. Updated every 15 s; shows <em>n/a</em> when JetStream is unavailable.</p>
      </Section>
      <Section title="Latency (averages)">
        <Grid>
          <DI label="Publish" value={fmtMs(data.publish_latency_sum_seconds, data.publish_latency_count)} />
          <DI label="Auth" value={fmtMs(data.auth_duration_sum_seconds, data.auth_duration_count)} />
          <DI label="Auth Webhook" value={fmtMs(data.auth_webhook_duration_sum_seconds, data.auth_webhook_duration_count)} />
          <DI label="JetStream Publish" value={fmtMs(data.jetstream_publish_duration_sum_seconds, data.jetstream_publish_duration_count)} />
          <DI label="QoS 2 Sync Persist" value={fmtMs(data.qos2_sync_persist_duration_sum_seconds, data.qos2_sync_persist_duration_count)} />
          <DI label="Subscribe" value={fmtMs(data.subscribe_duration_sum_seconds, data.subscribe_duration_count)} />
          <DI label="Dispatch Wait" value={fmtMs(data.dispatch_wait_sum_seconds, data.dispatch_wait_count)} />
          <DI label="TLS Handshake" value={fmtMs(data.tls_handshake_duration_sum_seconds, data.tls_handshake_duration_count)} />
        </Grid>
        <p className="text-xs text-gray-400 mt-2">All values are process-lifetime averages (sum/count from histogram). High dispatch wait correlates with <span className="font-mono">pool_full</span> events.</p>
      </Section>
      <Section title="Go Runtime">
        <Grid>
          <DI label="Goroutines" value={fmtNum(data.go_goroutines)} />
          <DI label="Heap In-Use" value={fmtBytes(data.go_heap_inuse_bytes)} />
          <DI label="GC Cycles" value={fmtNum(data.go_gc_cycles)} />
          <DI label="GC Pause Total" value={fmtMs(data.go_gc_pause_ns_total / 1e9, 1)} />
        </Grid>
        <p className="text-xs text-gray-400 mt-2">Heap and GC stats are cached with a 60 s TTL to limit stop-the-world impact.</p>
      </Section>
      {data.instance_id && (
        <Section title="Instance">
          <Grid>
            <DI label="Instance ID" value={data.instance_id} mono />
          </Grid>
        </Section>
      )}
    </div>
  )
}

// eslint-disable-next-line @typescript-eslint/no-explicit-any
function PoolTab({ data }: { data: any }) {
  if (!data || !data.slots) return <Empty msg="Connection pool not available (pool_size may be 0)" />
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  const slots = data.slots as any[]
  const connected = slots.filter((s) => s.connected).length
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  const totalSubs = slots.reduce((a: number, s: any) => a + s.sub_count, 0)
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  const totalPubs = slots.reduce((a: number, s: any) => a + s.pub_count, 0)
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  const totalFlush = slots.reduce((a: number, s: any) => a + s.flush_count, 0)
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  const maxSubs = Math.max(...slots.map((s: any) => s.sub_count), 1)

  return (
    <div className="space-y-6">
      <Section title="Summary">
        <Grid>
          <DI label="Pool Size" value={data.size.toString()} />
          <DI label="Connected" value={`${connected}/${data.size}`} />
          <DI label="Total Subscriptions" value={fmtNum(totalSubs)} />
          <DI label="Total Publishes" value={fmtNum(totalPubs)} />
          <DI label="Total Flushes" value={fmtNum(totalFlush)} />
          <DI label="Avg Subs/Slot" value={(totalSubs / Math.max(data.size, 1)).toFixed(0)} />
        </Grid>
      </Section>

      <Section title="Subscription Distribution">
        <div className="flex items-end gap-px h-20">
          {/* eslint-disable-next-line @typescript-eslint/no-explicit-any */}
          {slots.map((slot: any) => {
            const pct = (slot.sub_count / maxSubs) * 100
            return (
              <div
                key={slot.index}
                className="flex-1 min-w-[4px] rounded-t-sm cursor-default"
                style={{
                  height: `${Math.max(4, pct)}%`,
                  backgroundColor: slot.connected ? '#27aae1' : '#ef4444',
                  opacity: slot.connected ? 0.5 + (pct / 200) : 1,
                }}
                title={`Slot ${slot.index}: ${slot.sub_count.toLocaleString()} subs`}
              />
            )
          })}
        </div>
        <div className="flex justify-between text-[10px] text-gray-400 mt-1">
          <span>Slot 0</span>
          <span>Slot {data.size - 1}</span>
        </div>
      </Section>

      <Section title="All Slots">
        <Table
          headers={['Slot', 'Connected', 'Subscriptions', 'Publishes', 'Flushes']}
          // eslint-disable-next-line @typescript-eslint/no-explicit-any
          rows={slots.map((s: any) => [
            s.index,
            s.connected ? 'Yes' : 'No',
            s.sub_count.toLocaleString(),
            s.pub_count.toLocaleString(),
            s.flush_count.toLocaleString(),
          ])}
        />
      </Section>
    </div>
  )
}

// eslint-disable-next-line @typescript-eslint/no-explicit-any
function ClusterTab({ data, env, bridge }: { data: any; env: string; bridge?: string }) {
  const [clientId, setClientId] = useState('')
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  const [inspect, setInspect] = useState<any>(null)
  const [inspecting, setInspecting] = useState(false)

  const doInspect = async () => {
    if (!clientId.trim() || !bridge) return
    setInspecting(true)
    setInspect(null)
    try {
      const b = encodeURIComponent(bridge)
      const res = await fetchWithTimeout(
        `/api/environments/${env}/mqtt/${b}/cluster/inspect?client_id=${encodeURIComponent(clientId.trim())}`,
      )
      setInspect(res.ok ? await res.json() : { found: false, reason: `request failed (HTTP ${res.status})` })
    } catch {
      setInspect({ found: false, reason: 'request failed' })
    }
    setInspecting(false)
  }

  if (!data || data.available === false) {
    return <Empty msg={data?.reason || 'Cluster information not available'} />
  }
  const c = data.cluster
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  const instances = (c?.instances || []) as any[]
  const hmac = (c?.hmac_failures || {}) as Record<string, number>
  const hmacEntries = Object.entries(hmac)

  return (
    <div className="space-y-6">
      <Section title="Cluster">
        <Grid>
          <DI label="Local Instance" value={c?.local_instance_id || '-'} mono />
          <DI label="Local Connections" value={fmtNum(c?.local_connections || 0)} />
          <DI label="Instances" value={instances.length.toString()} />
          <DI label="Takeover Order Skew" value={fmtNum(c?.takeover_order_skew || 0)} />
        </Grid>
      </Section>

      <Section title="Members">
        <Table
          headers={['Instance', 'Address', 'Clients', 'Started', 'Last Seen']}
          rows={instances.map((i) => [
            i.self ? `${i.instance_id} (self)` : i.instance_id,
            i.addr,
            (i.clients || 0).toLocaleString(),
            i.started_at ? new Date(i.started_at).toLocaleString() : '-',
            fmtLastSeen(i.last_seen_ms),
          ])}
        />
      </Section>

      <Section title="HMAC Failures by Source Instance">
        {hmacEntries.length === 0 ? (
          <div className="text-sm text-gray-400">No HMAC failures recorded — cluster message authentication is healthy.</div>
        ) : (
          <Table
            headers={['Source Instance', 'Failures']}
            rows={hmacEntries.map(([id, n]) => [id, n.toLocaleString()])}
          />
        )}
      </Section>

      <Section title="Inspect Client Across Cluster">
        <div className="flex gap-2 mb-3">
          <input
            value={clientId}
            onChange={(e) => setClientId(e.target.value)}
            onKeyDown={(e) => { if (e.key === 'Enter') doInspect() }}
            placeholder="MQTT client ID"
            className="flex-1 bg-gray-50 dark:bg-gray-700 rounded px-3 py-1.5 text-sm outline-none border border-gray-200 dark:border-gray-600"
          />
          <button
            onClick={doInspect}
            disabled={inspecting || !clientId.trim()}
            className="px-3 py-1.5 text-sm rounded bg-brand-blue text-white disabled:opacity-50"
          >
            {inspecting ? 'Locating…' : 'Inspect'}
          </button>
        </div>
        {inspect && (inspect.found === false
          ? <div className="text-sm text-gray-500 dark:text-gray-400">{inspect.reason || 'Not found'}</div>
          : <InspectResult data={inspect.inspect} />)}
      </Section>
    </div>
  )
}

// eslint-disable-next-line @typescript-eslint/no-explicit-any
function InspectResult({ data }: { data: any }) {
  if (!data) return null
  const client = data.client || {}
  return (
    <div>
      <div className="text-xs text-gray-500 dark:text-gray-400 mb-2">
        Found on instance <span className="font-mono">{data.instance_id}</span>
      </div>
      <Grid>
        {Object.entries(client).map(([k, v]) => (
          <DI key={k} label={k} value={typeof v === 'object' && v !== null ? JSON.stringify(v) : String(v)} />
        ))}
      </Grid>
    </div>
  )
}

function fmtLastSeen(ms: number): string {
  if (ms == null) return '-'
  const stale = ms >= 30_000 ? ' (stale)' : ''
  if (ms < 1000) return 'just now'
  if (ms < 60_000) return `${Math.round(ms / 1000)}s ago${stale}`
  if (ms < 3_600_000) return `${Math.round(ms / 60_000)}m ago${stale}`
  return `${Math.round(ms / 3_600_000)}h ago${stale}`
}

function ActionButton({ label, onClick, busy, danger, disabled, title }: {
  label: string; onClick: () => void; busy: boolean; danger?: boolean; disabled?: boolean; title?: string
}) {
  return (
    <button
      onClick={onClick}
      disabled={disabled || busy}
      title={title}
      className={`px-3 py-1.5 text-sm rounded text-white disabled:opacity-40 disabled:cursor-not-allowed ${danger ? 'bg-red-600 hover:bg-red-700' : 'bg-brand-blue hover:opacity-90'}`}
    >
      {label}
    </button>
  )
}

function AdminTab({ env, bridge, clusterEnabled, onChanged }: {
  env: string; bridge?: string; clusterEnabled: boolean; onChanged: () => void
}) {
  const addToast = useStore((s) => s.addToast)
  const [busy, setBusy] = useState<string | null>(null)
  const [pending, setPending] = useState<{ action: string; label: string; body?: Record<string, string> } | null>(null)
  const [clientId, setClientId] = useState('')
  const [username, setUsername] = useState('')

  const confirm = (action: string, label: string, body?: Record<string, string>) => setPending({ action, label, body })

  const run = async (action: string, label: string, body?: Record<string, string>) => {
    if (!bridge) return
    setPending(null)
    setBusy(action)
    try {
      const b = encodeURIComponent(bridge)
      const res = await fetchWithTimeout(`/api/environments/${env}/mqtt/${b}/admin/${action}`, {
        method: 'POST',
        headers: body ? { 'Content-Type': 'application/json' } : undefined,
        body: body ? JSON.stringify(body) : undefined,
      })
      // eslint-disable-next-line @typescript-eslint/no-explicit-any
      let data: any = null
      try { data = await res.json() } catch { /* some actions return an empty body */ }
      if (res.ok) {
        addToast(`${label}: ${summarizeAction(data)}`, 'success')
        onChanged()
      } else {
        addToast(`${label} failed: ${data?.error || `HTTP ${res.status}`}`, 'error')
      }
    } catch {
      addToast(`${label} failed: network error`, 'error')
    }
    setBusy(null)
  }

  const anyBusy = busy !== null

  return (
    <div className="space-y-6">
      <div className="rounded border border-amber-300 bg-amber-50 px-3 py-2 text-sm text-amber-800 dark:border-amber-800 dark:bg-amber-900/30 dark:text-amber-300">
        These actions disconnect live MQTT clients or change the instance's serving state. Each is also gated on the bridge (<span className="font-mono">allow_kick_endpoint</span> / <span className="font-mono">allow_drain_endpoint</span> / <span className="font-mono">allow_reload_endpoint</span>); a disabled action reports the reason.
      </div>

      {pending && (
        <div className="rounded border border-red-300 bg-red-50 px-3 py-2 text-sm text-red-800 dark:border-red-800 dark:bg-red-900/30 dark:text-red-200 flex items-center justify-between gap-3">
          <span>Confirm: <strong>{pending.label}</strong>?</span>
          <span className="flex gap-2 shrink-0">
            <button onClick={() => run(pending.action, pending.label, pending.body)} className="px-3 py-1 rounded bg-red-600 text-white hover:bg-red-700">Confirm</button>
            <button onClick={() => setPending(null)} className="px-3 py-1 rounded bg-gray-200 dark:bg-gray-600">Cancel</button>
          </span>
        </div>
      )}

      <Section title="This Instance">
        <div className="flex flex-wrap gap-2">
          <ActionButton label="Kick All (local)" danger busy={anyBusy} onClick={() => confirm('kick-all-clients', 'Kick all clients on this instance')} />
          <ActionButton label="Drain" busy={anyBusy} onClick={() => confirm('drain', 'Drain this instance')} />
          <ActionButton label="Undrain" busy={anyBusy} onClick={() => confirm('undrain', 'Undrain this instance')} />
          <ActionButton label="Reload Config" busy={anyBusy} onClick={() => confirm('reload', 'Reload config from disk')} />
        </div>
        <p className="text-xs text-gray-400 mt-2">Drain stops new connections (existing sessions stay) until undrained.</p>
      </Section>

      <Section title="Cluster-wide">
        {!clusterEnabled && (
          <div className="text-xs text-amber-600 dark:text-amber-400 mb-3">Clustering is not enabled on this bridge — cluster-wide kicks are unavailable.</div>
        )}
        <div className="flex flex-wrap items-end gap-4">
          <div>
            <label className="block text-xs text-gray-500 dark:text-gray-400 mb-1">Kick by client ID</label>
            <div className="flex gap-2">
              <input value={clientId} onChange={(e) => setClientId(e.target.value)} placeholder="client ID"
                className="bg-gray-50 dark:bg-gray-700 rounded px-3 py-1.5 text-sm outline-none border border-gray-200 dark:border-gray-600" />
              <ActionButton label="Kick" danger busy={anyBusy} disabled={!clusterEnabled || !clientId.trim()}
                onClick={() => confirm('cluster-kick-client', `Kick client "${clientId.trim()}" across the cluster`, { client_id: clientId.trim() })} />
            </div>
          </div>
          <div>
            <label className="block text-xs text-gray-500 dark:text-gray-400 mb-1">Kick by username</label>
            <div className="flex gap-2">
              <input value={username} onChange={(e) => setUsername(e.target.value)} placeholder="username"
                className="bg-gray-50 dark:bg-gray-700 rounded px-3 py-1.5 text-sm outline-none border border-gray-200 dark:border-gray-600" />
              <ActionButton label="Kick" danger busy={anyBusy} disabled={!clusterEnabled || !username.trim()}
                onClick={() => confirm('cluster-kick-by-username', `Kick username "${username.trim()}" across the cluster`, { username: username.trim() })} />
            </div>
          </div>
          <ActionButton label="Kick All (cluster)" danger busy={anyBusy} disabled={!clusterEnabled}
            onClick={() => confirm('cluster-kick-all', 'Kick ALL clients across the entire cluster')} />
        </div>
      </Section>
    </div>
  )
}

// eslint-disable-next-line @typescript-eslint/no-explicit-any
function summarizeAction(d: any): string {
  if (!d) return 'done'
  if (typeof d.kicked === 'number') return `${d.kicked} kicked`
  if (typeof d.kicked_locally === 'number') return `${d.kicked_locally} kicked locally (broadcast to cluster)`
  if (typeof d.kicked_locally === 'boolean') return `kicked_locally=${d.kicked_locally} (broadcast to cluster)`
  if (d.drained === true) return 'instance draining'
  if (d.drained === false) return 'instance undrained'
  if (d.reloaded) return 'config reloaded'
  return 'done'
}

// Severity styling for the broker's license status string. "tampered" and
// "expired" are the two states that need operator action, so both render danger;
// "grace" is a warning (still licensed, running out); "valid" is healthy. Any
// other/unknown string stays neutral rather than guessing a severity.
const LICENSE_STATUS_TONES: Record<string, string> = {
  tampered: 'text-red-600 dark:text-red-400',
  expired: 'text-red-600 dark:text-red-400',
  grace: 'text-amber-600 dark:text-amber-400',
  valid: 'text-green-600 dark:text-green-400',
}

// Banner copy for the license statuses that need action. Same keys as the tones
// above; anything else gets no banner.
const LICENSE_STATUS_BANNERS: Record<string, { tone: 'danger' | 'warning'; text: string }> = {
  tampered: {
    tone: 'danger',
    text: 'Status "tampered" — investigate a possibly tampered binary. The broker is enforcing reduced limits.',
  },
  expired: {
    tone: 'danger',
    text: 'License expired — the broker is enforcing reduced limits until a valid license is installed.',
  },
  grace: {
    tone: 'warning',
    text: 'License in grace period — renew before it expires; enforcement tightens once grace runs out.',
  },
}

const BANNER_TONES = {
  danger: 'border-red-300 bg-red-50 text-red-800 dark:border-red-800 dark:bg-red-900/30 dark:text-red-300',
  warning: 'border-amber-300 bg-amber-50 text-amber-800 dark:border-amber-800 dark:bg-amber-900/30 dark:text-amber-300',
}

// eslint-disable-next-line @typescript-eslint/no-explicit-any
function LicenseTab({ data }: { data: any }) {
  if (!data) return <Empty msg="License information not available" />
  if (data.available === false) return <Empty msg={data.reason || 'License information not available'} />
  const hasRateLimits =
    data.effective_aggregate_msgs_per_sec || data.aggregate_msgs_per_sec || data.max_client_msgs_per_sec
  const statusKey = typeof data.status === 'string' ? data.status : ''
  const statusBanner = LICENSE_STATUS_BANNERS[statusKey]
  return (
    <div className="space-y-6">
      <Section title="License">
        {statusBanner && (
          <div className={`mb-3 rounded border px-3 py-2 text-sm ${BANNER_TONES[statusBanner.tone]}`}>
            {statusBanner.text}
          </div>
        )}
        {(data.capacity_clamped || data.block_confirmed) && (
          <div className="mb-3 rounded border border-red-300 bg-red-50 px-3 py-2 text-sm text-red-800 dark:border-red-800 dark:bg-red-900/30 dark:text-red-300">
            {data.capacity_clamped
              ? `Capacity clamped — new-connection admission is throttled to the clamp floor${
                  data.clamp_floor ? ` (${data.clamp_floor.toLocaleString()})` : ''
                }.`
              : 'License block confirmed.'}
            {data.block_reason ? ` ${data.block_reason}` : ''}
          </div>
        )}
        {data.degraded && (
          <div className="mb-3 rounded border border-amber-300 bg-amber-50 px-3 py-2 text-sm text-amber-800 dark:border-amber-800 dark:bg-amber-900/30 dark:text-amber-300">
            Degraded — peer discovery is disabled; global connection counts and instance totals may be stale.
            {data.degraded_reason ? ` ${data.degraded_reason}` : ''}
          </div>
        )}
        <Grid>
          <DI label="Status" value={data.status} valueClass={LICENSE_STATUS_TONES[statusKey]} />
          <DI label="License ID" value={data.license_id || '-'} />
          <DI label="Company" value={data.company || '-'} />
          <DI label="Contact" value={data.contact || '-'} />
          <DI label="Email" value={data.email || '-'} />
          <DI label="Kind" value={data.kind || '-'} />
          <DI label="Tier" value={data.tier || '-'} />
          <DI label="Max Connections" value={data.max_connections === 0 ? 'Unlimited' : data.max_connections.toLocaleString()} />
          <DI label="Max QoS" value={data.max_qos.toString()} />
          <DI label="Connections (Local)" value={data.connections_local.toLocaleString()} />
          <DI label="Connections (Global)" value={data.connections_global.toLocaleString()} />
          <DI label="Instances" value={data.instances.toString()} />
          <DI label="Expires At" value={data.expires_at || 'Never'} />
          <DI label="Grace Days" value={data.grace_days?.toString() || '-'} />
          {data.capacity_clamped && (
            <DI label="Clamp Floor" value={data.clamp_floor?.toLocaleString() || '-'} />
          )}
        </Grid>
      </Section>
      {hasRateLimits && (
        <Section title="Rate Limits (Fleet)">
          <Grid>
            <DI label="Effective Aggregate" value={`${(data.effective_aggregate_msgs_per_sec || 0).toLocaleString()} msg/s`} />
            <DI label="License Aggregate" value={`${(data.aggregate_msgs_per_sec || 0).toLocaleString()} msg/s`} />
            <DI label="Aggregate Burst" value={`${(data.aggregate_burst_msgs_per_sec || 0).toLocaleString()} msg/s`} />
            <DI label="Burst Window" value={`${(data.aggregate_burst_window_sec || 0).toLocaleString()} s`} />
            <DI label="Max per Client" value={`${(data.max_client_msgs_per_sec || 0).toLocaleString()} msg/s`} />
          </Grid>
          <p className="text-xs text-gray-400 mt-2">Effective aggregate is the ceiling actually enforced after applying <span className="font-mono">mqtt.max_aggregate_publish_rate</span> tightening (0 = no limit).</p>
        </Section>
      )}
    </div>
  )
}

// eslint-disable-next-line @typescript-eslint/no-explicit-any
function ConfigTab({ data }: { data: any }) {
  if (!data) return <Empty msg="Configuration not available" />
  if (data.available === false) return <Empty msg={data.reason || 'Configuration not available'} />
  return (
    <div className="space-y-4">
      {data.version && (
        <div className="bg-white dark:bg-gray-800 rounded-lg shadow p-4">
          <Grid>
            <DI label="Version" value={data.version} />
            <DI label="Config Path" value={data.config_path} />
          </Grid>
        </div>
      )}
      <div className="bg-white dark:bg-gray-800 rounded-lg shadow p-4">
        <h3 className="font-medium text-sm mb-3">Running Configuration</h3>
        <pre className="bg-gray-50 dark:bg-gray-900 rounded p-4 text-xs font-mono overflow-x-auto max-h-[600px] overflow-y-auto whitespace-pre-wrap">
          {JSON.stringify(data.config, null, 2)}
        </pre>
      </div>
    </div>
  )
}

// Shared components

function Empty({ msg }: { msg: string }) {
  return <div className="bg-white dark:bg-gray-800 rounded-lg shadow p-8 text-center text-gray-500 dark:text-gray-400">{msg}</div>
}

function Section({ title, children }: { title: string; children: React.ReactNode }) {
  return (
    <div className="bg-white dark:bg-gray-800 rounded-lg shadow p-4">
      <h3 className="font-medium text-sm mb-3">{title}</h3>
      {children}
    </div>
  )
}

function Grid({ children }: { children: React.ReactNode }) {
  return <div className="grid grid-cols-2 md:grid-cols-4 lg:grid-cols-6 gap-4 text-sm">{children}</div>
}

// valueClass carries severity styling for values that have one (see the license
// status tones); it is appended so callers can only add to the base classes.
function DI({ label, value, mono, valueClass }: { label: string; value: string; mono?: boolean; valueClass?: string }) {
  return (
    <div className="min-w-0">
      <div className="text-gray-500 dark:text-gray-400 text-xs">{label}</div>
      <div className={`font-medium truncate ${mono ? 'font-mono text-xs' : ''} ${valueClass ?? ''}`} title={value || '-'}>{value || '-'}</div>
    </div>
  )
}

// eslint-disable-next-line @typescript-eslint/no-explicit-any
function Table({ headers, rows }: { headers: string[]; rows: any[][] }) {
  return (
    <div className="overflow-x-auto">
      <table className="w-full text-sm">
        <thead className="bg-gray-50 dark:bg-gray-700 text-left text-gray-500 dark:text-gray-400">
          <tr>{headers.map((h, i) => <th key={i} className="px-3 py-2 whitespace-nowrap">{h}</th>)}</tr>
        </thead>
        <tbody className="divide-y divide-gray-100 dark:divide-gray-700">
          {rows.map((row, i) => (
            <tr key={i} className="hover:bg-gray-50 dark:hover:bg-gray-700/50">
              {row.map((cell, j) => <td key={j} className="px-3 py-1.5 whitespace-nowrap">{cell}</td>)}
            </tr>
          ))}
          {rows.length === 0 && (
            <tr><td colSpan={headers.length} className="px-3 py-4 text-center text-gray-400">None</td></tr>
          )}
        </tbody>
      </table>
    </div>
  )
}
