// Modal — accessible dialog: focus trap, ESC to close, backdrop click to cancel,
// focus returned to the trigger on close. Rounded card matching the mockup.

import { useCallback, useEffect, useRef, type ReactNode } from 'react'
import { T } from '../lib/theme'
import { Icon } from './Icon'

interface ModalProps {
  open: boolean
  onClose: () => void
  title?: ReactNode
  subtitle?: ReactNode
  children: ReactNode
  footer?: ReactNode
  width?: number
}

const FOCUSABLE =
  'a[href], button:not([disabled]), textarea:not([disabled]), input:not([disabled]), select:not([disabled]), [tabindex]:not([tabindex="-1"])'

export function Modal({ open, onClose, title, subtitle, children, footer, width = 480 }: ModalProps) {
  const dialogRef = useRef<HTMLDivElement>(null)
  const restoreRef = useRef<HTMLElement | null>(null)

  const focusables = useCallback((): HTMLElement[] => {
    const root = dialogRef.current
    if (!root) return []
    return Array.from(root.querySelectorAll<HTMLElement>(FOCUSABLE))
  }, [])

  // Remember the trigger and move focus into the dialog on open; restore on close.
  useEffect(() => {
    if (!open) return
    restoreRef.current = document.activeElement as HTMLElement | null
    const els = focusables()
    ;(els[0] ?? dialogRef.current)?.focus()
    return () => {
      restoreRef.current?.focus?.()
    }
  }, [open, focusables])

  // ESC to close + Tab focus trap.
  useEffect(() => {
    if (!open) return
    function onKeyDown(e: KeyboardEvent) {
      if (e.key === 'Escape') {
        e.stopPropagation()
        onClose()
        return
      }
      if (e.key !== 'Tab') return
      const els = focusables()
      if (els.length === 0) {
        e.preventDefault()
        return
      }
      const first = els[0]
      const last = els[els.length - 1]
      const active = document.activeElement
      if (e.shiftKey && active === first) {
        e.preventDefault()
        last.focus()
      } else if (!e.shiftKey && active === last) {
        e.preventDefault()
        first.focus()
      }
    }
    document.addEventListener('keydown', onKeyDown, true)
    return () => document.removeEventListener('keydown', onKeyDown, true)
  }, [open, onClose, focusables])

  if (!open) return null

  return (
    <div
      onMouseDown={e => {
        if (e.target === e.currentTarget) onClose()
      }}
      style={{
        position: 'fixed',
        inset: 0,
        zIndex: 100,
        background: 'rgba(20,28,44,.38)',
        display: 'flex',
        alignItems: 'flex-start',
        justifyContent: 'center',
        padding: '10vh 20px 20px',
        overflowY: 'auto',
      }}
    >
      <div
        ref={dialogRef}
        role="dialog"
        aria-modal="true"
        aria-label={typeof title === 'string' ? title : undefined}
        tabIndex={-1}
        style={{
          width: '100%',
          maxWidth: width,
          background: T.surface,
          border: `1px solid ${T.border}`,
          borderRadius: T.rXl,
          boxShadow: T.shadowModal,
          outline: 'none',
        }}
      >
        {(title || subtitle) && (
          <div style={{ padding: '22px 24px 0', position: 'relative' }}>
            <button
              onClick={onClose}
              aria-label="Close"
              style={{
                position: 'absolute',
                top: 18,
                right: 18,
                display: 'inline-flex',
                alignItems: 'center',
                justifyContent: 'center',
                width: 30,
                height: 30,
                borderRadius: 8,
                border: 'none',
                background: 'transparent',
                color: T.subtle,
                cursor: 'pointer',
              }}
            >
              <Icon name="x" size={17} />
            </button>
            {title && (
              <h2 style={{ fontSize: 19, fontWeight: 700, letterSpacing: '-.01em', color: T.text, margin: 0, paddingRight: 34 }}>
                {title}
              </h2>
            )}
            {subtitle && <p style={{ fontSize: 13.5, color: T.muted, margin: '6px 0 0', lineHeight: 1.5 }}>{subtitle}</p>}
          </div>
        )}

        <div style={{ padding: '18px 24px 4px' }}>{children}</div>

        {footer && (
          <div
            style={{
              display: 'flex',
              alignItems: 'center',
              justifyContent: 'flex-end',
              gap: 10,
              padding: '16px 24px 22px',
            }}
          >
            {footer}
          </div>
        )}
      </div>
    </div>
  )
}
