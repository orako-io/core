// SPDX-License-Identifier: AGPL-3.0-or-later
//
// MachineTokensPage — org-admin settings page for headless agent credentials
// (phase 1: non-interactive machine tokens, non_interactive-machine-auth).
// Mint, list, and revoke long-lived scoped tokens without a browser.
// List/Revoke are org-scoped server-side: any org admin sees and can revoke
// every machine token in the org, not just the ones they personally minted —
// a machine token is org infrastructure, not a personal session.

import { useEffect, useState } from 'react'
import { Page } from '../components/Layout'
import { Button } from '../components/Button'
import { Icon } from '../components/Icon'
import { Input } from '../components/Input'
import { Modal } from '../components/Modal'
import { ProjectMultiSelect } from '../components/ProjectMultiSelect'
import { api, type MachineToken, type ProjectSummary } from '../lib/client'
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

export function MachineTokensPage() {
  const toast = useToast()
  const { projects } = useIdentity()

  const [tokens, setTokens] = useState<MachineToken[]>([])
  const [loaded, setLoaded] = useState(false)

  const [createOpen, setCreateOpen] = useState(false)
  const [label, setLabel] = useState('')
  const [scope, setScope] = useState<string[]>([])
  const [creating, setCreating] = useState(false)

  // The raw secret, held ONLY long enough to show it once — never persisted,
  // never re-fetchable (ListMachineTokens never returns it).
  const [secretModal, setSecretModal] = useState<{ label: string; secret: string } | null>(null)
  const [copied, setCopied] = useState(false)

  const [confirmRevoke, setConfirmRevoke] = useState<MachineToken | null>(null)
  const [revoking, setRevoking] = useState(false)

  async function refresh() {
    const r = await api.listMachineTokens()
    setTokens(r.tokens ?? [])
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

  function openCreate() {
    setLabel('')
    setScope([])
    setCreateOpen(true)
  }

  async function createToken() {
    const trimmed = label.trim()
    if (!trimmed) return
    setCreating(true)
    try {
      const res = await api.createMachineToken(trimmed, scope)
      setCreateOpen(false)
      setCopied(false)
      setSecretModal({ label: res.token.label, secret: res.secret })
      await refresh()
    } catch (e) {
      toast.error(toastMessage(e))
    } finally {
      setCreating(false)
    }
  }

  async function copySecret() {
    if (!secretModal) return
    try {
      await navigator.clipboard.writeText(secretModal.secret)
      setCopied(true)
      toast.success('Copied')
    } catch {
      toast.error('Copy failed')
    }
  }

  async function revokeToken(tok: MachineToken) {
    setRevoking(true)
    try {
      await api.revokeMachineToken(tok.id)
      toast.success('Token revoked')
      await refresh()
      setConfirmRevoke(null)
    } catch (e) {
      toast.error(toastMessage(e))
    } finally {
      setRevoking(false)
    }
  }

  const projectName = (id: string) => projects.find(p => p.id === id)?.name ?? `${id.slice(0, 8)}…`

  return (
    <Page width={760}>
      <div style={{ display: 'flex', alignItems: 'flex-start', justifyContent: 'space-between', gap: 16 }}>
        <p style={{ fontSize: 14, lineHeight: 1.55, color: T.muted, margin: 0, maxWidth: 460 }}>
          Long-lived tokens for headless agents — CI pipelines, scheduled jobs, anything that can&rsquo;t sit through
          an interactive login. Scope one to a handful of projects, or leave it unscoped to reach every project the
          token can.
        </p>
        <Button variant="primary" size="sm" onClick={openCreate}>
          <Icon name="plus" size={15} color="#fff" strokeWidth={2.4} />
          Create token
        </Button>
      </div>

      <div style={{ marginTop: 22, display: 'flex', flexDirection: 'column', gap: 12 }}>
        {tokens.map(tok => (
          <TokenCard
            key={tok.id}
            token={tok}
            projects={projects}
            projectName={projectName}
            disabled={revoking}
            onRevoke={() => setConfirmRevoke(tok)}
          />
        ))}
      </div>

      {loaded && tokens.length === 0 && (
        <div
          style={{
            marginTop: 22,
            border: `1px dashed ${T.borderStrong}`,
            borderRadius: T.rMd,
            padding: '28px 22px',
            textAlign: 'center',
          }}
        >
          <p style={{ fontSize: 14, color: T.body, margin: '0 0 14px' }}>No machine tokens yet.</p>
          <Button variant="primary" onClick={openCreate}>
            <Icon name="plus" size={15} color="#fff" strokeWidth={2.4} />
            Create token
          </Button>
        </div>
      )}

      {/* Create form: label + project scope. */}
      <Modal
        open={createOpen}
        onClose={() => { if (!creating) setCreateOpen(false) }}
        title="Create machine token"
        subtitle="A long-lived credential for a headless agent. The secret is shown once, right after you create it."
      >
        <div style={{ display: 'flex', flexDirection: 'column', gap: 16 }}>
          <Input
            label="Label"
            placeholder="e.g. CI pipeline"
            value={label}
            onChange={e => setLabel(e.target.value)}
            autoFocus
          />
          <div>
            <label style={{ display: 'block', fontSize: 13, fontWeight: 500, color: '#3A414D', marginBottom: 6 }}>
              Project scope
            </label>
            <ProjectMultiSelect value={scope} onChange={setScope} />
            <div style={{ marginTop: 6, fontSize: 12.5, color: T.subtle }}>
              Leave empty for unscoped — the token reaches every project it can.
            </div>
          </div>
        </div>
        <div style={{ display: 'flex', justifyContent: 'flex-end', gap: 10, marginTop: 20 }}>
          <Button variant="secondary" disabled={creating} onClick={() => setCreateOpen(false)}>
            Cancel
          </Button>
          <Button variant="primary" loading={creating} disabled={!label.trim()} onClick={() => void createToken()}>
            Create token
          </Button>
        </div>
      </Modal>

      {/* The raw secret, shown exactly once. Never re-readable after this — the
          List RPC only ever returns metadata. */}
      <Modal
        open={secretModal !== null}
        onClose={() => setSecretModal(null)}
        title="Token created"
        subtitle={
          secretModal
            ? `“${secretModal.label}” is ready. Copy the secret now — you won’t see it again.`
            : undefined
        }
      >
        {secretModal && (
          <>
            <div
              style={{
                display: 'flex',
                alignItems: 'center',
                gap: 10,
                background: T.bg,
                border: `1px solid ${T.border}`,
                borderRadius: T.rMd,
                padding: '11px 13px',
              }}
            >
              <code
                style={{
                  flex: 1,
                  fontFamily: T.mono,
                  fontSize: 12.5,
                  color: T.body,
                  wordBreak: 'break-all',
                  lineHeight: 1.5,
                }}
              >
                {secretModal.secret}
              </code>
              <Button variant="secondary" size="sm" style={{ flex: 'none' }} onClick={() => void copySecret()}>
                <Icon name="copy" size={14} color={T.muted} />
                {copied ? 'Copied' : 'Copy'}
              </Button>
            </div>
            <div
              style={{
                display: 'flex',
                alignItems: 'flex-start',
                gap: 9,
                marginTop: 14,
                padding: '11px 13px',
                background: T.warnBg,
                border: `1px solid ${T.warnBorder}`,
                borderRadius: T.rMd,
              }}
            >
              <Icon
                name="alertTriangle"
                size={15}
                color={T.warn}
                strokeWidth={2}
                style={{ flex: 'none', marginTop: 1 }}
              />
              <p style={{ fontSize: 12.5, lineHeight: 1.5, color: T.warn, margin: 0 }}>
                This is the only time the secret is shown. Store it in your agent&rsquo;s config now — Orako never
                displays it again.
              </p>
            </div>
          </>
        )}
        <div style={{ display: 'flex', justifyContent: 'flex-end', marginTop: 20 }}>
          <Button variant="primary" onClick={() => setSecretModal(null)}>
            Done
          </Button>
        </div>
      </Modal>

      {confirmRevoke && (
        <Modal open onClose={() => { if (!revoking) setConfirmRevoke(null) }} title="Revoke machine token">
          <p style={{ fontSize: 13.5, color: T.body, lineHeight: 1.55, margin: 0 }}>
            Revoke <strong>{confirmRevoke.label || 'this token'}</strong>? Any agent using it stops working
            immediately.
          </p>
          <div style={{ display: 'flex', justifyContent: 'flex-end', gap: 10, marginTop: 20 }}>
            <Button variant="secondary" disabled={revoking} onClick={() => setConfirmRevoke(null)}>
              Cancel
            </Button>
            <Button
              variant="primary"
              loading={revoking}
              onClick={() => void revokeToken(confirmRevoke)}
              style={{ background: '#DC2626' }}
            >
              Revoke
            </Button>
          </div>
        </Modal>
      )}
    </Page>
  )
}

function TokenCard({
  token,
  projects,
  projectName,
  disabled,
  onRevoke,
}: {
  token: MachineToken
  projects: ProjectSummary[]
  projectName: (id: string) => string
  disabled: boolean
  onRevoke: () => void
}) {
  const scope = token.projectIds ?? []
  // Resolve each scoped id to a LIVE project. Ids that no longer resolve are
  // deleted projects still referenced in the token — hide them entirely
  // rather than leak a raw uuid. The primary/star belongs to index 0, kept
  // only if it still resolves.
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
          {token.label || 'Unnamed token'}
        </span>
        <Button variant="secondary" size="sm" style={{ flex: 'none' }} disabled={disabled} onClick={onRevoke}>
          Revoke
        </Button>
      </div>

      <div style={{ fontSize: 12, color: T.faint, marginTop: 8 }}>
        Created {token.createdAt ? new Date(token.createdAt).toLocaleDateString() : '—'}
        {' · '}
        Last used {token.lastUsedAt ? timeAgo(token.lastUsedAt) : 'never'}
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
