import { useEffect, useState } from 'react'
import { useNavigate, useSearchParams } from 'react-router-dom'
import { fetchJoinOrg } from '../lib/client'
import { saveToken, loadToken, parseToken } from '../lib/token'
import { isOidc, isLocal } from '../lib/auth-mode'
import { localLogin, requestPasswordReset } from '../lib/local-auth'
import { requireSupabase } from '../lib/supabase'
import { useIdentity } from '../lib/identity'
import { safeReturnTo } from '../lib/return-to'
import { validateEmail } from '../lib/validation'
import { Icon, LogoTile } from '../components/Icon'
import { T } from '../lib/theme'

// Out-of-app screen (no shell). Mockup frames 1a (sign up) / 1b (log in):
// split brand panel + form. `?mode=signup` toggles the two modes. The form
// panel is auth-mode aware:
//   oidc → real Supabase email/password (session.access_token bearer)
//   dev  → dev-stub Bearer token entry (memberID:projectID:role)

type Mode = 'signup' | 'login'

const fieldLabel: React.CSSProperties = { fontSize: 13, fontWeight: 500, color: '#3A414D' }

const fieldInput: React.CSSProperties = {
  height: 44,
  boxSizing: 'border-box',
  width: '100%',
  border: `1px solid ${T.borderStrong}`,
  borderRadius: 10,
  padding: '0 14px',
  fontSize: 14,
  fontFamily: 'inherit',
  color: '#1D2430',
  background: '#fff',
  outline: 'none',
}

const primaryBtn: React.CSSProperties = {
  width: '100%',
  height: 46,
  border: 'none',
  borderRadius: 10,
  background: T.accent,
  color: '#fff',
  fontSize: 14.5,
  fontWeight: 600,
  fontFamily: 'inherit',
  cursor: 'pointer',
  boxShadow: T.shadowBtn,
  display: 'flex',
  alignItems: 'center',
  justifyContent: 'center',
  gap: 8,
}

const googleBtn: React.CSSProperties = {
  width: '100%',
  height: 46,
  border: `1px solid ${T.borderStrong}`,
  borderRadius: 10,
  background: '#fff',
  color: '#1D2430',
  fontSize: 14,
  fontWeight: 500,
  fontFamily: 'inherit',
  display: 'flex',
  alignItems: 'center',
  justifyContent: 'center',
  gap: 10,
  cursor: 'pointer',
}

const switchLink: React.CSSProperties = { color: T.accent, fontWeight: 600, cursor: 'pointer' }

function GoogleLogo() {
  return (
    <svg width="18" height="18" viewBox="0 0 24 24" aria-hidden="true">
      <path fill="#4285F4" d="M23.5 12.3c0-.8-.1-1.6-.2-2.3H12v4.5h6.5a5.6 5.6 0 0 1-2.4 3.6v3h3.9c2.3-2.1 3.5-5.2 3.5-8.8z" />
      <path fill="#34A853" d="M12 24c3.2 0 6-1.1 8-2.9l-3.9-3c-1 .7-2.4 1.2-4.1 1.2-3.1 0-5.8-2.1-6.7-5H1.3v3.1A12 12 0 0 0 12 24z" />
      <path fill="#FBBC05" d="M5.3 14.3a7.2 7.2 0 0 1 0-4.6V6.6H1.3a12 12 0 0 0 0 10.8z" />
      <path fill="#EA4335" d="M12 4.8c1.8 0 3.3.6 4.6 1.8l3.4-3.4A12 12 0 0 0 1.3 6.6l4 3.1c.9-2.9 3.6-5 6.7-5z" />
    </svg>
  )
}

function OrDivider() {
  return (
    <div style={{ display: 'flex', alignItems: 'center', gap: 14 }}>
      <div style={{ flex: 1, height: 1, background: '#EAECEF' }} />
      <span style={{ fontSize: 12.5, color: T.faint }}>or</span>
      <div style={{ flex: 1, height: 1, background: '#EAECEF' }} />
    </div>
  )
}

