import { useState, useEffect, useCallback, useMemo, useRef } from 'react'
import { fetchWithTimeout } from '../utils/fetchWithTimeout'
import { Link } from 'react-router-dom'
import {
  createColumnHelper,
  flexRender,
  getCoreRowModel,
  getSortedRowModel,
  getFilteredRowModel,
  getPaginationRowModel,
  useReactTable,
  type SortingState,
  type ColumnFiltersState,
} from '@tanstack/react-table'
import { useStore } from '../store/store'
import { TableSkeleton, NoClusterEmptyState } from '../components/Skeleton'
import { ArrowUpDown, ArrowUp, ArrowDown, ArrowLeft } from 'lucide-react'
import { ColumnFilter } from '../components/ColumnFilter'
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

interface MQTTClientRow extends MQTTClient {
  bridge_name: string
  bridge_ip: string
  nats_server: string
}

interface BridgeInstance {
  ip: string
  server_id: string
  server_name: string
  configured_name?: string
  admin_url: string
  reachable: boolean
  status?: {
    name: string
    connz_available: boolean
    connections: number
    nats_connected: boolean
    nats?: { connection: { server_name: string; url: string } }
  }
}

const col = createColumnHelper<MQTTClientRow>()
// Each refresh issues one live admin-API fetch per bridge, so keep it moderate.
const REFRESH_INTERVAL = 5_000
const PAGE_SIZE_OPTIONS = [25, 50, 100, 250]

