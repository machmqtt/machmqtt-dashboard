import { useState, useEffect } from 'react'
import { fetchWithTimeout } from '../utils/fetchWithTimeout'
import { useStore } from '../store/store'
import { TableSkeleton, CardSkeleton, NoClusterEmptyState } from '../components/Skeleton'
import { ChevronDown, ChevronRight } from 'lucide-react'

interface ConsumerInfo {
  stream_name: string
  name: string
  config: { durable_name?: string; filter_subject?: string; deliver_policy: string; ack_policy: string }
  delivered: { consumer_seq: number; stream_seq: number }
  ack_floor: { consumer_seq: number; stream_seq: number }
  num_ack_pending: number
  num_redelivered: number
  num_pending: number
}

interface StreamDetail {
  name: string
  config: { subjects?: string[]; retention: string; storage: string; num_replicas: number }
  state: { messages: number; bytes: number; consumers: number; first_seq: number; last_seq: number }
  consumer_detail?: ConsumerInfo[]
}

interface AccountDetail {
  name: string
  memory: number
  storage: number
  stream_detail?: StreamDetail[]
}

interface JSData {
  streams: number
  consumers: number
  messages: number
  bytes: number
  memory: number
  storage: number
  account_details?: AccountDetail[]
}

export function JetStreamPage() {
  const activeEnv = useStore((s) => s.activeEnv)
  const environments = useStore((s) => s.environments)
  const [data, setData] = useState<Record<string, JSData> | null>(null)
  const [loading, setLoading] = useState(true)
  const [expandedStream, setExpandedStream] = useState<string | null>(null)
  const [filterAccount, setFilterAccount] = useState('')

  useEffect(() => {
    if (!activeEnv) return
    const run = async () => {
      setLoading(true)
      try {
        const r = await fetchWithTimeout(`/api/environments/${activeEnv}/jsz`)
        if (r.ok) setData(await r.json())
      } catch { /* */ }
      setLoading(false)
    }
    run()
  }, [activeEnv])

  const allStreams: { account: string; stream: StreamDetail }[] = []
  const accounts = new Set<string>()
  if (data) {
    for (const js of Object.values(data)) {
      for (const acc of js.account_details || []) {
        accounts.add(acc.name)
        for (const s of acc.stream_detail || []) {
          if (!filterAccount || acc.name === filterAccount) {
            allStreams.push({ account: acc.name, stream: s })
          }
        }
      }
    }
  }

  // Aggregate stats.
  let totalStreams = 0, totalConsumers = 0, totalMsgs = 0, totalBytes = 0
  if (data) {
    for (const js of Object.values(data)) {
      totalStreams += js.streams; totalConsumers += js.consumers
      totalMsgs += js.messages; totalBytes += js.bytes
    }
  }

  if (environments.length === 0 || !activeEnv) {
    return (
      <NoClusterEmptyState
        title="JetStream"
        description="Add a NATS cluster to monitor JetStream streams, consumers, and storage."
      />
    )
  }

  return (
    <div>
      <h1 className="text-2xl font-semibold mb-4">JetStream</h1>

      {loading ? (
        <>
          <div className="grid grid-cols-2 lg:grid-cols-4 gap-4 mb-6">
            {Array.from({ length: 4 }).map((_, i) => <CardSkeleton key={i} />)}
          </div>
          <TableSkeleton rows={3} cols={5} />
        </>
      ) : (
        <>
          <div className="grid grid-cols-2 lg:grid-cols-4 gap-4 mb-6">
            <SC label="Streams" value={totalStreams.toString()} />
            <SC label="Consumers" value={totalConsumers.toString()} />
            <SC label="Messages" value={totalMsgs.toLocaleString()} />
            <SC label="Storage" value={fmtBytes(totalBytes)} />
          </div>

          {accounts.size > 1 && (
            <div className="mb-4">
              <select
                value={filterAccount}
                onChange={(e) => setFilterAccount(e.target.value)}
                className="border dark:border-gray-600 dark:bg-gray-800 rounded px-3 py-1.5 text-sm"
              >
                <option value="">All accounts</option>
                {[...accounts].sort().map((a) => <option key={a} value={a}>{a}</option>)}
              </select>
            </div>
          )}

          {allStreams.length === 0 ? (
            <div className="text-gray-500 dark:text-gray-400 bg-white dark:bg-gray-800 rounded-lg shadow p-8 text-center">
              No JetStream streams found.
            </div>
          ) : (
            <div className="space-y-2">
              {allStreams.map(({ account, stream }) => {
                const key = `${account}/${stream.name}`
                const isExpanded = expandedStream === key
                return (
                  <div key={key} className="bg-white dark:bg-gray-800 rounded-lg shadow">
                    <button
                      onClick={() => setExpandedStream(isExpanded ? null : key)}
                      className="w-full flex items-center justify-between px-4 py-3 text-left hover:bg-gray-50 dark:hover:bg-gray-700/50"
                    >
                      <div className="flex items-center gap-3">
                        {isExpanded ? <ChevronDown className="w-4 h-4" /> : <ChevronRight className="w-4 h-4" />}
                        <span className="font-medium">{stream.name}</span>
                        <span className="text-xs text-gray-500 bg-gray-100 dark:bg-gray-700 rounded px-2 py-0.5">{stream.config.storage}</span>
                        <span className="text-xs text-gray-500 bg-gray-100 dark:bg-gray-700 rounded px-2 py-0.5">R{stream.config.num_replicas}</span>
                        {accounts.size > 1 && (
                          <span className="text-xs text-gray-400">{account}</span>
                        )}
                      </div>
                      <div className="flex gap-6 text-sm text-gray-500 dark:text-gray-400">
                        <span>{stream.state.messages.toLocaleString()} msgs</span>
                        <span>{fmtBytes(stream.state.bytes)}</span>
                        <span>{stream.state.consumers} consumers</span>
                      </div>
                    </button>

                    {isExpanded && (
                      <div className="border-t dark:border-gray-700 px-4 py-3">
                        <div className="grid grid-cols-2 md:grid-cols-4 gap-4 text-sm mb-4">
                          <div><span className="text-gray-500 dark:text-gray-400">Subjects:</span> {stream.config.subjects?.join(', ') || '-'}</div>
                          <div><span className="text-gray-500 dark:text-gray-400">Retention:</span> {stream.config.retention}</div>
                          <div><span className="text-gray-500 dark:text-gray-400">Seq Range:</span> {stream.state.first_seq} - {stream.state.last_seq}</div>
                          <div><span className="text-gray-500 dark:text-gray-400">Storage:</span> {stream.config.storage}</div>
                        </div>

                        {stream.consumer_detail && stream.consumer_detail.length > 0 ? (
                          <div>
                            <h3 className="font-medium text-sm mb-2">Consumers ({stream.consumer_detail.length})</h3>
                            <div className="overflow-x-auto">
                              <table className="w-full text-sm">
                                <thead className="bg-gray-50 dark:bg-gray-700 text-left text-gray-500 dark:text-gray-400">
                                  <tr>
                                    <th className="px-3 py-2">Name</th>
                                    <th className="px-3 py-2">Filter</th>
                                    <th className="px-3 py-2">Delivered</th>
                                    <th className="px-3 py-2">Ack Floor</th>
                                    <th className="px-3 py-2">Ack Pending</th>
                                    <th className="px-3 py-2">Redelivered</th>
                                    <th className="px-3 py-2">Pending</th>
                                  </tr>
                                </thead>
                                <tbody className="divide-y divide-gray-100 dark:divide-gray-700">
                                  {stream.consumer_detail.map((c) => (
                                    <tr key={c.name} className="hover:bg-gray-50 dark:hover:bg-gray-700/50">
                                      <td className="px-3 py-2 font-medium">{c.name}</td>
                                      <td className="px-3 py-2 font-mono text-xs">{c.config.filter_subject || '*'}</td>
                                      <td className="px-3 py-2">{c.delivered.consumer_seq.toLocaleString()}</td>
                                      <td className="px-3 py-2">{c.ack_floor.consumer_seq.toLocaleString()}</td>
                                      <td className="px-3 py-2">{c.num_ack_pending.toLocaleString()}</td>
                                      <td className="px-3 py-2">{c.num_redelivered.toLocaleString()}</td>
                                      <td className="px-3 py-2">{c.num_pending.toLocaleString()}</td>
                                    </tr>
                                  ))}
                                </tbody>
                              </table>
                            </div>
                          </div>
                        ) : (
                          <div className="text-sm text-gray-400">No consumers.</div>
                        )}
                      </div>
                    )}
                  </div>
                )
              })}
            </div>
          )}
        </>
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

function fmtBytes(b: number): string {
  if (b >= 1e9) return (b / 1e9).toFixed(1) + ' GB'
  if (b >= 1e6) return (b / 1e6).toFixed(1) + ' MB'
  if (b >= 1e3) return (b / 1e3).toFixed(1) + ' KB'
  return b + ' B'
}
