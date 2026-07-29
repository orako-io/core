// Button — shared primitive with consistent height and variants.
// Replaces ad-hoc `btnStyle` usage on refactored screens (Members/Invite).
// Heights are fixed (md=40, sm=32) so buttons line up with Input/Select.

import { forwardRef, type ButtonHTMLAttributes, type ReactNode } from 'react'
import { T } from '../lib/theme'
import { Spinner } from './Spinner'

export type ButtonVariant = 'primary' | 'secondary' | 'danger' | 'ghost'
export type ButtonSize = 'sm' | 'md'

interface ButtonProps extends ButtonHTMLAttributes<HTMLButtonElement> {
  variant?: ButtonVariant
  size?: ButtonSize
  loading?: boolean
  children?: ReactNode
}

const HEIGHT: Record<ButtonSize, number> = { sm: 32, md: 40 }

function variantStyle(variant: ButtonVariant): React.CSSProperties {
  switch (variant) {
    case 'primary':
      return { background: T.accent, color: '#fff', border: '1px solid transparent', boxShadow: T.shadowBtn }
    case 'danger':
      return { background: T.surface, color: T.dangerInk, border: `1px solid ${T.dangerBorder}` }
    case 'ghost':
      return { background: 'transparent', color: T.muted, border: '1px solid transparent' }
    case 'secondary':
    default:
      return { background: T.surface, color: T.body, border: `1px solid ${T.borderStrong}` }
  }
}

export const Button = forwardRef<HTMLButtonElement, ButtonProps>(function Button(
  { variant = 'secondary', size = 'md', loading = false, disabled, children, style, ...rest },
  ref,
) {
  const height = HEIGHT[size]
  const isDisabled = disabled || loading
  const spinnerColor = variant === 'primary' ? '#fff' : T.muted
  return (
    <button
      ref={ref}
      disabled={isDisabled}
      style={{
        display: 'inline-flex',
        alignItems: 'center',
        justifyContent: 'center',
        gap: 7,
        height,
        padding: size === 'sm' ? '0 12px' : '0 16px',
        borderRadius: T.rMd,
        fontSize: size === 'sm' ? 13 : 13.5,
        fontWeight: 600,
        fontFamily: 'inherit',
        lineHeight: 1,
        cursor: isDisabled ? 'not-allowed' : 'pointer',
        opacity: isDisabled ? 0.6 : 1,
        whiteSpace: 'nowrap',
        transition: 'background .12s, opacity .12s',
        ...variantStyle(variant),
        ...style,
      }}
      {...rest}
    >
      {loading ? <Spinner size={size === 'sm' ? 13 : 15} color={spinnerColor} /> : children}
    </button>
  )
})
