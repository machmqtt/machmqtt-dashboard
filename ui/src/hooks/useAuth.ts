import { useState, useEffect, useCallback } from 'react'
import { fetchWithTimeout } from '../utils/fetchWithTimeout'

export interface User {
  id: number
  username: string
  role: 'admin' | 'viewer'
  auth_provider: string
  must_change_password: boolean
}

export interface AuthProvider {
  name: string
  type: 'ldap' | 'oidc'
  login_url?: string
}

export function useAuth() {
  const [user, setUser] = useState<User | null>(null)
  const [providers, setProviders] = useState<AuthProvider[]>([])
  const [loading, setLoading] = useState(true)

  const checkSession = useCallback(async (signal?: AbortSignal) => {
    try {
      const [sessionRes, providersRes] = await Promise.all([
		fetchWithTimeout('/api/me', { signal }),
		fetchWithTimeout('/api/auth/providers', { signal }),
      ])
      if (sessionRes.ok) {
        setUser(await sessionRes.json())
      } else {
        setUser(null)
      }
      if (providersRes.ok) {
        const data = await providersRes.json()
        setProviders(data.providers || [])
      } else {
        setProviders([])
      }
		} catch {
			if (signal?.aborted) return
      setUser(null)
      setProviders([])
    } finally {
			if (!signal?.aborted) setLoading(false)
    }
  }, [])

	useEffect(() => {
		const controller = new AbortController()
		// eslint-disable-next-line react-hooks/set-state-in-effect -- asynchronous session hydration is intentional
		void checkSession(controller.signal)
		return () => controller.abort()
	}, [checkSession])

  const login = async (username: string, password: string, localOnly = false) => {
    const endpoint = localOnly ? '/api/auth/local/login' : '/api/login'
		const res = await fetchWithTimeout(endpoint, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ username, password }),
    })
    if (!res.ok) throw new Error('Invalid credentials')
    const u = await res.json()
    setUser(u)
    return u
  }

  const logout = async () => {
		await fetchWithTimeout('/api/logout', { method: 'POST' })
    setUser(null)
  }

  return { user, providers, loading, login, logout }
}
