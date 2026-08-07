import { act, renderHook, waitFor } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'
import { useAuth } from './useAuth'

function json(data: unknown, status = 200) {
  return Promise.resolve(new Response(JSON.stringify(data), { status, headers: { 'Content-Type': 'application/json' } }))
}

describe('useAuth', () => {
  it('hydrates, logs in locally, and logs out', async () => {
    const fetchMock = vi.spyOn(globalThis, 'fetch')
      .mockImplementationOnce(() => json({}, 401))
      .mockImplementationOnce(() => json({ providers: [{ name: 'corp', type: 'ldap' }] }))
      .mockImplementationOnce(() => json({ id: 1, username: 'admin', role: 'admin', auth_provider: 'local', must_change_password: false }))
      .mockImplementationOnce(() => json({ ok: true }))
    const { result } = renderHook(() => useAuth())
    await waitFor(() => expect(result.current.loading).toBe(false))
    expect(result.current.user).toBeNull()
    expect(result.current.providers).toEqual([{ name: 'corp', type: 'ldap' }])
    expect(fetchMock.mock.calls.slice(0, 2).map(([url]) => url)).toEqual(['/api/me', '/api/auth/providers'])
    let loggedIn: Awaited<ReturnType<typeof result.current.login>> | undefined
    await act(async () => { loggedIn = await result.current.login('admin', 'secret', true) })
    expect(loggedIn).toMatchObject({ username: 'admin', auth_provider: 'local' })
    expect(result.current.user).toEqual(loggedIn)
    expect(fetchMock).toHaveBeenNthCalledWith(3, '/api/auth/local/login', expect.objectContaining({
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ username: 'admin', password: 'secret' }),
    }))
    await act(() => result.current.logout())
    expect(result.current.user).toBeNull()
    expect(fetchMock).toHaveBeenNthCalledWith(4, '/api/logout', expect.objectContaining({ method: 'POST' }))
  })

  it('handles hydration failures and rejected credentials', async () => {
    vi.spyOn(globalThis, 'fetch')
      .mockRejectedValueOnce(new Error('offline'))
      .mockRejectedValueOnce(new Error('offline'))
      .mockImplementationOnce(() => json({}, 401))
    const { result } = renderHook(() => useAuth())
    await waitFor(() => expect(result.current.loading).toBe(false))
    expect(result.current.user).toBeNull()
    expect(result.current.providers).toEqual([])
    await expect(result.current.login('bad', 'bad')).rejects.toThrow('Invalid credentials')
    expect(fetch).toHaveBeenLastCalledWith('/api/login', expect.objectContaining({
      method: 'POST',
      body: JSON.stringify({ username: 'bad', password: 'bad' }),
    }))
  })

  it('keeps a valid session when provider discovery is unavailable', async () => {
    vi.spyOn(globalThis, 'fetch')
      .mockImplementationOnce(() => json({ id: 2, username: 'viewer', role: 'viewer', auth_provider: 'oidc', must_change_password: false }))
      .mockImplementationOnce(() => json({}, 503))
    const { result } = renderHook(() => useAuth())
    await waitFor(() => expect(result.current.loading).toBe(false))
    expect(result.current.user?.username).toBe('viewer')
    expect(result.current.providers).toEqual([])
  })

  it('does not clear a valid user when logout fails', async () => {
    vi.spyOn(globalThis, 'fetch')
      .mockImplementationOnce(() => json({ id: 3, username: 'admin', role: 'admin', auth_provider: 'local', must_change_password: false }))
      .mockImplementationOnce(() => json({ providers: [] }))
      .mockRejectedValueOnce(new Error('offline'))
    const { result } = renderHook(() => useAuth())
    await waitFor(() => expect(result.current.user?.username).toBe('admin'))
    await expect(result.current.logout()).rejects.toThrow('offline')
    expect(result.current.user?.username).toBe('admin')
  })

  it('ignores an aborted hydration request after unmount', async () => {
    vi.spyOn(globalThis, 'fetch').mockImplementation((_input, init) => new Promise((_resolve, reject) => {
      init?.signal?.addEventListener('abort', () => reject(new DOMException('aborted', 'AbortError')))
    }))
    const { unmount } = renderHook(() => useAuth())
    unmount()
    await act(async () => { await Promise.resolve(); await Promise.resolve() })
  })

  // Provider discovery that answers 200 without a providers field must yield an
  // empty list; anything else would be rendered as login buttons.
  it('normalises a provider payload that omits the list', async () => {
    vi.spyOn(globalThis, 'fetch')
      .mockImplementationOnce(() => json({}, 401))
      .mockImplementationOnce(() => json({}))
    const { result } = renderHook(() => useAuth())
    await waitFor(() => expect(result.current.loading).toBe(false))
    expect(result.current.providers).toEqual([])
  })

  it('exposes no providers until hydration resolves', () => {
    vi.spyOn(globalThis, 'fetch').mockImplementation(() => new Promise(() => undefined))
    const { result } = renderHook(() => useAuth())
    expect(result.current.providers).toEqual([])
    expect(result.current.loading).toBe(true)
  })

  // Provider discovery is auxiliary. If it fails while /api/me succeeds, the
  // established session must survive rather than the operator being signed out.
  it('keeps an authenticated session when provider discovery fails', async () => {
    vi.spyOn(globalThis, 'fetch')
      .mockImplementationOnce(() => json({ id: 1, username: 'admin', role: 'admin', auth_provider: 'local', must_change_password: false }))
      .mockImplementationOnce(() => Promise.resolve(new Response(null, { status: 500 })))
    const { result } = renderHook(() => useAuth())
    await waitFor(() => expect(result.current.loading).toBe(false))
    expect(result.current.user).toMatchObject({ username: 'admin' })
    expect(result.current.providers).toEqual([])
  })

  // Unmounting must cancel in-flight session checks; otherwise a slow
  // /api/me keeps a credentialed request alive after the view is gone.
  it('cancels in-flight session requests when unmounted', async () => {
    const signals: (AbortSignal | null | undefined)[] = []
    vi.spyOn(globalThis, 'fetch').mockImplementation((_input, init) => {
      signals.push(init?.signal)
      return new Promise(() => undefined)
    })
    const { unmount } = renderHook(() => useAuth())
    await waitFor(() => expect(signals).toHaveLength(2))
    expect(signals.every((signal) => signal instanceof AbortSignal)).toBe(true)
    expect(signals.some((signal) => signal?.aborted)).toBe(false)

    unmount()
    expect(signals.every((signal) => signal?.aborted)).toBe(true)
  })
})
