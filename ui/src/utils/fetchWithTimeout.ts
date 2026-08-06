const DEFAULT_TIMEOUT = 15_000

export function fetchWithTimeout(
  url: string,
  opts?: RequestInit & { timeout?: number },
): Promise<Response> {
  const { timeout = DEFAULT_TIMEOUT, signal, ...fetchOpts } = opts || {}
  const controller = new AbortController()
  const id = setTimeout(() => controller.abort(), timeout)
	const combinedSignal = signal
		? AbortSignal.any([signal, controller.signal])
		: controller.signal
  return fetch(url, { ...fetchOpts, signal: combinedSignal }).finally(() =>
    clearTimeout(id),
  )
}
