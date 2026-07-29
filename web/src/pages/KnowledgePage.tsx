// Knowledge section — curated knowledge entries (FAQ, config fixes, runbooks).
// These are searchable by agents alongside conversation history (search_knowledge
// returns both), but attributed to the org ("Curated by …") rather than a person
// and excluded from responder analytics. Any member may read; org admins may
// author/edit/mark-stale (the mutating RPCs are org-admin gated server-side).

import { useCallback, useEffect, useMemo, useState } from 'react'
import { api, type KnowledgeEntry } from '../lib/client'
import { useIdentity } from '../lib/identity'
import { useToast, toastMessage } from '../lib/toast'
import { Page, usePageHeader } from '../components/Layout'
import { Button } from '../components/Button'
import { Modal } from '../components/Modal'
import { Input } from '../components/Input'
import { ErrorBanner } from '../components/ErrorBanner'
import { Spinner } from '../components/Spinner'
import { Icon } from '../components/Icon'
import { T } from '../lib/theme'

// splitList turns a comma/newline-separated field into a trimmed, de-blanked
// list; joinList renders a list back into the comma form the inputs use.
function splitList(raw: string): string[] {
  return raw
    .split(/[,\n]/)
    .map(s => s.trim())
    .filter(Boolean)
}

function joinList(list?: string[]): string {
  return (list ?? []).join(', ')
}

// fmtReviewed renders an entry's updated_at as a short "last reviewed" date.
function fmtReviewed(iso?: string): string {
  if (!iso) return '—'
  return new Date(iso).toLocaleDateString('en-US', { month: 'short', day: 'numeric', year: 'numeric' })
}

type FormState = {
  entryId: string // '' = create
  question: string
  answer: string
  summary: string
  tags: string
  entities: string
  projectId: string // '' = org-wide
  status: string
}

const EMPTY_FORM: FormState = {
  entryId: '',
  question: '',
  answer: '',
  summary: '',
  tags: '',
  entities: '',
  projectId: '',
  status: 'active',
}

const STATUS_STYLE: Record<string, { label: string; bg: string; ink: string; border: string }> = {
  active: { label: 'Active', bg: T.successBg, ink: T.successInk, border: T.successBorder },
  stale: { label: 'Stale', bg: T.warnBg, ink: T.warn, border: T.warnBorder },
  pending: { label: 'Pending', bg: T.accentSofter, ink: T.accentInk, border: T.accentBorder },
}

