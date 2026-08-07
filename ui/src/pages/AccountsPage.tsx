import { useState, useEffect, useMemo } from 'react'
import { fetchWithTimeout } from '../utils/fetchWithTimeout'
import { useStore } from '../store/store'
import { TableSkeleton, NoClusterEmptyState } from '../components/Skeleton'
import { ChevronDown, ChevronRight } from 'lucide-react'
import { formatNumber as fmtNum } from '../utils/format'

interface AccountzData {
  [serverId: string]: {
    server_id: string
    system_account: string
    accounts: string[]
  }
}

interface AccountDetail {
  account_name: string
  is_system: boolean
  expired: boolean
  jetstream_enabled: boolean
  leafnode_connections: number
  client_connections: number
  subscriptions: number
}

interface ConnInfo {
  cid: number
  name: string
  ip: string
  port: number
  rtt: string
  in_msgs: number
  out_msgs: number
  subscriptions: number
  uptime: string
  lang: string
  version: string
}

interface LeafInfo {
  id: number
  name: string
  ip: string
  port: number
  account: string
  rtt: string
  in_msgs: number
  out_msgs: number
  subscriptions: number
}

export function AccountsPage() {
  const activeEnv = useStore((s) => s.activeEnv)
  const environments = useStore((s) => s.environments)
  const addToast = useStore((s) => s.addToast)
  const [data, setData] = useState<AccountzData | null>(null)
  const [loading, setLoading] = useState(true)
  const [expanded, setExpanded] = useState<string | null>(null)
  const [detail, setDetail] = useState<AccountDetail | null>(null)
  const [detailLoading, setDetailLoading] = useState(false)
  const [drilldown, setDrilldown] = useState<'connections' | 'leafs' | 'subs' | null>(null)
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  const [drillData, setDrillData] = useState<any>(null)
  const [drillLoading, setDrillLoading] = useState(false)

  useEffect(() => {
    if (!activeEnv) return
    let cancelled = false
    const run = async () => {
      setLoading(true)
      try {
        const res = await fetchWithTimeout(`/api/environments/${activeEnv}/accountz`)
        if (!cancelled && res.ok) setData(await res.json())
      } catch {
        if (!cancelled) addToast('Failed to load accounts', 'error')
      }
      if (!cancelled) setLoading(false)
    }
    run()
    // Guard against a late response for a previous env clobbering the current one.
    return () => { cancelled = true }
  }, [activeEnv, addToast])

  const allAccounts: string[] = []
  const seen = new Set<string>()
  const systemAccounts = new Set<string>()
  if (data) {
    for (const az of Object.values(data)) {
      if (az.system_account) systemAccounts.add(az.system_account)
      for (const a of az.accounts || []) {
        if (!seen.has(a)) { seen.add(a); allAccounts.push(a) }
      }
    }
  }
  allAccounts.sort()

  const toggleExpand = async (acc: string) => {
    if (expanded === acc) { setExpanded(null); setDetail(null); setDrilldown(null); return }
    setExpanded(acc); setDetail(null); setDrilldown(null); setDrillData(null)
    setDetailLoading(true)
    try {
      const res = await fetchWithTimeout(`/api/environments/${activeEnv}/accountz/${encodeURIComponent(acc)}`)
      if (res.ok) setDetail(await res.json())
    } catch { /* */ }
    setDetailLoading(false)
  }

  const fetchDrilldown = async (type: 'connections' | 'leafs' | 'subs') => {
    if (drilldown === type) { setDrilldown(null); return }
    setDrilldown(type); setDrillData(null); setDrillLoading(true)
    try {
      if (type === 'connections') {
        const res = await fetchWithTimeout(`/api/environments/${activeEnv}/connz?limit=1000&acc=${encodeURIComponent(expanded!)}`)
        if (res.ok) setDrillData(await res.json())
      } else if (type === 'leafs') {
        const res = await fetchWithTimeout(`/api/environments/${activeEnv}/leafz`)
        if (res.ok) {
          const all = await res.json()
          const leafs: LeafInfo[] = []
          // eslint-disable-next-line @typescript-eslint/no-explicit-any
          for (const lz of Object.values(all) as any[]) {
            for (const l of lz.leafs || []) {
              if (l.account === expanded) leafs.push(l)
            }
          }
          setDrillData({ leafs })
        }
      } else if (type === 'subs') {
        const res = await fetchWithTimeout(`/api/environments/${activeEnv}/subsz/detail?limit=1000&account=${encodeURIComponent(expanded!)}`)
        if (res.ok) setDrillData(await res.json())
      }
    } catch { /* */ }
    setDrillLoading(false)
  }

  if (environments.length === 0 || !activeEnv) {
    return (
      <NoClusterEmptyState
        title="Accounts"
        description="Add a NATS cluster to view account details, connections, and subscriptions."
      />
    )
  }

  return (
    <div>
      <h1 className="text-2xl font-semibold mb-4">Accounts</h1>

      <div className="grid grid-cols-2 lg:grid-cols-3 gap-4 mb-6">
        <SC label="Total Accounts" value={allAccounts.length.toString()} />
        <SC label="System Accounts" value={systemAccounts.size.toString()} />
        <SC label="Servers Reporting" value={data ? Object.keys(data).length.toString() : '-'} />
      </div>

      {loading ? <TableSkeleton rows={4} cols={4} /> : allAccounts.length === 0 ? (
        <div className="text-gray-500 dark:text-gray-400 bg-white dark:bg-gray-800 rounded-lg shadow p-8 text-center">No accounts found.</div>
      ) : (
        <div className="space-y-2">
          {allAccounts.map((acc) => {
            const isSystem = systemAccounts.has(acc)
            const isExpanded = expanded === acc
            return (
              <div key={acc} className="bg-white dark:bg-gray-800 rounded-lg shadow">
                <button onClick={() => toggleExpand(acc)}
                  className="w-full flex items-center justify-between px-4 py-3 text-left hover:bg-gray-50 dark:hover:bg-gray-700/50">
                  <div className="flex items-center gap-3">
                    {isExpanded ? <ChevronDown className="w-4 h-4" /> : <ChevronRight className="w-4 h-4" />}
                    <span className="font-mono text-sm">{acc}</span>
                    {isSystem && <span className="bg-blue-100 dark:bg-blue-900 text-blue-700 dark:text-blue-300 text-xs rounded px-2 py-0.5">system</span>}
                  </div>
                </button>

                {isExpanded && (
                  <div className="border-t dark:border-gray-700 px-4 py-3">
                    {detailLoading ? (
                      <div className="text-sm text-gray-400">Loading...</div>
                    ) : detail ? (
                      <>
                        <div className="grid grid-cols-2 md:grid-cols-3 gap-4 text-sm mb-4">
                          <ClickableMetric label="Client Connections" value={detail.client_connections}
                            active={drilldown === 'connections'} onClick={() => fetchDrilldown('connections')} />
                          <ClickableMetric label="Leaf Connections" value={detail.leafnode_connections}
                            active={drilldown === 'leafs'} onClick={() => fetchDrilldown('leafs')} />
                          <ClickableMetric label="Subscriptions" value={detail.subscriptions}
                            active={drilldown === 'subs'} onClick={() => fetchDrilldown('subs')} />
                          <DI label="JetStream" value={detail.jetstream_enabled ? 'Enabled' : 'Disabled'} />
                          <DI label="Expired" value={detail.expired ? 'Yes' : 'No'} />
                          <DI label="System" value={detail.is_system ? 'Yes' : 'No'} />
                        </div>

                        {drillLoading && <div className="text-sm text-gray-400 mb-2">Loading details...</div>}

                        {drilldown === 'connections' && drillData?.connections && (
                          <DrillTable title={`Client Connections (${drillData.total})`}
                            headers={['CID', 'Name', 'IP', 'RTT', 'Msgs In', 'Msgs Out', 'Subs', 'Uptime', 'Client']}
                            rows={(drillData.connections as ConnInfo[]).map((c) => [
                              c.cid, c.name || '-', `${c.ip}:${c.port}`, c.rtt || '-',
                              fmtNum(c.in_msgs), fmtNum(c.out_msgs), c.subscriptions, c.uptime,
                              `${c.lang || ''} ${c.version || ''}`.trim() || '-',
                            ])} />
                        )}

                        {drilldown === 'leafs' && drillData?.leafs && (
                          <DrillTable title={`Leaf Connections (${drillData.leafs.length})`}
                            headers={['ID', 'Name', 'IP', 'RTT', 'Msgs In', 'Msgs Out', 'Subs']}
                            rows={(drillData.leafs as LeafInfo[]).map((l) => [
                              l.id, l.name || '-', `${l.ip}:${l.port}`, l.rtt || '-',
                              fmtNum(l.in_msgs), fmtNum(l.out_msgs), l.subscriptions,
                            ])} />
                        )}

                        {drilldown === 'subs' && drillData?.subscriptions && (
                          <DrillTable title={`Subscriptions (${drillData.total})`}
                            headers={['Subject', 'Queue', 'Msgs', 'Connection', 'Server']}
                            // eslint-disable-next-line @typescript-eslint/no-explicit-any
                            rows={(drillData.subscriptions as any[]).map((s) => [
                              s.subject, s.queue || '-', fmtNum(s.msgs), s.conn_name || '-', s.server_name,
                            ])} />
                        )}
                      </>
                    ) : (
                      <div className="text-sm text-gray-400">No detail available.</div>
                    )}
                  </div>
                )}
              </div>
            )
          })}
        </div>
      )}
    </div>
  )
}

