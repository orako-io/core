// Project detail (org-admin) — per-project alert routing (reusing the same
// ConfigureProvider-backed RPC the Integrations pages use), a project-scoped
// member view (add/remove + per-project expertise tags), and a danger zone
// (archive = reversible freeze, delete = guarded hard delete).
//
// Alert routing note: ListProjectsDetailed's `routing` only carries providers
// that already have an explicit per-project channel override — a provider
// connected at the org level with no override yet is absent from it (falls
// back to the org default channel server-side). So the set of providers to
// show here comes from ListConnectedChannels (provider *connections* are
// org-level — every project sees the same connected kinds), and the per-kind
// channel value comes from this project's `routing` entry when present.

import { useCallback, useEffect, useState } from 'react'
import { useNavigate, useParams } from 'react-router'
import { api, type ProjectDetail, type Expert } from '../lib/client'
import { useIdentity } from '../lib/identity'
import { useToast, toastMessage } from '../lib/toast'
import { EXPERTISE_TAGS } from '../lib/expertise'
import { Page } from '../components/Layout'
import { Button } from '../components/Button'
import { Modal } from '../components/Modal'
import { DeleteProjectModal } from '../components/DeleteProjectModal'
import { ChipMultiSelect } from '../components/ChipMultiSelect'
import { ErrorBanner } from '../components/ErrorBanner'
import { Spinner } from '../components/Spinner'
import { Icon } from '../components/Icon'
import { SettingsCard, CardHeader, Divider } from '../components/Settings'
import { T } from '../lib/theme'

const PROVIDER_LABEL: Record<string, string> = { slack: 'Slack', teams: 'Microsoft Teams', discord: 'Discord' }

function parseChannels(v: string): string[] {
  return v
    .split(',')
    .map(s => s.trim())
    .filter(Boolean)
}

function initials(seed: string): string {
  return (seed?.replace(/[^a-zA-Z0-9]/g, '').slice(0, 2) || '--').toUpperCase()
}

