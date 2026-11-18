import { onUnmounted } from 'vue'
import api from '../api'

/**
 * Listens to the tenant's Server-Sent Events stream (GET /tenants/:id/events)
 * and calls `onEvent` with each event name. Uses fetch rather than EventSource
 * so the Bearer token can be sent in the Authorization header. Reconnects with
 * backoff and stops when the calling component unmounts.
 */
export function useTenantEvents(tenantId: () => string, onEvent: (event: string) => void) {
  let controller: AbortController | null = null
  let stopped = false
  let retryTimer: ReturnType<typeof setTimeout> | null = null
  let backoff = 1000

  async function connect() {
    if (stopped) return
    controller = new AbortController()
    try {
      const token = localStorage.getItem('cqa_access_token')
      const res = await fetch(`/api/v1/tenants/${tenantId()}/events`, {
        headers: { Accept: 'text/event-stream', ...(token ? { Authorization: `Bearer ${token}` } : {}) },
        signal: controller.signal,
      })
      if (res.status === 401) {
        // Let the axios interceptor refresh the access token, then retry.
        await api.get('/profile').catch(() => {})
        throw new Error('unauthorized')
      }
      if (!res.ok || !res.body) throw new Error(`stream_failed_${res.status}`)

      backoff = 1000
      const reader = res.body.getReader()
      const decoder = new TextDecoder()
      let buffer = ''
      for (;;) {
        const { value, done } = await reader.read()
        if (done) break
        buffer += decoder.decode(value, { stream: true })
        let sep: number
        while ((sep = buffer.indexOf('\n\n')) !== -1) {
          const block = buffer.slice(0, sep)
          buffer = buffer.slice(sep + 2)
          const line = block.split('\n').find((l) => l.startsWith('event:'))
          if (line) onEvent(line.slice(6).trim())
        }
      }
    } catch {
      // fall through to reconnect
    }
    if (stopped) return
    retryTimer = setTimeout(connect, backoff)
    backoff = Math.min(backoff * 2, 30000)
  }

  function stop() {
    stopped = true
    if (retryTimer) clearTimeout(retryTimer)
    controller?.abort()
  }

  connect()
  onUnmounted(stop)
  return { stop }
}
