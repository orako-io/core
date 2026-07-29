// SPDX-License-Identifier: AGPL-3.0-or-later
//
// ConnectionsPage — the MCP clients connected to the caller's account (one
// OAuth grant per row: grant_id groups the access+refresh pair and every pair
// produced by rotating it). Revoke disconnects the client immediately.
//
// This replaces the old agent-token-based Connections page. Personal agent
// tokens (orak_…) and their CLI login path have since been removed entirely
// (the orako CLI, their only consumer, is gone) — every connection here is
// an OAuth grant.

import { useEffect, useState } from 'react'
import { useNavigate } from 'react-router-dom'

import { Page } from '../components/Layout'
import { Button } from '../components/Button'
import { Icon } from '../components/Icon'
import { Modal } from '../components/Modal'
import { api, type McpConnection } from '../lib/client'
import { useIdentity } from '../lib/identity'
import { useToast, toastMessage } from '../lib/toast'
import { T } from '../lib/theme'

function timeAgo(iso?: string) {
  if (!iso) return ''
  const s = Math.floor((Date.now() - new Date(iso).getTime()) / 1000)
  if (s < 60) return 'just now'
  const m = Math.floor(s / 60)
  if (m < 60) return `${m}m ago`
  const h = Math.floor(m / 60)
  if (h < 24) return `${h}h ago`
  const d = Math.floor(h / 24)
  return d === 1 ? 'yesterday' : `${d}d ago`
}

// A confirm intent: revoke one connection, or all of them. Held in state so the
// modal opens instantly on click — the confirm is pure setState, never a
// blocking native window.confirm() (which froze the UI behind a native dialog).
type Confirm = { kind: 'one'; conn: McpConnection } | { kind: 'all' }

export function ConnectionsPage() {
  const navigate = useNavigate()
  const toast = useToast()
  const { projects } = useIdentity()

  const [connections, setConnections] = useState<McpConnection[]>([])
  const [loaded, setLoaded] = useState(false)
  const [confirm, setConfirm] = useState<Confirm | null>(null)
  const [working, setWorking] = useState(false)
  // Sequential revoke-all progress, shown inside the confirm modal.
  const [progress, setProgress] = useState<{ done: number; total: number } | null>(null)

  async function refresh() {
    const r = await api.listMcpConnections()
    setConnections(r.connections ?? [])
  }

  useEffect(() => {
    void (async () => {
      try {
        await refresh()
      } catch (e) {
        toast.error(toastMessage(e))
      } finally {
        setLoaded(true)
      }
    })()
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])

  async function revokeOne(c: McpConnection) {
    setWorking(true)
    try {
      await api.revokeMcpConnection(c.grantId)
      toast.success('Connection revoked')
      await refresh()
      setConfirm(null)
    } catch (e) {
      toast.error(toastMessage(e))
    } finally {
      setWorking(false)
    }
  }

  // Frontend-only bulk revoke: loop the existing RPC sequentially. N is small
  // (a handful of clients), so a bulk RPC isn't worth the wire break; the
  // per-item progress keeps it honest.
  async function revokeAll() {
    const list = connections
    setWorking(true)
    setProgress({ done: 0, total: list.length })
    let failed = 0
    for (let i = 0; i < list.length; i++) {
      try {
        await api.revokeMcpConnection(list[i].grantId)
      } catch {
        failed++
      }
      setProgress({ done: i + 1, total: list.length })
    }
    try {
      await refresh()
    } catch {
      /* refetch failure is non-fatal — the next load reconciles */
    }
    setWorking(false)
    setProgress(null)
    setConfirm(null)
    if (failed === 0) toast.success('All connections revoked')
    else toast.error(`${failed} of ${list.length} could not be revoked`)
  }

  const projectName = (id: string) => projects.find(p => p.id === id)?.name ?? `${id.slice(0, 8)}…`

  return (
    <Page width={760}>
      <Header
        onConnect={() => navigate('/connect-agent')}
        onRevokeAll={connections.length > 0 ? () => setConfirm({ kind: 'all' }) : undefined}
        busy={working}
      />

      <div style={{ marginTop: 22, display: 'flex', flexDirection: 'column', gap: 12 }}>
        {connections.map(c => (
          <ConnectionCard
            key={c.grantId}
            connection={c}
            projects={projects}
            projectName={projectName}
            disabled={working}
            onRevoke={() => setConfirm({ kind: 'one', conn: c })}
          />
        ))}
      </div>

      {loaded && connections.length === 0 && (
        <div
          style={{
            marginTop: 22,
            border: `1px dashed ${T.borderStrong}`,
            borderRadius: T.rMd,
            padding: '28px 22px',
            textAlign: 'center',
          }}
        >
          <p style={{ fontSize: 14, color: T.body, margin: '0 0 14px' }}>No agents connected yet.</p>
          <Button variant="primary" onClick={() => navigate('/connect-agent')}>
            <Icon name="plus" size={15} color="#fff" strokeWidth={2.4} />
            Connect an agent
          </Button>
        </div>
      )}

      {confirm && (
        <Modal
          open
          onClose={() => { if (!working) setConfirm(null) }}
          title={confirm.kind === 'all' ? 'Revoke all connections' : 'Revoke connection'}
        >
          {confirm.kind === 'all' ? (
            <p style={{ fontSize: 13.5, color: T.body, lineHeight: 1.55, margin: 0 }}>
              Revoke all <strong>{connections.length}</strong> connected clients? Every one stops working
              immediately and will need to reconnect.
            </p>
          ) : (
            <p style={{ fontSize: 13.5, color: T.body, lineHeight: 1.55, margin: 0 }}>
              Revoke access for <strong>{confirm.conn.clientName || 'this client'}</strong>? It stops working
              immediately.
            </p>
          )}
          {progress && (
            <div style={{ fontSize: 12.5, color: T.faint, marginTop: 12 }}>
              Revoking… {progress.done} of {progress.total}
            </div>
          )}
          <div style={{ display: 'flex', justifyContent: 'flex-end', gap: 10, marginTop: 20 }}>
            <Button variant="secondary" disabled={working} onClick={() => setConfirm(null)}>Cancel</Button>
            <Button
              variant="primary"
              loading={working}
              onClick={() => void (confirm.kind === 'all' ? revokeAll() : revokeOne(confirm.conn))}
              style={{ background: '#DC2626' }}
            >
              {confirm.kind === 'all' ? 'Revoke all' : 'Revoke'}
            </Button>
          </div>
        </Modal>
      )}
    </Page>
  )
}

