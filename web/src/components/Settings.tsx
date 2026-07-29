// Shared settings-page primitives, matching the "Relay Dashboard" UX mockups
// (5a Organization, 4b Integrations, 5b Billing, 5d License): every settings
// card is a white panel with an icon-tile header (36×36 accent tile + title +
// subtitle), optional divider, and content. Escalation timeouts use a Stepper;
// boolean settings use a ToggleSwitch.

import type { ReactNode } from 'react'
import { Icon, type IconName } from './Icon'
import { T } from '../lib/theme'

type Tone = 'accent' | 'danger'

// IconTile — the 36×36 rounded glyph tile in each settings-card header.
export function IconTile({ name, tone = 'accent', size = 36 }: { name: IconName; tone?: Tone; size?: number }) {
  const bg = tone === 'danger' ? T.dangerBg : T.accentSoft
  const fg = tone === 'danger' ? T.dangerInk : T.accent
  return (
    <div
      style={{
        width: size,
        height: size,
        borderRadius: 10,
        background: bg,
        color: fg,
        display: 'flex',
        alignItems: 'center',
        justifyContent: 'center',
        flex: 'none',
      }}
    >
      <Icon name={name} size={Math.round(size / 2)} color={fg} />
    </div>
  )
}

// SettingsCard — white panel with the mockup's border/radius/shadow. tone
// 'danger' tints it for the delete-organization zone.
export function SettingsCard({
  children,
  tone = 'accent',
  style,
}: {
  children: ReactNode
  tone?: Tone
  style?: React.CSSProperties
}) {
  return (
    <div
      style={{
        background: tone === 'danger' ? T.dangerBg : T.surface,
        border: `1px solid ${tone === 'danger' ? T.dangerBorder : T.border}`,
        borderRadius: 16,
        padding: 26,
        boxShadow: tone === 'danger' ? 'none' : '0 1px 2px rgba(20,28,44,.04)',
        ...style,
      }}
    >
      {children}
    </div>
  )
}

// CardHeader — icon tile + title/subtitle, with an optional right-aligned slot
// (used by Default project's inline dropdown and the danger-zone button).
export function CardHeader({
  icon,
  title,
  sub,
  tone = 'accent',
  right,
}: {
  icon: IconName
  title: string
  sub?: string
  tone?: Tone
  right?: ReactNode
}) {
  return (
    <div style={{ display: 'flex', alignItems: right ? 'center' : 'flex-start', justifyContent: 'space-between', gap: 20 }}>
      <div style={{ display: 'flex', alignItems: 'center', gap: 13 }}>
        <IconTile name={icon} tone={tone} />
        <div style={{ maxWidth: 640 }}>
          <h3 style={{ fontSize: 16, fontWeight: 700, color: tone === 'danger' ? T.dangerInk : T.text, letterSpacing: '-.01em', margin: 0 }}>
            {title}
          </h3>
          {sub && <p style={{ fontSize: 13, color: T.subtle, margin: '2px 0 0', lineHeight: 1.5 }}>{sub}</p>}
        </div>
      </div>
      {right && <div style={{ flex: 'none' }}>{right}</div>}
    </div>
  )
}

// Divider — the hairline between a card header and its body.
export function Divider({ margin = '20px 0' }: { margin?: string }) {
  return <div style={{ height: 1, background: T.line, margin }} />
}

// Stepper — the −/value/+ control for escalation timeouts. The middle stays an
// editable input (type via keyboard or nudge with the buttons) so an arbitrary
// value is still reachable, matching the mockup's stepper look.
export function Stepper({
  value,
  onChange,
  unit,
  step = 5,
  min = 0,
  disabled,
}: {
  value: string
  onChange: (v: string) => void
  unit: string
  step?: number
  min?: number
  disabled?: boolean
}) {
  const nudge = (delta: number) => {
    const n = Number(value) || 0
    onChange(String(Math.max(min, n + delta)))
  }
  const btn: React.CSSProperties = {
    width: 32,
    height: 38,
    border: `1px solid ${T.borderStrong}`,
    borderRadius: 9,
    background: T.surface,
    color: T.muted,
    fontSize: 17,
    fontFamily: 'inherit',
    cursor: disabled ? 'default' : 'pointer',
    display: 'flex',
    alignItems: 'center',
    justifyContent: 'center',
    flex: 'none',
  }
  return (
    <div style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
      {/* Native number spinners overlap the right edge of the field and push the
          centered value off-center; hide them (inline styles can't target the
          webkit pseudo-elements). */}
      <style>{'.orako-stepper-input::-webkit-inner-spin-button,.orako-stepper-input::-webkit-outer-spin-button{-webkit-appearance:none;margin:0}'}</style>
      <button style={btn} disabled={disabled} onClick={() => nudge(-step)} aria-label="decrease">−</button>
      <div
        style={{
          flex: 1,
          height: 38,
          border: `1px solid ${T.borderStrong}`,
          borderRadius: 9,
          display: 'flex',
          alignItems: 'baseline',
          justifyContent: 'center',
          gap: 4,
          background: T.surface,
        }}
      >
        <input
          type="number"
          className="orako-stepper-input"
          min={min}
          value={value}
          disabled={disabled}
          onChange={e => onChange(e.target.value)}
          style={{
            width: 48,
            border: 'none',
            outline: 'none',
            background: 'transparent',
            textAlign: 'center',
            fontSize: 18,
            fontWeight: 700,
            fontFamily: 'inherit',
            color: T.text,
            padding: 0,
            MozAppearance: 'textfield',
          }}
        />
        <span style={{ fontSize: 11, color: T.faint }}>{unit}</span>
      </div>
      <button style={btn} disabled={disabled} onClick={() => nudge(step)} aria-label="increase">+</button>
    </div>
  )
}

// ToggleSwitch — the 44×25 pill switch for boolean settings.
export function ToggleSwitch({ on, onChange, disabled }: { on: boolean; onChange: (v: boolean) => void; disabled?: boolean }) {
  return (
    <button
      type="button"
      role="switch"
      aria-checked={on}
      disabled={disabled}
      onClick={() => onChange(!on)}
      style={{
        width: 44,
        height: 25,
        borderRadius: 20,
        border: 'none',
        padding: 0,
        background: on ? T.accent : '#CDD2DA',
        position: 'relative',
        cursor: disabled ? 'default' : 'pointer',
        transition: 'background .15s',
        flex: 'none',
      }}
    >
      <span
        style={{
          position: 'absolute',
          top: 3,
          left: on ? 22 : 3,
          width: 19,
          height: 19,
          borderRadius: '50%',
          background: '#fff',
          boxShadow: '0 1px 3px rgba(0,0,0,.25)',
          transition: 'left .15s',
        }}
      />
    </button>
  )
}
