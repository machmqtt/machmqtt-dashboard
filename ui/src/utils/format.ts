// Shared display formatters. These were previously copy-pasted across ~10 pages
// and had quietly diverged, so the same value could render differently on
// different screens. Keep all number/byte/rate formatting here.

/** Compact count: 1.2K, 3.4M, 5.6B; below 1000 uses locale grouping.
 * Absent fields render as 0: the metric pages pass fields straight off wire
 * payloads, and one missing field must not take the whole page down. */
export function formatNumber(n: number | null | undefined): string {
  if (n == null) return '0'
  if (n >= 1e9) return (n / 1e9).toFixed(1) + 'B'
  if (n >= 1e6) return (n / 1e6).toFixed(1) + 'M'
  if (n >= 1e3) return (n / 1e3).toFixed(1) + 'K'
  return n.toLocaleString()
}

/** Byte size: GB / MB / KB / B. Absent fields render as 0 B (see formatNumber). */
export function formatBytes(b: number | null | undefined): string {
  if (b == null) return '0 B'
  if (b >= 1e9) return (b / 1e9).toFixed(1) + ' GB'
  if (b >= 1e6) return (b / 1e6).toFixed(1) + ' MB'
  if (b >= 1e3) return (b / 1e3).toFixed(1) + ' KB'
  return b + ' B'
}

/** Message rate without a unit suffix (inline labels and chart axes). */
export function formatRate(r: number): string {
  if (r >= 1e6) return (r / 1e6).toFixed(1) + 'M'
  if (r >= 1e3) return (r / 1e3).toFixed(1) + 'K'
  if (r >= 1) return r.toFixed(0)
  if (r > 0) return r.toFixed(2)
  return '0'
}

/** Message rate with a "/s" suffix. */
export function formatRatePerSec(r: number): string {
  if (r >= 1e6) return (r / 1e6).toFixed(1) + 'M/s'
  if (r >= 1e3) return (r / 1e3).toFixed(1) + 'K/s'
  if (r >= 1) return r.toFixed(0) + '/s'
  if (r > 0) return r.toFixed(1) + '/s'
  return '0/s'
}

/** Byte rate with a "/s" suffix. */
export function formatBytesPerSec(b: number): string {
  if (b >= 1e9) return (b / 1e9).toFixed(1) + ' GB/s'
  if (b >= 1e6) return (b / 1e6).toFixed(1) + ' MB/s'
  if (b >= 1e3) return (b / 1e3).toFixed(1) + ' KB/s'
  if (b > 0) return b.toFixed(0) + ' B/s'
  return '0 B/s'
}
