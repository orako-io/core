// Design tokens from the Orako dashboard mockup. Prefer these over raw hex values.

export const T = {
  // Surfaces
  bg: '#F7F8FA', // app content background
  surface: '#FFFFFF', // cards, sidebar, top bar
  surfaceAlt: '#FBFBFC', // subtle raised chips
  sunken: '#F1F2F5', // active nav item / inset

  // Borders / lines
  border: '#E9EBEE', // card border
  borderStrong: '#E2E5EA', // input border
  borderSubtle: '#EDEFF2', // dividers, chrome
  line: '#F0EFF6', // hairline inside cards

  // Text
  text: '#171B24', // headings
  body: '#1D2430', // body copy
  muted: '#6B7280', // secondary text
  subtle: '#8A94A2', // captions
  faint: '#9AA1AC', // placeholders / disabled
  label: '#A0A7B0', // section labels (uppercase)

  // Accent (indigo)
  accent: '#5850EC',
  accentHover: '#4A42D4',
  accentSoft: '#EEEDFD', // badge / avatar background
  accentSofter: '#F4F3FD', // banner background
  accentBorder: '#DCD9FB',
  accentInk: '#3A3568', // text on accent-soft

  // Status
  success: '#16A34A',
  successInk: '#15803D',
  successBg: '#EEF7F1',
  successBorder: '#CFE9DA',

  danger: '#DC2626',
  dangerInk: '#B91C1C',
  dangerBg: '#FEF2F2',
  dangerBorder: '#F3D0D0',

  warn: '#8A6D1A',
  warnBg: '#FBF3DC',
  warnBorder: '#F0E2B8',

  // Typography
  sans: "'Onest', system-ui, -apple-system, sans-serif",
  mono: "'Geist Mono', ui-monospace, 'SF Mono', monospace",

  // Radii
  rSm: 8,
  rMd: 10,
  rLg: 12,
  rXl: 16,

  // Shadows
  shadowCard: '0 2px 6px rgba(20,28,44,.04)',
  shadowPop: '0 8px 24px -14px rgba(88,80,236,.4)',
  shadowBtn: '0 6px 16px -6px rgba(88,80,236,.7)',
  shadowModal: '0 30px 70px -30px rgba(20,28,44,.32), 0 2px 8px rgba(20,28,44,.05)',
} as const

// Common label style for form fields.
export const labelStyle: React.CSSProperties = {
  display: 'block',
  fontSize: 13,
  fontWeight: 500,
  color: '#3A414D',
  marginBottom: 6,
}

// Uppercase section label used in the sidebar and section headers.
export const sectionLabelStyle: React.CSSProperties = {
  fontSize: 11,
  fontWeight: 600,
  letterSpacing: '.06em',
  color: T.label,
}

// A small monospace pill (e.g. IDs, tags, badges).
export function monoPill(): React.CSSProperties {
  return {
    fontFamily: T.mono,
    fontSize: 12,
    fontWeight: 500,
    color: T.accent,
    background: T.accentSoft,
    border: `1px solid ${T.accentBorder}`,
    padding: '3px 8px',
    borderRadius: 6,
  }
}
