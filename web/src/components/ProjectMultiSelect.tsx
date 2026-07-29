// ProjectMultiSelect — reusable cross-project filter for Conversations.
// Controlled: value = selected project ids; an empty array
// means "All projects" (the org-wide default). Fed by useIdentity().projects
// (already non-archived).

import { useEffect, useRef, useState } from 'react'
import { useIdentity } from '../lib/identity'
import { Icon } from './Icon'
import { Button } from './Button'
import { T } from '../lib/theme'

interface Props {
  value: string[]
  onChange: (next: string[]) => void
}

function Checkbox({ checked }: { checked: boolean }) {
  return (
    <span
      style={{
        width: 17,
        height: 17,
        flex: 'none',
        borderRadius: 5,
        border: `1.5px solid ${checked ? T.accent : T.borderStrong}`,
        background: checked ? T.accent : T.surface,
        display: 'flex',
        alignItems: 'center',
        justifyContent: 'center',
      }}
    >
      {checked && <Icon name="check" size={11} color="#fff" strokeWidth={3.4} />}
    </span>
  )
}

export function ProjectMultiSelect({ value, onChange }: Props) {
  const { projects } = useIdentity()
  const [open, setOpen] = useState(false)
  const ref = useRef<HTMLDivElement>(null)

  useEffect(() => {
    if (!open) return
    function onDoc(e: MouseEvent) {
      if (ref.current && !ref.current.contains(e.target as Node)) setOpen(false)
    }
    document.addEventListener('mousedown', onDoc)
    return () => document.removeEventListener('mousedown', onDoc)
  }, [open])

  const allSelected = value.length === 0
  const triggerLabel = allSelected
    ? 'Filter projects…'
    : value.length === 1
      ? (projects.find(p => p.id === value[0])?.name ?? '1 selected')
      : `${value.length} projects`

  function toggle(id: string) {
    onChange(value.includes(id) ? value.filter(v => v !== id) : [...value, id])
  }

  return (
    <div ref={ref} style={{ position: 'relative' }}>
      <button onClick={() => setOpen(o => !o)} style={triggerStyle}>
        <Icon name="folder" size={15} color={T.subtle} />
        <span
          style={{
            overflow: 'hidden',
            textOverflow: 'ellipsis',
            whiteSpace: 'nowrap',
            color: allSelected ? T.subtle : T.body,
            fontWeight: allSelected ? 500 : 600,
          }}
        >
          {triggerLabel}
        </span>
        <Icon name="chevronDown" size={13} strokeWidth={2} color={T.faint} style={{ marginLeft: 'auto', flex: 'none' }} />
      </button>

      {open && (
        <div style={menuStyle}>
          <button onClick={() => onChange([])} style={itemStyle}>
            <Checkbox checked={allSelected} />
            <span>All projects</span>
          </button>
          {projects.length > 0 && <div style={{ height: 1, background: T.borderSubtle, margin: '4px 0' }} />}
          <div style={{ maxHeight: 240, overflowY: 'auto' }}>
            {projects.map(p => (
              <button key={p.id} onClick={() => toggle(p.id)} style={itemStyle}>
                <Checkbox checked={value.includes(p.id)} />
                <span style={{ overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>{p.name}</span>
              </button>
            ))}
          </div>
          <div style={{ height: 1, background: T.borderSubtle, margin: '4px 0' }} />
          <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', padding: '6px 9px' }}>
            <span style={{ fontSize: 12, color: T.faint }}>
              {allSelected ? 'All projects' : `${value.length} selected`}
            </span>
            <Button size="sm" variant="primary" onClick={() => setOpen(false)}>
              Apply
            </Button>
          </div>
        </div>
      )}
    </div>
  )
}

const triggerStyle: React.CSSProperties = {
  display: 'flex',
  alignItems: 'center',
  gap: 8,
  height: 36,
  padding: '0 12px',
  borderRadius: 9,
  border: `1px solid ${T.borderStrong}`,
  background: T.surface,
  cursor: 'pointer',
  fontSize: 13.5,
  fontFamily: 'inherit',
  minWidth: 180,
  maxWidth: 260,
}

const menuStyle: React.CSSProperties = {
  position: 'absolute',
  top: 42,
  left: 0,
  width: 260,
  background: T.surface,
  border: `1px solid ${T.border}`,
  borderRadius: T.rMd,
  boxShadow: T.shadowModal,
  padding: 4,
  zIndex: 30,
}

const itemStyle: React.CSSProperties = {
  display: 'flex',
  alignItems: 'center',
  gap: 9,
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
