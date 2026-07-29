// History tab — searchable knowledge base over the team's conversations.
// Backed by the SearchHistory RPC (deterministic, embedding-free): free-text
// search + status facets + KPI counts + result cards, each navigating to the
// conversation detail. An empty query browses the most recent conversations.

import { useState, useEffect, useMemo, useCallback, type ReactNode } from 'react'
import { useNavigate } from 'react-router'
import { api, type HistoryHit, type HistoryStatusCounts, type Expert } from '../lib/client'
import { useIdentity } from '../lib/identity'
import { useRealtime } from '../lib/realtime'
import { usePageHeader, btnStyle } from '../components/Layout'
import { ErrorBanner } from '../components/ErrorBanner'
import { ProjectMultiSelect } from '../components/ProjectMultiSelect'
import { Spinner } from '../components/Spinner'
import { Icon, type IconName } from '../components/Icon'
import { initials, avatarColor } from './ConversationsPage'
import { T } from '../lib/theme'

// normalizeStatus folds the wire status into the History status vocabulary:
// timed_out surfaces as "expired". Unknown values fall back to "resolved".
function normalizeStatus(status: string): HistoryStatusKey {
  if (status === 'timed_out') return 'expired'
  if (status in HISTORY_STATUS) return status as HistoryStatusKey
  return 'resolved'
}

type HistoryStatusKey = 'resolved' | 'answered' | 'open' | 'expired' | 'dismissed'

// Status color map + facet labels, straight from the design spec.
const HISTORY_STATUS: Record<
  HistoryStatusKey,
  { label: string; ink: string; bg: string; border: string; dot: string }
> = {
  resolved: { label: 'Resolved', ink: '#15803D', bg: '#EBF6EF', border: '#CDE9D8', dot: '#22A55B' },
  answered: { label: 'Answered', ink: '#5850EC', bg: '#EEEDFD', border: '#DCD9FB', dot: '#5850EC' },
  open: { label: 'Open', ink: '#B45309', bg: '#FBF3DC', border: '#F0E2B8', dot: '#EA9E37' },
  expired: { label: 'Expired', ink: '#C0392B', bg: '#FDECEC', border: '#F2CFCF', dot: '#DC4A5E' },
  dismissed: { label: 'Dismissed', ink: '#6B7280', bg: '#F1F3F5', border: '#E4E7EB', dot: '#9AA1AC' },
}

// The action button each status maps to (label + icon + primary flag). All
// actions open the conversation detail; the label/affordance is what differs.
const STATUS_ACTION: Record<HistoryStatusKey, { verb: string; icon: IconName; primary: boolean }> = {
  resolved: { verb: 'Reuse answer', icon: 'refresh', primary: true },
  answered: { verb: 'Open conversation', icon: 'arrowRight', primary: false },
  open: { verb: 'Join thread', icon: 'arrowRight', primary: false },
  expired: { verb: 'Nudge', icon: 'bell', primary: false },
  dismissed: { verb: 'Reopen', icon: 'arrowRight', primary: false },
}

// Trailing meta after "Asked by {name}", per status.
const STATUS_META: Record<HistoryStatusKey, string> = {
  resolved: 'resolved',
  answered: 'answered',
  open: 'in progress',
  expired: 'no answer',
  dismissed: 'dismissed',
}

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

function askerName(memberId: string | undefined, map: Record<string, Expert>): string {
  if (!memberId) return 'someone'
  return map[memberId]?.displayName?.trim() || memberId.slice(0, 8)
}

function firstName(name: string): string {
  return name.trim().split(/\s+/)[0] || name
}

// Live width, so the layout can respond without CSS media queries (the app has
// none — every page uses inline width checks like this).
function useWindowWidth(): number {
  const [w, setW] = useState(() => (typeof window === 'undefined' ? 1200 : window.innerWidth))
  useEffect(() => {
    const onResize = () => setW(window.innerWidth)
    window.addEventListener('resize', onResize)
    return () => window.removeEventListener('resize', onResize)
  }, [])
  return w
}

