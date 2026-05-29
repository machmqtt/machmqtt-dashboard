import { useState, useEffect, useCallback, useMemo } from 'react'
import { fetchWithTimeout } from '../utils/fetchWithTimeout'
import { useParams, Link } from 'react-router-dom'
import { useStore } from '../store/store'
import { TableSkeleton } from '../components/Skeleton'
import { ArrowLeft } from 'lucide-react'
import { TimeSeriesChart } from '../components/TimeSeriesChart'
import { TimeRangeSelector } from '../components/TimeRangeSelector'
import { useMetrics } from '../hooks/useMetrics'

type Tab = 'nats' | 'metrics' | 'pool' | 'cluster' | 'license' | 'config'

const REFRESH_INTERVAL = 10_000

export function MQTTBridgeDetailPage() {
  const { bridge } = useParams<{ bridge: string }>()
  const activeEnv = useStore((s) => s.activeEnv)
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
  const [loading, setLoading] = useState(true)

  const bridgeMetricsParams = useMemo(() => (bridge ? { bridge_id: bridge } : undefined), [bridge])
  const bridgeMetrics = useMetrics(activeEnv, 'metrics/mqtt', bridgeMetricsParams)

  const fetchAll = useCallback(async () => {
    if (!activeEnv || !bridge) return
    setLoading(true)
    const b = encodeURIComponent(bridge)
    const base = `/api/environments/${activeEnv}/mqtt/${b}`
    const results = await Promise.allSettled([
      fetchWithTimeout(`${base}/diag`).then(r => r.ok ? r.json() : null),
      fetchWithTimeout(`${base}/metrics`).then(r => r.ok ? r.json() : null),
      fetchWithTimeout(`${base}/pool`).then(r => r.ok ? r.json() : null),
      fetchWithTimeout(`${base}/license`).then(r => r.ok ? r.json() : null),
      fetchWithTimeout(`${base}/diag/config`).then(r => r.ok ? r.json() : null),
      fetchWithTimeout(`${base}/cluster`).then(r => r.ok ? r.json() : null),
    ])
    setNats(results[0].status === 'fulfilled' ? results[0].value : null)
    setMetrics(results[1].status === 'fulfilled' ? results[1].value : null)
    setPool(results[2].status === 'fulfilled' ? results[2].value : null)
    setLicense(results[3].status === 'fulfilled' ? results[3].value : null)
    setDiag(results[4].status === 'fulfilled' ? results[4].value : null)
    setCluster(results[5].status === 'fulfilled' ? results[5].value : null)
    setLoading(false)
  }, [activeEnv, bridge])

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
      </div>

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
            lines={[
              { key: 'connections_active', color: '#a855f7', label: 'Active' },
            ]}
            yFormatter={(v) => v.toFixed(0)}
          />
        </div>
        <div className="bg-white dark:bg-gray-800 rounded-lg shadow p-4">
          <h3 className="text-sm font-medium text-gray-500 dark:text-gray-400 mb-2">Message Rate</h3>
          <TimeSeriesChart
            data={tsMetrics.data}
            lines={[
              { key: 'in_msgs_rate', color: '#22c55e', label: 'In msgs/s' },
              { key: 'out_msgs_rate', color: '#f97316', label: 'Out msgs/s' },
            ]}
            yFormatter={fmtRateAxis}
          />
        </div>
      </div>

      <Section title="Connections">
        <Grid>
          <DI label="Active" value={fmtNum(data.connections_active)} />
          <DI label="Total Accepted" value={fmtNum(data.connections_total)} />
          <DI label="Rejected" value={fmtNum(data.connections_rejected)} />
          <DI label="WS Active" value={fmtNum(data.ws_connections_active)} />
          <DI label="WS Total" value={fmtNum(data.ws_connections_total)} />
        </Grid>
      </Section>
      <Section title="Rejections by Reason">
        <Grid>
          <DI label="Max Conns" value={fmtNum(data.rejected_max_conns)} />
          <DI label="License" value={fmtNum(data.rejected_license)} />
          <DI label="Per-IP Conns" value={fmtNum(data.rejected_per_ip_conns)} />
          <DI label="Per-IP Accept" value={fmtNum(data.rejected_per_ip_accept)} />
          <DI label="Pool Full" value={fmtNum(data.rejected_pool_full)} />
        </Grid>
      </Section>
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
          <DI label="Auth Failure" value={fmtNum(data.auth_failure)} />
        </Grid>
      </Section>
      <Section title="MQTT Messages">
        <Grid>
          <DI label="Recv QoS 0" value={fmtNum(data.msgs_recv_qos0)} />
          <DI label="Recv QoS 1" value={fmtNum(data.msgs_recv_qos1)} />
          <DI label="Recv QoS 2" value={fmtNum(data.msgs_recv_qos2)} />
          <DI label="Sent QoS 0" value={fmtNum(data.msgs_sent_qos0)} />
          <DI label="Sent QoS 1" value={fmtNum(data.msgs_sent_qos1)} />
          <DI label="Sent QoS 2" value={fmtNum(data.msgs_sent_qos2)} />
        </Grid>
      </Section>
      <Section title="Protocol">
        <Grid>
          <DI label="Subscribes" value={fmtNum(data.subscribes)} />
          <DI label="Unsubscribes" value={fmtNum(data.unsubscribes)} />
          <DI label="Keepalive Timeouts" value={fmtNum(data.keepalive_timeouts)} />
        </Grid>
      </Section>
      <Section title="Connection Pool">
        <Grid>
          <DI label="Pool Publishes" value={fmtNum(data.pool_publishes)} />
          <DI label="Pool Subscribes" value={fmtNum(data.pool_subscribes)} />
        </Grid>
      </Section>
      <Section title="NATS">
        <Grid>
          <DI label="Disconnects" value={fmtNum(data.nats_disconnects)} />
          <DI label="Reconnects" value={fmtNum(data.nats_reconnects)} />
        </Grid>
      </Section>
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

// eslint-disable-next-line @typescript-eslint/no-explicit-any
function LicenseTab({ data }: { data: any }) {
  if (!data) return <Empty msg="License information not available" />
  return (
    <Section title="License">
      {data.degraded && (
        <div className="mb-3 rounded border border-amber-300 bg-amber-50 px-3 py-2 text-sm text-amber-800 dark:border-amber-800 dark:bg-amber-900/30 dark:text-amber-300">
          Degraded — peer discovery is disabled; global connection counts and instance totals may be stale.
        </div>
      )}
      <Grid>
        <DI label="Status" value={data.status} />
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
      </Grid>
    </Section>
  )
}

// eslint-disable-next-line @typescript-eslint/no-explicit-any
function ConfigTab({ data }: { data: any }) {
  if (!data) return <Empty msg="Configuration not available" />
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

function DI({ label, value, mono }: { label: string; value: string; mono?: boolean }) {
  return (
    <div>
      <div className="text-gray-500 dark:text-gray-400 text-xs">{label}</div>
      <div className={`font-medium ${mono ? 'font-mono text-xs' : ''}`}>{value || '-'}</div>
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

function fmtRateAxis(r: number): string {
  if (r >= 1e6) return (r / 1e6).toFixed(1) + 'M'
  if (r >= 1e3) return (r / 1e3).toFixed(1) + 'K'
  if (r >= 1) return r.toFixed(0)
  if (r > 0) return r.toFixed(2)
  return '0'
}
