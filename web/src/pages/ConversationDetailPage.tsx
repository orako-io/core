// Conversation detail (mockup 3b) — thread, saved-to-history strip, follow-up
// composer and a right-hand details panel.

import { useState, useEffect, useCallback } from 'react'
import { useNavigate, useParams } from 'react-router'
import { api, type ConversationMessage, type ConversationSummary, type Participant, type Expert } from '../lib/client'
import { useIdentity } from '../lib/identity'
import { useRealtime } from '../lib/realtime'
import { usePageHeader } from '../components/Layout'
import { Button } from '../components/Button'
import { ErrorBanner } from '../components/ErrorBanner'
import { Spinner } from '../components/Spinner'
import { Icon } from '../components/Icon'
import { useToast, toastMessage } from '../lib/toast'
import { agentIdentity } from '../lib/agents'
import { T } from '../lib/theme'
import { Markdown } from '../components/Markdown'
import { DeleteConversationModal } from '../components/DeleteConversationModal'
import {
  CONVERSATION_STATUS,
  normalizeStatus,
  StatusPill,
  relTime,
  avatarColor,
  initials,
  responderName,
} from './ConversationsPage'

type Detail = {
  conversationId: string
  status: string
  messages: ConversationMessage[]
  participants?: Participant[]
}

type MessageGroup = {
  key: string
  messages: ConversationMessage[]
  system: boolean
}

function groupConsecutiveMessages(messages: ConversationMessage[]): MessageGroup[] {
  const groups: MessageGroup[] = []
  for (const message of messages) {
    if (message.role === 4) {
      groups.push({ key: message.messageId, messages: [message], system: true })
      continue
    }
    const previous = groups[groups.length - 1]
    const first = previous?.messages[0]
    const sameAuthor =
      previous &&
      !previous.system &&
      first.authorMemberId === message.authorMemberId &&
      first.source === message.source &&
      first.agentClient === message.agentClient
    if (sameAuthor) {
      previous.messages.push(message)
    } else {
      groups.push({ key: message.messageId, messages: [message], system: false })
    }
  }
  return groups
}

function cap(s: string): string {
  return s ? s.charAt(0).toUpperCase() + s.slice(1) : s
}

function fmtDate(iso?: string): string {
  if (!iso) return '—'
  const d = new Date(iso)
  const date = d.toLocaleDateString('en-US', { month: 'short', day: 'numeric', year: 'numeric' })
  const time = d.toLocaleTimeString('en-US', { hour: '2-digit', minute: '2-digit', hour12: false })
  return `${date} · ${time}`
}

const detailLabel: React.CSSProperties = { fontSize: 12.5, color: T.faint, marginBottom: 5 }

