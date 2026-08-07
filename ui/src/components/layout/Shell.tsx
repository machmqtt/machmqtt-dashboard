import { Outlet } from 'react-router'
import { Sidebar } from './Sidebar'
import { Toasts } from '../Toasts'
import { ErrorBoundary } from '../ErrorBoundary'
import { useStore } from '../../store/store'

interface Props {
  username: string
  role: string
  version: string
  onLogout: () => void
}

export function Shell({ username, role, version, onLogout }: Props) {
  const { darkMode, sidebarOpen } = useStore()

  return (
    <div className={darkMode ? 'dark' : ''}>
      <div className="min-h-screen bg-gray-50 dark:bg-gray-900 dark:text-gray-100">
        <Sidebar username={username} role={role} version={version} onLogout={onLogout} />
        <main className={`transition-[margin] duration-200 p-6 ${sidebarOpen ? 'ml-64' : 'ml-14'}`}>
          <ErrorBoundary>
            <Outlet />
          </ErrorBoundary>
        </main>
        <Toasts />
      </div>
    </div>
  )
}
