// Dashboard — org KPI overview. Replaces "Get started" once onboarding is
// complete (GetStartedPage branches to this component). Backed by the
// GetDashboardMetrics RPC: 4 KPI cards (conversations, response rate, median
// first response, reuse-from-history BETA), 2 current-state cards (open, leads),
// two list columns (to-handle, recently-resolved) and two leaderboards
// (responders, topics). All layout is inline width checks — the app has no CSS
// media queries.

import { useState, useEffect, useCallback, type ReactNode } from 'react'
import { useNavigate } from 'react-router-dom'
import {
  api,
  type DashboardMetrics,
  type Expert,
  type ConversationRef,
  type HistoryRef,
} from '../lib/client'
import { useIdentity } from '../lib/identity'
import { useRealtime } from '../lib/realtime'
import { usePageHeader, btnStyle } from '../components/Layout'
import { ErrorBanner } from '../components/ErrorBanner'
import { Spinner } from '../components/Spinner'
import { Icon } from '../components/Icon'
import { initials, avatarColor } from './ConversationsPage'
import { T } from '../lib/theme'

type Period = '7d' | '30d' | 'all'

// Live width so the grids can collapse without CSS media queries.
function useWindowWidth(): number {
  const [w, setW] = useState(() => (typeof window === 'undefined' ? 1200 : window.innerWidth))
  useEffect(() => {
    const onResize = () => setW(window.innerWidth)
    window.addEventListener('resize', onResize)
    return () => window.removeEventListener('resize', onResize)
  }, [])
  return w
}

// relTime renders an ISO timestamp as a compact "6h ago" / "3 weeks ago".
function relTime(iso?: string): string {
  if (!iso) return '—'
  const mins = Math.floor((Date.now() - new Date(iso).getTime()) / 60000)
  if (mins < 1) return 'just now'
  if (mins < 60) return `${mins}m ago`
  const hours = Math.floor(mins / 60)
  if (hours < 24) return `${hours}h ago`
  const days = Math.floor(hours / 24)
  if (days === 1) return 'yesterday'
  if (days < 7) return `${days}d ago`
  const weeks = Math.floor(days / 7)
  if (weeks < 5) return `${weeks} week${weeks > 1 ? 's' : ''} ago`
  const months = Math.floor(days / 30)
  if (months < 12) return `${months} month${months > 1 ? 's' : ''} ago`
  return `${Math.floor(days / 365)}y ago`
}

// fmtDuration renders whole seconds as "1h 42m" / "12m" / "45s". '—' for 0/none.
function fmtDuration(sec: number): string {
  const s = Math.round(sec)
  if (s <= 0) return '—'
  if (s < 60) return `${s}s`
  const m = Math.round(s / 60)
  if (m < 60) return `${m}m`
  const h = Math.floor(m / 60)
  const rem = m % 60
  return rem ? `${h}h ${rem}m` : `${h}h`
}

// fmtDurationDelta renders a signed second-delta magnitude as a short duration
// (no '—' floor), for the median card's "23m faster" line.
function fmtDurationDelta(sec: number): string {
  const s = Math.round(Math.abs(sec))
  if (s < 60) return `${s}s`
  const m = Math.round(s / 60)
  if (m < 60) return `${m}m`
  const h = Math.floor(m / 60)
  const rem = m % 60
  return rem ? `${h}h ${rem}m` : `${h}h`
}

function periodLabel(p: Period): string {
  return p === '7d' ? 'prev 7d' : p === '30d' ? 'prev 30d' : ''
}

// Sparkline draws a filled area + line over a 0..1-normalized series.
function Sparkline({ data, color }: { data: number[]; color: string }) {
  const w = 240
  const h = 40
  if (!data || data.length < 2) return null
  const max = Math.max(...data, 1)
  const min = Math.min(...data, 0)
  const span = max - min || 1
  const step = w / (data.length - 1)
  const pts = data.map((v, i) => {
    const x = i * step
    const y = h - ((v - min) / span) * (h - 4) - 2
    return `${x.toFixed(1)},${y.toFixed(1)}`
  })
  const area = `0,${h} ${pts.join(' ')} ${w},${h}`
  const gid = `spark-${color.replace('#', '')}`
  return (
    <svg
      viewBox={`0 0 ${w} ${h}`}
      preserveAspectRatio="none"
      style={{ width: '100%', height: 40, display: 'block', marginTop: 10 }}
      aria-hidden="true"
    >
      <defs>
        <linearGradient id={gid} x1="0" y1="0" x2="0" y2="1">
          <stop offset="0%" stopColor={color} stopOpacity="0.18" />
          <stop offset="100%" stopColor={color} stopOpacity="0" />
        </linearGradient>
      </defs>
      <polygon points={area} fill={`url(#${gid})`} />
      <polyline points={pts.join(' ')} fill="none" stroke={color} strokeWidth={1.8} strokeLinejoin="round" strokeLinecap="round" />
    </svg>
  )
}

