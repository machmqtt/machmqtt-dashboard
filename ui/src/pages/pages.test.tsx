import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import { MemoryRouter, Route, Routes } from 'react-router'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { useStore, type Overview } from '../store/store'
import { OverviewPage } from './OverviewPage'
import { ConnectionsPage } from './ConnectionsPage'
import { SubscriptionsPage } from './SubscriptionsPage'
import { AccountsPage } from './AccountsPage'
import { JetStreamPage } from './JetStreamPage'
import { MQTTOverviewPage } from './MQTTOverviewPage'
import { MQTTConnectionsPage } from './MQTTConnectionsPage'
import { MQTTAllConnectionsPage } from './MQTTAllConnectionsPage'
import { MQTTBridgeDetailPage } from './MQTTBridgeDetailPage'
import { ServerDetailPage } from './ServerDetailPage'
import { UsersPage } from './UsersPage'

const client = {
  cid: 7, mqtt_client: 'client-1', name: 'client-1', kind: 'Client', type: 'mqtt', ip: '127.0.0.1', port: 4222,
  account: 'A', authorized_user: 'alice', username: 'alice', rtt: '1ms', state: 'active', in_msgs: 1_200,
  out_msgs: 2_300_000, in_bytes: 1_500, out_bytes: 2_500_000, subscriptions: 2,
  subscriptions_list: ['foo', 'bar'], uptime: '1h', idle: '1s', lang: 'go', version: '1.0', tls_version: '1.3',
  tls_cipher_suite: 'cipher', pending_bytes: 3, inflight_out: 4, clean_start: true, keep_alive: 30,
  session_expiry_interval: 60, receive_maximum: 10, is_websocket: true, start: '', last_activity: '',
}

const bridge = {
  ip: '127.0.0.1', server_id: 's1', server_name: 'server-one', configured_name: 'bridge-one', admin_url: 'http://bridge',
  reachable: true, pool_connections: 1, total_subs: 2, total_in_msgs: 3, total_out_msgs: 4,
  total_in_bytes: 5, total_out_bytes: 6, in_msgs_rate: 1_200, out_msgs_rate: 2_300_000,
  in_bytes_rate: 1_500, out_bytes_rate: 2_500_000,
  status: {
    name: 'bridge-one', url: 'http://bridge', ready: true, connections: 2, nats_connected: true, connz_available: true,
    total_connections: 10, nats: { connection: { connected: true, url: 'nats://one', server_name: 'one', server_version: '2', cluster_name: 'c', rtt: '1ms', in_msgs: 1, out_msgs: 2, in_bytes: 3, out_bytes: 4, reconnects: 0 }, streams: [{ name: 'S', messages: 1, bytes: 1000, consumers: 1 }], kv_buckets: [{ bucket: 'K', values: 1, bytes: 1000 }] },
    pool: { size: 1, slots: [{ index: 0, connected: true, sub_count: 1, pub_count: 2, flush_count: 3 }] },
    metrics: { connections_active: 2, connections_total: 3, connections_rejected: 1, ws_connections_active: 1, ws_connections_total: 2, auth_success: 3, auth_failure: 1, msgs_recv_qos0: 1, msgs_recv_qos1: 2, msgs_recv_qos2: 3, msgs_sent_qos0: 4, msgs_sent_qos1: 5, msgs_sent_qos2: 6, subscribes: 7, unsubscribes: 8, keepalive_timeouts: 1, pool_publishes: 2, pool_subscribes: 3, nats_disconnects: 1, nats_reconnects: 2 },
  },
}

const varz = { server_id: 's1', server_name: 'one', version: '2.11', host: 'localhost', port: 4222, go: 'go1', max_connections: 100, connections: 2, total_connections: 3, routes: 1, leafnodes: 1, in_msgs: 1_200, out_msgs: 2_300_000, in_bytes: 1_500, out_bytes: 2_500_000, mem: 2_500_000_000, cpu: 2.5, cores: 4, subscriptions: 2, slow_consumers: 1, uptime: '1h' }

