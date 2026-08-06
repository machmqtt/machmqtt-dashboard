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
})