function FormError({ error }: { error: string | null }) {
  if (!error) return null
  return <div style={{ fontSize: 13, color: T.dangerInk, lineHeight: 1.5 }}>{error}</div>
}

function validateCredentials(email: string, password: string): string | null {
  return validateEmail(email) ?? (!password ? 'Password is required.' : null)
}

// inviteCodeFrom derives the join code an invited visitor is authing for: either
// the explicit `invite` param (set by JoinPage) or a returnTo that points back at
// a /join/<code> link. Empty when this is not an invite flow.
function inviteCodeFrom(params: URLSearchParams): string {
  const explicit = params.get('invite')?.trim()
  if (explicit) return explicit

  const rt = params.get('returnTo') ?? ''
  if (rt.startsWith('/join/')) {
    return rt.slice('/join/'.length).split(/[/?#]/)[0]
  }

  return ''
}

export function AuthPage() {
  const navigate = useNavigate()
  const [params, setParams] = useSearchParams()
  const { authed } = useIdentity()
  const inviteCode = inviteCodeFrom(params)
  // An invite defaults to signup (the invitee usually has no account yet) unless
  // they explicitly switched to log in.
  const explicitMode = params.get('mode')
  const mode: Mode =
    explicitMode === 'login' ? 'login' : explicitMode === 'signup' || inviteCode ? 'signup' : 'login'
  const returnTo = safeReturnTo(params.get('returnTo'))

  // Invite-aware copy: name the org the code belongs to. The lookup reveals the
  // name ONLY for a valid, non-revoked code; a bad/revoked code resolves to null
  // and we fall back to the generic headings rather than hard-failing.
  const [inviteOrg, setInviteOrg] = useState<string | null>(null)
  const [inviteStatus, setInviteStatus] = useState<'idle' | 'loading' | 'valid' | 'invalid'>('idle')
  useEffect(() => {
    if (!inviteCode) {
      setInviteOrg(null)
      setInviteStatus('idle')
      return
    }
    setInviteStatus('loading')
    let cancelled = false
    void fetchJoinOrg(inviteCode).then(name => {
      if (!cancelled) {
        setInviteOrg(name)
        setInviteStatus(name ? 'valid' : 'invalid')
      }
    })
    return () => {
      cancelled = true
    }
  }, [inviteCode])

  // Already authenticated → skip the login screen, back to wherever the
  // guard sent us from (e.g. /authorize?client_id=…&state=…).
  useEffect(() => {
    if (authed) navigate(returnTo, { replace: true })
  }, [authed, navigate, returnTo])

  const setMode = (m: Mode) => {
    const next: Record<string, string> = { mode: m }
    const rt = params.get('returnTo')
    if (rt) next.returnTo = rt
    const inv = params.get('invite')
    if (inv) next.invite = inv
    setParams(next, { replace: true })
  }

  return (
    <div style={{ minHeight: '100vh', display: 'flex', background: T.surface }}>
      <BrandPanel mode={mode} />
      <div style={{ flex: 1, display: 'flex', alignItems: 'center', justifyContent: 'center', padding: 48, background: '#fff' }}>
        <div style={{ width: '100%', maxWidth: 384 }}>
          {inviteStatus === 'invalid' && (
            <div style={{ marginBottom: 18, padding: '11px 13px', borderRadius: 10, color: T.dangerInk, background: T.dangerBg, border: `1px solid ${T.dangerBorder}`, fontSize: 13, lineHeight: 1.5 }}>
              This join code is invalid or has been revoked. You can still sign in, but you won&apos;t join an organization with this link.
            </div>
          )}
          {isLocal ? (
            <LocalLoginForm returnTo={returnTo} />
          ) : !isOidc ? (
            <DevTokenForm />
          ) : mode === 'signup' ? (
            <SignupForm prefillEmail={params.get('email') ?? ''} returnTo={returnTo} inviteOrg={inviteOrg} onSwitch={() => setMode('login')} />
          ) : (
            <LoginForm prefillEmail={params.get('email') ?? ''} returnTo={returnTo} inviteOrg={inviteOrg} onSwitch={() => setMode('signup')} />
          )}
        </div>
      </div>
    </div>
  )
}

// ── Left brand panel ─────────────────────────────────────────────────────────

function BrandPanel({ mode }: { mode: Mode }) {
  return (
    <div
      style={{
        width: '45%',
        flex: 'none',
        background: '#F4F3FD',
        borderRight: '1px solid #E9E7FA',
        padding: '52px 50px',
        display: 'flex',
        flexDirection: 'column',
        position: 'relative',
        overflow: 'hidden',
      }}
    >
      <div style={{ display: 'flex', alignItems: 'center', gap: 11 }}>
        <LogoTile size={34} radius={10} />
        <span style={{ fontSize: 19, fontWeight: 700, letterSpacing: '-.02em', color: '#211E3B' }}>Orako</span>
      </div>
      {mode === 'signup' ? <SignupBrand /> : <LoginBrand />}
      <div style={{ marginTop: 'auto', fontSize: 12.5, color: '#9A97B5' }}>
        {mode === 'signup' ? '© 2026 Orako · Ask once, reuse forever' : '© 2026 Orako'}
      </div>
    </div>
  )
}

function SignupBrand() {
  return (
    <>
      <div style={{ marginTop: 64 }}>
        <h2 style={{ fontSize: 33, fontWeight: 700, lineHeight: 1.18, letterSpacing: '-.03em', color: '#211E3B', maxWidth: 400 }}>
          Ask a human once. Reuse the answer forever.
        </h2>
        <p style={{ fontSize: 15.5, lineHeight: 1.6, color: '#615E86', marginTop: 18, maxWidth: 400 }}>
          Your coding agents ask the right teammate a question — in Slack, Teams or Telegram — and every answer
          becomes shared team knowledge.
        </p>
      </div>
      <div
        style={{
          marginTop: 44,
          background: '#fff',
          border: '1px solid #ECEBF6',
          borderRadius: 16,
          padding: 18,
          boxShadow: '0 18px 40px -22px rgba(33,30,59,.4)',
          maxWidth: 400,
        }}
      >
        <div style={{ display: 'flex', alignItems: 'center', gap: 9, marginBottom: 10 }}>
          <div
            style={{
              width: 24,
              height: 24,
              borderRadius: 7,
              background: '#211E3B',
              display: 'flex',
              alignItems: 'center',
              justifyContent: 'center',
              color: '#fff',
              fontFamily: T.mono,
              fontSize: 10,
              fontWeight: 600,
            }}
          >
            AI
          </div>
          <span style={{ fontSize: 12.5, fontWeight: 600, color: '#2A313C' }}>Claude Code</span>
          <span style={{ fontSize: 11.5, color: '#9A97B5' }}>asks in #orako</span>
        </div>
        <p style={{ fontSize: 14, lineHeight: 1.5, color: '#2A313C', background: '#F5F5FA', borderRadius: 10, padding: '11px 13px', marginBottom: 14 }}>
          How should we rotate expired refresh tokens on retry?
        </p>
        <div style={{ display: 'flex', alignItems: 'center', gap: 9, marginBottom: 8 }}>
          <div
            style={{
              width: 24,
              height: 24,
              borderRadius: '50%',
              background: '#F3D9A4',
              display: 'flex',
              alignItems: 'center',
              justifyContent: 'center',
              color: '#7A5A16',
              fontSize: 10,
              fontWeight: 700,
            }}
          >
            SB
          </div>
          <span style={{ fontSize: 12.5, fontWeight: 600, color: '#2A313C' }}>Sarah · Backend</span>
          <svg width="14" height="14" viewBox="0 0 24 24" fill="#4A154B" aria-hidden="true">
            <path d="M6 15a2 2 0 1 1-2-2h2zm1 0a2 2 0 0 1 4 0v5a2 2 0 1 1-4 0zM9 6a2 2 0 1 1 2 2H9zm0 1a2 2 0 0 1 0 4H4a2 2 0 1 1 0-4zM18 9a2 2 0 1 1 2 2h-2zm-1 0a2 2 0 0 1-4 0V4a2 2 0 1 1 4 0zM15 18a2 2 0 1 1-2-2h2zm0-1a2 2 0 0 1 0-4h5a2 2 0 1 1 0 4z" />
          </svg>
        </div>
        <p style={{ fontSize: 14, lineHeight: 1.5, color: '#3A414D' }}>
          Issue a new refresh token on every use and revoke the old one — detect reuse to catch theft.
        </p>
        <div style={{ display: 'flex', alignItems: 'center', gap: 7, marginTop: 13, paddingTop: 12, borderTop: `1px solid ${T.line}` }}>
          <Icon name="check" size={15} strokeWidth={2.4} color={T.accent} />
          <span style={{ fontSize: 12.5, fontWeight: 500, color: T.accent }}>Saved to history</span>
        </div>
      </div>
    </>
  )
}

function LoginBrand() {
  return (
    <div style={{ marginTop: 'auto', marginBottom: 'auto' }}>
      <div style={{ display: 'flex', gap: 8, marginBottom: 22 }}>
        {['Slack', 'Teams', 'Telegram'].map(x => (
          <span
            key={x}
            style={{ fontSize: 12, fontWeight: 600, color: T.accent, background: '#EBEAFB', padding: '5px 11px', borderRadius: 20 }}
          >
            {x}
          </span>
        ))}
      </div>
      <h2 style={{ fontSize: 31, fontWeight: 700, lineHeight: 1.2, letterSpacing: '-.03em', color: '#211E3B', maxWidth: 390 }}>
        Your teammates, one message away from every agent.
      </h2>
      <div style={{ display: 'flex', gap: 34, marginTop: 34 }}>
        {[
          ['1,240', 'questions answered'],
          ['96%', 'reused from history'],
        ].map(([n, l]) => (
          <div key={l}>
            <div style={{ fontSize: 30, fontWeight: 700, color: T.accent, letterSpacing: '-.02em' }}>{n}</div>
            <div style={{ fontSize: 12.5, color: '#615E86', marginTop: 2 }}>{l}</div>
          </div>
        ))}
      </div>
    </div>
  )
}

// ── oidc: sign up ────────────────────────────────────────────────────────────

function SignupForm({
  prefillEmail,
  returnTo,
  inviteOrg,
  onSwitch,
}: {
  prefillEmail?: string
  returnTo: string
  inviteOrg?: string | null
  onSwitch: () => void
}) {
  const navigate = useNavigate()
  const [email, setEmail] = useState(prefillEmail ?? '')
  const [password, setPassword] = useState('')
  const [error, setError] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)
  const [sentTo, setSentTo] = useState<string | null>(null)

  async function handleSignup() {
    if (busy) return
    const validationError = validateCredentials(email, password)
    if (validationError) {
      setError(validationError)
      return
    }
    setError(null)
    setBusy(true)
    try {
      const { data, error } = await requireSupabase().auth.signUp({ email: email.trim(), password })
      if (error) {
        // Existing account → the invitee just needs to log in; auto-linking to
        // the org happens server-side after login.
        if (/already/i.test(error.message)) {
          onSwitch()
          return
        }
        setError(error.message)
        return
      }
      if (data.session) {
        navigate('/welcome')
      } else {
        setSentTo(email.trim())
      }
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e))
    } finally {
      setBusy(false)
    }
  }

  async function handleGoogle() {
    setError(null)
    try {
      // Supabase's own round trip drops any query string on `redirectTo`, so
      // the destination (incl. e.g. /authorize?client_id=…&state=…) is carried
      // as its own `returnTo` param instead — /auth/callback reads it back
      // out and hands off to the same safeReturnTo() guard used everywhere
      // else. Requires https://app.orako.io/auth/callback (and this
      // query-bearing form) in the Supabase project's redirect allowlist.
      const { error } = await requireSupabase().auth.signInWithOAuth({
        provider: 'google',
        options: { redirectTo: `${window.location.origin}/auth/callback?returnTo=${encodeURIComponent(returnTo)}` },
      })
      if (error) setError(error.message)
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e))
    }
  }

  return (
    <div style={{ width: '100%', maxWidth: 384, display: 'flex', flexDirection: 'column', gap: 22 }}>
      <div>
        <h3 style={{ fontSize: 26, fontWeight: 700, letterSpacing: '-.02em', color: T.text }}>
          {inviteOrg ? `You've been invited to ${inviteOrg}` : 'Create your account'}
        </h3>
        <p style={{ fontSize: 14.5, color: T.muted, marginTop: 7 }}>
          {inviteOrg ? (
            <>Sign in or create your account to join {inviteOrg}.</>
          ) : (
            <>
              Start your <strong style={{ color: '#3A414D' }}>14-day free trial</strong>. No credit card required.
            </>
          )}
        </p>
      </div>

      {sentTo ? (
        <div
          style={{
            background: T.successBg,
            border: `1px solid ${T.successBorder}`,
            borderRadius: 10,
            padding: '13px 15px',
            fontSize: 13.5,
            lineHeight: 1.55,
            color: T.successInk,
          }}
        >
          Check your email — we sent a confirmation link to <strong>{sentTo}</strong>. Once confirmed, log in to
          continue.
        </div>
      ) : (
        <>
          <div style={{ display: 'flex', flexDirection: 'column', gap: 16 }}>
            <label style={{ display: 'flex', flexDirection: 'column', gap: 7 }}>
              <span style={fieldLabel}>Work email</span>
              <input
                type="email"
                autoComplete="email"
                placeholder="you@company.com"
                value={email}
                onChange={e => setEmail(e.target.value)}
                style={fieldInput}
              />
            </label>
            <label style={{ display: 'flex', flexDirection: 'column', gap: 7 }}>
              <span style={fieldLabel}>Password</span>
              <input
                type="password"
                autoComplete="new-password"
                placeholder="••••••••••"
                value={password}
                onChange={e => setPassword(e.target.value)}
                onKeyDown={e => e.key === 'Enter' && handleSignup()}
                style={fieldInput}
              />
            </label>
          </div>
          <FormError error={error} />
          <button onClick={handleSignup} disabled={busy} style={{ ...primaryBtn, opacity: busy ? 0.7 : 1 }}>
            {busy ? 'Creating account…' : 'Create account'}
          </button>
          <OrDivider />
          <button onClick={handleGoogle} style={googleBtn}>
            <GoogleLogo />
            Continue with Google
          </button>
        </>
      )}

      <p style={{ fontSize: 13.5, color: T.muted, textAlign: 'center' }}>
        Already have an account?{' '}
        <span onClick={onSwitch} style={switchLink}>
          Log in
        </span>
      </p>
    </div>
  )
}

