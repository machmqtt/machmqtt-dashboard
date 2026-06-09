import { NavLink } from 'react-router-dom'
import { useStore } from '../../store/store'
import {
  LayoutDashboard, Network, Cable, GitBranch,
  Database, Users, UserCog, Server, LogOut, Moon, Sun, PanelLeftClose, PanelLeft,
  Radio, Plug, ScrollText,
} from 'lucide-react'

const navItems = [
  { to: '/', icon: LayoutDashboard, label: 'Overview' },
  { to: '/topology', icon: Network, label: 'Topology' },
  { to: '/connections', icon: Cable, label: 'Connections' },
  { to: '/subscriptions', icon: GitBranch, label: 'Subscriptions' },
  { to: '/jetstream', icon: Database, label: 'JetStream' },
  { to: '/accounts', icon: Users, label: 'Accounts' },
]

interface Props {
  username: string
  role: string
  version: string
  onLogout: () => void
}

export function Sidebar({ username, role, version, onLogout }: Props) {
  const { activeEnv, environments, setActiveEnv, darkMode, toggleDarkMode, sidebarOpen, toggleSidebar } = useStore()

  if (!sidebarOpen) {
    return (
      <button
        onClick={toggleSidebar}
        className="fixed top-4 left-4 z-50 bg-brand-sidebar text-white p-2 rounded-lg shadow-lg hover:bg-brand-sidebar/90"
        title="Open sidebar"
      >
        <PanelLeft className="w-5 h-5" />
      </button>
    )
  }

  return (
    <aside className="fixed top-0 left-0 h-screen w-64 bg-brand-sidebar text-white flex flex-col z-40">
      <div className="p-4 border-b border-white/10">
        <div className="flex items-center justify-between mb-3">
          <div className="flex items-center gap-2">
            <Server className="w-6 h-6 text-brand-blue" />
            <span className="font-semibold text-lg">MachMQTT Dashboard</span>
          </div>
          <button onClick={toggleSidebar} className="text-white/50 hover:text-white" title="Collapse sidebar">
            <PanelLeftClose className="w-4 h-4" />
          </button>
        </div>
        {environments.length === 0 ? (
          <div className="text-xs text-white/50 italic px-1 py-1.5">No clusters — add one in Cluster Management</div>
        ) : (
          <select
            value={activeEnv}
            onChange={(e) => setActiveEnv(e.target.value)}
            className="w-full bg-white/10 rounded px-2 py-1.5 text-sm outline-none"
          >
            {environments.map((env) => (
              <option key={env.id} value={env.id} className="bg-brand-sidebar">{env.name}</option>
            ))}
          </select>
        )}
      </div>

      <nav className="flex-1 py-2">
        {navItems.map(({ to, icon: Icon, label }) => (
          <NavLink
            key={to}
            to={to}
            end={to === '/'}
            className={({ isActive }) =>
              `flex items-center gap-3 px-4 py-2.5 text-sm transition-colors ${
                isActive ? 'bg-white/10 text-brand-blue' : 'text-white/70 hover:text-white hover:bg-white/5'
              }`
            }
          >
            <Icon className="w-4 h-4" />
            {label}
          </NavLink>
        ))}

        <div className="mx-4 my-2 border-t border-white/10" />
        <div className="px-4 py-1 text-[10px] text-white/40 uppercase tracking-wider">MachMQTT Fleet</div>
        <NavLink
          to="/mqtt"
          end
          className={({ isActive }) =>
            `flex items-center gap-3 px-4 py-2.5 text-sm transition-colors ${
              isActive ? 'bg-white/10 text-brand-blue' : 'text-white/70 hover:text-white hover:bg-white/5'
            }`
          }
        >
          <Radio className="w-4 h-4" />
          Overview
        </NavLink>
        <NavLink
          to="/mqtt/connections"
          className={({ isActive }) =>
            `flex items-center gap-3 px-4 py-2.5 text-sm transition-colors ${
              isActive ? 'bg-white/10 text-brand-blue' : 'text-white/70 hover:text-white hover:bg-white/5'
            }`
          }
        >
          <Plug className="w-4 h-4" />
          Connections
        </NavLink>

        {role === 'admin' && (
          <>
            <div className="mx-4 my-2 border-t border-white/10" />
            <NavLink
              to="/admin/clusters"
              className={({ isActive }) =>
                `flex items-center gap-3 px-4 py-2.5 text-sm transition-colors ${
                  isActive ? 'bg-white/10 text-brand-blue' : 'text-white/70 hover:text-white hover:bg-white/5'
                }`
              }
            >
              <Server className="w-4 h-4" />
              Cluster Management
            </NavLink>
            <NavLink
              to="/admin/users"
              className={({ isActive }) =>
                `flex items-center gap-3 px-4 py-2.5 text-sm transition-colors ${
                  isActive ? 'bg-white/10 text-brand-blue' : 'text-white/70 hover:text-white hover:bg-white/5'
                }`
              }
            >
              <UserCog className="w-4 h-4" />
              User Management
            </NavLink>
            <NavLink
              to="/admin/logs"
              className={({ isActive }) =>
                `flex items-center gap-3 px-4 py-2.5 text-sm transition-colors ${
                  isActive ? 'bg-white/10 text-brand-blue' : 'text-white/70 hover:text-white hover:bg-white/5'
                }`
              }
            >
              <ScrollText className="w-4 h-4" />
              Server Logs
            </NavLink>
          </>
        )}
      </nav>

      <div className="p-4 border-t border-white/10 space-y-3">
        <button
          onClick={toggleDarkMode}
          className="flex items-center gap-2 text-sm text-white/70 hover:text-white w-full"
        >
          {darkMode ? <Sun className="w-4 h-4" /> : <Moon className="w-4 h-4" />}
          {darkMode ? 'Light Mode' : 'Dark Mode'}
        </button>
        <div className="flex items-center justify-between">
          <div>
            <span className="text-sm text-white/70">{username}</span>
            <span className="text-xs text-white/40 ml-2">{role}</span>
          </div>
          <button onClick={onLogout} className="text-white/50 hover:text-white" title="Logout">
            <LogOut className="w-4 h-4" />
          </button>
        </div>
        <div className="text-[10px] text-white/30 text-center pt-1">
          version {version}
        </div>
      </div>
    </aside>
  )
}
