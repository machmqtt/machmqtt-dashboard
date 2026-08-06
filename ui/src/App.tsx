import { lazy, Suspense, useCallback, useEffect, useState } from 'react'
import { BrowserRouter, Navigate, Route, Routes } from 'react-router'
import { ErrorBoundary } from './components/ErrorBoundary'
import { Shell } from './components/layout/Shell'
import { useAuth, type User } from './hooks/useAuth'
import { useWebSocket } from './hooks/useWebSocket'
import { ChangePasswordPage } from './pages/ChangePasswordPage'
import { LoginPage } from './pages/LoginPage'
import { useStore, type ClusterInfo } from './store/store'
import { fetchWithTimeout } from './utils/fetchWithTimeout'
import './index.css'

const OverviewPage = lazy(() => import('./pages/OverviewPage').then((module) => ({ default: module.OverviewPage })))
const TopologyPage = lazy(() => import('./pages/TopologyPage').then((module) => ({ default: module.TopologyPage })))
const ConnectionsPage = lazy(() => import('./pages/ConnectionsPage').then((module) => ({ default: module.ConnectionsPage })))
const SubscriptionsPage = lazy(() => import('./pages/SubscriptionsPage').then((module) => ({ default: module.SubscriptionsPage })))
const JetStreamPage = lazy(() => import('./pages/JetStreamPage').then((module) => ({ default: module.JetStreamPage })))
const AccountsPage = lazy(() => import('./pages/AccountsPage').then((module) => ({ default: module.AccountsPage })))
const ServerDetailPage = lazy(() => import('./pages/ServerDetailPage').then((module) => ({ default: module.ServerDetailPage })))
const UsersPage = lazy(() => import('./pages/UsersPage').then((module) => ({ default: module.UsersPage })))
const ClustersPage = lazy(() => import('./pages/ClustersPage').then((module) => ({ default: module.ClustersPage })))
const LogsPage = lazy(() => import('./pages/LogsPage').then((module) => ({ default: module.LogsPage })))
const MQTTOverviewPage = lazy(() => import('./pages/MQTTOverviewPage').then((module) => ({ default: module.MQTTOverviewPage })))
const MQTTConnectionsPage = lazy(() => import('./pages/MQTTConnectionsPage').then((module) => ({ default: module.MQTTConnectionsPage })))
const MQTTBridgeDetailPage = lazy(() => import('./pages/MQTTBridgeDetailPage').then((module) => ({ default: module.MQTTBridgeDetailPage })))
const MQTTAllConnectionsPage = lazy(() => import('./pages/MQTTAllConnectionsPage').then((module) => ({ default: module.MQTTAllConnectionsPage })))

function RouteFallback() {
  return <div className="p-6 text-gray-500 dark:text-gray-400">Loading view...</div>
}

function AuthenticatedApp({ user, onLogout }: { user: User; onLogout: () => void }) {
  const { setEnvironments, setActiveEnv, activeEnv } = useStore()
  const [version, setVersion] = useState('dev')
  useWebSocket()

  const refreshEnvironments = useCallback(() => {
    fetchWithTimeout('/api/environments')
      .then((response) => response.ok ? response.json() : null)
      .then((data) => {
        if (!data || !Array.isArray(data.environments)) return
        const environments: ClusterInfo[] = data.environments
        setEnvironments(environments)
        if (!environments.some((environment) => environment.id === activeEnv)) {
          setActiveEnv(environments.length > 0 ? environments[0].id : '')
        }
      })
      .catch(() => {})
  }, [activeEnv, setActiveEnv, setEnvironments])

  useEffect(() => {
    const controller = new AbortController()
    refreshEnvironments()
    fetchWithTimeout('/api/version', { signal: controller.signal })
      .then((response) => response.ok ? response.json() : null)
      .then((data) => { if (data) setVersion(data.version || 'dev') })
      .catch(() => {})
    return () => controller.abort()
  }, [refreshEnvironments])

  return (
    <Suspense fallback={<RouteFallback />}>
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
          {user.role === 'admin' && <Route path="/admin/users" element={<UsersPage />} />}
          {user.role === 'admin' && <Route path="/admin/clusters" element={<ClustersPage onClustersChanged={refreshEnvironments} />} />}
          {user.role === 'admin' && <Route path="/admin/logs" element={<LogsPage />} />}
        </Route>
        <Route path="*" element={<Navigate to="/" replace />} />
      </Routes>
    </Suspense>
  )
}

export default function App() {
  const { user, providers, loading, login, logout } = useAuth()
  const darkMode = useStore((state) => state.darkMode)

  if (loading) {
    return (
      <div className={darkMode ? 'dark' : ''}>
        <div className="min-h-screen flex items-center justify-center bg-gray-50 dark:bg-gray-900">
          <div className="text-gray-500 dark:text-gray-400 animate-pulse">Loading...</div>
        </div>
      </div>
    )
  }

  return (
    <ErrorBoundary>
      <BrowserRouter>
        {user ? (
          user.auth_provider === 'local' && user.must_change_password ? (
            <ChangePasswordPage userId={user.id} onChanged={() => window.location.reload()} />
          ) : (
            <AuthenticatedApp user={user} onLogout={logout} />
          )
        ) : (
          <Routes>
            <Route path="/login/local" element={<LoginPage localOnly onLogin={(username, password) => login(username, password, true)} />} />
            <Route path="*" element={<LoginPage providers={providers} onLogin={(username, password) => login(username, password)} />} />
          </Routes>
        )}
      </BrowserRouter>
    </ErrorBoundary>
  )
}
