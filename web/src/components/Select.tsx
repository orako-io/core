// Select — shared dropdown primitive, height-matched to Input/Button (40px).

import { forwardRef, useId, type SelectHTMLAttributes, type ReactNode } from 'react'
import { T } from '../lib/theme'

interface SelectProps extends SelectHTMLAttributes<HTMLSelectElement> {
  label?: ReactNode
  children?: ReactNode
}

// Custom dropdown chevron so the arrow keeps a consistent gap from the right
// border (native markers ignore padding-right). Paired with appearance:none.
const CHEVRON_BG =
  "url(\"data:image/svg+xml,%3Csvg xmlns='http://www.w3.org/2000/svg' width='12' height='12' viewBox='0 0 24 24' fill='none' stroke='%236B7280' stroke-width='2.5' stroke-linecap='round' stroke-linejoin='round'%3E%3Cpolyline points='6 9 12 15 18 9'/%3E%3C/svg%3E\")"

export const Select = forwardRef<HTMLSelectElement, SelectProps>(function Select(
  { label, id, style, children, ...rest },
  ref,
) {
  const autoId = useId()
  const selectId = id ?? autoId
  return (
    <div style={{ width: '100%' }}>
      {label && (
        <label
          htmlFor={selectId}
          style={{ display: 'block', fontSize: 13, fontWeight: 500, color: '#3A414D', marginBottom: 6 }}
        >
          {label}
        </label>
      )}
      <select
        ref={ref}
        id={selectId}
        style={{
          boxSizing: 'border-box',
          width: '100%',
          height: 40,
          backgroundColor: T.surface,
          backgroundImage: CHEVRON_BG,
          backgroundRepeat: 'no-repeat',
          backgroundPosition: 'right 12px center',
          appearance: 'none',
          WebkitAppearance: 'none',
          MozAppearance: 'none',
          border: `1px solid ${T.borderStrong}`,
          borderRadius: T.rMd,
          color: T.body,
          padding: '0 34px 0 13px',
          fontSize: 14,
          fontFamily: 'inherit',
          outline: 'none',
          cursor: 'pointer',
          ...style,
        }}
        {...rest}
      >
        {children}
      </select>
    </div>
  )
})
