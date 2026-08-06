import { act } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { useStore, type HealthStatus, type Overview, type TopologyData } from './store'

const overview = { servers: [] } as unknown as Overview
const topology = { nodes: [], links: [] } satisfies TopologyData
const health = { n1: { status: 'ok' } } satisfies HealthStatus

describe('dashboard store', () => {
  beforeEach(() => {
    localStorage.clear()
    useStore.setState({
      activeEnv: '', environments: [], overview: null, topology: null, health: null,
      darkMode: false, sidebarOpen: true, toasts: [],
    })
  })

  afterEach(() => vi.useRealTimers())

  it('updates environment snapshots and clears stale data on environment changes', () => {
    const state = useStore.getState()
    act(() => {
      state.setEnvironments([{ id: 'dev', name: 'Development' }, { id: 'prod', name: 'Production' }])
      state.setOverview(overview)
      state.setTopology(topology)
      state.setHealth(health)
    })
    expect(useStore.getState()).toMatchObject({ environments: [{ id: 'dev' }, { id: 'prod' }], overview, topology, health })
    act(() => useStore.getState().setActiveEnv('prod'))
    expect(useStore.getState()).toMatchObject({ activeEnv: 'prod', overview: null, topology: null, health: null })
  })

  it('persists dark mode and toggles the sidebar', () => {
    act(() => useStore.getState().toggleDarkMode())
    expect(useStore.getState().darkMode).toBe(true)
    expect(localStorage.getItem('darkMode')).toBe('true')
    act(() => useStore.getState().toggleSidebar())
    expect(useStore.getState().sidebarOpen).toBe(false)
  })

  it('adds, removes, and automatically expires toasts', () => {
    vi.useFakeTimers()
    act(() => useStore.getState().addToast('one', 'info'))
    const first = useStore.getState().toasts[0]
    act(() => vi.advanceTimersByTime(1000))
    act(() => useStore.getState().addToast('two', 'error'))
    const second = useStore.getState().toasts[1]
    expect(useStore.getState().toasts).toHaveLength(2)
    expect(second.id).toBe(first.id + 1)
    act(() => useStore.getState().removeToast(first.id))
    expect(useStore.getState().toasts.map((toast) => toast.message)).toEqual(['two'])
    act(() => vi.advanceTimersByTime(2999))
    expect(useStore.getState().toasts.map((toast) => toast.message)).toEqual(['two'])
    act(() => vi.advanceTimersByTime(1001))
    expect(useStore.getState().toasts).toEqual([])
  })
})
