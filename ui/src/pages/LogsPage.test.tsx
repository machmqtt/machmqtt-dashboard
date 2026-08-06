import { act, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { LogsPage } from './LogsPage'

function json(data: unknown, status = 200) {
  return Promise.resolve(new Response(JSON.stringify(data), { status }))
}

describe('LogsPage', () => {
  afterEach(() => vi.useRealTimers())

  it('renders formatted entries, refreshes, and controls polling', async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true })
    const fetchMock = vi.spyOn(globalThis, 'fetch').mockImplementation(() => json({ logs: [
      { time: '2026-01-02T03:04:05Z', level: 'WARN', msg: 'slow request', attrs: { ms: 12, ok: false } },
      { time: 'not-a-date', level: 'CUSTOM', msg: 'custom' },
    ] }))
    render(<LogsPage />)
    expect(await screen.findByText('slow request')).toBeInTheDocument()
    expect(screen.getByText('ms=12 ok=false')).toBeInTheDocument()
    expect(screen.getByText('CUSTOM')).toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: 'Refresh' }))
    await waitFor(() => expect(fetchMock.mock.calls.length).toBeGreaterThanOrEqual(2))
    await act(async () => { vi.advanceTimersByTime(3000) })
    await waitFor(() => expect(fetchMock.mock.calls.length).toBeGreaterThanOrEqual(3))
    fireEvent.click(screen.getByRole('checkbox'))
    const before = fetchMock.mock.calls.length
    await act(async () => { vi.advanceTimersByTime(6000) })
    expect(fetchMock.mock.calls.length).toBe(before)
  })

  it('shows empty state and tolerates HTTP and network failures', async () => {
    const fetchMock = vi.spyOn(globalThis, 'fetch')
      .mockImplementationOnce(() => json({}, 500))
      .mockImplementationOnce(() => Promise.reject(new Error('offline')))
      .mockImplementation(() => json({ logs: [] }))
    render(<LogsPage />)
    expect(await screen.findByText('No log entries captured yet.')).toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: 'Refresh' }))
    await waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(2))
    fireEvent.click(screen.getByRole('button', { name: 'Refresh' }))
    await waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(3))
    expect(screen.getByText(/Showing the last 0 entries/)).toBeInTheDocument()
  })
})
