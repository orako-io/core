// Conversations list (mockup 3a) — status chips + table, client-side filtering.

import { useState, useEffect, useMemo, useCallback } from 'react'
import { useNavigate, useSearchParams } from 'react-router-dom'
import { api, type ConversationSummary, type Expert } from '../lib/client'
import { cacheGet, cacheSet } from '../lib/queryCache'
import { useIdentity } from '../lib/identity'
import { useRealtime } from '../lib/realtime'
import { usePageHeader } from '../components/Layout'
import { ErrorBanner } from '../components/ErrorBanner'
import { ProviderNudge } from '../components/ProviderNudge'
import { ProjectMultiSelect } from '../components/ProjectMultiSelect'
import { Spinner } from '../components/Spinner'
import { Icon } from '../components/Icon'
import { T } from '../lib/theme'

// scopeKey turns the selected project-id set into a stable SWR cache key:
// sorted-joined ids, or "all" when the filter is empty (org-wide default).
function scopeKey(projectIds: string[]): string {
  return projectIds.length ? [...projectIds].sort().join(',') : 'all'
}

export const CONVERSATION_STATUS: Record<
  string,
  { label: string; ink: string; bg: string; border: string; dot: string }
> = {
  open: { label: 'Open', ink: '#B45309', bg: '#FDF3E4', border: '#F5DFB8', dot: '#EA9E37' },
  answered: { label: 'Answered', ink: T.successInk, bg: T.successBg, border: T.successBorder, dot: T.success },
  resolved: { label: 'Resolved', ink: '#5C6470', bg: T.sunken, border: '#E4E7EB', dot: T.faint },
  dismissed: { label: 'Dismissed', ink: '#5C6470', bg: T.sunken, border: '#E4E7EB', dot: T.faint },
  expired: { label: 'Expired', ink: '#B4293E', bg: '#FCEEF0', border: '#F3CDD4', dot: '#DC4A5E' },
}

export function normalizeStatus(status: string): string {
  if (status === 'timed_out') return 'expired'
  return CONVERSATION_STATUS[status] ? status : 'resolved'
}

export function StatusPill({ status, padding = '4px 9px' }: { status: string; padding?: string }) {
  const c = CONVERSATION_STATUS[normalizeStatus(status)]
  return (
    <span
      style={{
        display: 'inline-flex',
        alignItems: 'center',
        gap: 6,
        fontSize: 12,
        fontWeight: 600,
        color: c.ink,
        background: c.bg,
        border: `1px solid ${c.border}`,
        padding,
        borderRadius: 20,
        justifySelf: 'start',
      }}
    >
      <span style={{ width: 6, height: 6, borderRadius: '50%', background: c.dot }} />
      {c.label}
    </span>
  )
}

export function relTime(iso?: string): string {
  if (!iso) return '—'
  const mins = Math.floor((Date.now() - new Date(iso).getTime()) / 60000)
  if (mins < 1) return 'just now'
  if (mins < 60) return `${mins}m ago`
  const hours = Math.floor(mins / 60)
  if (hours < 24) return `${hours}h ago`
  const days = Math.floor(hours / 24)
  if (days === 1) return 'Yesterday'
  return `${days}d ago`
}

const AVATAR_COLORS = [
  { bg: '#F3D9A4', fg: '#7A5A16' },
  { bg: '#F7DAD9', fg: '#99342C' },
  { bg: '#D9E5F7', fg: '#2C5C99' },
  { bg: '#DBEAD9', fg: '#3A6B33' },
]

export function avatarColor(seed: string) {
  let h = 0
  for (let i = 0; i < seed.length; i++) h = (h * 31 + seed.charCodeAt(i)) >>> 0
  return AVATAR_COLORS[h % AVATAR_COLORS.length]
}

export function initials(seed: string): string {
  const parts = seed.trim().split(/\s+/).filter(Boolean)
  const chars = parts.length >= 2 ? parts[0][0] + parts[1][0] : seed.replace(/[^a-zA-Z0-9]/g, '').slice(0, 2)
  return (chars || 'M').toUpperCase()
}

export function responderName(memberId: string, map: Record<string, Expert>): string {
  const s = map[memberId]
  return s?.displayName?.trim() || (memberId ? memberId.slice(0, 8) : '—')
}

