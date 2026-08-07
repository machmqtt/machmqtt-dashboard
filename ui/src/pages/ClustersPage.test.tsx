import { fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { useStore } from '../store/store'
import { ClustersPage } from './ClustersPage'

const richCluster = {
  id: 'cluster-1',
  name: 'Production',
  servers: [{ url: 'https://nats-1:8222' }],
  mqtt_bridges: [{ name: 'manual', url: 'https://bridge:8080', has_bearer_token: true }],
  mqtt_discovery: { enabled: true, admin_ports: [8080, 8081] },
  tls: { ca_file: '/etc/ca.pem', insecure: false },
  has_admin_token: true,
  nats_conn: {
    urls: ['nats://nats-1:4222'], username: 'collector', has_password: true,
    subject_prefix: '$MQTT5', sys_collection: true,
  },
  created_at: '2026-01-02T00:00:00Z',
}

function json(data: unknown, status = 200) {
  return Promise.resolve(new Response(JSON.stringify(data), {
    status,
    headers: { 'Content-Type': 'application/json' },
  }))
}

describe('ClustersPage', () => {
  beforeEach(() => {
    useStore.setState({ toasts: [] })
    vi.spyOn(window, 'confirm').mockReturnValue(true)
  })

  it('creates a fully configured cluster and exercises every editor control', async () => {
    const changed = vi.fn()
    let reads = 0
    const fetchMock = vi.spyOn(globalThis, 'fetch').mockImplementation((input, init) => {
      if (String(input) === '/api/admin/clusters' && !init?.method) {
        reads++
        return json({ clusters: [] })
      }
      if (String(input) === '/api/admin/clusters' && init?.method === 'POST') return json({ id: 'new' }, 201)
      return json({}, 404)
    })

    render(<ClustersPage onClustersChanged={changed} />)
    expect(await screen.findByText(/No clusters configured yet/)).toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: 'Add Cluster' }))

    fireEvent.click(screen.getByRole('button', { name: 'Create Cluster' }))
    expect(useStore.getState().toasts.at(-1)?.message).toBe('Cluster name is required')
    fireEvent.change(screen.getByPlaceholderText('production'), { target: { value: 'New Cluster' } })
    fireEvent.click(screen.getByRole('button', { name: 'Create Cluster' }))
    expect(useStore.getState().toasts.at(-1)?.message).toBe('At least one monitoring URL is required')

    fireEvent.change(screen.getByPlaceholderText('http://nats-1:8222'), { target: { value: 'https://nats:8222' } })
    fireEvent.change(screen.getByPlaceholderText('optional'), { target: { value: 'admin-token' } })
    fireEvent.click(screen.getByRole('button', { name: 'Add server' }))
    const monitoringInputs = screen.getAllByPlaceholderText('http://nats-1:8222')
    fireEvent.change(monitoringInputs[1], { target: { value: 'https://nats-2:8222' } })
    fireEvent.click(monitoringInputs[1].parentElement!.querySelector('button')!)

    const switches = screen.getAllByRole('switch')
    fireEvent.click(switches[0])
    fireEvent.change(screen.getByPlaceholderText('/etc/ssl/certs/ca.pem'), { target: { value: '/etc/ca.pem' } })
    fireEvent.click(screen.getByLabelText(/Skip TLS verification/))
    fireEvent.change(screen.getByPlaceholderText('8080'), { target: { value: '8080, bad, 8081' } })
    fireEvent.click(switches[2])

    fireEvent.change(screen.getByPlaceholderText('nats://nats-1:4222'), { target: { value: 'nats://nats:4222' } })
    fireEvent.click(screen.getByRole('button', { name: 'Add URL' }))
    const natsInputs = screen.getAllByPlaceholderText('nats://nats-1:4222')
    fireEvent.change(natsInputs[1], { target: { value: 'nats://nats-2:4222' } })
    fireEvent.click(natsInputs[1].parentElement!.querySelector('button')!)

    const auth = screen.getByRole('combobox')
    fireEvent.change(auth, { target: { value: 'username_password' } })
    fireEvent.change(screen.getByPlaceholderText('user'), { target: { value: 'collector' } })
    fireEvent.change(screen.getByPlaceholderText('••••••••'), { target: { value: 'password' } })
    fireEvent.change(auth, { target: { value: 'nkey' } })
    fireEvent.change(screen.getByPlaceholderText('SUAM…'), { target: { value: 'SUA123' } })
    fireEvent.change(auth, { target: { value: 'creds_file' } })
    fireEvent.change(screen.getByPlaceholderText('/etc/nats/user.creds'), { target: { value: '/etc/user.creds' } })
    fireEvent.change(auth, { target: { value: 'token' } })
    fireEvent.change(screen.getByPlaceholderText('secret-token'), { target: { value: 'token-value' } })
    fireEvent.change(screen.getByPlaceholderText('$MQTT5'), { target: { value: '$CUSTOM' } })
    fireEvent.click(screen.getByText('Enable $SYS collection').closest('label')!.querySelector('input')!)

    fireEvent.click(screen.getByRole('button', { name: 'Add bridge' }))
    fireEvent.change(screen.getByPlaceholderText('my-bridge'), { target: { value: 'manual' } })
    fireEvent.change(screen.getByPlaceholderText('http://bridge:8080'), { target: { value: 'http://bridge:8080' } })
    fireEvent.change(screen.getByPlaceholderText('token (optional)'), { target: { value: 'bridge-token' } })
    fireEvent.click(screen.getByPlaceholderText('my-bridge').parentElement!.querySelector('button')!)
    fireEvent.click(screen.getByRole('button', { name: 'Add bridge' }))
    fireEvent.change(screen.getByPlaceholderText('my-bridge'), { target: { value: 'manual' } })
    fireEvent.change(screen.getByPlaceholderText('http://bridge:8080'), { target: { value: 'http://bridge:8080' } })

    fireEvent.click(screen.getByRole('button', { name: 'Create Cluster' }))
    await waitFor(() => expect(changed).toHaveBeenCalledTimes(1))
    expect(reads).toBeGreaterThanOrEqual(2)
    const post = fetchMock.mock.calls.find(([, init]) => init?.method === 'POST')
    expect(JSON.parse(String(post?.[1]?.body))).toMatchObject({
      name: 'New Cluster',
      servers: [{ url: 'https://nats:8222' }],
      tls: { ca_file: '/etc/ca.pem', insecure: true },
      mqtt_discovery: { enabled: true, admin_ports: [8080, 8081] },
      nats_conn: { urls: ['nats://nats:4222'], token: 'token-value', subject_prefix: '$CUSTOM', sys_collection: true },
    })
    expect(useStore.getState().toasts.at(-1)).toMatchObject({ type: 'success' })
  })

  it('supports keyboard section controls and both explicit create-form cancellation paths', async () => {
    vi.spyOn(globalThis, 'fetch').mockImplementation(() => json({ clusters: [] }))
    render(<ClustersPage onClustersChanged={vi.fn()} />)
    expect(await screen.findByText(/No clusters configured yet/)).toBeInTheDocument()

    fireEvent.click(screen.getByRole('button', { name: 'Add Cluster' }))
    const tlsHeader = screen.getByText('TLS').closest('[role="button"]')!
    fireEvent.keyDown(tlsHeader, { key: 'Escape' })
    fireEvent.keyDown(tlsHeader, { key: 'Enter' })
    fireEvent.keyDown(tlsHeader, { key: ' ' })
    fireEvent.click(screen.getAllByRole('button', { name: 'Cancel' }).at(-1)!)
    expect(screen.queryByRole('heading', { name: 'New Cluster' })).not.toBeInTheDocument()

    fireEvent.click(screen.getByRole('button', { name: 'Add Cluster' }))
    fireEvent.click(screen.getAllByRole('button', { name: 'Cancel' })[0])
    expect(screen.queryByRole('heading', { name: 'New Cluster' })).not.toBeInTheDocument()
  })

  it('edits and deletes a redacted cluster while preserving secret placeholders', async () => {
    const changed = vi.fn()
    const fetchMock = vi.spyOn(globalThis, 'fetch').mockImplementation((input, init) => {
      const url = String(input)
      if (url === '/api/admin/clusters' && !init?.method) return json({ clusters: [richCluster] })
      if (url === '/api/admin/clusters/cluster-1' && init?.method === 'PUT') return json({ ok: true })
      if (url === '/api/admin/clusters/cluster-1' && init?.method === 'DELETE') return json({ ok: true })
      return json({}, 404)
    })

    render(<ClustersPage onClustersChanged={changed} />)
    expect(await screen.findByText('Production')).toBeInTheDocument()
    expect(screen.getByText('NATS Push')).toBeInTheDocument()
    expect(screen.getByText('Discovery')).toBeInTheDocument()
    fireEvent.click(screen.getByTitle('Edit cluster'))
    const modal = screen.getByRole('heading', { name: /Edit Cluster/ }).parentElement!.parentElement!
    expect(within(modal).getAllByPlaceholderText('•••• set — leave blank to keep')).toHaveLength(2)

    for (const title of ['TLS', 'MachMQTT Discovery', 'NATS Push Collection']) {
      fireEvent.click(within(modal).getByText(title).closest('[role="button"]')!)
    }
    expect(within(modal).getByPlaceholderText('/etc/ssl/certs/ca.pem')).toHaveValue('/etc/ca.pem')
    expect(within(modal).getAllByPlaceholderText('•••• set — leave blank to keep')).toHaveLength(3)
    fireEvent.change(within(modal).getByPlaceholderText('production'), { target: { value: 'Production 2' } })
    fireEvent.click(within(modal).getByRole('button', { name: 'Save Changes' }))
    await waitFor(() => expect(fetchMock).toHaveBeenCalledWith('/api/admin/clusters/cluster-1', expect.objectContaining({ method: 'PUT' })))
    expect(changed).toHaveBeenCalledTimes(1)

    fireEvent.click(screen.getByTitle('Delete cluster'))
    await waitFor(() => expect(fetchMock).toHaveBeenCalledWith('/api/admin/clusters/cluster-1', expect.objectContaining({ method: 'DELETE' })))
    expect(window.confirm).toHaveBeenCalledWith(expect.stringContaining('Production'))
    expect(changed).toHaveBeenCalledTimes(2)
  })

  it('maps absent options and every redacted NATS credential type into the editor', async () => {
    const variants = [
      {
        id: 'none', name: 'No Options', servers: [], created_at: '2026-01-01T00:00:00Z',
        tls: null, mqtt_discovery: null, mqtt_bridges: null, nats_conn: null,
      },
      {
        id: 'token', name: 'Token Auth', servers: [{ url: 'http://one:8222' }], created_at: '2026-01-01T00:00:00Z',
        nats_conn: { urls: [], has_token: true },
      },
      {
        id: 'nkey', name: 'NKey Auth', servers: [{ url: 'http://one:8222' }], created_at: '2026-01-01T00:00:00Z',
        nats_conn: { urls: ['nats://one:4222'], has_nkey: true },
      },
      {
        id: 'creds', name: 'Creds Auth', servers: [{ url: 'http://one:8222' }], created_at: '2026-01-01T00:00:00Z',
        nats_conn: { urls: ['nats://one:4222'], has_creds: true },
      },
    ]
    vi.spyOn(globalThis, 'fetch').mockImplementation(() => json({ clusters: variants }))
    render(<ClustersPage onClustersChanged={vi.fn()} />)
    expect(await screen.findByText('No Options')).toBeInTheDocument()

    for (let index = 0; index < variants.length; index++) {
      fireEvent.click(screen.getAllByTitle('Edit cluster')[index])
      const modal = screen.getByRole('heading', { name: /Edit Cluster/ }).parentElement!.parentElement!
      if (index === 0) {
        expect(within(modal).getByPlaceholderText('http://nats-1:8222')).toHaveValue('')
      } else {
        fireEvent.click(within(modal).getByText('NATS Push Collection').closest('[role="button"]')!)
        expect(within(modal).getByRole('combobox')).toHaveValue(['token', 'nkey', 'creds_file'][index - 1])
        expect(within(modal).getByPlaceholderText('•••• set — leave blank to keep')).toBeInTheDocument()
      }
      fireEvent.click(within(modal).getByRole('button', { name: 'Cancel' }))
    }
  })

  it('reports load, save, create, and delete failures without false success', async () => {
    let mode = 'load-http'
    vi.spyOn(globalThis, 'fetch').mockImplementation((_input, init) => {
      if (mode === 'load-http') return json({ error: 'bad' }, 500)
      if (mode === 'load-network') return Promise.reject(new Error('offline'))
      if (!init?.method) return json({ clusters: [richCluster] })
      if (mode === 'mutation-network') return Promise.reject(new Error('offline'))
      return json({ error: `rejected ${init.method}` }, 400)
    })
    const view = render(<ClustersPage onClustersChanged={vi.fn()} />)
    await waitFor(() => expect(useStore.getState().toasts.at(-1)?.message).toBe('Failed to load clusters'))
    view.unmount()

    mode = 'load-network'
    const second = render(<ClustersPage onClustersChanged={vi.fn()} />)
    await waitFor(() => expect(useStore.getState().toasts.at(-1)?.message).toBe('Network error loading clusters'))
    second.unmount()

    mode = 'mutations-http'
    render(<ClustersPage onClustersChanged={vi.fn()} />)
    expect(await screen.findByText('Production')).toBeInTheDocument()
    fireEvent.click(screen.getByTitle('Edit cluster'))
    fireEvent.click(screen.getByRole('button', { name: 'Save Changes' }))
    await waitFor(() => expect(useStore.getState().toasts.at(-1)?.message).toBe('rejected PUT'))
    fireEvent.click(screen.getByRole('button', { name: 'Cancel' }))
    fireEvent.click(screen.getByTitle('Delete cluster'))
    await waitFor(() => expect(useStore.getState().toasts.at(-1)?.message).toBe('rejected DELETE'))

    fireEvent.click(screen.getByRole('button', { name: 'Add Cluster' }))
    fireEvent.change(screen.getByPlaceholderText('production'), { target: { value: 'Rejected' } })
    fireEvent.change(screen.getByPlaceholderText('http://nats-1:8222'), { target: { value: 'http://nats:8222' } })
    fireEvent.click(screen.getByRole('button', { name: 'Create Cluster' }))
    await waitFor(() => expect(useStore.getState().toasts.at(-1)?.message).toBe('rejected POST'))

    mode = 'mutation-network'
    fireEvent.click(screen.getByRole('button', { name: 'Create Cluster' }))
    await waitFor(() => expect(useStore.getState().toasts.at(-1)?.message).toBe('Network error'))
  })

  it('uses stable fallback messages when mutation errors have empty bodies', async () => {
    vi.spyOn(globalThis, 'fetch').mockImplementation((_input, init) => {
      if (!init?.method) return json({ clusters: [richCluster] })
      return json({}, 400)
    })
    render(<ClustersPage onClustersChanged={vi.fn()} />)
    expect(await screen.findByText('Production')).toBeInTheDocument()

    fireEvent.click(screen.getByTitle('Edit cluster'))
    fireEvent.click(screen.getByRole('button', { name: 'Save Changes' }))
    await waitFor(() => expect(useStore.getState().toasts.at(-1)?.message).toBe('Failed to update cluster'))
    fireEvent.click(screen.getByRole('button', { name: 'Cancel' }))

    fireEvent.click(screen.getByTitle('Delete cluster'))
    await waitFor(() => expect(useStore.getState().toasts.at(-1)?.message).toBe('Failed to delete cluster'))

    fireEvent.click(screen.getByRole('button', { name: 'Add Cluster' }))
    fireEvent.change(screen.getByPlaceholderText('production'), { target: { value: 'Empty error' } })
    fireEvent.change(screen.getByPlaceholderText('http://nats-1:8222'), { target: { value: 'http://nats:8222' } })
    fireEvent.click(screen.getByRole('button', { name: 'Create Cluster' }))
    await waitFor(() => expect(useStore.getState().toasts.at(-1)?.message).toBe('Failed to create cluster'))
  })
})
