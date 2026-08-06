import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import { MemoryRouter, Route, Routes } from 'react-router'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { useStore, type Overview } from '../store/store'
import { AccountsPage } from './AccountsPage'
import { ConnectionsPage } from './ConnectionsPage'
import { JetStreamPage } from './JetStreamPage'
import { MQTTAllConnectionsPage } from './MQTTAllConnectionsPage'
import { MQTTBridgeDetailPage } from './MQTTBridgeDetailPage'
import { MQTTConnectionsPage } from './MQTTConnectionsPage'
import { MQTTOverviewPage } from './MQTTOverviewPage'
import { OverviewPage } from './OverviewPage'
import { ServerDetailPage } from './ServerDetailPage'
import { SubscriptionsPage } from './SubscriptionsPage'
import { TopologyPage } from './TopologyPage'
import { UsersPage } from './UsersPage'

vi.mock('../components/TopologyGraph', () => ({
  TopologyGraphView: () => <div>topology graph rendered</div>,
}))

function json(data: unknown, status = 200) {
  return Promise.resolve(new Response(JSON.stringify(data), { status, headers: { 'Content-Type': 'application/json' } }))
}

function renderPage(element: React.ReactNode, path = '/', pattern = '*') {
  return render(<MemoryRouter initialEntries={[path]}><Routes><Route path={pattern} element={element} /></Routes></MemoryRouter>)
}

