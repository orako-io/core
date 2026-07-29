// Projects tab (org-admin) — the home for project CRUD and a stats overview.
// List every project in the active org (name, members, resolved conversations,
// alert routing), create/rename/archive/delete. Row click -> detail (/projects/:id).

import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { api, type ProjectDetail } from '../lib/client'
import { cacheGet, cacheSet, cacheInvalidate } from '../lib/queryCache'
import { useIdentity } from '../lib/identity'
import { useToast, toastMessage } from '../lib/toast'
import { Page, usePageHeader } from '../components/Layout'
import { Button } from '../components/Button'
import { Modal } from '../components/Modal'
import { DeleteProjectModal } from '../components/DeleteProjectModal'
import { ErrorBanner } from '../components/ErrorBanner'
import { Spinner } from '../components/Spinner'
import { Icon } from '../components/Icon'
import { CapNudge } from '../components/UpgradeCard'
import { useEdition, atCap } from '../lib/edition'
import { T } from '../lib/theme'

const CACHE_KEY = 'projects-detailed'
const COLS = 'minmax(200px,1.6fr) 110px 110px minmax(160px,1.3fr) 40px'

function fmtRouting(p: ProjectDetail): { label: string; empty: boolean } {
  const routing = (p.routing ?? []).filter(r => (r.alertChannelIds ?? []).length > 0)
  if (routing.length === 0) return { label: 'Org default channel', empty: true }
  const first = routing[0]
  const label = `${cap(first.kind)} · ${(first.alertChannelIds ?? []).length} channel${(first.alertChannelIds ?? []).length === 1 ? '' : 's'}`
  return { label: routing.length > 1 ? `${label} +${routing.length - 1}` : label, empty: false }
}

function cap(s: string): string {
  return s ? s.charAt(0).toUpperCase() + s.slice(1) : s
}

