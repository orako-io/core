import { useEffect, useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { Page, Card } from '../components/Layout'
import { Button } from '../components/Button'
import { Input } from '../components/Input'
import { Spinner } from '../components/Spinner'
import { ErrorBanner } from '../components/ErrorBanner'
import { Icon } from '../components/Icon'
import { api } from '../lib/client'
import { isOidc } from '../lib/auth-mode'
import { supabase } from '../lib/supabase'
import { clearToken, loadToken } from '../lib/token'
import { useToast, toastMessage } from '../lib/toast'
import { DiscordIdHint } from '../components/DiscordIdHint'
import { T } from '../lib/theme'

const cardTitle: React.CSSProperties = { fontSize: 16.5, fontWeight: 700, letterSpacing: '-.01em', color: T.text }
const cardSub: React.CSSProperties = { fontSize: 13.5, color: T.muted, marginTop: 4, lineHeight: 1.5 }

// Per-provider contact handles a member can set once the org connects that
// provider — so teammates can @mention them and the agent can DM them there.
// kind matches the ConnectedChannel kinds; field is the member binding column.
type ProviderKind = 'slack' | 'discord' | 'telegram' | 'teams'
// Delivery channel = where Orako sends YOU a question an agent routed to you.
// dashboard always works (the Inbox); an external channel needs its handle set.
const DELIVERY_CHANNELS = ['dashboard', 'slack', 'teams', 'telegram', 'discord'] as const
const CHANNEL_LABEL: Record<string, string> = { dashboard: 'Dashboard', slack: 'Slack', teams: 'Teams', telegram: 'Telegram', discord: 'Discord' }
const PROVIDER_FIELDS: { kind: ProviderKind; label: string; placeholder: string; hint: string }[] = [
  { kind: 'slack', label: 'Slack member ID', placeholder: 'U01234ABC', hint: 'Your Slack member ID — teammates @mention you and the agent DMs you here.' },
  { kind: 'discord', label: 'Discord user ID', placeholder: '123456789012345678', hint: 'Your Discord user ID (snowflake) — enables Discord DMs and mentions.' },
  { kind: 'telegram', label: 'Telegram chat ID', placeholder: '123456789', hint: 'Your Telegram chat ID (from messaging the bot).' },
  { kind: 'teams', label: 'Teams user ID', placeholder: 'Azure AD object id', hint: 'Your Microsoft Teams (Azure AD) user ID for Teams DMs.' },
]

export function AccountPage() {
  return (
    <Page width={640}>
      <div style={{ display: 'flex', flexDirection: 'column', gap: 20 }}>
        <ProfileCard />
        {!isOidc && <DevTokenCard />}
        <SessionCard />
      </div>
    </Page>
  )
}

function ProfileCard() {
  const toast = useToast()
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<unknown>(null)
  const [firstName, setFirstName] = useState('')
  const [lastName, setLastName] = useState('')
  const [displayName, setDisplayName] = useState('')
  const [email, setEmail] = useState('')
  const [gitHandle, setGitHandle] = useState('')
  // Where Orako reaches this member when an agent asks them a question.
  const [deliveryChannel, setDeliveryChannel] = useState('dashboard')
  const [saving, setSaving] = useState(false)
  // Per-provider contact handles, keyed by provider kind.
  const [bindings, setBindings] = useState<Record<ProviderKind, string>>({ slack: '', discord: '', telegram: '', teams: '' })
  // The org's connected external providers — which handle fields to show.
  const [connected, setConnected] = useState<string[]>([])

  useEffect(() => {
    let cancelled = false
    Promise.all([api.getMember(), api.listConnectedChannels().catch(() => ({ channels: [] as string[] }))])
      .then(([memberRes, channelsRes]) => {
        if (cancelled) return
        const m = memberRes.member
        setDisplayName(m?.displayName ?? '')
        setEmail(m?.email ?? '')
        setFirstName(m?.firstName ?? '')
        setLastName(m?.lastName ?? '')
        setGitHandle(m?.gitHandle ?? '')
        setDeliveryChannel(m?.deliveryChannel || 'dashboard')
        setBindings({
          slack: m?.slackUserId ?? '',
          discord: m?.discordUserId ?? '',
          telegram: m?.telegramChatId ?? '',
          teams: m?.teamsUserId ?? '',
        })
        setConnected((channelsRes.channels ?? []).filter(c => c !== 'dashboard'))
      })
      .catch(e => {
        if (!cancelled) setError(e)
      })
      .finally(() => {
        if (!cancelled) setLoading(false)
      })
    return () => {
      cancelled = true
    }
  }, [])

  async function handleSave() {
    setSaving(true)
    try {
      // Wire convention: empty string = "leave unchanged", "-" = clear. An
      // emptied clearable field (git handle, channel bindings) must actually
      // clear server-side — a UX profile has no git handle, and a wrong
      // Discord ID must be removable.
      const orClear = (v: string) => (v.trim() === '' ? '-' : v.trim())
      const res = await api.updateMember({
        displayName: displayName.trim(),
        email: email.trim(),
        firstName: firstName.trim(),
        lastName: lastName.trim(),
        gitHandle: orClear(gitHandle),
        deliveryChannel,
        // Round-trip all provider handles so unshown (unconnected) ones are
        // preserved; connected ones carry the user's edits.
        slackUserId: orClear(bindings.slack),
        discordUserId: orClear(bindings.discord),
        telegramChatId: orClear(bindings.telegram),
        teamsUserId: orClear(bindings.teams),
      })
      setDisplayName(res.member?.displayName ?? displayName)
      setEmail(res.member?.email ?? email)
      toast.success('Profile updated')
    } catch (e) {
      toast.error(toastMessage(e))
    } finally {
      setSaving(false)
    }
  }

  return (
    <Card>
      <div style={cardTitle}>Profile</div>
      <p style={cardSub}>
        Who you are for your teammates — and for the agent, which uses these fields to route questions to the right
        person.
      </p>
      <div style={{ marginTop: 20 }}>
        {loading ? (
          <Spinner />
        ) : error ? (
          <ErrorBanner error={error} />
        ) : (
          <div style={{ display: 'flex', flexDirection: 'column', gap: 16 }}>
            <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 14 }}>
              <Input
                label="First name"
                placeholder="Ellen"
                value={firstName}
                onChange={e => setFirstName(e.target.value)}
              />
              <Input
                label="Last name"
                placeholder="Ripley"
                value={lastName}
                onChange={e => setLastName(e.target.value)}
              />
            </div>
            <Input
              label="Display name"
              placeholder="How your name shows up in conversations"
              value={displayName}
              onChange={e => setDisplayName(e.target.value)}
            />
            <Input
              label="Email"
              type="email"
              placeholder="you@company.com"
              value={email}
              onChange={e => setEmail(e.target.value)}
              hint="Where invitations and notifications reach you. Independent from your login."
            />
            <Input
              label="Git handle"
              placeholder="nostromo"
              value={gitHandle}
              onChange={e => setGitHandle(e.target.value)}
              hint="Your GitHub/GitLab username — lets the agent match commits to you when picking a teammate."
              style={{ fontFamily: T.mono, fontSize: 13 }}
            />

            <div style={{ borderTop: `1px solid ${T.borderSubtle}`, paddingTop: 16 }}>
              <div style={{ fontSize: 13.5, fontWeight: 600, color: '#3A414D' }}>Where teammates reach you</div>
              <p style={{ fontSize: 12.5, color: T.subtle, margin: '3px 0 0', lineHeight: 1.5 }}>
                Your handle on each connected provider — so teammates can @mention you and the agent can DM you there.
              </p>

              <div style={{ marginTop: 16 }}>
                <div style={{ fontSize: 12.5, fontWeight: 600, color: '#3A414D', marginBottom: 7 }}>Deliver questions to me on</div>
                <div style={{ display: 'flex', gap: 7, flexWrap: 'wrap' }}>
                  {DELIVERY_CHANNELS.filter(c => c === 'dashboard' || connected.includes(c)).map(c => {
                    const on = deliveryChannel === c
                    return (
                      <button
                        key={c}
                        type="button"
                        onClick={() => setDeliveryChannel(c)}
                        style={{
                          cursor: 'pointer',
                          fontSize: 13,
                          fontWeight: 600,
                          fontFamily: 'inherit',
                          padding: '7px 14px',
                          borderRadius: 9,
                          border: `1px solid ${on ? T.accent : T.border}`,
                          background: on ? T.accentSoft : T.surface,
                          color: on ? T.accentHover : T.subtle,
                        }}
                      >
                        {CHANNEL_LABEL[c] ?? c}
                      </button>
                    )
                  })}
                </div>
                <p style={{ fontSize: 12.5, color: T.subtle, margin: '8px 0 0', lineHeight: 1.5 }}>
                  Where Orako sends you a question a teammate's agent routes to you. Dashboard = your Inbox here; pick a
                  messaging app to get DMed instead.
                </p>
                {deliveryChannel !== 'dashboard' && !bindings[deliveryChannel as ProviderKind]?.trim() && (
                  <p style={{ fontSize: 12, color: T.warn, margin: '6px 0 0', lineHeight: 1.5 }}>
                    Set your {CHANNEL_LABEL[deliveryChannel]} handle below and save, or Orako can't reach you there.
                  </p>
                )}
              </div>

              {connected.length === 0 ? (
                <div
                  style={{
                    marginTop: 12,
                    fontSize: 13,
                    color: T.muted,
                    background: T.surfaceAlt,
                    border: `1px solid ${T.borderSubtle}`,
                    borderRadius: T.rMd,
                    padding: '11px 13px',
                    lineHeight: 1.5,
                  }}
                >
                  No messaging provider connected yet. Once an admin connects Slack, Discord or Teams in{' '}
                  <strong style={{ color: T.body }}>Integrations</strong>, add your handle here so teammates and the
                  agent can reach you there.
                </div>
              ) : (
                <div style={{ display: 'flex', flexDirection: 'column', gap: 14, marginTop: 14 }}>
                  {PROVIDER_FIELDS.filter(f => connected.includes(f.kind)).map(f => (
                    <div key={f.kind}>
                      <Input
                        label={f.label}
                        placeholder={f.placeholder}
                        value={bindings[f.kind]}
                        onChange={e => setBindings(b => ({ ...b, [f.kind]: e.target.value }))}
                        hint={f.hint}
                        style={{ fontFamily: T.mono, fontSize: 13 }}
                      />
                      {f.kind === 'discord' && <DiscordIdHint />}
                    </div>
                  ))}
                </div>
              )}
            </div>

            <div>
              <Button
                variant="primary"
                loading={saving}
                disabled={!displayName.trim() && !firstName.trim() && !email.trim()}
                onClick={handleSave}
              >
                Save changes
              </Button>
            </div>
          </div>
        )}
      </div>
    </Card>
  )
}