export function KnowledgePage() {
  const { isOrgAdmin, projects } = useIdentity()
  const toast = useToast()

  const [entries, setEntries] = useState<KnowledgeEntry[]>([])
  // Pending agent-suggested entries awaiting review (org-admin only).
  const [pending, setPending] = useState<KnowledgeEntry[]>([])
  // authorMemberId → display name, for the "Suggested by …" label (admin only).
  const [memberNames, setMemberNames] = useState<Record<string, string>>({})
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const [filter, setFilter] = useState('')

  const [form, setForm] = useState<FormState | null>(null)
  const [saving, setSaving] = useState(false)

  const load = useCallback(async () => {
    setLoading(true)
    setError(null)
    try {
      const res = await api.listKnowledgeEntries()
      setEntries(res.entries ?? [])
      if (isOrgAdmin) {
        // The review queue + author names are admin-only surfaces.
        const [pendRes, membersRes] = await Promise.all([
          api.listPendingKnowledge().catch(() => ({ entries: [] as KnowledgeEntry[] })),
          api.listMembers().catch(() => ({ members: [] })),
        ])
        setPending(pendRes.entries ?? [])
        const names: Record<string, string> = {}
        for (const m of membersRes.members ?? []) names[m.memberId] = m.displayName
        setMemberNames(names)
      } else {
        setPending([])
      }
    } catch (err) {
      setError(toastMessage(err))
    } finally {
      setLoading(false)
    }
  }, [isOrgAdmin])

  useEffect(() => {
    void load()
  }, [load])

  const openCreate = useCallback(() => setForm({ ...EMPTY_FORM }), [])

  const openEdit = useCallback((e: KnowledgeEntry) => {
    setForm({
      entryId: e.id,
      question: e.question,
      answer: e.answer,
      summary: e.summary ?? '',
      tags: joinList(e.tags),
      entities: joinList(e.entities),
      projectId: e.projectId ?? '',
      status: e.status ?? 'active',
    })
  }, [])

  const save = useCallback(async () => {
    if (!form) return
    if (!form.question.trim() || !form.answer.trim()) {
      toast.error('Question and answer are required.')
      return
    }
    setSaving(true)
    try {
      if (form.entryId) {
        await api.updateKnowledgeEntry({
          entryId: form.entryId,
          question: form.question,
          answer: form.answer,
          summary: form.summary,
          tags: splitList(form.tags),
          entities: splitList(form.entities),
          status: form.status,
        })
        toast.success('Entry updated.')
      } else {
        await api.createKnowledgeEntry({
          question: form.question,
          answer: form.answer,
          summary: form.summary,
          tags: splitList(form.tags),
          entities: splitList(form.entities),
          projectId: form.projectId,
        })
        toast.success('Entry added.')
      }
      setForm(null)
      await load()
    } catch (err) {
      toast.error(toastMessage(err))
    } finally {
      setSaving(false)
    }
  }, [form, load, toast])

  const markStale = useCallback(
    async (e: KnowledgeEntry) => {
      try {
        await api.markKnowledgeStale(e.id)
        toast.success('Entry marked stale.')
        await load()
      } catch (err) {
        toast.error(toastMessage(err))
      }
    },
    [load, toast],
  )

  const revalidate = useCallback(
    async (e: KnowledgeEntry) => {
      try {
        await api.revalidateKnowledgeEntry(e.id)
        toast.success('Entry revalidated — active again.')
        await load()
      } catch (err) {
        toast.error(toastMessage(err))
      }
    },
    [load, toast],
  )

  const approve = useCallback(
    async (e: KnowledgeEntry) => {
      try {
        await api.approveKnowledgeEntry(e.id)
        toast.success('Suggestion approved — now searchable.')
        await load()
      } catch (err) {
        toast.error(toastMessage(err))
      }
    },
    [load, toast],
  )

  const dismiss = useCallback(
    async (e: KnowledgeEntry) => {
      try {
        await api.dismissKnowledgeEntry(e.id)
        toast.success('Suggestion dismissed.')
        await load()
      } catch (err) {
        toast.error(toastMessage(err))
      }
    },
    [load, toast],
  )

  usePageHeader(
    {
      title: 'Knowledge',
      actions: isOrgAdmin ? (
        <Button variant="primary" size="sm" onClick={openCreate}>
          <Icon name="plus" size={15} /> Add entry
        </Button>
      ) : undefined,
    },
    [isOrgAdmin, openCreate],
  )

  const visible = useMemo(() => {
    const q = filter.trim().toLowerCase()
    if (!q) return entries
    return entries.filter(e =>
      [e.question, e.answer, e.summary ?? '', joinList(e.tags), joinList(e.entities)]
        .join(' ')
        .toLowerCase()
        .includes(q),
    )
  }, [entries, filter])

  return (
    <Page>
      <p style={{ color: T.muted, fontSize: 14, margin: '0 0 16px' }}>
        Curated answers your agents find via <code>search_knowledge</code>, right alongside
        real conversation history — but attributed to the team and kept out of responder
        metrics. Seed FAQ, config fixes, and runbooks so agents get answers before anyone is
        pinged.
      </p>

      {error && <ErrorBanner error={error} />}

      {isOrgAdmin && pending.length > 0 && (
        <div style={{ marginBottom: 24 }}>
          <div style={{ display: 'flex', alignItems: 'center', gap: 8, marginBottom: 10 }}>
            <span style={{ fontSize: 14, fontWeight: 600, color: T.text }}>Pending review</span>
            <span
              style={{
                fontSize: 12,
                fontWeight: 600,
                padding: '1px 8px',
                borderRadius: 999,
                background: T.accentSofter,
                color: T.accentInk,
                border: `1px solid ${T.accentBorder}`,
              }}
            >
              {pending.length}
            </span>
          </div>
          <p style={{ color: T.muted, fontSize: 13, margin: '0 0 12px' }}>
            Knowledge your teammates' agents proposed from their work. It is NOT searchable until
            you approve it.
          </p>
          <div style={{ display: 'flex', flexDirection: 'column', gap: 10 }}>
            {pending.map(e => (
              <PendingCard
                key={e.id}
                entry={e}
                suggestedBy={memberNames[e.authorMemberId ?? ''] || 'a teammate'}
                onApprove={() => approve(e)}
                onDismiss={() => dismiss(e)}
              />
            ))}
          </div>
        </div>
      )}

      <div style={{ marginBottom: 16, maxWidth: 360 }}>
        <Input
          placeholder="Filter entries…"
          value={filter}
          onChange={e => setFilter(e.target.value)}
        />
      </div>

      {loading ? (
        <div style={{ display: 'flex', justifyContent: 'center', padding: 48 }}>
          <Spinner />
        </div>
      ) : visible.length === 0 ? (
        <div
          style={{
            border: `1px solid ${T.border}`,
            borderRadius: T.rLg,
            background: T.surface,
            padding: 40,
            textAlign: 'center',
            color: T.muted,
            fontSize: 14,
          }}
        >
          {entries.length === 0
            ? isOrgAdmin
              ? 'No curated knowledge yet. Add your first entry to seed the knowledge base.'
              : 'No curated knowledge yet.'
            : 'No entries match your filter.'}
        </div>
      ) : (
        <div style={{ display: 'flex', flexDirection: 'column', gap: 10 }}>
          {visible.map(e => (
            <EntryCard
              key={e.id}
              entry={e}
              isOrgAdmin={isOrgAdmin}
              onEdit={() => openEdit(e)}
              onMarkStale={() => markStale(e)}
              onRevalidate={() => revalidate(e)}
            />
          ))}
        </div>
      )}

      <Modal
        open={form !== null}
        onClose={() => setForm(null)}
        title={form?.entryId ? 'Edit entry' : 'Add curated entry'}
        subtitle="Searchable by agents alongside conversation history."
        width={560}
        footer={
          <div style={{ display: 'flex', justifyContent: 'flex-end', gap: 8 }}>
            <Button variant="secondary" onClick={() => setForm(null)}>
              Cancel
            </Button>
            <Button variant="primary" loading={saving} onClick={save}>
              {form?.entryId ? 'Save' : 'Add entry'}
            </Button>
          </div>
        }
      >
        {form && (
          <div style={{ display: 'flex', flexDirection: 'column', gap: 14 }}>
            <Input
              label="Question"
              placeholder="How do I configure Discord?"
              value={form.question}
              onChange={e => setForm({ ...form, question: e.target.value })}
            />
            <Field label="Answer">
              <textarea
                value={form.answer}
                onChange={e => setForm({ ...form, answer: e.target.value })}
                placeholder="The curated answer (Markdown supported)…"
                rows={6}
                style={textareaStyle}
              />
            </Field>
            <Input
              label="Summary (optional)"
              placeholder="One-line gist"
              value={form.summary}
              onChange={e => setForm({ ...form, summary: e.target.value })}
            />
            <Input
              label="Tags (comma-separated, optional)"
              placeholder="discord, setup"
              value={form.tags}
              onChange={e => setForm({ ...form, tags: e.target.value })}
            />
            <Input
              label="Entities (comma-separated, optional)"
              placeholder="config, bot-token"
              value={form.entities}
              onChange={e => setForm({ ...form, entities: e.target.value })}
            />
            {!form.entryId && (
              <Field label="Scope">
                <select
                  value={form.projectId}
                  onChange={e => setForm({ ...form, projectId: e.target.value })}
                  style={selectStyle}
                >
                  <option value="">Org-wide (all projects)</option>
                  {projects.map(p => (
                    <option key={p.id} value={p.id}>
                      {p.name}
                    </option>
                  ))}
                </select>
              </Field>
            )}
            {form.entryId && (
              <Field label="Status">
                <select
                  value={form.status}
                  onChange={e => setForm({ ...form, status: e.target.value })}
                  style={selectStyle}
                >
                  <option value="active">Active (searchable)</option>
                  <option value="stale">Stale (hidden from search)</option>
                </select>
              </Field>
            )}
          </div>
        )}
      </Modal>
    </Page>
  )
}

