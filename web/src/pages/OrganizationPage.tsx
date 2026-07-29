// Organization settings — profile (org name + slug, real; logo upload is a
// stub toast, no backend yet), default project (real: drives the identity's
// selected project), project list/create (real, ported from the former
// Projects page), escalation (real), and a danger zone.

import { useEffect, useState } from 'react'
import { useIdentity } from '../lib/identity'
import { api, type ProjectSummary } from '../lib/client'
import { Page } from '../components/Layout'
import { Button } from '../components/Button'
import { Select } from '../components/Select'
import { ErrorBanner } from '../components/ErrorBanner'
import { Icon } from '../components/Icon'
import { SettingsCard, CardHeader, Divider, Stepper } from '../components/Settings'
import { DeleteProjectModal } from '../components/DeleteProjectModal'
import { DeleteOrganizationModal } from '../components/DeleteOrganizationModal'
import { T } from '../lib/theme'
import { useToast, toastMessage } from '../lib/toast'
import { useEdition, atCap } from '../lib/edition'
import { UPGRADE_URL } from '../components/UpgradeCard'

const fieldLabel: React.CSSProperties = { fontSize: 13, fontWeight: 600, color: '#3A414D' }

const inputStyle: React.CSSProperties = {
  height: 44,
  border: `1px solid ${T.borderStrong}`,
  borderRadius: T.rLg,
  padding: '0 14px',
  fontSize: 14,
  fontFamily: 'inherit',
  color: T.body,
  background: T.surface,
  outline: 'none',
}

const defaultPill: React.CSSProperties = {
  fontSize: 10,
  fontWeight: 600,
  color: T.subtle,
  background: T.sunken,
  padding: '2px 7px',
  borderRadius: 20,
}

// ── Escalation card ──────────────────────────────────────────────────────────

// minutesLabel renders a seconds value in human units.
function minutesLabel(seconds: number): string {
  if (seconds === 0) return 'disabled'
  if (seconds % 3600 === 0) {
    const h = seconds / 3600
    return `${h} hour${h > 1 ? 's' : ''}`
  }
  return `${Math.round(seconds / 60)} min`
}

function EscalationCard() {
  const toast = useToast()
  const [claimMin, setClaimMin] = useState('')
  const [alertMin, setAlertMin] = useState('')
  const [claimDefault, setClaimDefault] = useState(true)
  const [alertDefault, setAlertDefault] = useState(true)
  const [loaded, setLoaded] = useState(false)
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState<unknown>(null)

  useEffect(() => {
    api
      .getOrganizationSettings()
      .then(res => {
        const s = res.settings ?? {}
        setClaimMin(String(Math.round(Number(s.claimTimeoutSeconds ?? 0) / 60)))
        setAlertMin(String(Math.round(Number(s.alertTimeoutSeconds ?? 0) / 60)))
        setClaimDefault(!!s.claimTimeoutIsDefault)
        setAlertDefault(!!s.alertTimeoutIsDefault)
        setLoaded(true)
      })
      .catch(e => setError(e))
  }, [])

  async function save() {
    const claim = Number(claimMin)
    const alert = Number(alertMin)
    if (Number.isNaN(claim) || claim < 0 || Number.isNaN(alert) || alert < 0) {
      toast.error('Timeouts must be zero (disabled) or a positive number of minutes.')
      return
    }
    setSaving(true)
    try {
      const res = await api.updateOrganizationSettings(
        Math.round(claim * 60),
        Math.round(alert * 60),
        // Org-wide default alert channel is retired from the UI — alert channels
        // are set per provider now. Empty string leaves the stored value alone.
        '',
      )
      const s = res.settings ?? {}
      setClaimDefault(!!s.claimTimeoutIsDefault)
      setAlertDefault(!!s.alertTimeoutIsDefault)
      toast.success('Escalation settings saved.')
    } catch (e) {
      toast.error(toastMessage(e))
    } finally {
      setSaving(false)
    }
  }

  const timeoutBox = (
    label: string,
    hint: string,
    value: string,
    isDefault: boolean,
    onChange: (v: string) => void,
  ) => (
    <div
      style={{
        border: `1px solid ${T.borderStrong}`,
        borderRadius: 13,
        padding: '15px 15px 17px',
        background: T.surfaceAlt,
      }}
    >
      <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', marginBottom: 5 }}>
        <span style={{ fontSize: 13, fontWeight: 700, color: T.body }}>{label}</span>
        {isDefault && <span style={defaultPill}>default</span>}
      </div>
      <p style={{ fontSize: 11.5, color: T.faint, lineHeight: 1.45, minHeight: 34, margin: 0 }}>{hint}</p>
      <div style={{ marginTop: 11 }}>
        <Stepper value={value} onChange={onChange} unit="min" step={5} min={0} disabled={!loaded} />
      </div>
    </div>
  )

  return (
    <SettingsCard>
      <CardHeader
        icon="alertTriangle"
        title="Escalation"
        sub={`How pool questions escalate when nobody answers. Zero disables a rule; defaults are ${minutesLabel(
          30 * 60,
        )} and ${minutesLabel(4 * 3600)}.`}
      />
      <Divider />
      {error != null && (
        <div style={{ marginBottom: 14 }}>
          <ErrorBanner error={error} />
        </div>
      )}
      <div style={{ display: 'grid', gridTemplateColumns: 'repeat(2, 1fr)', gap: 14 }}>
        {timeoutBox(
          'Reminder timeout',
          'Remind candidates once when a question sits unanswered.',
          claimMin,
          claimDefault,
          setClaimMin,
        )}
        {timeoutBox(
          'Alert timeout',
          'Post to the alert channel if still unanswered.',
          alertMin,
          alertDefault,
          setAlertMin,
        )}
      </div>

      <div style={{ display: 'flex', justifyContent: 'flex-end', marginTop: 24 }}>
        <Button variant="primary" onClick={save} loading={saving} disabled={!loaded}>
          Save changes
        </Button>
      </div>
    </SettingsCard>
  )
}