// ── oidc: log in ─────────────────────────────────────────────────────────────

function LoginForm({
  prefillEmail,
  returnTo,
  inviteOrg,
  onSwitch,
}: {
  prefillEmail?: string
  returnTo: string
  inviteOrg?: string | null
  onSwitch: () => void
}) {
  const [email, setEmail] = useState(prefillEmail ?? '')
  const [password, setPassword] = useState('')
  const [error, setError] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)
  const [forgot, setForgot] = useState(false)

  async function handleSignIn() {
    if (busy) return
    const validationError = validateCredentials(email, password)
    if (validationError) {
      setError(validationError)
      return
    }
    setError(null)
    setBusy(true)
    try {
      const { error } = await requireSupabase().auth.signInWithPassword({ email: email.trim(), password })
      if (error) {
        setError(error.message)
        return
      }
      // On success, onAuthStateChange updates the identity provider, which
      // redirects away from /auth.
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e))
    } finally {
      setBusy(false)
    }
  }

  if (forgot) {
    return <SupabaseForgotForm email={email} onEmailChange={setEmail} onBack={() => setForgot(false)} />
  }

  async function handleGoogle() {
    setError(null)
    try {
      // See the matching comment in SignupForm.handleGoogle: returnTo rides
      // as a query param on /auth/callback since Supabase strips the rest of
      // the query string on the OAuth round trip.
      const { error } = await requireSupabase().auth.signInWithOAuth({
        provider: 'google',
        options: { redirectTo: `${window.location.origin}/auth/callback?returnTo=${encodeURIComponent(returnTo)}` },
      })
      if (error) setError(error.message)
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e))
    }
  }

  return (
    <div style={{ width: '100%', maxWidth: 384, display: 'flex', flexDirection: 'column', gap: 22 }}>
      <div>
        <h3 style={{ fontSize: 26, fontWeight: 700, letterSpacing: '-.02em', color: T.text }}>
          {inviteOrg ? `You've been invited to ${inviteOrg}` : 'Welcome back'}
        </h3>
        <p style={{ fontSize: 14.5, color: T.muted, marginTop: 7 }}>
          {inviteOrg ? `Sign in or create your account to join ${inviteOrg}.` : 'Log in to your Orako workspace.'}
        </p>
      </div>

      <div style={{ display: 'flex', flexDirection: 'column', gap: 16 }}>
        <label style={{ display: 'flex', flexDirection: 'column', gap: 7 }}>
          <span style={fieldLabel}>Email</span>
          <input
            type="email"
            autoComplete="email"
            placeholder="you@company.com"
            value={email}
            onChange={e => setEmail(e.target.value)}
            style={fieldInput}
          />
        </label>
        <label style={{ display: 'flex', flexDirection: 'column', gap: 7 }}>
          <div style={{ display: 'flex', justifyContent: 'space-between' }}>
            <span style={fieldLabel}>Password</span>
            <button type="button" onClick={() => setForgot(true)} style={{ border: 0, background: 'none', padding: 0, fontSize: 12.5, color: T.accent, fontWeight: 500, cursor: 'pointer', fontFamily: 'inherit' }}>Forgot?</button>
          </div>
          <input
            type="password"
            autoComplete="current-password"
            placeholder="••••••••••"
            value={password}
            onChange={e => setPassword(e.target.value)}
            onKeyDown={e => e.key === 'Enter' && handleSignIn()}
            style={fieldInput}
          />
        </label>
      </div>

      <FormError error={error} />

      <button onClick={handleSignIn} disabled={busy} style={{ ...primaryBtn, opacity: busy ? 0.7 : 1 }}>
        {busy ? 'Logging in…' : 'Log in'}
      </button>

      <OrDivider />

      <button onClick={handleGoogle} style={googleBtn}>
        <GoogleLogo />
        Continue with Google
      </button>

      <p style={{ fontSize: 13.5, color: T.muted, textAlign: 'center' }}>
        New to Orako?{' '}
        <span onClick={onSwitch} style={switchLink}>
          Create an account
        </span>
      </p>
    </div>
  )
}

