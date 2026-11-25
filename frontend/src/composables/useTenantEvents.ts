import { onUnmounted } from 'vue'
import api from '../api'

/**
 * Listens to the tenant's Server-Sent Events stream (GET /tenants/:id/events)
 * and calls `onEvent` with each event name. Uses fetch rather than EventSource
 * so the Bearer token can be sent in the Authorization header. Reconnects with
 * backoff and stops when the calling component unmounts.
 */
export function useTenantEvents(tenantId: () => string, onEvent: (event: string) => void, onConnected?: () => void) {
  let controller: AbortController | null = null
  let stopped = false
  let retryTimer: ReturnType<typeof setTimeout> | null = null
  let backoff = 1000

  async function connect() {
    if (stopped) return
    retryTimer = null
    const attempt = new AbortController()
    controller = attempt
    try {
      const token = localStorage.getItem('cqa_access_token')
      const res = await fetch(`/api/v1/tenants/${tenantId()}/events`, {
        headers: { Accept: 'text/event-stream', ...(token ? { Authorization: `Bearer ${token}` } : {}) },
        signal: attempt.signal,
      })
      if (attempt.signal.aborted) return
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
      onConnected?.()
      for (;;) {
        const { value, done } = await reader.read()
        if (done || attempt.signal.aborted) break
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
    if (stopped || attempt.signal.aborted) return
    retryTimer = setTimeout(connect, backoff)
    backoff = Math.min(backoff * 2, 30000)
  }

  function reconnect() {
    if (stopped) return
    if (retryTimer !== null) clearTimeout(retryTimer)
    controller?.abort()
    backoff = 1000
    void connect()
  }

  function stop() {
    stopped = true
    if (retryTimer !== null) clearTimeout(retryTimer)
    controller?.abort()
    window.removeEventListener('online', reconnect)
  }

  window.addEventListener('online', reconnect)
  connect()
  onUnmounted(stop)
  return { stop }
}

// Refresh a REST snapshot on a matching signal and whenever the stream opens.
// A signal during a refresh queues one more snapshot after that request ends.
export function useTenantEventRefresh(tenantId: () => string, event: string, refresh: () => Promise<void>) {
  let refreshing = false
  let pending = false

  async function refreshLatest() {
    pending = true
    if (refreshing) return
    refreshing = true
    try {
      while (pending) {
        pending = false
        await refresh()
      }
    } finally {
      refreshing = false
    }
  }

  const schedule = () => { void refreshLatest() }
  useTenantEvents(tenantId, (name) => {
    if (name === event) schedule()
  }, schedule)
  onUnmounted(() => { pending = false })
}
