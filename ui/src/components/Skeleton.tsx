import { Link } from 'react-router'
import { Server } from 'lucide-react'

export function NoClusterEmptyState({ title, description }: { title: string; description?: string }) {
  return (
    <div>
      <h1 className="text-2xl font-semibold mb-6">{title}</h1>
      <div className="bg-white dark:bg-gray-800 rounded-lg shadow p-12 text-center">
        <Server className="w-12 h-12 text-gray-300 dark:text-gray-600 mx-auto mb-4" />
        <h2 className="text-lg font-semibold text-gray-700 dark:text-gray-200 mb-2">No clusters configured</h2>
        <p className="text-sm text-gray-500 dark:text-gray-400 mb-6 max-w-sm mx-auto">
          {description ?? 'Add a NATS cluster to start monitoring your infrastructure.'}
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

export function Skeleton({ className = '' }: { className?: string }) {
  return <div className={`animate-pulse bg-gray-200 dark:bg-gray-700 rounded ${className}`} />
}

export function CardSkeleton() {
  return (
    <div className="bg-white dark:bg-gray-800 rounded-lg shadow p-4">
      <Skeleton className="h-4 w-24 mb-2" />
      <Skeleton className="h-8 w-32" />
    </div>
  )
}

export function TableSkeleton({ rows = 5, cols = 6 }: { rows?: number; cols?: number }) {
  return (
    <div className="bg-white dark:bg-gray-800 rounded-lg shadow overflow-hidden">
      <div className="bg-gray-50 dark:bg-gray-700 px-4 py-3">
        <Skeleton className="h-4 w-full" />
      </div>
      {Array.from({ length: rows }).map((_, i) => (
        <div key={i} className="flex gap-4 px-4 py-3 border-t border-gray-100 dark:border-gray-700">
          {Array.from({ length: cols }).map((_, j) => (
            <Skeleton key={j} className="h-4 flex-1" />
          ))}
        </div>
      ))}
    </div>
  )
}
