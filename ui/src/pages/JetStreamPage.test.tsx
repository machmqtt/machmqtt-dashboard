import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { MemoryRouter } from 'react-router'
import { useStore } from '../store/store'
import { JetStreamPage } from './JetStreamPage'

function json(data: unknown, status = 200) {
  return Promise.resolve(new Response(JSON.stringify(data), { status }))
}

const consumer = {
  stream_name: 'ORDERS', name: 'worker',
  config: { durable_name: 'worker', filter_subject: 'orders.new', deliver_policy: 'all', ack_policy: 'explicit' },
  delivered: { consumer_seq: 4, stream_seq: 4 }, ack_floor: { consumer_seq: 3, stream_seq: 3 },
  num_ack_pending: 1, num_redelivered: 2, num_waiting: 3, num_pending: 4,
}

function stream(name: string, age: number, replicas = 1) {
  return {
    name,
    config: { subjects: [`${name.toLowerCase()}.*`], retention: 'limits', storage: 'file', num_replicas: replicas, max_msgs: 100, max_bytes: 1024, max_age: age, discard: 'old' },
    state: { messages: 10, bytes: 1024, consumer_count: name === 'ORDERS' ? 1 : 0, first_seq: 1, last_seq: 10 },
    ...(replicas > 1 ? { cluster: { name: 'raft-orders', leader: 'leader-one', replicas: [
      { name: 'leader-one', current: true }, { name: 'follower-two', current: false, lag: 2 }, { name: 'offline-three', current: false, offline: true },
    ] } } : {}),
    consumer_detail: name === 'ORDERS' ? [consumer] : [],
  }
}