function fixture(url: string) {
  if (url.includes('/metrics/')) return { points: [{ ts: 1, cpu: 2, mem: 3, in_msgs_rate: 4, out_msgs_rate: 5, connection_count: 6, connections_active: 7 }] }
  if (url.endsWith('/mqtt/bridges')) return { bridges: [bridge, { ...bridge, ip: '127.0.0.2', configured_name: '', reachable: false, status: { ...bridge.status, name: '', ready: false, nats_connected: false, connz_available: false } }] }
  if (url.includes('/mqtt/') && url.includes('/connz')) return { server_id: 's1', connections: [client], num_connections: 1, total: 120, limit: 50, offset: 0 }
  if (url.endsWith('/diag/config')) return { version: '1.2', config: { listener: 'mqtt', nested: { enabled: true } } }
  if (url.endsWith('/diag')) return { connection: bridge.status.nats.connection, streams: bridge.status.nats.streams, kv_buckets: bridge.status.nats.kv_buckets }
  if (url.endsWith('/pool')) return bridge.status.pool
  if (url.endsWith('/license')) return { status: 'valid', license_id: 'L1', company: 'Example', contact: 'Admin', email: 'a@example.com', kind: 'enterprise', tier: 'pro', max_connections: 0, max_qos: 2, connections_local: 2, connections_global: 3, instances: 1, expires_at: '2030-01-01', grace_days: 7 }
  if (url.includes('/mqtt/') && url.endsWith('/metrics')) return bridge.status.metrics
  if (url.endsWith('/varz')) return { s1: varz }
  if (url.endsWith('/jsz')) return { s1: { streams: 2, consumers: 2, messages: 1200, bytes: 2_500_000, memory: 100, storage: 200, account_details: [
    { name: 'A', memory: 1, storage: 2, stream_detail: [{ name: 'ORDERS', config: { subjects: ['orders.*'], retention: 'limits', storage: 'file', num_replicas: 3 }, state: { messages: 10, bytes: 1500, consumer_count: 1, first_seq: 1, last_seq: 10 }, consumer_detail: [{ stream_name: 'ORDERS', name: 'worker', config: { durable_name: 'worker', filter_subject: 'orders.new', deliver_policy: 'all', ack_policy: 'explicit' }, delivered: { consumer_seq: 3, stream_seq: 3 }, ack_floor: { consumer_seq: 2, stream_seq: 2 }, num_ack_pending: 1, num_redelivered: 1, num_pending: 7 }] }] },
    { name: 'B', memory: 0, storage: 0, stream_detail: [{ name: 'EMPTY', config: { retention: 'limits', storage: 'memory', num_replicas: 1 }, state: { messages: 0, bytes: 0, consumer_count: 0, first_seq: 0, last_seq: 0 } }] },
  ] } }
  if (url.endsWith('/accountz')) return { s1: { server_id: 's1', system_account: '$SYS', accounts: ['A', '$SYS'] }, s2: { server_id: 's2', accounts: ['A', 'B'] } }
  if (url.includes('/accountz/')) return { account_name: 'A', is_system: false, expired: false, jetstream_enabled: true, leafnode_connections: 1, client_connections: 2, subscriptions: 3 }
  if (url.endsWith('/leafz')) return { s1: { leafs: [{ id: 1, name: 'leaf', ip: '1.2.3.4', port: 7422, account: 'A', rtt: '1ms', in_msgs: 1, out_msgs: 2, subscriptions: 3 }] } }
  if (url.includes('/subsz/detail')) return { subscriptions: [{ subject: 'foo', queue: 'q', sid: '1', msgs: 2, conn_cid: 7, conn_name: 'client', conn_ip: '1.2.3.4', account: 'A', server_id: 's1', server_name: 'one' }], total: 120, limit: 100, offset: 0 }
  if (url.endsWith('/subsz')) return { s1: { server_id: 's1', num_subscriptions: 5, num_cache: 2, num_inserts: 3, num_removes: 1, num_matches: 4, cache_hit_rate: 0.8, max_fanout: 2, avg_fanout: 1.5 } }
  if (url.match(/\/connz\/\d+$/)) return client
  if (url.includes('/connz')) return { connections: [client], total: 120, limit: 50, offset: 0 }
  if (url === '/api/admin/users') return { users: [
    { id: 1, username: 'admin', role: 'admin', auth_provider: 'local', created_at: '2026-01-01', last_login: '2026-01-02', failed_attempts: 0, last_failed_at: null },
    { id: 2, username: 'external', role: 'viewer', auth_provider: 'ldap', created_at: '2026-01-01', last_login: null, failed_attempts: 2, last_failed_at: '2026-01-03' },
  ] }
  return { ok: true }
}