// DeltaLine renders the up/down arrow + colored change text under a KPI number.
function DeltaLine({ text, good }: { text: string; good: boolean }) {
  const color = good ? T.success : T.danger
  return (
    <div style={{ display: 'flex', alignItems: 'center', gap: 5, marginTop: 6, fontSize: 12.5, fontWeight: 600, color }}>
      <Icon name="arrowRight" size={13} color={color} style={{ transform: good ? 'rotate(-45deg)' : 'rotate(45deg)' }} />
      <span>{text}</span>
    </div>
  )
}

const cardStyle: React.CSSProperties = {
  background: T.surface,
  border: `1px solid ${T.border}`,
  borderRadius: T.rXl,
  padding: 18,
  boxShadow: T.shadowCard,
}

const eyebrowStyle: React.CSSProperties = {
  fontFamily: T.mono,
  fontSize: 11,
  fontWeight: 500,
  letterSpacing: '.04em',
  textTransform: 'uppercase',
  color: T.faint,
}

// KpiCard is one of the first three sparkline KPIs.
function KpiCard({
  eyebrow,
  value,
  unit,
  delta,
  series,
}: {
  eyebrow: string
  value: string
  unit?: string
  delta?: { text: string; good: boolean }
  series?: number[]
}) {
  return (
    <div style={cardStyle}>
      <div style={eyebrowStyle}>{eyebrow}</div>
      <div style={{ display: 'flex', alignItems: 'baseline', gap: 6, marginTop: 8 }}>
        <span style={{ fontSize: 30, fontWeight: 800, letterSpacing: '-.02em', color: T.text }}>{value}</span>
        {unit && <span style={{ fontSize: 14, fontWeight: 600, color: T.subtle }}>{unit}</span>}
      </div>
      {delta ? <DeltaLine text={delta.text} good={delta.good} /> : <div style={{ height: 18, marginTop: 6 }} />}
      {series && series.length >= 2 && <Sparkline data={series} color={T.accent} />}
    </div>
  )
}

// ReuseCard is the BETA "reused from history" KPI: purple gradient + BETA badge.
function ReuseBetaCard({ available, pct }: { available: boolean; pct: number }) {
  return (
    <div
      style={{
        ...cardStyle,
        border: '1px solid #DCD9FB',
        background: 'linear-gradient(135deg, #F4F3FD 0%, #EEEDFD 100%)',
      }}
    >
      <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between' }}>
        <div style={{ ...eyebrowStyle, color: T.accentInk }}>Reused from history</div>
        <span
          style={{
            fontFamily: T.mono,
            fontSize: 10,
            fontWeight: 600,
            letterSpacing: '.06em',
            color: '#fff',
            background: T.accent,
            padding: '2px 7px',
            borderRadius: 5,
          }}
        >
          BETA
        </span>
      </div>
      <div style={{ display: 'flex', alignItems: 'baseline', gap: 6, marginTop: 8 }}>
        <span style={{ fontSize: 30, fontWeight: 800, letterSpacing: '-.02em', color: available ? T.accentInk : '#B4AEE6' }}>
          {available ? `${Math.round(pct)}%` : '—'}
        </span>
        {available && <span style={{ fontSize: 14, fontWeight: 600, color: T.accent }}>deflected</span>}
      </div>
      <div style={{ fontSize: 12, color: T.accent, marginTop: 6, lineHeight: 1.4 }}>
        Needs instrumentation — coming soon.
      </div>
    </div>
  )
}