function SupabaseForgotForm({
  email,
  onEmailChange,
  onBack,
}: {
  email: string
  onEmailChange: (email: string) => void
  onBack: () => void
}) {
  const [error, setError] = useState<string | null>(null)
  const [sent, setSent] = useState(false)
  const [busy, setBusy] = useState(false)

  async function submit() {
    const validationError = validateEmail(email)
    if (validationError) {
      setError(validationError)
      return
    }
    setError(null)
    setBusy(true)
    try {
      const { error: resetError } = await requireSupabase().auth.resetPasswordForEmail(email.trim(), {
        redirectTo: `${window.location.origin}/auth/callback?type=recovery`,
      })
      if (resetError) throw resetError
      setSent(true)
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e))
    } finally {
      setBusy(false)
    }
  }

  return (
    <div style={{ width: '100%', display: 'flex', flexDirection: 'column', gap: 22 }}>
      <div>
        <h3 style={{ fontSize: 26, fontWeight: 700, letterSpacing: '-.02em', color: T.text }}>Reset your password</h3>
        <p style={{ fontSize: 14.5, color: T.muted, marginTop: 7 }}>
          {sent ? `Check ${email.trim()} for a reset link.` : 'We’ll email you a secure link to choose a new password.'}
        </p>
      </div>
      {!sent && (
        <>
          <label style={{ display: 'flex', flexDirection: 'column', gap: 7 }}>
            <span style={fieldLabel}>Email</span>
            <input type="email" autoComplete="email" value={email} onChange={e => onEmailChange(e.target.value)} onKeyDown={e => e.key === 'Enter' && void submit()} style={fieldInput} />
          </label>
          <FormError error={error} />
          <button type="button" onClick={() => void submit()} disabled={busy} style={{ ...primaryBtn, opacity: busy ? 0.7 : 1 }}>
            {busy ? 'Sending…' : 'Send reset link'}
          </button>
        </>
      )}
      <button type="button" onClick={onBack} style={{ border: 0, background: 'none', color: T.accent, fontWeight: 600, cursor: 'pointer', fontFamily: 'inherit' }}>
        Back to login
      </button>
    </div>
  )
}

