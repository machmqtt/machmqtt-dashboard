/* eslint-disable react-hooks/refs -- force-graph maintains mutable simulation state outside React */
import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import ForceGraph2D, { type ForceGraphMethods } from 'react-force-graph-2d'
import type { TopologyData, TopologyNode, TopologyLink } from '../store/store'
import { useStore } from '../store/store'
import { NodeDetailPanel } from './NodeDetailPanel'
import { X, RotateCcw, Maximize } from 'lucide-react'
import { formatRate as fmtRate } from '../utils/format'

interface Props {
  data: TopologyData
}

const NODE_COLORS: Record<string, string> = {
  server: '#27aae1',
  gateway: '#8b5cf6',
  leaf: '#22c55e',
  mqtt: '#f59e0b',
}

const LINK_DASH: Record<string, number[]> = {
  route: [],
  gateway: [5, 5],
  leaf: [2, 3],
  mqtt: [4, 2],
}

const NODE_RADIUS = 10

function structureKey(data: TopologyData): string {
  const nk = (data.nodes || []).map((n) => n.id).sort().join(',')
  const lk = (data.links || []).map((l) => `${l.source}>${l.target}`).sort().join(',')
  return nk + '|' + lk
}

type PositionMap = Record<string, { x: number; y: number }>
interface CameraState { zoom: number; center_x: number; center_y: number }
interface SavedLayout { positions: PositionMap; camera: CameraState | null }

async function fetchLayout(env: string): Promise<SavedLayout> {
  try {
    const res = await fetch(`/api/environments/${encodeURIComponent(env)}/topology/positions`)
    if (!res.ok) return { positions: {}, camera: null }
    const data = await res.json()
    const map: PositionMap = {}
    for (const p of data.positions || []) {
      map[p.node_id] = { x: p.x, y: p.y }
    }
    return { positions: map, camera: data.camera || null }
  } catch {
    return { positions: {}, camera: null }
  }
}

async function persistLayout(env: string, positions: PositionMap, camera: CameraState | null) {
  try {
    const arr = Object.entries(positions).map(([node_id, { x, y }]) => ({ node_id, x, y }))
    await fetch(`/api/environments/${encodeURIComponent(env)}/topology/positions`, {
      method: 'PUT',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ positions: arr, camera }),
    })
  } catch { /* best effort */ }
}

// Compute a static layout. If all nodes have saved positions, use those.
// Otherwise, arrange in a circle with generous spacing and run a force sim.
function computeLayout(
  nodes: TopologyData['nodes'],
  links: TopologyData['links'],
  savedPositions: PositionMap,
): Map<string, { x: number; y: number }> {
  const pos = new Map<string, { x: number; y: number }>()

  if (nodes.length === 0) return pos

  if (nodes.every((n) => savedPositions[n.id])) {
    for (const n of nodes) pos.set(n.id, { ...savedPositions[n.id] })
    return pos
  }

  // Initial circle layout with enough spacing that labels don't overlap.
  const circumference = Math.max(400, nodes.length * 100)
  const r = circumference / (2 * Math.PI)

  for (let i = 0; i < nodes.length; i++) {
    const saved = savedPositions[nodes[i].id]
    if (saved) {
      pos.set(nodes[i].id, { ...saved })
    } else {
      const angle = (2 * Math.PI * i) / nodes.length - Math.PI / 2
      pos.set(nodes[i].id, { x: r * Math.cos(angle), y: r * Math.sin(angle) })
    }
  }

  const nodeArr = nodes.map((n) => ({ id: n.id, ...pos.get(n.id)! }))
  const idxMap = new Map(nodeArr.map((n, i) => [n.id, i]))

  for (let iter = 0; iter < 300; iter++) {
    const alpha = 0.8 * (1 - iter / 300)
    if (alpha < 0.01) break

    for (let i = 0; i < nodeArr.length; i++) {
      for (let j = i + 1; j < nodeArr.length; j++) {
        const dx = nodeArr[j].x - nodeArr[i].x
        const dy = nodeArr[j].y - nodeArr[i].y
        const d2 = Math.max(1, dx * dx + dy * dy)
        const d = Math.sqrt(d2)
        const force = -800 * alpha / d2
        const fx = force * dx
        const fy = force * dy
        nodeArr[i].x -= fx
        nodeArr[i].y -= fy
        nodeArr[j].x += fx
        nodeArr[j].y += fy

        if (d < 120) {
          const push = (120 - d) / 2
          const ux = dx / d, uy = dy / d
          nodeArr[i].x -= ux * push
          nodeArr[i].y -= uy * push
          nodeArr[j].x += ux * push
          nodeArr[j].y += uy * push
        }
      }
    }

    for (const link of links) {
      const si = idxMap.get(link.source)
      const ti = idxMap.get(link.target)
      if (si === undefined || ti === undefined) continue
      const dx = nodeArr[ti].x - nodeArr[si].x
      const dy = nodeArr[ti].y - nodeArr[si].y
      const d = Math.sqrt(dx * dx + dy * dy) || 1
      const f = (d - 180) * 0.03 * alpha
      const fx = (f * dx) / d
      const fy = (f * dy) / d
      nodeArr[si].x += fx
      nodeArr[si].y += fy
      nodeArr[ti].x -= fx
      nodeArr[ti].y -= fy
    }

    for (const n of nodeArr) {
      n.x *= 1 - 0.005 * alpha
      n.y *= 1 - 0.005 * alpha
    }
  }

  for (const n of nodeArr) pos.set(n.id, { x: n.x, y: n.y })
  return pos
}

