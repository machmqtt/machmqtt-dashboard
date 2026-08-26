import { fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { MemoryRouter, Route, Routes } from 'react-router'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { useStore } from '../store/store'
import { MQTTBridgeDetailPage } from './MQTTBridgeDetailPage'

vi.mock('../components/TimeSeriesChart', () => ({
  TimeSeriesChart: ({ yFormatter }: { yFormatter?: (value: number) => string }) => (
    <div data-testid="time-series-chart">{yFormatter?.(1234) ?? 'chart'}</div>
  ),
}))

function json(data: unknown, status = 200) {
  return Promise.resolve(new Response(JSON.stringify(data), {
    status,
    headers: { 'Content-Type': 'application/json' },
  }))
}

function renderPage(role = 'admin') {
  return render(
    <MemoryRouter initialEntries={['/mqtt/bridge%20one/detail']}>
      <Routes><Route path="/mqtt/:bridge/detail" element={<MQTTBridgeDetailPage role={role} />} /></Routes>
    </MemoryRouter>,
  )
}

const nats = {
  connection: {
    connected: true, reconnecting: false, draining: false, url: 'nats://one', server_name: 'n1',
    server_version: '2.12', cluster_name: 'c1', rtt: '1ms', max_payload: 1024, subscriptions: 2,
    reconnects: 1, in_msgs: 2, out_msgs: 3, in_bytes: 4, out_bytes: 5, server_id: 'server-id',
    servers: ['nats://one', 'nats://two'],
  },
  account: { domain: 'D', memory_bytes: 10, store_bytes: 20, streams: 1, consumers: 2 },
  streams: [{ name: 'ORDERS', messages: 3, bytes: 40, consumers: 1, num_subjects: 2, first_seq: 1, last_seq: 3, created: '2026-01-01T00:00:00Z' }],
  kv_buckets: [{ bucket: 'SESSIONS', values: 2, bytes: 10, ttl: '1h' }],
}

const metrics = new Proxy({
  drained: 1,
  consumer_pending_messages: -1,
  auth_latency_seconds_sum: 2,
  auth_latency_seconds_count: 1,
  publish_latency_seconds_sum: 0.0002,
  publish_latency_seconds_count: 1,
  connack_rejected_by_reason: { '0x88': 2 },
  suback_rejected_by_reason: { '0x87': 1 },
  disconnects_sent_by_reason: { '0x93': 1 },
  instance_id: 'i1',
  uncurated: { 'new_metric{label="x"}': 4 },
  uncurated_help: { new_metric: 'A newly observed metric' },
}, { get: (target, key) => Reflect.get(target, key) ?? 1 })

const pool = {
  size: 4,
  slots: [
    { index: 0, connected: true, sub_count: 0, pub_count: 1, flush_count: 2 },
    { index: 1, connected: true, sub_count: 5, pub_count: 2, flush_count: 3 },
    { index: 2, connected: false, sub_count: 1, pub_count: 0, flush_count: 0 },
  ],
}

const cluster = {
  available: true,
  cluster: {
    local_instance_id: 'i1', local_connections: 3, takeover_order_skew: 2,
    instances: [
      { instance_id: 'i1', self: true, addr: 'one', clients: 1, started_at: '2026-01-01T00:00:00Z', last_seen_ms: 500 },
      { instance_id: 'i2', addr: 'two', clients: 2, last_seen_ms: 35_000 },
      { instance_id: 'i3', addr: 'three', clients: 3, last_seen_ms: 120_000 },
      { instance_id: 'i4', addr: 'four', clients: 4, last_seen_ms: 7_200_000 },
    ],
    hmac_failures: { i2: 2 },
  },
}

const license = {
  available: true, status: 'expired', license_id: 'L1', company: 'Example', contact: 'Admin',
  email: 'admin@example.com', kind: 'enterprise', tier: 'pro', max_connections: 100, max_qos: 2,
  connections_local: 3, connections_global: 5, instances: 2, expires_at: '2026-01-01', grace_days: 7,
  capacity_clamped: true, clamp_floor: 10, block_reason: 'expired', degraded: true,
  degraded_reason: 'peer unavailable', effective_aggregate_msgs_per_sec: 50,
  aggregate_msgs_per_sec: 100, aggregate_burst_msgs_per_sec: 200, aggregate_burst_window_sec: 2,
  max_client_msgs_per_sec: 20,
}

describe('MQTTBridgeDetailPage', () => {
  beforeEach(() => useStore.setState({ activeEnv: 'prod', toasts: [] }))

  it('renders every rich diagnostic tab and performs cluster/admin operations', async () => {
    const fetchMock = vi.spyOn(globalThis, 'fetch').mockImplementation((input, init) => {
      const url = String(input)
      if (url.includes('/metrics/mqtt')) return json({ points: [{ ts: 1, connections_active: 2, go_heap_inuse_bytes: 10 }] })
      if (url.includes('/cluster/inspect')) return json({ found: true, inspect: { instance_id: 'i2', client: { id: 'client-1', nested: { ok: true } } } })
      if (url.includes('/admin/')) {
        if (url.endsWith('/drain')) return json({ drained: true })
        if (url.endsWith('/undrain')) return json({ drained: false })
        if (url.endsWith('/reload')) return json({ reloaded: true })
        if (url.endsWith('/cluster-kick-client')) return json({ kicked: 1 })
        if (url.endsWith('/cluster-kick-by-username')) return json({ kicked_locally: 2 })
        return json({ kicked_locally: true })
      }
      if (url.endsWith('/diag/config')) return json({ version: '1.2.3', config_path: '/etc/mqtt.yaml', config: { listener: { port: 1883 } } })
      if (url.endsWith('/diag')) return json(nats)
      if (url.endsWith('/metrics')) return json(metrics)
      if (url.endsWith('/pool')) return json(pool)
      if (url.endsWith('/license')) return json(license)
      if (url.endsWith('/cluster')) return json(cluster)
      if (url.endsWith('/readyz')) return json({ jetstream_degraded: true })
      return json({ method: init?.method }, 404)
    })

    renderPage()
    expect(await screen.findByText('JetStream Account')).toBeInTheDocument()
    expect(screen.getAllByText('Draining').length).toBeGreaterThan(0)
    expect(screen.getByText('JS Degraded')).toBeInTheDocument()
    expect(screen.getByText('ORDERS')).toBeInTheDocument()
    expect(screen.getByText('SESSIONS')).toBeInTheDocument()

    fireEvent.click(screen.getByRole('button', { name: 'Metrics' }))
    expect(await screen.findByText('Sockets (raw transport accepts)')).toBeInTheDocument()
    expect(screen.getByText('n/a (JS unavailable)')).toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: '24h' }))

    fireEvent.click(screen.getByRole('button', { name: 'Connection Pool' }))
    expect(await screen.findByText('Subscription Distribution')).toBeInTheDocument()
    expect(screen.getByText('All Slots')).toBeInTheDocument()

    fireEvent.click(screen.getByRole('button', { name: 'Cluster' }))
    expect(await screen.findByText('HMAC Failures by Source Instance')).toBeInTheDocument()
    const inspectInput = screen.getByPlaceholderText('MQTT client ID')
    fireEvent.keyDown(inspectInput, { key: 'Enter' })
    fireEvent.change(inspectInput, { target: { value: 'client-1' } })
    fireEvent.keyDown(inspectInput, { key: 'Enter' })
    expect(await screen.findByText('Found on instance')).toBeInTheDocument()
    expect(screen.getByText('{"ok":true}')).toBeInTheDocument()

    fireEvent.click(screen.getByRole('button', { name: 'License' }))
    expect(await screen.findByText(/License expired/)).toBeInTheDocument()
    expect(screen.getByText(/Capacity clamped/)).toBeInTheDocument()
    expect(screen.getByText('Rate Limits (Fleet)')).toBeInTheDocument()

    fireEvent.click(screen.getByRole('button', { name: 'Config' }))
    expect(await screen.findByText('Running Configuration')).toBeInTheDocument()
    expect(screen.getByText(/1883/)).toBeInTheDocument()

    fireEvent.click(screen.getByRole('button', { name: 'Admin' }))
    expect(await screen.findByText('This Instance')).toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: 'Drain' }))
    fireEvent.click(screen.getByRole('button', { name: 'Cancel' }))
    fireEvent.click(screen.getByRole('button', { name: 'Drain' }))
    fireEvent.click(screen.getByRole('button', { name: 'Confirm' }))
    await waitFor(() => expect(useStore.getState().toasts.at(-1)?.message).toContain('instance draining'))

    const clientID = screen.getByPlaceholderText('client ID')
    fireEvent.change(clientID, { target: { value: 'client-1' } })
    const clientKick = within(clientID.parentElement!).getByRole('button', { name: 'Kick' })
    fireEvent.click(clientKick)
    fireEvent.click(screen.getByRole('button', { name: 'Confirm' }))
    await waitFor(() => expect(fetchMock).toHaveBeenCalledWith(expect.stringContaining('cluster-kick-client'), expect.objectContaining({ method: 'POST' })))

    const username = screen.getByPlaceholderText('username')
    fireEvent.change(username, { target: { value: 'operator' } })
    fireEvent.click(within(username.parentElement!).getByRole('button', { name: 'Kick' }))
    fireEvent.click(screen.getByRole('button', { name: 'Confirm' }))
    await waitFor(() => expect(fetchMock).toHaveBeenCalledWith(expect.stringContaining('cluster-kick-by-username'), expect.objectContaining({ method: 'POST' })))

    fireEvent.click(screen.getByRole('button', { name: 'Kick All (local)' }))
    fireEvent.click(screen.getByRole('button', { name: 'Confirm' }))
    await waitFor(() => expect(fetchMock).toHaveBeenCalledWith(expect.stringContaining('kick-all-clients'), expect.objectContaining({ method: 'POST' })))

    fireEvent.click(screen.getByRole('button', { name: 'Kick All (cluster)' }))
    fireEvent.click(screen.getByRole('button', { name: 'Confirm' }))
    await waitFor(() => expect(fetchMock).toHaveBeenCalledWith(expect.stringContaining('cluster-kick-all'), expect.objectContaining({ method: 'POST' })))
  })

  it('shows unavailable states, total load failure, and failed admin/inspect requests', async () => {
    let phase = 'empty'
    vi.spyOn(globalThis, 'fetch').mockImplementation((input) => {
      const url = String(input)
      if (url.includes('/metrics/mqtt')) return json({ points: [] })
      if (url.includes('/cluster/inspect')) return phase === 'network' ? Promise.reject(new Error('offline')) : json({}, 503)
      if (url.includes('/admin/')) return phase === 'network' ? Promise.reject(new Error('offline')) : json({ error: 'disabled' }, 403)
      if (phase === 'all-fail') return Promise.reject(new Error('offline'))
      if (url.endsWith('/cluster')) return json({ available: false, reason: 'cluster disabled' })
      if (url.endsWith('/license')) return json({ available: false, reason: 'license disabled' })
      if (url.endsWith('/diag/config')) return json({ available: false, reason: 'config disabled' })
      if (url.endsWith('/readyz')) return json({})
      return json(null)
    })

    const view = renderPage('admin')
    expect(await screen.findByText('NATS diagnostics not available')).toBeInTheDocument()
    for (const [tab, text] of [
      ['Metrics', 'Metrics not available'], ['Connection Pool', 'Connection pool not available (pool_size may be 0)'],
      ['Cluster', 'cluster disabled'], ['License', 'license disabled'], ['Config', 'config disabled'],
    ]) {
      fireEvent.click(screen.getByRole('button', { name: tab }))
      expect(await screen.findByText(text)).toBeInTheDocument()
    }

    fireEvent.click(screen.getByRole('button', { name: 'Admin' }))
    fireEvent.click(screen.getByRole('button', { name: 'Reload Config' }))
    fireEvent.click(screen.getByRole('button', { name: 'Confirm' }))
    await waitFor(() => expect(useStore.getState().toasts.at(-1)?.message).toContain('disabled'))
    phase = 'network'
    fireEvent.click(screen.getByRole('button', { name: 'Undrain' }))
    fireEvent.click(screen.getByRole('button', { name: 'Confirm' }))
    await waitFor(() => expect(useStore.getState().toasts.at(-1)?.message).toContain('network error'))
    view.unmount()

    phase = 'all-fail'
    renderPage('viewer')
    expect(await screen.findByText(/Could not load any details/)).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Admin' })).not.toBeInTheDocument()
  })

  it('renders the JetStream health pair unhealthy arms (#164)', async () => {
    // Plain object, not the rich test's Proxy: JSON.stringify serializes only
    // explicit keys anyway, and these four are exactly the arms under test.
    const unhealthyMetrics = {
      jetstream_available: 0,
      jetstream_transitions: 5,
      op_queue_dropped_worker_abort: 0,
      retained_delivery_truncated: 0,
      connack_rejected_by_reason: {}, suback_rejected_by_reason: {}, disconnects_sent_by_reason: {},
      uncurated: {}, uncurated_help: {},
    }
    vi.spyOn(globalThis, 'fetch').mockImplementation((input) => {
      const url = String(input)
      if (url.includes('/metrics/mqtt')) return json({ points: [] })
      if (url.endsWith('/diag')) return json(nats)
      if (url.endsWith('/metrics')) return json(unhealthyMetrics)
      if (url.endsWith('/pool')) return json(pool)
      if (url.endsWith('/license')) return json(license)
      if (url.endsWith('/cluster')) return json(cluster)
      if (url.endsWith('/readyz')) return json({})
      return json({}, 404)
    })

    renderPage('viewer')
    await screen.findByText('JetStream Account')
    fireEvent.click(screen.getByRole('button', { name: 'Metrics' }))
    // Live gauge: 0 must render the Unavailable arm with the alert class;
    // flips at 5 (>2) takes the flapping-amber arm. Both are the opposite
    // arms from the rich-render test, whose fixture leaves these fields at 1
    // or absent.
    expect(await screen.findByText('Unavailable')).toBeInTheDocument()
    expect(screen.getByText('JetStream Flips')).toBeInTheDocument()
    expect(screen.getByText('Dropped: Worker Abort')).toBeInTheDocument()
  })

  it('renders sparse but valid provider payloads using safe defaults', async () => {
    vi.spyOn(globalThis, 'fetch').mockImplementation((input) => {
      const url = String(input)
      if (url.includes('/metrics/mqtt')) return json({ points: [] })
      if (url.endsWith('/diag')) return json({ connection: { connected: true, servers: [] }, streams: [], kv_buckets: [] })
      if (url.endsWith('/metrics')) return json({
        consumer_pending_messages: 0,
        connack_rejected_by_reason: {}, suback_rejected_by_reason: {}, disconnects_sent_by_reason: {},
        uncurated: {}, uncurated_help: {},
      })
      if (url.endsWith('/pool')) return json({ size: 0, slots: [] })
      if (url.endsWith('/cluster')) return json({ available: true, cluster: { instances: [], hmac_failures: {} } })
      if (url.endsWith('/license')) return json({
        available: true, status: 'grace', max_connections: 0, max_qos: 0,
        connections_local: 0, connections_global: 0, instances: 0, grace_days: 0,
        capacity_clamped: false, block_confirmed: true, degraded: false,
      })
      if (url.endsWith('/diag/config')) return json({ config: null })
      if (url.endsWith('/readyz')) return json({})
      return json({}, 404)
    })

    renderPage('viewer')
    expect(await screen.findByText(/JetStream unavailable/)).toBeInTheDocument()
    expect(screen.getAllByText('No').length).toBeGreaterThan(0)

    fireEvent.click(screen.getByRole('button', { name: 'Metrics' }))
    expect(await screen.findByText('Connections (established MQTT)')).toBeInTheDocument()
    expect(screen.queryByText('CONNACK Rejections by Reason Code')).not.toBeInTheDocument()

    fireEvent.click(screen.getByRole('button', { name: 'Connection Pool' }))
    expect(await screen.findByText('Subscription Distribution')).toBeInTheDocument()
    expect(screen.getByText('None')).toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: 'Cluster' }))
    expect(await screen.findByText(/No HMAC failures recorded/)).toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: 'License' }))
    expect(await screen.findByText(/License in grace period/)).toBeInTheDocument()
    expect(screen.getByText('License block confirmed.')).toBeInTheDocument()
    expect(screen.getByText('Unlimited')).toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: 'Config' }))
    expect(await screen.findByText('Running Configuration')).toBeInTheDocument()
  })
})