// ── local: self-host email+password sign-in ──────────────────────────────────

function LocalLoginForm({ returnTo }: { returnTo: string }) {
  const navigate = useNavigate()
  const [email, setEmail] = useState('')
  const [password, setPassword] = useState('')
  const [error, setError] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)
  const [forgot, setForgot] = useState(false)

  async function submit() {
    const validationError = validateCredentials(email, password)
    if (validationError) {
      setError(validationError)
      return
    }
    setError(null)
    setBusy(true)
    try {
      await localLogin(email, password)
      navigate(returnTo)
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Login failed.')
    } finally {
      setBusy(false)
    }
  }

  if (forgot) {
    return <LocalForgotForm email={email} onEmailChange={setEmail} onBack={() => setForgot(false)} />
  }

  return (
    <form
      onSubmit={e => {
        e.preventDefault()
        void submit()
      }}
      style={{ width: '100%', maxWidth: 384, display: 'flex', flexDirection: 'column', gap: 22 }}
    >
      <div>
        <h3 style={{ fontSize: 26, fontWeight: 700, letterSpacing: '-.02em', color: T.text }}>Sign in</h3>
        <p style={{ fontSize: 14.5, color: T.muted, marginTop: 7 }}>Log in with your Orako email and password.</p>
      </div>

      <label style={{ display: 'flex', flexDirection: 'column', gap: 7 }}>
        <span style={fieldLabel}>Email</span>
        <input type="email" autoComplete="email" value={email} onChange={e => setEmail(e.target.value)} style={fieldInput} />
      </label>

      <label style={{ display: 'flex', flexDirection: 'column', gap: 7 }}>
        <div style={{ display: 'flex', justifyContent: 'space-between' }}>
          <span style={fieldLabel}>Password</span>
          <button type="button" onClick={() => { setError(null); setForgot(true) }} style={{ border: 0, background: 'none', padding: 0, fontSize: 12.5, color: T.accent, fontWeight: 500, cursor: 'pointer', fontFamily: 'inherit' }}>
            Forgot?
          </button>
        </div>
        <input
          type="password"
          autoComplete="current-password"
          value={password}
          onChange={e => setPassword(e.target.value)}
          style={fieldInput}
        />
      </label>

      <FormError error={error} />

      <button type="submit" disabled={busy} style={primaryBtn}>
        {busy ? 'Signing in…' : 'Sign in'}
        <Icon name="arrowRight" size={17} strokeWidth={2.2} color="#fff" />
      </button>
    </form>
  )
}

