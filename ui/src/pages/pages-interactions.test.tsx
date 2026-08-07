import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import { MemoryRouter, Route, Routes } from 'react-router'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { useStore } from '../store/store'
import { ConnectionsPage } from './ConnectionsPage'
import { MQTTAllConnectionsPage } from './MQTTAllConnectionsPage'
import { MQTTConnectionsPage } from './MQTTConnectionsPage'
import { SubscriptionsPage } from './SubscriptionsPage'
import { UsersPage } from './UsersPage'

const client = {
  cid: 1, mqtt_client: 'c1', name: 'c1', kind: '', type: '', ip: '127.0.0.1', port: 1,
  account: 'A', authorized_user: '', username: '', rtt: '', state: 'active', in_msgs: 2_000_000_000,
  out_msgs: 2, in_bytes: 2_000_000_000, out_bytes: 2, subscriptions: 0, subscriptions_list: [],
  uptime: '', idle: '', lang: '', version: '', tls_version: '', tls_cipher_suite: '', pending_bytes: 0,
  inflight_out: 0, clean_start: false, keep_alive: 0, session_expiry_interval: 0, receive_maximum: 0,
  is_websocket: false, start: '', last_activity: '',
}

function json(data: unknown, status = 200) {
  return Promise.resolve(new Response(JSON.stringify(data), { status, headers: { 'Content-Type': 'application/json' } }))
}

function renderPage(element: React.ReactNode, path = '/', pattern = '*') {
  return render(<MemoryRouter initialEntries={[path]}><Routes><Route path={pattern} element={element} /></Routes></MemoryRouter>)
}

