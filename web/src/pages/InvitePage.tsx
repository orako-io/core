// Invite acceptance (mockup 1d). The invitation backend does not exist yet, so
// context comes from query params (?org, ?from, ?role) and accepting routes to
// account creation.

import { useNavigate, useSearchParams } from 'react-router'
import { LogoTile } from '../components/Icon'
import { T } from '../lib/theme'

function initials(name: string): string {
  const parts = name.trim().split(/\s+/)
  return ((parts[0]?.[0] ?? '') + (parts[1]?.[0] ?? '')).toUpperCase() || '??'
}

export function InvitePage() {
  const navigate = useNavigate()
  const [params] = useSearchParams()
  const org = params.get('org') || 'your team'
  const from = params.get('from') || ''
  const email = params.get('email') || ''
  const role = params.get('role') || 'Teammate'

  function accept() {
    const q = new URLSearchParams({ mode: 'signup' })
    if (email) q.set('email', email)
    navigate(`/auth?${q.toString()}`)
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
        <div style={{ display: 'flex', justifyContent: 'center', marginBottom: 18 }}>
          <LogoTile size={44} radius={13} />
        </div>
        <div style={{ display: 'flex', justifyContent: 'center', marginBottom: 16 }}>
          {from && (
            <div
              style={{
                width: 38,
                height: 38,
                borderRadius: '50%',
                background: '#F3D9A4',
                color: '#7A5A16',
                display: 'flex',
                alignItems: 'center',
                justifyContent: 'center',
                fontSize: 13,
                fontWeight: 700,
                border: '2px solid #fff',
                zIndex: 1,
              }}
            >
              {initials(from)}
            </div>
          )}
          <div
            style={{
              width: 38,
              height: 38,
              borderRadius: '50%',
              background: T.accentSoft,
              color: T.accent,
              display: 'flex',
              alignItems: 'center',
              justifyContent: 'center',
              fontSize: 14,
              fontWeight: 700,
              border: '2px solid #fff',
              marginLeft: -10,
            }}
          >
            {org.charAt(0).toUpperCase()}
          </div>
        </div>
        <h1 style={{ fontSize: 23, fontWeight: 700, letterSpacing: '-.02em', color: T.text, lineHeight: 1.3 }}>
          {from ? `${from.split(/\s+/)[0]} invited you to join ${org}` : `You're invited to join ${org}`}
        </h1>
        <p style={{ fontSize: 14.5, lineHeight: 1.55, color: T.muted, marginTop: 10 }}>
          Orako connects your team's teammates to the coding agents that need answers.
          {email && (
            <>
              {' '}
              Sign up with <strong style={{ color: '#3A414D' }}>{email}</strong> to join automatically.
            </>
          )}
        </p>
        <div style={{ display: 'flex', justifyContent: 'center', marginTop: 16 }}>
          <span
            style={{
              display: 'inline-flex',
              alignItems: 'center',
              gap: 7,
              background: '#EDF5EC',
              border: '1px solid #CFE6CC',
              padding: '5px 12px',
              borderRadius: 20,
              fontSize: 12.5,
              fontWeight: 600,
              color: '#3A6B33',
            }}
          >
            <span style={{ width: 6, height: 6, borderRadius: '50%', background: '#3A9A34' }} />
            Role · {role}
          </span>
        </div>
        <button
          onClick={accept}
          style={{
            width: '100%',
            height: 46,
            marginTop: 22,
            border: 'none',
            borderRadius: 11,
            background: T.accent,
            color: '#fff',
            fontSize: 15,
            fontWeight: 600,
            fontFamily: 'inherit',
            cursor: 'pointer',
            boxShadow: T.shadowBtn,
          }}
        >
          Accept invitation
        </button>
        <p style={{ fontSize: 12, color: T.faint, marginTop: 14, lineHeight: 1.5 }}>
          By accepting you agree to Orako's Terms and Privacy Policy.
        </p>
      </div>
      <p style={{ fontSize: 13, color: T.subtle, marginTop: 22 }}>
        Already have an account?{' '}
        <span
          onClick={() => navigate(email ? `/auth?email=${encodeURIComponent(email)}` : '/auth')}
          style={{ color: T.accent, fontWeight: 600, cursor: 'pointer' }}
        >
          Log in
        </span>{' '}
        — you'll join {org} automatically.
      </p>
    </div>
  )
}
