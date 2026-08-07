import { useState, useEffect, useCallback, useMemo } from 'react'
import { fetchWithTimeout } from '../utils/fetchWithTimeout'

export type TimeRange = '1h' | '6h' | '24h'

const RANGE_SECONDS: Record<TimeRange, number> = {
  '1h': 3600,
  '6h': 21600,
  '24h': 86400,
}

const REFRESH_INTERVAL = 30_000

interface UseMetricsResult {
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  data: Record<string, any>[]
  loading: boolean
  range: TimeRange
  setRange: (r: TimeRange) => void
}

export function useMetrics(
  env: string | null,
  endpoint: string,
  params?: Record<string, string>
): UseMetricsResult {
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  const [data, setData] = useState<Record<string, any>[]>([])
  const [loading, setLoading] = useState(true)
  const [range, setRange] = useState<TimeRange>('1h')
	const paramsKey = useMemo(
		() => JSON.stringify(Object.entries(params || {}).sort(([a], [b]) => a.localeCompare(b))),
		[params],
	)

  const fetchData = useCallback(async (signal?: AbortSignal) => {
    if (!env) return
    setLoading(true)
    const now = Math.floor(Date.now() / 1000)
    const from = now - RANGE_SECONDS[range]

    const search = new URLSearchParams({ from: from.toString(), to: now.toString() })
		const stableParams = JSON.parse(paramsKey) as [string, string][]
		if (stableParams.length > 0) {
		for (const [k, v] of stableParams) {
        if (v) search.set(k, v)
      }
    }

    try {
			const res = await fetchWithTimeout(`/api/environments/${env}/${endpoint}?${search}`, { signal })
      if (res.ok) {
        const json = await res.json()
        setData(json.points || [])
      }
		} catch {
			// The next jittered poll retries transient failures.
    }
		if (!signal?.aborted) setLoading(false)
	}, [env, endpoint, range, paramsKey])

  useEffect(() => {
    if (!env) return
		const controller = new AbortController()
		let timer: ReturnType<typeof setTimeout> | undefined
		const poll = async () => {
			await fetchData(controller.signal)
			if (!controller.signal.aborted) {
				const jitter = Math.floor(Math.random() * 5000)
				timer = setTimeout(poll, REFRESH_INTERVAL + jitter)
			}
		}
		void poll()
		return () => {
			controller.abort()
			if (timer) clearTimeout(timer)
		}
  }, [env, fetchData])

  return { data, loading, range, setRange }
}
