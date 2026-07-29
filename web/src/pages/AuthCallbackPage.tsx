// Signed action-link landing (invitation / magiclink) *and* the Google/OAuth
// return stop. Two unrelated flows share this route because Supabase always
// wants one fixed `redirectTo`:
//   - ?token_hash&type: verifyOtp proves email ownership and creates the
//     session directly (invite/magiclink) — no second verification email.
//     Invitees pick a password here while already authenticated.
//   - ?returnTo=…: the OAuth (Google) round trip. Supabase's own redirect
//     drops every query param we send it *except* what we bake into
//     `redirectTo` itself, so AuthPage's Google buttons point here with
//     `returnTo` attached; Supabase's JS client parses the session out of the
//     URL on its own (detectSessionInUrl), we just wait for the identity
//     provider to observe it, then hop to the original destination — reusing
//     the same safeReturnTo() guard as the email/password and dev-token
//     paths so this can never be driven off-site.
import { useEffect, useRef, useState } from 'react'
import { useNavigate, useSearchParams, Link } from 'react-router-dom'
import { LogoTile } from '../components/Icon'
import { T } from '../lib/theme'
import { requireSupabase } from '../lib/supabase'
import { useIdentity } from '../lib/identity'
import { safeReturnTo } from '../lib/return-to'
import { isLocal } from '../lib/auth-mode'
import { acceptInvite, resetPassword } from '../lib/local-auth'

type Step = 'verifying' | 'oauth' | 'set-password' | 'error'

// AuthCallbackPage dispatches by auth mode: self-host (local) sets a password
// (invite acceptance or password reset); SaaS (Supabase) runs the OTP/OAuth
// callback.
export function AuthCallbackPage() {
  return isLocal ? <LocalSetPassword /> : <SupabaseCallback />
}

// LocalSetPassword: the user chooses a password from an email-link token
// (?token_hash=…). type=reset spends a reset token (POST /auth/reset-password);
// anything else accepts an invite (POST /auth/accept-invite). Both then sign in.
function LocalSetPassword() {
  const navigate = useNavigate()
  const [params] = useSearchParams()
  const token = params.get('token_hash') || ''
  const email = params.get('email') || ''
  const isReset = params.get('type') === 'reset'

  const [password, setPassword] = useState('')
  const [confirm, setConfirm] = useState('')
  const [error, setError] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)
  const [done, setDone] = useState(false)

  async function submit() {
    setError(null)

    if (password.length < 8) {
      setError('Password must be at least 8 characters.')
      return
    }

    if (password !== confirm) {
      setError('Passwords do not match.')
      return
    }

    setBusy(true)
    try {
      await (isReset ? resetPassword(token, password) : acceptInvite(token, password))
      setDone(true)
      setTimeout(() => navigate('/auth', { replace: true }), 1200)
    } catch (err) {
      setError(err instanceof Error ? err.message : isReset ? 'Could not reset your password.' : 'Could not accept the invitation.')
    } finally {
      setBusy(false)
    }
  }

  return (
    <div style={callbackShell}>
      <div style={callbackCard}>
        <LogoTile size={44} />
        {!token ? (
          <p style={{ color: T.danger, marginTop: 18 }}>This {isReset ? 'reset' : 'invite'} link is missing its token.</p>
        ) : done ? (
          <p style={{ marginTop: 18, color: T.text }}>Password set. Redirecting to sign in…</p>
        ) : (
          <form
            onSubmit={e => {
              e.preventDefault()
              void submit()
            }}
            style={{ marginTop: 18, display: 'flex', flexDirection: 'column', gap: 14, width: '100%' }}
          >
            <h3 style={{ fontSize: 22, fontWeight: 700, color: T.text, margin: 0 }}>
              {isReset ? 'Choose a new password' : 'Set your password'}
            </h3>
            {email && (
              <p style={{ fontSize: 13.5, color: T.muted, margin: 0 }}>
                {isReset ? `Resetting the password for ${email}.` : `Accepting the invite for ${email}.`}
              </p>
            )}
            <input
              type="password"
              autoComplete="new-password"
              placeholder="New password (min 8 characters)"
              value={password}
              onChange={e => setPassword(e.target.value)}
              style={callbackInput}
            />
            <input
              type="password"
              autoComplete="new-password"
              placeholder="Confirm password"
              value={confirm}
              onChange={e => setConfirm(e.target.value)}
              style={callbackInput}
            />
            {error && <p style={{ color: T.danger, fontSize: 13, margin: 0 }}>{error}</p>}
            <button type="submit" disabled={busy} style={callbackBtn}>
              {busy ? 'Setting…' : 'Set password & continue'}
            </button>
          </form>
        )}
      </div>
    </div>
  )
}