// LocalForgotForm requests a password-reset email (auth mode 'local'). The
// confirmation is deliberately neutral — it does not reveal whether the email has
// an account.
function LocalForgotForm({
  email,
  onEmailChange,
  onBack,
}: {
  email: string
  onEmailChange: (v: string) => void
  onBack: () => void
}) {
  const [error, setError] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)
  const [sent, setSent] = useState(false)

  async function submit() {
    const validationError = validateEmail(email)
    if (validationError) {
      setError(validationError)
      return
    }
    setError(null)
    setBusy(true)
    try {
      await requestPasswordReset(email)
      setSent(true)
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Could not send the reset email.')
    } finally {
      setBusy(false)
    }
  }

  return (
    <form
      onSubmit={e => {
        e.preventDefault()
        void submit()
      }}
      style={{ width: '100%', maxWidth: 384, display: 'flex', flexDirection: 'column', gap: 22 }}
    >
      <div>
        <h3 style={{ fontSize: 26, fontWeight: 700, letterSpacing: '-.02em', color: T.text }}>Reset password</h3>
        <p style={{ fontSize: 14.5, color: T.muted, marginTop: 7 }}>
          Enter your email and we'll send you a link to choose a new password.
        </p>
      </div>

      {sent ? (
        <div
          style={{
            background: T.successBg,
            border: `1px solid ${T.successBorder}`,
            borderRadius: 10,
            padding: '13px 15px',
            fontSize: 13.5,
            lineHeight: 1.55,
            color: T.successInk,
          }}
        >
          If an account exists for <strong>{email.trim()}</strong>, a reset link is on its way. Check your inbox.
        </div>
      ) : (
        <>
          <label style={{ display: 'flex', flexDirection: 'column', gap: 7 }}>
            <span style={fieldLabel}>Email</span>
            <input type="email" autoComplete="email" value={email} onChange={e => onEmailChange(e.target.value)} style={fieldInput} />
          </label>

          <FormError error={error} />

          <button type="submit" disabled={busy} style={{ ...primaryBtn, opacity: busy ? 0.7 : 1 }}>
            {busy ? 'Sending…' : 'Send reset link'}
          </button>
        </>
      )}

      <p style={{ fontSize: 13.5, color: T.muted, textAlign: 'center' }}>
        <span onClick={onBack} style={switchLink}>
          Back to sign in
        </span>
      </p>
    </form>
  )
}

