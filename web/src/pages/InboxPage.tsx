// Inbox — the caller's pending questions as a responder (ListInbox), plus the
// detail + reply composer per mockup 6c. /inbox lists waiting questions;
// /inbox/:id shows the thread with the answer composer. Reply = FollowUp
// (keeps the conversation open), Send answer = ResolveConversation (records the
// answer on the conversation and returns it to the agent).

import { useCallback, useEffect, useState } from 'react'
import { useNavigate, useParams } from 'react-router-dom'
import { api, type ConversationMessage, type InboxItem } from '../lib/client'
import { cacheGet, cacheSet } from '../lib/queryCache'
import { useIdentity } from '../lib/identity'
import { useRealtime } from '../lib/realtime'
import { Page, usePageHeader } from '../components/Layout'
import { Button } from '../components/Button'
import { ErrorBanner } from '../components/ErrorBanner'
import { Spinner } from '../components/Spinner'
import { Icon } from '../components/Icon'
import { T } from '../lib/theme'
import { useToast } from '../lib/toast'
import { Markdown } from '../components/Markdown'

const AMBER = { fg: '#B45309', bg: '#FDF3E4', bd: '#F5DFB8', dot: '#EA9E37' }

function pill(c: typeof AMBER, label: string) {
  return (
    <span
      style={{
        display: 'inline-flex',
        alignItems: 'center',
        gap: 6,
        fontSize: 12,
        fontWeight: 600,
        color: c.fg,
        background: c.bg,
        border: `1px solid ${c.bd}`,
        padding: '4px 10px',
        borderRadius: 20,
      }}
    >
      <span style={{ width: 6, height: 6, borderRadius: '50%', background: c.dot }} />
      {label}
    </span>
  )
}

function waitingPill() {
  return pill(AMBER, 'Waiting for you')
}

function timeAgo(iso?: string) {
  if (!iso) return ''
  const s = Math.floor((Date.now() - new Date(iso).getTime()) / 1000)
  if (s < 60) return 'just now'
  const m = Math.floor(s / 60)
  if (m < 60) return `${m}m ago`
  const h = Math.floor(m / 60)
  if (h < 24) return `${h}h ago`
  const d = Math.floor(h / 24)
  return d === 1 ? 'Yesterday' : `${d}d ago`
}

function initials(seed: string) {
  return (seed?.replace(/[^a-zA-Z0-9]/g, '').slice(0, 2) || '--').toUpperCase()
}

export function InboxPage() {
  const { id } = useParams()
  return id ? <InboxDetail id={id} /> : <InboxList />
}

// ── List ─────────────────────────────────────────────────────────────────────

