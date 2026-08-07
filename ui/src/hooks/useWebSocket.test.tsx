import { act, renderHook, waitFor } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { useStore } from '../store/store'
import { fetchWithTimeout } from '../utils/fetchWithTimeout'
import { useWebSocket } from './useWebSocket'

vi.mock('../utils/fetchWithTimeout', () => ({ fetchWithTimeout: vi.fn() }))

class MockWebSocket {
  static instances: MockWebSocket[] = []
  readonly url: string
  sent: string[] = []
  close = vi.fn()
  onopen: (() => void) | null = null
  onmessage: ((event: MessageEvent<string>) => void) | null = null
  onclose: (() => void) | null = null

  constructor(url: string | URL) {
    this.url = String(url)
    MockWebSocket.instances.push(this)
  }

  send(value: string) { this.sent.push(value) }
  open() { this.onopen?.() }
  message(value: unknown) { this.onmessage?.(new MessageEvent('message', { data: String(value) })) }
  closed() { this.onclose?.() }
}

describe('useWebSocket', () => {
  beforeEach(() => {
    MockWebSocket.instances = []
    vi.stubGlobal('WebSocket', MockWebSocket)
    useStore.setState({ activeEnv: '', overview: null, topology: null, health: null })
    // Default to a failing seed so the socket-focused tests below observe only
    // what the WebSocket itself puts in the store.
    vi.mocked(fetchWithTimeout).mockReset()
    vi.mocked(fetchWithTimeout).mockRejectedValue(new Error('seed unavailable'))
  })

  afterEach(() => {
    vi.useRealTimers()
    vi.unstubAllGlobals()
  })

  it('does nothing without an active environment', () => {
    const { unmount } = renderHook(() => useWebSocket())
    expect(MockWebSocket.instances).toHaveLength(0)
    unmount()
  })

  // A page served over HTTPS must not open a plaintext ws:// socket: the
  // browser blocks it as mixed content and the session cookie would ride an
  // unencrypted upgrade request.
  it.each([
    ['https:', 'wss://localhost:3000/api/ws'],
    ['http:', 'ws://localhost:3000/api/ws'],
  ])('matches the socket scheme to the page protocol %s', (protocol, expected) => {
    vi.stubGlobal('location', { ...window.location, protocol, host: 'localhost:3000' })
    useStore.setState({ activeEnv: 'prod' })
    const { unmount } = renderHook(() => useWebSocket())
    expect(MockWebSocket.instances[0].url).toBe(expected)
    unmount()
  })

  it('subscribes and applies only valid messages for the active environment', () => {
    useStore.setState({ activeEnv: 'prod' })
    const { unmount } = renderHook(() => useWebSocket())
    const socket = MockWebSocket.instances[0]
    expect(socket.url).toBe('ws://localhost:3000/api/ws')

    act(() => socket.open())
    expect(socket.sent).toEqual([JSON.stringify({ subscribe: 'prod' })])

    act(() => {
      socket.message(JSON.stringify({ env: 'other', type: 'overview', data: { ignored: true } }))
      socket.message('not-json')
      socket.message(JSON.stringify({ env: 'prod', type: 'unknown', data: {} }))
      socket.message(JSON.stringify({ env: 'prod', type: 'overview', data: { server_count: 2 } }))
      socket.message(JSON.stringify({ env: 'prod', type: 'topology', data: { nodes: [], links: [] } }))
      socket.message(JSON.stringify({ env: 'prod', type: 'health', data: { n1: { status: 'ok' } } }))
    })
    expect(useStore.getState().overview).toMatchObject({ server_count: 2 })
    expect(useStore.getState().topology).toEqual({ nodes: [], links: [] })
    expect(useStore.getState().health).toEqual({ n1: { status: 'ok' } })

    unmount()
    expect(socket.close).toHaveBeenCalled()
  })

  it('reconnects with capped exponential backoff and stops after unmount', () => {
    vi.useFakeTimers()
    useStore.setState({ activeEnv: 'prod' })
    const { unmount } = renderHook(() => useWebSocket())
    let socket = MockWebSocket.instances[0]

    for (let attempt = 1; attempt <= 7; attempt += 1) {
      act(() => socket.closed())
      const delay = Math.min(1000 * (2 ** (attempt - 1)), 30000)
      act(() => vi.advanceTimersByTime(delay))
      socket = MockWebSocket.instances[attempt]
      expect(socket).toBeDefined()
    }

    act(() => socket.open())
    act(() => socket.closed())
    act(() => vi.advanceTimersByTime(1000))
    expect(MockWebSocket.instances).toHaveLength(9)

    socket = MockWebSocket.instances[8]
    act(() => socket.closed())
    unmount()
    act(() => socket.closed())
    act(() => vi.runAllTimers())
    expect(MockWebSocket.instances).toHaveLength(9)
  })

  describe('REST seed', () => {
    function seedResponses(overview: unknown, topology: unknown, status = 200) {
      vi.mocked(fetchWithTimeout)
        .mockReset()
        .mockResolvedValueOnce(new Response(JSON.stringify(overview), { status }))
        .mockResolvedValueOnce(new Response(JSON.stringify(topology), { status }))
    }

    it('fills empty live views from REST so the page is not stuck on a skeleton', async () => {
      seedResponses({ server_count: 3 }, { nodes: [{ id: 'n1' }], links: [] })
      useStore.setState({ activeEnv: 'prod' })
      const { unmount } = renderHook(() => useWebSocket())

      await waitFor(() => expect(useStore.getState().overview).toMatchObject({ server_count: 3 }))
      expect(useStore.getState().topology).toEqual({ nodes: [{ id: 'n1' }], links: [] })
      expect(fetchWithTimeout).toHaveBeenCalledWith('/api/environments/prod/overview')
      expect(fetchWithTimeout).toHaveBeenCalledWith('/api/environments/prod/topology')
      unmount()
    })

    // The seed is a fallback, not a source of truth. A WebSocket update that
    // already landed is fresher than the in-flight REST response, so the seed
    // must leave it alone rather than roll the view back.
    it('does not overwrite views the WebSocket already populated', async () => {
      seedResponses({ server_count: 1 }, { nodes: [], links: [] })
      useStore.setState({
        activeEnv: 'prod',
        overview: { server_count: 99 } as never,
        topology: { nodes: [{ id: 'live' }], links: [] } as never,
      })
      const { unmount } = renderHook(() => useWebSocket())

      await waitFor(() => expect(fetchWithTimeout).toHaveBeenCalledTimes(2))
      await act(async () => { await Promise.resolve(); await Promise.resolve() })
      expect(useStore.getState().overview).toMatchObject({ server_count: 99 })
      expect(useStore.getState().topology).toEqual({ nodes: [{ id: 'live' }], links: [] })
      unmount()
    })

    it('leaves the views empty when the seed endpoints fail', async () => {
      seedResponses({ server_count: 3 }, { nodes: [], links: [] }, 503)
      useStore.setState({ activeEnv: 'prod' })
      const { unmount } = renderHook(() => useWebSocket())

      await waitFor(() => expect(fetchWithTimeout).toHaveBeenCalledTimes(2))
      await act(async () => { await Promise.resolve(); await Promise.resolve() })
      expect(useStore.getState().overview).toBeNull()
      expect(useStore.getState().topology).toBeNull()
      unmount()
    })

    it('discards a seed that resolves after unmount', async () => {
      let resolveOverview!: (value: Response) => void
      let resolveTopology!: (value: Response) => void
      vi.mocked(fetchWithTimeout)
        .mockReset()
        .mockReturnValueOnce(new Promise<Response>((resolve) => { resolveOverview = resolve }))
        .mockReturnValueOnce(new Promise<Response>((resolve) => { resolveTopology = resolve }))

      useStore.setState({ activeEnv: 'prod' })
      const { unmount } = renderHook(() => useWebSocket())
      unmount()

      await act(async () => {
        resolveOverview(new Response(JSON.stringify({ server_count: 7 })))
        resolveTopology(new Response(JSON.stringify({ nodes: [], links: [] })))
        await Promise.resolve()
        await Promise.resolve()
      })
      expect(useStore.getState().overview).toBeNull()
      expect(useStore.getState().topology).toBeNull()
    })
  })
})
