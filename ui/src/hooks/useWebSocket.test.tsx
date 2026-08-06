import { act, renderHook } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { useStore } from '../store/store'
import { useWebSocket } from './useWebSocket'

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
})