function InboxList() {
  const navigate = useNavigate()
  // Stale-while-revalidate: seed from the last cached inbox (instant, no loader
  // on return) and revalidate in the background; SSE keeps it fresh in between.
  const [items, setItems] = useState<InboxItem[]>(() => cacheGet<InboxItem[]>('inbox') ?? [])
  const [loading, setLoading] = useState(cacheGet('inbox') === undefined)
  const [error, setError] = useState<unknown>(null)

  const fetchInbox = useCallback(() => {
    api
      .listInbox()
      .then(res => {
        setItems(res.items ?? [])
        cacheSet('inbox', res.items ?? [])
        setError(null)
      })
      .catch(e => setError(e))
      .finally(() => setLoading(false))
  }, [])
  useEffect(() => {
    fetchInbox()
  }, [fetchInbox])
  // Push, not poll: a question asked by an agent lands here the moment its
  // event crosses the SSE stream; an answer elsewhere makes the card vanish.
  useRealtime(['conversation_opened', 'message_posted', 'conversation_closed'], fetchInbox)

  // When nothing is waiting, surface recent conversations across the member's
  // projects (newest first) as cards, instead of a blank "all caught up".
  const { projects } = useIdentity()
  const [recent, setRecent] = useState<{ id: string; title: string; status: string; projectId: string; createdAt?: string }[]>([])
  useEffect(() => {
    if (loading || items.length > 0) return
    let cancelled = false
    Promise.all(
      projects.map(p =>
        api
          .listConversations([p.id])
          .then(r => (r.conversations ?? []).map(c => ({ id: c.id, title: c.title || c.question, status: c.status, projectId: p.id, createdAt: c.createdAt })))
          .catch(() => []),
      ),
    ).then(all => {
      if (cancelled) return
      const flat = all
        .flat()
        .sort((a, b) => (b.createdAt ?? '').localeCompare(a.createdAt ?? ''))
        .slice(0, 6)
      setRecent(flat)
    })
    return () => {
      cancelled = true
    }
  }, [loading, items.length, projects])

  const card = (it: InboxItem) => (
    <div
      key={it.conversationId}
      onClick={() => navigate(`/inbox/${it.conversationId}`)}
      style={{
        textAlign: 'left',
        width: '100%',
        background: T.surface,
        border: `1px solid ${T.border}`,
        borderRadius: 14,
        padding: '16px 20px',
        cursor: 'pointer',
        fontFamily: 'inherit',
        boxShadow: T.shadowCard,
      }}
    >
      <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', gap: 12 }}>
        {waitingPill()}
      </div>
      <div style={{ fontSize: 15, fontWeight: 600, color: T.text, lineHeight: 1.4, marginTop: 10 }}>
        {it.title || it.question}
      </div>
      <div style={{ display: 'flex', alignItems: 'center', gap: 8, marginTop: 8, fontSize: 12.5, color: T.faint }}>
        <span style={{ fontFamily: T.mono }}>project {it.projectId.slice(0, 8)}</span>
        {it.createdAt && (
          <>
            <span>·</span>
            <span>{timeAgo(it.createdAt)}</span>
          </>
        )}
      </div>
    </div>
  )

  const sectionTitle = (label: string, count: number) => (
    <div style={{ fontSize: 11.5, fontWeight: 600, letterSpacing: '.07em', color: T.label, margin: '4px 0 2px' }}>
      {label} ({count})
    </div>
  )

  return (
    <Page width={820}>
      {error != null && <ErrorBanner error={error} />}

      {loading && (
        <div style={{ color: T.muted, fontSize: 13 }}>
          <Spinner size={14} /> Loading…
        </div>
      )}

      {!loading && error == null && items.length === 0 && (
        <div>
          <div style={{ textAlign: 'center', padding: recent.length ? '8px 0 26px' : '70px 0' }}>
            <Icon name="inbox" size={30} color={T.faint} />
            <div style={{ fontSize: 15, fontWeight: 600, color: T.body, marginTop: 12, marginBottom: 4 }}>
              You're all caught up
            </div>
            <div style={{ fontSize: 13, color: T.muted }}>No questions waiting for your answer.</div>
          </div>
          {recent.length > 0 && (
            <>
              <div style={{ fontSize: 11.5, fontWeight: 600, letterSpacing: '.05em', color: T.faint, textTransform: 'uppercase', margin: '4px 0 12px' }}>
                Recent conversations
              </div>
              <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fill, minmax(280px, 1fr))', gap: 12 }}>
                {recent.map(c => (
                  <button
                    key={c.id}
                    onClick={() => navigate(`/inbox/${c.id}`)}
                    style={{
                      textAlign: 'left',
                      background: T.surface,
                      border: `1px solid ${T.border}`,
                      borderRadius: 12,
                      padding: '14px 16px',
                      cursor: 'pointer',
                      fontFamily: 'inherit',
                      display: 'flex',
                      flexDirection: 'column',
                      gap: 8,
                    }}
                  >
                    <div
                      style={{
                        fontSize: 13.5,
                        fontWeight: 600,
                        color: T.body,
                        overflow: 'hidden',
                        textOverflow: 'ellipsis',
                        display: '-webkit-box',
                        WebkitLineClamp: 2,
                        WebkitBoxOrient: 'vertical',
                      }}
                    >
                      {c.title}
                    </div>
                    <div style={{ display: 'flex', alignItems: 'center', gap: 8, fontSize: 11.5, color: T.faint, flexWrap: 'wrap' }}>
                      <span style={{ fontFamily: T.mono }}>{projects.find(p => p.id === c.projectId)?.name ?? c.projectId.slice(0, 8)}</span>
                      <span>· {c.status}</span>
                      {c.createdAt && <span>· {timeAgo(c.createdAt)}</span>}
                    </div>
                  </button>
                ))}
              </div>
            </>
          )}
        </div>
      )}

      {items.length > 0 && (
        <div style={{ display: 'flex', flexDirection: 'column', gap: 12 }}>
          {sectionTitle('WAITING FOR YOU', items.length)}
          {items.map(it => card(it))}
        </div>
      )}
    </Page>
  )
}

// ── Detail + composer ────────────────────────────────────────────────────────

type Detail = { conversationId: string; status: string; messages: ConversationMessage[]; title?: string }

const ROLE_WORD: Record<number, string> = {
  1: 'agent',
  2: 'answered',
  3: 'follow-up',
  4: 'system',
  5: 'second opinion',
}