// ── dev: dev-stub Bearer token entry ─────────────────────────────────────────

function DevTokenForm() {
  const navigate = useNavigate()
  const [params] = useSearchParams()
  const [raw, setRaw] = useState(loadToken() ?? '')
  const [error, setError] = useState<string | null>(null)

  function handleContinue() {
    setError(null)
    const stripped = raw.trim().replace(/^Bearer\s+/i, '')
    const p = parseToken(stripped)
    if (!p) {
      setError('Invalid token format. Expected: <memberID>:<projectID>:<role>  (role = dev | specialist | lead | admin)')
      return
    }
    saveToken(stripped)
    navigate(safeReturnTo(params.get('returnTo')))
  }

  return (
    <div style={{ width: '100%', maxWidth: 384, display: 'flex', flexDirection: 'column', gap: 22 }}>
      <div>
        <h3 style={{ fontSize: 26, fontWeight: 700, letterSpacing: '-.02em', color: T.text }}>Developer access</h3>
        <p style={{ fontSize: 14.5, color: T.muted, marginTop: 7 }}>
          Paste the dev-stub Bearer token to authenticate. Format:{' '}
          <code style={{ fontFamily: T.mono, color: T.accent }}>&lt;memberID&gt;:&lt;projectID&gt;:&lt;role&gt;</code>.
        </p>
      </div>

      <label style={{ display: 'flex', flexDirection: 'column', gap: 7 }}>
        <span style={fieldLabel}>Bearer token</span>
        <input
          type="text"
          placeholder="018e8f…:018e8f…:lead"
          value={raw}
          onChange={e => setRaw(e.target.value)}
          onKeyDown={e => e.key === 'Enter' && handleContinue()}
          style={{ ...fieldInput, fontFamily: T.mono, fontSize: 13 }}
        />
      </label>

      <FormError error={error} />

      <button onClick={handleContinue} style={primaryBtn}>
        Continue
        <Icon name="arrowRight" size={17} strokeWidth={2.2} color="#fff" />
      </button>

      <p style={{ fontSize: 13, color: T.subtle, lineHeight: 1.6 }}>
        Role is one of <code style={{ fontFamily: T.mono }}>dev</code>, <code style={{ fontFamily: T.mono }}>specialist</code>,{' '}
        <code style={{ fontFamily: T.mono }}>lead</code>, <code style={{ fontFamily: T.mono }}>admin</code>. This dev-stub
        path is only active when <code style={{ fontFamily: T.mono }}>VITE_ORAKO_AUTH_MODE=dev</code>.
      </p>
    </div>
  )
}
