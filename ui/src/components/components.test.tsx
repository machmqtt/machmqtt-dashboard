/* eslint-disable @typescript-eslint/no-explicit-any */
import { Component, type ReactNode } from 'react'
import { fireEvent, render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter } from 'react-router'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { ColumnFilter } from './ColumnFilter'
import { ErrorBoundary } from './ErrorBoundary'
import { NodeDetailPanel } from './NodeDetailPanel'
import { TimeRangeSelector } from './TimeRangeSelector'
import { TimeSeriesChart } from './TimeSeriesChart'
import { Toasts } from './Toasts'
import { useStore } from '../store/store'

const chartProps: Record<string, any[]> = {}
vi.mock('recharts', async () => {
  const React = await import('react')
  const component = (name: string) => ({ children, ...props }: { children?: ReactNode }) => {
    chartProps[name] = [...(chartProps[name] || []), props]
    return React.createElement('div', { 'data-testid': name }, children)
  }
  return {
    ResponsiveContainer: component('ResponsiveContainer'), LineChart: component('LineChart'),
    CartesianGrid: component('CartesianGrid'), XAxis: component('XAxis'), YAxis: component('YAxis'),
    Tooltip: component('Tooltip'), Line: component('Line'),
  }
})

describe('small dashboard components', () => {
  beforeEach(() => {
    for (const key of Object.keys(chartProps)) delete chartProps[key]
    useStore.setState({ toasts: [] })
  })

  it('filters a table column without propagating header clicks', () => {
    const setFilterValue = vi.fn()
    const parentClick = vi.fn()
    const column = { getFilterValue: () => 'current', setFilterValue }
    render(<div onClick={parentClick}><ColumnFilter column={column as any} /></div>)
    const input = screen.getByPlaceholderText('Filter...')
    expect(input).toHaveValue('current')
    fireEvent.click(input)
    expect(parentClick).not.toHaveBeenCalled()
    fireEvent.change(input, { target: { value: 'next' } })
    fireEvent.change(input, { target: { value: '' } })
    expect(setFilterValue).toHaveBeenNthCalledWith(1, 'next')
    expect(setFilterValue).toHaveBeenNthCalledWith(2, undefined)
  })

  it('shows topology node details, formatted rates, and server navigation', async () => {
    const onClose = vi.fn()
    const { rerender } = render(<MemoryRouter><NodeDetailPanel node={{
      id: 'n1', name: '', type: 'server', connections: 3, healthy: true,
      in_msgs_rate: 1_250, out_msgs_rate: 2_500_000, cluster: 'east',
    }} onClose={onClose} /></MemoryRouter>)
    expect(screen.getByText('n1')).toBeInTheDocument()
    expect(screen.getByText('Healthy')).toBeInTheDocument()
    expect(screen.getByText('1.3K')).toBeInTheDocument()
    expect(screen.getByText('2.5M')).toBeInTheDocument()
    expect(screen.getByRole('link', { name: 'View Server Detail' })).toHaveAttribute('href', '/servers/n1')
    await userEvent.click(screen.getByRole('button'))
    expect(onClose).toHaveBeenCalled()

    rerender(<MemoryRouter><NodeDetailPanel node={{
      id: 'm1', name: 'mqtt', type: 'mqtt', connections: 0, healthy: false,
      in_msgs_rate: 12, out_msgs_rate: 0,
    }} onClose={onClose} /></MemoryRouter>)
    expect(screen.getByText('Unhealthy')).toBeInTheDocument()
    expect(screen.queryByText('Cluster')).not.toBeInTheDocument()
    expect(screen.queryByRole('link')).not.toBeInTheDocument()
  })

  it('selects all supported time ranges and reflects the active one', async () => {
    const onChange = vi.fn()
    render(<TimeRangeSelector value="6h" onChange={onChange} />)
    for (const range of ['1h', '6h', '24h']) await userEvent.click(screen.getByRole('button', { name: range }))
    expect(onChange.mock.calls.map(([range]) => range)).toEqual(['1h', '6h', '24h'])
    expect(screen.getByRole('button', { name: '6h' })).toHaveClass('bg-brand-blue')
  })

  it('renders empty and populated charts and invokes formatter callbacks', () => {
    const { rerender } = render(<TimeSeriesChart data={[]} lines={[]} height={123} />)
    expect(screen.getByText('No data yet')).toHaveStyle({ height: '123px' })

    rerender(<TimeSeriesChart data={[{ ts: 1, cpu: 2.34 }]} lines={[
      { key: 'cpu', label: 'CPU', color: 'red' },
      { key: 'mem', label: 'Memory', color: 'blue' },
    ]} />)
    expect(chartProps.Line).toHaveLength(2)
    expect(chartProps.ResponsiveContainer[0].height).toBe(200)
    expect(chartProps.XAxis[0].tickFormatter(1)).toEqual(expect.any(String))
    expect(chartProps.YAxis[0].tickFormatter(2.34)).toBe('2.3')
    expect(chartProps.Tooltip[0].labelFormatter(1)).toEqual(expect.any(String))
    expect(chartProps.Tooltip[0].formatter(2.34, 'cpu')).toEqual(['2.3', 'CPU'])
    expect(chartProps.Tooltip[0].formatter(5, 'missing')).toEqual(['5.0', 'missing'])

    rerender(<TimeSeriesChart data={[{ ts: 1, cpu: 2 }]} lines={[]} yFormatter={(value) => `${value}%`} />)
    expect(chartProps.YAxis.at(-1).tickFormatter(2)).toBe('2%')
  })

  it('renders every toast style and removes a selected toast', async () => {
    useStore.setState({ toasts: [
      { id: 1, message: 'info', type: 'info' },
      { id: 2, message: 'error', type: 'error' },
      { id: 3, message: 'success', type: 'success' },
    ] })
    const { rerender } = render(<Toasts />)
    expect(screen.getByText('info').parentElement).toHaveClass('bg-blue-500')
    expect(screen.getByText('error').parentElement).toHaveClass('bg-red-500')
    expect(screen.getByText('success').parentElement).toHaveClass('bg-green-500')
    await userEvent.click(screen.getAllByRole('button')[1])
    expect(useStore.getState().toasts.map((toast) => toast.id)).toEqual([1, 3])
    useStore.setState({ toasts: [] })
    rerender(<Toasts />)
    expect(screen.queryByText('info')).not.toBeInTheDocument()
  })
})

let shouldThrow = true
function UnstableChild() {
  if (shouldThrow) throw new Error('broken child')
  return <div>recovered</div>
}

class BoundaryHarness extends Component<{ fallback?: ReactNode }> {
  render() { return <ErrorBoundary fallback={this.props.fallback}><UnstableChild /></ErrorBoundary> }
}

describe('ErrorBoundary', () => {
  it('logs a failure and can retry the default fallback', async () => {
    shouldThrow = true
    const error = vi.spyOn(console, 'error').mockImplementation(() => undefined)
    render(<BoundaryHarness />)
    expect(screen.getByText('Something went wrong')).toBeInTheDocument()
    expect(screen.getByText('broken child')).toBeInTheDocument()
    shouldThrow = false
    await userEvent.click(screen.getByRole('button', { name: 'Try Again' }))
    expect(screen.getByText('recovered')).toBeInTheDocument()
    expect(error).toHaveBeenCalled()
  })

  it('renders a caller-provided fallback', () => {
    shouldThrow = true
    vi.spyOn(console, 'error').mockImplementation(() => undefined)
    render(<BoundaryHarness fallback={<div>custom fallback</div>} />)
    expect(screen.getByText('custom fallback')).toBeInTheDocument()
  })
})