export function ConversationDetailPage() {
  const { id = '' } = useParams()
  const navigate = useNavigate()
  const toast = useToast()
  const { selectedProjectId: projectId, isOrgAdmin } = useIdentity()

  const shortId = id.slice(0, 8)
  usePageHeader(
    {
      crumb: { to: '/conversations', label: 'Conversations' },
      title: <span style={{ fontFamily: T.mono, fontSize: 14, fontWeight: 600 }}>#{shortId}</span>,
    },
    [id],
  )

  const [detail, setDetail] = useState<Detail | null>(null)
  const [summary, setSummary] = useState<ConversationSummary | null>(null)
  const [specMap, setSpecMap] = useState<Record<string, Expert>>({})
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<unknown>(null)

  const [draft, setDraft] = useState('')
  const [sending, setSending] = useState(false)
  const [confirmClose, setConfirmClose] = useState(false)
  const [closing, setClosing] = useState(false)
  const [confirmDelete, setConfirmDelete] = useState(false)
  const [deleting, setDeleting] = useState(false)
  const [promoting, setPromoting] = useState(false)
  // 1-click resolve: the responder already answered, so the last human answer IS
  // the resolution. We save it to history verbatim (Orako never rewrites it) and
  // mark the conversation resolved — no agent round-trip, no summary to type. The
  // curated summary/tags set at ask time are inherited server-side.
  const [resolving, setResolving] = useState(false)

  // Two-column on wide screens (thread + sticky details rail), stacked on
  // narrow ones (details below the thread). No media-query utility in this
  // app, so this mirrors the width-check pattern used elsewhere.
  const [narrow, setNarrow] = useState(() => typeof window !== 'undefined' && window.innerWidth < 900)
  useEffect(() => {
    const onResize = () => setNarrow(window.innerWidth < 900)
    window.addEventListener('resize', onResize)
    return () => window.removeEventListener('resize', onResize)
  }, [])

  const load = useCallback(async () => {
    setError(null)
    try {
      const [conv, specRes, listRes] = await Promise.all([
        api.getConversation(id),
        projectId ? api.listExperts(projectId) : Promise.resolve({ experts: [] as Expert[] }),
        projectId
          ? api.listConversations([projectId], '')
          : Promise.resolve({ conversations: [] as ConversationSummary[] }),
      ])
      setDetail(conv)
      const map: Record<string, Expert> = {}
      for (const s of specRes.experts ?? []) map[s.memberId] = s
      setSpecMap(map)
      setSummary((listRes.conversations ?? []).find(c => c.id === id) ?? null)
    } catch (e) {
      setError(e)
    } finally {
      setLoading(false)
    }
  }, [id, projectId])

  useEffect(() => {
    setLoading(true)
    load()
  }, [load])
  // Live thread: new messages and the closure arrive over SSE.
  useRealtime(['message_posted', 'conversation_closed'], load)

  if (loading) {
    return (
      <div style={{ display: 'flex', justifyContent: 'center', padding: 60 }}>
        <Spinner size={22} />
      </div>
    )
  }

  if (error != null || !detail) {
    return (
      <div style={{ padding: '28px 34px' }}>
        <ErrorBanner error={error ?? 'Conversation not found.'} />
      </div>
    )
  }

  const messages = detail.messages ?? []
  const messageGroups = groupConsecutiveMessages(messages)
  // The asker (whose agent posted the question). An agent-authored message is
  // shown under this person's name — "Jordan · Claude Code" — not a bare "Agent".
  const askerName = detail.participants?.find(p => p.role === 'asker')?.displayName?.trim() || ''
  const norm = normalizeStatus(detail.status)
  const statusMeta = CONVERSATION_STATUS[norm]
  const question = summary?.title || summary?.question || messages[0]?.body.split('\n')[0] || `#${shortId}`

  const responderId =
    summary?.responderMemberId || messages.find(m => specMap[m.authorMemberId])?.authorMemberId || ''
  const spec = specMap[responderId]
  const specName = responderId ? responderName(responderId, specMap) : '—'
  const specAv = avatarColor(responderId || specName)
  const createdAt = summary?.createdAt ?? messages[0]?.at

  const canFollowUp = norm === 'open' || norm === 'answered'
  const savedToKb = norm === 'answered' || norm === 'resolved'

  async function sendFollowUp() {
    if (!draft.trim()) return
    setSending(true)
    try {
      await api.followUp(id, draft.trim())
      setDraft('')
      await load()
    } catch (e) {
      toast.error(toastMessage(e))
    } finally {
      setSending(false)
    }
  }

  async function doClose() {
    setClosing(true)
    try {
      await api.dismissConversation(id)
      toast.success('Conversation dismissed')
      setConfirmClose(false)
      await load()
    } catch (e) {
      toast.error(toastMessage(e))
    } finally {
      setClosing(false)
    }
  }

  // 1-click resolve: save the exchange as-is. The last human answer becomes the
  // resolution (the backend rejects an empty one), the status flips to resolved,
  // and the conversation's curated summary/tags (set at ask time) are inherited
  // server-side — no agent round-trip, nothing to type.
  async function doResolve() {
    if (resolving) return
    const lastHuman = [...messages].reverse().find(m => m.source !== 'agent' && m.role !== 4)
    const resolution = (lastHuman?.body || question || '').trim()
    if (!resolution) {
      toast.error('Nothing to save yet — no answer on this conversation.')
      return
    }
    setResolving(true)
    try {
      await api.resolveConversation(id, resolution)
      toast.success('Resolved — saved to history')
      await load()
    } catch (e) {
      toast.error(toastMessage(e))
    } finally {
      setResolving(false)
    }
  }

  async function doDelete() {
    setDeleting(true)
    try {
      await api.deleteConversation(id)
      toast.success('Conversation deleted')
      navigate('/conversations')
    } catch (e) {
      toast.error(toastMessage(e))
      setDeleting(false)
    }
  }

  async function doPromote() {
    setPromoting(true)
    try {
      await api.promoteConversationToKnowledge(id)
      toast.success('Promoted to Knowledge — now curated & searchable')
    } catch (e) {
      toast.error(toastMessage(e))
    } finally {
      setPromoting(false)
    }
  }

  // Sticky right rail on wide screens; full-width block below the thread on
  // narrow ones. Sticks under the 60px Layout header (main is the scroll box),
  // with its own scroll when taller than the viewport.
  const detailsPanelStyle: React.CSSProperties = {
    width: narrow ? '100%' : 280,
    flex: 'none',
    borderLeft: narrow ? 'none' : `1px solid ${T.borderSubtle}`,
    borderTop: narrow ? `1px solid ${T.borderSubtle}` : 'none',
    background: T.surface,
    padding: '24px 22px',
    position: narrow ? 'static' : 'sticky',
    top: narrow ? undefined : 0,
    alignSelf: narrow ? undefined : 'flex-start',
    maxHeight: narrow ? undefined : 'calc(100vh - 60px)',
    overflowY: narrow ? undefined : 'auto',
  }

  return (
    <div style={{ display: 'flex', flexDirection: narrow ? 'column' : 'row', alignItems: 'stretch', minHeight: '100%' }}>
      <div style={{ flex: 1, minWidth: 0, padding: '28px 34px' }}>
        <div style={{ display: 'flex', alignItems: 'center', gap: 10, marginBottom: 8 }}>
          <StatusPill status={detail.status} padding="4px 10px" />
        </div>
        <h2 style={{ fontSize: 22, fontWeight: 700, letterSpacing: '-.02em', color: T.text, lineHeight: 1.35 }}>
          {question}
        </h2>

        <div style={{ display: 'flex', flexDirection: 'column', gap: 18, marginTop: 28 }}>
          {messageGroups.map(group => {
            const m = group.messages[0]
            const author = specMap[m.authorMemberId]
            // Claim/release routing notes render as centered dim lines, not bubbles.
            if (m.role === 4) {
              return (
                <div key={group.key} style={{ textAlign: 'center', padding: '2px 0' }}>
                  <span style={{ fontSize: 12.5, color: T.faint, fontStyle: 'italic' }}>
                    {m.authorMemberId ? `${responderName(m.authorMemberId, specMap)} ` : ''}
                    {m.body}
                    {m.at ? ` · ${relTime(m.at)}` : ''}
                  </span>
                </div>
              )
            }
            const isAgent = !author
            // Authoritative agent marker from the persisted message source +
            // client. An agent-authored message shows a client badge ("· Claude
            // Code"); if the client is undeclared it falls back to "(via agent)".
            const viaAgent = m.source === 'agent'
            const agent = viaAgent ? agentIdentity(m.agentClient) : null
            const baseName = isAgent
              ? askerName || 'Agent'
              : author.displayName?.trim() || responderName(m.authorMemberId, specMap)
            const name = viaAgent && !agent && !isAgent ? `${baseName} (via agent)` : baseName
            const av = avatarColor(m.authorMemberId || name)
            const meta = isAgent
              ? `${messages[0]?.messageId === m.messageId ? 'asked ' : ''}${relTime(m.at)}`
              : `${author.domains?.[0] ? `${cap(author.domains[0])} · ` : ''}replied ${relTime(m.at)}`
            return (
              <div key={group.key} style={{ display: 'flex', gap: 12 }}>
                {isAgent ? (
                  <div
                    style={{
                      width: 34,
                      height: 34,
                      borderRadius: 9,
                      background: '#211E3B',
                      display: 'flex',
                      alignItems: 'center',
                      justifyContent: 'center',
                      color: '#fff',
                      fontFamily: T.mono,
                      fontSize: 11,
                      fontWeight: 600,
                      flex: 'none',
                    }}
                  >
                    AI
                  </div>
                ) : (
                  <div
                    style={{
                      width: 34,
                      height: 34,
                      borderRadius: '50%',
                      background: av.bg,
                      display: 'flex',
                      alignItems: 'center',
                      justifyContent: 'center',
                      color: av.fg,
                      fontSize: 12,
                      fontWeight: 700,
                      flex: 'none',
                    }}
                  >
                    {initials(name)}
                  </div>
                )}
                <div style={{ flex: 1, minWidth: 0 }}>
                  <div style={{ display: 'flex', alignItems: 'center', gap: 8, marginBottom: 6, flexWrap: 'wrap' }}>
                    <span style={{ fontSize: 13.5, fontWeight: 600, color: T.body }}>{name}</span>
                    {agent && (
                      <span
                        title={`Posted via ${agent.label}`}
                        style={{
                          display: 'inline-flex',
                          alignItems: 'center',
                          gap: 5,
                          fontSize: 11.5,
                          fontWeight: 600,
                          color: agent.fg,
                          background: agent.bg,
                          borderRadius: 6,
                          padding: '1px 7px 1px 3px',
                        }}
                      >
                        <span
                          style={{
                            display: 'inline-flex',
                            alignItems: 'center',
                            justifyContent: 'center',
                            width: 15,
                            height: 15,
                            borderRadius: 4,
                            background: agent.fg,
                            color: agent.bg,
                            fontSize: 8.5,
                            fontWeight: 700,
                            letterSpacing: '-.02em',
                          }}
                        >
                          {agent.short}
                        </span>
                        {agent.label}
                      </span>
                    )}
                    <span style={{ fontSize: 12, color: T.faint }}>{meta}</span>
                  </div>
                  <div
                    style={{
                      background: isAgent ? T.surface : T.accentSofter,
                      border: `1px solid ${isAgent ? T.border : '#E3E0FA'}`,
                      borderRadius: 12,
                      borderTopLeftRadius: 4,
                      padding: '14px 16px',
                      fontSize: 14,
                      lineHeight: 1.6,
                      color: isAgent ? '#3A414D' : '#2E2A4A',
                    }}
                  >
                    {group.messages.map((message, index) => (
                      <div
                        key={message.messageId}
                        style={{
                          paddingTop: index === 0 ? 0 : 12,
                          marginTop: index === 0 ? 0 : 12,
                          borderTop: index === 0 ? 'none' : `1px solid ${isAgent ? T.borderSubtle : T.accentBorder}`,
                        }}
                      >
                        <Markdown text={message.body} />
                      </div>
                    ))}
                  </div>
                </div>
              </div>
            )
          })}
        </div>

        {savedToKb && (
          <div
            style={{
              display: 'flex',
              alignItems: 'center',
              gap: 10,
              marginTop: 26,
              padding: '14px 16px',
              background: T.surface,
              border: `1px solid ${T.border}`,
              borderRadius: 12,
            }}
          >
            <Icon name="book" size={18} color={T.accent} />
            <span style={{ fontSize: 13.5, color: '#3A414D' }}>Saved to history</span>
          </div>
        )}

        {canFollowUp && (
          <div style={{ marginTop: 26 }}>
            <textarea
              value={draft}
              onChange={e => setDraft(e.target.value)}
              onKeyDown={e => {
                if (e.key === 'Enter' && (e.ctrlKey || e.metaKey)) {
                  e.preventDefault()
                  void sendFollowUp()
                }
              }}
              placeholder="Write a follow-up…"
              rows={3}
              style={{
                width: '100%',
                background: T.surface,
                border: `1px solid ${T.borderStrong}`,
                borderRadius: T.rMd,
                color: T.body,
                padding: '12px 14px',
                fontSize: 14,
                lineHeight: 1.55,
                fontFamily: 'inherit',
                resize: 'vertical',
              }}
            />
            <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', gap: 12, marginTop: 10 }}>
              <span style={{ fontSize: 12, color: T.faint }}>Ctrl/⌘ + Enter to send</span>
              <Button variant="primary" loading={sending} disabled={!draft.trim()} onClick={sendFollowUp}>
                Send follow-up
              </Button>
            </div>
          </div>
        )}
      </div>

      <div style={detailsPanelStyle}>
        <div
          style={{
            fontSize: 11.5,
            fontWeight: 600,
            letterSpacing: '.05em',
            color: T.faint,
            textTransform: 'uppercase',
            marginBottom: 16,
          }}
        >
          Details
        </div>
        <div style={{ display: 'flex', flexDirection: 'column', gap: 16 }}>
          <div>
            <div style={detailLabel}>Status</div>
            <span
              style={{
                display: 'inline-flex',
                alignItems: 'center',
                gap: 6,
                fontSize: 12.5,
                fontWeight: 600,
                color: statusMeta.ink,
              }}
            >
              <span style={{ width: 7, height: 7, borderRadius: '50%', background: statusMeta.dot }} />
              {statusMeta.label}
            </span>
          </div>
          <div>
            <div style={detailLabel}>Participants</div>
            {detail?.participants && detail.participants.length > 0 ? (
              <div style={{ display: 'flex', flexDirection: 'column', gap: 8 }}>
                {detail.participants.map(p => {
                  const pav = avatarColor(p.memberId)
                  return (
                    <div key={p.memberId} style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
                      <div
                        style={{
                          width: 22,
                          height: 22,
                          borderRadius: '50%',
                          background: pav.bg,
                          display: 'flex',
                          alignItems: 'center',
                          justifyContent: 'center',
                          fontSize: 9,
                          fontWeight: 700,
                          color: pav.fg,
                          flex: 'none',
                        }}
                      >
                        {initials(p.displayName)}
                      </div>
                      <span style={{ fontSize: 13.5, color: T.body, fontWeight: 500, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>
                        {p.displayName}
                      </span>
                      <span style={{ fontSize: 11.5, color: T.faint, marginLeft: 'auto', flex: 'none' }}>
                        {p.role === 'asker'
                          ? 'asked'
                          : p.role === 'responder'
                            ? 'responder'
                            : p.role === 'candidate'
                              ? 'contacted'
                              : 'added'}
                      </span>
                    </div>
                  )
                })}
              </div>
            ) : responderId ? (
              <div style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
                <div
                  style={{
                    width: 22,
                    height: 22,
                    borderRadius: '50%',
                    background: specAv.bg,
                    display: 'flex',
                    alignItems: 'center',
                    justifyContent: 'center',
                    fontSize: 9,
                    fontWeight: 700,
                    color: specAv.fg,
                    flex: 'none',
                  }}
                >
                  {initials(specName)}
                </div>
                <span style={{ fontSize: 13.5, color: T.body, fontWeight: 500 }}>
                  {specName}
                  {spec?.domains?.[0] ? ` · ${cap(spec.domains[0])}` : ''}
                </span>
              </div>
            ) : (
              <span style={{ fontSize: 13.5, color: T.faint }}>—</span>
            )}
          </div>
          <div>
            <div style={detailLabel}>Created</div>
            <span style={{ fontSize: 13.5, color: T.body }}>{fmtDate(createdAt)}</span>
          </div>
        </div>

        {norm === 'answered' && !confirmClose && (
          <button
            onClick={doResolve}
            disabled={resolving}
            style={{
              width: '100%',
              height: 38,
              marginTop: 22,
              border: 'none',
              borderRadius: 9,
              background: T.accent,
              color: '#fff',
              fontSize: 13,
              fontWeight: 600,
              fontFamily: 'inherit',
              cursor: resolving ? 'default' : 'pointer',
              opacity: resolving ? 0.6 : 1,
              boxShadow: T.shadowBtn,
            }}
          >
            {resolving ? 'Resolving…' : 'Resolve & save to history'}
          </button>
        )}

        {canFollowUp &&
          (confirmClose ? (
            <div style={{ marginTop: 10 }}>
              <div style={{ fontSize: 13, color: T.body, fontWeight: 500, marginBottom: 10 }}>
                Dismiss this conversation? It is dismissed without saving an answer to history.
              </div>
              <div style={{ display: 'flex', gap: 8 }}>
                <Button size="sm" variant="secondary" disabled={closing} onClick={() => setConfirmClose(false)}>
                  Cancel
                </Button>
                <Button size="sm" variant="primary" loading={closing} onClick={doClose}>
                  Dismiss
                </Button>
              </div>
            </div>
          ) : (
            <button
              onClick={() => setConfirmClose(true)}
              style={{
                width: '100%',
                height: 38,
                marginTop: 10,
                border: `1px solid ${T.borderStrong}`,
                borderRadius: 9,
                background: T.surface,
                color: '#5C6470',
                fontSize: 13,
                fontWeight: 600,
                fontFamily: 'inherit',
                cursor: 'pointer',
              }}
            >
              Dismiss without saving
            </button>
          ))}

        {isOrgAdmin && norm === 'resolved' && (
          <button
            onClick={doPromote}
            disabled={promoting}
            style={{
              width: '100%',
              height: 38,
              marginTop: 10,
              border: `1px solid ${T.accentBorder}`,
              borderRadius: 9,
              background: T.accentSofter,
              color: T.accentInk,
              fontSize: 13,
              fontWeight: 600,
              fontFamily: 'inherit',
              cursor: promoting ? 'default' : 'pointer',
              opacity: promoting ? 0.6 : 1,
            }}
          >
            {promoting ? 'Promoting…' : 'Promote to Knowledge'}
          </button>
        )}

        {isOrgAdmin && (
          <button
            onClick={() => setConfirmDelete(true)}
            style={{
              width: '100%',
              height: 38,
              marginTop: 10,
              border: `1px solid ${T.borderStrong}`,
              borderRadius: 9,
              background: T.surface,
              color: '#DC2626',
              fontSize: 13,
              fontWeight: 600,
              fontFamily: 'inherit',
              cursor: 'pointer',
            }}
          >
            Delete conversation
          </button>
        )}
      </div>

      <DeleteConversationModal
        open={confirmDelete}
        onClose={() => setConfirmDelete(false)}
        onConfirmDelete={doDelete}
        deleting={deleting}
      />
    </div>
  )
}
