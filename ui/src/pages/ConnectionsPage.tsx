import { useState, useEffect, useCallback, useMemo, useRef } from 'react'
import { fetchWithTimeout } from '../utils/fetchWithTimeout'
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
import { TableSkeleton, NoClusterEmptyState } from '../components/Skeleton'
import { ArrowUpDown, ArrowUp, ArrowDown } from 'lucide-react'

interface Connection {
  cid: number
  name: string
  ip: string
  port: number
  account: string
  authorized_user: string
  rtt: string
  in_msgs: number
  out_msgs: number
  in_bytes: number
  out_bytes: number
  subscriptions: number
  uptime: string
  lang: string
  version: string
  subscriptions_list?: string[]
  tls_version?: string
  tls_cipher_suite?: string
}

interface ConnzResponse {
  connections: Connection[]
  total: number
  limit: number
  offset: number
}

const col = createColumnHelper<Connection>()

const PAGE_SIZE_OPTIONS = [25, 50, 100, 250]
const REFRESH_INTERVAL = 10_000

export function ConnectionsPage() {
  const activeEnv = useStore((s) => s.activeEnv)
  const environments = useStore((s) => s.environments)
  const addToast = useStore((s) => s.addToast)
  const [data, setData] = useState<ConnzResponse | null>(null)
  const [loading, setLoading] = useState(true)
  const [offset, setOffset] = useState(0)
  const [pageSize, setPageSize] = useState(50)
  const [acc, setAcc] = useState('')
  const [user, setUser] = useState('')
  const [state, setState] = useState('')
  const [filterSubject, setFilterSubject] = useState('')
  const [selected, setSelected] = useState<Connection | null>(null)
  const [detailLoading, setDetailLoading] = useState(false)
  const [sorting, setSorting] = useState<SortingState>([])
  const [columnFilters, setColumnFilters] = useState<ColumnFiltersState>([])
  const fetchCounter = useRef(0)

  const fetchData = useCallback(async () => {
    if (!activeEnv) return
    const isInitial = !data
    if (isInitial) setLoading(true)
    try {
      const params = new URLSearchParams()
      params.set('limit', pageSize.toString())
      params.set('offset', offset.toString())
      if (acc) params.set('acc', acc)
      if (state) params.set('state', state)
      if (filterSubject) params.set('filter_subject', filterSubject)

      const res = await fetchWithTimeout(`/api/environments/${activeEnv}/connz?${params}`)
      if (res.ok) {
        setData(await res.json())
      } else if (isInitial) {
        addToast('Failed to fetch connections', 'error')
      }
    } catch {
      if (isInitial) addToast('Network error fetching connections', 'error')
    } finally {
      setLoading(false)
    }
  // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [activeEnv, offset, pageSize, acc, state, filterSubject])

  useEffect(() => { fetchData() }, [fetchData])

  // Auto-refresh.
  useEffect(() => {
    if (!activeEnv) return
    const id = setInterval(() => {
      fetchCounter.current++
      fetchData()
    }, REFRESH_INTERVAL)
    return () => clearInterval(id)
  }, [activeEnv, fetchData])

  // Fetch connection detail with subscription list.
  const openDetail = async (conn: Connection) => {
    setSelected(conn)
    setDetailLoading(true)
    try {
      const res = await fetchWithTimeout(`/api/environments/${activeEnv}/connz/${conn.cid}`)
      if (res.ok) {
        const detail = await res.json()
        setSelected(detail)
      }
    } catch { /* keep the basic connection data */ }
    setDetailLoading(false)
  }

  const columns = useMemo(() => [
    col.accessor('cid', {
      header: 'CID',
      cell: (i) => <span className="font-mono">{i.getValue()}</span>,
    }),
    col.accessor('name', {
      header: 'Name',
      cell: (i) => i.getValue() || '-',
    }),
    col.accessor((row) => `${row.ip}:${row.port}`, {
      id: 'addr',
      header: 'IP:Port',
      cell: (i) => <span className="font-mono">{i.getValue()}</span>,
      enableSorting: false,
    }),
    col.accessor('account', {
      header: 'Account',
      cell: (i) => i.getValue() || '-',
    }),
    col.accessor('authorized_user', {
      header: 'User',
      cell: (i) => i.getValue() || '-',
    }),
    col.accessor('rtt', {
      header: 'RTT',
      cell: (i) => i.getValue() || '-',
    }),
    col.accessor('in_msgs', {
      header: 'Msgs In',
      cell: (i) => fmtNum(i.getValue()),
    }),
    col.accessor('out_msgs', {
      header: 'Msgs Out',
      cell: (i) => fmtNum(i.getValue()),
    }),
    col.accessor('in_bytes', {
      header: 'Bytes In',
      cell: (i) => fmtBytes(i.getValue()),
    }),
    col.accessor('out_bytes', {
      header: 'Bytes Out',
      cell: (i) => fmtBytes(i.getValue()),
    }),
    col.accessor('subscriptions', { header: 'Subs' }),
    col.accessor('uptime', { header: 'Uptime' }),
    col.accessor((row) => `${row.lang || ''} ${row.version || ''}`.trim(), {
      id: 'client',
      header: 'Client',
      cell: (i) => i.getValue() || '-',
      enableSorting: false,
    }),
  ], [])

  // Client-side filter by user/name.
  const filtered = useMemo(() => {
    if (!data?.connections) return []
    if (!user) return data.connections
    const u = user.toLowerCase()
    return data.connections.filter((c) =>
      (c.authorized_user || '').toLowerCase().includes(u) ||
      (c.name || '').toLowerCase().includes(u)
    )
  }, [data, user])

  const table = useReactTable({
    data: filtered,
    columns,
    state: { sorting, columnFilters },
    onSortingChange: setSorting,
    onColumnFiltersChange: setColumnFilters,
    getCoreRowModel: getCoreRowModel(),
    getSortedRowModel: getSortedRowModel(),
    getFilteredRowModel: getFilteredRowModel(),
  })

  const total = data?.total ?? 0
  const totalPages = Math.max(1, Math.ceil(total / pageSize))
  const currentPage = Math.floor(offset / pageSize) + 1
  const hasNext = offset + pageSize < total
  const hasPrev = offset > 0

  if (environments.length === 0 || !activeEnv) {
    return (
      <NoClusterEmptyState
        title="Connections"
        description="Add a NATS cluster to inspect active client connections."
      />
    )
  }

  return (
    <div>
      <div className="flex items-center justify-between mb-4">
        <h1 className="text-2xl font-semibold">Connections</h1>
        <span className="text-xs text-gray-400">Auto-refreshes every {REFRESH_INTERVAL / 1000}s</span>
      </div>

      <div className="flex gap-3 mb-4 flex-wrap">
        <input
          placeholder="Account"
          value={acc}
          onChange={(e) => { setAcc(e.target.value); setOffset(0) }}
          className="border dark:border-gray-600 dark:bg-gray-800 rounded px-3 py-1.5 text-sm w-40"
        />
        <input
          placeholder="User / Name"
          value={user}
          onChange={(e) => setUser(e.target.value)}
          className="border dark:border-gray-600 dark:bg-gray-800 rounded px-3 py-1.5 text-sm w-40"
        />
        <input
          placeholder="Subject filter"
          value={filterSubject}
          onChange={(e) => { setFilterSubject(e.target.value); setOffset(0) }}
          className="border dark:border-gray-600 dark:bg-gray-800 rounded px-3 py-1.5 text-sm w-48"
        />
        <select
          value={state}
          onChange={(e) => { setState(e.target.value); setOffset(0) }}
          className="border dark:border-gray-600 dark:bg-gray-800 rounded px-3 py-1.5 text-sm"
        >
          <option value="">All states</option>
          <option value="open">Open</option>
          <option value="closed">Closed</option>
        </select>
        <button
          onClick={() => { fetchCounter.current++; fetchData() }}
          className="bg-brand-blue text-white rounded px-4 py-1.5 text-sm hover:opacity-90"
        >
          Refresh
        </button>
      </div>

      {loading && !data ? (
        <TableSkeleton rows={8} cols={10} />
      ) : data ? (
        <>
          <div className="flex items-center justify-between mb-2">
            <div className="text-sm text-gray-500 dark:text-gray-400">
              {total} connections total
              {total > 0 && !data.connections?.some(c => c.account || c.authorized_user) && (
                <span className="ml-2 text-xs text-gray-400">(no auth configured — Account/User will be empty)</span>
              )}
            </div>
            <div className="flex items-center gap-2 text-sm">
              <span className="text-gray-500 dark:text-gray-400">Per page:</span>
              <select
                value={pageSize}
                onChange={(e) => { setPageSize(Number(e.target.value)); setOffset(0) }}
                className="border dark:border-gray-600 dark:bg-gray-800 rounded px-2 py-1 text-sm"
              >
                {PAGE_SIZE_OPTIONS.map((n) => (
                  <option key={n} value={n}>{n}</option>
                ))}
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
                    onClick={() => openDetail(row.original)}
                  >
                    {row.getVisibleCells().map((cell) => (
                      <td key={cell.id} className="px-3 py-2 whitespace-nowrap">
                        {flexRender(cell.column.columnDef.cell, cell.getContext())}
                      </td>
                    ))}
                  </tr>
                ))}
                {table.getRowModel().rows.length === 0 && (
                  <tr><td colSpan={columns.length} className="px-3 py-8 text-center text-gray-400">No connections found</td></tr>
                )}
              </tbody>
            </table>
          </div>

          <div className="flex items-center justify-between mt-4">
            <div className="text-sm text-gray-500">
              Page {currentPage} of {totalPages}
            </div>
            <div className="flex gap-2">
              <button
                disabled={!hasPrev}
                onClick={() => setOffset(0)}
                className="px-3 py-1 border dark:border-gray-600 rounded text-sm disabled:opacity-30"
              >
                First
              </button>
              <button
                disabled={!hasPrev}
                onClick={() => setOffset(Math.max(0, offset - pageSize))}
                className="px-3 py-1 border dark:border-gray-600 rounded text-sm disabled:opacity-30"
              >
                Previous
              </button>
              <button
                disabled={!hasNext}
                onClick={() => setOffset(offset + pageSize)}
                className="px-3 py-1 border dark:border-gray-600 rounded text-sm disabled:opacity-30"
              >
                Next
              </button>
              <button
                disabled={!hasNext}
                onClick={() => setOffset((totalPages - 1) * pageSize)}
                className="px-3 py-1 border dark:border-gray-600 rounded text-sm disabled:opacity-30"
              >
                Last
              </button>
            </div>
          </div>
        </>
      ) : null}

      {selected && (
        <div className="fixed inset-0 bg-black/30 flex items-center justify-center z-50" onClick={() => setSelected(null)}>
          <div className="bg-white dark:bg-gray-800 rounded-lg shadow-xl p-6 max-w-2xl w-full max-h-[80vh] overflow-y-auto" onClick={(e) => e.stopPropagation()}>
            <h2 className="text-lg font-semibold mb-4">Connection {selected.cid}</h2>
            <div className="grid grid-cols-2 gap-3 text-sm">
              <DI label="Name" value={selected.name} />
              <DI label="IP" value={`${selected.ip}:${selected.port}`} />
              <DI label="Account" value={selected.account} />
              <DI label="User" value={selected.authorized_user} />
              <DI label="RTT" value={selected.rtt} />
              <DI label="Msgs In" value={fmtNum(selected.in_msgs)} />
              <DI label="Msgs Out" value={fmtNum(selected.out_msgs)} />
              <DI label="Bytes In" value={fmtBytes(selected.in_bytes)} />
              <DI label="Bytes Out" value={fmtBytes(selected.out_bytes)} />
              <DI label="Subs" value={selected.subscriptions.toString()} />
              <DI label="Client" value={`${selected.lang} ${selected.version}`} />
              <DI label="Uptime" value={selected.uptime} />
              {selected.tls_version && <DI label="TLS" value={`${selected.tls_version} ${selected.tls_cipher_suite || ''}`} />}
            </div>

            <div className="mt-4">
              <h3 className="font-medium mb-2">Subscriptions</h3>
              {detailLoading ? (
                <div className="text-sm text-gray-400">Loading subscriptions...</div>
              ) : selected.subscriptions_list && selected.subscriptions_list.length > 0 ? (
                <div className="bg-gray-50 dark:bg-gray-700 rounded overflow-hidden">
                  <table className="w-full text-sm">
                    <thead className="bg-gray-100 dark:bg-gray-600 text-left text-gray-500 dark:text-gray-300">
                      <tr>
                        <th className="px-3 py-1.5">#</th>
                        <th className="px-3 py-1.5">Subject</th>
                      </tr>
                    </thead>
                    <tbody className="divide-y divide-gray-200 dark:divide-gray-600 max-h-48 overflow-y-auto">
                      {selected.subscriptions_list.map((s, i) => (
                        <tr key={i} className="hover:bg-gray-100 dark:hover:bg-gray-600/50">
                          <td className="px-3 py-1 text-gray-400 text-xs">{i + 1}</td>
                          <td className="px-3 py-1 font-mono text-xs">{s}</td>
                        </tr>
                      ))}
                    </tbody>
                  </table>
                </div>
              ) : (
                <div className="text-sm text-gray-400 bg-gray-50 dark:bg-gray-700 rounded p-3">
                  No active subscriptions on this connection.
                </div>
              )}
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
      <div className="font-medium">{value || '-'}</div>
    </div>
  )
}

function fmtNum(n: number): string {
  if (n >= 1e6) return (n / 1e6).toFixed(1) + 'M'
  if (n >= 1e3) return (n / 1e3).toFixed(1) + 'K'
  return n.toString()
}

function fmtBytes(b: number): string {
  if (b >= 1e9) return (b / 1e9).toFixed(1) + ' GB'
  if (b >= 1e6) return (b / 1e6).toFixed(1) + ' MB'
  if (b >= 1e3) return (b / 1e3).toFixed(1) + ' KB'
  return b + ' B'
}
