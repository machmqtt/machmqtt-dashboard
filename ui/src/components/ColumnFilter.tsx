import type { Column } from '@tanstack/react-table'

export function ColumnFilter<T>({ column }: { column: Column<T, unknown> }) {
  const value = (column.getFilterValue() as string) ?? ''

  return (
    <input
      value={value}
      onChange={(e) => column.setFilterValue(e.target.value || undefined)}
      onClick={(e) => e.stopPropagation()}
      placeholder="Filter..."
      className="mt-1 w-full border dark:border-gray-600 dark:bg-gray-800 rounded px-1.5 py-0.5 text-xs font-normal text-gray-700 dark:text-gray-300 placeholder:text-gray-400 dark:placeholder:text-gray-500 outline-none focus:ring-1 focus:ring-brand-blue"
    />
  )
}
