import { useState, type FormEvent } from 'react'
import { Server } from 'lucide-react'
import { Link } from 'react-router'
import type { AuthProvider } from '../hooks/useAuth'

interface Props {
  onLogin: (username: string, password: string) => Promise<void>
  onOIDCLogin?: (url: string) => void
  localOnly?: boolean
  providers?: AuthProvider[]
}

export function LoginPage({
  onLogin,
  onOIDCLogin = (url) => window.location.assign(url),
  localOnly = false,
  providers = [],
}: Props) {
  const [username, setUsername] = useState('')
  const [password, setPassword] = useState('')
  const [error, setError] = useState('')
  const [loading, setLoading] = useState(false)
  const oidcProviders = localOnly ? [] : providers.filter((provider) => provider.type === 'oidc')
  const showPasswordLogin = localOnly || providers.length === 0 || providers.some((provider) => provider.type === 'ldap')

  const handleSubmit = async (e: FormEvent) => {
    e.preventDefault()
    setError('')
    setLoading(true)
    try {
      await onLogin(username, password)
    } catch {
      setError('Invalid credentials')
    } finally {
      setLoading(false)
    }
  }

  return (
    <div className="min-h-screen bg-brand-dark flex items-center justify-center">
      <div className="bg-white rounded-lg shadow-xl p-8 w-96">
        <div className="flex items-center justify-center gap-2 mb-6">
          <Server className="w-8 h-8 text-brand-blue" />
          <h1 className="text-2xl font-semibold">MachMQTT Dashboard</h1>
        </div>
        <p className="text-center text-sm text-gray-500 mb-5">
          {localOnly ? 'Local administrator sign in' : 'Sign in to continue'}
        </p>
        {oidcProviders.length > 0 && (
          <div className="space-y-3 mb-5">
            {oidcProviders.map((provider) => (
              <button
                key={provider.name}
                type="button"
                onClick={() => { if (provider.login_url) onOIDCLogin(provider.login_url) }}
                className="w-full border border-nats-blue text-nats-blue rounded py-2 font-medium hover:bg-blue-50"
              >
                Sign in with {provider.name}
              </button>
            ))}
          </div>
        )}
        {oidcProviders.length > 0 && showPasswordLogin && (
          <div className="flex items-center gap-3 mb-5 text-xs text-gray-400">
            <span className="h-px bg-gray-200 flex-1" />
            <span>or</span>
            <span className="h-px bg-gray-200 flex-1" />
          </div>
        )}
        {showPasswordLogin && <form onSubmit={handleSubmit} className="space-y-4">
          <div>
            <label htmlFor="login-username" className="block text-sm font-medium text-gray-700 mb-1">Username</label>
            <input
              id="login-username"
              type="text"
              autoComplete="username"
              value={username}
              onChange={(e) => setUsername(e.target.value)}
              className="w-full border rounded px-3 py-2 outline-none focus:ring-2 focus:ring-brand-blue"
              autoFocus
            />
          </div>
          <div>
            <label htmlFor="login-password" className="block text-sm font-medium text-gray-700 mb-1">Password</label>
            <input
              id="login-password"
              type="password"
              autoComplete="current-password"
              value={password}
              onChange={(e) => setPassword(e.target.value)}
              className="w-full border rounded px-3 py-2 outline-none focus:ring-2 focus:ring-brand-blue"
            />
          </div>
          {error && <p className="text-red-500 text-sm">{error}</p>}
          <button
            type="submit"
            disabled={loading}
            className="w-full bg-brand-blue text-white rounded py-2 font-medium hover:opacity-90 disabled:opacity-50"
          >
            {loading ? 'Signing in...' : 'Sign In'}
          </button>
        </form>}
        <div className="mt-5 text-center text-sm">
          {localOnly ? (
            <Link to="/login" className="text-nats-blue hover:underline">
              Return to organization sign in
            </Link>
          ) : (
            <Link to="/login/local" className="text-gray-500 hover:text-nats-blue hover:underline">
              Local administrator login
            </Link>
          )}
        </div>
      </div>
    </div>
  )
}