function cap(s: string): string {
  return s ? s.charAt(0).toUpperCase() + s.slice(1) : s
}

const GRID = '110px 1fr 130px 190px 96px'
const CHIPS = [
  ['all', 'All'],
  ['open', 'Open'],
  ['answered', 'Answered'],
  ['resolved', 'Resolved'],
  ['expired', 'Expired'],
] as const

export function ConversationsPage() {
  const { projects } = useIdentity()
  const navigate = useNavigate()

  const [query, setQuery] = useState('')
  // Multi-project filter: empty = "All" (org-wide), the default.
  const [projectIds, setProjectIds] = useState<string[]>([])
  const cacheKey = `conversations:${scopeKey(projectIds)}`
  type Cached = { conversations: ConversationSummary[]; specMap: Record<string, Expert> }
  // Initial status chip: honor a ?status= param (the dashboard's Open/Leads
  // cards deep-link here, e.g. ?status=open / ?status=timed_out → "expired").
  const [searchParams] = useSearchParams()
  const [filter, setFilter] = useState(() => {
    const s = searchParams.get('status')
    return s ? normalizeStatus(s) : 'all'
  })
  const [conversations, setConversations] = useState<ConversationSummary[]>([])
  const [specMap, setSpecMap] = useState<Record<string, Expert>>({})
  const [loading, setLoading] = useState(true)

  // Stale-while-revalidate: on mount / project switch, paint the last cached
  // result instantly (no loader) and let load() refresh in the background.
  useEffect(() => {
    const c = cacheGet<Cached>(cacheKey)
    if (c) {
      setConversations(c.conversations)
      setSpecMap(c.specMap)
      setLoading(false)
    } else {
      setLoading(true)
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [cacheKey])
  const [error, setError] = useState<unknown>(null)
  const [hovered, setHovered] = useState('')

  usePageHeader(
    {
      actions: (
        <div
          style={{
            display: 'flex',
            alignItems: 'center',
            gap: 10,
            height: 36,
            border: '1px solid #E6E9ED',
            borderRadius: 9,
            padding: '0 12px',
            background: T.surface,
            width: 280,
          }}
        >
          <Icon name="search" size={16} color={T.faint} />
          <input
            value={query}
            onChange={e => setQuery(e.target.value)}
            placeholder="Search conversations…"
            style={{
              flex: 1,
              border: 'none',
              outline: 'none',
              background: 'transparent',
              fontSize: 13.5,
              color: T.body,
              fontFamily: 'inherit',
            }}
          />
        </div>
      ),
    },
    [query],
  )

  const load = useCallback(async () => {
    if (projects.length === 0) return
    // No setLoading(true) here: the initial state covers the first render,
    // and realtime refetches swap data in place instead of flashing.
    setError(null)
    try {
      // Expert names/domains are still per-project (ListExperts has no
      // multi-project variant) — fan out across the filtered set (or every
      // project the caller sees, for "All") and merge by memberId.
      const specScope = projectIds.length ? projectIds : projects.map(p => p.id)
      const [convRes, specResults] = await Promise.all([
        api.listConversations(projectIds, ''),
        Promise.all(specScope.map(id => api.listExperts(id).catch(() => ({ experts: [] as Expert[] })))),
      ])
      const convs = convRes.conversations ?? []
      const map: Record<string, Expert> = {}
      for (const res of specResults) for (const s of res.experts ?? []) map[s.memberId] = s
      setConversations(convs)
      setSpecMap(map)
      cacheSet<Cached>(cacheKey, { conversations: convs, specMap: map })
    } catch (e) {
      setError(e)
    } finally {
      setLoading(false)
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [projectIds, projects])

  useEffect(() => {
    load()
  }, [load])
  useRealtime(
    ['conversation_opened', 'message_posted', 'conversation_closed'],
    load,
  )

  const counts = useMemo(() => {
    const c: Record<string, number> = { all: conversations.length, open: 0, answered: 0, resolved: 0, expired: 0 }
    for (const conv of conversations) c[normalizeStatus(conv.status)] += 1
    return c
  }, [conversations])

  const filtered = useMemo(() => {
    const q = query.trim().toLowerCase()
    return conversations.filter(c => {
      if (filter !== 'all' && normalizeStatus(c.status) !== filter) return false
      if (!q) return true
      const name = responderName(c.responderMemberId, specMap).toLowerCase()
      return c.question.toLowerCase().includes(q) || name.includes(q)
    })
  }, [conversations, filter, query, specMap])

  if (projects.length === 0) {
    return (
      <div style={{ padding: '26px 32px' }}>
        <ErrorBanner error="No project selected. Create or select a project first." />
      </div>
    )
  }

  return (
    <div style={{ padding: '26px 32px' }}>
      <ProviderNudge />
      <div style={{ display: 'flex', alignItems: 'center', gap: 8, marginBottom: 20, flexWrap: 'wrap' }}>
        {CHIPS.map(([key, label]) => {
          const active = filter === key
          return (
            <button
              key={key}
              onClick={() => setFilter(key)}
              style={{
                fontSize: 13,
                fontWeight: active ? 600 : 500,
                color: active ? '#fff' : '#5C6470',
                background: active ? T.body : T.surface,
                border: active ? '1px solid #1D2430' : '1px solid #E6E9ED',
                padding: '6px 13px',
                borderRadius: 8,
                cursor: 'pointer',
                fontFamily: 'inherit',
              }}
            >
              {label} {counts[key]}
            </button>
          )
        })}
        <div style={{ marginLeft: 'auto' }}>
          <ProjectMultiSelect value={projectIds} onChange={setProjectIds} />
        </div>
      </div>

      {error != null && <ErrorBanner error={error} />}

      {loading ? (
        <div style={{ display: 'flex', justifyContent: 'center', padding: 60 }}>
          <Spinner size={22} />
        </div>
      ) : (
        <div style={{ background: T.surface, border: `1px solid ${T.border}`, borderRadius: 14, overflow: 'hidden' }}>
          <div
            style={{
              display: 'grid',
              gridTemplateColumns: GRID,
              gap: 16,
              padding: '12px 20px',
              borderBottom: `1px solid ${T.borderSubtle}`,
              background: T.surfaceAlt,
              fontSize: 11.5,
              fontWeight: 600,
              letterSpacing: '.04em',
              color: T.faint,
              textTransform: 'uppercase',
            }}
          >
            <span>Status</span>
            <span>Question</span>
            <span>Project</span>
            <span>Teammate</span>
            <span style={{ textAlign: 'right' }}>Updated</span>
          </div>

          {filtered.length === 0 && (
            <div style={{ padding: '56px 20px', textAlign: 'center' }}>
              <div
                style={{
                  width: 40,
                  height: 40,
                  borderRadius: 10,
                  background: T.accentSoft,
                  display: 'inline-flex',
                  alignItems: 'center',
                  justifyContent: 'center',
                  marginBottom: 12,
                }}
              >
                <Icon name="message" size={20} color={T.accent} />
              </div>
              <div style={{ fontSize: 14, fontWeight: 600, color: T.body, marginBottom: 4 }}>
                {conversations.length === 0 ? 'No conversations yet' : 'No conversations match'}
              </div>
              <div style={{ fontSize: 13, color: T.subtle }}>
                {conversations.length === 0
                  ? 'Questions your agent escalates to teammates will appear here.'
                  : 'Try a different search or filter.'}
              </div>
            </div>
          )}

          {filtered.map((c, i) => {
            const spec = specMap[c.responderMemberId]
            const name = responderName(c.responderMemberId, specMap)
            const domain = spec?.domains?.[0]
            const av = avatarColor(c.responderMemberId || name)
            return (
              <div
                key={c.id}
                onClick={() => navigate(`/conversations/${c.id}`)}
                onMouseEnter={() => setHovered(c.id)}
                onMouseLeave={() => setHovered('')}
                style={{
                  display: 'grid',
                  gridTemplateColumns: GRID,
                  gap: 16,
                  padding: '16px 20px',
                  borderBottom: i === filtered.length - 1 ? 'none' : '1px solid #F1F3F5',
                  alignItems: 'center',
                  cursor: 'pointer',
                  background: hovered === c.id ? T.surfaceAlt : T.surface,
                }}
              >
                <StatusPill status={c.status} />
                <span
                  title={c.title || c.question}
                  style={{
                    fontSize: 14,
                    fontWeight: 500,
                    color: T.body,
                    overflow: 'hidden',
                    display: '-webkit-box',
                    WebkitLineClamp: 2,
                    WebkitBoxOrient: 'vertical',
                    lineHeight: 1.4,
                    overflowWrap: 'anywhere',
                  }}
                >
                  {c.title || c.question}
                </span>
                {c.projectName ? (
                  <span
                    style={{
                      display: 'inline-flex',
                      alignItems: 'center',
                      gap: 5,
                      fontSize: 12,
                      fontWeight: 500,
                      color: T.muted,
                      background: T.sunken,
                      border: `1px solid ${T.borderSubtle}`,
                      padding: '3px 9px',
                      borderRadius: 20,
                      justifySelf: 'start',
                      overflow: 'hidden',
                      textOverflow: 'ellipsis',
                      whiteSpace: 'nowrap',
                      maxWidth: '100%',
                    }}
                  >
                    <Icon name="folder" size={11} color={T.faint} />
                    {c.projectName}
                  </span>
                ) : (
                  <span style={{ fontSize: 13, color: T.faint }}>—</span>
                )}
                {c.participants && c.participants.length > 0 ? (
                  // Participant avatar stack: everyone on the thread (asker,
                  // responder, added), initials in overlapping circles.
                  <div
                    style={{ display: 'flex', alignItems: 'center', minWidth: 0 }}
                    title={c.participants.map(p => p.displayName).join(', ')}
                  >
                    {c.participants.slice(0, 4).map((p, idx) => {
                      const pav = avatarColor(p.memberId)
                      return (
                        <div
                          key={p.memberId}
                          style={{
                            width: 24,
                            height: 24,
                            borderRadius: '50%',
                            background: pav.bg,
                            display: 'flex',
                            alignItems: 'center',
                            justifyContent: 'center',
                            fontSize: 9,
                            fontWeight: 700,
                            color: pav.fg,
                            flex: 'none',
                            marginLeft: idx === 0 ? 0 : -7,
                            border: `2px solid ${hovered === c.id ? T.surfaceAlt : T.surface}`,
                          }}
                        >
                          {initials(p.displayName)}
                        </div>
                      )
                    })}
                    {c.participants.length > 4 && (
                      <span style={{ fontSize: 11.5, fontWeight: 600, color: T.muted, marginLeft: 5 }}>
                        +{c.participants.length - 4}
                      </span>
                    )}
                  </div>
                ) : c.responderMemberId ? (
                  <div style={{ display: 'flex', alignItems: 'center', gap: 8, minWidth: 0 }}>
                    <div
                      style={{
                        width: 24,
                        height: 24,
                        borderRadius: '50%',
                        background: av.bg,
                        display: 'flex',
                        alignItems: 'center',
                        justifyContent: 'center',
                        fontSize: 9,
                        fontWeight: 700,
                        color: av.fg,
                        flex: 'none',
                      }}
                    >
                      {initials(name)}
                    </div>
                    <span
                      style={{
                        fontSize: 13,
                        color: '#3A414D',
                        overflow: 'hidden',
                        textOverflow: 'ellipsis',
                        whiteSpace: 'nowrap',
                      }}
                    >
                      {name}
                      {domain ? ` · ${cap(domain)}` : ''}
                    </span>
                  </div>
                ) : normalizeStatus(c.status) === 'open' ? (
                  // Pool dispatch still unanswered: the first answerer is labeled the responder.
                  <span
                    style={{
                      display: 'inline-flex',
                      alignItems: 'center',
                      gap: 6,
                      fontSize: 12,
                      fontWeight: 600,
                      color: '#0F766E',
                      background: '#E8F6F4',
                      border: '1px solid #BEE5E0',
                      padding: '3px 9px',
                      borderRadius: 20,
                      justifySelf: 'start',
                    }}
                  >
                    <span style={{ width: 6, height: 6, borderRadius: '50%', background: '#14B8A6' }} />
                    pool
                  </span>
                ) : (
                  <span style={{ fontSize: 13, color: T.faint }}>—</span>
                )}
                <span style={{ fontSize: 13, color: T.faint, textAlign: 'right' }}>{relTime(c.createdAt)}</span>
              </div>
            )
          })}
        </div>
      )}
    </div>
  )
}
