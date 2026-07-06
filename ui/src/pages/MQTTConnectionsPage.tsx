import { useState, useEffect, useCallback, useMemo, useRef } from 'react'
import { fetchWithTimeout } from '../utils/fetchWithTimeout'
import { useParams, Link } from 'react-router-dom'
import {
  createColumnHelper,
  flexRender,
  getCoreRowModel,
  getSortedRowModel,
  getFilteredRowModel,
  useReactTable,
  type SortingState,
  type ColumnFiltersState,
} from '@tanstack/react-table'
import { ColumnFilter } from '../components/ColumnFilter'
import { useStore } from '../store/store'
import { TableSkeleton } from '../components/Skeleton'
import { ArrowUpDown, ArrowUp, ArrowDown, ArrowLeft } from 'lucide-react'
import { formatNumber as fmtNum, formatBytes as fmtBytes } from '../utils/format'

interface MQTTClient {
  cid: number
  mqtt_client: string
  kind: string
  type: string
  ip: string
  port: number
  start: string
  last_activity: string
  uptime: string
  idle: string
  pending_bytes: number
  in_msgs: number
  out_msgs: number
  in_bytes: number
  out_bytes: number
  subscriptions: number
  lang: string
  is_websocket: boolean
  clean_start: boolean
  keep_alive: number
  session_expiry_interval: number
  receive_maximum: number
  inflight_out: number
  username: string
  state: string
  slow_consumer?: boolean
}

interface ConnzResponse {
  server_id: string
  num_connections: number
  total: number
  offset: number
  limit: number
  connections: MQTTClient[]
}

const col = createColumnHelper<MQTTClient>()
const PAGE_SIZE_OPTIONS = [25, 50, 100, 250]
// Each refresh issues a live admin-API fetch to the bridge, so keep it moderate.
const REFRESH_INTERVAL = 5_000

