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
import { TableSkeleton } from '../components/Skeleton'
import { ArrowUpDown, ArrowUp, ArrowDown, RefreshCw, Search, X } from 'lucide-react'

// --- Types ---

interface SubRow {
  subject: string
  queue: string
  sid: string
  msgs: number
  conn_cid: number
  conn_name: string
  conn_ip: string
  account: string
  server_id: string
  server_name: string
}

interface SubsResponse {
  subscriptions: SubRow[]
  total: number
  limit: number
  offset: number
}

interface SubsSummary {
  server_id: string
  num_subscriptions: number
  num_cache: number
  num_inserts: number
  num_removes: number
  num_matching: number
  cache_hit_rate: number
  max_fanout: number
  avg_fanout: number
}

// --- Constants ---

const col = createColumnHelper<SubRow>()
const PAGE_SIZE_OPTIONS = [50, 100, 250, 500]
const DEBOUNCE_MS = 500
const SUMMARY_REFRESH_INTERVAL = 10_000

function useDebounce<T>(value: T, delay: number): T {
  const [debounced, setDebounced] = useState(value)
  useEffect(() => {
    const id = setTimeout(() => setDebounced(value), delay)
    return () => clearTimeout(id)
  }, [value, delay])
  return debounced
}

// --- Component ---