// StateCard is a clickable current-state card (Open · waiting / Leads).
function StateCard({
  tone,
  icon,
  count,
  title,
  sub,
  onClick,
}: {
  tone: 'warn' | 'danger'
  icon: 'clock' | 'alertTriangle'
  count: number
  title: string
  sub: string
  onClick: () => void
}) {
  const border = tone === 'warn' ? T.warnBorder : T.dangerBorder
  const ink = tone === 'warn' ? T.warn : T.dangerInk
  const bg = tone === 'warn' ? T.warnBg : T.dangerBg
  return (
    <div
      onClick={onClick}
      style={{
        ...cardStyle,
        border: `1px solid ${border}`,
        display: 'flex',
        alignItems: 'center',
        gap: 14,
        cursor: 'pointer',
      }}
    >
      <div
        style={{
          width: 40,
          height: 40,
          borderRadius: 10,
          background: bg,
          display: 'flex',
          alignItems: 'center',
          justifyContent: 'center',
          flex: 'none',
        }}
      >
        <Icon name={icon} size={20} color={ink} />
      </div>
      <div style={{ flex: 1, minWidth: 0 }}>
        <div style={{ display: 'flex', alignItems: 'baseline', gap: 8 }}>
          <span style={{ fontSize: 24, fontWeight: 800, color: T.text }}>{count}</span>
          <span style={{ fontSize: 14, fontWeight: 600, color: T.body }}>{title}</span>
        </div>
        <div style={{ fontSize: 12.5, color: T.subtle, marginTop: 2 }}>{sub}</div>
      </div>
      <Icon name="chevronRight" size={17} color={T.faint} />
    </div>
  )
}

// SectionCard wraps a list/leaderboard column with a header + optional action.
function SectionCard({
  title,
  right,
  children,
}: {
  title: ReactNode
  right?: ReactNode
  children: ReactNode
}) {
  return (
    <div style={{ ...cardStyle, padding: 0, overflow: 'hidden' }}>
      <div
        style={{
          display: 'flex',
          alignItems: 'center',
          justifyContent: 'space-between',
          padding: '15px 18px',
          borderBottom: `1px solid ${T.borderSubtle}`,
        }}
      >
        <div style={{ display: 'flex', alignItems: 'center', gap: 9 }}>{title}</div>
        {right}
      </div>
      <div style={{ padding: '6px 8px' }}>{children}</div>
    </div>
  )
}

const STATE_TONE: Record<string, { label: string; ink: string; bg: string; border: string; dot: string }> = {
  open: { label: 'Open', ink: '#B45309', bg: '#FBF3DC', border: '#F0E2B8', dot: '#EA9E37' },
  timed_out: { label: 'Expired', ink: '#C0392B', bg: '#FDECEC', border: '#F2CFCF', dot: '#DC4A5E' },
}