const FACETS: { key: string; label: string }[] = [
  { key: 'all', label: 'All' },
  { key: 'resolved', label: 'Resolved' },
  { key: 'answered', label: 'Answered' },
  { key: 'open', label: 'Open' },
  { key: 'expired', label: 'Expired' },
  { key: 'dismissed', label: 'Dismissed' },
]

// facetCount maps a facet key to its number in the backend counts (expired =
// timed_out bucket). Undefined counts are treated as 0.
function facetCount(key: string, counts: HistoryStatusCounts): number {
  switch (key) {
    case 'all':
      return counts.all ?? 0
    case 'resolved':
      return counts.resolved ?? 0
    case 'answered':
      return counts.answered ?? 0
    case 'open':
      return counts.open ?? 0
    case 'expired':
      return counts.timedOut ?? 0
    case 'dismissed':
      return counts.dismissed ?? 0
    default:
      return 0
  }
}

// The facet key -> the wire status the RPC filters on ("expired" -> timed_out;
// "all" -> "" = no filter).
function facetToStatus(key: string): string {
  if (key === 'all') return ''
  if (key === 'expired') return 'timed_out'
  return key
}

export function HistoryPage() {
  const { projects } = useIdentity()
  const navigate = useNavigate()
  const width = useWindowWidth()
  const narrow = width < 720

  const [query, setQuery] = useState('')
  const [debounced, setDebounced] = useState('')
  const [facet, setFacet] = useState('all')
  const [projectIds, setProjectIds] = useState<string[]>([])
  const [activeTags, setActiveTags] = useState<string[]>([])

  const [hits, setHits] = useState<HistoryHit[]>([])
  const [counts, setCounts] = useState<HistoryStatusCounts>({})
  const [total, setTotal] = useState(0)
  const [specMap, setSpecMap] = useState<Record<string, Expert>>({})
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<unknown>(null)
  const [hovered, setHovered] = useState('')

  // Debounce the search box (~250ms) so each keystroke doesn't fire an RPC.
  useEffect(() => {
    const id = window.setTimeout(() => setDebounced(query.trim()), 250)
    return () => window.clearTimeout(id)
  }, [query])

  usePageHeader(
    {
      actions: (
        <button
          onClick={() => navigate('/conversations')}
          title="Coding agents ask specialists through the MCP tools; browse and answer conversations here."
          style={btnStyle('primary')}
        >
          <Icon name="plus" size={15} color="#fff" />
          Ask a question
        </button>
      ),
    },
    [navigate],
  )

  const load = useCallback(async () => {
    if (projects.length === 0) return
    setError(null)
    try {
      const specScope = projectIds.length ? projectIds : projects.map(p => p.id)
      const [res, specResults, membersResult] = await Promise.all([
        api.searchHistory({
          query: debounced,
          projectIds,
          status: facetToStatus(facet),
          tags: activeTags,
        }),
        Promise.all(specScope.map(id => api.listExperts(id).catch(() => ({ experts: [] as Expert[] })))),
        api.listMembers().catch(() => ({ members: [] })),
      ])
      const map: Record<string, Expert> = {}
      for (const r of specResults) for (const s of r.experts ?? []) map[s.memberId] = s
      for (const member of membersResult.members ?? []) {
        map[member.memberId] = {
          memberId: member.memberId,
          displayName: member.displayName || member.email,
          domains: map[member.memberId]?.domains ?? [],
          online: map[member.memberId]?.online ?? false,
          email: member.email,
          status: member.status,
        }
      }
      setHits(res.hits ?? [])
      setCounts(res.counts ?? {})
      setTotal(res.total ?? 0)
      setSpecMap(map)
    } catch (e) {
      setError(e)
    } finally {
      setLoading(false)
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [debounced, facet, projectIds, activeTags, projects])

  useEffect(() => {
    load()
  }, [load])
  useRealtime(['conversation_opened', 'message_posted', 'conversation_closed'], load)

  // Tag vocabulary offered as filter chips: the union of tags across the current
  // result set. Readily available without a second RPC; clicking a chip adds it
  // to the RPC's `tags` (facet overlap). Entities/People/Date facets are
  // deferred (rendered disabled below).
  const tagVocab = useMemo(() => {
    const set = new Set<string>()
    for (const h of hits) for (const t of h.tags ?? []) set.add(t)
    for (const t of activeTags) set.add(t)
    return [...set].sort().slice(0, 24)
  }, [hits, activeTags])

  const leads = (counts.timedOut ?? 0) + (counts.dismissed ?? 0)

  if (projects.length === 0) {
    return (
      <div style={{ padding: '26px 32px' }}>
        <ErrorBanner error="No project selected. Create or select a project first." />
      </div>
    )
  }

  return (
    <div style={{ padding: narrow ? '22px 18px' : '26px 32px' }}>
      <div style={{ maxWidth: 960, margin: '0 auto', display: 'flex', flexDirection: 'column', gap: 18 }}>
        <p style={{ fontSize: 14.5, color: T.muted, margin: 0 }}>
          Everything your team was asked to answer — or hasn't — searchable.
        </p>

        {/* 1. Search hero */}
        <div
          style={{
            display: 'flex',
            alignItems: 'center',
            gap: 12,
            height: 56,
            padding: '0 18px',
            background: T.surface,
            border: `1px solid ${T.borderStrong}`,
            borderRadius: 14,
            boxShadow: T.shadowCard,
          }}
        >
          <Icon name="search" size={19} color={T.faint} />
          <input
            value={query}
            onChange={e => setQuery(e.target.value)}
            placeholder="Search summaries, questions, answers, tags & entities…"
            style={{
              flex: 1,
              border: 'none',
              outline: 'none',
              background: 'transparent',
              fontSize: 15,
              color: T.body,
              fontFamily: 'inherit',
            }}
          />
          <span
            style={{
              display: 'inline-flex',
              alignItems: 'center',
              gap: 6,
              fontSize: 11.5,
              fontWeight: 600,
              color: T.successInk,
              background: T.successBg,
              border: `1px solid ${T.successBorder}`,
              padding: '3px 9px',
              borderRadius: 20,
            }}
          >
            <span style={{ width: 6, height: 6, borderRadius: '50%', background: T.success }} />
            Live
          </span>
        </div>

        {/* 2. KPI pills */}
        <div style={{ display: 'flex', flexWrap: 'wrap', gap: 10 }}>
          <KpiPill dot="#22A55B" label={`${counts.resolved ?? 0} resolved`} />
          <KpiPill dot={T.accent} label={`${counts.answered ?? 0} answered`} />
          <KpiPill dot="#DC4A5E" label={`${leads} leads without an answer`} reddish />
        </div>

        {/* 3. Facet bar */}
        <div style={{ display: 'flex', flexDirection: 'column', gap: 12 }}>
          <div style={{ display: 'flex', alignItems: 'center', gap: 8, flexWrap: 'wrap' }}>
            {FACETS.map(f => {
              const active = facet === f.key
              const c = f.key === 'all' ? null : HISTORY_STATUS[f.key as HistoryStatusKey]
              return (
                <button
                  key={f.key}
                  onClick={() => setFacet(f.key)}
                  style={{
                    display: 'inline-flex',
                    alignItems: 'center',
                    gap: 7,
                    fontSize: 13,
                    fontWeight: active ? 600 : 500,
                    color: active ? '#fff' : '#5C6470',
                    background: active ? '#1D2430' : T.surface,
                    border: active ? '1px solid #1D2430' : '1px solid #E6E9ED',
                    padding: '6px 12px',
                    borderRadius: 8,
                    cursor: 'pointer',
                    fontFamily: 'inherit',
                  }}
                >
                  {c && (
                    <span
                      style={{
                        width: 7,
                        height: 7,
                        borderRadius: '50%',
                        background: active ? '#fff' : c.dot,
                      }}
                    />
                  )}
                  {f.label}
                  <span style={{ opacity: active ? 0.85 : 0.6 }}>{facetCount(f.key, counts)}</span>
                </button>
              )
            })}
            <div style={{ marginLeft: narrow ? 0 : 'auto' }}>
              <ProjectMultiSelect value={projectIds} onChange={setProjectIds} />
            </div>
          </div>

          {/* Filter dropdowns: Tags is live (from result vocabulary); the rest
              are deferred for v1 (present but disabled). */}
          <div style={{ display: 'flex', alignItems: 'center', gap: 8, flexWrap: 'wrap' }}>
            {tagVocab.length > 0 &&
              tagVocab.map(tag => {
                const on = activeTags.includes(tag)
                return (
                  <button
                    key={tag}
                    onClick={() =>
                      setActiveTags(prev => (on ? prev.filter(t => t !== tag) : [...prev, tag]))
                    }
                    style={{
                      fontFamily: T.mono,
                      fontSize: 12,
                      fontWeight: 500,
                      color: on ? '#fff' : T.accent,
                      background: on ? T.accent : T.accentSoft,
                      border: `1px solid ${on ? T.accent : T.accentBorder}`,
                      padding: '3px 9px',
                      borderRadius: 7,
                      cursor: 'pointer',
                    }}
                  >
                    #{tag}
                    {on ? ' ✕' : ''}
                  </button>
                )
              })}
            {(['Entities', 'People', 'Date'] as const).map(l => (
              <span
                key={l}
                title="Coming soon"
                style={{
                  display: 'inline-flex',
                  alignItems: 'center',
                  gap: 5,
                  fontSize: 12.5,
                  fontWeight: 500,
                  color: T.faint,
                  background: T.surfaceAlt,
                  border: `1px dashed ${T.borderStrong}`,
                  padding: '4px 10px',
                  borderRadius: 8,
                  cursor: 'not-allowed',
                }}
              >
                {l}
                <Icon name="chevronDown" size={12} color={T.faint} />
              </span>
            ))}
            {activeTags.length > 0 && (
              <button
                onClick={() => setActiveTags([])}
                style={{
                  fontSize: 12.5,
                  fontWeight: 500,
                  color: T.muted,
                  background: 'transparent',
                  border: 'none',
                  cursor: 'pointer',
                  fontFamily: 'inherit',
                  textDecoration: 'underline',
                }}
              >
                Clear
              </button>
            )}
          </div>

          <div style={{ fontSize: 12.5, color: T.subtle }}>
            Showing {hits.length} of {total} conversation{total === 1 ? '' : 's'}
            {(debounced || facet !== 'all' || activeTags.length > 0) && ' · filtered'}
          </div>
        </div>

        {error != null && <ErrorBanner error={error} />}

        {/* 4. Result cards */}
        {loading ? (
          <div style={{ display: 'flex', justifyContent: 'center', padding: 60 }}>
            <Spinner size={22} />
          </div>
        ) : hits.length === 0 ? (
          <NudgeCard onAsk={() => navigate('/conversations')} empty />
        ) : (
          <div style={{ display: 'flex', flexDirection: 'column', gap: 14 }}>
            {hits.map(h => (
              <HistoryCard
                key={h.conversationId}
                hit={h}
                specMap={specMap}
                hovered={hovered === h.conversationId}
                onHover={v => setHovered(v ? h.conversationId : '')}
                onOpen={() => navigate(`/conversations/${h.conversationId}`)}
              />
            ))}
            <NudgeCard onAsk={() => navigate('/conversations')} />
          </div>
        )}
      </div>
    </div>
  )
}

function KpiPill({ dot, label, reddish }: { dot: string; label: string; reddish?: boolean }) {
  return (
    <div
      style={{
        display: 'inline-flex',
        alignItems: 'center',
        gap: 9,
        background: T.surface,
        border: `1px solid ${reddish ? '#F2CFCF' : T.border}`,
        borderRadius: 11,
        padding: '9px 14px',
      }}
    >
      <span style={{ width: 8, height: 8, borderRadius: '50%', background: dot }} />
      <span style={{ fontSize: 13.5, fontWeight: 500, color: T.body }}>{label}</span>
    </div>
  )
}

function HistoryCard({
  hit,
  specMap,
  hovered,
  onHover,
  onOpen,
}: {
  hit: HistoryHit
  specMap: Record<string, Expert>
  hovered: boolean
  onHover: (v: boolean) => void
  onOpen: () => void
}) {
  const key = normalizeStatus(hit.status)
  const c = HISTORY_STATUS[key]
  const action = STATUS_ACTION[key]
  const asker = askerName(hit.askerMemberId, specMap)
  const av = avatarColor(hit.askerMemberId || asker)
  const summary = hit.summary?.trim()
  const age = key === 'expired' ? `timed out ${relTime(hit.createdAt)}` : relTime(hit.createdAt)

  return (
    <div
      onClick={onOpen}
      onMouseEnter={() => onHover(true)}
      onMouseLeave={() => onHover(false)}
      style={{
        background: T.surface,
        border: `1px solid ${hovered ? T.borderStrong : '#E9EBEE'}`,
        borderRadius: 16,
        padding: '20px 22px',
        cursor: 'pointer',
        boxShadow: hovered ? T.shadowCard : 'none',
        transition: 'box-shadow .12s ease, border-color .12s ease',
      }}
    >
      {/* Top row: status pill + age */}
      <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', gap: 10 }}>
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
            padding: '3px 10px',
            borderRadius: 20,
          }}
        >
          <span style={{ width: 6, height: 6, borderRadius: '50%', background: c.dot }} />
          {c.label}
        </span>
        <span style={{ fontSize: 12.5, color: T.faint }}>{age}</span>
      </div>

      {/* Title */}
      <div
        title={hit.title}
        style={{
          fontSize: 16.5,
          fontWeight: 700,
          color: T.text,
          marginTop: 12,
          letterSpacing: '-.01em',
          lineHeight: 1.4,
          display: '-webkit-box',
          WebkitLineClamp: 2,
          WebkitBoxOrient: 'vertical',
          overflow: 'hidden',
          overflowWrap: 'anywhere',
        }}
      >
        {hit.title}
      </div>

      {/* Summary: shown as a green answer box for resolved (the curated answer),
          else as a plain muted line. Single field — no separate answer fetch. */}
      {summary &&
        (key === 'resolved' ? (
          <div
            style={{
              display: 'flex',
              gap: 9,
              marginTop: 11,
              padding: '11px 13px',
              background: '#F6F9F7',
              border: '1px solid #DDEDE3',
              borderRadius: 11,
            }}
          >
            <Icon name="check" size={16} color="#15803D" style={{ flex: 'none', marginTop: 1 }} />
            <span title={summary} style={{ fontSize: 13.5, color: '#2C5942', lineHeight: 1.5, display: '-webkit-box', WebkitLineClamp: 3, WebkitBoxOrient: 'vertical', overflow: 'hidden', overflowWrap: 'anywhere' }}>{summary}</span>
          </div>
        ) : (
          <div title={summary} style={{ fontSize: 14, color: T.muted, marginTop: 8, lineHeight: 1.5, display: '-webkit-box', WebkitLineClamp: 3, WebkitBoxOrient: 'vertical', overflow: 'hidden', overflowWrap: 'anywhere' }}>{summary}</div>
        ))}

      {/* Chips: tags (#) + entities */}
      {((hit.tags?.length ?? 0) > 0 || (hit.entities?.length ?? 0) > 0) && (
        <div style={{ display: 'flex', flexWrap: 'wrap', gap: 6, marginTop: 13 }}>
          {(hit.tags ?? []).map(t => (
            <span
              key={`t-${t}`}
              title={t}
              style={{
                fontFamily: T.mono,
                fontSize: 11.5,
                color: T.accent,
                background: T.surface,
                border: `1px solid ${T.accentBorder}`,
                padding: '2px 8px',
                borderRadius: 6,
                maxWidth: 220,
                overflow: 'hidden',
                textOverflow: 'ellipsis',
                whiteSpace: 'nowrap',
              }}
            >
              #{t}
            </span>
          ))}
          {(hit.entities ?? []).map(e => (
            <span
              key={`e-${e}`}
              title={e}
              style={{
                fontFamily: T.mono,
                fontSize: 11.5,
                color: T.muted,
                background: '#F4F5F7',
                border: `1px solid ${T.borderSubtle}`,
                padding: '2px 8px',
                borderRadius: 6,
                maxWidth: 220,
                overflow: 'hidden',
                textOverflow: 'ellipsis',
                whiteSpace: 'nowrap',
              }}
            >
              {e}
            </span>
          ))}
        </div>
      )}

      {/* Footer: asker + meta, and the status action */}
      <div
        style={{
          display: 'flex',
          alignItems: 'center',
          justifyContent: 'space-between',
          gap: 10,
          marginTop: 15,
          paddingTop: 14,
          borderTop: `1px solid ${T.line}`,
        }}
      >
        <div style={{ display: 'flex', alignItems: 'center', gap: 9, minWidth: 0 }}>
          <div
            style={{
              width: 26,
              height: 26,
              borderRadius: '50%',
              background: av.bg,
              display: 'flex',
              alignItems: 'center',
              justifyContent: 'center',
              fontSize: 10,
              fontWeight: 700,
              color: av.fg,
              flex: 'none',
            }}
          >
            {initials(asker)}
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
            Asked by {asker} · {STATUS_META[key]}
            {hit.projectName ? ` · ${hit.projectName}` : ''}
          </span>
        </div>
        <button
          onClick={e => {
            e.stopPropagation()
            onOpen()
          }}
          style={{
            ...btnStyle(action.primary ? 'primary' : 'ghost'),
            flex: 'none',
            padding: action.primary ? '8px 14px' : '6px 10px',
            fontSize: 13,
          }}
        >
          <Icon name={action.icon} size={15} color={action.primary ? '#fff' : T.muted} />
          {key === 'expired' ? `Nudge ${firstName(asker)}` : action.verb}
        </button>
      </div>
    </div>
  )
}

// NudgeCard is both the zero-result empty state and the persistent footer CTA.
function NudgeCard({ onAsk, empty }: { onAsk: () => void; empty?: boolean }): ReactNode {
  return (
    <div
      style={{
        border: `1px dashed ${T.borderStrong}`,
        borderRadius: 16,
        padding: empty ? '40px 24px' : '22px 24px',
        textAlign: 'center',
        background: T.surfaceAlt,
      }}
    >
      <div style={{ fontSize: 15, fontWeight: 600, color: T.body, marginBottom: 6 }}>
        {empty ? 'Nothing in history yet' : "Can't find it in history?"}
      </div>
      <div style={{ fontSize: 13.5, color: T.subtle, maxWidth: 520, margin: '0 auto 14px', lineHeight: 1.55 }}>
        Ask a new question — it routes to the right specialist and lands back here once answered.
      </div>
      <button onClick={onAsk} style={btnStyle('primary')}>
        <Icon name="plus" size={15} color="#fff" />
        Ask a question
      </button>
    </div>
  )
}
