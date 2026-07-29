// Sidebar organization switcher. Organizations are the top isolation boundary:
// different orgs (e.g. uxia / viadi / palmthree) have their own teams, projects
// and integrations. Switching sets the active org header (Orako-Org-Id) that the
// server honors for orgs the caller participates in, then reloads so every
// scope (projects, members, settings) re-resolves under the new org.

import { useEffect, useRef, useState } from 'react'
import { api, type OrganizationSummary } from '../lib/client'
import { useToast, toastMessage } from '../lib/toast'
import { Icon } from './Icon'
import { T } from '../lib/theme'
import { useEdition, atCap } from '../lib/edition'
import { UPGRADE_URL } from './UpgradeCard'

const ACTIVE_ORG_KEY = 'orako:activeOrg'

export function OrgSwitcher({
  orgName,
  subtitle,
  collapsed = false,
}: {
  orgName: string
  subtitle: string
  collapsed?: boolean
}) {
  const [orgs, setOrgs] = useState<OrganizationSummary[]>([])
  const [open, setOpen] = useState(false)
  const [naming, setNaming] = useState(false)
  const [newName, setNewName] = useState('')
  const [busy, setBusy] = useState(false)
  const ref = useRef<HTMLDivElement>(null)
  const toast = useToast()
  const { data: edition } = useEdition()
  // Org cap is instance-wide; orgs.length (the caller's orgs) matches it on
  // Community, where only one org can exist.
  const orgsCapped = atCap(edition, 'orgs', orgs.length)

  useEffect(() => {
    api
      .listOrganizations()
      .then(res => setOrgs(res.organizations ?? []))
      .catch(() => {})
  }, [])

  useEffect(() => {
    if (!open) return
    function onDoc(e: MouseEvent) {
      if (ref.current && !ref.current.contains(e.target as Node)) setOpen(false)
    }
    document.addEventListener('mousedown', onDoc)
    return () => document.removeEventListener('mousedown', onDoc)
  }, [open])

  const current = orgs.find(o => o.isCurrent)
  const label = current?.name || orgName || 'Organization'

  function switchTo(id: string) {
    if (current?.id === id) {
      setOpen(false)
      return
    }
    // Reload so projects, members and the selected project re-resolve under the
    // new org — every subsequent request carries the new active-org header.
    localStorage.setItem(ACTIVE_ORG_KEY, id)
    window.location.reload()
  }

  // Create a new organization (self-serve — the caller becomes its admin) and
  // switch to it. Available to anyone, not just admins of the current org: a
  // freelancer in someone else's org can spin up their own.
  async function createOrg() {
    const name = newName.trim()
    if (!name || busy) return
    setBusy(true)
    try {
      const res = await api.createOrganization(name)
      localStorage.setItem(ACTIVE_ORG_KEY, res.organizationId)
      window.location.reload()
    } catch (e) {
      // Surface the failure — a silently swallowed error here reads as
      // "creating an organization does nothing".
      toast.error(toastMessage(e))
      setBusy(false)
    }
  }

  return (
    <div ref={ref} style={{ position: 'relative', marginBottom: 18 }}>
      <button
        onClick={() => setOpen(o => !o)}
        title={collapsed ? label : undefined}
        style={{
          display: 'flex',
          alignItems: 'center',
          justifyContent: collapsed ? 'center' : 'flex-start',
          gap: collapsed ? 0 : 10,
          padding: collapsed ? '8px 0' : '8px 10px',
          borderRadius: T.rMd,
          border: '1px solid #EAECEF',
          background: T.surfaceAlt,
          cursor: 'pointer',
          width: '100%',
          fontFamily: 'inherit',
        }}
      >
        <div
          style={{
            width: 26,
            height: 26,
            borderRadius: 8,
            background: T.accentSoft,
            color: T.accent,
            display: 'flex',
            alignItems: 'center',
            justifyContent: 'center',
            fontSize: 13,
            fontWeight: 700,
            flex: 'none',
          }}
        >
          {label.charAt(0).toUpperCase()}
        </div>
        {!collapsed && (
          <>
            <div style={{ display: 'flex', flexDirection: 'column', alignItems: 'flex-start', lineHeight: 1.25, minWidth: 0 }}>
              <span
                style={{
                  fontSize: 13.5,
                  fontWeight: 600,
                  color: T.body,
                  overflow: 'hidden',
                  textOverflow: 'ellipsis',
                  whiteSpace: 'nowrap',
                  maxWidth: 150,
                }}
              >
                {label}
              </span>
              <span style={{ fontSize: 11, color: T.subtle }}>{subtitle}</span>
            </div>
            <Icon name="chevronDown" size={15} strokeWidth={2} color={T.faint} style={{ marginLeft: 'auto', flex: 'none' }} />
          </>
        )}
      </button>

      {open && (
        <div
          style={{
            position: 'absolute',
            top: 52,
            left: 0,
            width: collapsed ? 236 : undefined,
            right: collapsed ? undefined : 0,
            background: T.surface,
            border: `1px solid ${T.border}`,
            borderRadius: T.rMd,
            boxShadow: T.shadowModal,
            padding: 4,
            zIndex: 30,
            maxHeight: 280,
            overflowY: 'auto',
          }}
        >
          {orgs.map(o => (
            <button
              key={o.id}
              onClick={() => switchTo(o.id)}
              style={{
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
              }}
            >
              <span style={{ overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>{o.name}</span>
              {o.isCurrent && (
                <Icon name="check" size={14} strokeWidth={2.4} color={T.accent} style={{ marginLeft: 'auto', flex: 'none' }} />
              )}
            </button>
          ))}

          {orgs.length > 0 && <div style={{ height: 1, background: T.borderSubtle, margin: '4px 6px' }} />}

          {naming ? (
            <div style={{ display: 'flex', alignItems: 'center', gap: 6, padding: '4px 6px' }}>
              <input
                autoFocus
                value={newName}
                onChange={e => setNewName(e.target.value)}
                onKeyDown={e => {
                  if (e.key === 'Enter') void createOrg()
                  if (e.key === 'Escape') {
                    setNaming(false)
                    setNewName('')
                  }
                }}
                placeholder="Organization name"
                disabled={busy}
                style={{
                  flex: 1,
                  minWidth: 0,
                  height: 30,
                  border: `1px solid ${T.border}`,
                  borderRadius: 7,
                  padding: '0 9px',
                  fontSize: 13,
                  fontFamily: 'inherit',
                  color: T.body,
                  outline: 'none',
                }}
              />
              <button
                onClick={() => void createOrg()}
                disabled={busy || !newName.trim()}
                style={{
                  height: 30,
                  padding: '0 10px',
                  border: 'none',
                  borderRadius: 7,
                  background: T.accent,
                  color: '#fff',
                  fontSize: 12.5,
                  fontWeight: 600,
                  cursor: 'pointer',
                  fontFamily: 'inherit',
                  opacity: busy || !newName.trim() ? 0.6 : 1,
                }}
              >
                {busy ? '…' : 'Create'}
              </button>
            </div>
          ) : orgsCapped ? (
            <div style={{ padding: '8px 9px', display: 'flex', flexDirection: 'column', gap: 5 }}>
              <div style={{ display: 'flex', alignItems: 'center', gap: 6, fontSize: 12, color: T.subtle }}>
                <Icon name="lock" size={13} color={T.faint} />
                1 organization on Community
              </div>
              <a
                href={UPGRADE_URL}
                target="_blank"
                rel="noreferrer"
                style={{ fontSize: 12.5, fontWeight: 600, color: T.accent, textDecoration: 'none' }}
              >
                Upgrade for unlimited orgs →
              </a>
            </div>
          ) : (
            <button
              onClick={() => setNaming(true)}
              style={{
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
                fontWeight: 500,
                color: T.accent,
                fontFamily: 'inherit',
                textAlign: 'left',
              }}
            >
              <Icon name="plus" size={14} strokeWidth={2.4} color={T.accent} />
              New organization
            </button>
          )}
        </div>
      )}
    </div>
  )
}
