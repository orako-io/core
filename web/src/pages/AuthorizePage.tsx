// OAuth `/authorize` dashboard SPA route — the seamless replacement for
// phase-2's server-rendered "paste your session bearer" form. Reached when
// an MCP client (Claude Code, Cursor, another coding agent) opens
// `{origin}/authorize?response_type=code&client_id=…&code_challenge=…
// &code_challenge_method=S256&redirect_uri=…&state=…&scope=…&resource=…`
// in the user's browser. The page runs behind RequireToken (App.tsx), so the
// browser's existing Supabase/dev session authenticates the human; there is
// never a bearer to copy-paste.
//
// Approve/Deny both call the authenticated `POST /oauth/authorize/approve`
// endpoint (internal/infra/transport/oauth/authorize.go): it re-validates
// client_id/redirect_uri/PKCE/resource (never duplicated client-side) before
// ever building a redirect target, deny included — an unregistered
// redirect_uri must never be rewarded with a browser navigation, even an
// error one. The endpoint returns `{ redirect }`; this page performs the
// actual navigation via window.location.assign.

import { useEffect, useMemo, useState } from 'react'
import { useSearchParams } from 'react-router-dom'
import { isOidc } from '../lib/auth-mode'
import { bearer } from '../lib/token'
import { api } from '../lib/client'
import { Button } from '../components/Button'
import { LogoTile } from '../components/Icon'
import { T } from '../lib/theme'

interface OAuthParams {
  responseType: string
  clientId: string
  redirectUri: string
  state: string
  codeChallenge: string
  codeChallengeMethod: string
  resource: string
}

function readParams(sp: URLSearchParams): OAuthParams {
  return {
    responseType: sp.get('response_type') ?? '',
    clientId: sp.get('client_id') ?? '',
    redirectUri: sp.get('redirect_uri') ?? '',
    state: sp.get('state') ?? '',
    codeChallenge: sp.get('code_challenge') ?? '',
    codeChallengeMethod: sp.get('code_challenge_method') ?? '',
    resource: sp.get('resource') ?? '',
  }
}

// Structural check only — it lets the page show "this link is not valid"
// without a round trip, but it is never a substitute for the backend's
// authoritative re-validation, which both Approve and Deny go through
// regardless.
function paramsLookSane(p: OAuthParams): boolean {
  return (
    p.responseType === 'code' &&
    p.clientId !== '' &&
    p.redirectUri !== '' &&
    p.codeChallenge !== '' &&
    p.codeChallengeMethod === 'S256'
  )
}

interface ApproveBody {
  redirect?: string
  error?: string
  error_description?: string
}

async function postApprove(
  params: OAuthParams,
  decision: 'approve' | 'deny',
  orgId: string,
  projectIds: string[],
): Promise<ApproveBody & { ok: boolean }> {
  const token = await bearer()
  const headers: HeadersInit = { 'Content-Type': 'application/json' }
  if (token) headers['Authorization'] = `Bearer ${token}`

  const resp = await fetch('/oauth/authorize/approve', {
    method: 'POST',
    headers,
    body: JSON.stringify({
      client_id: params.clientId,
      redirect_uri: params.redirectUri,
      state: params.state,
      code_challenge: params.codeChallenge,
      code_challenge_method: params.codeChallengeMethod,
      resource: params.resource,
      decision,
      org_id: orgId,
      // Ordered; the first is the primary project used when a tool omits one.
      project_ids: projectIds,
    }),
  })

  const body = (await resp.json().catch(() => ({}))) as ApproveBody

  return { ...body, ok: resp.ok }
}

const pageStyle: React.CSSProperties = {
  minHeight: '100vh',
  background: T.bg,
  display: 'flex',
  flexDirection: 'column',
  alignItems: 'center',
  justifyContent: 'center',
  padding: 24,
  fontFamily: T.sans,
}

