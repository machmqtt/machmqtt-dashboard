import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter, Route, Routes } from 'react-router'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { useStore } from '../../store/store'
import { Shell } from './Shell'
import { Sidebar } from './Sidebar'

describe('application layout', () => {
  beforeEach(() => {
    useStore.setState({ activeEnv: 'dev', environments: [{ id: 'dev', name: 'Development' }, { id: 'prod', name: 'Production' }], darkMode: false, sidebarOpen: true, toasts: [] })
  })

  it('navigates, changes environment and theme, logs out, and collapses', async () => {
    const onLogout = vi.fn()
    render(<MemoryRouter><Sidebar username="alice" role="admin" version="1.2.3" onLogout={onLogout} /></MemoryRouter>)
    expect(screen.getByRole('link', { name: 'User Management' })).toBeInTheDocument()
    await userEvent.selectOptions(screen.getByRole('combobox'), 'prod')
    expect(useStore.getState().activeEnv).toBe('prod')
    await userEvent.click(screen.getByRole('button', { name: 'Dark Mode' }))
    expect(useStore.getState().darkMode).toBe(true)
    await userEvent.click(screen.getByTitle('Logout'))
    expect(onLogout).toHaveBeenCalled()
    await userEvent.click(screen.getByTitle('Collapse sidebar'))
    expect(screen.getByTitle('Open sidebar')).toBeInTheDocument()
    await userEvent.click(screen.getByTitle('Open sidebar'))
    expect(screen.getByText('MachMQTT Dashboard')).toBeInTheDocument()
  })

  it('hides administration for viewers and renders shell outlet variants', () => {
    const first = render(<MemoryRouter><Sidebar username="viewer" role="viewer" version="dev" onLogout={vi.fn()} /></MemoryRouter>)
    expect(screen.queryByRole('link', { name: 'User Management' })).not.toBeInTheDocument()
    first.unmount()

    useStore.setState({ darkMode: true, sidebarOpen: false })
    const { container } = render(<MemoryRouter initialEntries={['/']}><Routes><Route element={
      <Shell username="viewer" role="viewer" version="dev" onLogout={vi.fn()} />
    }><Route index element={<div>outlet content</div>} /></Route></Routes></MemoryRouter>)
    expect(screen.getByText('outlet content')).toBeInTheDocument()
    expect(container.firstChild).toHaveClass('dark')
    expect(container.querySelector('main')).toHaveClass('ml-14')
  })

  it('explains every degraded collection state and marks active admin routes', () => {
    const renderSidebar = (path: string, environment: Record<string, unknown>) => {
      useStore.setState({
        activeEnv: 'degraded',
        environments: [{ id: 'degraded', name: 'Degraded', degraded: true, ...environment }],
        sidebarOpen: true,
      })
      return render(
        <MemoryRouter initialEntries={[path]}>
          <Sidebar username="admin" role="admin" version="dev" onLogout={vi.fn()} />
        </MemoryRouter>,
      )
    }

    const explicit = renderSidebar('/admin/clusters', { degraded_reason: 'Push collector disconnected' })
    expect(screen.getByText('Push collector disconnected')).toBeInTheDocument()
    expect(screen.getByRole('link', { name: 'Cluster Management' })).toHaveClass('text-brand-blue')
    explicit.unmount()

    const fallback = renderSidebar('/admin/logs', { collection_mode: 'sys-fallback' })
    expect(screen.getByText('Using HTTP fallback ($SYS unavailable)')).toBeInTheDocument()
    expect(screen.getByRole('link', { name: 'Server Logs' })).toHaveClass('text-brand-blue')
    fallback.unmount()

    renderSidebar('/admin/users', {})
    expect(screen.getByText('Collection degraded')).toBeInTheDocument()
    expect(screen.getByRole('link', { name: 'User Management' })).toHaveClass('text-brand-blue')
  })
})