export function MQTTAllConnectionsPage() {
  const activeEnv = useStore((s) => s.activeEnv)
  const environments = useStore((s) => s.environments)
  const addToast = useStore((s) => s.addToast)
  const [rows, setRows] = useState<MQTTClientRow[]>([])
  const [loading, setLoading] = useState(true)
  const [sorting, setSorting] = useState<SortingState>([])
  const [columnFilters, setColumnFilters] = useState<ColumnFiltersState>([])
  const [selected, setSelected] = useState<MQTTClientRow | null>(null)
  const [bridgeCount, setBridgeCount] = useState(0)
  const [totalConns, setTotalConns] = useState(0)
  const fetchCounter = useRef(0)

  const fetchData = useCallback(async () => {
    if (!activeEnv) return
    const isInitial = fetchCounter.current === 0
    if (isInitial) setLoading(true)

    try {
      const bridgeRes = await fetchWithTimeout(`/api/environments/${activeEnv}/mqtt/bridges`)
      if (!bridgeRes.ok) { setLoading(false); return }
      const bridgeData = await bridgeRes.json()
      const bridges: BridgeInstance[] = bridgeData.bridges || []
      setBridgeCount(bridges.length)

      // Try connz for every reachable bridge. We can't rely on status.connz_available
      // here: push-discovered bridges never report it, yet their connz is reachable
      // through the configured admin URL. The per-bridge connz endpoint returns a
      // clean error for bridges that genuinely lack it, handled as zero rows below.
      const reachable = bridges.filter(b => b.reachable)
      const connTotal = bridges.reduce((s, b) => s + (b.status?.connections ?? 0), 0)
      setTotalConns(connTotal)

      const results = await Promise.allSettled(
        reachable.map(async (b) => {
          const name = b.configured_name || b.status?.name || `mqtt@${b.ip}`
          const res = await fetchWithTimeout(`/api/environments/${activeEnv}/mqtt/${encodeURIComponent(name)}/connz?limit=1000&offset=0`)
          if (!res.ok) return []
          const data = await res.json()
          if (data.error) return []
          const natsServer = b.status?.nats?.connection?.server_name || b.status?.nats?.connection?.url || b.server_name || '-'
          return (data.connections || []).map((c: MQTTClient) => ({
            ...c,
            bridge_name: name,
            bridge_ip: b.ip,
            nats_server: natsServer,
          }))
        })
      )

      const allRows: MQTTClientRow[] = []
      for (const r of results) {
        if (r.status === 'fulfilled') allRows.push(...r.value)
      }
      setRows(allRows)
    } catch {
      if (isInitial) addToast('Network error loading MQTT connections', 'error')
    }
    setLoading(false)
  }, [activeEnv, addToast])

  useEffect(() => { fetchData() }, [fetchData])
  useEffect(() => {
    if (!activeEnv) return
    const id = setInterval(() => { fetchCounter.current++; fetchData() }, REFRESH_INTERVAL)
    return () => clearInterval(id)
  }, [activeEnv, fetchData])

  const columns = useMemo(() => [
    col.accessor('bridge_name', { header: 'Bridge' }),
    col.accessor('mqtt_client', {
      header: 'Client ID',
      cell: (i) => <span className="font-mono text-xs">{i.getValue()}</span>,
    }),
    col.accessor('ip', {
      header: 'IP',
      cell: (i) => <span className="font-mono text-xs">{i.getValue()}:{i.row.original.port}</span>,
    }),
    col.accessor('username', { header: 'User', cell: (i) => i.getValue() || '-' }),
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
    col.accessor('nats_server', { header: 'NATS Server' }),
    col.display({
      id: 'ws',
      header: 'WS',
      cell: (i) => i.row.original.is_websocket ? 'Yes' : '-',
      enableSorting: false,
      enableColumnFilter: false,
    }),
  ], [])

  // eslint-disable-next-line react-hooks/incompatible-library -- TanStack Table API is intentional
  const table = useReactTable({
    data: rows,
    columns,
    state: { sorting, columnFilters },
    onSortingChange: setSorting,
    onColumnFiltersChange: setColumnFilters,
    getCoreRowModel: getCoreRowModel(),
    getSortedRowModel: getSortedRowModel(),
    getFilteredRowModel: getFilteredRowModel(),
    getPaginationRowModel: getPaginationRowModel(),
    initialState: { pagination: { pageSize: 50 } },
  })

  if (environments.length === 0 || !activeEnv) {
    return (
      <NoClusterEmptyState
        title="All MQTT Connections"
        description="Add a NATS cluster to see MQTT client connections across all bridges."
      />
    )
  }

  return (
    <div>
      <div className="flex items-center gap-3 mb-4">
        <Link to="/mqtt" className="text-gray-400 hover:text-gray-600 dark:hover:text-gray-200">
          <ArrowLeft className="w-5 h-5" />
        </Link>
        <h1 className="text-2xl font-semibold">All MQTT Connections</h1>
        <span className="text-xs text-gray-400">Auto-refreshes every {REFRESH_INTERVAL / 1000}s</span>
      </div>

      <div className="grid grid-cols-2 lg:grid-cols-4 gap-4 mb-6">
        <SC label="Bridges" value={bridgeCount.toString()} />
        <SC label="Total Connections" value={totalConns.toLocaleString()} />
        <SC label="Loaded in Table" value={rows.length.toLocaleString()} />
        <SC label="Filtered" value={table.getFilteredRowModel().rows.length.toLocaleString()} />
      </div>

      {loading && rows.length === 0 ? (
        <TableSkeleton rows={8} cols={12} />
      ) : (
        <>
          <div className="flex items-center justify-between mb-2">
            <div className="text-sm text-gray-500 dark:text-gray-400">
              {table.getFilteredRowModel().rows.length} connections
              {columnFilters.length > 0 && ` (filtered from ${rows.length})`}
            </div>
            <div className="flex items-center gap-2 text-sm">
              <span className="text-gray-500 dark:text-gray-400">Per page:</span>
              <select
                value={table.getState().pagination.pageSize}
                onChange={(e) => table.setPageSize(Number(e.target.value))}
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
                {table.getRowModel().rows.length === 0 && (
                  <tr><td colSpan={columns.length} className="px-3 py-8 text-center text-gray-400">No MQTT connections found</td></tr>
                )}
              </tbody>
            </table>
          </div>

          {table.getPageCount() > 1 && (
            <div className="flex items-center justify-between mt-4">
              <div className="text-sm text-gray-500">
                Page {table.getState().pagination.pageIndex + 1} of {table.getPageCount()}
              </div>
              <div className="flex gap-2">
                <button disabled={!table.getCanPreviousPage()} onClick={() => table.setPageIndex(0)}
                  className="px-3 py-1 border dark:border-gray-600 rounded text-sm disabled:opacity-30">First</button>
                <button disabled={!table.getCanPreviousPage()} onClick={() => table.previousPage()}
                  className="px-3 py-1 border dark:border-gray-600 rounded text-sm disabled:opacity-30">Previous</button>
                <button disabled={!table.getCanNextPage()} onClick={() => table.nextPage()}
                  className="px-3 py-1 border dark:border-gray-600 rounded text-sm disabled:opacity-30">Next</button>
                <button disabled={!table.getCanNextPage()} onClick={() => table.setPageIndex(table.getPageCount() - 1)}
                  className="px-3 py-1 border dark:border-gray-600 rounded text-sm disabled:opacity-30">Last</button>
              </div>
            </div>
          )}
        </>
      )}

      {selected && (
        <div className="fixed inset-0 bg-black/30 flex items-center justify-center z-50" onClick={() => setSelected(null)}>
          <div className="bg-white dark:bg-gray-800 rounded-lg shadow-xl p-6 max-w-2xl w-full max-h-[80vh] overflow-y-auto" onClick={(e) => e.stopPropagation()}>
            <h2 className="text-lg font-semibold mb-4">
              MQTT Client: <span className="font-mono">{selected.mqtt_client}</span>
            </h2>
            <div className="grid grid-cols-2 md:grid-cols-3 lg:grid-cols-4 gap-3 text-sm">
              <DI label="Bridge" value={selected.bridge_name} />
              <DI label="NATS Server" value={selected.nats_server} />
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

function SC({ label, value }: { label: string; value: string }) {
  return (
    <div className="bg-white dark:bg-gray-800 rounded-lg shadow p-4">
      <div className="text-sm text-gray-500 dark:text-gray-400 mb-1">{label}</div>
      <div className="text-2xl font-semibold">{value}</div>
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