export function ProjectsPage() {
  const navigate = useNavigate()
  const toast = useToast()
  const { selectedProjectId, refresh: refreshIdentity } = useIdentity()

  const [projects, setProjects] = useState<ProjectDetail[]>(() => cacheGet<ProjectDetail[]>(CACHE_KEY) ?? [])
  const firstLoad = useRef(cacheGet(CACHE_KEY) === undefined)
  const [loading, setLoading] = useState(firstLoad.current)
  const [error, setError] = useState<unknown>(null)

  const [includeArchived, setIncludeArchived] = useState(false)
  const [search, setSearch] = useState('')

  const load = useCallback(async () => {
    setError(null)
    try {
      // Always fetch the superset (archived included) so counts/toggle never
      // need a second round-trip — the toggle just filters client-side.
      const res = await api.listProjectsDetailed(true)
      const list = res.projects ?? []
      setProjects(list)
      cacheSet(CACHE_KEY, list)
    } catch (e) {
      if (firstLoad.current) setError(e)
      else toast.error(toastMessage(e))
    } finally {
      setLoading(false)
      firstLoad.current = false
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])

  useEffect(() => {
    void load()
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])

  const activeCount = useMemo(() => projects.filter(p => !p.archived).length, [projects])
  const archivedCount = useMemo(() => projects.filter(p => p.archived).length, [projects])

  const rows = useMemo(() => {
    const q = search.trim().toLowerCase()
    return projects
      .filter(p => includeArchived || !p.archived)
      .filter(p => !q || p.name.toLowerCase().includes(q))
  }, [projects, includeArchived, search])

  // --- create ---
  const [createOpen, setCreateOpen] = useState(false)
  const [createName, setCreateName] = useState('')
  const [creating, setCreating] = useState(false)

  async function handleCreate() {
    const name = createName.trim()
    if (!name || creating) return
    setCreating(true)
    try {
      await api.createProject(name)
      toast.success(`Created project ${name}`)
      setCreateOpen(false)
      setCreateName('')
      await refreshIdentity()
      await load()
    } catch (e) {
      toast.error(toastMessage(e))
    } finally {
      setCreating(false)
    }
  }

  const { data: edition } = useEdition()
  const projectsCapped = atCap(edition, 'projects', activeCount)

  usePageHeader(
    {
      actions: (
        <Button
          variant="primary"
          size="sm"
          disabled={projectsCapped}
          title={projectsCapped ? 'Community limit reached — upgrade to add more projects' : undefined}
          onClick={() => setCreateOpen(true)}
        >
          <Icon name="plus" size={15} color="#fff" strokeWidth={2.4} />
          New project
        </Button>
      ),
    },
    [projectsCapped],
  )

  // --- rename (inline) ---
  const [renamingId, setRenamingId] = useState<string | null>(null)
  const [renameValue, setRenameValue] = useState('')
  const [renaming, setRenaming] = useState(false)

  function startRename(p: ProjectDetail) {
    setOpenMenuFor(null)
    setRenamingId(p.projectId)
    setRenameValue(p.name)
  }

  async function commitRename() {
    const name = renameValue.trim()
    if (!renamingId || !name || renaming) return
    setRenaming(true)
    try {
      await api.renameProject(renamingId, name)
      toast.success('Project renamed')
      setRenamingId(null)
      await refreshIdentity()
      await load()
    } catch (e) {
      toast.error(toastMessage(e))
    } finally {
      setRenaming(false)
    }
  }

  // --- row menu ---
  const [openMenuFor, setOpenMenuFor] = useState<string | null>(null)
  const menuRef = useRef<HTMLDivElement>(null)
  useEffect(() => {
    if (!openMenuFor) return
    function onDoc(e: MouseEvent) {
      if (menuRef.current && !menuRef.current.contains(e.target as Node)) setOpenMenuFor(null)
    }
    document.addEventListener('mousedown', onDoc)
    return () => document.removeEventListener('mousedown', onDoc)
  }, [openMenuFor])

  // --- archive ---
  const [archivingId, setArchivingId] = useState<string | null>(null)

  async function handleToggleArchive(p: ProjectDetail) {
    const next = !p.archived
    setArchivingId(p.projectId)
    try {
      await api.setProjectArchived(p.projectId, next)
      toast.success(
        next
          ? 'Project archived — routing stops; its external-only members are freed from the seat count. Reactivate anytime.'
          : 'Project reactivated',
      )
      setOpenMenuFor(null)
      cacheInvalidate(CACHE_KEY)
      await refreshIdentity()
      await load()
    } catch (e) {
      toast.error(toastMessage(e))
    } finally {
      setArchivingId(null)
    }
  }

  // --- delete (guarded) ---
  const [deleteTarget, setDeleteTarget] = useState<ProjectDetail | null>(null)
  const [deleting, setDeleting] = useState(false)

  async function handleConfirmDelete() {
    if (!deleteTarget || deleting) return
    setDeleting(true)
    try {
      await api.deleteProject(deleteTarget.projectId)
      toast.success(`Deleted ${deleteTarget.name}`)
      setDeleteTarget(null)
      await refreshIdentity()
      await load()
    } catch (e) {
      toast.error(toastMessage(e))
    } finally {
      setDeleting(false)
    }
  }

  async function handleArchiveInsteadFromModal() {
    if (!deleteTarget) return
    await handleToggleArchive(deleteTarget)
    setDeleteTarget(null)
  }

  if (loading) {
    return (
      <Page width={1100}>
        <div style={{ display: 'flex', justifyContent: 'center', padding: 60 }}>
          <Spinner />
        </div>
      </Page>
    )
  }

  return (
    <Page width={1100}>
      {error != null && (
        <div style={{ marginBottom: 16 }}>
          <ErrorBanner error={error} />
        </div>
      )}

      <CapNudge resource="projects" used={activeCount} />

      <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', gap: 16, flexWrap: 'wrap', marginBottom: 18 }}>
        <div style={{ display: 'flex', alignItems: 'center', gap: 16, fontSize: 13.5 }}>
          <span style={{ color: T.body }}>
            <strong>{activeCount}</strong> active
          </span>
          <span style={{ color: T.faint }}>·</span>
          <span style={{ color: T.faint }}>
            <strong style={{ color: T.subtle }}>{archivedCount}</strong> archived
          </span>
          <label style={{ display: 'flex', alignItems: 'center', gap: 7, marginLeft: 8, fontSize: 13, color: T.muted, cursor: 'pointer' }}>
            <input type="checkbox" checked={includeArchived} onChange={e => setIncludeArchived(e.target.checked)} />
            Show archived
          </label>
        </div>
        <div
          style={{
            display: 'flex',
            alignItems: 'center',
            gap: 9,
            height: 38,
            border: `1px solid ${T.borderStrong}`,
            borderRadius: 9,
            padding: '0 13px',
            background: T.surface,
            width: 240,
          }}
        >
          <Icon name="search" size={15} color={T.faint} />
          <input
            value={search}
            onChange={e => setSearch(e.target.value)}
            placeholder="Search projects…"
            style={{ border: 'none', background: 'transparent', fontSize: 13.5, color: T.text, width: '100%', outline: 'none' }}
          />
        </div>
      </div>

      {rows.length > 0 ? (
        <div style={{ background: T.surface, border: `1px solid ${T.border}`, borderRadius: 16, overflow: 'visible' }}>
          <div
            style={{
              display: 'grid',
              gridTemplateColumns: COLS,
              gap: 16,
              padding: '12px 20px',
              borderBottom: `1px solid ${T.borderSubtle}`,
              background: T.surfaceAlt,
              fontSize: 11.5,
              fontWeight: 600,
              letterSpacing: '.04em',
              color: T.faint,
              textTransform: 'uppercase',
              borderRadius: '16px 16px 0 0',
            }}
          >
            <span>Project</span>
            <span>Members</span>
            <span>Resolved</span>
            <span>Alert routing</span>
            <span />
          </div>

          {rows.map((p, i) => {
            const routing = fmtRouting(p)
            const isPrimary = p.projectId === selectedProjectId
            const isRenaming = renamingId === p.projectId
            return (
              <div
                key={p.projectId}
                style={{
                  display: 'grid',
                  gridTemplateColumns: COLS,
                  gap: 16,
                  padding: '14px 20px',
                  borderTop: i === 0 ? 'none' : `1px solid ${T.borderSubtle}`,
                  alignItems: 'center',
                  opacity: p.archived ? 0.65 : 1,
                }}
              >
                {/* name */}
                {isRenaming ? (
                  <div style={{ display: 'flex', alignItems: 'center', gap: 6 }} onClick={e => e.stopPropagation()}>
                    <input
                      autoFocus
                      value={renameValue}
                      onChange={e => setRenameValue(e.target.value)}
                      onKeyDown={e => {
                        if (e.key === 'Enter') void commitRename()
                        if (e.key === 'Escape') setRenamingId(null)
                      }}
                      disabled={renaming}
                      style={{ height: 34, border: `1px solid ${T.accentBorder}`, borderRadius: 8, padding: '0 10px', fontSize: 13.5, fontFamily: 'inherit', flex: 1, minWidth: 0 }}
                    />
                    <button onClick={() => void commitRename()} disabled={renaming} style={iconBtnStyle} aria-label="Save name">
                      <Icon name="check" size={15} color={T.accent} />
                    </button>
                    <button onClick={() => setRenamingId(null)} disabled={renaming} style={iconBtnStyle} aria-label="Cancel rename">
                      <Icon name="x" size={15} color={T.faint} />
                    </button>
                  </div>
                ) : (
                  <div
                    onClick={() => navigate(`/projects/${p.projectId}`)}
                    style={{ display: 'flex', alignItems: 'center', gap: 9, cursor: 'pointer', minWidth: 0 }}
                  >
                    <Icon name="folder" size={16} color={T.faint} style={{ flex: 'none' }} />
                    <span style={{ fontSize: 14, fontWeight: 600, color: T.body, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>
                      {p.name}
                    </span>
                    {isPrimary && (
                      <span title="Your default project scope" style={{ flex: 'none', fontSize: 11, fontWeight: 600, color: T.accentInk, background: T.accentSofter, border: `1px solid ${T.accentBorder}`, padding: '2px 8px', borderRadius: 20 }}>
                        Primary
                      </span>
                    )}
                    {p.archived && (
                      <span style={{ flex: 'none', fontSize: 11, fontWeight: 600, color: T.subtle, background: T.sunken, padding: '2px 8px', borderRadius: 20 }}>
                        Archived
                      </span>
                    )}
                  </div>
                )}

                <span onClick={() => navigate(`/projects/${p.projectId}`)} style={{ fontSize: 13.5, color: T.muted, cursor: 'pointer' }}>
                  {p.memberCount}
                </span>
                <span onClick={() => navigate(`/projects/${p.projectId}`)} style={{ fontSize: 13.5, color: T.muted, cursor: 'pointer' }}>
                  {p.resolvedCount}
                </span>
                <span
                  onClick={() => navigate(`/projects/${p.projectId}`)}
                  style={{ fontSize: 13, color: routing.empty ? T.faint : T.muted, cursor: 'pointer', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}
                >
                  {routing.label}
                </span>

                {/* row menu */}
                <div style={{ position: 'relative', justifySelf: 'end' }} ref={openMenuFor === p.projectId ? menuRef : undefined}>
                  <button
                    onClick={() => setOpenMenuFor(o => (o === p.projectId ? null : p.projectId))}
                    style={{ ...iconBtnStyle, width: 30, height: 30 }}
                    aria-label="Project actions"
                  >
                    <Icon name="dots" size={16} color={T.subtle} />
                  </button>
                  {openMenuFor === p.projectId && (
                    <div
                      style={{
                        position: 'absolute',
                        top: 34,
                        right: 0,
                        width: 200,
                        background: T.surface,
                        border: `1px solid ${T.border}`,
                        borderRadius: T.rMd,
                        boxShadow: T.shadowModal,
                        padding: 4,
                        zIndex: 40,
                      }}
                    >
                      <button onClick={() => startRename(p)} style={menuItemStyle}>
                        <Icon name="edit" size={14} color={T.subtle} />
                        Rename
                      </button>
                      <button
                        onClick={() => void handleToggleArchive(p)}
                        disabled={archivingId === p.projectId}
                        style={menuItemStyle}
                      >
                        {archivingId === p.projectId ? <Spinner size={13} /> : <Icon name={p.archived ? 'refresh' : 'archive'} size={14} color={T.subtle} />}
                        {p.archived ? 'Reactivate' : 'Archive'}
                      </button>
                      <div style={{ height: 1, background: T.borderSubtle, margin: '4px 0' }} />
                      <button
                        onClick={() => {
                          setOpenMenuFor(null)
                          setDeleteTarget(p)
                        }}
                        style={{ ...menuItemStyle, color: T.dangerInk }}
                      >
                        <Icon name="trash" size={14} color={T.dangerInk} />
                        Delete…
                      </button>
                    </div>
                  )}
                </div>
              </div>
            )
          })}
        </div>
      ) : (
        <div style={{ background: T.surface, border: `1px solid ${T.border}`, borderRadius: 16, padding: '56px 24px', textAlign: 'center' }}>
          <div style={{ fontSize: 15, fontWeight: 600, color: T.text }}>
            {projects.length === 0 ? 'No projects yet' : 'No projects match your search'}
          </div>
          <div style={{ fontSize: 13, color: T.faint, marginTop: 5 }}>
            {projects.length === 0 ? 'Create your first project to get started.' : 'Try a different search.'}
          </div>
        </div>
      )}

      {/* create modal */}
      <Modal
        open={createOpen}
        onClose={() => setCreateOpen(false)}
        title="New project"
        subtitle="Projects group members and conversations."
        footer={
          <>
            <Button variant="secondary" onClick={() => setCreateOpen(false)}>
              Cancel
            </Button>
            <Button variant="primary" loading={creating} disabled={!createName.trim()} onClick={() => void handleCreate()}>
              Create project
            </Button>
          </>
        }
      >
        <input
          autoFocus
          value={createName}
          onChange={e => setCreateName(e.target.value)}
          onKeyDown={e => e.key === 'Enter' && handleCreate()}
          placeholder="Project name"
          style={{ height: 44, width: '100%', boxSizing: 'border-box', border: `1px solid ${T.borderStrong}`, borderRadius: 10, padding: '0 14px', fontSize: 14, color: T.text, background: T.surface, fontFamily: 'inherit' }}
        />
      </Modal>

      {/* delete modal */}
      <DeleteProjectModal
        open={deleteTarget != null}
        projectName={deleteTarget?.name ?? ''}
        onClose={() => setDeleteTarget(null)}
        onArchiveInstead={() => void handleArchiveInsteadFromModal()}
        onConfirmDelete={() => void handleConfirmDelete()}
        archiving={deleteTarget != null && archivingId === deleteTarget.projectId}
        deleting={deleting}
      />
    </Page>
  )
}

const iconBtnStyle: React.CSSProperties = {
  display: 'inline-flex',
  alignItems: 'center',
  justifyContent: 'center',
  width: 28,
  height: 28,
  border: 'none',
  background: 'transparent',
  borderRadius: 8,
  cursor: 'pointer',
  flex: 'none',
}

const menuItemStyle: React.CSSProperties = {
  display: 'flex',
  alignItems: 'center',
  gap: 9,
  width: '100%',
  padding: '8px 10px',
  border: 'none',
  background: 'transparent',
  borderRadius: 7,
  cursor: 'pointer',
  fontSize: 13,
  color: T.body,
  fontFamily: 'inherit',
  textAlign: 'left',
}
