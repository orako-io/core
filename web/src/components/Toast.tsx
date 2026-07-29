// ToastViewport — the visual stack of toasts (bottom-right). Purely presentational;
// state lives in the ToastProvider (../lib/toast).

import { T } from '../lib/theme'
import { Icon } from './Icon'

export type ToastVariant = 'success' | 'error' | 'info'

export interface ToastItem {
  id: number
  variant: ToastVariant
  message: string
}

const TONE: Record<ToastVariant, { bg: string; border: string; ink: string; icon: 'check' | 'x' | 'send' }> = {
  success: { bg: T.successBg, border: T.successBorder, ink: T.successInk, icon: 'check' },
  error: { bg: T.dangerBg, border: T.dangerBorder, ink: T.dangerInk, icon: 'x' },
  info: { bg: T.accentSofter, border: T.accentBorder, ink: T.accentInk, icon: 'send' },
}

export function ToastViewport({ toasts, onDismiss }: { toasts: ToastItem[]; onDismiss: (id: number) => void }) {
  return (
    <div
      style={{
        position: 'fixed',
        right: 20,
        bottom: 20,
        zIndex: 200,
        display: 'flex',
        flexDirection: 'column',
        gap: 10,
        maxWidth: 380,
      }}
    >
      {toasts.map(t => {
        const tone = TONE[t.variant]
        return (
          <div
            key={t.id}
            role="status"
            style={{
              display: 'flex',
              alignItems: 'flex-start',
              gap: 10,
              background: tone.bg,
              border: `1px solid ${tone.border}`,
              borderRadius: T.rLg,
              boxShadow: T.shadowPop,
              padding: '11px 12px 11px 13px',
              color: tone.ink,
              fontSize: 13.5,
              lineHeight: 1.45,
            }}
          >
            <span style={{ flex: 'none', marginTop: 1 }}>
              <Icon name={tone.icon} size={16} color={tone.ink} strokeWidth={2.2} />
            </span>
            <span style={{ flex: 1, fontWeight: 500 }}>{t.message}</span>
            <button
              onClick={() => onDismiss(t.id)}
              aria-label="Dismiss"
              style={{
                flex: 'none',
                border: 'none',
                background: 'transparent',
                color: tone.ink,
                opacity: 0.7,
                cursor: 'pointer',
                padding: 0,
                display: 'inline-flex',
              }}
            >
              <Icon name="x" size={15} color={tone.ink} />
            </button>
          </div>
        )
      })}
    </div>
  )
}