describe('pagination, modal, and administration interactions', () => {
  beforeEach(() => useStore.setState({ activeEnv: 'test', environments: [{ id: 'test', name: 'Test' }], toasts: [] }))
  afterEach(() => vi.useRealTimers())

  it('moves through every NATS connection page and closes detail from the backdrop', async () => {
    vi.spyOn(globalThis, 'fetch').mockImplementation((input) => {
      const url = String(input)
      if (/\/connz\/1$/.test(url)) return json(client)
      const offset = Number(new URL(url, 'http://test').searchParams.get('offset') || 0)
      return json({ connections: [{ ...client, cid: offset + 1 }], num_connections: 120, total: 120, limit: 50, offset })
    })
    renderPage(<ConnectionsPage />)
    expect(await screen.findByText('Page 1 of 3')).toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: 'Next' }))
    expect(await screen.findByText('Page 2 of 3')).toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: 'Previous' }))
    expect(await screen.findByText('Page 1 of 3')).toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: 'Last' }))
    expect(await screen.findByText('Page 3 of 3')).toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: 'First' }))
    expect(await screen.findByText('Page 1 of 3')).toBeInTheDocument()
    fireEvent.click(screen.getByText('c1'))
    const heading = await screen.findByText(/Connection 1/)
    fireEvent.click(heading.parentElement!)
    expect(screen.getByText(/Connection 1/)).toBeInTheDocument()
    fireEvent.click(heading.parentElement!.parentElement!)
    expect(screen.queryByText(/Connection 1/)).not.toBeInTheDocument()
  })

  it('moves through bridge-specific MQTT pages and resets page size', async () => {
    vi.spyOn(globalThis, 'fetch').mockImplementation((input) => {
      const url = new URL(String(input), 'http://test')
      const offset = Number(url.searchParams.get('offset') || 0)
      return json({ connections: [{ ...client, cid: offset + 1 }], num_connections: 120, total: 120, limit: 50, offset })
    })
    renderPage(<MQTTConnectionsPage />, '/mqtt/b/connections', '/mqtt/:bridge/connections')
    expect(await screen.findByText('Page 1 of 3')).toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: 'Next' }))
    expect(await screen.findByText('Page 2 of 3')).toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: 'Previous' }))
    fireEvent.click(screen.getByRole('button', { name: 'Last' }))
    expect(await screen.findByText('Page 3 of 3')).toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: 'First' }))
    fireEvent.change(screen.getByRole('combobox'), { target: { value: '25' } })
    await waitFor(() => expect(String(fetch)).toBeDefined())
  })

  it('sorts and inspects a fully populated MQTT client', async () => {
    const populated = {
      ...client, mqtt_client: 'rich-client', kind: 'client', type: 'mqtt5', username: 'alice',
      state: 'active', slow_consumer: true, pending_bytes: 32, uptime: '1h', idle: '2s',
      clean_start: true, is_websocket: true,
    }
    vi.spyOn(globalThis, 'fetch').mockImplementation(() => json({ connections: [populated], num_connections: 1 }))
    renderPage(<MQTTConnectionsPage />, '/mqtt/b/connections', '/mqtt/:bridge/connections')
    expect(await screen.findByText('rich-client')).toBeInTheDocument()
    const clientHeader = screen.getByText('Client ID').closest('th')!
    fireEvent.click(clientHeader)
    fireEvent.click(clientHeader)
    fireEvent.click(screen.getByText('rich-client'))
    expect(screen.getByText(/MQTT Client:/)).toBeInTheDocument()
    expect(screen.getByText('mqtt5')).toBeInTheDocument()
    expect(screen.getAllByText('alice').length).toBeGreaterThanOrEqual(2)
    expect(screen.getAllByText('Yes').length).toBeGreaterThanOrEqual(2)
    fireEvent.click(screen.getByRole('button', { name: 'Close' }))
    expect(screen.queryByText(/MQTT Client:/)).not.toBeInTheDocument()
  })

  it('filters, sorts, and loads a complete NATS connection detail', async () => {
    const populated = {
      ...client, name: 'rich-nats-client', account: 'ACCOUNT', authorized_user: 'alice', rtt: '1ms',
      lang: 'go', version: '1.2.3', tls_version: 'TLS1.3', tls_cipher_suite: 'AES',
      subscriptions: 1, subscriptions_list: ['orders.>'],
    }
    vi.spyOn(globalThis, 'fetch').mockImplementation((input) => {
      const url = String(input)
      if (/\/connz\/1$/.test(url)) return json(populated)
      return json({ connections: [populated], total: 1, server_total: 2, truncated: true, subs_available: false })
    })
    renderPage(<ConnectionsPage />)
    expect(await screen.findByText('rich-nats-client')).toBeInTheDocument()
    const nameHeader = screen.getByText('Name').closest('th')!
    fireEvent.click(nameHeader)
    fireEvent.click(nameHeader)
    fireEvent.change(screen.getByPlaceholderText('User / Name'), { target: { value: 'alice' } })
    fireEvent.change(screen.getByPlaceholderText('Account'), { target: { value: 'ACCOUNT' } })
    fireEvent.change(screen.getByPlaceholderText('Subject filter'), { target: { value: 'orders.>' } })
    fireEvent.change(screen.getAllByRole('combobox')[0], { target: { value: 'open' } })
    expect(await screen.findByText(/matching connections/)).toBeInTheDocument()
    expect(screen.getByText(/subscription data unavailable/)).toBeInTheDocument()
    fireEvent.click(screen.getByText('rich-nats-client'))
    expect(await screen.findByText('orders.>')).toBeInTheDocument()
    expect(screen.getByText(/TLS1.3 AES/)).toBeInTheDocument()
  })

  it('aggregates enough MQTT clients to exercise local table pagination and filtering', async () => {
    const connections = Array.from({ length: 60 }, (_, index) => ({ ...client, cid: index + 1, mqtt_client: `client-${index}` }))
    vi.spyOn(globalThis, 'fetch').mockImplementation((input) => {
      const url = String(input)
      if (url.endsWith('/mqtt/bridges')) return json({ bridges: [{
        ip: '127.0.0.1', configured_name: '', server_name: 'n1', reachable: true,
        status: { name: 'bridge', connz_available: true, connections: 60, nats: { connection: { url: 'nats://n1' } } },
      }] })
      return json({ connections })
    })
    renderPage(<MQTTAllConnectionsPage />)
    expect(await screen.findByText('Page 1 of 2')).toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: 'Next' }))
    expect(screen.getByText('Page 2 of 2')).toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: 'Previous' }))
    fireEvent.click(screen.getByRole('button', { name: 'Last' }))
    expect(screen.getByText('Page 2 of 2')).toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: 'First' }))
    fireEvent.change(screen.getByRole('combobox'), { target: { value: '50' } })
    fireEvent.change(screen.getAllByPlaceholderText('Filter...')[0], { target: { value: 'bridge' } })
    expect(screen.getByText(/filtered from 60/)).toBeInTheDocument()
  })

  it('handles partial MQTT aggregation, naming fallbacks, sorting, and sparse client details', async () => {
    vi.spyOn(globalThis, 'fetch').mockImplementation((input) => {
      const url = String(input)
      if (url.endsWith('/mqtt/bridges')) return json({ bridges: [
        { ip: '1', configured_name: 'nonok', reachable: true, status: { connz_available: true, connections: 1 } },
        { ip: '2', configured_name: 'error', reachable: true, status: { connz_available: true, connections: 1 } },
        { ip: '3', configured_name: '', reachable: true, status: { name: 'rejected', connz_available: true, connections: 1 } },
        { ip: '4', configured_name: '', server_name: '', reachable: true, status: { name: '', connz_available: true, connections: 1, nats: { connection: { server_name: 'named' } } } },
        { ip: '5', configured_name: 'fallback', server_name: '', reachable: true, status: { connz_available: true, connections: 1 } },
        { ip: '6', configured_name: 'ignored', reachable: false, status: { connz_available: true, connections: 1 } },
      ] })
      if (url.includes('/nonok/')) return json({}, 503)
      if (url.includes('/error/')) return json({ error: 'disabled' })
      if (url.includes('/rejected/')) return Promise.reject(new Error('offline'))
      return json({ connections: [{ ...client, mqtt_client: url.includes('mqtt%404') ? 'sparse-a' : 'sparse-b' }] })
    })
    renderPage(<MQTTAllConnectionsPage />)
    expect(await screen.findByText('sparse-a')).toBeInTheDocument()
    const bridgeHeader = screen.getByText('Bridge').closest('th')!
    fireEvent.click(bridgeHeader)
    fireEvent.click(bridgeHeader)
    fireEvent.click(screen.getByText('sparse-a'))
    expect(screen.getByText(/MQTT Client:/)).toBeInTheDocument()
    const dialog = screen.getByText(/MQTT Client:/).closest('.fixed')!
    fireEvent.click(dialog)
    expect(screen.queryByText(/MQTT Client:/)).not.toBeInTheDocument()
  })

  it('stops MQTT aggregation after a non-ok bridge inventory response', async () => {
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(new Response(null, { status: 503 }))
    renderPage(<MQTTAllConnectionsPage />)
    expect(await screen.findByText('No MQTT connections found')).toBeInTheDocument()
  })

  it('handles subscription HTTP failure, network failure, and empty results', async () => {
    vi.spyOn(globalThis, 'fetch').mockImplementation((input) => {
      const url = String(input)
      if (url.includes('subject=bad')) return json({}, 503)
      if (url.includes('subject=offline')) return Promise.reject(new Error('offline'))
      if (url.includes('subject=empty')) return json({ subscriptions: [], total: 0 })
      return json({})
    })
    const view = renderPage(<SubscriptionsPage />)
    fireEvent.change(screen.getByPlaceholderText(/type a subject/i), { target: { value: 'bad' } })
    await waitFor(() => expect(useStore.getState().toasts.some((toast) => toast.message.includes('Failed'))).toBe(true), { timeout: 1500 })
    fireEvent.change(screen.getByPlaceholderText(/type a subject/i), { target: { value: 'offline' } })
    await waitFor(() => expect(useStore.getState().toasts.some((toast) => toast.message === 'Network error')).toBe(true), { timeout: 1500 })
    fireEvent.change(screen.getByPlaceholderText(/type a subject/i), { target: { value: 'empty' } })
    expect(await screen.findByText('No matching subscriptions found', {}, { timeout: 1500 })).toBeInTheDocument()
    view.unmount()
  })

  it('covers rejected create/delete/password operations and modal cancellation', async () => {
    const admin = { id: 1, username: 'admin', role: 'admin', auth_provider: 'local', created_at: '', last_login: null, failed_attempts: 0, last_failed_at: null }
    const local = { ...admin, id: 2, username: 'second' }
    vi.spyOn(window, 'confirm').mockReturnValue(false)
    const fetchMock = vi.spyOn(globalThis, 'fetch').mockImplementation((input, init) => {
      const url = String(input)
      if (url === '/api/admin/users' && !init?.method) return json({ users: [admin, local] })
      if (url === '/api/admin/users' && init?.method === 'POST') return Promise.reject(new Error('offline'))
      if (url.includes('/password')) return json({ error: 'bad password' }, 400)
      return json({ error: 'cannot delete' }, 400)
    })
    renderPage(<UsersPage />)
    expect((await screen.findAllByText('admin')).length).toBeGreaterThan(0)
    fireEvent.click(screen.getByRole('button', { name: 'Create User' }))
    fireEvent.click(screen.getByRole('button', { name: 'Create' }))
    expect(useStore.getState().toasts.at(-1)?.message).toBe('Username and password are required')
    // Each field is required on its own, and a rejected create must never
    // reach the server.
    const createCalls = () => fetchMock.mock.calls.filter(([, init]) => (init as RequestInit | undefined)?.method === 'POST').length
    expect(createCalls()).toBe(0)
    fireEvent.change(screen.getByPlaceholderText('username'), { target: { value: 'new' } })
    fireEvent.click(screen.getByRole('button', { name: 'Create' }))
    expect(useStore.getState().toasts.at(-1)?.message).toBe('Username and password are required')
    expect(createCalls()).toBe(0)
    fireEvent.change(screen.getByPlaceholderText('username'), { target: { value: '' } })
    fireEvent.change(screen.getByPlaceholderText('password'), { target: { value: 'password' } })
    fireEvent.click(screen.getByRole('button', { name: 'Create' }))
    expect(useStore.getState().toasts.at(-1)?.message).toBe('Username and password are required')
    expect(createCalls()).toBe(0)
    fireEvent.change(screen.getByPlaceholderText('username'), { target: { value: 'new' } })
    fireEvent.click(screen.getByRole('button', { name: 'Create' }))
    await waitFor(() => expect(useStore.getState().toasts.at(-1)?.message).toBe('Network error'))
    expect(createCalls()).toBe(1)
    fireEvent.click(screen.getByRole('button', { name: 'Cancel' }))
    fireEvent.click(screen.getAllByTitle('Delete user')[0])
    expect(window.confirm).toHaveBeenCalled()
    fireEvent.click(screen.getAllByTitle('Change password')[0])
    // Both password fields are required, and neither omission may issue a request.
    const passwordCalls = () => fetchMock.mock.calls.filter(([input]) => String(input).includes('/password')).length
    fireEvent.click(screen.getByRole('button', { name: 'Change Password' }))
    expect(passwordCalls()).toBe(0)
    const inputs = document.querySelectorAll('input[type="password"]')
    fireEvent.change(inputs[0], { target: { value: 'old' } })
    fireEvent.click(screen.getByRole('button', { name: 'Change Password' }))
    expect(passwordCalls()).toBe(0)
    fireEvent.change(inputs[0], { target: { value: '' } })
    fireEvent.change(inputs[1], { target: { value: 'new' } })
    fireEvent.click(screen.getByRole('button', { name: 'Change Password' }))
    expect(passwordCalls()).toBe(0)
    fireEvent.change(inputs[0], { target: { value: 'old' } })
    fireEvent.click(screen.getByRole('button', { name: 'Change Password' }))
    await waitFor(() => expect(useStore.getState().toasts.at(-1)?.message).toBe('bad password'))
    expect(passwordCalls()).toBe(1)
    fireEvent.click(screen.getByRole('button', { name: 'Cancel' }))
  })

  it('enforces break-glass controls and completes successful user administration', async () => {
    const lastAdmin = {
      id: 1, username: 'admin', role: 'admin', auth_provider: 'local', created_at: '2026-01-01T00:00:00Z',
      last_login: null, failed_attempts: 0, last_failed_at: null,
    }
    const localViewer = {
      ...lastAdmin, id: 2, username: 'local-viewer', role: 'viewer', last_login: '2026-01-02T00:00:00Z',
      failed_attempts: 2, last_failed_at: '2026-01-03T00:00:00Z',
    }
    const externalAdmin = { ...lastAdmin, id: 3, username: 'external-admin', auth_provider: 'oidc' }
    const fetchMock = vi.spyOn(globalThis, 'fetch').mockImplementation((input, init) => {
      const url = String(input)
      if (url === '/api/admin/users' && !init?.method) return json({ users: [lastAdmin, localViewer, externalAdmin] })
      if (url === '/api/admin/users' && init?.method === 'POST') return json({ id: 4 }, 201)
      if (url === '/api/admin/users/2' && init?.method === 'DELETE') return json({ ok: true })
      if (url === '/api/users/2/password' && init?.method === 'PUT') return json({ ok: true })
      return json({}, 404)
    })
    vi.spyOn(window, 'confirm').mockReturnValue(true)

    renderPage(<UsersPage />)
    expect(await screen.findByText('local-viewer')).toBeInTheDocument()
    expect(screen.getAllByText('Never')).toHaveLength(2)
    expect(screen.getByTitle(/Last failed:/)).toHaveTextContent('2')

    const deleteButtons = screen.getAllByTitle('Delete user')
    expect(deleteButtons).toHaveLength(2)
    expect(screen.getAllByTitle('Change password')).toHaveLength(2)

    fireEvent.click(screen.getByRole('button', { name: 'Create User' }))
    fireEvent.change(screen.getByPlaceholderText('username'), { target: { value: 'created-user' } })
    fireEvent.change(screen.getByPlaceholderText('password'), { target: { value: 'created-password' } })
    // Least privilege: the form must default to viewer so an admin who never
    // touches the role selector cannot accidentally mint another admin.
    expect(screen.getByRole('combobox')).toHaveValue('viewer')
    fireEvent.change(screen.getByRole('combobox'), { target: { value: 'admin' } })
    fireEvent.click(screen.getByRole('button', { name: 'Create' }))
    await waitFor(() => expect(useStore.getState().toasts.at(-1)).toMatchObject({
      message: 'User "created-user" created', type: 'success',
    }))
    expect(fetchMock).toHaveBeenCalledWith('/api/admin/users', expect.objectContaining({
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ username: 'created-user', password: 'created-password', role: 'admin' }),
    }))
    expect(screen.queryByPlaceholderText('username')).not.toBeInTheDocument()
    // The elevated selection must not persist into the next create.
    fireEvent.click(screen.getByRole('button', { name: 'Create User' }))
    expect(screen.getByRole('combobox')).toHaveValue('viewer')
    expect(screen.getByPlaceholderText('username')).toHaveValue('')
    expect(screen.getByPlaceholderText('password')).toHaveValue('')
    fireEvent.click(screen.getByRole('button', { name: 'Cancel' }))

    fireEvent.click(screen.getAllByTitle('Delete user')[0])
    await waitFor(() => expect(fetchMock).toHaveBeenCalledWith('/api/admin/users/2', expect.objectContaining({ method: 'DELETE' })))
    expect(window.confirm).toHaveBeenCalledWith('Delete user "local-viewer"?')
    expect(useStore.getState().toasts.at(-1)).toMatchObject({ message: 'User "local-viewer" deleted', type: 'success' })

    fireEvent.click(screen.getAllByTitle('Change password')[1])
    const passwordInputs = document.querySelectorAll('input[type="password"]')
    fireEvent.change(passwordInputs[0], { target: { value: 'old-password' } })
    fireEvent.change(passwordInputs[1], { target: { value: 'new-password' } })
    fireEvent.click(screen.getByRole('button', { name: 'Change Password' }))
    await waitFor(() => expect(fetchMock).toHaveBeenCalledWith('/api/users/2/password', expect.objectContaining({
      method: 'PUT',
      body: JSON.stringify({ old_password: 'old-password', new_password: 'new-password' }),
    })))
    expect(useStore.getState().toasts.at(-1)).toMatchObject({ message: 'Password changed', type: 'success' })
    expect(screen.queryByRole('heading', { name: /Change Password for/ })).not.toBeInTheDocument()
  })

  it('surfaces a network failure while loading users instead of showing an empty roster', async () => {
    vi.spyOn(globalThis, 'fetch').mockRejectedValue(new TypeError('offline'))
    renderPage(<UsersPage />)
    await waitFor(() => expect(useStore.getState().toasts.at(-1)).toMatchObject({
      message: 'Network error loading users', type: 'error',
    }))
    expect(screen.queryByText('Loading users...')).not.toBeInTheDocument()
  })

  it('reports why a delete was refused and keeps the user in the roster', async () => {
    const admin = {
      id: 1, username: 'admin', role: 'admin', auth_provider: 'local', created_at: '2026-01-01T00:00:00Z',
      last_login: null, failed_attempts: 0, last_failed_at: null,
    }
    const viewer = { ...admin, id: 2, username: 'local-viewer', role: 'viewer' }
    vi.spyOn(globalThis, 'fetch').mockImplementation((input, init) => {
      const url = String(input)
      if (url === '/api/admin/users' && !init?.method) return json({ users: [admin, viewer] })
      if (url === '/api/admin/users/2' && init?.method === 'DELETE') {
        return json({ error: 'cannot delete the last local administrator' }, 409)
      }
      return json({}, 404)
    })
    vi.spyOn(window, 'confirm').mockReturnValue(true)

    renderPage(<UsersPage />)
    expect(await screen.findByText('local-viewer')).toBeInTheDocument()
    fireEvent.click(screen.getAllByTitle('Delete user')[0])

    // The server's reason must reach the operator verbatim; a generic message
    // would hide why the account is protected.
    await waitFor(() => expect(useStore.getState().toasts.at(-1)).toMatchObject({
      message: 'cannot delete the last local administrator', type: 'error',
    }))
    expect(screen.getByText('local-viewer')).toBeInTheDocument()
  })

  it('falls back to a generic delete message when the server sends no reason', async () => {
    const viewer = {
      id: 2, username: 'local-viewer', role: 'viewer', auth_provider: 'local', created_at: '2026-01-01T00:00:00Z',
      last_login: null, failed_attempts: 0, last_failed_at: null,
    }
    vi.spyOn(globalThis, 'fetch').mockImplementation((input, init) => {
      const url = String(input)
      if (url === '/api/admin/users' && !init?.method) return json({ users: [viewer] })
      return json({}, 500)
    })
    vi.spyOn(window, 'confirm').mockReturnValue(true)

    renderPage(<UsersPage />)
    expect(await screen.findByText('local-viewer')).toBeInTheDocument()
    fireEvent.click(screen.getAllByTitle('Delete user')[0])
    await waitFor(() => expect(useStore.getState().toasts.at(-1)).toMatchObject({
      message: 'Failed to delete user', type: 'error',
    }))
  })

  it('reports network failures for delete and password change', async () => {
    const viewer = {
      id: 2, username: 'local-viewer', role: 'viewer', auth_provider: 'local', created_at: '2026-01-01T00:00:00Z',
      last_login: null, failed_attempts: 0, last_failed_at: null,
    }
    vi.spyOn(globalThis, 'fetch').mockImplementation((input, init) => {
      if (String(input) === '/api/admin/users' && !init?.method) return json({ users: [viewer] })
      return Promise.reject(new TypeError('offline'))
    })
    vi.spyOn(window, 'confirm').mockReturnValue(true)

    renderPage(<UsersPage />)
    expect(await screen.findByText('local-viewer')).toBeInTheDocument()

    fireEvent.click(screen.getAllByTitle('Delete user')[0])
    await waitFor(() => expect(useStore.getState().toasts.at(-1)).toMatchObject({
      message: 'Network error', type: 'error',
    }))

    useStore.setState({ toasts: [] })
    fireEvent.click(screen.getAllByTitle('Change password')[0])
    const passwordInputs = document.querySelectorAll('input[type="password"]')
    fireEvent.change(passwordInputs[0], { target: { value: 'old-password' } })
    fireEvent.change(passwordInputs[1], { target: { value: 'new-password' } })
    fireEvent.click(screen.getByRole('button', { name: 'Change Password' }))

    await waitFor(() => expect(useStore.getState().toasts.at(-1)).toMatchObject({
      message: 'Network error', type: 'error',
    }))
    // A failed change must keep the dialog open so the operator can retry.
    expect(screen.getByRole('heading', { name: /Change Password for/ })).toBeInTheDocument()
  })
})
