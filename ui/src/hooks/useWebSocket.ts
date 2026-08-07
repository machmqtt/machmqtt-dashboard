import { useEffect, useRef } from 'react'
import { useStore } from '../store/store'
import { fetchWithTimeout } from '../utils/fetchWithTimeout'

export function useWebSocket() {
  const activeEnv = useStore((s) => s.activeEnv)
  const setOverview = useStore((s) => s.setOverview)
  const setTopology = useStore((s) => s.setTopology)
  const setHealth = useStore((s) => s.setHealth)
  const wsRef = useRef<WebSocket | null>(null)
  const retryRef = useRef(1000)

  useEffect(() => {
    if (!activeEnv) return

    let cancelled = false

    // Seed the live views once over REST so a page isn't stuck on a skeleton
    // when the WebSocket is slow to connect or never connects. The WS stream
    // then keeps them live. Only fill fields the WS hasn't already populated so
    // a fresher live update is never clobbered by the (possibly staler) seed.
    async function seed() {
      try {
        const [ov, topo] = await Promise.all([
          fetchWithTimeout(`/api/environments/${activeEnv}/overview`),
          fetchWithTimeout(`/api/environments/${activeEnv}/topology`),
        ])
        if (cancelled) return
        if (ov.ok && useStore.getState().overview == null) {
          setOverview(await ov.json())
        }
        if (topo.ok && useStore.getState().topology == null) {
          setTopology(await topo.json())
        }
      } catch {
        /* WS will populate if the REST seed fails */
      }
    }
    seed()

    function connect() {
      if (cancelled) return
      const proto = location.protocol === 'https:' ? 'wss:' : 'ws:'
      const ws = new WebSocket(`${proto}//${location.host}/api/ws`)
      wsRef.current = ws

      ws.onopen = () => {
        retryRef.current = 1000
        ws.send(JSON.stringify({ subscribe: activeEnv }))
      }

      ws.onmessage = (e) => {
        try {
          const msg = JSON.parse(e.data)
          if (msg.env !== activeEnv) return
          // React 18+ batches these automatically, but grouping by
          // message type makes intent explicit and avoids unnecessary calls.
          switch (msg.type) {
            case 'overview': setOverview(msg.data); break
            case 'topology': setTopology(msg.data); break
            case 'health': setHealth(msg.data); break
          }
        } catch { /* ignore parse errors from malformed messages */ }
      }

      ws.onclose = () => {
        if (cancelled) return
        const delay = Math.min(retryRef.current, 30000)
        retryRef.current = delay * 2
        setTimeout(connect, delay)
      }
    }

    connect()

    return () => {
      cancelled = true
      wsRef.current?.close()
    }
  }, [activeEnv, setOverview, setTopology, setHealth])
}