export function ProjectDetailPage() {
  const { id = '' } = useParams<{ id: string }>()
  const navigate = useNavigate()
  const toast = useToast()
  const { selectedProjectId, refresh: refreshIdentity } = useIdentity()

  const [project, setProject] = useState<ProjectDetail | null>(null)
  const [connectedKinds, setConnectedKinds] = useState<string[]>([])
  const [members, setMembers] = useState<Expert[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<unknown>(null)
  const [notFound, setNotFound] = useState(false)

  const load = useCallback(async () => {
    setError(null)
    try {
      const [detailRes, channelsRes, specRes] = await Promise.all([
        api.listProjectsDetailed(true),
        api.listConnectedChannels().catch(() => ({ channels: [] as string[] })),
        api.listExperts(id).catch(() => ({ experts: [] as Expert[] })),
      ])
      const found = (detailRes.projects ?? []).find(p => p.projectId === id) ?? null
      setProject(found)
      setNotFound(!found)
      // Credentials/connections are org-level, so every project sees the same
      // connected kinds — only the per-project channel override differs.
      setConnectedKinds((channelsRes.channels ?? []).filter(c => c !== 'dashboard'))
      setMembers(specRes.experts ?? [])
    } catch (e) {
      setError(e)
    } finally {
      setLoading(false)
    }
  }, [id])

  useEffect(() => {
    void load()
  }, [load])

  // --- alert routing ---
  const [channelInputs, setChannelInputs] = useState<Record<string, string>>({})
  const [savingKind, setSavingKind] = useState<string | null>(null)

  useEffect(() => {
    if (!project) return
    const next: Record<string, string> = {}
    for (const kind of connectedKinds) {
      const r = (project.routing ?? []).find(x => x.kind === kind)
      next[kind] = (r?.alertChannelIds ?? []).join(', ')
    }
    setChannelInputs(next)
  }, [project, connectedKinds])

  async function saveChannels(kind: string) {
    setSavingKind(kind)
    try {
      await api.updateProviderAlertChannels(id, kind, parseChannels(channelInputs[kind] ?? ''))
      toast.success(`${PROVIDER_LABEL[kind] ?? kind} alert channels updated`)
      await load()
    } catch (e) {
      toast.error(toastMessage(e))
    } finally {
      setSavingKind(null)
    }
  }

  // --- project members ---
  const [addOpen, setAddOpen] = useState(false)
  const [addEmail, setAddEmail] = useState('')
  const [adding, setAdding] = useState(false)

  async function handleAddMember() {
    const email = addEmail.trim()
    if (!email || adding) return
    setAdding(true)
    try {
      // Empty expertise — the member sets it during onboarding, or an admin
      // edits it here later via "Edit tags".
      await api.addMember(id, email, [])
      toast.success(`Added ${email} to ${project?.name ?? 'the project'}`)
      setAddOpen(false)
      setAddEmail('')
      await load()
    } catch (e) {
      toast.error(toastMessage(e))
    } finally {
      setAdding(false)
    }
  }

  const [editingMemberId, setEditingMemberId] = useState<string | null>(null)
  const [editTags, setEditTags] = useState<string[]>([])
  const [savingTags, setSavingTags] = useState(false)

  function startEditTags(m: Expert) {
    setEditingMemberId(m.memberId)
    setEditTags(m.domains ?? [])
  }

  async function commitTags() {
    if (!editingMemberId || savingTags) return
    setSavingTags(true)
    try {
      await api.setMemberExpertise(id, editingMemberId, editTags)
      toast.success('Expertise tags updated')
      setEditingMemberId(null)
      await load()
    } catch (e) {
      toast.error(toastMessage(e))
    } finally {
      setSavingTags(false)
    }
  }

  const [removingId, setRemovingId] = useState<string | null>(null)

  async function handleRemoveMember(m: Expert) {
    if (!window.confirm(`Remove ${m.displayName || m.email || 'this member'} from ${project?.name ?? 'this project'}?`)) return
    setRemovingId(m.memberId)
    try {
      await api.removeMember(id, m.memberId, false)
      toast.success('Member removed from project')
      await load()
    } catch (e) {
      toast.error(toastMessage(e))
    } finally {
      setRemovingId(null)
    }
  }

  // --- danger zone ---
  const [archiving, setArchiving] = useState(false)

  async function handleToggleArchive() {
    if (!project) return
    const next = !project.archived
    setArchiving(true)
    try {
      await api.setProjectArchived(id, next)
      toast.success(
        next
          ? 'Project archived — routing stops; its external-only members are freed from the seat count. Reactivate anytime.'
          : 'Project reactivated',
      )
      await refreshIdentity()
      await load()
    } catch (e) {
      toast.error(toastMessage(e))
    } finally {
      setArchiving(false)
    }
  }

  const [deleteOpen, setDeleteOpen] = useState(false)
  const [deleting, setDeleting] = useState(false)

  async function handleConfirmDelete() {
    if (!project || deleting) return
    setDeleting(true)
    try {
      await api.deleteProject(id)
      toast.success(`Deleted ${project.name}`)
      await refreshIdentity()
      navigate('/projects')
    } catch (e) {
      toast.error(toastMessage(e))
      setDeleting(false)
    }
  }

  async function handleArchiveInsteadFromModal() {
    setDeleteOpen(false)
    await handleToggleArchive()
  }

  if (loading) {
    return (
      <Page width={860}>
        <div style={{ display: 'flex', justifyContent: 'center', padding: 60 }}>
          <Spinner />
        </div>
      </Page>
    )
  }

  if (notFound || !project) {
    return (
      <Page width={860}>
        <p style={{ color: T.muted }}>Project not found.</p>
        <Button variant="secondary" onClick={() => navigate('/projects')} style={{ marginTop: 14 }}>
          Back to Projects
        </Button>
      </Page>
    )
  }

  const isPrimary = project.projectId === selectedProjectId

  return (
    <Page width={860}>
      <button
        onClick={() => navigate('/projects')}
        style={{
          display: 'inline-flex',
          alignItems: 'center',
          gap: 6,
          fontSize: 12.5,
          fontWeight: 600,
          color: T.faint,
          background: 'none',
          border: 'none',
          cursor: 'pointer',
          marginBottom: 14,
        }}
      >
        <Icon name="chevronRight" size={14} style={{ transform: 'rotate(180deg)' }} /> Projects
      </button>

      <div style={{ display: 'flex', alignItems: 'center', gap: 14, flexWrap: 'wrap', marginBottom: 24 }}>
        <div style={{ minWidth: 0, marginRight: 'auto' }}>
          <div style={{ display: 'flex', alignItems: 'center', gap: 10, flexWrap: 'wrap' }}>
            <h1 style={{ fontSize: 22, fontWeight: 700, letterSpacing: '-.02em', color: T.text, margin: 0 }}>{project.name}</h1>
            {isPrimary && (
              <span
                title="Your default project scope"
                style={{ fontSize: 11.5, fontWeight: 600, color: T.accentInk, background: T.accentSofter, border: `1px solid ${T.accentBorder}`, padding: '3px 9px', borderRadius: 20 }}
              >
                Primary
              </span>
            )}
            {project.archived && (
              <span style={{ fontSize: 11.5, fontWeight: 600, color: T.subtle, background: T.sunken, padding: '3px 9px', borderRadius: 20 }}>
                Archived
              </span>
            )}
          </div>
          <div style={{ fontSize: 13, color: T.faint, marginTop: 3 }}>
            {project.memberCount} members · {project.conversationCount} conversations · {project.resolvedCount} resolved
          </div>
        </div>
      </div>

      {error != null && (
        <div style={{ marginBottom: 16 }}>
          <ErrorBanner error={error} />
        </div>
      )}

      {project.archived && (
        <div
          style={{
            display: 'flex',
            alignItems: 'center',
            gap: 10,
            background: T.surfaceAlt,
            border: `1px solid ${T.border}`,
            borderRadius: 10,
            padding: '10px 14px',
            marginBottom: 20,
            fontSize: 13,
            color: T.muted,
          }}
        >
          <Icon name="archive" size={15} color={T.subtle} />
          <span style={{ flex: 1 }}>This project is archived — the agent has stopped routing to it. Data is intact; reactivate to resume.</span>
          <Button variant="secondary" size="sm" loading={archiving} onClick={() => void handleToggleArchive()}>
            Reactivate
          </Button>
        </div>
      )}

      <div style={{ display: 'flex', flexDirection: 'column', gap: 18 }}>
        {/* Alert routing */}
        <SettingsCard>
          <CardHeader icon="send" title="Alert routing" sub="This project's escalation channel per connected provider. On Discord, conversation threads are also created in this channel — pick one the whole team can see (threads stay private; only invited members see them), since Discord can only pull people into a private thread if they can view its parent channel. Bot tokens are org-level — set once in Integrations." />
          <Divider />
          {connectedKinds.length === 0 ? (
            <p style={{ fontSize: 13, color: T.faint, margin: 0 }}>
              No messaging provider connected yet.{' '}
              <span onClick={() => navigate('/integrations')} style={{ color: T.accent, fontWeight: 600, cursor: 'pointer' }}>
                Connect one in Integrations →
              </span>
            </p>
          ) : (
            <div style={{ display: 'flex', flexDirection: 'column', gap: 16 }}>
              {connectedKinds.map(kind => (
                <div key={kind}>
                  <div style={{ fontSize: 13, fontWeight: 600, color: T.body, marginBottom: 7 }}>{PROVIDER_LABEL[kind] ?? kind}</div>
                  <div style={{ display: 'flex', alignItems: 'center', gap: 10 }}>
                    <input
                      type="text"
                      value={channelInputs[kind] ?? ''}
                      onChange={e => setChannelInputs(s => ({ ...s, [kind]: e.target.value }))}
                      placeholder="#project-alerts (comma-separated)"
                      disabled={savingKind === kind}
                      style={{ flex: 1, height: 40, border: `1px solid ${T.borderStrong}`, borderRadius: T.rMd, padding: '0 13px', fontSize: 13.5, fontFamily: T.mono, color: T.body, background: T.surfaceAlt }}
                    />
                    <Button variant="secondary" size="sm" loading={savingKind === kind} onClick={() => void saveChannels(kind)} style={{ flex: 'none' }}>
                      Save
                    </Button>
                  </div>
                </div>
              ))}
              <p style={{ fontSize: 12, color: T.faint, margin: 0 }}>Empty falls back to the organization's default alert channel.</p>
            </div>
          )}
        </SettingsCard>

        {/* Project members */}
        <SettingsCard>
          <CardHeader
            icon="users"
            title="Project members"
            sub="Who is in this project and their expertise tags here. The org roster lives in Team."
            right={
              <Button variant="secondary" size="sm" onClick={() => setAddOpen(true)}>
                <Icon name="plus" size={14} />
                Add member
              </Button>
            }
          />
          <Divider />
          {members.length === 0 ? (
            <p style={{ fontSize: 13, color: T.faint, margin: 0 }}>No members in this project yet.</p>
          ) : (
            <div style={{ display: 'flex', flexDirection: 'column', gap: 10 }}>
              {members.map(m => {
                const editing = editingMemberId === m.memberId
                return (
                  <div key={m.memberId} style={{ border: `1px solid ${T.borderSubtle}`, borderRadius: 12, padding: 14, background: T.surfaceAlt }}>
                    <div style={{ display: 'flex', alignItems: 'center', gap: 12 }}>
                      <div style={{ width: 32, height: 32, flex: 'none', borderRadius: '50%', background: '#F3D9A4', display: 'flex', alignItems: 'center', justifyContent: 'center', fontSize: 11, fontWeight: 700, color: '#7A5A16' }}>
                        {initials(m.displayName || m.email || m.memberId)}
                      </div>
                      <div style={{ flex: 1, minWidth: 0 }}>
                        <div style={{ fontSize: 13.5, fontWeight: 600, color: T.body, whiteSpace: 'nowrap', overflow: 'hidden', textOverflow: 'ellipsis' }}>
                          {m.displayName || m.email || m.memberId.slice(0, 8)}
                        </div>
                        {m.email && <div style={{ fontSize: 12, color: T.faint }}>{m.email}</div>}
                      </div>
                      {!editing && (
                        <Button variant="ghost" size="sm" onClick={() => startEditTags(m)}>
                          Edit tags
                        </Button>
                      )}
                      <Button
                        variant="ghost"
                        size="sm"
                        loading={removingId === m.memberId}
                        onClick={() => void handleRemoveMember(m)}
                        style={{ color: T.dangerInk }}
                      >
                        Remove
                      </Button>
                    </div>
                    {editing ? (
                      <div style={{ marginTop: 12 }}>
                        <ChipMultiSelect options={EXPERTISE_TAGS} value={editTags} onChange={setEditTags} disabled={savingTags} />
                        <div style={{ display: 'flex', gap: 8, marginTop: 10 }}>
                          <Button variant="primary" size="sm" loading={savingTags} onClick={() => void commitTags()}>
                            Save tags
                          </Button>
                          <Button variant="secondary" size="sm" disabled={savingTags} onClick={() => setEditingMemberId(null)}>
                            Cancel
                          </Button>
                        </div>
                      </div>
                    ) : (
                      (m.domains ?? []).length > 0 && (
                        <div style={{ display: 'flex', flexWrap: 'wrap', gap: 5, marginTop: 10 }}>
                          {(m.domains ?? []).map(d => (
                            <span key={d} style={{ fontSize: 11.5, fontWeight: 600, color: '#4A4F5A', background: T.sunken, border: `1px solid ${T.borderSubtle}`, padding: '3px 9px', borderRadius: 20 }}>
                              {d}
                            </span>
                          ))}
                        </div>
                      )
                    )}
                  </div>
                )
              })}
            </div>
          )}
        </SettingsCard>

        {/* Danger zone */}
        <SettingsCard tone="danger">
          <CardHeader tone="danger" icon="alertTriangle" title="Danger zone" sub="Archive is reversible; delete is permanent." />
          <Divider />
          <div style={{ display: 'flex', flexDirection: 'column', gap: 10 }}>
            <div style={{ display: 'flex', alignItems: 'center', gap: 16, padding: '14px 16px', border: `1px solid ${T.borderSubtle}`, borderRadius: 12, background: T.surface, flexWrap: 'wrap' }}>
              <div style={{ flex: 1, minWidth: 220 }}>
                <div style={{ fontSize: 13.5, fontWeight: 600, color: T.text }}>{project.archived ? 'Reactivate project' : 'Archive project'}</div>
                <div style={{ fontSize: 12.5, color: T.muted, lineHeight: 1.5 }}>
                  {project.archived
                    ? 'Restores routing instantly — nothing to re-import.'
                    : 'Reversible freeze: stops routing, keeps conversations and their history intact. External-only members are freed from the seat count.'}
                </div>
              </div>
              <Button variant="secondary" loading={archiving} onClick={() => void handleToggleArchive()}>
                {project.archived ? 'Reactivate' : 'Archive'}
              </Button>
            </div>
            <div style={{ display: 'flex', alignItems: 'center', gap: 16, padding: '14px 16px', border: '1px solid #F1D9D9', borderRadius: 12, background: '#FDF6F6', flexWrap: 'wrap' }}>
              <div style={{ flex: 1, minWidth: 220 }}>
                <div style={{ fontSize: 13.5, fontWeight: 600, color: '#8A2E2E' }}>Delete project</div>
                <div style={{ fontSize: 12.5, color: '#B08282', lineHeight: 1.5 }}>
                  Permanently removes this project, its conversations and their history. Cannot be undone.
                </div>
              </div>
              <Button variant="danger" onClick={() => setDeleteOpen(true)}>
                Delete…
              </Button>
            </div>
          </div>
        </SettingsCard>
      </div>

      {/* add member modal */}
      <Modal
        open={addOpen}
        onClose={() => setAddOpen(false)}
        title="Add a member"
        subtitle={`Adds an existing org member (or invites a new one) to ${project.name}.`}
        footer={
          <>
            <Button variant="secondary" onClick={() => setAddOpen(false)}>
              Cancel
            </Button>
            <Button variant="primary" loading={adding} disabled={!addEmail.trim()} onClick={() => void handleAddMember()}>
              Add member
            </Button>
          </>
        }
      >
        <div style={{ display: 'flex', flexDirection: 'column', gap: 16 }}>
          <label style={{ display: 'flex', flexDirection: 'column', gap: 7 }}>
            <span style={{ fontSize: 13, fontWeight: 500, color: T.body }}>Email address</span>
            <input
              value={addEmail}
              onChange={e => setAddEmail(e.target.value)}
              placeholder="name@company.com"
              style={{ height: 44, border: `1px solid ${T.borderStrong}`, borderRadius: 10, padding: '0 14px', fontSize: 14, color: T.text, background: T.surface, outline: 'none' }}
            />
          </label>
          <p style={{ fontSize: 12.5, color: T.faint, margin: 0 }}>
            They pick their expertise during onboarding — you can also set it here later with “Edit tags”.
          </p>
        </div>
      </Modal>

      {/* delete modal */}
      <DeleteProjectModal
        open={deleteOpen}
        projectName={project.name}
        onClose={() => setDeleteOpen(false)}
        onArchiveInstead={() => void handleArchiveInsteadFromModal()}
        onConfirmDelete={() => void handleConfirmDelete()}
        archiving={archiving}
        deleting={deleting}
      />
    </Page>
  )
}
