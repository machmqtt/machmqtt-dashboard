import { create } from 'zustand'

export interface ServerSummary {
  id: string
  name: string
  version: string
  connections: number
  cpu: number
  mem: number
  in_msgs_rate: number
  out_msgs_rate: number
  healthy: boolean
  uptime: string
}

export interface Overview {
  server_count: number
  healthy_count: number
  connection_count: number
  in_msgs_rate: number
  out_msgs_rate: number
  in_bytes_rate: number
  out_bytes_rate: number
  subscriptions: number
  js_streams: number
  js_consumers: number
  js_messages: number
  js_bytes: number
  servers: ServerSummary[]
}

export interface TopologyNode {
  id: string
  name: string
  type: 'server' | 'gateway' | 'leaf' | 'mqtt'
  connections: number
  healthy: boolean
  in_msgs_rate: number
  out_msgs_rate: number
  cluster?: string
}

export interface TopologyLink {
  source: string
  target: string
  type: 'route' | 'gateway' | 'leaf' | 'mqtt'
  in_msgs_rate: number
  out_msgs_rate: number
}

export interface TopologyData {
  nodes: TopologyNode[]
  links: TopologyLink[]
}

export interface HealthStatus {
  [serverId: string]: { status: string; error?: string }
}

export interface Toast {
  id: number
  message: string
  type: 'info' | 'error' | 'success'
}

// ClusterInfo is the minimal shape returned by GET /api/environments.
// activeEnv holds the cluster ID (stable); name is the display label.
export interface ClusterInfo {
  id: string
  name: string
}

interface DashboardState {
  activeEnv: string
  environments: ClusterInfo[]
  overview: Overview | null
  topology: TopologyData | null
  health: HealthStatus | null
  darkMode: boolean
  sidebarOpen: boolean
  toasts: Toast[]
  setActiveEnv: (env: string) => void
  setEnvironments: (envs: ClusterInfo[]) => void
  setOverview: (o: Overview) => void
  setTopology: (t: TopologyData) => void
  setHealth: (h: HealthStatus) => void
  toggleDarkMode: () => void
  toggleSidebar: () => void
  addToast: (message: string, type: Toast['type']) => void
  removeToast: (id: number) => void
}

let toastId = 0

export const useStore = create<DashboardState>((set) => ({
  activeEnv: '',
  environments: [] as ClusterInfo[],
  overview: null,
  topology: null,
  health: null,
  darkMode: localStorage.getItem('darkMode') === 'true',
  sidebarOpen: true,
  toasts: [],
  setActiveEnv: (env) => set({ activeEnv: env, overview: null, topology: null, health: null }),
  setEnvironments: (envs) => set({ environments: envs }),
  setOverview: (o) => set({ overview: o }),
  setTopology: (t) => set({ topology: t }),
  setHealth: (h) => set({ health: h }),
  toggleDarkMode: () =>
    set((s) => {
      const next = !s.darkMode
      localStorage.setItem('darkMode', String(next))
      return { darkMode: next }
    }),
  toggleSidebar: () => set((s) => ({ sidebarOpen: !s.sidebarOpen })),
  addToast: (message, type) => {
    const id = ++toastId
    set((s) => ({ toasts: [...s.toasts, { id, message, type }] }))
    setTimeout(() => set((s) => ({ toasts: s.toasts.filter((t) => t.id !== id) })), 4000)
  },
  removeToast: (id) => set((s) => ({ toasts: s.toasts.filter((t) => t.id !== id) })),
}))