function EntryCard({
  entry,
  isOrgAdmin,
  onEdit,
  onMarkStale,
  onRevalidate,
}: {
  entry: KnowledgeEntry
  isOrgAdmin: boolean
  onEdit: () => void
  onMarkStale: () => void
  onRevalidate: () => void
}) {
  const statusKey = entry.status ?? 'active'
  const status = STATUS_STYLE[statusKey] ?? STATUS_STYLE.active
  const isStale = statusKey === 'stale'
  const scope = entry.projectName || 'Org-wide'
  const snippet = entry.summary || entry.answer

  return (
    <div
      style={{
        border: `1px solid ${isStale ? T.warnBorder : T.border}`,
        borderRadius: T.rLg,
        background: isStale ? T.warnBg : T.surface,
        padding: 16,
        boxShadow: T.shadowCard,
      }}
    >
      <div style={{ display: 'flex', justifyContent: 'space-between', gap: 12, alignItems: 'flex-start' }}>
        <div style={{ minWidth: 0 }}>
          <div style={{ fontSize: 15, fontWeight: 600, color: T.text }}>{entry.question}</div>
          <div
            style={{
              marginTop: 4,
              fontSize: 13.5,
              color: T.muted,
              overflow: 'hidden',
              textOverflow: 'ellipsis',
              display: '-webkit-box',
              WebkitLineClamp: 2,
              WebkitBoxOrient: 'vertical',
            }}
          >
            {snippet}
          </div>
        </div>
        <span
          style={{
            flexShrink: 0,
            fontSize: 12,
            fontWeight: 600,
            padding: '3px 9px',
            borderRadius: 999,
            background: status.bg,
            color: status.ink,
            border: `1px solid ${status.border}`,
          }}
        >
          {status.label}
        </span>
      </div>

      <div style={{ display: 'flex', flexWrap: 'wrap', gap: 6, marginTop: 12, alignItems: 'center' }}>
        <ScopeChip label={scope} />
        {(entry.tags ?? []).map(t => (
          <TagChip key={t} label={t} />
        ))}
        <span style={{ marginLeft: 'auto', fontSize: 12, color: T.faint }}>
          Last reviewed {fmtReviewed(entry.updatedAt)}
        </span>
      </div>

      {isOrgAdmin && (
        <div style={{ display: 'flex', gap: 8, marginTop: 14 }}>
          <Button size="sm" variant="secondary" onClick={onEdit}>
            <Icon name="edit" size={14} /> Edit
          </Button>
          {isStale ? (
            <Button size="sm" variant="ghost" onClick={onRevalidate}>
              <Icon name="check" size={14} /> Revalidate
            </Button>
          ) : (
            <Button size="sm" variant="ghost" onClick={onMarkStale}>
              <Icon name="archive" size={14} /> Mark stale
            </Button>
          )}
        </div>
      )}
    </div>
  )
}