describe('JetStreamPage cluster semantics', () => {
  beforeEach(() => {
    useStore.setState({
      activeEnv: 'prod', environments: [{ id: 'prod', name: 'Production' }], toasts: [],
      overview: { server_count: 2, healthy_count: 2, connection_count: 0, in_msgs_rate: 0, out_msgs_rate: 0, in_bytes_rate: 0, out_bytes_rate: 0, subscriptions: 0, js_streams: 0, js_consumers: 0, js_messages: 0, js_bytes: 0, servers: [
        { id: 's1', name: 'leader-one', version: '', connections: 0, cpu: 0, mem: 0, in_msgs_rate: 0, out_msgs_rate: 0, healthy: true, uptime: '' },
        { id: 's2', name: 'follower-two', version: '', connections: 0, cpu: 0, mem: 0, in_msgs_rate: 0, out_msgs_rate: 0, healthy: true, uptime: '' },
      ] },
    })
  })

  it('deduplicates replicas, renders RAFT health, and exercises filters and expansion', async () => {
    const ordersLeader = stream('ORDERS', 30e9, 3)
    const ordersFollower = { ...stream('ORDERS', 30e9, 3), state: { ...stream('ORDERS', 30e9, 3).state, messages: 8 } }
    vi.spyOn(globalThis, 'fetch').mockImplementation(() => json({
      s1: {
        streams: 4, consumers: 1, messages: 40, bytes: 4096, memory: 100, storage: 200,
        reserved_memory: 300, reserved_storage: 400, api: { total: 20, errors: 2 },
        config: { domain: 'D', max_memory: 1000, max_storage: 2000 },
        meta_cluster: { name: 'META', leader: 'leader-one', cluster_size: 3, replicas: [
          { name: 'leader-one', current: true }, { name: 'follower-two', current: false, lag: 3 }, { name: 'offline-three', current: false, offline: true },
        ] },
        account_details: [{ name: 'A', memory: 0, storage: 0, stream_detail: [ordersLeader, stream('SECONDS', 30e9), stream('MINUTES', 120e9)] }],
      },
      s2: {
        streams: 3, consumers: 1, messages: 28, bytes: 3072, memory: 50, storage: 75,
        api: { total: 10, errors: 0 }, config: { domain: 'D' },
        meta_cluster: { cluster_size: 3, replicas: [{ name: 'follower-two', current: true }] },
        account_details: [
          { name: 'A', memory: 0, storage: 0, stream_detail: [ordersFollower, stream('HOURS', 7200e9)] },
          { name: 'B', memory: 0, storage: 0, stream_detail: [stream('DAYS', 172800e9)] },
        ],
      },
    }))

    render(<MemoryRouter><JetStreamPage /></MemoryRouter>)
    expect(await screen.findByText('Meta Cluster: META')).toBeInTheDocument()
    expect(screen.getByText('API Errors').nextElementSibling).toHaveTextContent('2')
    expect(screen.getAllByText(/offline/).length).toBeGreaterThan(0)
    expect(screen.getAllByText(/lag/).length).toBeGreaterThan(0)
    expect(screen.getAllByText('ORDERS')).toHaveLength(1)

    fireEvent.change(screen.getByRole('combobox'), { target: { value: 'B' } })
    expect(screen.getByText('DAYS')).toBeInTheDocument()
    expect(screen.queryByText('ORDERS')).not.toBeInTheDocument()
    fireEvent.change(screen.getByRole('combobox'), { target: { value: '' } })

    for (const name of ['ORDERS', 'SECONDS', 'MINUTES', 'HOURS', 'DAYS']) {
      fireEvent.click(screen.getByRole('button', { name: new RegExp(name) }))
      if (name === 'ORDERS') expect(screen.getByText('Consumers (1)')).toBeInTheDocument()
      else expect(screen.getByText('No consumers.')).toBeInTheDocument()
      fireEvent.click(screen.getByRole('button', { name: new RegExp(name) }))
    }

    const leaderGroup = screen.getByRole('button', { name: /leader-one.*streams/ })
    fireEvent.click(leaderGroup)
    expect(screen.queryByText('ORDERS')).not.toBeInTheDocument()
    fireEvent.click(leaderGroup)
    expect(screen.getByText('ORDERS')).toBeInTheDocument()
  })

  it('uses aggregate fallback and reserved capacity when stream detail is absent', async () => {
    vi.spyOn(globalThis, 'fetch').mockImplementation(() => json({
      s1: { streams: 2, consumers: 3, messages: 4, bytes: 5, memory: 6, storage: 7, reserved_memory: 8, reserved_storage: 9, api: { total: 0, errors: 0 }, account_details: [] },
    }))
    render(<MemoryRouter><JetStreamPage /></MemoryRouter>)
    expect(await screen.findByText('No JetStream streams found.')).toBeInTheDocument()
    expect(screen.getByText('Streams').nextElementSibling).toHaveTextContent('2')
    expect(screen.getByText('Memory Store').parentElement).toHaveTextContent('reserved')
    expect(screen.getByText('File Store').parentElement).toHaveTextContent('reserved')
  })

  it('reports network failure and ignores a late response after environment removal', async () => {
    let resolve!: (value: Response) => void
    vi.spyOn(globalThis, 'fetch')
      .mockImplementationOnce(() => Promise.reject(new Error('offline')))
      .mockImplementationOnce(() => new Promise<Response>((r) => { resolve = r }))
    const first = render(<MemoryRouter><JetStreamPage /></MemoryRouter>)
    await waitFor(() => expect(useStore.getState().toasts.at(-1)?.message).toBe('Failed to load JetStream data'))
    first.unmount()

    const second = render(<MemoryRouter><JetStreamPage /></MemoryRouter>)
    useStore.setState({ activeEnv: '', environments: [] })
    second.rerender(<MemoryRouter><JetStreamPage /></MemoryRouter>)
    resolve(new Response(JSON.stringify({ stale: {} }), { status: 200 }))
    expect(await screen.findByText('Add a NATS cluster to monitor JetStream streams, consumers, and storage.')).toBeInTheDocument()
  })
})