function renderPage(element: React.ReactNode, path = '/', pattern = '*') {
  return render(<MemoryRouter initialEntries={[path]}><Routes><Route path={pattern} element={element} /></Routes></MemoryRouter>)
}

const overview: Overview = { server_count: 2, healthy_count: 1, connection_count: 2_500, in_msgs_rate: 1_200, out_msgs_rate: 2_300_000, in_bytes_rate: 1_500, out_bytes_rate: 2_500_000_000, subscriptions: 4_000, js_streams: 2, js_consumers: 3, js_messages: 4_000, js_bytes: 5_000, servers: [{ id: 's1', name: 'one', version: '2', connections: 2_500, cpu: 2.5, mem: 2_500_000, in_msgs_rate: 1_200, out_msgs_rate: 2_300_000, healthy: false, uptime: '1h' }] }

describe('data-rich dashboard pages', () => {
  beforeEach(() => {
    useStore.setState({ activeEnv: 'test', overview, environments: [{ id: 'test', name: 'Test' }], toasts: [] })
    vi.spyOn(globalThis, 'fetch').mockImplementation((input) => Promise.resolve(new Response(JSON.stringify(fixture(String(input))), { status: 200, headers: { 'Content-Type': 'application/json' } })))
    vi.spyOn(window, 'confirm').mockReturnValue(true)
  })

  it('renders overview and server details with metrics controls', async () => {
    const first = renderPage(<OverviewPage />)
    expect(await screen.findByText('Cluster totals across all servers')).toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: '24h' }))
    first.unmount()
    renderPage(<ServerDetailPage />, '/servers/s1', '/servers/:id')
    expect(await screen.findByRole('heading', { name: 'one' })).toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: '6h' }))
  })

  it('filters, sorts, pages, and opens NATS connection detail', async () => {
    renderPage(<ConnectionsPage />)
    expect(await screen.findByText('client-1')).toBeInTheDocument()
    fireEvent.click(screen.getByText('client-1'))
    expect(await screen.findByText(/Connection 7/)).toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: 'Close' }))
    for (const input of screen.getAllByRole('textbox')) fireEvent.change(input, { target: { value: 'A' } })
    for (const select of screen.getAllByRole('combobox')) fireEvent.change(select, { target: { value: select.querySelectorAll('option')[1]?.value || '' } })
    for (const button of screen.getAllByRole('button')) if (!button.hasAttribute('disabled')) fireEvent.click(button)
  })

  it('loads subscription summary and debounced detail', async () => {
    renderPage(<SubscriptionsPage />)
    expect(await screen.findByRole('heading', { name: 'Subscriptions' })).toBeInTheDocument()
    const subject = screen.getByPlaceholderText(/type a subject/i)
    fireEvent.change(subject, { target: { value: 'foo' } })
    expect(await screen.findByText('foo', {}, { timeout: 2000 })).toBeInTheDocument()
    fireEvent.click(screen.getByRole('checkbox'))
    for (const button of screen.getAllByRole('button')) if (!button.hasAttribute('disabled')) fireEvent.click(button)
  })

  it('expands account detail and every drilldown', async () => {
    renderPage(<AccountsPage />)
    expect(await screen.findByText('Total Accounts')).toBeInTheDocument()
    fireEvent.click(screen.getAllByRole('button').find((button) => button.textContent?.trim() === 'A')!)
    expect(await screen.findByText('Client Connections')).toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: /^Client Connections/ }))
    expect(await screen.findByText('Client Connections (120)')).toBeInTheDocument()
    fireEvent.change(screen.getAllByPlaceholderText('Filter...')[0], { target: { value: 'does-not-match' } })
    expect(screen.getByText('None')).toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: /^Client Connections/ }))
    fireEvent.click(screen.getByRole('button', { name: /^Leaf Connections/ }))
    expect(await screen.findByText('Leaf Connections (1)')).toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: /^Leaf Connections/ }))
    fireEvent.click(screen.getByRole('button', { name: /^Subscriptions/ }))
    expect(await screen.findByText('Subscriptions (120)')).toBeInTheDocument()
    fireEvent.click(screen.getAllByRole('button').find((button) => button.textContent?.trim() === 'A')!)
  })

  it('filters and expands JetStream streams and consumers', async () => {
    renderPage(<JetStreamPage />)
    expect(await screen.findByText('ORDERS')).toBeInTheDocument()
    fireEvent.change(screen.getByRole('combobox'), { target: { value: 'A' } })
    fireEvent.click(screen.getByRole('button', { name: /ORDERS/ }))
    expect(screen.getByText(/Consumers \(1\)/)).toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: /ORDERS/ }))
  })

  it('renders MQTT overview, connection tables, and modals', async () => {
    const overviewView = renderPage(<MQTTOverviewPage />)
    expect(await screen.findByText('bridge-one')).toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: '24h' }))
    overviewView.unmount()

    const one = renderPage(<MQTTConnectionsPage />, '/mqtt/bridge-one/connections', '/mqtt/:bridge/connections')
    expect(await screen.findByText('client-1')).toBeInTheDocument()
    fireEvent.click(screen.getByText('client-1'))
    expect(screen.getByText(/MQTT Client:/)).toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: 'Close' }))
    one.unmount()

    renderPage(<MQTTAllConnectionsPage />)
    expect(await screen.findByText('client-1')).toBeInTheDocument()
    fireEvent.click(screen.getByText('client-1'))
    expect(screen.getByText(/MQTT Client:/)).toBeInTheDocument()
  })

  it('loads and switches through every MQTT diagnostic tab', async () => {
    renderPage(<MQTTBridgeDetailPage />, '/mqtt/bridge-one/detail', '/mqtt/:bridge/detail')
    expect(await screen.findByRole('heading', { name: 'bridge-one' })).toBeInTheDocument()
    for (const label of ['Metrics', 'Connection Pool', 'License', 'Config', 'NATS Connection']) {
      fireEvent.click(screen.getByRole('button', { name: label }))
    }
  })

  it('creates, deletes, and changes local users', async () => {
    renderPage(<UsersPage />)
    expect((await screen.findAllByText('admin')).length).toBeGreaterThan(0)
    fireEvent.click(screen.getByRole('button', { name: 'Create User' }))
    fireEvent.click(screen.getByRole('button', { name: 'Create' }))
    fireEvent.change(screen.getByPlaceholderText('username'), { target: { value: 'new-user' } })
    fireEvent.change(screen.getByPlaceholderText('password'), { target: { value: 'new-password' } })
    fireEvent.change(screen.getByRole('combobox'), { target: { value: 'admin' } })
    fireEvent.click(screen.getByRole('button', { name: 'Create' }))
    await waitFor(() => expect(globalThis.fetch).toHaveBeenCalledWith('/api/admin/users', expect.objectContaining({ method: 'POST' })))
    fireEvent.click(screen.getAllByTitle('Change password')[0])
    for (const input of document.querySelectorAll('input[type="password"]')) fireEvent.change(input, { target: { value: 'password' } })
    fireEvent.click(screen.getByRole('button', { name: 'Change Password' }))
    fireEvent.click(screen.getAllByTitle('Delete user')[0])
  })
})
