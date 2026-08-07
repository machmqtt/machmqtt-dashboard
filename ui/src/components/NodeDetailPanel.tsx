import { memo } from 'react'
import { X } from 'lucide-react'
import type { TopologyNode } from '../store/store'
import { Link } from 'react-router'
import { formatRate as fmtRate } from '../utils/format'

interface Props {
  node: TopologyNode
  onClose: () => void
}

export const NodeDetailPanel = memo(function NodeDetailPanel({ node, onClose }: Props) {
  return (
    <div className="fixed top-0 right-0 h-screen w-[400px] bg-white dark:bg-gray-800 shadow-xl border-l border-gray-200 dark:border-gray-700 z-50 overflow-y-auto">
      <div className="flex items-center justify-between p-4 border-b dark:border-gray-700">
        <h2 className="font-semibold text-lg">{node.name || node.id}</h2>
        <button onClick={onClose} className="text-gray-400 hover:text-gray-600 dark:hover:text-gray-200">
          <X className="w-5 h-5" />
        </button>
      </div>
      <div className="p-4 space-y-4">
        <div className="grid grid-cols-2 gap-3">
          <InfoItem label="Type" value={node.type} />
          <InfoItem label="Status" value={node.healthy ? 'Healthy' : 'Unhealthy'} />
          <InfoItem label="Connections" value={node.connections.toString()} />
          {node.cluster && <InfoItem label="Cluster" value={node.cluster} />}
          <InfoItem label="Msgs In/s" value={fmtRate(node.in_msgs_rate)} />
          <InfoItem label="Msgs Out/s" value={fmtRate(node.out_msgs_rate)} />
        </div>
        {node.type === 'server' && (
          <Link
            to={`/servers/${node.id}`}
            className="block text-center bg-brand-blue text-white rounded px-4 py-2 text-sm hover:opacity-90"
          >
            View Server Detail
          </Link>
        )}
      </div>
    </div>
  )
})

function InfoItem({ label, value }: { label: string; value: string }) {
  return (
    <div>
      <div className="text-xs text-gray-500 dark:text-gray-400">{label}</div>
      <div className="font-medium">{value}</div>
    </div>
  )
}
