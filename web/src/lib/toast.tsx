// Toast provider + useToast hook — lightweight, no dependency.
// Mount <ToastProvider> once at the app root; call useToast().success/error
// from any page. Toasts auto-dismiss after AUTO_DISMISS_MS.

import { createContext, useCallback, useContext, useMemo, useRef, useState, type ReactNode } from 'react'
import { ToastViewport, type ToastItem, type ToastVariant } from '../components/Toast'

const AUTO_DISMISS_MS = 4000
// Identical (variant, message) pairs arriving within this window are dropped:
// a retry loop or a burst of failing background calls must not stack the same
// error N times.
const DEDUPE_MS = 2500

interface ToastApi {
  show: (variant: ToastVariant, message: string) => void
  success: (message: string) => void
  error: (message: string) => void
  info: (message: string) => void
}

const ToastContext = createContext<ToastApi | null>(null)

export function ToastProvider({ children }: { children: ReactNode }) {
  const [toasts, setToasts] = useState<ToastItem[]>([])
  const nextId = useRef(1)

  const dismiss = useCallback((id: number) => {
    setToasts(prev => prev.filter(t => t.id !== id))
  }, [])

  const lastShown = useRef<{ key: string; at: number }>({ key: '', at: 0 })

  const show = useCallback(
    (variant: ToastVariant, message: string) => {
      // Drop rapid duplicates: a failing background call retried in a loop must
      // not stack the same message N times.
      const key = `${variant}:${message}`
      const now = Date.now()
      if (lastShown.current.key === key && now - lastShown.current.at < DEDUPE_MS) return
      lastShown.current = { key, at: now }

      const id = nextId.current++
      setToasts(prev => [...prev, { id, variant, message }])
      window.setTimeout(() => dismiss(id), AUTO_DISMISS_MS)
    },
    [dismiss],
  )

  // The api object identity MUST be stable across renders: it is the context
  // value, and consumers legitimately put `toast` in dependency arrays. The
  // previous version rebuilt it on every render, so each shown toast changed
  // the context identity → consumers' callbacks were re-created → their
  // effects re-ran → refetched → failed → toasted again: an infinite
  // error-toast loop whenever the API was unreachable (server restart).
  const api = useMemo<ToastApi>(
    () => ({
      show,
      success: (m: string) => show('success', m),
      error: (m: string) => show('error', m),
      info: (m: string) => show('info', m),
    }),
    [show],
  )

  return (
    <ToastContext.Provider value={api}>
      {children}
      <ToastViewport toasts={toasts} onDismiss={dismiss} />
    </ToastContext.Provider>
  )
}

export function useToast(): ToastApi {
  const ctx = useContext(ToastContext)
  if (!ctx) throw new Error('useToast must be used within a ToastProvider')
  return ctx
}

// toastMessage normalizes an unknown error into a human string for toasts.
export function toastMessage(err: unknown): string {
  return err instanceof Error ? err.message : String(err)
}