function InboxDetail({ id }: { id: string }) {
  const navigate = useNavigate()
  const toast = useToast()

  usePageHeader(
    {
      crumb: { to: '/inbox', label: 'Inbox' },
      title: <span style={{ fontFamily: T.mono }}>#{id.slice(0, 8)}</span>,
    },
    [id],
  )

  const [detail, setDetail] = useState<Detail | null>(null)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<unknown>(null)

  const [draft, setDraft] = useState('')
  const [sending, setSending] = useState(false)
  const [actionError, setActionError] = useState<unknown>(null)

  const [item, setItem] = useState<InboxItem | null>(null)
  const [channel, setChannel] = useState('')
  const [names, setNames] = useState<Record<string, string>>({})
  const { selectedProjectId, projects } = useIdentity()

  useEffect(() => {
    if (!selectedProjectId) return
    api
      .listExperts(selectedProjectId)
      .then(res => {
        const map: Record<string, string> = {}
        for (const s of res.experts ?? []) map[s.memberId] = s.displayName
        setNames(map)
      })
      .catch(() => {})
  }, [selectedProjectId])

  const load = useCallback(async (opts?: { silent?: boolean }) => {
    if (!opts?.silent) setLoading(true)
    setError(null)
    try {
      const res = await api.getConversation(id)
      setDetail(res)
    } catch (e) {
      setError(e)
    } finally {
      setLoading(false)
    }
  }, [id])

  // Live thread: a follow-up or the closure refreshes the open detail in
  // place (no spinner flash).
  useRealtime(['message_posted', 'conversation_closed'], () => void load({ silent: true }))

  useEffect(() => {
    load()
    api
      .listInbox()
      .then(res => setItem(res.items?.find(i => i.conversationId === id) ?? null))
      .catch(() => {})
    api
      .getMember()
      .then(res => setChannel(res.member?.deliveryChannel ?? ''))
      .catch(() => {})
  }, [load, id])

  const open = detail?.status === 'open'
  const question =
    item?.question || detail?.messages.find(m => m.role === 1)?.body.split('\n')[0] || detail?.messages[0]?.body || ''
  const heading = item?.title || detail?.title || question
  const askedByAgent = detail?.messages[0]?.role === 1
  const askerId = item?.askerMemberId || detail?.messages[0]?.authorMemberId || ''
  const askerName = names[askerId] ?? (askerId ? `${askerId.slice(0, 8)}…` : '—')
  const projectName = projects.find(p => p.id === item?.projectId)?.name || item?.projectId?.slice(0, 12) || ''
  const expires = expiresIn(item?.createdAt)

  async function sendReply() {
    if (!draft.trim()) return
    setSending(true)
    setActionError(null)
    try {
      // A reply is a thread message: the conversation stays open for the
      // dialogue (the agent may follow up). Closing is a separate act.
      await api.followUp(id, draft.trim())
      toast.success('Reply sent — the thread stays open for follow-ups.')
      setDraft('')
      await load({ silent: true })
    } catch (e) {
      setActionError(e)
    } finally {
      setSending(false)
    }
  }

  async function replyAndClose() {
    if (!draft.trim()) return
    setSending(true)
    setActionError(null)
    try {
      await api.resolveConversation(id, draft.trim())
      toast.success('Answered and closed — saved to history.')
      navigate('/inbox')
    } catch (e) {
      setActionError(e)
      setSending(false)
    }
  }

  async function closeWithout(message: string) {
    setSending(true)
    setActionError(null)
    try {
      await api.dismissConversation(id)
      toast.info(message)
      navigate('/inbox')
    } catch (e) {
      setActionError(e)
      setSending(false)
    }
  }

  if (loading && !detail) {
    return (
      <Page width={820}>
        <div style={{ color: T.muted, fontSize: 13 }}>
          <Spinner size={14} /> Loading…
        </div>
      </Page>
    )
  }

  if (error != null) {
    return (
      <Page width={820}>
        <ErrorBanner error={error} />
      </Page>
    )
  }

  if (!detail) return null

  return (
    <Page width={1100}>
      <div style={{ display: 'flex', gap: 36, alignItems: 'flex-start' }}>
        <div style={{ flex: 1, minWidth: 0 }}>
      <div style={{ marginBottom: 8 }}>
        {open ? (
          waitingPill()
        ) : (
          <span
            style={{
              display: 'inline-flex',
              alignItems: 'center',
              gap: 6,
              fontSize: 12,
              fontWeight: 600,
              color: T.muted,
              background: T.sunken,
              border: `1px solid ${T.borderSubtle}`,
              padding: '4px 10px',
              borderRadius: 20,
            }}
          >
            <span style={{ width: 6, height: 6, borderRadius: '50%', background: T.faint }} />
            {detail.status}
          </span>
        )}
      </div>
      <h2 style={{ fontSize: 20, fontWeight: 700, letterSpacing: '-.02em', color: T.text, lineHeight: 1.35, margin: 0 }}>
        {heading.replace(/`/g, '')}
      </h2>

      <div style={{ display: 'flex', flexDirection: 'column', gap: 18, marginTop: 22 }}>
        {detail.messages.map(m => (
          <MessageRow key={m.messageId} m={m} names={names} />
        ))}
        {detail.messages.length === 0 && <div style={{ color: T.muted, fontSize: 13 }}>No messages yet.</div>}
      </div>

      {open ? (
        <div
          style={{
            marginTop: 26,
            background: T.surface,
            border: `1px solid ${T.borderStrong}`,
            borderRadius: 14,
            padding: 14,
            boxShadow: '0 8px 24px -16px rgba(20,28,44,.4)',
          }}
        >
          <textarea
            value={draft}
            onChange={e => setDraft(e.target.value)}
            placeholder="Write your answer…"
            style={{
              width: '100%',
              minHeight: 90,
              border: 'none',
              outline: 'none',
              resize: 'vertical',
              fontFamily: 'inherit',
              fontSize: 14,
              lineHeight: 1.55,
              color: T.body,
              background: 'transparent',
            }}
          />
          {actionError != null && (
            <div style={{ marginTop: 8 }}>
              <ErrorBanner error={actionError} />
            </div>
          )}
          <div
            style={{
              display: 'flex',
              alignItems: 'center',
              justifyContent: 'space-between',
              gap: 16,
              marginTop: 6,
              paddingTop: 12,
              borderTop: '1px solid #F1F3F5',
            }}
          >
            <div style={{ display: 'flex', alignItems: 'center', gap: 8, fontSize: 12.5, color: T.faint }}>
              <Icon name="book" size={15} strokeWidth={1.9} color={T.accent} />
              Send reply keeps the thread open · Reply & close saves it to history
            </div>
            <div style={{ display: 'flex', gap: 8, flex: 'none' }}>
              <Button
                variant="secondary"
                size="sm"
                disabled={sending}
                onClick={() => closeWithout('Closed — the agent will route it to someone else.')}
              >
                Not my area
              </Button>
              <Button variant="secondary" size="sm" loading={sending} disabled={!draft.trim()} onClick={replyAndClose}>
                Reply &amp; close
              </Button>
              <Button variant="primary" size="sm" loading={sending} disabled={!draft.trim()} onClick={sendReply}>
                <Icon name="send" size={13} color="#fff" />
                Send reply
              </Button>
            </div>
          </div>
        </div>
      ) : (
        <div style={{ marginTop: 20, fontSize: 13, color: T.faint }}>
          This conversation is {detail.status}. It can no longer be replied to.
        </div>
      )}
        </div>

        {/* Details panel (mockup 6c) */}
        <div style={{ width: 280, flex: 'none' }}>
          <div style={{ fontSize: 11.5, fontWeight: 600, letterSpacing: '.07em', color: T.label, marginBottom: 16 }}>
            DETAILS
          </div>
          <DetailRow label="Status">
            <span style={{ display: 'inline-flex', alignItems: 'center', gap: 7 }}>
              <span
                style={{
                  width: 7,
                  height: 7,
                  borderRadius: '50%',
                  background: open ? AMBER.dot : T.faint,
                }}
              />
              <span style={{ fontSize: 13.5, fontWeight: 600, color: open ? AMBER.fg : T.muted }}>
                {cap(detail.status)}
              </span>
            </span>
          </DetailRow>
          <DetailRow label="Asked by">
            <span style={{ fontSize: 13.5, color: T.body }}>
              {askerName}
              {askedByAgent ? ' (agent)' : ''}
            </span>
          </DetailRow>
          {channel && (
            <DetailRow label="Routed to you via">
              <span style={{ display: 'inline-flex', alignItems: 'center', gap: 7, fontSize: 13.5, color: T.body }}>
                <Icon
                  name={channel === 'slack' ? 'slack' : channel === 'telegram' ? 'send' : 'inbox'}
                  size={15}
                  color={T.accent}
                />
                {cap(channel)}
              </span>
            </DetailRow>
          )}
          {projectName && (
            <DetailRow label="Project">
              <span style={{ fontSize: 13.5, color: T.body }}>{projectName}</span>
            </DetailRow>
          )}
          {open && expires && (
            <DetailRow label="Expires">
              <span style={{ fontSize: 13.5, color: T.body }}>{expires}</span>
            </DetailRow>
          )}
          {open && (
            <Button
              variant="secondary"
              disabled={sending}
              onClick={() => closeWithout('Conversation closed without an answer.')}
              style={{ width: '100%', marginTop: 10 }}
            >
              Close without answering
            </Button>
          )}
        </div>
      </div>
    </Page>
  )
}

function DetailRow({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div style={{ marginBottom: 16 }}>
      <div style={{ fontSize: 12.5, color: T.faint, marginBottom: 5 }}>{label}</div>
      {children}
    </div>
  )
}

function cap(s: string): string {
  return s ? s.charAt(0).toUpperCase() + s.slice(1) : s
}

// Conversations time out ~24h after creation (the "expired" status); show the remainder.
function expiresIn(iso?: string): string {
  if (!iso) return ''
  const msLeft = new Date(iso).getTime() + 24 * 3600_000 - Date.now()
  if (msLeft <= 0) return ''
  const h = Math.round(msLeft / 3600_000)
  return h >= 1 ? `in ${h} hour${h > 1 ? 's' : ''}` : `in ${Math.max(1, Math.round(msLeft / 60_000))} min`
}

// SystemNote renders a routing/escalation note as a centered dim line, not
// a message bubble.
export function SystemNote({ body, author, at }: { body: string; author?: string; at?: string }) {
  return (
    <div style={{ textAlign: 'center', padding: '2px 0' }}>
      <span style={{ fontSize: 12.5, color: T.faint, fontStyle: 'italic' }}>
        {author ? `${author} ` : ''}
        {body}
        {at ? ` · ${timeAgo(at)}` : ''}
      </span>
    </div>
  )
}

// SecondOpinionNote renders a legacy role=5 message (a captured second
// opinion, from before the claim-model teardown; new replies are plain
// answers/follow-ups). Attributed but visually subordinate — muted text, a
// dashed border, no avatar.
function SecondOpinionNote({ body, author, at }: { body: string; author: string; at?: string }) {
  return (
    <div style={{ paddingLeft: 46 }}>
      <div style={{ display: 'flex', alignItems: 'center', gap: 8, marginBottom: 6 }}>
        <span style={{ fontSize: 12.5, color: T.faint, fontStyle: 'italic' }}>
          ◇ Second opinion — {author}
        </span>
        {at && <span style={{ fontSize: 12, color: T.faint }}>· {timeAgo(at)}</span>}
      </div>
      <div
        style={{
          border: `1px dashed ${T.border}`,
          borderRadius: 12,
          padding: '10px 14px',
          fontSize: 13.5,
          lineHeight: 1.6,
          color: T.muted,
        }}
      >
        <Markdown text={body} />
      </div>
    </div>
  )
}

function MessageRow({ m, names }: { m: ConversationMessage; names: Record<string, string> }) {
  const agent = m.role === 1
  const author =
    names[m.authorMemberId] ?? (m.authorMemberId ? `${m.authorMemberId.slice(0, 8)}…` : '—')
  const word = ROLE_WORD[m.role]
  if (m.role === 4) {
    return <SystemNote body={m.body} author={m.authorMemberId ? author : ''} at={m.at} />
  }
  if (m.role === 5) {
    return <SecondOpinionNote body={m.body} author={author} at={m.at} />
  }
  return (
    <div style={{ display: 'flex', gap: 12 }}>
      {agent ? (
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
            background: '#F3D9A4',
            display: 'flex',
            alignItems: 'center',
            justifyContent: 'center',
            color: '#7A5A16',
            fontSize: 12,
            fontWeight: 700,
            flex: 'none',
          }}
        >
          {initials(m.authorMemberId)}
        </div>
      )}
      <div style={{ flex: 1, minWidth: 0 }}>
        <div style={{ display: 'flex', alignItems: 'center', gap: 8, marginBottom: 6 }}>
          <span style={{ fontSize: 13.5, fontWeight: 600, color: T.body }}>{author}</span>
          <span style={{ fontSize: 12, color: T.faint }}>
            {word ? `${word} · ` : ''}
            {timeAgo(m.at)}
          </span>
        </div>
        <div
          style={{
            background: T.surface,
            border: `1px solid ${T.border}`,
            borderRadius: 12,
            borderTopLeftRadius: 4,
            padding: '14px 16px',
            fontSize: 14,
            lineHeight: 1.6,
            color: '#3A414D',
          }}
        >
          <Markdown text={m.body} />
        </div>
      </div>
    </div>
  )
}
