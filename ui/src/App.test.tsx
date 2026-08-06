import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import App from './App'
import { useStore } from './store/store'

vi.mock('react-force-graph-2d', () => ({ default: () => <div data-testid="force-graph" /> }))

function response(data: unknown, status = 200) {
  return Promise.resolve(new Response(JSON.stringify(data), { status, headers: { 'Content-Type': 'application/json' } }))
}

class WebSocketStub {
  static OPEN = 1
  readyState = 1
  onopen: (() => void) | null = null
  onmessage: ((event: MessageEvent) => void) | null = null
  onclose: (() => void) | null = null
  send = vi.fn()
  close = vi.fn()
  constructor() { queueMicrotask(() => this.onopen?.()) }
}

describe('App authentication and navigation', () => {
  beforeEach(() => {
    Object.defineProperty(globalThis, 'WebSocket', { configurable: true, value: WebSocketStub })
    window.history.replaceState({}, '', '/')
    useStore.setState({ activeEnv: '', darkMode: false, environments: [], overview: null, topology: null, health: null })
  })

  it('renders the dark loading shell while session hydration is pending', () => {
    useStore.setState({ darkMode: true })
    vi.spyOn(globalThis, 'fetch').mockImplementation(() => new Promise(() => undefined))
    const { container } = render(<App />)
    expect(screen.getByText('Loading...')).toBeInTheDocument()
    expect(container.firstChild).toHaveClass('dark')
  })

  it('shows external login by default', async () => {
    vi.spyOn(globalThis, 'fetch').mockImplementation((input) => {
      const url = String(input)
      if (url === '/api/me') return response({}, 401)
      if (url === '/api/auth/providers') return response({ providers: [{ name: 'dex', type: 'oidc', login_url: '/api/auth/oidc/dex/login' }] })
      return response({})
    })
    render(<App />)
    expect(await screen.findByRole('button', { name: /sign in with dex/i })).toBeInTheDocument()
    expect(screen.getByRole('link', { name: /local administrator login/i })).toHaveAttribute('href', '/login/local')
  })

  it('shows the explicit local recovery route', async () => {
    window.history.replaceState({}, '', '/login/local')
    const fetchMock = vi.spyOn(globalThis, 'fetch').mockImplementation((input) => {
      if (String(input) === '/api/me') return response({}, 401)
      if (String(input) === '/api/auth/local/login') return response({ id: 1, username: 'admin', role: 'admin', auth_provider: 'local', must_change_password: false })
      if (String(input) === '/api/environments') return response({ environments: [] })
      if (String(input) === '/api/version') return response({ version: 'test' })
      return response({ providers: [{ name: 'dex', type: 'oidc' }] })
    })
    render(<App />)
    expect(await screen.findByText(/local administrator sign in/i)).toBeInTheDocument()
    const user = userEvent.setup()
    await user.type(screen.getByLabelText('Username'), 'admin')
    await user.type(screen.getByLabelText('Password'), 'recovery-secret')
    await user.click(screen.getByRole('button', { name: 'Sign In' }))
    await waitFor(() => expect(fetchMock).toHaveBeenCalledWith('/api/auth/local/login', expect.objectContaining({
      method: 'POST',
      body: JSON.stringify({ username: 'admin', password: 'recovery-secret' }),
    })))
  })

  it('submits the organization password-login route for LDAP users', async () => {
    const fetchMock = vi.spyOn(globalThis, 'fetch').mockImplementation((input) => {
      const url = String(input)
      if (url === '/api/me') return response({}, 401)
      if (url === '/api/auth/providers') return response({ providers: [{ name: 'directory', type: 'ldap' }] })
      if (url === '/api/login') return response({ id: 9, username: 'alice', role: 'viewer', auth_provider: 'ldap', must_change_password: false })
      if (url === '/api/environments' || url === '/api/version') return response({}, 503)
      return response({})
    })
    render(<App />)
    const user = userEvent.setup()
    await user.type(await screen.findByLabelText('Username'), 'alice')
    await user.type(screen.getByLabelText('Password'), 'secret')
    await user.click(screen.getByRole('button', { name: 'Sign In' }))
    await waitFor(() => expect(fetchMock).toHaveBeenCalledWith('/api/login', expect.objectContaining({ method: 'POST' })))
    expect(screen.queryByRole('link', { name: 'User Management' })).not.toBeInTheDocument()
  })

  it('accepts empty environment/version payloads without replacing an active environment', async () => {
    useStore.setState({ activeEnv: 'existing' })
    vi.spyOn(globalThis, 'fetch').mockImplementation((input) => {
      const url = String(input)
      if (url === '/api/me') return response({ id: 1, username: 'viewer', role: 'viewer', auth_provider: 'oidc', must_change_password: false })
      if (url === '/api/auth/providers') return response({})
      if (url === '/api/environments') return response({})
      if (url === '/api/version') return response({ version: '' })
      return response({})
    })
    render(<App />)
    expect(await screen.findByText('version dev', {}, { timeout: 5000 })).toBeInTheDocument()
    expect(useStore.getState().activeEnv).toBe('existing')
  })

  it('selects the first configured environment only when none is active', async () => {
    vi.spyOn(globalThis, 'fetch').mockImplementation((input) => {
      const url = String(input)
      if (url === '/api/me') return response({ id: 1, username: 'viewer', role: 'viewer', auth_provider: 'oidc', must_change_password: false })
      if (url === '/api/auth/providers') return response({ providers: [] })
      if (url === '/api/environments') return response({ environments: [{ id: 'first', name: 'First' }, { id: 'second', name: 'Second' }] })
      if (url === '/api/version') return response({ version: '1.2.3' })
      return response({})
    })
    render(<App />)
    expect(await screen.findByText('version 1.2.3', {}, { timeout: 5000 })).toBeInTheDocument()
    await waitFor(() => expect(useStore.getState()).toMatchObject({
      environments: [{ id: 'first', name: 'First' }, { id: 'second', name: 'Second' }],
      activeEnv: 'first',
    }))
  })

  it('forces bootstrap password rotation before protected routes', async () => {
    vi.spyOn(globalThis, 'fetch').mockImplementation((input) => {
      if (String(input) === '/api/me') return response({ id: 1, username: 'admin', role: 'admin', auth_provider: 'local', must_change_password: true })
      return response({ providers: [] })
    })
    render(<App />)
    expect(await screen.findByText(/change password/i)).toBeInTheDocument()
  })

  it('does not apply local password rotation rules to an external identity', async () => {
    vi.spyOn(globalThis, 'fetch').mockImplementation((input) => {
      const url = String(input)
      if (url === '/api/me') return response({ id: 1, username: 'external', role: 'viewer', auth_provider: 'oidc', must_change_password: true })
      if (url === '/api/auth/providers') return response({ providers: [] })
      if (url === '/api/environments') return response({ environments: [] })
      if (url === '/api/version') return response({ version: 'test' })
      return response({})
    })
    render(<App />)
    expect(await screen.findByRole('heading', { name: 'Overview' }, { timeout: 5000 })).toBeInTheDocument()
    expect(screen.queryByText(/must change your password/i)).not.toBeInTheDocument()
  })

  it('redirects viewers away from the administrator route', async () => {
    window.history.replaceState({}, '', '/admin/users')
    vi.spyOn(globalThis, 'fetch').mockImplementation((input) => {
      const url = String(input)
      if (url === '/api/me') return response({ id: 1, username: 'viewer', role: 'viewer', auth_provider: 'oidc', must_change_password: false })
      if (url === '/api/auth/providers') return response({ providers: [] })
      if (url === '/api/environments') return response({ environments: [] })
      if (url === '/api/version') return response({ version: 'test' })
      return response({})
    })
    render(<App />)
    expect(await screen.findByRole('heading', { name: 'Overview' }, { timeout: 5000 })).toBeInTheDocument()
    expect(screen.queryByRole('heading', { name: 'User Management' })).not.toBeInTheDocument()
  })

  it('renders authenticated navigation and logs out', async () => {
    const fetchMock = vi.spyOn(globalThis, 'fetch').mockImplementation((input) => {
      const url = String(input)
      if (url === '/api/me') return response({ id: 1, username: 'admin', role: 'admin', auth_provider: 'local', must_change_password: false })
      if (url === '/api/auth/providers') return response({ providers: [] })
      if (url === '/api/environments') return response({ environments: [{ id: 'test', name: 'Test' }] })
      if (url === '/api/version') return response({ version: 'test' })
      if (url === '/api/logout') return response({ ok: true })
      return response({})
    })
    render(<App />)
		await waitFor(() => expect(screen.getAllByText('admin').length).toBeGreaterThan(0))
    await userEvent.click(screen.getByRole('button', { name: /logout/i }))
    await waitFor(() => expect(fetchMock).toHaveBeenCalledWith('/api/logout', expect.objectContaining({ method: 'POST', signal: expect.any(AbortSignal) })))
  })

  it.each([
    ['/topology', 'Cluster Topology'],
    ['/connections', 'Connections'],
    ['/subscriptions', 'Subscriptions'],
    ['/jetstream', 'JetStream'],
    ['/accounts', 'Accounts'],
    ['/servers/server-1', 'server one'],
    ['/admin/users', 'User Management'],
    ['/mqtt', 'MachMQTT Fleet'],
    ['/mqtt/connections', 'All MQTT Connections'],
    ['/mqtt/bridge-1/connections', 'bridge-1 — MQTT Connections'],
    ['/mqtt/bridge-1/detail', 'bridge-1'],
  ])('loads authenticated route %s and renders %s', async (path, heading) => {
    window.history.replaceState({}, '', path)
    vi.spyOn(globalThis, 'fetch').mockImplementation((input) => {
      const url = String(input)
      if (url === '/api/me') return response({ id: 1, username: 'admin', role: 'admin', auth_provider: 'local', must_change_password: false })
      if (url === '/api/auth/providers') return response({ providers: [] })
      if (url === '/api/environments') return response({ environments: [{ id: 'test', name: 'Test' }] })
      if (url === '/api/version') return response({ version: 'test' })
      if (url.endsWith('/subsz')) return response({})
      if (url.endsWith('/varz')) return response({
        'server-1': {
          server_id: 'server-1', server_name: 'server one', version: '1', host: '127.0.0.1', port: 4222,
          go: 'go1.26', max_connections: 100, connections: 1, total_connections: 2, routes: 0, leafnodes: 0,
          in_msgs: 1, out_msgs: 2, in_bytes: 3, out_bytes: 4, mem: 5, cpu: 1, cores: 2,
          subscriptions: 1, slow_consumers: 0, uptime: '1m',
        },
      })
      return response({
        points: [], servers: [], bridges: [], connections: [], subscriptions: [],
        accounts: [], users: [], varz: {}, routez: {}, gatewayz: {}, leafz: {},
        jsinfo: {}, accountz: {}, total: 0, num_connections: 0,
        server_id: 'server-1', server_name: 'server one',
      })
    })
    render(<App />)
    expect(await screen.findByRole('heading', { name: heading }, { timeout: 5000 })).toBeInTheDocument()
    expect(screen.queryByText('Loading view...')).not.toBeInTheDocument()
  })
})