export function MQTTConnectionsPage() {
  const { bridge } = useParams<{ bridge: string }>()
  const activeEnv = useStore((s) => s.activeEnv)
  const [data, setData] = useState<ConnzResponse | null>(null)
  const [loading, setLoading] = useState(true)
  const [offset, setOffset] = useState(0)
  const [pageSize, setPageSize] = useState(50)
  const [sorting, setSorting] = useState<SortingState>([])
  const [columnFilters, setColumnFilters] = useState<ColumnFiltersState>([])
  const [selected, setSelected] = useState<MQTTClient | null>(null)
  const fetchCounter = useRef(0)

  const [connzError, setConnzError] = useState<string | null>(null)

  const fetchData = useCallback(async () => {
    if (!activeEnv || !bridge) return
    const isInitial = !data
    if (isInitial) setLoading(true)
    try {
      const res = await fetchWithTimeout(`/api/environments/${activeEnv}/mqtt/${encodeURIComponent(bridge)}/connz?limit=${pageSize}&offset=${offset}`)
      if (res.ok) {
        const json = await res.json()
        if (json.error) {
          setConnzError(json.detail || json.error)
          setData(null)
        } else {
          setConnzError(null)
          setData(json)
        }
      }
    } catch { /* ignore */ }
    setLoading(false)
  // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [activeEnv, bridge, offset, pageSize])

  useEffect(() => { fetchData() }, [fetchData])
  useEffect(() => {
    if (!activeEnv || !bridge) return
    const id = setInterval(() => { fetchCounter.current++; fetchData() }, REFRESH_INTERVAL)
    return () => clearInterval(id)
  }, [activeEnv, bridge, fetchData])

  const columns = useMemo(() => [
    col.accessor('mqtt_client', {
      header: 'Client ID',
      cell: (i) => <span className="font-mono text-xs">{i.getValue()}</span>,
    }),
    col.accessor('ip', {
      header: 'IP',
      cell: (i) => <span className="font-mono text-xs">{i.getValue()}:{i.row.original.port}</span>,
    }),
    col.accessor('state', {
      header: 'State',
      cell: (i) => (
        <span className="flex items-center gap-1.5">
          {i.getValue()}
          {i.row.original.slow_consumer && (
            <span className="px-1.5 py-0.5 rounded text-xs font-medium bg-amber-100 text-amber-800 dark:bg-amber-900/40 dark:text-amber-300">
              Slow
            </span>
          )}
        </span>
      ),
    }),
    col.accessor('subscriptions', { header: 'Subs' }),
    col.accessor('in_msgs', { header: 'Msgs In', cell: (i) => fmtNum(i.getValue()) }),
    col.accessor('out_msgs', { header: 'Msgs Out', cell: (i) => fmtNum(i.getValue()) }),
    col.accessor('in_bytes', { header: 'Bytes In', cell: (i) => fmtBytes(i.getValue()) }),
    col.accessor('out_bytes', { header: 'Bytes Out', cell: (i) => fmtBytes(i.getValue()) }),
    col.accessor('inflight_out', { header: 'Inflight' }),
    col.accessor('uptime', { header: 'Uptime' }),
    col.accessor('idle', { header: 'Idle' }),
    col.accessor('username', { header: 'User', cell: (i) => i.getValue() || '-' }),
  ], [])

  const table = useReactTable({
    data: data?.connections || [],
    columns,
    state: { sorting, columnFilters },
    onSortingChange: setSorting,
    onColumnFiltersChange: setColumnFilters,
    getCoreRowModel: getCoreRowModel(),
    getSortedRowModel: getSortedRowModel(),
    getFilteredRowModel: getFilteredRowModel(),
  })

  // num_connections is the current connection count in the bridge snapshot. The
  // connz `total` field is a cumulative lifetime CONNECT counter (machmqtt's
  // socket-split clarified this), so it must NOT be used for pagination.
  const total = data?.num_connections ?? 0
  const totalPages = Math.max(1, Math.ceil(total / pageSize))
  const currentPage = Math.floor(offset / pageSize) + 1
  const hasNext = offset + pageSize < total
  const hasPrev = offset > 0

  return (
    <div>
      <div className="flex items-center gap-3 mb-4">
        <Link to="/mqtt" className="text-gray-400 hover:text-gray-600 dark:hover:text-gray-200">
          <ArrowLeft className="w-5 h-5" />
        </Link>
        <h1 className="text-2xl font-semibold">{bridge} — MQTT Connections</h1>
        <span className="text-xs text-gray-400">Auto-refreshes every {REFRESH_INTERVAL / 1000}s</span>
      </div>

      {connzError && (
        <div className="bg-yellow-50 dark:bg-yellow-900/20 border border-yellow-200 dark:border-yellow-800 rounded-lg p-4 mb-4">
          <h3 className="font-medium text-yellow-800 dark:text-yellow-300 mb-1">Connection details not available</h3>
          <p className="text-sm text-yellow-700 dark:text-yellow-400">{connzError}</p>
          <p className="text-xs text-yellow-600 dark:text-yellow-500 mt-2">
            Add <code className="bg-yellow-100 dark:bg-yellow-900 px-1 rounded">clients_snapshot_interval: 10s</code> to the bridge's <code className="bg-yellow-100 dark:bg-yellow-900 px-1 rounded">admin</code> config section, then restart the bridge.
          </p>
        </div>
      )}

      {loading && !data && !connzError ? (
        <TableSkeleton rows={8} cols={10} />
      ) : data ? (
        <>
          <div className="flex items-center justify-between mb-2">
            <div className="text-sm text-gray-500 dark:text-gray-400">
              {total} MQTT connections
            </div>
            <div className="flex items-center gap-2 text-sm">
              <span className="text-gray-500 dark:text-gray-400">Per page:</span>
              <select
                value={pageSize}
                onChange={(e) => { setPageSize(Number(e.target.value)); setOffset(0) }}
                className="border dark:border-gray-600 dark:bg-gray-800 rounded px-2 py-1 text-sm"
              >
                {PAGE_SIZE_OPTIONS.map((n) => <option key={n} value={n}>{n}</option>)}
              </select>
            </div>
          </div>

          <div className="bg-white dark:bg-gray-800 rounded-lg shadow overflow-x-auto">
            <table className="w-full text-sm">
              <thead className="bg-gray-50 dark:bg-gray-700 text-left text-gray-500 dark:text-gray-400">
                {table.getHeaderGroups().map((hg) => (
                  <tr key={hg.id}>
                    {hg.headers.map((h) => (
                      <th
                        key={h.id}
                        className={`px-3 py-2 whitespace-nowrap ${h.column.getCanSort() ? 'cursor-pointer select-none hover:text-gray-700 dark:hover:text-gray-200' : ''}`}
                        onClick={h.column.getToggleSortingHandler()}
                      >
                        <div className="flex items-center gap-1">
                          {flexRender(h.column.columnDef.header, h.getContext())}
                          {h.column.getCanSort() && (
                            h.column.getIsSorted() === 'asc' ? <ArrowUp className="w-3 h-3" /> :
                            h.column.getIsSorted() === 'desc' ? <ArrowDown className="w-3 h-3" /> :
                            <ArrowUpDown className="w-3 h-3 opacity-30" />
                          )}
                        </div>
                        {h.column.getCanFilter() && <ColumnFilter column={h.column} />}
                      </th>
                    ))}
                  </tr>
                ))}
              </thead>
              <tbody className="divide-y divide-gray-100 dark:divide-gray-700">
                {table.getRowModel().rows.map((row) => (
                  <tr
                    key={row.id}
                    className="hover:bg-gray-50 dark:hover:bg-gray-700/50 cursor-pointer"
                    onClick={() => setSelected(row.original)}
                  >
                    {row.getVisibleCells().map((cell) => (
                      <td key={cell.id} className="px-3 py-2 whitespace-nowrap">
                        {flexRender(cell.column.columnDef.cell, cell.getContext())}
                      </td>
                    ))}
                  </tr>
                ))}
                {(!data.connections || data.connections.length === 0) && (
                  <tr><td colSpan={columns.length} className="px-3 py-8 text-center text-gray-400">No MQTT connections</td></tr>
                )}
              </tbody>
            </table>
          </div>

          {totalPages > 1 && (
            <div className="flex items-center justify-between mt-4">
              <div className="text-sm text-gray-500">Page {currentPage} of {totalPages}</div>
              <div className="flex gap-2">
                <button disabled={!hasPrev} onClick={() => setOffset(0)}
                  className="px-3 py-1 border dark:border-gray-600 rounded text-sm disabled:opacity-30">First</button>
                <button disabled={!hasPrev} onClick={() => setOffset(Math.max(0, offset - pageSize))}
                  className="px-3 py-1 border dark:border-gray-600 rounded text-sm disabled:opacity-30">Previous</button>
                <button disabled={!hasNext} onClick={() => setOffset(offset + pageSize)}
                  className="px-3 py-1 border dark:border-gray-600 rounded text-sm disabled:opacity-30">Next</button>
                <button disabled={!hasNext} onClick={() => setOffset((totalPages - 1) * pageSize)}
                  className="px-3 py-1 border dark:border-gray-600 rounded text-sm disabled:opacity-30">Last</button>
              </div>
            </div>
          )}
        </>
      ) : null}

      {selected && (
        <div className="fixed inset-0 bg-black/30 flex items-center justify-center z-50" onClick={() => setSelected(null)}>
          <div className="bg-white dark:bg-gray-800 rounded-lg shadow-xl p-6 max-w-2xl w-full max-h-[80vh] overflow-y-auto" onClick={(e) => e.stopPropagation()}>
            <h2 className="text-lg font-semibold mb-4">
              MQTT Client: <span className="font-mono">{selected.mqtt_client}</span>
            </h2>
            <div className="grid grid-cols-2 md:grid-cols-3 lg:grid-cols-4 gap-3 text-sm">
              <DI label="Client ID" value={selected.mqtt_client} />
              <DI label="Kind" value={selected.kind || '-'} />
              <DI label="Type" value={selected.type || '-'} />
              <DI label="State" value={selected.state} />
              <DI label="Slow Consumer" value={selected.slow_consumer ? 'Yes' : 'No'} />
              <DI label="IP" value={`${selected.ip}:${selected.port}`} />
              <DI label="User" value={selected.username || '-'} />
              <DI label="Subscriptions" value={selected.subscriptions.toString()} />
              <DI label="Pending Bytes" value={fmtBytes(selected.pending_bytes || 0)} />
              <DI label="Msgs In" value={fmtNum(selected.in_msgs)} />
              <DI label="Msgs Out" value={fmtNum(selected.out_msgs)} />
              <DI label="Bytes In" value={fmtBytes(selected.in_bytes)} />
              <DI label="Bytes Out" value={fmtBytes(selected.out_bytes)} />
              <DI label="Inflight Out" value={selected.inflight_out.toString()} />
              <DI label="Uptime" value={selected.uptime || '-'} />
              <DI label="Idle" value={selected.idle || '-'} />
              <DI label="Clean Start" value={selected.clean_start ? 'Yes' : 'No'} />
              <DI label="Keep Alive" value={`${selected.keep_alive}s`} />
              <DI label="Session Expiry" value={`${selected.session_expiry_interval}s`} />
              <DI label="Receive Max" value={selected.receive_maximum.toString()} />
              <DI label="WebSocket" value={selected.is_websocket ? 'Yes' : 'No'} />
              <DI label="Language" value={selected.lang} />
              <DI label="CID" value={selected.cid.toString()} />
            </div>
            <button
              onClick={() => setSelected(null)}
              className="mt-4 w-full bg-gray-100 dark:bg-gray-700 text-gray-700 dark:text-gray-200 rounded py-2 text-sm hover:bg-gray-200 dark:hover:bg-gray-600"
            >
              Close
            </button>
          </div>
        </div>
      )}
    </div>
  )
}

function DI({ label, value }: { label: string; value: string }) {
  return (
    <div>
      <div className="text-gray-500 dark:text-gray-400 text-xs">{label}</div>
      <div className="font-medium">{value}</div>
    </div>
  )
}
