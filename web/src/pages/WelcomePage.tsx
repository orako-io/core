import { useEffect, useState } from 'react'
import { useNavigate } from 'react-router'
import { api } from '../lib/client'
import { supabase } from '../lib/supabase'
import { useIdentity } from '../lib/identity'
import { Icon, LogoTile } from '../components/Icon'
import { T } from '../lib/theme'

// Post-signup onboarding (mockup 1c): full-viewport page outside the app
// shell, shown when the account has no organization yet (accountOnly).

function slugify(name: string): string {
  return name
    .toLowerCase()
    .trim()
    .replace(/[^a-z0-9]+/g, '-')
    .replace(/^-+|-+$/g, '')
}

function initials(seed: string): string {
  const s = seed.replace(/[^a-zA-Z0-9]/g, '')
  return (s.slice(0, 2) || 'ME').toUpperCase()
}

export function WelcomePage() {
  const navigate = useNavigate()
  const { refresh } = useIdentity()

  const [email, setEmail] = useState('')
  const [name, setName] = useState('')
  const [error, setError] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)

  useEffect(() => {
    let cancelled = false
    async function loadEmail() {
      try {
        const m = await api.getMember()
        if (m.member?.email) {
          if (!cancelled) setEmail(m.member.email)
          return
        }
      } catch {
        // account-only users have no member record yet
      }
      const { data } = (await supabase?.auth.getSession()) ?? { data: { session: null } }
      if (!cancelled) setEmail(data.session?.user.email ?? '')
    }
    void loadEmail()
    return () => {
      cancelled = true
    }
  }, [])

  async function handleCreate() {
    const trimmed = name.trim()
    if (busy || !trimmed) return
    setError(null)
    setBusy(true)
    try {
      const res = await api.createOrganization(trimmed)
      // Make the new org the active one before identity re-resolves — the
      // token's default org is whichever member row is oldest, not this one.
      localStorage.setItem('orako:activeOrg', res.organizationId)
      await refresh()
      navigate('/get-started')
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e))
    } finally {
      setBusy(false)
    }
  }

  const slug = slugify(name)

  return (
    <div style={{ minHeight: '100vh', background: T.bg, display: 'flex', flexDirection: 'column' }}>
      {/* Top bar */}
      <div
        style={{
          height: 64,
          flex: 'none',
          display: 'flex',
          alignItems: 'center',
          justifyContent: 'space-between',
          padding: '0 32px',
          borderBottom: `1px solid ${T.borderSubtle}`,
          background: T.surface,
        }}
      >
        <div style={{ display: 'flex', alignItems: 'center', gap: 10 }}>
          <LogoTile size={30} radius={9} />
          <span style={{ fontSize: 17, fontWeight: 700, letterSpacing: '-.02em', color: T.text }}>Orako</span>
        </div>
        <div style={{ display: 'flex', alignItems: 'center', gap: 14 }}>
          {email && <span style={{ fontSize: 13, color: T.subtle }}>{email}</span>}
          <div
            style={{
              width: 30,
              height: 30,
              borderRadius: '50%',
              background: '#F3D9A4',
              display: 'flex',
              alignItems: 'center',
              justifyContent: 'center',
              fontSize: 11,
              fontWeight: 700,
              color: '#7A5A16',
            }}
          >
            {initials(email || 'ME')}
          </div>
        </div>
      </div>

      {/* Centered card */}
      <div style={{ flex: 1, display: 'flex', alignItems: 'center', justifyContent: 'center', padding: 40 }}>
        <div style={{ width: '100%', maxWidth: 640 }}>
          {/* Stepper */}
          <div style={{ display: 'flex', alignItems: 'center', gap: 8, marginBottom: 26 }}>
            <div style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
              <div style={{ ...stepDot, background: T.accent, color: '#fff' }}>1</div>
              <span style={{ fontSize: 13, fontWeight: 600, color: '#1D2430' }}>Organization</span>
            </div>
            <div style={{ width: 44, height: 2, background: T.borderStrong, borderRadius: 2 }} />
            <div style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
              <div style={{ ...stepDot, background: T.borderSubtle, color: T.faint }}>2</div>
              <span style={{ fontSize: 13, fontWeight: 500, color: T.faint }}>Get started</span>
            </div>
          </div>

          <div
            style={{
              background: T.surface,
              border: `1px solid ${T.border}`,
              borderRadius: 16,
              padding: 34,
              boxShadow: T.shadowCard,
            }}
          >
            <h3 style={{ fontSize: 24, fontWeight: 700, letterSpacing: '-.02em', color: T.text }}>
              Create your organization
            </h3>
            <p style={{ fontSize: 14.5, lineHeight: 1.55, color: T.muted, marginTop: 8 }}>
              This is where your team, projects, conversations and subscription live. You'll be the{' '}
              <strong style={{ color: '#3A414D' }}>admin</strong>.
            </p>

            <div style={{ marginTop: 26, display: 'flex', flexDirection: 'column', gap: 18 }}>
              <label style={{ display: 'flex', flexDirection: 'column', gap: 7 }}>
                <span style={{ fontSize: 13, fontWeight: 500, color: '#3A414D' }}>Organization name</span>
                <input
                  autoFocus
                  placeholder="Acme"
                  value={name}
                  onChange={e => setName(e.target.value)}
                  onKeyDown={e => e.key === 'Enter' && handleCreate()}
                  style={{
                    height: 46,
                    boxSizing: 'border-box',
                    border: `1px solid ${T.borderStrong}`,
                    borderRadius: 10,
                    padding: '0 14px',
                    fontSize: 15,
                    fontFamily: 'inherit',
                    color: '#1D2430',
                    background: '#fff',
                    outline: 'none',
                  }}
                />
              </label>
              <div
                style={{
                  display: 'flex',
                  alignItems: 'center',
                  gap: 2,
                  fontSize: 13,
                  color: T.subtle,
                  background: T.bg,
                  border: `1px solid ${T.borderSubtle}`,
                  borderRadius: 9,
                  padding: '11px 13px',
                }}
              >
                <span>app.orako.io/</span>
                <span style={{ color: slug ? '#3A414D' : T.faint, fontWeight: 600 }}>{slug || 'acme'}</span>
              </div>
            </div>

            {error && <div style={{ marginTop: 14, fontSize: 13, color: T.dangerInk, lineHeight: 1.5 }}>{error}</div>}

            <button
              onClick={handleCreate}
              disabled={busy || !name.trim()}
              style={{
                width: '100%',
                height: 48,
                marginTop: 26,
                border: 'none',
                borderRadius: 10,
                background: T.accent,
                color: '#fff',
                fontSize: 15,
                fontWeight: 600,
                fontFamily: 'inherit',
                cursor: busy || !name.trim() ? 'not-allowed' : 'pointer',
                opacity: busy || !name.trim() ? 0.7 : 1,
                boxShadow: T.shadowBtn,
                display: 'flex',
                alignItems: 'center',
                justifyContent: 'center',
                gap: 8,
              }}
            >
              {busy ? 'Creating…' : 'Create organization'}
              {!busy && <Icon name="arrowRight" size={17} strokeWidth={2.2} color="#fff" />}
            </button>
          </div>

          <p style={{ fontSize: 13, color: T.faint, textAlign: 'center', marginTop: 18 }}>
            You can rename it and add more projects later.
          </p>
        </div>
      </div>
    </div>
  )
}

const stepDot: React.CSSProperties = {
  width: 24,
  height: 24,
  borderRadius: '50%',
  display: 'flex',
  alignItems: 'center',
  justifyContent: 'center',
  fontSize: 12,
  fontWeight: 600,
}
