// Input — shared text-field primitive with an optional label and error slot.
// Fixed 40px height so it lines up with Button (md) and Select.

import { forwardRef, useId, type InputHTMLAttributes, type ReactNode } from 'react'
import { T } from '../lib/theme'

interface InputProps extends InputHTMLAttributes<HTMLInputElement> {
  label?: ReactNode
  error?: ReactNode
  hint?: ReactNode
}

export const Input = forwardRef<HTMLInputElement, InputProps>(function Input(
  { label, error, hint, id, style, ...rest },
  ref,
) {
  const autoId = useId()
  const inputId = id ?? autoId
  return (
    <div style={{ width: '100%' }}>
      {label && (
        <label
          htmlFor={inputId}
          style={{ display: 'block', fontSize: 13, fontWeight: 500, color: '#3A414D', marginBottom: 6 }}
        >
          {label}
        </label>
      )}
      <input
        ref={ref}
        id={inputId}
        style={{
          boxSizing: 'border-box',
          width: '100%',
          height: 40,
          background: T.surface,
          border: `1px solid ${error ? T.dangerBorder : T.borderStrong}`,
          borderRadius: T.rMd,
          color: T.body,
          padding: '0 13px',
          fontSize: 14,
          fontFamily: 'inherit',
          outline: 'none',
          ...style,
        }}
        {...rest}
      />
      {error ? (
        <div style={{ marginTop: 6, fontSize: 12.5, color: T.dangerInk }}>{error}</div>
      ) : hint ? (
        <div style={{ marginTop: 6, fontSize: 12.5, color: T.subtle }}>{hint}</div>
      ) : null}
    </div>
  )
})
