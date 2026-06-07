import { useState, useEffect, useCallback, useMemo } from 'react'
import { fetchWithTimeout } from '../utils/fetchWithTimeout'
import { useParams, Link } from 'react-router-dom'
import { useStore } from '../store/store'
import { TableSkeleton } from '../components/Skeleton'
import { ArrowLeft } from 'lucide-react'
import { TimeSeriesChart } from '../components/TimeSeriesChart'
import { TimeRangeSelector } from '../components/TimeRangeSelector'
import { useMetrics } from '../hooks/useMetrics'

type Tab = 'nats' | 'metrics' | 'pool' | 'cluster' | 'license' | 'config' | 'admin'

const REFRESH_INTERVAL = 10_000

export function MQTTBridgeDetailPage({ role }: { role?: string }) {
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
          <DI label="Auth Failure (total)" value={fmtNum(data.auth_failure)} />
          <DI label="Bad Credentials" value={fmtNum(data.auth_fail_bad_credentials)} />
          <DI label="Enhanced (SCRAM)" value={fmtNum(data.auth_fail_enhanced)} />
          <DI label="Account Locked" value={fmtNum(data.auth_fail_locked)} />
          <DI label="Other" value={fmtNum(data.auth_fail_other)} />
          <DI label="SCRAM Sessions Active" value={fmtNum(data.scram_sessions_active)} />
        </Grid>
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
          <DI label="Pending" value={fmtNum(data.will_pending)} />
          <DI label="Retry Pending" value={fmtNum(data.will_retry_pending)} />
        </Grid>
      </Section>
      <Section title="Protocol">
        <Grid>
          <DI label="Subscribes" value={fmtNum(data.subscribes)} />
          <DI label="Unsubscribes" value={fmtNum(data.unsubscribes)} />
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
          <DI label="Proxy Protocol Errors" value={fmtNum(data.proxy_protocol_errors)} />
          <DI label="WS Upgrade Failures" value={fmtNum(data.ws_upgrade_failures)} />
          <DI label="Flow-Control Overflow" value={fmtNum(data.flowcontrol_overflow)} />
        </Grid>
      </Section>
      <Section title="Durability & DLQ">
        <Grid>
          <DI label="QoS 2 Publish Failed" value={fmtNum(data.qos2_server_publish_failed)} />
          <DI label="QoS 1 Client Send Failed" value={fmtNum(data.qos1_client_send_failed)} />
          <DI label="Server Publish Dropped" value={fmtNum(data.server_publish_dropped)} />
          <DI label="Dead Lettered" value={fmtNum(data.messages_dead_lettered)} />
          <DI label="Poison Terminated" value={fmtNum(data.poison_messages_terminated)} />
          <DI label="DLQ Write Failed" value={fmtNum(data.dead_letter_write_failed)} />
          <DI label="Outbound Queue Dropped" value={fmtNum(data.outbound_queue_dropped)} />
        </Grid>
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
          <DI label="JetStream Publish" value={fmtMs(data.jetstream_publish_duration_sum_seconds, data.jetstream_publish_duration_count)} />
          <DI label="Subscribe" value={fmtMs(data.subscribe_duration_sum_seconds, data.subscribe_duration_count)} />
          <DI label="Dispatch Wait" value={fmtMs(data.dispatch_wait_sum_seconds, data.dispatch_wait_count)} />
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