function DevTokenCard() {
  const toast = useToast()
  const token = loadToken()

  async function handleCopy() {
    if (!token) return
    try {
      await navigator.clipboard.writeText(token)
      toast.success('Token copied')
    } catch (e) {
      toast.error(toastMessage(e))
    }
  }

  return (
    <Card>
      <div style={cardTitle}>Developer token</div>
      <p style={cardSub}>
        The dev-stub Bearer token this session authenticates with (
        <code style={{ fontFamily: T.mono, fontSize: 12.5 }}>memberID:projectID:role</code>).
      </p>
      <div style={{ display: 'flex', alignItems: 'center', gap: 10, marginTop: 16 }}>
        <div
          style={{
            flex: 1,
            fontFamily: T.mono,
            fontSize: 13,
            color: T.body,
            background: T.bg,
            border: `1px solid ${T.border}`,
            borderRadius: T.rMd,
            padding: '11px 13px',
            wordBreak: 'break-all',
            lineHeight: 1.5,
          }}
        >
          {token ?? '—'}
        </div>
        <Button variant="secondary" onClick={handleCopy} disabled={!token}>
          <Icon name="copy" size={15} color={T.muted} />
          Copy
        </Button>
      </div>
    </Card>
  )
}

function SessionCard() {
  const navigate = useNavigate()

  async function handleSignOut() {
    if (isOidc) {
      await supabase?.auth.signOut()
    } else {
      clearToken()
    }
    navigate('/auth')
  }

  return (
    <Card>
      <div style={cardTitle}>Session</div>
      <p style={cardSub}>Sign out of Orako on this device.</p>
      <div style={{ marginTop: 16 }}>
        <Button variant="danger" onClick={handleSignOut}>
          <Icon name="logout" size={15} color={T.dangerInk} />
          Sign out
        </Button>
      </div>
    </Card>
  )
}