export function DashboardPage() {
  const navigate = useNavigate()
  const { projects } = useIdentity()
  const width = useWindowWidth()

  const [period, setPeriod] = useState<Period>('30d')
  const [metrics, setMetrics] = useState<DashboardMetrics | null>(null)
  const [names, setNames] = useState<Record<string, string>>({})
  const [orgName, setOrgName] = useState('')
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<unknown>(null)

  useEffect(() => {
    api.getOrganization().then(r => setOrgName(r.name)).catch(() => {})
  }, [])

  const load = useCallback(async () => {
    setError(null)
    try {
      const scope = projects.map(p => p.id)
      const [m, specResults] = await Promise.all([
        api.getDashboardMetrics(period),
        // Resolve member ids → names via listExperts (callable by any member,
        // unlike admin-only listMembers), one map across the visible projects.
        Promise.all(scope.map(id => api.listExperts(id).catch(() => ({ experts: [] as Expert[] })))),
      ])
      const map: Record<string, string> = {}
      for (const r of specResults) for (const s of r.experts ?? []) map[s.memberId] = s.displayName
      setMetrics(m)
      setNames(map)
    } catch (e) {
      setError(e)
    } finally {
      setLoading(false)
    }
  }, [period, projects])

  useEffect(() => {
    setLoading(true)
    load()
  }, [load])

  // A new/closed conversation moves the numbers; refresh in the background.
  useRealtime(['conversation_opened', 'conversation_closed', 'message_posted'], load)

  const memberName = useCallback(
    (id?: string): string => {
      if (!id) return 'Someone'
      return names[id]?.trim() || `${id.slice(0, 6)}…`
    },
    [names],
  )

  usePageHeader(
    {
      title: (
        <span style={{ display: 'inline-flex', alignItems: 'baseline', gap: 9 }}>
          <span>Dashboard</span>
          {orgName && <span style={{ fontSize: 13, fontWeight: 500, color: T.faint }}>{orgName}</span>}
        </span>
      ),
      actions: (
        <div style={{ display: 'flex', alignItems: 'center', gap: 12 }}>
          <PeriodToggle period={period} onChange={setPeriod} />
          <button onClick={() => navigate('/conversations')} style={btnStyle('primary')}>
            <Icon name="plus" size={15} color="#fff" />
            Ask a question
          </button>
        </div>
      ),
    },
    [period, orgName, navigate],
  )

  if (loading && !metrics) {
    return (
      <div style={{ display: 'flex', justifyContent: 'center', padding: '80px 0' }}>
        <Spinner size={22} />
      </div>
    )
  }

  const kpiCols = width >= 1100 ? 4 : width >= 680 ? 2 : 1
  const twoCols = width >= 900 ? 2 : 1

  const conv = metrics?.conversations ?? {}
  const rate = metrics?.responseRate ?? {}
  const median = metrics?.medianFirstResponseSeconds ?? {}
  const reuse = metrics?.reuse ?? {}
  const hasPrev = period !== 'all'
  const pl = periodLabel(period)

  return (
    <div style={{ padding: '24px 28px', maxWidth: 1160, margin: '0 auto' }}>
      {error != null && <ErrorBanner error={error} />}

      {/* KPI row */}
      <div style={{ display: 'grid', gridTemplateColumns: `repeat(${kpiCols}, 1fr)`, gap: 14 }}>
        <KpiCard
          eyebrow="Conversations"
          value={String(conv.value ?? 0)}
          unit="asked"
          delta={hasPrev ? { text: `${signed(conv.deltaPct)}% vs ${pl}`, good: (conv.deltaPct ?? 0) >= 0 } : undefined}
          series={conv.series}
        />
        <KpiCard
          eyebrow="Response rate"
          value={`${Math.round(rate.value ?? 0)}%`}
          unit="answered"
          delta={hasPrev ? { text: `${signed(rate.deltaPct)} pts`, good: (rate.deltaPct ?? 0) >= 0 } : undefined}
          series={rate.series}
        />
        <KpiCard
          eyebrow="Median 1st response"
          value={fmtDuration(median.value ?? 0)}
          delta={
            hasPrev && (median.deltaPct ?? 0) !== 0
              ? {
                  text: `${fmtDurationDelta(median.deltaPct ?? 0)} ${(median.deltaPct ?? 0) < 0 ? 'faster' : 'slower'}`,
                  good: (median.deltaPct ?? 0) < 0,
                }
              : undefined
          }
          series={median.series}
        />
        <ReuseBetaCard available={!!reuse.available} pct={reuse.pct ?? 0} />
      </div>

      {/* Current state */}
      <div style={{ display: 'grid', gridTemplateColumns: `repeat(${twoCols}, 1fr)`, gap: 14, marginTop: 14 }}>
        <StateCard
          tone="warn"
          icon="clock"
          count={metrics?.openCount ?? 0}
          title="Open · waiting"
          sub="Needs a human to answer right now"
          onClick={() => navigate('/conversations?status=open')}
        />
        <StateCard
          tone="danger"
          icon="alertTriangle"
          count={metrics?.leadsCount ?? 0}
          title="Leads without an answer"
          sub="Expired & never answered — questions slipping away"
          onClick={() => navigate('/conversations?status=timed_out')}
        />
      </div>

      {/* To handle + Recently resolved */}
      <div style={{ display: 'grid', gridTemplateColumns: `repeat(${twoCols}, 1fr)`, gap: 14, marginTop: 14 }}>
        <SectionCard
          title={
            <>
              <span style={{ fontSize: 14.5, fontWeight: 700, color: T.text }}>To handle</span>
              {(metrics?.toHandle?.length ?? 0) > 0 && (
                <span
                  style={{
                    fontSize: 11,
                    fontWeight: 700,
                    color: T.warn,
                    background: T.warnBg,
                    border: `1px solid ${T.warnBorder}`,
                    padding: '1px 8px',
                    borderRadius: 10,
                  }}
                >
                  {metrics?.toHandle?.length}
                </span>
              )}
            </>
          }
          right={
            <span onClick={() => navigate('/conversations')} style={linkStyle}>
              View all
            </span>
          }
        >
          <ToHandleList items={metrics?.toHandle ?? []} memberName={memberName} navigate={navigate} />
        </SectionCard>

        <SectionCard
          title={<span style={{ fontSize: 14.5, fontWeight: 700, color: T.text }}>Recently added to history</span>}
          right={
            <span onClick={() => navigate('/history')} style={linkStyle}>
              Open history
            </span>
          }
        >
          <ResolvedList items={metrics?.recentlyResolved ?? []} memberName={memberName} navigate={navigate} />
        </SectionCard>
      </div>

      {/* Leaderboards */}
      <div style={{ display: 'grid', gridTemplateColumns: `repeat(${twoCols}, 1fr)`, gap: 14, marginTop: 14 }}>
        <SectionCard
          title={
            <>
              <span style={{ fontSize: 14.5, fontWeight: 700, color: T.text }}>Top responders</span>
              <span style={{ fontFamily: T.mono, fontSize: 11, color: T.faint }}>answers · {period}</span>
            </>
          }
        >
          <Leaderboard
            rows={(metrics?.topResponders ?? []).map(r => ({
              key: r.memberId,
              label: r.displayName?.trim() || memberName(r.memberId),
              value: r.answerCount ?? 0,
              avatar: true,
            }))}
          />
        </SectionCard>

        <SectionCard
          title={
            <>
              <span style={{ fontSize: 14.5, fontWeight: 700, color: T.text }}>Top topics</span>
              <span style={{ fontFamily: T.mono, fontSize: 11, color: T.faint }}>most asked · {period}</span>
            </>
          }
        >
          <Leaderboard
            rows={(metrics?.topTopics ?? []).map(t => ({
              key: t.label,
              label: t.label,
              value: t.count ?? 0,
              mono: true,
            }))}
          />
        </SectionCard>
      </div>
    </div>
  )
}

