import { T } from '../lib/theme'

interface Props {
  error: unknown
}

export function ErrorBanner({ error }: Props) {
  const msg = error instanceof Error ? error.message : String(error)
  return (
    <div
      style={{
        background: T.dangerBg,
        border: `1px solid ${T.dangerBorder}`,
        borderRadius: T.rMd,
        padding: '11px 14px',
        color: T.dangerInk,
        fontSize: 13,
        lineHeight: 1.5,
        marginBottom: 16,
      }}
    >
      {msg}
    </div>
  )
}
