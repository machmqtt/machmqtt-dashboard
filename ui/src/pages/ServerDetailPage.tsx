import { useState, useEffect, useMemo } from 'react'
import { useParams, Link } from 'react-router-dom'
import { fetchWithTimeout } from '../utils/fetchWithTimeout'
import { useStore } from '../store/store'
import { CardSkeleton } from '../components/Skeleton'
import { ArrowLeft } from 'lucide-react'
import { TimeSeriesChart } from '../components/TimeSeriesChart'
import { TimeRangeSelector } from '../components/TimeRangeSelector'
import { useMetrics } from '../hooks/useMetrics'

interface Varz {
  server_id: string
  server_name: string
  version: string
  host: string
  port: number
  go: string
  max_connections: number
  connections: number
  total_connections: number
  routes: number
  leafnodes: number
  in_msgs: number
  out_msgs: number
  in_bytes: number
  out_bytes: number
  mem: number
  cpu: number
  cores: number
  subscriptions: number
  slow_consumers: number
  uptime: string
}

export function ServerDetailPage() {
  const { id } = useParams<{ id: string }>()
  const activeEnv = useStore((s) => s.activeEnv)
  const [data, setData] = useState<Record<string, Varz> | null>(null)
  const [loading, setLoading] = useState(true)
  const metricsParams = useMemo(() => (id ? { server_id: id } : undefined), [id])
  const metrics = useMetrics(activeEnv, 'metrics/servers', metricsParams)

  useEffect(() => {
    if (!activeEnv) return
    const run = async () => {
      setLoading(true)
      try {
        const r = await fetchWithTimeout(`/api/environments/${activeEnv}/varz`)
        if (r.ok) setData(await r.json())
      } catch { /* */ }
      setLoading(false)
    }
    run()
  }, [activeEnv])

  const server = data && id ? data[id] : null

  if (loading) {
    return (
      <div>
        <div className="mb-6"><CardSkeleton /></div>
        <div className="grid grid-cols-3 gap-4">
          {Array.from({ length: 12 }).map((_, i) => <CardSkeleton key={i} />)}
        </div>
      </div>
    )
  }

  if (!server) {
    return (
      <div className="text-gray-500 dark:text-gray-400 bg-white dark:bg-gray-800 rounded-lg shadow p-8 text-center">
        Server not found.
        <div className="mt-4">
          <Link to="/" className="text-brand-blue hover:underline">Back to Overview</Link>
        </div>
      </div>
    )
  }

  return (
    <div>
      <div className="flex items-center gap-3 mb-6">
        <Link to="/" className="text-gray-400 hover:text-gray-600 dark:hover:text-gray-200">
          <ArrowLeft className="w-5 h-5" />
        </Link>
        <h1 className="text-2xl font-semibold">{server.server_name || server.server_id}</h1>
      </div>
      <div className="bg-white dark:bg-gray-800 rounded-lg shadow p-6">
        <div className="grid grid-cols-2 md:grid-cols-3 gap-6 text-sm">
          <Item label="Server ID" value={server.server_id} mono />
          <Item label="Version" value={server.version} />
          <Item label="Go Version" value={server.go} />
          <Item label="Host:Port" value={`${server.host}:${server.port}`} />
          <Item label="Uptime" value={server.uptime} />
          <Item label="CPU" value={`${server.cpu.toFixed(1)}% (${server.cores} cores)`} />
          <Item label="Memory" value={fmtBytes(server.mem)} />
          <Item label="Connections" value={`${server.connections} / ${server.max_connections}`} />
          <Item label="Total Connections" value={server.total_connections.toLocaleString()} />
          <Item label="Routes" value={server.routes.toString()} />
          <Item label="Leaf Nodes" value={server.leafnodes.toString()} />
          <Item label="Subscriptions" value={server.subscriptions.toLocaleString()} />
          <Item label="Msgs In" value={server.in_msgs.toLocaleString()} />
          <Item label="Msgs Out" value={server.out_msgs.toLocaleString()} />
          <Item label="Bytes In" value={fmtBytes(server.in_bytes)} />
          <Item label="Bytes Out" value={fmtBytes(server.out_bytes)} />
          <Item label="Slow Consumers" value={server.slow_consumers.toLocaleString()} />
        </div>
      </div>

      <div className="flex items-center justify-between mt-6 mb-3">
        <h2 className="text-lg font-semibold">Trends</h2>
        <TimeRangeSelector value={metrics.range} onChange={metrics.setRange} />
      </div>
      <div className="grid grid-cols-1 lg:grid-cols-2 gap-4">
        <div className="bg-white dark:bg-gray-800 rounded-lg shadow p-4">
          <h3 className="text-sm font-medium text-gray-500 dark:text-gray-400 mb-2">CPU & Memory</h3>
          <TimeSeriesChart
            data={metrics.data}
            lines={[
              { key: 'cpu', color: '#3b82f6', label: 'CPU %' },
            ]}
            yFormatter={(v) => v.toFixed(1) + '%'}
          />
        </div>
        <div className="bg-white dark:bg-gray-800 rounded-lg shadow p-4">
          <h3 className="text-sm font-medium text-gray-500 dark:text-gray-400 mb-2">Memory</h3>
          <TimeSeriesChart
            data={metrics.data}
            lines={[
              { key: 'mem', color: '#8b5cf6', label: 'Memory' },
            ]}
            yFormatter={fmtBytes}
          />
        </div>
        <div className="bg-white dark:bg-gray-800 rounded-lg shadow p-4">
          <h3 className="text-sm font-medium text-gray-500 dark:text-gray-400 mb-2">Message Rate</h3>
          <TimeSeriesChart
            data={metrics.data}
            lines={[
              { key: 'in_msgs_rate', color: '#22c55e', label: 'In msgs/s' },
              { key: 'out_msgs_rate', color: '#f97316', label: 'Out msgs/s' },
            ]}
            yFormatter={fmtRateAxis}
          />
        </div>
        <div className="bg-white dark:bg-gray-800 rounded-lg shadow p-4">
          <h3 className="text-sm font-medium text-gray-500 dark:text-gray-400 mb-2">Connections</h3>
          <TimeSeriesChart
            data={metrics.data}
            lines={[
              { key: 'connections', color: '#a855f7', label: 'Connections' },
            ]}
            yFormatter={(v) => v.toFixed(0)}
          />
        </div>
      </div>
    </div>
  )
}

function Item({ label, value, mono }: { label: string; value: string; mono?: boolean }) {
  return (
    <div>
      <div className="text-gray-500 dark:text-gray-400 text-xs mb-1">{label}</div>
      <div className={`font-medium ${mono ? 'font-mono text-xs' : ''}`}>{value}</div>
    </div>
  )
}

function fmtBytes(b: number): string {
  if (b >= 1e9) return (b / 1e9).toFixed(1) + ' GB'
  if (b >= 1e6) return (b / 1e6).toFixed(1) + ' MB'
  if (b >= 1e3) return (b / 1e3).toFixed(1) + ' KB'
  return b + ' B'
}

function fmtRateAxis(r: number): string {
  if (r >= 1e6) return (r / 1e6).toFixed(1) + 'M'
  if (r >= 1e3) return (r / 1e3).toFixed(1) + 'K'
  if (r >= 1) return r.toFixed(0)
  if (r > 0) return r.toFixed(2)
  return '0'
}