describe('page empty, failure, and boundary states', () => {
  beforeEach(() => {
    useStore.setState({ activeEnv: 'test', environments: [{ id: 'test', name: 'Test' }], overview: null, topology: null, health: null, toasts: [] })
  })

  afterEach(() => vi.useRealTimers())

  it('renders overview loading, empty servers, and every topology state', () => {
    const loading = renderPage(<OverviewPage />)
    expect(loading.container.querySelectorAll('.animate-pulse').length).toBeGreaterThan(0)
    loading.unmount()

    useStore.setState({ overview: {
      server_count: 0, healthy_count: 0, connection_count: 0, in_msgs_rate: 0.2, out_msgs_rate: 0,
      in_bytes_rate: 0, out_bytes_rate: 0, subscriptions: 0, js_streams: 0, js_consumers: 0,
      js_messages: 0, js_bytes: 0, servers: [],
    } as Overview })
    const empty = renderPage(<OverviewPage />)
    expect(screen.getByText('No servers discovered yet')).toBeInTheDocument()
    empty.unmount()

    const topologyLoading = renderPage(<TopologyPage />)
    expect(screen.getByRole('heading', { name: 'Cluster Topology' })).toBeInTheDocument()
    topologyLoading.unmount()
    useStore.setState({ topology: { nodes: [], links: [] } })
    const topologyEmpty = renderPage(<TopologyPage />)
    expect(screen.getByText('No servers discovered yet.')).toBeInTheDocument()
    topologyEmpty.unmount()
    useStore.setState({ topology: { nodes: [{ id: 'n1', name: 'one', type: 'server', healthy: true, connections: 0, in_msgs_rate: 0, out_msgs_rate: 0 }], links: [] } })
    renderPage(<TopologyPage />)
    expect(screen.getByText('topology graph rendered')).toBeInTheDocument()
  })

  it('shows missing server and empty account, subscription, and JetStream datasets', async () => {
    vi.spyOn(globalThis, 'fetch').mockImplementation((input) => {
      const url = String(input)
      if (url.includes('/metrics/')) return json({ points: [] })
      if (url.endsWith('/varz')) return json({})
      if (url.endsWith('/accountz')) return json({})
      if (url.endsWith('/subsz')) return json({})
      if (url.endsWith('/jsz')) return json({})
      return json({})
    })
    const server = renderPage(<ServerDetailPage />, '/servers/missing', '/servers/:id')
    expect(await screen.findByText('Server not found.')).toBeInTheDocument()
    server.unmount()
    const accounts = renderPage(<AccountsPage />)
    expect(await screen.findByText('No accounts found.')).toBeInTheDocument()
    accounts.unmount()
    const subs = renderPage(<SubscriptionsPage />)
    expect(await screen.findByText('No subscription data available.')).toBeInTheDocument()
    subs.unmount()
    renderPage(<JetStreamPage />)
    expect(await screen.findByText('No JetStream streams found.')).toBeInTheDocument()
  })

  it('renders alternate account detail and sparse drilldown fields', async () => {
    vi.spyOn(globalThis, 'fetch').mockImplementation((input) => {
      const url = String(input)
      if (url.endsWith('/accountz')) return json({ s1: { accounts: ['A'] } })
      if (url.includes('/accountz/')) return json({ account_name: 'A', is_system: true, expired: true,
        jetstream_enabled: false, leafnode_connections: 1, client_connections: 1, subscriptions: 1 })
      if (url.includes('/connz')) return json({ total: 1, connections: [{ cid: 1, name: '', ip: '', port: 0,
        rtt: '', in_msgs: 2_000_000_000, out_msgs: 2, subscriptions: 0, uptime: '', lang: '', version: '' }] })
      if (url.endsWith('/leafz')) return json({ s1: { leafs: [
        { id: 1, name: '', ip: '', port: 0, account: 'other', rtt: '', in_msgs: 0, out_msgs: 0, subscriptions: 0 },
        { id: 2, name: '', ip: '', port: 0, account: 'A', rtt: '', in_msgs: 2_000_000, out_msgs: 2, subscriptions: 0 },
      ] }, s2: {} })
      if (url.includes('/subsz/detail')) return json({ total: 1, subscriptions: [{ subject: 'foo', queue: '', msgs: 2_000,
        conn_name: '', server_name: '' }] })
      return json({})
    })
    renderPage(<AccountsPage />)
    fireEvent.click(await screen.findByRole('button', { name: 'A' }))
    expect(await screen.findByText('Expired')).toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: /^Client Connections/ }))
    expect(await screen.findByText('Client Connections (1)')).toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: /^Leaf Connections/ }))
    expect(await screen.findByText('Leaf Connections (1)')).toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: /^Subscriptions/ }))
    expect(await screen.findByText('Subscriptions (1)')).toBeInTheDocument()
  })

  it('shows an expanded account with unavailable detail after a non-ok response', async () => {
    vi.spyOn(globalThis, 'fetch').mockImplementation((input) => String(input).endsWith('/accountz')
      ? json({ s1: { accounts: ['A'] } })
      : json({}, 503))
    renderPage(<AccountsPage />)
    fireEvent.click(await screen.findByRole('button', { name: 'A' }))
    expect(await screen.findByText('No detail available.')).toBeInTheDocument()
  })

  it('reports initial connection HTTP and network failures', async () => {
    vi.spyOn(globalThis, 'fetch').mockResolvedValueOnce(new Response(null, { status: 503 }))
    const first = renderPage(<ConnectionsPage />)
    await waitFor(() => expect(useStore.getState().toasts.some((toast) => toast.message === 'Failed to fetch connections')).toBe(true))
    first.unmount()

    vi.mocked(fetch).mockRejectedValueOnce(new Error('offline'))
    renderPage(<ConnectionsPage />)
    await waitFor(() => expect(useStore.getState().toasts.some((toast) => toast.message === 'Network error fetching connections')).toBe(true))
  })

  it('renders empty and unavailable MQTT overview and connection states', async () => {
    vi.spyOn(globalThis, 'fetch').mockImplementation((input) => {
      const url = String(input)
      if (url.includes('/metrics/')) return json({ points: [] })
      if (url.endsWith('/mqtt/bridges')) return json({ bridges: [] })
      return json({})
    })
    const overview = renderPage(<MQTTOverviewPage />)
    expect(await screen.findByText(/No MachMQTT bridges discovered yet/)).toBeInTheDocument()
    overview.unmount()

    vi.mocked(fetch).mockImplementation((input) => {
      const url = String(input)
      if (url.includes('/connz')) return json({ error: 'snapshot disabled', detail: 'enable snapshots' })
      return json({ points: [] })
    })
    const one = renderPage(<MQTTConnectionsPage />, '/mqtt/bridge/connections', '/mqtt/:bridge/connections')
    expect(await screen.findByText('enable snapshots')).toBeInTheDocument()
    one.unmount()

    vi.mocked(fetch).mockImplementation((input) => {
      const url = String(input)
      if (url.endsWith('/mqtt/bridges')) return json({ bridges: [{ configured_name: 'b1', server_name: 'n1', ip: '127.0.0.1' }] })
      if (url.includes('/connz')) return json({ connections: [], total: 0 })
      return json({})
    })
    const all = renderPage(<MQTTAllConnectionsPage />)
    expect(await screen.findByText('No MQTT connections found')).toBeInTheDocument()
    all.unmount()

    vi.mocked(fetch).mockImplementation((input) => String(input).endsWith('/mqtt/bridges') ? Promise.reject(new Error('offline')) : json({}))
    renderPage(<MQTTAllConnectionsPage />)
    expect(await screen.findByText('No MQTT connections found')).toBeInTheDocument()
  })

  it('renders every unavailable bridge diagnostic tab after provider failures', async () => {
    vi.spyOn(globalThis, 'fetch').mockRejectedValue(new Error('offline'))
    renderPage(<MQTTBridgeDetailPage />, '/mqtt/bridge/detail', '/mqtt/:bridge/detail')
    expect(await screen.findByText('NATS diagnostics not available')).toBeInTheDocument()
    const expected = [
      ['Metrics', 'Metrics not available'],
      ['Connection Pool', 'Connection pool not available (pool_size may be 0)'],
      ['License', 'License information not available'],
      ['Config', 'Configuration not available'],
    ]
    for (const [tab, message] of expected) {
      fireEvent.click(screen.getByRole('button', { name: tab }))
      expect(screen.getByText(message)).toBeInTheDocument()
    }
  })

  it('renders degraded MQTT bridges and diagnostic default branches', async () => {
    vi.spyOn(globalThis, 'fetch').mockImplementation((input) => {
      const url = String(input)
      if (url.includes('/metrics/')) return json({ points: [] })
      if (url.endsWith('/mqtt/bridges')) return json({ bridges: [
        { ip: '1', server_name: 'n1', configured_name: '', admin_url: '', reachable: true, pool_connections: 4,
          total_subs: 0, total_in_msgs: 0, total_out_msgs: 0, total_in_bytes: 0, total_out_bytes: 0,
          in_msgs_rate: 2_000_000_000, out_msgs_rate: 2, in_bytes_rate: 2_000_000, out_bytes_rate: 2 },
        { ip: '2', server_name: 'n2', configured_name: '', admin_url: 'http://admin', reachable: true,
          pool_connections: 0, total_subs: 0, total_in_msgs: 0, total_out_msgs: 0, total_in_bytes: 0, total_out_bytes: 0,
          in_msgs_rate: 0, out_msgs_rate: 0.25, in_bytes_rate: 0, out_bytes_rate: 0,
          status: { name: 'degraded', ready: false, nats_connected: false, connz_available: false, connections: 0,
            error: 'diagnostic error', metrics: { connections_total: 2_000_000_000, connections_rejected: 2_000_000,
              ws_connections_active: 0, ws_connections_total: 0, auth_success: 0, auth_failure: 0,
              msgs_recv_qos0: 0, msgs_recv_qos1: 0, msgs_recv_qos2: 0, msgs_sent_qos0: 0,
              msgs_sent_qos1: 0, msgs_sent_qos2: 0, subscribes: 0, unsubscribes: 0,
              keepalive_timeouts: 0, pool_publishes: 0, pool_subscribes: 0, nats_disconnects: 0, nats_reconnects: 0 },
            nats: { connection: { server_name: '', url: 'nats://fallback', cluster_name: '', rtt: '', reconnects: 0 },
              streams: [], kv_buckets: [] },
            pool: { size: 2, slots: [{ index: 0, connected: false, sub_count: 0, pub_count: 0, flush_count: 0 }] },
          } },
      ] })
      if (url.endsWith('/diag')) return json({
        connection: { connected: false, reconnecting: true, draining: true, url: '', server_name: '', server_version: '',
          cluster_name: '', rtt: '', max_payload: 2_000_000_000, subscriptions: 0, reconnects: 0,
          in_msgs: 2_000_000_000, out_msgs: 2_000_000, in_bytes: 2_000_000, out_bytes: 2_000_000_000,
          server_id: '', servers: [] },
        account: { domain: '', memory_bytes: 0, store_bytes: 0, streams: 0, consumers: 0 },
        streams: [{ name: 'empty', messages: 0, bytes: 0, consumers: 0, num_subjects: 0, first_seq: 0, last_seq: 0, created: '', error: '' }],
        kv_buckets: [{ bucket: 'empty', values: 0, bytes: 0, ttl: '', error: '' }],
      })
      if (url.endsWith('/metrics')) return json({
        connections_active: 0, connections_total: 2_000_000_000, connections_rejected: 2_000_000,
        ws_connections_active: 0, ws_connections_total: 0, auth_success: 0, auth_failure: 0,
        msgs_recv_qos0: 0, msgs_recv_qos1: 0, msgs_recv_qos2: 0, msgs_sent_qos0: 0,
        msgs_sent_qos1: 0, msgs_sent_qos2: 0, subscribes: 0, unsubscribes: 0, keepalive_timeouts: 0,
        pool_publishes: 0, pool_subscribes: 0, nats_disconnects: 0, nats_reconnects: 0,
      })
      if (url.endsWith('/pool')) return json({ size: 1, slots: [{ index: 0, connected: false, sub_count: 0, pub_count: 0, flush_count: 0 }] })
      if (url.endsWith('/license')) return json({ status: '', license_id: '', company: '', contact: '', email: '', kind: '', tier: '',
        max_connections: 5, max_qos: 0, connections_local: 0, connections_global: 0, instances: 0, expires_at: '', grace_days: 0 })
      if (url.endsWith('/diag/config')) return json({ version: '', config_path: '', config: null })
      return json({})
    })
    const overview = renderPage(<MQTTOverviewPage />)
    expect(await screen.findByText('diagnostic error')).toBeInTheDocument()
    expect(screen.getByText('nats://fallback')).toBeInTheDocument()
    overview.unmount()

    renderPage(<MQTTBridgeDetailPage />, '/mqtt/degraded/detail', '/mqtt/:bridge/detail')
    expect(await screen.findByText('JetStream Account')).toBeInTheDocument()
    for (const tab of ['Metrics', 'Connection Pool', 'License', 'Config']) {
      fireEvent.click(screen.getByRole('button', { name: tab }))
    }
    expect(screen.getByText('Running Configuration')).toBeInTheDocument()
  })

  it('validates user forms and reports API failures', async () => {
    vi.spyOn(globalThis, 'fetch')
      .mockImplementationOnce(() => json({ users: [] }))
      .mockImplementationOnce(() => json({ error: 'duplicate user' }, 409))
      .mockImplementationOnce(() => json({ users: [] }))
    renderPage(<UsersPage />)
    expect(await screen.findByRole('heading', { name: 'User Management' })).toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: 'Create User' }))
    fireEvent.change(screen.getByPlaceholderText('username'), { target: { value: 'duplicate' } })
    fireEvent.change(screen.getByPlaceholderText('password'), { target: { value: 'long-enough' } })
    fireEvent.click(screen.getByRole('button', { name: 'Create' }))
    await waitFor(() => expect(useStore.getState().toasts.some((toast) => toast.message === 'duplicate user')).toBe(true))
  })

  it('handles unavailable user inventory and error responses without messages', async () => {
    vi.spyOn(globalThis, 'fetch')
      .mockImplementationOnce(() => json({}, 503))
      .mockImplementationOnce(() => json({}))
      .mockImplementationOnce(() => json({}, 400))
    const unavailable = renderPage(<UsersPage />)
    expect(await screen.findByRole('heading', { name: 'User Management' })).toBeInTheDocument()
    unavailable.unmount()

    renderPage(<UsersPage />)
    expect(await screen.findByRole('heading', { name: 'User Management' })).toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: 'Create User' }))
    fireEvent.change(screen.getByPlaceholderText('username'), { target: { value: 'new-user' } })
    fireEvent.change(screen.getByPlaceholderText('password'), { target: { value: 'long-enough' } })
    fireEvent.click(screen.getByRole('button', { name: 'Create' }))
    await waitFor(() => expect(useStore.getState().toasts.at(-1)?.message).toBe('Failed to create user'))
  })

  it('does not start fetches when no environment is active', () => {
    useStore.setState({ activeEnv: '' })
    const fetchMock = vi.spyOn(globalThis, 'fetch')
    const views = [<AccountsPage />, <ConnectionsPage />, <SubscriptionsPage />, <JetStreamPage />, <MQTTOverviewPage />, <MQTTAllConnectionsPage />]
    for (const view of views) {
      const rendered = renderPage(view)
      rendered.unmount()
    }
    expect(fetchMock).not.toHaveBeenCalled()
  })
})