function ClickableMetric({ label, value, active, onClick }: { label: string; value: number; active: boolean; onClick: () => void }) {
  return (
    <button onClick={onClick}
      className={`text-left rounded-lg p-3 transition-all border ${
        active
          ? 'bg-brand-blue/10 border-brand-blue shadow-sm'
          : 'border-gray-200 dark:border-gray-600 hover:border-brand-blue/50 hover:bg-gray-50 dark:hover:bg-gray-700 cursor-pointer'
      }`}>
      <div className="text-gray-500 dark:text-gray-400 text-xs">{label}</div>
      <div className="font-semibold text-xl text-brand-blue">{value.toLocaleString()}</div>
    </button>
  )
}

// eslint-disable-next-line @typescript-eslint/no-explicit-any
function DrillTable({ title, headers, rows }: { title: string; headers: string[]; rows: any[][] }) {
  const [filters, setFilters] = useState<Record<number, string>>({})

  const filtered = useMemo(() => {
    const active = Object.entries(filters).filter(([, v]) => v)
    if (active.length === 0) return rows
    return rows.filter((row) =>
      active.every(([colIdx, term]) => {
        const val = String(row[Number(colIdx)] ?? '').toLowerCase()
        return val.includes(term.toLowerCase())
      })
    )
  }, [rows, filters])

  const setFilter = (col: number, value: string) => {
    setFilters((prev) => ({ ...prev, [col]: value }))
  }

  return (
    <div className="mb-2">
      <h3 className="font-medium text-sm mb-2">{title}</h3>
      <div className="overflow-x-auto max-h-72 overflow-y-auto">
        <table className="w-full text-sm">
          <thead className="bg-gray-50 dark:bg-gray-700 text-left text-gray-500 dark:text-gray-400 sticky top-0">
            <tr>{headers.map((h, i) => (
              <th key={i} className="px-3 py-2 whitespace-nowrap">
                {h}
                <input
                  value={filters[i] || ''}
                  onChange={(e) => setFilter(i, e.target.value)}
                  placeholder="Filter..."
                  className="mt-1 w-full border dark:border-gray-600 dark:bg-gray-800 rounded px-1.5 py-0.5 text-xs font-normal text-gray-700 dark:text-gray-300 placeholder:text-gray-400 dark:placeholder:text-gray-500 outline-none focus:ring-1 focus:ring-brand-blue"
                />
              </th>
            ))}</tr>
          </thead>
          <tbody className="divide-y divide-gray-100 dark:divide-gray-700">
            {filtered.map((row, i) => (
              <tr key={i} className="hover:bg-gray-50 dark:hover:bg-gray-700/50">
                {row.map((cell, j) => <td key={j} className="px-3 py-1.5 whitespace-nowrap">{cell}</td>)}
              </tr>
            ))}
            {filtered.length === 0 && (
              <tr><td colSpan={headers.length} className="px-3 py-4 text-center text-gray-400">None</td></tr>
            )}
          </tbody>
        </table>
      </div>
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
    <div className="p-2">
      <div className="text-gray-500 dark:text-gray-400 text-xs">{label}</div>
      <div className="font-medium">{value}</div>
    </div>
  )
}
