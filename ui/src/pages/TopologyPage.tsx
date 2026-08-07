import { useStore } from '../store/store'
import { TopologyGraphView } from '../components/TopologyGraph'
import { Skeleton, NoClusterEmptyState } from '../components/Skeleton'

export function TopologyPage() {
  const topology = useStore((s) => s.topology)
  const environments = useStore((s) => s.environments)
  const activeEnv = useStore((s) => s.activeEnv)

  if (environments.length === 0 || !activeEnv) {
    return (
      <NoClusterEmptyState
        title="Cluster Topology"
        description="Add a NATS cluster to visualize server routes, gateways, leaf nodes, and MachMQTT bridges."
      />
    )
  }

  if (!topology) {
    return (
      <div>
        <h1 className="text-2xl font-semibold mb-4">Cluster Topology</h1>
        <div className="bg-white dark:bg-gray-800 rounded-lg shadow p-8 flex items-center justify-center" style={{ height: 'calc(100vh - 140px)' }}>
          <div className="text-center space-y-4">
            <Skeleton className="w-32 h-32 rounded-full mx-auto" />
            <Skeleton className="w-48 h-4 mx-auto" />
          </div>
        </div>
      </div>
    )
  }

  if (!topology.nodes || topology.nodes.length === 0) {
    return (
      <div>
        <h1 className="text-2xl font-semibold mb-4">Cluster Topology</h1>
        <div className="text-gray-500 dark:text-gray-400 bg-white dark:bg-gray-800 rounded-lg shadow p-8 text-center">
          No servers discovered yet.
        </div>
      </div>
    )
  }

  return (
    <div>
      <h1 className="text-2xl font-semibold mb-4">Cluster Topology</h1>
      <TopologyGraphView data={topology} />
    </div>
  )
}
