import { render, screen } from '@testing-library/react'
import { MemoryRouter, Route, Routes } from 'react-router'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { useStore, type Overview } from '../store/store'
import { OverviewPage } from './OverviewPage'
import { ServerDetailPage } from './ServerDetailPage'
import { MQTTOverviewPage } from './MQTTOverviewPage'
import { MQTTBridgeDetailPage } from './MQTTBridgeDetailPage'

const formatted: string[] = []
vi.mock('../components/TimeSeriesChart', () => ({
  TimeSeriesChart: ({ yFormatter }: { yFormatter?: (value: number) => string }) => {
    if (yFormatter) for (const value of [0, 0.25, 2, 2_000, 2_000_000, 2_000_000_000]) formatted.push(yFormatter(value))
    return <div>chart</div>
  },
}))

vi.mock('../hooks/useMetrics', () => ({
  useMetrics: () => ({ data: [], loading: false, range: '1h', setRange: vi.fn() }),
}))

function json(data: unknown) {
  return Promise.resolve(new Response(JSON.stringify(data), { headers: { 'Content-Type': 'application/json' } }))
}

function renderRoute(element: React.ReactNode, path = '/', pattern = '*') {
  return render(<MemoryRouter initialEntries={[path]}><Routes><Route path={pattern} element={element} /></Routes></MemoryRouter>)
}

describe('page-provided metric formatters', () => {
  beforeEach(() => {
    formatted.length = 0
    useStore.setState({ activeEnv: 'test', environments: [{ id: 'test', name: 'Test' }], overview: null })
  })

  it('exercises overview and server formatter ranges', async () => {
    useStore.setState({ overview: {
      server_count: 1, healthy_count: 1, connection_count: 2, in_msgs_rate: 0.25, out_msgs_rate: 0,
      in_bytes_rate: 2, out_bytes_rate: 2_000, subscriptions: 2_000_000, js_streams: 1,
      js_consumers: 1, js_messages: 2_000_000, js_bytes: 2_000_000_000,
      servers: [{ id: 's1', name: '', version: '1', connections: 2, cpu: 1, mem: 2,
        in_msgs_rate: 0.25, out_msgs_rate: 0, healthy: true, uptime: '1s' }],
    } as Overview })
    const overview = renderRoute(<OverviewPage />)
    expect(screen.getAllByText('chart')).toHaveLength(2)
    expect(formatted).toEqual(expect.arrayContaining(['0', '0.25', '2', '2.0K', '2.0M']))
    overview.unmount()

    vi.spyOn(globalThis, 'fetch').mockImplementation((input) => String(input).endsWith('/varz') ? json({ s1: {
      server_id: 's1', server_name: '', version: '1', go: 'go', host: 'localhost', port: 4222,
      max_connections: 2, connections: 1, total_connections: 1, routes: 0, leafnodes: 0,
      in_msgs: 1, out_msgs: 1, in_bytes: 2, out_bytes: 2_000, mem: 2_000_000_000,
      cpu: 1, cores: 1, subscriptions: 1, slow_consumers: 0, uptime: '1s',
    } }) : json({}))
    renderRoute(<ServerDetailPage />, '/servers/s1', '/servers/:id')
    expect(await screen.findByRole('heading', { name: 's1' })).toBeInTheDocument()
    expect(formatted).toEqual(expect.arrayContaining(['0 B', '2 B', '2.0 KB', '2.0 MB', '2.0 GB', '0.25']))
  })

  it('exercises MQTT overview and bridge diagnostic formatter ranges', async () => {
    vi.spyOn(globalThis, 'fetch').mockImplementation((input) => {
      const url = String(input)
      if (url.endsWith('/mqtt/bridges')) return json({ bridges: [{
        ip: '127.0.0.1', server_id: 's1', server_name: 'n1', configured_name: 'b1', admin_url: '',
        reachable: true, pool_connections: 0, total_subs: 0, total_in_msgs: 0, total_out_msgs: 0,
        total_in_bytes: 0, total_out_bytes: 0, in_msgs_rate: 0.25, out_msgs_rate: 0,
        in_bytes_rate: 2, out_bytes_rate: 2_000_000_000, status: { ready: true, nats_connected: true, connections: 0 },
      }] })
      if (url.endsWith('/diag')) return json({ connection: {}, streams: [], kv_buckets: [] })
      if (url.endsWith('/metrics')) return json({ connections_active: 0, msgs_recv_qos0: 0, msgs_recv_qos1: 0, msgs_recv_qos2: 0 })
      if (url.endsWith('/pool')) return json({ size: 0, slots: [] })
      if (url.endsWith('/license')) return json({ status: 'valid', max_connections: 1, max_qos: 0, connections_local: 0, connections_global: 0, instances: 0 })
      if (url.endsWith('/diag/config')) return json({ config: {} })
      return json({})
    })
    const overview = renderRoute(<MQTTOverviewPage />)
    expect(await screen.findByText('b1')).toBeInTheDocument()
    expect(formatted).toEqual(expect.arrayContaining(['0', '0.25', '2', '2.0K', '2.0M']))
    overview.unmount()

    renderRoute(<MQTTBridgeDetailPage />, '/mqtt/b1/detail', '/mqtt/:bridge/detail')
    expect(await screen.findByRole('heading', { name: 'b1' })).toBeInTheDocument()
    expect(formatted).toEqual(expect.arrayContaining(['0', '0.25', '2', '2.0K', '2.0M']))
  })
})