export function TopologyGraphView({ data }: Props) {
  const fgRef = useRef<ForceGraphMethods | undefined>(undefined)
  const [selectedNode, setSelectedNode] = useState<TopologyNode | null>(null)
  const [selectedLink, setSelectedLink] = useState<TopologyLink | null>(null)
  const darkMode = useStore((s) => s.darkMode)
  const sidebarOpen = useStore((s) => s.sidebarOpen)
  const activeEnv = useStore((s) => s.activeEnv)
  const initialFitDone = useRef(false)
  const draggingRef = useRef(false)

  const metricsRef = useRef(new Map<string, TopologyNode>())
  const linkMetricsRef = useRef(new Map<string, TopologyLink>())
  const positionsRef = useRef<PositionMap>({})
  const positionsLoaded = useRef(false)
  const saveTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null)
  const cameraRef = useRef<CameraState | null>(null)
  const savedCameraRef = useRef<CameraState | null>(null)

  // Keep a ref to the latest data so drag-end can access it without a stale closure.
  const dataRef = useRef(data)
  dataRef.current = data

  // Re-render on window resize so the canvas width/height (derived from
  // window.innerWidth/innerHeight below) track the viewport.
  const [, bumpResize] = useState(0)
  useEffect(() => {
    const onResize = () => bumpResize((n) => n + 1)
    window.addEventListener('resize', onResize)
    return () => window.removeEventListener('resize', onResize)
  }, [])

  // Load saved positions + camera from server on mount / env change.
  const [ready, setReady] = useState(false)
  useEffect(() => {
    let cancelled = false
    positionsLoaded.current = false
    // eslint-disable-next-line react-hooks/set-state-in-effect -- reset loading state when the environment changes
    setReady(false)
    fetchLayout(activeEnv).then((layout) => {
      if (cancelled) return
      positionsRef.current = layout.positions
      savedCameraRef.current = layout.camera
      positionsLoaded.current = true
      setReady(true)
    })
    return () => { cancelled = true }
  }, [activeEnv])

  const debouncedSave = useCallback((env: string) => {
    if (saveTimerRef.current) clearTimeout(saveTimerRef.current)
    saveTimerRef.current = setTimeout(() => {
      persistLayout(env, positionsRef.current, cameraRef.current)
    }, 500)
  }, [])

  const curKey = structureKey(data)

  // Update metrics refs (always, even during drag — these don't affect layout).
  for (const n of data.nodes || []) metricsRef.current.set(n.id, n)
  for (const l of data.links || []) linkMetricsRef.current.set(`${l.source}>${l.target}`, l)

  if (selectedLink) {
    const key = `${selectedLink.source}>${selectedLink.target}`
    const fresh = linkMetricsRef.current.get(key)
    if (fresh && (fresh.in_msgs_rate !== selectedLink.in_msgs_rate || fresh.out_msgs_rate !== selectedLink.out_msgs_rate)) {
      setSelectedLink(fresh)
    }
  }

  // Freeze graph structure updates while dragging so nodes don't snap back.
  // Only update the effective key when not dragging; on drag end we sync it.
  const [effectiveKey, setEffectiveKey] = useState('')
  useEffect(() => {
    if (!draggingRef.current) {
      setEffectiveKey(curKey)
    }
  }, [curKey])

  const graphData = useMemo(() => {
    if (!ready) return { nodes: [], links: [] }

    const d = dataRef.current
    const dNodes = d.nodes || []
    const dLinks = d.links || []
    const layout = computeLayout(dNodes, dLinks, positionsRef.current)
    const saved: PositionMap = {}
    for (const [id, p] of layout) saved[id] = p
    positionsRef.current = saved

    const nodes = dNodes.map((n) => {
      const p = layout.get(n.id) || { x: 0, y: 0 }
      return {
        id: n.id, name: n.name, type: n.type,
        connections: n.connections, healthy: n.healthy,
        in_msgs_rate: n.in_msgs_rate, out_msgs_rate: n.out_msgs_rate,
        cluster: n.cluster, val: 1,
        x: p.x, y: p.y, fx: p.x, fy: p.y,
      }
    })

    const links = dLinks.map((l) => ({
      source: l.source, target: l.target, type: l.type,
      in_msgs_rate: l.in_msgs_rate, out_msgs_rate: l.out_msgs_rate,
    }))

    return { nodes, links }
  // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [effectiveKey, ready])

  // Set initial camera once when graph first has nodes.
  useEffect(() => {
    if (!initialFitDone.current && graphData.nodes.length > 0) {
      initialFitDone.current = true
      const cam = savedCameraRef.current
      if (cam && cam.zoom > 0) {
        setTimeout(() => {
          const fg = fgRef.current
          if (!fg) return
          fg.centerAt(cam.center_x, cam.center_y, 0)
          fg.zoom(cam.zoom, 0)
          cameraRef.current = cam
        }, 100)
      } else {
        setTimeout(() => fgRef.current?.zoomToFit?.(300, 200), 100)
      }
    }
  }, [graphData])

  const handleZoomPan = useCallback(() => {
    const fg = fgRef.current
    if (!fg) return
    // eslint-disable-next-line @typescript-eslint/no-explicit-any -- centerAt() getter not in type defs
    const zoom = fg.zoom(), center = (fg as any).centerAt()
    if (zoom == null || center == null) return
    cameraRef.current = { zoom, center_x: center.x, center_y: center.y }
    debouncedSave(activeEnv)
  }, [activeEnv, debouncedSave])

  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  const handleNodeDragEnd = useCallback((node: any) => {
    node.fx = node.x; node.fy = node.y
    positionsRef.current[node.id] = { x: node.x, y: node.y }
    draggingRef.current = false
    debouncedSave(activeEnv)
    // Apply any topology changes that arrived during the drag.
    const latestKey = structureKey(dataRef.current)
    setEffectiveKey(latestKey)
  }, [activeEnv, debouncedSave])

  const [, forceUpdate] = useState(0)
  const handleResetLayout = useCallback(() => {
    positionsRef.current = {}
    cameraRef.current = null
    savedCameraRef.current = null
    persistLayout(activeEnv, {}, null)
    initialFitDone.current = false
    forceUpdate((n) => n + 1)
  }, [activeEnv])

  const handleCenter = useCallback(() => {
    fgRef.current?.zoomToFit?.(300, 200)
  }, [])

  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  const nodeCanvasObject = useCallback((node: any, ctx: CanvasRenderingContext2D, globalScale: number) => {
    const { x, y } = node
    if (x == null || y == null) return
    const m = metricsRef.current.get(node.id) || node
    const color = NODE_COLORS[m.type] || '#999'
    const borderColor = m.healthy ? '#22c55e' : '#ef4444'

    ctx.beginPath()
    if (m.type === 'gateway') {
      ctx.moveTo(x, y - NODE_RADIUS); ctx.lineTo(x + NODE_RADIUS, y); ctx.lineTo(x, y + NODE_RADIUS); ctx.lineTo(x - NODE_RADIUS, y); ctx.closePath()
    } else if (m.type === 'leaf') {
      ctx.moveTo(x, y - NODE_RADIUS); ctx.lineTo(x + NODE_RADIUS, y + NODE_RADIUS * 0.7); ctx.lineTo(x - NODE_RADIUS, y + NODE_RADIUS * 0.7); ctx.closePath()
    } else if (m.type === 'mqtt') {
      const r = NODE_RADIUS
      for (let i = 0; i < 6; i++) {
        const a = (Math.PI / 3) * i - Math.PI / 2
        const px = x + r * Math.cos(a), py = y + r * Math.sin(a)
        if (i === 0) ctx.moveTo(px, py); else ctx.lineTo(px, py)
      }
      ctx.closePath()
    } else {
      ctx.arc(x, y, NODE_RADIUS, 0, 2 * Math.PI)
    }
    ctx.fillStyle = color; ctx.fill()
    ctx.strokeStyle = borderColor; ctx.lineWidth = 2; ctx.stroke()

    const label = m.name || node.id
    const fontSize = Math.max(10, 12 / globalScale)
    ctx.font = `${fontSize}px sans-serif`
    ctx.fillStyle = darkMode ? '#d1d5db' : '#333'
    ctx.textAlign = 'center'
    ctx.fillText(label, x, y + NODE_RADIUS + fontSize + 2)
  }, [darkMode])

  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  const linkCanvasObject = useCallback((link: any, ctx: CanvasRenderingContext2D, globalScale: number) => {
    const src = link.source, tgt = link.target
    if (!src?.x || !tgt?.x) return

    const srcId = typeof src === 'string' ? src : src.id
    const tgtId = typeof tgt === 'string' ? tgt : tgt.id
    const lm = linkMetricsRef.current.get(`${srcId}>${tgtId}`) || link
    const rate = (lm.in_msgs_rate || 0) + (lm.out_msgs_rate || 0)
    const width = Math.max(1, Math.min(4, rate > 0 ? 1 + Math.log10(rate + 1) : 1))

    ctx.beginPath()
    ctx.setLineDash(LINK_DASH[link.type] || [])
    ctx.moveTo(src.x, src.y); ctx.lineTo(tgt.x, tgt.y)
    ctx.strokeStyle = darkMode ? '#555' : '#999'
    ctx.lineWidth = width; ctx.stroke(); ctx.setLineDash([])

    const mx = (src.x + tgt.x) / 2, my = (src.y + tgt.y) / 2
    const label = rate > 0 ? fmtRate(rate) + '/s' : ''
    if (label) {
      const fontSize = Math.max(8, 10 / globalScale)
      ctx.font = `${fontSize}px sans-serif`
      const tw = ctx.measureText(label).width
      const px = 3 / globalScale, py = 1.5 / globalScale
      ctx.fillStyle = darkMode ? 'rgba(31,41,55,0.85)' : 'rgba(255,255,255,0.85)'
      ctx.beginPath()
      ctx.roundRect(mx - tw / 2 - px, my - fontSize / 2 - py, tw + px * 2, fontSize + py * 2, 3 / globalScale)
      ctx.fill()
      ctx.fillStyle = darkMode ? '#9ca3af' : '#666'
      ctx.textAlign = 'center'; ctx.textBaseline = 'middle'
      ctx.fillText(label, mx, my)
    }
  }, [darkMode])

  const sidebarW = sidebarOpen ? 256 : 56
  const w = typeof window !== 'undefined' ? window.innerWidth - sidebarW - 48 : 800
  const h = typeof window !== 'undefined' ? window.innerHeight - 88 - 24 : 600

  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  const handleLinkClick = useCallback((link: any) => {
    const srcId = typeof link.source === 'string' ? link.source : link.source.id
    const tgtId = typeof link.target === 'string' ? link.target : link.target.id
    const lm = linkMetricsRef.current.get(`${srcId}>${tgtId}`)
    if (lm) { setSelectedNode(null); setSelectedLink(lm) }
  }, [])

  const nodeName = useCallback((id: string): string => {
    const m = metricsRef.current.get(id)
    return m?.name || id.slice(0, 12)
  }, [])

  return (
    <div className="relative" style={{ width: w, height: h }}>
      <div className="absolute top-2 right-2 z-10 flex gap-1.5">
        <button
          onClick={handleCenter}
          className="bg-white dark:bg-gray-700 border dark:border-gray-600 rounded px-3 py-1.5 text-xs flex items-center gap-1.5 hover:bg-gray-50 dark:hover:bg-gray-600 shadow-sm"
          title="Center graph in view"
        >
          <Maximize className="w-3 h-3" /> Center
        </button>
        <button
          onClick={handleResetLayout}
          className="bg-white dark:bg-gray-700 border dark:border-gray-600 rounded px-3 py-1.5 text-xs flex items-center gap-1.5 hover:bg-gray-50 dark:hover:bg-gray-600 shadow-sm"
          title="Reset layout to auto-arrange"
        >
          <RotateCcw className="w-3 h-3" /> Reset Layout
        </button>
      </div>

      <ForceGraph2D
        ref={fgRef}
        graphData={graphData}
        nodeId="id"
        nodeVal="val"
        nodeCanvasObject={nodeCanvasObject}
        // eslint-disable-next-line @typescript-eslint/no-explicit-any
        nodePointerAreaPaint={(node: any, color, ctx) => {
          ctx.fillStyle = color; ctx.beginPath()
          ctx.arc(node.x, node.y, NODE_RADIUS + 4, 0, 2 * Math.PI); ctx.fill()
        }}
        linkCanvasObjectMode={() => 'replace'}
        linkCanvasObject={linkCanvasObject}
        // eslint-disable-next-line @typescript-eslint/no-explicit-any
        linkPointerAreaPaint={(link: any, color, ctx) => {
          const src = link.source, tgt = link.target
          if (!src?.x || !tgt?.x) return
          ctx.beginPath(); ctx.moveTo(src.x, src.y); ctx.lineTo(tgt.x, tgt.y)
          ctx.strokeStyle = color; ctx.lineWidth = 10; ctx.stroke()
        }}
        // eslint-disable-next-line @typescript-eslint/no-explicit-any
        onNodeClick={(node: any) => { setSelectedLink(null); setSelectedNode(metricsRef.current.get(node.id) || node) }}
        onNodeDrag={() => { draggingRef.current = true }}
        onNodeDragEnd={handleNodeDragEnd}
        onLinkClick={handleLinkClick}
        onZoomEnd={handleZoomPan}
        enableNodeDrag={true}
        backgroundColor={darkMode ? '#1f2937' : '#ffffff'}
        cooldownTicks={0}
        cooldownTime={0}
        width={w}
        height={h}
      />

      {selectedNode && <NodeDetailPanel node={selectedNode} onClose={() => setSelectedNode(null)} />}

      {selectedLink && (
        <div className="fixed top-0 right-0 h-screen w-[400px] bg-white dark:bg-gray-800 shadow-xl border-l border-gray-200 dark:border-gray-700 z-50 overflow-y-auto">
          <div className="flex items-center justify-between p-4 border-b dark:border-gray-700">
            <h2 className="font-semibold text-lg">Connection Detail</h2>
            <button onClick={() => setSelectedLink(null)} className="text-gray-400 hover:text-gray-600 dark:hover:text-gray-200">
              <X className="w-5 h-5" />
            </button>
          </div>
          <div className="p-4 space-y-4">
            <div className="flex items-center justify-center gap-3 text-sm">
              <span className="font-medium">{nodeName(selectedLink.source)}</span>
              <span className="text-gray-400">---</span>
              <span className={`text-xs rounded px-2 py-0.5 ${
                selectedLink.type === 'route' ? 'bg-blue-100 dark:bg-blue-900 text-blue-700 dark:text-blue-300' :
                selectedLink.type === 'gateway' ? 'bg-purple-100 dark:bg-purple-900 text-purple-700 dark:text-purple-300' :
                'bg-green-100 dark:bg-green-900 text-green-700 dark:text-green-300'
              }`}>{selectedLink.type}</span>
              <span className="text-gray-400">---</span>
              <span className="font-medium">{nodeName(selectedLink.target)}</span>
            </div>
            <div className="grid grid-cols-2 gap-4">
              <div className="bg-gray-50 dark:bg-gray-700 rounded p-3 text-center">
                <div className="text-xs text-gray-500 dark:text-gray-400 mb-1">Msgs In/s</div>
                <div className="text-xl font-semibold text-green-600 dark:text-green-400">{fmtRate(selectedLink.in_msgs_rate)}</div>
              </div>
              <div className="bg-gray-50 dark:bg-gray-700 rounded p-3 text-center">
                <div className="text-xs text-gray-500 dark:text-gray-400 mb-1">Msgs Out/s</div>
                <div className="text-xl font-semibold text-orange-600 dark:text-orange-400">{fmtRate(selectedLink.out_msgs_rate)}</div>
              </div>
            </div>
            <div className="bg-gray-50 dark:bg-gray-700 rounded p-3 text-center">
              <div className="text-xs text-gray-500 dark:text-gray-400 mb-1">Total Throughput</div>
              <div className="text-xl font-semibold">{fmtRate(selectedLink.in_msgs_rate + selectedLink.out_msgs_rate)} msgs/s</div>
            </div>
            <div className="text-xs text-gray-400 text-center">Updates in real-time with each poll cycle</div>
          </div>
        </div>
      )}
    </div>
  )
}