const linkStyle: React.CSSProperties = {
  fontSize: 12.5,
  fontWeight: 600,
  color: T.accent,
  cursor: 'pointer',
}

function PeriodToggle({ period, onChange }: { period: Period; onChange: (p: Period) => void }) {
  const opts: { key: Period; label: string }[] = [
    { key: '7d', label: '7d' },
    { key: '30d', label: '30d' },
    { key: 'all', label: 'All' },
  ]
  return (
    <div style={{ display: 'flex', background: T.sunken, borderRadius: 9, padding: 3, gap: 2 }}>
      {opts.map(o => {
        const active = o.key === period
        return (
          <button
            key={o.key}
            onClick={() => onChange(o.key)}
            style={{
              border: 'none',
              borderRadius: 7,
              padding: '5px 12px',
              fontSize: 12.5,
              fontWeight: 600,
              fontFamily: 'inherit',
              cursor: 'pointer',
              background: active ? T.surface : 'transparent',
              color: active ? T.body : T.subtle,
              boxShadow: active ? T.shadowCard : 'none',
            }}
          >
            {o.label}
          </button>
        )
      })}
    </div>
  )
}

function emptyRow(text: string): ReactNode {
  return <div style={{ padding: '20px 14px', textAlign: 'center', fontSize: 13, color: T.faint }}>{text}</div>
}

function ToHandleList({
  items,
  memberName,
  navigate,
}: {
  items: ConversationRef[]
  memberName: (id?: string) => string
  navigate: (to: string) => void
}) {
  if (items.length === 0) return <>{emptyRow('Nothing waiting — you are all caught up.')}</>
  return (
    <>
      {items.map(c => {
        const tone = STATE_TONE[c.status] ?? STATE_TONE.open
        const meta =
          c.status === 'timed_out'
            ? `timed out ${relTime(c.createdAt)}`
            : `${memberName(c.askerMemberId)} · ${relTime(c.createdAt)}`
        return (
          <div key={c.conversationId} onClick={() => navigate(`/conversations/${c.conversationId}`)} style={rowStyle}>
            <span style={{ width: 8, height: 8, borderRadius: '50%', background: tone.dot, flex: 'none' }} />
            <div style={{ flex: 1, minWidth: 0 }}>
              <div style={ellipsis}>{c.title}</div>
              <div style={{ fontSize: 12, color: T.subtle, marginTop: 1 }}>{meta}</div>
            </div>
            <span
              style={{
                fontSize: 11,
                fontWeight: 600,
                color: tone.ink,
                background: tone.bg,
                border: `1px solid ${tone.border}`,
                padding: '2px 8px',
                borderRadius: 20,
                flex: 'none',
              }}
            >
              {tone.label}
            </span>
          </div>
        )
      })}
    </>
  )
}

