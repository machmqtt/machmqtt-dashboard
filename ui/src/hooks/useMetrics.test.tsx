import { act, renderHook, waitFor } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { fetchWithTimeout } from '../utils/fetchWithTimeout'
import { useMetrics } from './useMetrics'

vi.mock('../utils/fetchWithTimeout', () => ({ fetchWithTimeout: vi.fn() }))

describe('useMetrics', () => {
  beforeEach(() => {
    vi.mocked(fetchWithTimeout).mockReset()
    vi.spyOn(Date, 'now').mockReturnValue(1_700_000_000_000)
    vi.spyOn(Math, 'random').mockReturnValue(0)
  })

  afterEach(() => vi.useRealTimers())

  it('remains idle without an environment', () => {
    const { result } = renderHook(() => useMetrics(null, 'metrics'))
    expect(result.current).toMatchObject({ data: [], loading: true, range: '1h' })
    expect(fetchWithTimeout).not.toHaveBeenCalled()
  })

  it('fetches points with stable filtered parameters and changes range', async () => {
    vi.mocked(fetchWithTimeout).mockResolvedValue(new Response(JSON.stringify({ points: [{ ts: 1, cpu: 2 }] })))
    const params = { server: 'n1', empty: '' }
    const { result, rerender } = renderHook(
      ({ currentParams }) => useMetrics('prod', 'metrics', currentParams),
      { initialProps: { currentParams: params } },
    )
    await waitFor(() => expect(result.current.loading).toBe(false))
    expect(result.current.data).toEqual([{ ts: 1, cpu: 2 }])
    expect(fetchWithTimeout).toHaveBeenCalledWith(
      '/api/environments/prod/metrics?from=1699996400&to=1700000000&server=n1',
      expect.objectContaining({ signal: expect.any(AbortSignal) }),
    )

    rerender({ currentParams: { empty: '', server: 'n1' } })
    expect(fetchWithTimeout).toHaveBeenCalledTimes(1)
    act(() => result.current.setRange('24h'))
    await waitFor(() => expect(fetchWithTimeout).toHaveBeenCalledTimes(2))
    expect(vi.mocked(fetchWithTimeout).mock.calls[1][0]).toContain('from=1699913600')
  })

  it('handles empty, non-ok, and rejected responses and aborts on cleanup', async () => {
    vi.mocked(fetchWithTimeout)
      .mockResolvedValueOnce(new Response(JSON.stringify({})))
      .mockResolvedValueOnce(new Response(null, { status: 503 }))
      .mockRejectedValueOnce(new Error('offline'))
    const first = renderHook(() => useMetrics('prod', 'metrics'))
    await waitFor(() => expect(first.result.current.loading).toBe(false))
    expect(first.result.current.data).toEqual([])
    first.unmount()

    const second = renderHook(() => useMetrics('prod', 'metrics'))
    await waitFor(() => expect(second.result.current.loading).toBe(false))
    second.unmount()

    const third = renderHook(() => useMetrics('prod', 'metrics'))
    await waitFor(() => expect(third.result.current.loading).toBe(false))
    const signal = vi.mocked(fetchWithTimeout).mock.calls[2][1]?.signal
    third.unmount()
    expect(signal?.aborted).toBe(true)
  })

  it('schedules a jittered refresh and cancels it on cleanup', async () => {
    vi.useFakeTimers()
    vi.mocked(Math.random).mockReturnValue(0.5)
    vi.mocked(fetchWithTimeout).mockResolvedValue(new Response(JSON.stringify({ points: [] })))
    const { unmount } = renderHook(() => useMetrics('prod', 'metrics'))
    await act(async () => { await Promise.resolve(); await Promise.resolve() })
    expect(fetchWithTimeout).toHaveBeenCalledTimes(1)
    await act(async () => { vi.advanceTimersByTime(32_499); await Promise.resolve() })
    expect(fetchWithTimeout).toHaveBeenCalledTimes(1)
    await act(async () => { vi.advanceTimersByTime(1); await Promise.resolve() })
    expect(fetchWithTimeout).toHaveBeenCalledTimes(2)
    unmount()
    await act(async () => { vi.runAllTimers(); await Promise.resolve() })
    expect(fetchWithTimeout).toHaveBeenCalledTimes(2)
  })

  // A transient backend failure must leave the last good series on screen
  // rather than blanking the chart on every hiccup.
  it('retains the last successful series when a refresh fails', async () => {
    vi.mocked(fetchWithTimeout)
      .mockResolvedValueOnce(new Response(JSON.stringify({ points: [{ ts: 1, cpu: 2 }] })))
      .mockResolvedValueOnce(new Response(JSON.stringify({ error: 'upstream unavailable' }), { status: 502 }))
    const { result } = renderHook(() => useMetrics('prod', 'metrics'))
    await waitFor(() => expect(result.current.data).toEqual([{ ts: 1, cpu: 2 }]))

    act(() => result.current.setRange('6h'))
    await waitFor(() => expect(fetchWithTimeout).toHaveBeenCalledTimes(2))
    expect(vi.mocked(fetchWithTimeout).mock.calls[1][0]).toContain('from=1699978400')
    await waitFor(() => expect(result.current.loading).toBe(false))
    expect(result.current.data).toEqual([{ ts: 1, cpu: 2 }])
  })
})