const cardStyle: React.CSSProperties = {
  width: '100%',
  maxWidth: 440,
  background: T.surface,
  border: `1px solid ${T.border}`,
  borderRadius: 18,
  boxShadow: T.shadowModal,
  padding: '36px 32px 30px',
  textAlign: 'center',
}

function Frame({ children }: { children: React.ReactNode }) {
  return (
    <div style={pageStyle}>
      <div style={cardStyle}>
        <LogoTile />
        {children}
      </div>
    </div>
  )
}

export function AuthorizePage() {
  const [sp] = useSearchParams()
  const params = useMemo(() => readParams(sp), [sp])
  const sane = useMemo(() => paramsLookSane(params), [params])

  const [clientName, setClientName] = useState<string | null>(null)
  const [infoLoading, setInfoLoading] = useState(true)
  const [infoError, setInfoError] = useState<string | null>(null)
  const [busy, setBusy] = useState<'approve' | 'deny' | null>(null)
  const [actionError, setActionError] = useState<string | null>(null)
  const [projects, setProjects] = useState<{ id: string; name: string; orgId: string }[]>([])
  const [orgNames, setOrgNames] = useState<Record<string, string>>({})
  const [orgsLoading, setOrgsLoading] = useState(true)
  const [projectsLoading, setProjectsLoading] = useState(false)
  const [selectedOrgId, setSelectedOrgId] = useState('')
  const [selectedProjectIds, setSelectedProjectIds] = useState<string[]>([])

  useEffect(() => {
    let cancelled = false
    api.listOrganizations()
      .then(or => {
        if (cancelled) return
        const names: Record<string, string> = {}
        for (const o of or.organizations ?? []) names[o.id] = o.name
        setOrgNames(names)
        const activeOrg = localStorage.getItem('orako:activeOrg') ?? ''
        const organizations = or.organizations ?? []
        setSelectedOrgId(organizations.some(org => org.id === activeOrg) ? activeOrg : organizations[0]?.id ?? '')
      })
      .catch(() => {})
      .finally(() => {
        if (!cancelled) setOrgsLoading(false)
      })
    return () => {
      cancelled = true
    }
  }, [])

  useEffect(() => {
    if (!selectedOrgId) {
      setProjects([])
      setSelectedProjectIds([])
      return
    }
    let cancelled = false
    setActionError(null)
    setProjectsLoading(true)
    setProjects([])
    setSelectedProjectIds([])
    api
      .listProjectsForOrg(selectedOrgId)
      .then(result => {
        if (!cancelled) {
          setProjects((result.projects ?? []).map(project => ({
            id: project.id,
            name: project.name,
            orgId: selectedOrgId,
          })))
        }
      })
      .catch(() => {
        if (!cancelled) setActionError('Could not load the projects for this organization.')
      })
      .finally(() => {
        if (!cancelled) setProjectsLoading(false)
      })
    return () => {
      cancelled = true
    }
  }, [selectedOrgId])

  useEffect(() => {
    if (!sane || !isOidc) {
      setInfoLoading(false)

      return
    }

    let cancelled = false
    const q = new URLSearchParams({
      client_id: params.clientId,
      redirect_uri: params.redirectUri,
      code_challenge: params.codeChallenge,
      code_challenge_method: params.codeChallengeMethod,
    })
    if (params.resource) q.set('resource', params.resource)

    fetch(`/oauth/authorize/client?${q.toString()}`)
      .then(async r => {
        const body = (await r.json().catch(() => ({}))) as { client_name?: string; error_description?: string }
        if (cancelled) return
        if (!r.ok) {
          setInfoError(body.error_description || 'This authorization request is not valid.')

          return
        }
        setClientName(body.client_name || null)
      })
      .catch(() => {
        if (!cancelled) setInfoError('Could not reach the server to validate this request.')
      })
      .finally(() => {
        if (!cancelled) setInfoLoading(false)
      })

    return () => {
      cancelled = true
    }
  }, [sane, params.clientId, params.redirectUri, params.codeChallenge, params.codeChallengeMethod, params.resource])

  async function decide(decision: 'approve' | 'deny') {
    if (busy) return
    setBusy(decision)
    setActionError(null)

    try {
      const result = await postApprove(
        params,
        decision,
        decision === 'approve' ? selectedOrgId : '',
        decision === 'approve' ? selectedProjectIds : [],
      )
      if (!result.ok || !result.redirect) {
        setActionError(
          result.error_description || `Could not ${decision === 'approve' ? 'authorize' : 'deny'} this request.`,
        )
        setBusy(null)

        return
      }
      window.location.assign(result.redirect)
    } catch {
      setActionError('Network error — try again.')
      setBusy(null)
    }
  }

  if (!sane) {
    return (
      <Frame>
        <h1 style={{ margin: '22px 0 0', fontSize: 20, fontWeight: 700, color: T.text }}>
          This authorization link is not valid
        </h1>
        <p style={{ margin: '10px 0 0', fontSize: 14, lineHeight: 1.6, color: T.muted }}>
          It must come from a coding agent's own remote-MCP connect flow, with{' '}
          <code style={{ fontFamily: T.mono }}>response_type=code</code>, a{' '}
          <code style={{ fontFamily: T.mono }}>client_id</code>, a{' '}
          <code style={{ fontFamily: T.mono }}>redirect_uri</code>, and PKCE (
          <code style={{ fontFamily: T.mono }}>code_challenge</code> with{' '}
          <code style={{ fontFamily: T.mono }}>code_challenge_method=S256</code>). Reconnect your agent and try again.
        </p>
      </Frame>
    )
  }

  if (!isOidc) {
    return (
      <Frame>
        <h1 style={{ margin: '22px 0 0', fontSize: 20, fontWeight: 700, color: T.text }}>
          This server runs in dev mode
        </h1>
        <p style={{ margin: '10px 0 0', fontSize: 14, lineHeight: 1.6, color: T.muted }}>
          Dev-mode servers have no real sign-in, so remote MCP OAuth is not available here. Set{' '}
          <code style={{ fontFamily: T.mono, color: T.accent }}>ORAKO_AUTH_MODE</code> to <code style={{ fontFamily: T.mono, color: T.accent }}>oidc</code>{' '}
          or <code style={{ fontFamily: T.mono, color: T.accent }}>local</code> to enable remote MCP connections.
        </p>
      </Frame>
    )
  }

  if (infoLoading) {
    return (
      <Frame>
        <p style={{ margin: '22px 0 0', fontSize: 14, color: T.muted }}>Checking this request…</p>
      </Frame>
    )
  }

  if (infoError) {
    return (
      <Frame>
        <h1 style={{ margin: '22px 0 0', fontSize: 20, fontWeight: 700, color: T.text }}>
          This authorization request could not be completed
        </h1>
        <p style={{ margin: '10px 0 0', fontSize: 14, lineHeight: 1.6, color: T.muted }}>{infoError}</p>
      </Frame>
    )
  }

  const displayName = clientName || params.clientId

  return (
    <Frame>
      <h1 style={{ margin: '22px 0 0', fontSize: 21, fontWeight: 700, color: T.text, lineHeight: 1.35 }}>
        Authorize &ldquo;{displayName}&rdquo;?
      </h1>
      <p style={{ margin: '12px 0 0', fontSize: 14, lineHeight: 1.6, color: T.muted }}>
        This lets <strong>{displayName}</strong> act as you in Orako — in the projects you pick below, and only
        those. You can revoke it anytime from <strong>Connect agent</strong>.
      </p>

      {Object.keys(orgNames).length > 0 && (
      <div style={{ marginTop: 18 }}>
        {Object.keys(orgNames).length > 1 && (
          <div style={{ marginBottom: 14, textAlign: 'left' }}>
            <label htmlFor="authorize-org" style={{ display: 'block', fontSize: 13, fontWeight: 600, color: T.text, marginBottom: 8 }}>
              Which organization?
            </label>
            <select
              id="authorize-org"
              value={selectedOrgId}
              onChange={event => setSelectedOrgId(event.target.value)}
              style={{ width: '100%', height: 40, border: `1px solid ${T.borderStrong}`, borderRadius: 10, padding: '0 12px', background: T.surface, color: T.text, fontFamily: 'inherit', fontSize: 13.5 }}
            >
              {Object.entries(orgNames).map(([id, name]) => (
                <option key={id} value={id}>{name}</option>
              ))}
            </select>
          </div>
        )}
        <div style={{ fontSize: 13, fontWeight: 600, color: T.text, marginBottom: 8 }}>
          Which projects can it access?
        </div>
        {projectsLoading && <p style={{ margin: '0 0 8px', fontSize: 12.5, color: T.faint }}>Loading projects…</p>}
        <div style={{ display: 'flex', flexDirection: 'column', gap: 12 }}>
          <div>
            <div style={{ display: 'flex', justifyContent: 'flex-end', marginBottom: 6 }}>
              <button
                type="button"
                onClick={() => setSelectedProjectIds(selectedProjectIds.length === projects.length ? [] : projects.map(project => project.id))}
                style={{ border: 'none', background: 'transparent', color: T.accent, fontSize: 12, fontWeight: 600, cursor: 'pointer', fontFamily: 'inherit' }}
              >
                {projects.length > 0 && selectedProjectIds.length === projects.length ? 'Clear' : 'Select all'}
              </button>
            </div>
                <div style={{ display: 'flex', flexWrap: 'wrap', gap: 8 }}>
                  {projects.map(p => {
                    const on = selectedProjectIds.includes(p.id)
                    return (
                      <button
                        key={p.id}
                        type="button"
                        onClick={() => setSelectedProjectIds(sel => (on ? sel.filter(id => id !== p.id) : [...sel, p.id]))}
                        style={{
                          display: 'inline-flex',
                          alignItems: 'center',
                          gap: 6,
                          height: 30,
                          padding: '0 12px',
                          borderRadius: 8,
                          border: `1px solid ${on ? T.accent : T.border}`,
                          background: on ? T.accentSoft : T.surface,
                          color: on ? T.accent : T.body,
                          fontSize: 13,
                          fontWeight: on ? 600 : 500,
                          cursor: 'pointer',
                          fontFamily: 'inherit',
                        }}
                      >
                        {p.name}
                      </button>
                    )
                  })}
                </div>
          </div>
        </div>
        <p style={{ margin: '8px 0 0', fontSize: 12, color: T.faint }}>
          Pick at least one. The first you pick is the default project when a tool omits one.
        </p>
      </div>
      )}

      {!orgsLoading && Object.keys(orgNames).length === 0 && (
        <p style={{ margin: '18px 0 0', fontSize: 13.5, lineHeight: 1.55, color: T.danger }}>
          Your account has not joined an organization yet. Open a <code style={{ fontFamily: T.mono }}>/join/&lt;code&gt;</code> link first, then reconnect your agent.
        </p>
      )}

      {actionError && <p style={{ margin: '14px 0 0', fontSize: 13.5, color: T.danger }}>{actionError}</p>}

      <div style={{ marginTop: 22, display: 'flex', gap: 10 }}>
        <Button
          variant="secondary"
          style={{ flex: 1 }}
          disabled={!!busy}
          loading={busy === 'deny'}
          onClick={() => void decide('deny')}
        >
          Deny
        </Button>
        <Button
          variant="primary"
          style={{ flex: 1 }}
          disabled={!!busy || orgsLoading || projectsLoading || !selectedOrgId || selectedProjectIds.length === 0}
          loading={busy === 'approve'}
          onClick={() => void decide('approve')}
        >
          Approve
        </Button>
      </div>

      <p style={{ margin: '20px 0 0', fontSize: 12.5, color: T.faint }}>server {window.location.host}</p>
    </Frame>
  )
}
