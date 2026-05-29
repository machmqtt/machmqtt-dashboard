import { useEffect, useState } from 'react'
import { fetchWithTimeout } from './utils/fetchWithTimeout'
import { BrowserRouter, Routes, Route, Navigate } from 'react-router-dom'
import { useAuth } from './hooks/useAuth'
import type { User } from './hooks/useAuth'
import { useWebSocket } from './hooks/useWebSocket'
import { useStore } from './store/store'
import { Shell } from './components/layout/Shell'
import { ErrorBoundary } from './components/ErrorBoundary'
import { LoginPage } from './pages/LoginPage'
import { ChangePasswordPage } from './pages/ChangePasswordPage'
import { OverviewPage } from './pages/OverviewPage'
import { TopologyPage } from './pages/TopologyPage'
import { ConnectionsPage } from './pages/ConnectionsPage'
import { SubscriptionsPage } from './pages/SubscriptionsPage'
import { JetStreamPage } from './pages/JetStreamPage'
import { AccountsPage } from './pages/AccountsPage'
import { ServerDetailPage } from './pages/ServerDetailPage'
import { UsersPage } from './pages/UsersPage'
import { MQTTOverviewPage } from './pages/MQTTOverviewPage'
import { MQTTConnectionsPage } from './pages/MQTTConnectionsPage'
import { MQTTBridgeDetailPage } from './pages/MQTTBridgeDetailPage'
import { MQTTAllConnectionsPage } from './pages/MQTTAllConnectionsPage'
import './index.css'

function AuthenticatedApp({ user, onLogout }: { user: User; onLogout: () => void }) {
  const { setEnvironments, setActiveEnv, activeEnv } = useStore()
  const [version, setVersion] = useState('dev')
  useWebSocket()

  useEffect(() => {
    fetchWithTimeout('/api/environments')
      .then((r) => r.ok ? r.json() : null)
      .then((data) => {
        if (!data) return
        const envs: string[] = data.environments || []
        setEnvironments(envs)
        if (envs.length > 0 && !activeEnv) {
          setActiveEnv(envs[0])
        }
      })
      .catch(() => {})
    fetchWithTimeout('/api/version')
      .then((r) => r.ok ? r.json() : null)
      .then((d) => { if (d) setVersion(d.version || 'dev') })
      .catch(() => {})
  }, [setEnvironments, setActiveEnv, activeEnv])

  return (
    <Routes>
      <Route element={<Shell username={user.username} role={user.role} version={version} onLogout={onLogout} />}>
        <Route path="/" element={<OverviewPage />} />
        <Route path="/topology" element={<TopologyPage />} />
        <Route path="/connections" element={<ConnectionsPage />} />
        <Route path="/subscriptions" element={<SubscriptionsPage />} />
        <Route path="/jetstream" element={<JetStreamPage />} />
        <Route path="/accounts" element={<AccountsPage />} />
        <Route path="/servers/:id" element={<ServerDetailPage />} />
        <Route path="/mqtt" element={<MQTTOverviewPage />} />
        <Route path="/mqtt/connections" element={<MQTTAllConnectionsPage />} />
        <Route path="/mqtt/:bridge/connections" element={<MQTTConnectionsPage />} />
        <Route path="/mqtt/:bridge/detail" element={<MQTTBridgeDetailPage role={user.role} />} />
        {user.role === 'admin' && (
          <Route path="/admin/users" element={<UsersPage />} />
        )}
      </Route>
      <Route path="*" element={<Navigate to="/" replace />} />
    </Routes>
  )
}

export default function App() {
  const { user, loading, login, logout } = useAuth()
  const darkMode = useStore((s) => s.darkMode)

  if (loading) {
    return (
      <div className={darkMode ? 'dark' : ''}>
        <div className="min-h-screen flex items-center justify-center bg-gray-50 dark:bg-gray-900">
          <div className="text-gray-500 dark:text-gray-400 animate-pulse">Loading...</div>
        </div>
      </div>
    )
  }

  const handlePasswordChanged = () => {
    window.location.reload()
  }

  return (
    <ErrorBoundary>
      <BrowserRouter>
        {user ? (
          user.must_change_password ? (
            <ChangePasswordPage userId={user.id} onChanged={handlePasswordChanged} />
          ) : (
            <AuthenticatedApp user={user} onLogout={logout} />
          )
        ) : (
          <Routes>
            <Route path="*" element={<LoginPage onLogin={login} />} />
          </Routes>
        )}
      </BrowserRouter>
    </ErrorBoundary>
  )
}
