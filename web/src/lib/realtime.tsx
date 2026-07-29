// Realtime: one authenticated SSE connection per session (/events/stream).
// Events are invalidation signals — pages subscribe to the types they care
// about and refetch their own queries; a reconnect fires every listener so a
// disconnect can never lose state permanently.

import { createContext, useContext, useEffect, useRef, type ReactNode } from 'react'
import { bearer } from './token'
import { useIdentity } from './identity'

interface Listener {
  types: Set<string>
  fire: () => void
}

interface RealtimeContextValue {
  register(types: string[], fire: () => void): () => void
}

const RealtimeContext = createContext<RealtimeContextValue | null>(null)

export function RealtimeProvider({ children }: { children: ReactNode }) {
  const { authed, memberID } = useIdentity()
  const listeners = useRef(new Set<Listener>())

  useEffect(() => {
    if (!authed || !memberID) return

    let stopped = false
    let controller: AbortController | null = null
    let retryMs = 1_000

    const dispatch = (type: string) => {
      for (const l of listeners.current) {
        if (type === 'open' || l.types.has(type)) l.fire()
      }
    }

    async function pump(): Promise<void> {
      while (!stopped) {
        try {
          const token = await bearer()
          controller = new AbortController()
          const resp = await fetch('/events/stream', {
            headers: token ? { Authorization: `Bearer ${token}` } : {},
            signal: controller.signal,
          })
          if (!resp.ok || !resp.body) throw new Error(`stream ${resp.status}`)
          // A proxy or dev-server fallback answering HTML must read as a
          // failed connection, never as a stream (reconnect-refetch storm).
          if (!resp.headers.get('content-type')?.includes('text/event-stream')) {
            throw new Error('not an event stream')
          }

          retryMs = 1_000
          dispatch('open') // heal anything missed while disconnected

          const reader = resp.body.getReader()
          const decoder = new TextDecoder()
          let buf = ''

          for (;;) {
            const { done, value } = await reader.read()
            if (done) break
            buf += decoder.decode(value, { stream: true })

            let sep: number
            while ((sep = buf.indexOf('\n\n')) >= 0) {
              const frame = buf.slice(0, sep)
              buf = buf.slice(sep + 2)
              const eventLine = frame.split('\n').find(l => l.startsWith('event: '))
              if (eventLine) dispatch(eventLine.slice('event: '.length).trim())
            }
          }
        } catch {
          // network error or abort — retried below unless stopped
        }

        if (stopped) return
        await new Promise(r => setTimeout(r, retryMs + Math.random() * 500))
        retryMs = Math.min(retryMs * 2, 30_000)
      }
    }

    void pump()

    return () => {
      stopped = true
      controller?.abort()
    }
  }, [authed, memberID])

  const value = useRef<RealtimeContextValue>({
    register(types, fire) {
      const listener: Listener = { types: new Set(types), fire }
      listeners.current.add(listener)
      return () => {
        listeners.current.delete(listener)
      }
    },
  })

  return <RealtimeContext.Provider value={value.current}>{children}</RealtimeContext.Provider>
}

// useRealtime re-runs cb when one of the event types arrives (or the stream
// (re)connects). Bursts are coalesced (250ms) so one screenful of events
// causes one refetch.
export function useRealtime(types: string[], cb: () => void) {
  const ctx = useContext(RealtimeContext)
  const cbRef = useRef(cb)
  cbRef.current = cb

  const key = types.join(',')

  useEffect(() => {
    if (!ctx) return

    let timer: number | null = null
    const fire = () => {
      if (timer != null) return
      timer = window.setTimeout(() => {
        timer = null
        cbRef.current()
      }, 250)
    }

    const unregister = ctx.register(key.split(','), fire)

    return () => {
      unregister()
      if (timer != null) window.clearTimeout(timer)
    }
  }, [ctx, key])
}