export function SubscriptionsPage() {
  const activeEnv = useStore((s) => s.activeEnv)
  const overview = useStore((s) => s.overview)
  const addToast = useStore((s) => s.addToast)

  // Summary (default) state.
  const [summary, setSummary] = useState<Record<string, SubsSummary> | null>(null)
  const [summaryLoading, setSummaryLoading] = useState(true)

  // Detail state (active when subject filter is set).
  const [detail, setDetail] = useState<SubsResponse | null>(null)
  const [detailLoading, setDetailLoading] = useState(false)
  const [detailFetching, setDetailFetching] = useState(false)
  const [offset, setOffset] = useState(0)
  const [pageSize, setPageSize] = useState(100)
  const [subjectInput, setSubjectInput] = useState('')
  const [hideSystem, setHideSystem] = useState(true)
  const [sorting, setSorting] = useState<SortingState>([{ id: 'subject', desc: false }])
  const [columnFilters, setColumnFilters] = useState<ColumnFiltersState>([])
  const abortRef = useRef<AbortController | null>(null)

  const filterSubject = useDebounce(subjectInput, DEBOUNCE_MS)
  const hasFilter = filterSubject.length > 0

  // --- Summary fetch (lightweight, from cached snapshot) ---

  const fetchSummary = useCallback(async () => {
    if (!activeEnv) return
    try {
      const res = await fetchWithTimeout(`/api/environments/${activeEnv}/subsz`)
      if (res.ok) setSummary(await res.json())
    } catch { /* ignore */ }
    setSummaryLoading(false)
  }, [activeEnv])

  useEffect(() => { fetchSummary() }, [fetchSummary])

  useEffect(() => {
    if (!activeEnv) return
    const id = setInterval(fetchSummary, SUMMARY_REFRESH_INTERVAL)
    return () => clearInterval(id)
  }, [activeEnv, fetchSummary])

  // --- Detail fetch (only when subject filter is active) ---

  const fetchDetail = useCallback(async () => {
    if (!activeEnv || !filterSubject) {
      setDetail(null)
      return
    }
    abortRef.current?.abort()
    const ctrl = new AbortController()
    abortRef.current = ctrl

    if (!detail) setDetailLoading(true)
    setDetailFetching(true)
    try {
      const params = new URLSearchParams()
      params.set('limit', pageSize.toString())
      params.set('offset', offset.toString())
      params.set('subject', filterSubject)
      if (hideSystem) params.set('hide_system', 'true')

      const timeoutId = setTimeout(() => ctrl.abort(), 15000)
      const res = await fetch(`/api/environments/${activeEnv}/subsz/detail?${params}`, { signal: ctrl.signal })
      clearTimeout(timeoutId)
      if (res.ok) setDetail(await res.json())
      else if (!detail) addToast('Failed to fetch subscription detail', 'error')
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    } catch (e: any) {
      if (e?.name !== 'AbortError' && !detail) addToast('Network error', 'error')
    } finally {
      setDetailLoading(false)
      setDetailFetching(false)
    }
  // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [activeEnv, filterSubject, offset, pageSize, hideSystem])

  useEffect(() => { fetchDetail() }, [fetchDetail])
  useEffect(() => { setOffset(0) }, [filterSubject, hideSystem])

  // Clear detail when filter is removed.
  useEffect(() => {
    if (!filterSubject) setDetail(null)
  }, [filterSubject])

  // --- Summary view helpers ---

  const resolveServerName = (id: string): string => {
    const s = overview?.servers?.find((s) => s.id === id)
    return s?.name || id.slice(0, 12)
  }

  const sortedServers = useMemo(() => {
    if (!summary) return []
    return Object.values(summary).sort((a, b) =>
      resolveServerName(a.server_id).localeCompare(resolveServerName(b.server_id))
    )
  // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [summary, overview])

  // --- Detail table ---

  const columns = useMemo(() => [
    col.accessor('subject', {
      header: 'Subject',
      cell: (i) => <span className="font-mono text-xs">{i.getValue()}</span>,
    }),
    col.accessor('queue', {
      header: 'Queue',
      cell: (i) => i.getValue() ? <span className="font-mono text-xs">{i.getValue()}</span> : <span className="text-gray-400">-</span>,
    }),
    col.accessor('conn_name', {
      header: 'Connection',
      cell: (i) => i.getValue() || '-',
    }),
    col.accessor('conn_cid', {
      header: 'CID',
      cell: (i) => <span className="font-mono">{i.getValue()}</span>,
    }),
    col.accessor('conn_ip', {
      header: 'Client IP',
      cell: (i) => <span className="font-mono text-xs">{i.getValue()}</span>,
    }),
    col.accessor('account', {
      header: 'Account',
      cell: (i) => i.getValue() || '-',
    }),
    col.accessor('server_name', { header: 'Server' }),
    col.accessor('sid', {
      header: 'SID',
      cell: (i) => <span className="font-mono text-xs">{i.getValue()}</span>,
    }),
  ], [])

  const table = useReactTable({
    data: detail?.subscriptions || [],
    columns,
    state: { sorting, columnFilters },
    onSortingChange: setSorting,
    onColumnFiltersChange: setColumnFilters,
    getCoreRowModel: getCoreRowModel(),
    getSortedRowModel: getSortedRowModel(),
    getFilteredRowModel: getFilteredRowModel(),
  })

  const total = detail?.total ?? 0
  const totalPages = Math.max(1, Math.ceil(total / pageSize))
  const currentPage = Math.floor(offset / pageSize) + 1
  const hasNext = offset + pageSize < total
  const hasPrev = offset > 0
  const totalSubs = overview?.subscriptions ?? 0

  return (
    <div>
      <h1 className="text-2xl font-semibold mb-4">Subscriptions</h1>

      <div className="grid grid-cols-2 lg:grid-cols-3 gap-4 mb-6">
        <SC label="Total Server Subs" value={totalSubs.toLocaleString()} sub="All subs including internal" />
        <SC label="Servers" value={sortedServers.length.toString()} />
        {hasFilter && <SC label="Matching Subscriptions" value={total.toLocaleString()} sub={`Filtered by "${filterSubject}"`} />}
      </div>

      {/* Filter bar */}
      <div className="flex gap-3 mb-4 flex-wrap items-center">
        <div className="relative">
          <Search className="absolute left-3 top-1/2 -translate-y-1/2 w-4 h-4 text-gray-400" />
          <input
            placeholder="Type a subject to see detail..."
            value={subjectInput}
            onChange={(e) => setSubjectInput(e.target.value)}
            className="border dark:border-gray-600 dark:bg-gray-800 rounded pl-9 pr-8 py-1.5 text-sm w-72"
          />
          {subjectInput && (
            <button
              onClick={() => setSubjectInput('')}
              className="absolute right-2 top-1/2 -translate-y-1/2 text-gray-400 hover:text-gray-600"
            >
              <X className="w-4 h-4" />
            </button>
          )}
        </div>
        {hasFilter && (
          <>
            <label className="flex items-center gap-1.5 text-sm cursor-pointer">
              <input type="checkbox" checked={hideSystem} onChange={(e) => setHideSystem(e.target.checked)} className="rounded" />
              <span className="text-gray-600 dark:text-gray-400">Hide system topics</span>
            </label>
            <button
              onClick={fetchDetail}
              disabled={detailFetching}
              className="bg-brand-blue text-white rounded px-4 py-1.5 text-sm hover:opacity-90 disabled:opacity-50 flex items-center gap-1.5"
            >
              <RefreshCw className={`w-3.5 h-3.5 ${detailFetching ? 'animate-spin' : ''}`} />
              Refresh
            </button>
          </>
        )}
      </div>

      {/* Summary view (default) */}
      {!hasFilter && (
        summaryLoading ? (
          <TableSkeleton rows={3} cols={7} />
        ) : sortedServers.length === 0 ? (
          <div className="text-gray-500 dark:text-gray-400 bg-white dark:bg-gray-800 rounded-lg shadow p-8 text-center">
            No subscription data available.
          </div>
        ) : (
          <div className="bg-white dark:bg-gray-800 rounded-lg shadow overflow-hidden">
            <table className="w-full text-sm">
              <thead className="bg-gray-50 dark:bg-gray-700 text-left text-gray-500 dark:text-gray-400">
                <tr>
                  <th className="px-4 py-3">Server</th>
                  <th className="px-4 py-3">Subscriptions</th>
                  <th className="px-4 py-3">Cache</th>
                  <th className="px-4 py-3">Inserts</th>
                  <th className="px-4 py-3">Matches</th>
                  <th className="px-4 py-3">Cache Hit Rate</th>
                  <th className="px-4 py-3">Fanout (max/avg)</th>
                </tr>
              </thead>
              <tbody className="divide-y divide-gray-100 dark:divide-gray-700">
                {sortedServers.map((s) => (
                  <tr key={s.server_id} className="hover:bg-gray-50 dark:hover:bg-gray-700/50">
                    <td className="px-4 py-3 font-medium">{resolveServerName(s.server_id)}</td>
                    <td className="px-4 py-3">{s.num_subscriptions.toLocaleString()}</td>
                    <td className="px-4 py-3">{s.num_cache.toLocaleString()}</td>
                    <td className="px-4 py-3">{s.num_inserts.toLocaleString()}</td>
                    <td className="px-4 py-3">{s.num_matching.toLocaleString()}</td>
                    <td className="px-4 py-3">
                      <div className="flex items-center gap-2">
                        <div className="w-16 bg-gray-200 dark:bg-gray-600 rounded-full h-2">
                          <div className="bg-brand-blue h-2 rounded-full" style={{ width: `${Math.min(100, s.cache_hit_rate)}%` }} />
                        </div>
                        <span>{s.cache_hit_rate}%</span>
                      </div>
                    </td>
                    <td className="px-4 py-3">{s.max_fanout} / {s.avg_fanout.toFixed(1)}</td>
                  </tr>
                ))}
              </tbody>
            </table>
            <div className="px-4 py-3 bg-gray-50 dark:bg-gray-700 text-xs text-gray-500 dark:text-gray-400 border-t dark:border-gray-600">
              Type a subject in the search box above to see per-subscription detail with subscriber info, message counts, and server location.
            </div>
          </div>
        )
      )}

      {/* Detail view (when filtered) */}
      {hasFilter && (
        detailLoading ? (
          <TableSkeleton rows={8} cols={8} />
        ) : detail ? (
          <>
            <div className="flex items-center justify-between mb-2">
              <div className="text-sm text-gray-500 dark:text-gray-400">
                {total} matching subscriptions
                {detailFetching && <span className="ml-2 text-xs text-gray-400">(refreshing...)</span>}
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
                    <tr key={row.id} className="hover:bg-gray-50 dark:hover:bg-gray-700/50">
                      {row.getVisibleCells().map((cell) => (
                        <td key={cell.id} className="px-3 py-2 whitespace-nowrap">
                          {flexRender(cell.column.columnDef.cell, cell.getContext())}
                        </td>
                      ))}
                    </tr>
                  ))}
                  {(!detail.subscriptions || detail.subscriptions.length === 0) && (
                    <tr><td colSpan={columns.length} className="px-3 py-8 text-center text-gray-400">No matching subscriptions found</td></tr>
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
        ) : null
      )}
    </div>
  )
}

function SC({ label, value, sub }: { label: string; value: string; sub?: string }) {
  return (
    <div className="bg-white dark:bg-gray-800 rounded-lg shadow p-4">
      <div className="text-sm text-gray-500 dark:text-gray-400 mb-1">{label}</div>
      <div className="text-2xl font-semibold">{value}</div>
      {sub && <div className="text-xs text-gray-400 mt-0.5">{sub}</div>}
    </div>
  )
}