function Header({
  onConnect,
  onRevokeAll,
  busy,
}: {
  onConnect: () => void
  onRevokeAll?: () => void
  busy: boolean
}) {
  return (
    <div style={{ display: 'flex', alignItems: 'flex-start', justifyContent: 'space-between', gap: 16 }}>
      <p style={{ fontSize: 14, lineHeight: 1.55, color: T.muted, margin: 0, maxWidth: 460 }}>
        The MCP clients connected to your account and the projects each one can reach. Revoke a connection to
        disconnect it immediately.
      </p>
      <div style={{ display: 'flex', alignItems: 'center', gap: 10, flex: 'none' }}>
        {onRevokeAll && (
          <Button variant="secondary" size="sm" disabled={busy} onClick={onRevokeAll}>
            Revoke all
          </Button>
        )}
        <Button variant="primary" size="sm" onClick={onConnect}>
          <Icon name="plus" size={15} color="#fff" strokeWidth={2.4} />
          Connect agent
        </Button>
      </div>
    </div>
  )
}

function ConnectionCard({
  connection,
  projects,
  projectName,
  disabled,
  onRevoke,
}: {
  connection: McpConnection
  projects: { id: string; name: string }[]
  projectName: (id: string) => string
  disabled: boolean
  onRevoke: () => void
}) {
  const scope = connection.projectIds ?? []
  // Resolve each scoped id to a LIVE project. Ids that no longer resolve are
  // deleted projects still referenced in the grant — hide them entirely rather
  // than leak a raw uuid. The primary/star belongs to index 0, kept only if it
  // still resolves.
  const resolved = scope
    .map((id, i) => ({ id, name: projects.find(p => p.id === id)?.name, primary: i === 0 }))
    .filter((x): x is { id: string; name: string; primary: boolean } => x.name !== undefined)

  return (
    <div
      style={{
        border: `1px solid ${T.borderSubtle}`,
        borderRadius: T.rMd,
        background: T.surfaceAlt,
        padding: '14px 16px',
      }}
    >
      <div style={{ display: 'flex', alignItems: 'center', gap: 10 }}>
        <span
          style={{
            fontSize: 13.5,
            fontWeight: 600,
            color: T.body,
            flex: 1,
            overflow: 'hidden',
            textOverflow: 'ellipsis',
            whiteSpace: 'nowrap',
          }}
        >
          {connection.clientName || 'Unnamed client'}
        </span>
        <Button variant="secondary" size="sm" style={{ flex: 'none' }} disabled={disabled} onClick={onRevoke}>
          Revoke
        </Button>
      </div>

      <div style={{ fontSize: 12, color: T.faint, marginTop: 8 }}>
        Connected {connection.connectedAt ? new Date(connection.connectedAt).toLocaleDateString() : '—'}
        {' · '}
        Last used {connection.lastUsedAt ? timeAgo(connection.lastUsedAt) : '—'}
      </div>

      <div style={{ display: 'flex', alignItems: 'center', flexWrap: 'wrap', gap: 6, marginTop: 11 }}>
        <span style={{ fontSize: 12, color: T.faint, marginRight: 2 }}>Scope</span>
        {scope.length === 0 ? (
          <ScopeChip label="All projects" />
        ) : resolved.length === 0 ? (
          <span style={{ fontSize: 12, color: T.faint }}>No live projects</span>
        ) : (
          resolved.map(r => <ScopeChip key={r.id} label={projectName(r.id)} primary={r.primary} />)
        )}
      </div>
    </div>
  )
}

function ScopeChip({ label, primary = false }: { label: string; primary?: boolean }) {
  return (
    <span
      title={primary ? 'Default project (used when a tool omits project_id)' : undefined}
      style={{
        display: 'inline-flex',
        alignItems: 'center',
        gap: 5,
        height: 24,
        padding: '0 9px',
        borderRadius: 7,
        border: `1px solid ${primary ? T.accentBorder : T.borderStrong}`,
        background: primary ? T.accentSofter : T.bg,
        color: primary ? T.accentInk : T.body,
        fontSize: 12,
        fontWeight: primary ? 600 : 500,
      }}
    >
      {label}
      {primary && <span style={{ fontSize: 11 }}>★</span>}
    </span>
  )
}