function ResolvedList({
  items,
  memberName,
  navigate,
}: {
  items: HistoryRef[]
  memberName: (id?: string) => string
  navigate: (to: string) => void
}) {
  if (items.length === 0) return <>{emptyRow('No resolved conversations yet.')}</>
  return (
    <>
      {items.map(h => (
        <div key={h.conversationId} onClick={() => navigate(`/conversations/${h.conversationId}`)} style={rowStyle}>
          <div
            style={{
              width: 24,
              height: 24,
              borderRadius: 7,
              background: T.successBg,
              border: `1px solid ${T.successBorder}`,
              display: 'flex',
              alignItems: 'center',
              justifyContent: 'center',
              flex: 'none',
            }}
          >
            <Icon name="check" size={13} strokeWidth={3} color={T.successInk} />
          </div>
          <div style={{ flex: 1, minWidth: 0 }}>
            <div style={ellipsis}>{h.title}</div>
            <div style={{ display: 'flex', alignItems: 'center', gap: 6, marginTop: 3, flexWrap: 'wrap' }}>
              {(h.tags ?? []).slice(0, 3).map(t => (
                <span
                  key={t}
                  style={{
                    fontFamily: T.mono,
                    fontSize: 10.5,
                    color: T.accent,
                    background: T.accentSoft,
                    border: `1px solid ${T.accentBorder}`,
                    padding: '1px 6px',
                    borderRadius: 5,
                  }}
                >
                  {t}
                </span>
              ))}
              <span style={{ fontSize: 12, color: T.subtle }}>
                {h.resolverMemberId ? `Answered by ${memberName(h.resolverMemberId)} · ` : ''}
                {relTime(h.resolvedAt)}
              </span>
            </div>
          </div>
        </div>
      ))}
    </>
  )
}

function Leaderboard({
  rows,
}: {
  rows: { key: string; label: string; value: number; avatar?: boolean; mono?: boolean }[]
}) {
  if (rows.length === 0) return <>{emptyRow('No data for this period yet.')}</>
  const max = Math.max(...rows.map(r => r.value), 1)
  return (
    <div style={{ padding: '4px 10px' }}>
      {rows.map(r => (
        <div key={r.key} style={{ display: 'flex', alignItems: 'center', gap: 10, padding: '8px 4px' }}>
          {r.avatar && (
            <span
              style={{
                width: 26,
                height: 26,
                borderRadius: '50%',
                background: avatarColor(r.key).bg,
                color: avatarColor(r.key).fg,
                fontSize: 10,
                fontWeight: 700,
                display: 'flex',
                alignItems: 'center',
                justifyContent: 'center',
                flex: 'none',
              }}
            >
              {initials(r.label)}
            </span>
          )}
          <span
            style={{
              fontSize: r.mono ? 12.5 : 13.5,
              fontFamily: r.mono ? T.mono : 'inherit',
              fontWeight: r.mono ? 500 : 600,
              color: T.body,
              width: r.mono ? 110 : 'auto',
              flex: r.mono ? 'none' : 1,
              overflow: 'hidden',
              textOverflow: 'ellipsis',
              whiteSpace: 'nowrap',
            }}
          >
            {r.label}
          </span>
          <div style={{ flex: 1, height: 6, borderRadius: 4, background: T.sunken, overflow: 'hidden' }}>
            <div style={{ width: `${(r.value / max) * 100}%`, height: '100%', background: T.accent, borderRadius: 4 }} />
          </div>
          <span style={{ fontSize: 12.5, fontWeight: 700, color: T.text, width: 28, textAlign: 'right', flex: 'none' }}>
            {r.value}
          </span>
        </div>
      ))}
    </div>
  )
}

const rowStyle: React.CSSProperties = {
  display: 'flex',
  alignItems: 'center',
  gap: 11,
  padding: '10px 10px',
  borderRadius: 9,
  cursor: 'pointer',
}

const ellipsis: React.CSSProperties = {
  fontSize: 13.5,
  fontWeight: 600,
  color: T.body,
  overflow: 'hidden',
  textOverflow: 'ellipsis',
  whiteSpace: 'nowrap',
}

// signed renders a number with an explicit + sign, rounded to a whole number.
function signed(v?: number): string {
  const n = Math.round(v ?? 0)
  return n >= 0 ? `+${n}` : String(n)
}
