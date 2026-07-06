import { Link } from 'react-router-dom'
import { useStore } from '../store/store'
import { CardSkeleton, TableSkeleton } from '../components/Skeleton'
import { Activity, Cable, ArrowDownToLine, ArrowUpFromLine, Database, GitBranch, Server } from 'lucide-react'
import { TimeSeriesChart } from '../components/TimeSeriesChart'
import { TimeRangeSelector } from '../components/TimeRangeSelector'
import { useMetrics } from '../hooks/useMetrics'
import { formatNumber as fmtNum, formatRate as fmtRate, formatBytes as fmtBytes } from '../utils/format'

export function OverviewPage() {
  const overview = useStore((s) => s.overview)
  const activeEnv = useStore((s) => s.activeEnv)
  const environments = useStore((s) => s.environments)
  const metrics = useMetrics(activeEnv, 'metrics/overview')

  if (environments.length === 0 || !activeEnv) {
    return (
      <div>
        <h1 className="text-2xl font-semibold mb-6">Overview</h1>
        <div className="bg-white dark:bg-gray-800 rounded-lg shadow p-12 text-center">
          <Server className="w-12 h-12 text-gray-300 dark:text-gray-600 mx-auto mb-4" />
          <h2 className="text-lg font-semibold text-gray-700 dark:text-gray-200 mb-2">No clusters configured</h2>
          <p className="text-sm text-gray-500 dark:text-gray-400 mb-6 max-w-sm mx-auto">
            Add a NATS cluster to start monitoring servers, connections, and message rates.
          </p>
          <Link
            to="/admin/clusters"
            className="inline-flex items-center gap-2 bg-brand-blue text-white rounded-lg px-5 py-2 text-sm font-medium hover:opacity-90 transition-opacity"
          >
            <Server className="w-4 h-4" />
            Go to Cluster Management
          </Link>
        </div>
      </div>
    )
  }

  if (!overview) {
    return (
      <div>
        <h1 className="text-2xl font-semibold mb-6">Overview</h1>
        <div className="grid grid-cols-2 lg:grid-cols-4 gap-4 mb-8">
          {Array.from({ length: 8 }).map((_, i) => <CardSkeleton key={i} />)}
        </div>
        <TableSkeleton rows={3} cols={8} />
      </div>
    )
  }

  return (
    <div>
      <div className="flex items-center justify-between mb-6">
        <h1 className="text-2xl font-semibold">Overview</h1>
        <span className="text-xs text-gray-400">Cluster totals across all servers</span>
      </div>

      <div className="grid grid-cols-2 lg:grid-cols-4 gap-4 mb-8">
        <Card
          icon={<Server className="w-5 h-5 text-brand-blue" />}
          label="Servers"
          value={`${overview.healthy_count}/${overview.server_count}`}
          sub="healthy"
        />
        <Card
          icon={<Cable className="w-5 h-5 text-purple-500" />}
          label="Connections"
          value={fmtNum(overview.connection_count)}
        />
        <Card
          icon={<ArrowDownToLine className="w-5 h-5 text-green-500" />}
          label="Msgs In/s"
          value={fmtRate(overview.in_msgs_rate)}
        />
        <Card
          icon={<ArrowUpFromLine className="w-5 h-5 text-orange-500" />}
          label="Msgs Out/s"
          value={fmtRate(overview.out_msgs_rate)}
        />
        <Card
          icon={<Activity className="w-5 h-5 text-blue-500" />}
          label="Bytes In/s"
          value={fmtBytes(overview.in_bytes_rate)}
        />
        <Card
          icon={<Activity className="w-5 h-5 text-red-500" />}
          label="Bytes Out/s"
          value={fmtBytes(overview.out_bytes_rate)}
        />
        <Card
          icon={<GitBranch className="w-5 h-5 text-teal-500" />}
          label="Subscriptions"
          value={fmtNum(overview.subscriptions)}
        />
        <Card
          icon={<Database className="w-5 h-5 text-indigo-500" />}
          label="JS Streams"
          value={`${overview.js_streams} / ${fmtNum(overview.js_messages)} msgs`}
        />
      </div>

      <div className="flex items-center justify-between mb-3">
        <h2 className="text-lg font-semibold">Trends</h2>
        <TimeRangeSelector value={metrics.range} onChange={metrics.setRange} />
      </div>
      <div className="grid grid-cols-1 lg:grid-cols-2 gap-4 mb-8">
        <div className="bg-white dark:bg-gray-800 rounded-lg shadow p-4">
          <h3 className="text-sm font-medium text-gray-500 dark:text-gray-400 mb-2">Message Rate</h3>
          <TimeSeriesChart
            data={metrics.data}
            lines={[
              { key: 'in_msgs_rate', color: '#22c55e', label: 'In msgs/s' },
              { key: 'out_msgs_rate', color: '#f97316', label: 'Out msgs/s' },
            ]}
            yFormatter={fmtRate}
          />
        </div>
        <div className="bg-white dark:bg-gray-800 rounded-lg shadow p-4">
          <h3 className="text-sm font-medium text-gray-500 dark:text-gray-400 mb-2">Connections</h3>
          <TimeSeriesChart
            data={metrics.data}
            lines={[
              { key: 'connection_count', color: '#a855f7', label: 'Connections' },
            ]}
            yFormatter={(v) => fmtNum(v)}
          />
        </div>
      </div>

      <h2 className="text-lg font-semibold mb-3">Servers</h2>
      <div className="bg-white dark:bg-gray-800 rounded-lg shadow overflow-hidden">
        <table className="w-full text-sm">
          <thead className="bg-gray-50 dark:bg-gray-700 text-left text-gray-500 dark:text-gray-400">
            <tr>
              <th className="px-4 py-3">Name</th>
              <th className="px-4 py-3">Version</th>
              <th className="px-4 py-3">Connections</th>
              <th className="px-4 py-3">CPU</th>
              <th className="px-4 py-3">Memory</th>
              <th className="px-4 py-3">Msgs/s</th>
              <th className="px-4 py-3">Uptime</th>
              <th className="px-4 py-3">Health</th>
            </tr>
          </thead>
          <tbody className="divide-y divide-gray-100 dark:divide-gray-700">
            {overview.servers?.map((s) => (
              <tr key={s.id} className="hover:bg-gray-50 dark:hover:bg-gray-700/50">
                <td className="px-4 py-3 font-medium">
                  <Link to={`/servers/${s.id}`} className="block max-w-[220px] truncate text-brand-blue hover:underline" title={s.name || s.id}>{s.name || s.id}</Link>
                </td>
                <td className="px-4 py-3">{s.version}</td>
                <td className="px-4 py-3">{fmtNum(s.connections)}</td>
                <td className="px-4 py-3">{s.cpu.toFixed(1)}%</td>
                <td className="px-4 py-3">{fmtBytes(s.mem)}</td>
                <td className="px-4 py-3">
                  <span className="text-green-600 dark:text-green-400">{fmtRate(s.in_msgs_rate)}</span>
                  {' / '}
                  <span className="text-orange-600 dark:text-orange-400">{fmtRate(s.out_msgs_rate)}</span>
                </td>
                <td className="px-4 py-3">{s.uptime}</td>
                <td className="px-4 py-3">
                  <span className={`inline-block w-2.5 h-2.5 rounded-full ${s.healthy ? 'bg-healthy' : 'bg-unhealthy'}`} />
                </td>
              </tr>
            ))}
            {(!overview.servers || overview.servers.length === 0) && (
              <tr><td colSpan={8} className="px-4 py-8 text-center text-gray-400">No servers discovered yet</td></tr>
            )}
          </tbody>
        </table>
      </div>
    </div>
  )
}

function Card({ icon, label, value, sub }: { icon: React.ReactNode; label: string; value: string; sub?: string }) {
  return (
    <div className="bg-white dark:bg-gray-800 rounded-lg shadow p-4">
      <div className="flex items-center gap-2 mb-1">
        {icon}
        <span className="text-sm text-gray-500 dark:text-gray-400">{label}</span>
      </div>
      <div className="text-2xl font-semibold">{value}</div>
      {sub && <div className="text-xs text-gray-400">{sub}</div>}
    </div>
  )
}