// PendingCard renders one agent-suggested entry awaiting review, with Approve /
// Dismiss actions and the "Suggested by …" attribution.
function PendingCard({
  entry,
  suggestedBy,
  onApprove,
  onDismiss,
}: {
  entry: KnowledgeEntry
  suggestedBy: string
  onApprove: () => void
  onDismiss: () => void
}) {
  const snippet = entry.summary || entry.answer

  return (
    <div
      style={{
        border: `1px solid ${T.accentBorder}`,
        borderRadius: T.rLg,
        background: T.accentSofter,
        padding: 16,
      }}
    >
      <div style={{ fontSize: 15, fontWeight: 600, color: T.text }}>{entry.question}</div>
      <div
        style={{
          marginTop: 4,
          fontSize: 13.5,
          color: T.muted,
          overflow: 'hidden',
          textOverflow: 'ellipsis',
          display: '-webkit-box',
          WebkitLineClamp: 3,
          WebkitBoxOrient: 'vertical',
        }}
      >
        {snippet}
      </div>

      <div style={{ display: 'flex', flexWrap: 'wrap', gap: 6, marginTop: 12, alignItems: 'center' }}>
        <ScopeChip label={entry.projectName || 'Org-wide'} />
        {(entry.tags ?? []).map(t => (
          <TagChip key={t} label={t} />
        ))}
        <span style={{ marginLeft: 'auto', fontSize: 12, color: T.faint }}>Suggested by {suggestedBy}</span>
      </div>

      <div style={{ display: 'flex', gap: 8, marginTop: 14 }}>
        <Button size="sm" variant="primary" onClick={onApprove}>
          <Icon name="check" size={14} /> Approve
        </Button>
        <Button size="sm" variant="ghost" onClick={onDismiss}>
          <Icon name="x" size={14} /> Dismiss
        </Button>
      </div>
    </div>
  )
}

function ScopeChip({ label }: { label: string }) {
  return (
    <span
      style={{
        fontSize: 12,
        fontWeight: 500,
        padding: '3px 8px',
        borderRadius: 6,
        background: T.accentSofter,
        color: T.accentInk,
        border: `1px solid ${T.accentBorder}`,
      }}
    >
      {label}
    </span>
  )
}

function TagChip({ label }: { label: string }) {
  return (
    <span
      style={{
        fontSize: 12,
        padding: '3px 8px',
        borderRadius: 6,
        background: T.sunken,
        color: T.muted,
      }}
    >
      {label}
    </span>
  )
}

function Field({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div style={{ width: '100%' }}>
      <label style={{ display: 'block', fontSize: 13, fontWeight: 500, color: '#3A414D', marginBottom: 6 }}>
        {label}
      </label>
      {children}
    </div>
  )
}

const textareaStyle: React.CSSProperties = {
  boxSizing: 'border-box',
  width: '100%',
  background: T.surface,
  border: `1px solid ${T.borderStrong}`,
  borderRadius: T.rMd,
  color: T.body,
  padding: '10px 13px',
  fontSize: 14,
  fontFamily: 'inherit',
  outline: 'none',
  resize: 'vertical',
}

const selectStyle: React.CSSProperties = {
  boxSizing: 'border-box',
  width: '100%',
  height: 40,
  background: T.surface,
  border: `1px solid ${T.borderStrong}`,
  borderRadius: T.rMd,
  color: T.body,
  padding: '0 13px',
  fontSize: 14,
  fontFamily: 'inherit',
  outline: 'none',
}