export function OrganizationPage() {
  const { projects, selectedProjectId, setSelectedProjectId, refresh, isOrgAdmin } = useIdentity()
  const toast = useToast()

  const [orgName, setOrgName] = useState('')
  // savedName is the last persisted value; the Save button lights up only when
  // the field diverges from it.
  const [savedName, setSavedName] = useState('')
  const [savingName, setSavingName] = useState(false)

  // Load the real organization name into both the editable field and the
  // saved baseline.
  useEffect(() => {
    let cancelled = false
    api
      .getOrganization()
      .then(res => {
        if (!cancelled) {
          setOrgName(res.name)
          setSavedName(res.name)
        }
      })
      .catch(() => {})
    return () => {
      cancelled = true
    }
  }, [])

  const nameDirty = orgName.trim() !== '' && orgName.trim() !== savedName
  const canSaveName = isOrgAdmin && nameDirty && !savingName

  async function saveOrgName() {
    if (!canSaveName) return
    setSavingName(true)
    try {
      const res = await api.renameOrganization(orgName.trim())
      setOrgName(res.name)
      setSavedName(res.name)
      // The sidebar org name refetches getOrganization on its own; a toast is
      // enough to confirm the rename here.
      toast.success('Organization renamed.')
    } catch (e) {
      toast.error(toastMessage(e))
    } finally {
      setSavingName(false)
    }
  }

  const [newName, setNewName] = useState('')
  const [creating, setCreating] = useState(false)
  const [createError, setCreateError] = useState<unknown>(null)

  const { data: edition } = useEdition()
  const projectsCapped = atCap(edition, 'projects', projects.length)

  const [deleteOpen, setDeleteOpen] = useState(false)
  const [deletingOrg, setDeletingOrg] = useState(false)

  // The org's default project is the first-created one (the identity list is
  // ordered created_at ASC). It can never be deleted — the org always needs it.
  const defaultProjectId = projects[0]?.id ?? ''

  // Per-project delete (reuses the DeleteProject RPC). projectToDelete holds the
  // project whose danger modal is open; archiving/deleting track the two actions.
  const [projectToDelete, setProjectToDelete] = useState<ProjectSummary | null>(null)
  const [archiving, setArchiving] = useState(false)
  const [deletingProject, setDeletingProject] = useState(false)

  async function handleCreate() {
    if (!newName.trim() || creating) return
    setCreating(true)
    setCreateError(null)
    try {
      await api.createProject(newName.trim())
      setNewName('')
      await refresh()
    } catch (e) {
      setCreateError(e)
    } finally {
      setCreating(false)
    }
  }

  async function handleDeleteProject() {
    if (!projectToDelete || deletingProject) return
    setDeletingProject(true)
    try {
      await api.deleteProject(projectToDelete.id)
      toast.success(`Deleted ${projectToDelete.name}`)
      setProjectToDelete(null)
      await refresh()
    } catch (e) {
      toast.error(toastMessage(e))
    } finally {
      setDeletingProject(false)
    }
  }

  async function handleArchiveProjectInstead() {
    if (!projectToDelete || archiving) return
    setArchiving(true)
    try {
      await api.setProjectArchived(projectToDelete.id, true)
      toast.success(`Archived ${projectToDelete.name}`)
      setProjectToDelete(null)
      await refresh()
    } catch (e) {
      toast.error(toastMessage(e))
    } finally {
      setArchiving(false)
    }
  }

  async function handleDeleteOrg() {
    if (deletingOrg) return
    setDeletingOrg(true)
    try {
      await api.deleteOrganization(savedName)
      // Drop the active-org header (it now points at a deleted org) and hard
      // reload from the root so the app re-bootstraps into the caller's
      // remaining org (or onboarding when none is left).
      localStorage.removeItem('orako:activeOrg')
      toast.success('Organization deleted.')
      window.location.href = '/'
    } catch (e) {
      toast.error(toastMessage(e))
      setDeletingOrg(false)
    }
  }

  const slug = orgName.toLowerCase().trim().replace(/[^a-z0-9]+/g, '-').replace(/^-+|-+$/g, '') || 'acme'

  return (
    <Page width={860}>
      <div style={{ display: 'flex', flexDirection: 'column', gap: 18 }}>
        <SettingsCard>
          <CardHeader icon="building" title="Organization profile" sub="Shown to your team and on invitations." />
          <Divider />
          <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 16 }}>
            <label style={{ display: 'flex', flexDirection: 'column', gap: 7 }}>
              <span style={fieldLabel}>Organization name</span>
              <input
                value={orgName}
                onChange={e => setOrgName(e.target.value)}
                onKeyDown={e => e.key === 'Enter' && saveOrgName()}
                placeholder="Acme"
                disabled={!isOrgAdmin}
                maxLength={120}
                style={{ ...inputStyle, opacity: isOrgAdmin ? 1 : 0.6 }}
              />
            </label>
            <label style={{ display: 'flex', flexDirection: 'column', gap: 7 }}>
              <span style={fieldLabel}>Workspace URL</span>
              <div
                style={{
                  ...inputStyle,
                  display: 'flex',
                  alignItems: 'center',
                  color: T.subtle,
                  background: T.surfaceAlt,
                }}
              >
                app.orako.io/<span style={{ color: T.body, fontWeight: 600 }}>{slug}</span>
              </div>
            </label>
          </div>
          {isOrgAdmin && (
            <div style={{ display: 'flex', justifyContent: 'flex-end', marginTop: 18 }}>
              <Button variant="primary" onClick={saveOrgName} loading={savingName} disabled={!canSaveName}>
                Save
              </Button>
            </div>
          )}
        </SettingsCard>

        <SettingsCard style={{ padding: '22px 26px' }}>
          <CardHeader
            icon="grid"
            title="Default project"
            sub="Where new conversations land unless routed elsewhere."
            right={
              <div style={{ width: 190, flex: 'none' }}>
                <Select value={selectedProjectId} onChange={e => setSelectedProjectId(e.target.value)}>
                  {projects.length === 0 && <option value="">No projects</option>}
                  {projects.map(p => (
                    <option key={p.id} value={p.id}>
                      {p.name}
                    </option>
                  ))}
                </Select>
              </div>
            }
          />
        </SettingsCard>

        <SettingsCard>
          <CardHeader icon="grid" title="Projects" sub="Projects group members and conversations." />
          <Divider />
          {projects.length > 0 && (
            <div style={{ display: 'flex', flexDirection: 'column', gap: 10, marginBottom: 12 }}>
              {projects.map(p => {
                const isDefault = p.id === defaultProjectId
                return (
                  <div
                    key={p.id}
                    style={{
                      display: 'flex',
                      alignItems: 'center',
                      justifyContent: 'space-between',
                      padding: '11px 14px',
                      border: `1px solid ${T.border}`,
                      borderRadius: 12,
                      background: T.surfaceAlt,
                    }}
                  >
                    <span style={{ fontSize: 14, fontWeight: 600, color: T.body }}>{p.name}</span>
                    <div style={{ display: 'flex', alignItems: 'center', gap: 10 }}>
                      <span
                        style={{
                          fontFamily: T.mono,
                          fontSize: 11.5,
                          color: T.subtle,
                          background: T.sunken,
                          padding: '4px 9px',
                          borderRadius: 6,
                        }}
                      >
                        {p.id.slice(0, 8)}
                      </span>
                      {isOrgAdmin && (
                        <button
                          type="button"
                          onClick={() => !isDefault && setProjectToDelete(p)}
                          disabled={isDefault}
                          title={
                            isDefault
                              ? 'The default project cannot be deleted — the organization always needs it.'
                              : 'Delete project'
                          }
                          style={{
                            display: 'flex',
                            alignItems: 'center',
                            justifyContent: 'center',
                            width: 30,
                            height: 30,
                            border: `1px solid ${T.border}`,
                            borderRadius: 8,
                            background: T.surface,
                            cursor: isDefault ? 'not-allowed' : 'pointer',
                            opacity: isDefault ? 0.4 : 1,
                            padding: 0,
                          }}
                        >
                          <Icon name="trash" size={15} color={isDefault ? T.subtle : T.dangerInk} />
                        </button>
                      )}
                    </div>
                  </div>
                )
              })}
            </div>
          )}
          {createError != null && (
            <div style={{ marginBottom: 14 }}>
              <ErrorBanner error={createError} />
            </div>
          )}
          <div style={{ display: 'flex', gap: 10 }}>
            <input
              type="text"
              placeholder="New project"
              value={newName}
              onChange={e => setNewName(e.target.value)}
              onKeyDown={e => e.key === 'Enter' && !projectsCapped && handleCreate()}
              disabled={creating || projectsCapped}
              style={{ ...inputStyle, flex: 1, opacity: projectsCapped ? 0.6 : 1 }}
            />
            <Button
              variant="primary"
              onClick={handleCreate}
              loading={creating}
              disabled={projectsCapped}
              title={projectsCapped ? 'Community limit reached — upgrade to add more projects' : undefined}
            >
              <Icon name="plus" size={16} strokeWidth={2.2} color="#fff" />
              Create
            </Button>
          </div>
          {projectsCapped ? (
            <p style={{ fontSize: 12.5, color: T.subtle, margin: '9px 0 0' }}>
              Community limit reached (1 project).{' '}
              <a href={UPGRADE_URL} target="_blank" rel="noreferrer" style={{ color: T.accent, fontWeight: 600, textDecoration: 'none' }}>
                Upgrade for unlimited projects →
              </a>
            </p>
          ) : (
            <p style={{ fontSize: 12.5, color: T.faint, margin: '9px 0 0' }}>Requires lead or admin role.</p>
          )}
        </SettingsCard>

        {isOrgAdmin && <EscalationCard />}

        {isOrgAdmin && (
          <SettingsCard tone="danger" style={{ padding: '22px 26px' }}>
            <CardHeader
              tone="danger"
              icon="trash"
              title="Delete organization"
              sub="Permanently removes projects, conversations, and their history."
              right={
                <Button variant="danger" onClick={() => setDeleteOpen(true)} style={{ height: 40, flex: 'none' }}>
                  Delete…
                </Button>
              }
            />
          </SettingsCard>
        )}
      </div>

      <DeleteOrganizationModal
        open={deleteOpen}
        orgName={savedName}
        onClose={() => setDeleteOpen(false)}
        onConfirmDelete={() => void handleDeleteOrg()}
        deleting={deletingOrg}
      />

      <DeleteProjectModal
        open={projectToDelete !== null}
        projectName={projectToDelete?.name ?? ''}
        onClose={() => setProjectToDelete(null)}
        onArchiveInstead={() => void handleArchiveProjectInstead()}
        onConfirmDelete={() => void handleDeleteProject()}
        archiving={archiving}
        deleting={deletingProject}
      />
    </Page>
  )
}