const callbackShell: React.CSSProperties = {
  minHeight: '100vh',
  display: 'flex',
  alignItems: 'center',
  justifyContent: 'center',
  background: T.surface,
  padding: 24,
}

const callbackCard: React.CSSProperties = {
  width: '100%',
  maxWidth: 380,
  display: 'flex',
  flexDirection: 'column',
  alignItems: 'center',
  background: '#fff',
  border: `1px solid ${T.borderSubtle}`,
  borderRadius: 16,
  padding: 32,
}

const callbackInput: React.CSSProperties = {
  height: 42,
  boxSizing: 'border-box',
  width: '100%',
  border: `1px solid ${T.borderStrong}`,
  borderRadius: 10,
  padding: '0 13px',
  fontSize: 14,
  fontFamily: 'inherit',
}

const callbackBtn: React.CSSProperties = {
  height: 44,
  border: 'none',
  borderRadius: 10,
  background: T.accent,
  color: '#fff',
  fontSize: 14.5,
  fontWeight: 600,
  cursor: 'pointer',
}

function SupabaseCallback() {
  const navigate = useNavigate()
  const [params] = useSearchParams()
  const { authed } = useIdentity()
  const tokenHash = params.get('token_hash') || ''
  const isRecovery = params.get('type') === 'recovery'
  const linkType = params.get('type') === 'magiclink' ? 'magiclink' : 'invite'
  const oauthReturnTo = params.get('returnTo')
  const oauthError = params.get('error_description') || params.get('error')

  const [step, setStep] = useState<Step>(tokenHash ? 'verifying' : isRecovery ? 'set-password' : 'oauth')
  const [error, setError] = useState<string | null>(null)
  const [password, setPassword] = useState('')
  const [confirm, setConfirm] = useState('')
  const [busy, setBusy] = useState(false)
  const verified = useRef(false)

  // OAuth branch: no token_hash to verify — either bounce back to /auth (a
  // bare hit on this route) or, when `returnTo` is present, wait for the
  // session Supabase just parsed out of the URL and then navigate onward.
  useEffect(() => {
    if (tokenHash || isRecovery) return
    if (!oauthReturnTo) {
      navigate('/auth', { replace: true })
      return
    }
    if (oauthError) {
      setError(oauthError)
      setStep('error')
      return
    }
    if (authed) {
      navigate(safeReturnTo(oauthReturnTo), { replace: true })
    }
  }, [tokenHash, isRecovery, oauthReturnTo, oauthError, authed, navigate])

  useEffect(() => {
    if (!tokenHash) return
    if (verified.current) return
    verified.current = true // React strict-mode double-mount: verify exactly once
    void (async () => {
      try {
        const { error } = await requireSupabase().auth.verifyOtp({
          type: linkType,
          token_hash: tokenHash,
        })
        if (error) {
          setError(error.message)
          setStep('error')
          return
        }
        if (linkType === 'magiclink') {
          navigate('/', { replace: true })
          return
        }
        setStep('set-password')
      } catch (e) {
        setError(e instanceof Error ? e.message : String(e))
        setStep('error')
      }
    })()
  }, [tokenHash, linkType, navigate])

  async function handleSetPassword() {
    if (busy) return
    if (password.length < 8) {
      setError('Password must be at least 8 characters.')
      return
    }
    if (password !== confirm) {
      setError('Passwords do not match.')
      return
    }
    setError(null)
    setBusy(true)
    try {
      const { error } = await requireSupabase().auth.updateUser({ password })
      if (error) {
        setError(error.message)
        return
      }
      navigate('/', { replace: true })
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e))
    } finally {
      setBusy(false)
    }
  }

  const inputStyle: React.CSSProperties = {
    width: '100%',
    boxSizing: 'border-box',
    padding: '11px 14px',
    fontSize: 14.5,
    fontFamily: T.sans,
    color: T.text,
    background: T.surface,
    border: `1px solid ${T.border}`,
    borderRadius: 10,
    outline: 'none',
  }

  return (
    <div
      style={{
        minHeight: '100vh',
        background: T.bg,
        display: 'flex',
        flexDirection: 'column',
        alignItems: 'center',
        justifyContent: 'center',
        padding: 24,
        fontFamily: T.sans,
      }}
    >
      <div
        style={{
          width: '100%',
          maxWidth: 420,
          background: T.surface,
          border: `1px solid ${T.border}`,
          borderRadius: 18,
          boxShadow: T.shadowModal,
          padding: '36px 32px 30px',
          textAlign: 'center',
        }}
      >
        <LogoTile />

        {step === 'verifying' && (
          <p style={{ margin: '22px 0 0', fontSize: 14.5, color: T.muted }}>
            Verifying your invitation…
          </p>
        )}

        {step === 'oauth' && (
          <p style={{ margin: '22px 0 0', fontSize: 14.5, color: T.muted }}>
            Completing sign-in…
          </p>
        )}

        {step === 'set-password' && (
          <>
            <h1 style={{ margin: '22px 0 0', fontSize: 21, fontWeight: 700, color: T.text }}>
              {isRecovery ? 'Choose a new password' : "Welcome — you're in"}
            </h1>
            <p style={{ margin: '10px 0 0', fontSize: 14, lineHeight: 1.6, color: T.muted }}>
              {isRecovery
                ? 'Pick a new password for your Orako account.'
                : 'Your email is verified. Pick a password to finish setting up your account.'}
            </p>
            <div style={{ marginTop: 22, display: 'flex', flexDirection: 'column', gap: 10 }}>
              <input
                type="password"
                placeholder="Password (min. 8 characters)"
                value={password}
                autoFocus
                onChange={(e) => setPassword(e.target.value)}
                style={inputStyle}
              />
              <input
                type="password"
                placeholder="Confirm password"
                value={confirm}
                onChange={(e) => setConfirm(e.target.value)}
                onKeyDown={(e) => {
                  if (e.key === 'Enter') void handleSetPassword()
                }}
                style={inputStyle}
              />
            </div>
            <button
              onClick={() => void handleSetPassword()}
              disabled={busy || !password || !confirm}
              style={{
                marginTop: 16,
                width: '100%',
                padding: '13px 0',
                fontSize: 15,
                fontWeight: 600,
                fontFamily: T.sans,
                color: '#fff',
                background: busy ? T.accentHover : T.accent,
                border: 'none',
                borderRadius: 11,
                cursor: busy ? 'default' : 'pointer',
                opacity: !password || !confirm ? 0.6 : 1,
              }}
            >
              {busy ? 'Saving…' : isRecovery ? 'Save new password' : 'Enter the dashboard →'}
            </button>
          </>
        )}

        {step === 'error' && (
          <>
            <h1 style={{ margin: '22px 0 0', fontSize: 20, fontWeight: 700, color: T.text }}>
              {tokenHash ? "This link didn't work" : 'Sign-in failed'}
            </h1>
            <p style={{ margin: '10px 0 0', fontSize: 14, lineHeight: 1.6, color: T.muted }}>
              {tokenHash
                ? 'It may have expired or already been used. Ask your admin for a fresh invitation, or sign in if you already have an account.'
                : error || 'The sign-in attempt did not complete. Try again, or use email and password instead.'}
            </p>
            <Link
              to="/auth"
              style={{
                display: 'inline-block',
                marginTop: 18,
                fontSize: 14.5,
                fontWeight: 600,
                color: T.accent,
                textDecoration: 'none',
              }}
            >
              Go to sign in →
            </Link>
          </>
        )}

        {error && step !== 'error' && (
          <p style={{ margin: '14px 0 0', fontSize: 13.5, color: T.danger }}>{error}</p>
        )}
      </div>
    </div>
  )
}
