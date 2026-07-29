// `/join/:code` — the dedicated "join by invitation link" welcome page. An admin
// shares `{origin}/join/<CODE>`; the invited person clicks it, signs in (or
// creates an account), and is provisioned as a PENDING member of the code's
// org/project — no hand-typed code, no confusing "paste a code at the OAuth
// consent" detour.
//
// This route is intentionally NOT wrapped in RequireToken/Layout (App.tsx): it
// must be reachable by a brand-new person with no membership yet. It manages its
// own auth — an unauthenticated visitor is sent to the normal /auth flow with a
// `returnTo` back here, so the `:code` from the URL is carried automatically and
// the person never types it.

import { useEffect, useRef, useState } from 'react'
import { useNavigate, useParams } from 'react-router'
import { redeemJoinCode, type JoinResult } from '../lib/client'
import { useIdentity } from '../lib/identity'
import { Button } from '../components/Button'
import { LogoTile } from '../components/Icon'
import { T } from '../lib/theme'

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
  maxWidth: 460,
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

function Heading({ children }: { children: React.ReactNode }) {
  return (
    <h1 style={{ margin: '22px 0 0', fontSize: 21, fontWeight: 700, color: T.text, lineHeight: 1.35 }}>{children}</h1>
  )
}

function Sub({ children }: { children: React.ReactNode }) {
  return <p style={{ margin: '12px 0 0', fontSize: 14, lineHeight: 1.6, color: T.muted }}>{children}</p>
}

type Phase = 'checking' | 'joining' | 'error'

export function JoinPage() {
  const { code = '' } = useParams<{ code: string }>()
  const navigate = useNavigate()
  const { authed, loading, accountOnly, refresh } = useIdentity()

  const [phase, setPhase] = useState<Phase>('checking')
  const [errorMsg, setErrorMsg] = useState('')
  // Held until the redeem resolves; the routing effect below waits for identity
  // to actually reflect the new membership before navigating.
  const [result, setResult] = useState<JoinResult | null>(null)
  // Redeem exactly once per mount, even though the identity context can
  // re-render (project/member fetch settling) after auth is established.
  const redeemed = useRef(false)

  // Step 1 — redeem the code once the visitor is authenticated.
  useEffect(() => {
    if (loading) return

    if (!authed) {
      // No pre-auth card anymore: send the visitor straight to the invite-aware
      // auth page. returnTo carries them back here after sign-in (so the code is
      // never typed); invite lets AuthPage name the org in its heading.
      navigate(
        `/auth?mode=signup&returnTo=${encodeURIComponent(`/join/${code}`)}&invite=${encodeURIComponent(code)}`,
        { replace: true },
      )

      return
    }

    if (redeemed.current) return
    redeemed.current = true
    setPhase('joining')

    redeemJoinCode(code)
      .then(async (res: JoinResult) => {
        // Refresh identity so the freshly-provisioned member is recognized. We do
        // NOT navigate here — the routing effect waits until identity actually
        // reflects the membership, so the org-less "create organization" screen
        // can never flash while the redeem/refresh settles.
        await refresh().catch(() => {})
        setResult(res)
      })
      .catch((e: unknown) => {
        setErrorMsg(e instanceof Error ? e.message : 'This invitation link could not be used.')
        setPhase('error')
      })
  }, [authed, loading, code, navigate, refresh])

  // Step 2 — route ONLY once identity shows the redeemed membership (no longer
  // account-only). An invited joiner therefore never reaches the create-org flow,
  // even briefly. A fresh pending member lands on the standalone onboarding
  // WIZARD (/onboarding) — the canonical first-run for invited/team members;
  // an existing member goes to their dashboard.
  useEffect(() => {
    if (!result || loading || accountOnly) return

    if (result.alreadyMember) {
      navigate('/', { replace: true })
    } else {
      navigate('/onboarding', { replace: true })
    }
  }, [result, loading, accountOnly, navigate])

  if (phase === 'error') {
    return (
      <Frame>
        <Heading>This invitation couldn&rsquo;t be used</Heading>
        <Sub>{errorMsg}</Sub>
        <p style={{ margin: '14px 0 0', fontSize: 13, lineHeight: 1.6, color: T.faint }}>
          The code may have been revoked or replaced. Ask whoever invited you for a fresh join link.
        </p>
        <div style={{ marginTop: 22 }}>
          <Button variant="secondary" style={{ width: '100%' }} onClick={() => navigate('/')}>
            Go to the dashboard
          </Button>
        </div>
      </Frame>
    )
  }

  return (
    <Frame>
      <Sub>{phase === 'checking' ? 'Loading your invitation…' : 'Joining…'}</Sub>
    </Frame>
  )
}
