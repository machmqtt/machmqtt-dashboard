/* eslint-disable @typescript-eslint/no-explicit-any */
import { act, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { forwardRef, useImperativeHandle } from 'react'
import { MemoryRouter } from 'react-router'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { TopologyGraphView } from './TopologyGraph'
import { useStore, type TopologyData } from '../store/store'

const graph = vi.hoisted(() => ({ props: {} as Record<string, any> }))
const methods = vi.hoisted(() => ({
  zoomToFit: vi.fn(), centerAt: vi.fn(() => ({ x: 2, y: 3 })), zoom: vi.fn(() => 4),
}))

vi.mock('react-force-graph-2d', () => ({
  default: forwardRef((props: Record<string, any>, ref) => {
    graph.props = props
    useImperativeHandle(ref, () => methods)
    return <div data-testid="force-graph" />
  }),
}))

const topology: TopologyData = {
  nodes: [
    { id: 's', name: 'Server', type: 'server', connections: 2, healthy: true, in_msgs_rate: 1, out_msgs_rate: 2 },
    { id: 'g', name: 'Gateway', type: 'gateway', connections: 1, healthy: false, in_msgs_rate: 1_000, out_msgs_rate: 2 },
    { id: 'l', name: 'Leaf', type: 'leaf', connections: 1, healthy: true, in_msgs_rate: 1_000_000, out_msgs_rate: 2 },
    { id: 'm', name: 'MQTT', type: 'mqtt', connections: 1, healthy: true, in_msgs_rate: 0.5, out_msgs_rate: 0 },
  ],
  links: [
    { source: 's', target: 'g', type: 'route', in_msgs_rate: 1_000_000, out_msgs_rate: 1 },
    { source: 'g', target: 'l', type: 'gateway', in_msgs_rate: 1_000, out_msgs_rate: 1 },
    { source: 'l', target: 'm', type: 'leaf', in_msgs_rate: 0.5, out_msgs_rate: 0 },
    { source: 'm', target: 's', type: 'mqtt', in_msgs_rate: 0, out_msgs_rate: 0 },
  ],
}

function canvas() {
  return {
    beginPath: vi.fn(), moveTo: vi.fn(), lineTo: vi.fn(), closePath: vi.fn(), arc: vi.fn(), fill: vi.fn(), stroke: vi.fn(),
    setLineDash: vi.fn(), fillText: vi.fn(), roundRect: vi.fn(), measureText: vi.fn(() => ({ width: 12 })),
    fillStyle: '', strokeStyle: '', lineWidth: 0, font: '', textAlign: '', textBaseline: '',
  } as unknown as CanvasRenderingContext2D
}

describe('TopologyGraphView', () => {
  beforeEach(() => {
    useStore.setState({ activeEnv: 'prod', darkMode: false, sidebarOpen: true })
    methods.zoomToFit.mockClear()
  })

  it('loads, draws, selects, drags, zooms, resets, and persists a complete graph', async () => {
    const fetchMock = vi.spyOn(globalThis, 'fetch').mockResolvedValue(new Response(JSON.stringify({
      positions: [{ node_id: 's', x: 1, y: 2 }], camera: { zoom: 2, center_x: 3, center_y: 4 },
    }), { status: 200 }))
    const { rerender } = render(<MemoryRouter><TopologyGraphView data={topology} /></MemoryRouter>)
    await waitFor(() => expect(graph.props.graphData?.nodes).toHaveLength(4))

    const ctx = canvas()
    for (const node of graph.props.graphData.nodes) graph.props.nodeCanvasObject(node, ctx, 1)
    graph.props.nodeCanvasObject({ id: 'none' }, ctx, 1)
    graph.props.nodeCanvasObject({ id: 'none', x: 1, y: 1, type: 'unknown', healthy: false }, ctx, 2)
    graph.props.nodeCanvasObject({ id: 'none', x: null, y: 1 }, ctx, 1)
    graph.props.nodePointerAreaPaint({ x: 1, y: 2 }, '#fff', ctx)
    for (const link of graph.props.graphData.links) {
      link.source = { id: link.source, x: 1, y: 2 }
      link.target = { id: link.target, x: 4, y: 5 }
      graph.props.linkCanvasObject(link, ctx, 1)
      graph.props.linkPointerAreaPaint(link, '#fff', ctx)
    }
    graph.props.linkCanvasObject({ source: {}, target: {} }, ctx, 1)
    graph.props.linkCanvasObject({ source: { id: 'unknown-a', x: 1, y: 1 }, target: { id: 'unknown-b', x: 2, y: 2 }, type: 'unknown' }, ctx, 1)
    graph.props.linkPointerAreaPaint({ source: {}, target: {} }, '#fff', ctx)
    expect(graph.props.linkCanvasObjectMode()).toBe('replace')

    act(() => graph.props.onNodeClick({ id: 's' }))
    expect(screen.getByRole('heading', { name: 'Server' })).toBeInTheDocument()
    fireEvent.click(screen.getAllByRole('button').find((button) => !button.getAttribute('title'))!)

    act(() => graph.props.onLinkClick(graph.props.graphData.links[0]))
    expect(screen.getByText('Connection Detail')).toBeInTheDocument()
    for (const index of [1, 2, 3]) {
      act(() => graph.props.onLinkClick(graph.props.graphData.links[index]))
      expect(screen.getByText('Connection Detail')).toBeInTheDocument()
    }
    act(() => graph.props.onLinkClick({ source: { id: 'missing' }, target: { id: 'other' } }))
    act(() => graph.props.onNodeDrag())
    rerender(<MemoryRouter><TopologyGraphView data={{ ...topology, nodes: topology.nodes.map((node) => node.id === 'm' ? { ...node, name: '' } : node) }} /></MemoryRouter>)
    act(() => graph.props.onLinkClick({ source: 'm', target: 's' }))
    expect(screen.getByText('Connection Detail')).toBeInTheDocument()
    act(() => graph.props.onNodeDragEnd({ id: 's', x: 9, y: 10 }))
    act(() => graph.props.onZoomEnd())
    methods.zoom.mockReturnValueOnce(undefined as unknown as number)
    act(() => graph.props.onZoomEnd())
    methods.centerAt.mockReturnValueOnce(undefined as unknown as { x: number; y: number })
    act(() => graph.props.onZoomEnd())
    fireEvent.click(screen.getByTitle('Center graph in view'))
    fireEvent.click(screen.getByTitle('Reset layout to auto-arrange'))
    act(() => graph.props.onNodeClick({
      id: 'adhoc', name: 'Ad hoc', type: 'server', healthy: true,
      connections: 0, in_msgs_rate: 0, out_msgs_rate: 0,
    }))
    expect(screen.getByRole('heading', { name: 'Ad hoc' })).toBeInTheDocument()
    await waitFor(() => expect(fetchMock).toHaveBeenCalledWith('/api/environments/prod/topology/positions', expect.objectContaining({ method: 'PUT' })))
  })

  it('handles empty, fully saved, failed, and non-ok layouts', async () => {
    vi.spyOn(globalThis, 'fetch').mockRejectedValueOnce(new Error('offline')).mockRejectedValue(new Error('save failed'))
    const view = render(<MemoryRouter><TopologyGraphView data={{ nodes: [], links: [] }} /></MemoryRouter>)
    await waitFor(() => expect(graph.props.graphData?.nodes).toHaveLength(0))
    view.unmount()

    vi.spyOn(globalThis, 'fetch').mockResolvedValue(new Response(JSON.stringify({ positions: topology.nodes.map((n, i) => ({ node_id: n.id, x: i, y: i })) }), { status: 200 }))
    render(<MemoryRouter><TopologyGraphView data={topology} /></MemoryRouter>)
    await waitFor(() => expect(graph.props.graphData?.nodes).toHaveLength(4))
  })

  it('handles non-ok layout fetches, dark drawing, missing links, and live selected-link updates', async () => {
    useStore.setState({ darkMode: true, sidebarOpen: false })
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(new Response(null, { status: 503 }))
    const withMissingLink: TopologyData = {
      ...topology,
      links: [...topology.links, { source: 'missing', target: 'also-missing', type: 'route', in_msgs_rate: 0, out_msgs_rate: 0 }],
    }
    const { rerender } = render(<MemoryRouter><TopologyGraphView data={withMissingLink} /></MemoryRouter>)
    await waitFor(() => expect(graph.props.graphData?.nodes).toHaveLength(4))
    const ctx = canvas()
    graph.props.nodeCanvasObject({ id: 'unknown', name: '', type: 'unknown', healthy: false, x: 1, y: 2 }, ctx, 0.5)
    graph.props.linkCanvasObject({ source: { id: 's', x: 1, y: 1 }, target: { id: 'g', x: 2, y: 2 }, type: 'unknown', in_msgs_rate: 0, out_msgs_rate: 0 }, ctx, 0.5)

    act(() => graph.props.onLinkClick({ source: { id: 's' }, target: { id: 'g' } }))
    expect(screen.getByText('Connection Detail')).toBeInTheDocument()
    const updated: TopologyData = {
      ...topology,
      links: topology.links.map((link, index) => index === 0 ? { ...link, in_msgs_rate: 42 } : link),
    }
    rerender(<MemoryRouter><TopologyGraphView data={updated} /></MemoryRouter>)
    expect(screen.getByText('42')).toBeInTheDocument()
    const panel = screen.getByRole('heading', { name: 'Connection Detail' }).parentElement
    fireEvent.click(panel!.querySelector('button')!)
    expect(screen.queryByText('Connection Detail')).not.toBeInTheDocument()
  })

  it('accepts legacy topology and layout payloads with omitted arrays', async () => {
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(new Response(JSON.stringify({}), { status: 200 }))
    render(<MemoryRouter><TopologyGraphView data={{} as TopologyData} /></MemoryRouter>)
    await waitFor(() => expect(graph.props.graphData).toEqual({ nodes: [], links: [] }))
  })
})
