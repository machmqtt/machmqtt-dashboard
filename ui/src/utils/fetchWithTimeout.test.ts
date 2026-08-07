import { afterEach, describe, expect, it, vi } from 'vitest'
import { fetchWithTimeout } from './fetchWithTimeout'

describe('fetchWithTimeout', () => {
  afterEach(() => vi.useRealTimers())

  it('forwards options and returns the response', async () => {
    vi.useFakeTimers()
    const response = new Response('{}', { status: 200 })
    const fetchMock = vi.spyOn(globalThis, 'fetch').mockResolvedValue(response)
    await expect(fetchWithTimeout('/ok', { method: 'POST', timeout: 100 })).resolves.toBe(response)
    expect(fetchMock).toHaveBeenCalledWith('/ok', expect.objectContaining({ method: 'POST', signal: expect.any(AbortSignal) }))
    expect(vi.getTimerCount()).toBe(0)
  })

  it('aborts at the deadline and combines a caller signal', async () => {
    vi.useFakeTimers()
    let seenSignal: AbortSignal | undefined
    vi.spyOn(globalThis, 'fetch').mockImplementation((_url, init) => {
      seenSignal = init?.signal as AbortSignal
      return new Promise((_resolve, reject) => {
        seenSignal?.addEventListener('abort', () => reject(new DOMException('aborted', 'AbortError')))
      })
    })
    const caller = new AbortController()
    const pending = fetchWithTimeout('/slow', { timeout: 10, signal: caller.signal })
		const assertion = expect(pending).rejects.toMatchObject({ name: 'AbortError' })
    await vi.advanceTimersByTimeAsync(10)
		await assertion
    expect(seenSignal?.aborted).toBe(true)
  })
})
