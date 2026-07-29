// Sidebar project switcher: lists the caller's projects, switches the active
// one (which scopes the whole dashboard — members and conversations), and lets
// an admin create a new project inline. Projects are the segmentation boundary:
// a teammate can belong to some projects and not others (per-project members).

import { useEffect, useRef, useState } from 'react'
import { api } from '../lib/client'
import { useIdentity } from '../lib/identity'
import { useToast, toastMessage } from '../lib/toast'
import { Icon } from './Icon'
import { T } from '../lib/theme'
import { useEdition, atCap } from '../lib/edition'
import { UPGRADE_URL } from './UpgradeCard'

export function ProjectSwitcher() {
  const { projects, selectedProjectId, setSelectedProjectId, isOrgAdmin, refresh } = useIdentity()
  const toast = useToast()
  const [open, setOpen] = useState(false)
  const [creating, setCreating] = useState(false)
  const [name, setName] = useState('')
  const [busy, setBusy] = useState(false)
  const ref = useRef<HTMLDivElement>(null)
  const { data: edition } = useEdition()
  const projectsCapped = atCap(edition, 'projects', projects.length)

  useEffect(() => {
    if (!open) return
    function onDoc(e: MouseEvent) {
      if (ref.current && !ref.current.contains(e.target as Node)) {
        setOpen(false)
        setCreating(false)
      }
    }
    document.addEventListener('mousedown', onDoc)
    return () => document.removeEventListener('mousedown', onDoc)
  }, [open])

  const selected = projects.find(p => p.id === selectedProjectId)

  function pick(id: string) {
    setSelectedProjectId(id)
    setOpen(false)
  }

  async function create() {
    const trimmed = name.trim()
    if (!trimmed || busy) return
    setBusy(true)
    try {
      const res = await api.createProject(trimmed)
      await refresh()
      setSelectedProjectId(res.projectId)
      toast.success(`Created project ${trimmed}`)
      setName('')
      setCreating(false)
      setOpen(false)
    } catch (err) {
      toast.error(toastMessage(err))
    } finally {
      setBusy(false)
    }
  }

  // Nothing to switch and can't create: don't render the control at all.
  if (projects.length === 0 && !isOrgAdmin) return null

  return (
    <div ref={ref} style={{ position: 'relative', marginBottom: 14 }}>
      <div style={labelStyle}>Project</div>
      <button onClick={() => setOpen(o => !o)} style={triggerStyle}>
        <span
          style={{
            overflow: 'hidden',
            textOverflow: 'ellipsis',
            whiteSpace: 'nowrap',
            color: selected ? T.body : T.subtle,
          }}
        >
          {selected?.name ?? 'Select a project'}
        </span>
        <Icon name="chevronDown" size={14} strokeWidth={2} color={T.faint} style={{ marginLeft: 'auto', flex: 'none' }} />
      </button>

      {open && (
        <div style={menuStyle}>
          {projects.map(p => (
            <button key={p.id} onClick={() => pick(p.id)} style={itemStyle}>
              <span style={{ overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>{p.name}</span>
              {p.id === selectedProjectId && (
                <Icon name="check" size={14} strokeWidth={2.4} color={T.accent} style={{ marginLeft: 'auto', flex: 'none' }} />
              )}
            </button>
          ))}

          {isOrgAdmin && (
            <>
              <div style={{ height: 1, background: T.border, margin: '4px 0' }} />
              {creating ? (
                <div style={{ padding: '4px 6px' }}>
                  <input
                    autoFocus
                    value={name}
                    onChange={e => setName(e.target.value)}
                    onKeyDown={e => {
                      if (e.key === 'Enter') void create()
                      if (e.key === 'Escape') setCreating(false)
                    }}
                    placeholder="Project name"
                    style={inputStyle}
                  />
                  <button onClick={() => void create()} disabled={busy || !name.trim()} style={createBtnStyle}>
                    {busy ? 'Creating…' : 'Create project'}
                  </button>
                </div>
              ) : projectsCapped ? (
                <div style={{ padding: '7px 9px', display: 'flex', flexDirection: 'column', gap: 4 }}>
                  <div style={{ display: 'flex', alignItems: 'center', gap: 6, fontSize: 12, color: T.subtle }}>
                    <Icon name="lock" size={13} color={T.faint} />
                    1 project on Community
                  </div>
                  <a href={UPGRADE_URL} target="_blank" rel="noreferrer" style={{ fontSize: 12.5, fontWeight: 600, color: T.accent, textDecoration: 'none' }}>
                    Upgrade for unlimited →
                  </a>
                </div>
              ) : (
                <button onClick={() => setCreating(true)} style={{ ...itemStyle, color: T.accent, fontWeight: 600 }}>
                  + New project
                </button>
              )}
            </>
          )}
        </div>
      )}
    </div>
  )
}

const labelStyle: React.CSSProperties = {
  fontSize: 10.5,
  fontWeight: 600,
  letterSpacing: '.06em',
  textTransform: 'uppercase',
  color: T.faint,
  margin: '0 2px 5px',
}

const triggerStyle: React.CSSProperties = {
  display: 'flex',
  alignItems: 'center',
  gap: 8,
  width: '100%',
  height: 34,
  padding: '0 10px',
  borderRadius: T.rMd,
  border: '1px solid #EAECEF',
  background: T.surface,
  cursor: 'pointer',
  fontSize: 13,
  fontWeight: 600,
  fontFamily: 'inherit',
}

const menuStyle: React.CSSProperties = {
  position: 'absolute',
  top: 58,
  left: 0,
  right: 0,
  background: T.surface,
  border: `1px solid ${T.border}`,
  borderRadius: T.rMd,
  boxShadow: T.shadowModal,
  padding: 4,
  zIndex: 30,
  maxHeight: 280,
  overflowY: 'auto',
}

const itemStyle: React.CSSProperties = {
  display: 'flex',
  alignItems: 'center',
  gap: 8,
  width: '100%',
  padding: '7px 9px',
  border: 'none',
  background: 'transparent',
  borderRadius: 7,
  cursor: 'pointer',
  fontSize: 13,
  color: T.body,
  fontFamily: 'inherit',
  textAlign: 'left',
}

const inputStyle: React.CSSProperties = {
  width: '100%',
  height: 32,
  padding: '0 9px',
  border: `1px solid ${T.border}`,
  borderRadius: 7,
  fontSize: 13,
  fontFamily: 'inherit',
  boxSizing: 'border-box',
  marginBottom: 6,
}

const createBtnStyle: React.CSSProperties = {
  width: '100%',
  height: 32,
  border: 'none',
  borderRadius: 7,
  background: T.accent,
  color: '#fff',
  fontSize: 12.5,
  fontWeight: 600,
  cursor: 'pointer',
  fontFamily: 'inherit',
}
