import { useState, useEffect, useCallback, useRef } from 'react'
import { fetchWithTimeout } from '../utils/fetchWithTimeout'
import { RefreshCw } from 'lucide-react'

interface LogEntry {
  time: string
  level: string
  msg: string
  attrs?: Record<string, unknown>
}

const levelColors: Record<string, string> = {
  DEBUG: 'text-gray-400 dark:text-gray-500',
  INFO:  'text-blue-600 dark:text-blue-400',
  WARN:  'text-yellow-600 dark:text-yellow-400',
  ERROR: 'text-red-600 dark:text-red-400',
}

const levelBg: Record<string, string> = {
  DEBUG: '',
  INFO:  '',
  WARN:  'bg-yellow-50 dark:bg-yellow-900/10',
  ERROR: 'bg-red-50 dark:bg-red-900/10',
}

export function LogsPage() {
  const [logs, setLogs] = useState<LogEntry[]>([])
  const [loading, setLoading] = useState(true)
  const [autoRefresh, setAutoRefresh] = useState(true)
  const intervalRef = useRef<ReturnType<typeof setInterval> | null>(null)

  const fetchLogs = useCallback(async () => {
    try {
      const res = await fetchWithTimeout('/api/admin/logs')
      if (res.ok) {
        const data = await res.json()
        setLogs(data.logs || [])
      }
    } catch { /* ignore */ }
    setLoading(false)
  }, [])

  useEffect(() => {
    fetchLogs() // eslint-disable-line react-hooks/set-state-in-effect -- fetch-on-mount is intentional
  }, [fetchLogs])

  useEffect(() => {
    if (autoRefresh) {
      intervalRef.current = setInterval(fetchLogs, 3000)
    } else if (intervalRef.current) {
      clearInterval(intervalRef.current)
    }
    return () => { if (intervalRef.current) clearInterval(intervalRef.current) }
  }, [autoRefresh, fetchLogs])

  const fmtTime = (iso: string) => {
    try {
      return new Date(iso).toLocaleTimeString(undefined, { hour12: false, hour: '2-digit', minute: '2-digit', second: '2-digit' })
    } catch {
      return iso
    }
  }

  const fmtAttrs = (attrs?: Record<string, unknown>) => {
    if (!attrs) return ''
    return Object.entries(attrs).map(([k, v]) => `${k}=${JSON.stringify(v)}`).join(' ')
  }

  return (
    <div>
      <div className="flex items-center justify-between mb-4">
        <h1 className="text-2xl font-semibold">Server Logs</h1>
        <div className="flex items-center gap-3">
          <label className="flex items-center gap-2 text-sm text-gray-600 dark:text-gray-400 cursor-pointer select-none">
            <input
              type="checkbox"
              checked={autoRefresh}
              onChange={(e) => setAutoRefresh(e.target.checked)}
              className="rounded"
            />
            Auto-refresh
          </label>
          <button
            onClick={fetchLogs}
            className="flex items-center gap-1.5 px-3 py-1.5 text-sm bg-white dark:bg-gray-800 border border-gray-200 dark:border-gray-600 rounded-lg hover:bg-gray-50 dark:hover:bg-gray-700"
          >
            <RefreshCw className="w-3.5 h-3.5" />
            Refresh
          </button>
        </div>
      </div>

      <div className="bg-white dark:bg-gray-800 rounded-lg shadow overflow-hidden">
        {loading ? (
          <div className="p-8 text-center text-gray-400 dark:text-gray-500 text-sm">Loading logs...</div>
        ) : logs.length === 0 ? (
          <div className="p-8 text-center text-gray-400 dark:text-gray-500 text-sm">No log entries captured yet.</div>
        ) : (
          <div className="overflow-x-auto">
            <table className="w-full text-sm font-mono">
              <thead className="bg-gray-50 dark:bg-gray-700 text-xs text-gray-500 dark:text-gray-400">
                <tr>
                  <th className="px-4 py-2 text-left whitespace-nowrap">Time</th>
                  <th className="px-4 py-2 text-left whitespace-nowrap">Level</th>
                  <th className="px-4 py-2 text-left">Message</th>
                  <th className="px-4 py-2 text-left">Attrs</th>
                </tr>
              </thead>
              <tbody className="divide-y divide-gray-100 dark:divide-gray-700">
                {logs.map((entry, i) => (
                  <tr key={i} className={`${levelBg[entry.level] ?? ''} hover:bg-gray-50 dark:hover:bg-gray-700/50`}>
                    <td className="px-4 py-1.5 whitespace-nowrap text-gray-500 dark:text-gray-400 text-xs">
                      {fmtTime(entry.time)}
                    </td>
                    <td className={`px-4 py-1.5 whitespace-nowrap font-semibold text-xs ${levelColors[entry.level] ?? 'text-gray-600'}`}>
                      {entry.level}
                    </td>
                    <td className="px-4 py-1.5 text-gray-800 dark:text-gray-200">{entry.msg}</td>
                    <td className="px-4 py-1.5 text-gray-500 dark:text-gray-400 text-xs">{fmtAttrs(entry.attrs)}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </div>
      <p className="mt-2 text-xs text-gray-400 dark:text-gray-500">
        Showing the last {logs.length} entries (buffer: 500). Newest first.
      </p>
    </div>
  )
}
