export function Spinner({ size = 16, color = '#5850EC' }: { size?: number; color?: string } = {}) {
  return (
    <span
      style={{
        display: 'inline-block',
        width: size,
        height: size,
        border: '2px solid rgba(88,80,236,.25)',
        borderTopColor: color,
        borderRadius: '50%',
        animation: 'spin 0.7s linear infinite',
        verticalAlign: 'middle',
      }}
    />
  )
}

// Inject keyframe once
if (typeof document !== 'undefined') {
  const id = '__orako_spin'
  if (!document.getElementById(id)) {
    const s = document.createElement('style')
    s.id = id
    s.textContent = '@keyframes spin { to { transform: rotate(360deg); } }'
    document.head.appendChild(s)
  }
}
