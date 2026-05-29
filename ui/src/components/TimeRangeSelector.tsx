import type { TimeRange } from '../hooks/useMetrics'

const RANGES: TimeRange[] = ['1h', '6h', '24h']

interface Props {
  value: TimeRange
  onChange: (r: TimeRange) => void
}

export function TimeRangeSelector({ value, onChange }: Props) {
  return (
    <div className="flex gap-1">
      {RANGES.map((r) => (
        <button
          key={r}
          onClick={() => onChange(r)}
          className={`px-2.5 py-1 text-xs rounded font-medium transition-colors ${
            value === r
              ? 'bg-brand-blue text-white'
              : 'bg-gray-100 dark:bg-gray-700 text-gray-600 dark:text-gray-300 hover:bg-gray-200 dark:hover:bg-gray-600'
          }`}
        >
          {r}
        </button>
      ))}
    </div>
  )
}
