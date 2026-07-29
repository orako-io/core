// Manage a connected messaging provider (mockup 5a — healthy state only).
// The server never returns stored secrets, so credentials render as a single
// masked value with a "Replace" action that re-runs the connect stepper.
// Alert channels are editable in place via ConfigureProvider with an empty
// credentials map (api.updateProviderAlertChannels) — no secrets required.
//
// Mockup 5b (binding failing / last-error) is intentionally omitted: the API
// has no binding-health signal to back it, and inventing one would be a lie.

import { useCallback, useEffect, useState } from 'react'
import { useNavigate, useParams } from 'react-router'
import { useIdentity } from '../lib/identity'
import { useToast, toastMessage } from '../lib/toast'
import { api, communityInvites, type ConnectedChannel } from '../lib/client'
import { isProviderKind, PROVIDERS } from '../lib/providers'
import { ProviderLogo, providerTileBg } from '../components/ProviderLogos'
import { Page } from '../components/Layout'
import { Button } from '../components/Button'
import { Modal } from '../components/Modal'
import { Spinner } from '../components/Spinner'
import { ErrorBanner } from '../components/ErrorBanner'
import { SettingsCard, CardHeader, Divider } from '../components/Settings'
import { T } from '../lib/theme'

function parseChannels(v: string): string[] {
  return v
    .split(',')
    .map(s => s.trim())
    .filter(Boolean)
}

const textInputStyle: React.CSSProperties = {
  width: '100%',
  boxSizing: 'border-box',
  height: 42,
  border: `1px solid ${T.borderStrong}`,
  borderRadius: T.rMd,
  padding: '0 13px',
  fontSize: 13.5,
  fontFamily: 'inherit',
  color: T.body,
  background: T.surfaceAlt,
}

export function IntegrationsManagePage() {
  const { kind = '' } = useParams<{ kind: string }>()
  const navigate = useNavigate()
  const { selectedProjectId: projectId } = useIdentity()
  const toast = useToast()

  const validKind = isProviderKind(kind)

  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<unknown>(null)
  const [entry, setEntry] = useState<ConnectedChannel | null>(null)
  const [channelsInput, setChannelsInput] = useState('')
  const [saving, setSaving] = useState(false)
  const [disconnecting, setDisconnecting] = useState(false)
  const [confirmDisconnect, setConfirmDisconnect] = useState(false)
  const [oauthSecret, setOauthSecret] = useState('')
  const [savingSecret, setSavingSecret] = useState(false)
  const [inviteUrl, setInviteUrl] = useState('')
  const [savingInvite, setSavingInvite] = useState(false)

  const load = useCallback(async () => {
    setLoading(true)
    setError(null)
    try {
      const [res, invites] = await Promise.all([api.listConnectedChannels(), communityInvites()])
      const found = (res.connected ?? []).find(c => c.kind === kind) ?? null
      setEntry(found)
      setChannelsInput(found ? (found.alertChannelIds ?? []).join(', ') : '')
      setInviteUrl((kind === 'discord' ? invites.discord : kind === 'slack' ? invites.slack : '') ?? '')
    } catch (e) {
      setError(e)
    } finally {
      setLoading(false)
    }
  }, [kind])

  // Guard first: an unknown kind in the URL bounces straight back.
  useEffect(() => {
    if (!validKind) {
      navigate('/integrations')
      return
    }
    void load()
  }, [validKind, load, navigate])

  // Nothing to manage yet (never configured) → send them through setup.
  useEffect(() => {
    if (validKind && !loading && !error && !entry) {
      navigate(`/integrations/connect/${kind}`)
    }
  }, [validKind, loading, error, entry, kind, navigate])

  if (!validKind) return null
  const def = PROVIDERS[kind]

  async function handleSaveChannels() {
    if (!projectId) return
    setSaving(true)
    try {
      await api.updateProviderAlertChannels(projectId, kind, parseChannels(channelsInput))
      toast.success('Alert channels updated.')
      await load()
    } catch (e) {
      toast.error(toastMessage(e))
    } finally {
      setSaving(false)
    }
  }

  // Discord only: add or rotate the OAuth2 client secret WITHOUT re-entering the
  // bot token. The server merges a partial credential set over the stored one, so
  // sending just { client_secret } preserves bot_token.
  async function handleSaveOAuthSecret() {
    if (!projectId) return
    const secret = oauthSecret.trim()
    if (!secret) return
    setSavingSecret(true)
    try {
      await api.configureProvider(projectId, kind, { client_secret: secret })
      toast.success('OAuth2 client secret saved.')
      setOauthSecret('')
      await load()
    } catch (e) {
      toast.error(toastMessage(e))
    } finally {
      setSavingSecret(false)
    }
  }

  // Community invite link (Discord/Slack, optional): stored as a credential-map
  // key merged over the existing secrets, so sending just { community_invite_url }
  // leaves the bot token untouched. Shown to new members during onboarding.
  async function handleSaveInvite() {
    if (!projectId) return
    setSavingInvite(true)
    try {
      await api.configureProvider(projectId, kind, { community_invite_url: inviteUrl.trim() })
      toast.success('Community invite link saved.')
      await load()
    } catch (e) {
      toast.error(toastMessage(e))
    } finally {
      setSavingInvite(false)
    }
  }

  async function handleDisconnect() {
    if (!projectId) return
    setDisconnecting(true)
    try {
      await api.disconnectProvider(projectId, kind)
      toast.success(`${def.label} disconnected.`)
      navigate('/integrations')
    } catch (e) {
      toast.error(toastMessage(e))
      setDisconnecting(false)
      setConfirmDisconnect(false)
    }
  }

  return (
    <Page width={720}>
      <div
        onClick={() => navigate('/integrations')}
        style={{
          display: 'inline-flex',
          alignItems: 'center',
          gap: 6,
          fontSize: 13.5,
          color: T.subtle,
          cursor: 'pointer',
          marginBottom: 22,
        }}
      >
        <span aria-hidden="true">←</span> Integrations
      </div>

      {error != null && <ErrorBanner error={error} />}

      {loading ? (
        <div style={{ display: 'flex', justifyContent: 'center', padding: 60 }}>
          <Spinner />
        </div>
      ) : (
        entry && (
          <div style={{ display: 'flex', flexDirection: 'column', gap: 16 }}>
            {/* Header */}
            <div style={{ display: 'flex', alignItems: 'center', gap: 16 }}>
              <div
                style={{
                  width: 52,
                  height: 52,
                  borderRadius: 14,
                  background: providerTileBg(kind),
                  display: 'flex',
                  alignItems: 'center',
                  justifyContent: 'center',
                  flex: 'none',
                }}
              >
                <ProviderLogo kind={kind} size={28} />
              </div>
              <div style={{ flex: 1 }}>
                <div style={{ display: 'flex', alignItems: 'center', gap: 10 }}>
                  <h1 style={{ fontSize: 22, fontWeight: 700, letterSpacing: '-.02em', color: T.text, margin: 0 }}>
                    {def.label}
                  </h1>
                  <span
                    style={{
                      display: 'inline-flex',
                      alignItems: 'center',
                      gap: 5,
                      fontSize: 12,
                      fontWeight: 600,
                      color: T.successInk,
                      background: T.successBg,
                      border: `1px solid ${T.successBorder}`,
                      padding: '3px 10px',
                      borderRadius: 20,
                    }}
                  >
                    <span style={{ width: 6, height: 6, borderRadius: '50%', background: T.success }} />
                    Connected
                  </span>
                </div>
              </div>
              <Button variant="danger" onClick={() => setConfirmDisconnect(true)} loading={disconnecting}>
                Disconnect
              </Button>
            </div>

            {/* Credentials */}
            <SettingsCard>
              <CardHeader icon="lock" title="Credentials" sub="Never shown after saving" />
              <Divider />
              <div style={{ display: 'flex', alignItems: 'center', gap: 12 }}>
                <code
                  style={{
                    flex: 1,
                    fontSize: 13,
                    color: T.subtle,
                    background: T.surfaceAlt,
                    border: `1px solid ${T.borderSubtle}`,
                    borderRadius: T.rMd,
                    padding: '9px 13px',
                    fontFamily: T.mono,
                  }}
                >
                  ••••••••••
                </code>
                <Button variant="secondary" onClick={() => navigate(`/integrations/connect/${kind}`)}>
                  Replace
                </Button>
              </div>
              <p style={{ fontSize: 12, color: T.faint, marginTop: 12, lineHeight: 1.5 }}>
                Credentials are stored securely and never shown. Replace re-runs setup.
              </p>
            </SettingsCard>

            {/* Bot permissions (Discord): re-run the OAuth invite to grant
                newly required permissions to the already-installed bot —
                Discord updates the existing bot's role, no duplicate. */}
            {kind === 'discord' && entry.botClientId && (
              <SettingsCard>
                <CardHeader icon="shield" title="Bot permissions" sub="Re-run the server invite when Orako needs new permissions" />
                <Divider />
                <p style={{ fontSize: 13, color: T.muted, margin: '0 0 14px', lineHeight: 1.55 }}>
                  Orako opens a private discussion thread per pool question, mirrors messages under each
                  member's identity, and — on one-click “Connect Discord” — adds a teammate to your server.
                  That needs “Create Instant Invite”, “Create Private Threads”, “Send Messages in Threads”,
                  “Manage Threads” and “Manage Webhooks” on top of the original invite. Re-authorizing on
                  the same server updates the bot in place.
                </p>
                <a
                  href={`https://discord.com/oauth2/authorize?client_id=${entry.botClientId}&scope=bot&permissions=361314126849`}
                  target="_blank"
                  rel="noreferrer"
                  style={{
                    display: 'inline-flex',
                    alignItems: 'center',
                    gap: 8,
                    height: 40,
                    padding: '0 18px',
                    borderRadius: T.rMd,
                    background: def.brand ?? T.accent,
                    color: '#fff',
                    fontSize: 13.5,
                    fontWeight: 600,
                    textDecoration: 'none',
                  }}
                >
                  Update bot permissions on your server ↗
                </a>
              </SettingsCard>
            )}

            {/* 1-click connect (Discord OAuth2): set/rotate ONLY the client
                secret. The merge on the server preserves the bot token, so the
                admin never re-enters it. */}
            {kind === 'discord' && (
              <SettingsCard>
                <CardHeader
                  icon="lock"
                  title="1-click connect (OAuth2)"
                  sub="Let teammates self-connect Discord without pasting a channel ID"
                />
                <Divider />
                <p style={{ fontSize: 13, color: T.muted, margin: '0 0 14px', lineHeight: 1.55 }}>
                  Add your app's OAuth2 client secret to enable the “Connect Discord” button on each
                  teammate's onboarding. In the{' '}
                  <a
                    href="https://discord.com/developers/applications"
                    target="_blank"
                    rel="noreferrer"
                    style={{ color: T.accent, textDecoration: 'underline' }}
                  >
                    Discord Developer Portal
                  </a>{' '}
                  → your app → <strong>OAuth2</strong>: register the redirect{' '}
                  <code style={{ fontFamily: T.mono, fontSize: 12 }}>
                    {window.location.origin}/discord/oauth/callback
                  </code>
                  , then <strong>Reset Secret</strong>, copy it, and paste it here. The bot token you
                  already saved stays untouched.
                </p>
                <input
                  type="password"
                  autoComplete="off"
                  value={oauthSecret}
                  onChange={e => setOauthSecret(e.target.value)}
                  placeholder="OAuth2 client secret"
                  disabled={savingSecret}
                  style={textInputStyle}
                />
                <div style={{ display: 'flex', alignItems: 'center', gap: 12, marginTop: 14 }}>
                  <Button
                    variant="primary"
                    onClick={handleSaveOAuthSecret}
                    loading={savingSecret}
                    disabled={!oauthSecret.trim()}
                  >
                    Save secret
                  </Button>
                  <span style={{ fontSize: 12.5, color: T.faint }}>
                    Updates only the OAuth2 secret — the bot token stays untouched.
                  </span>
                </div>
              </SettingsCard>
            )}

            {/* Community invite link (Discord/Slack): shown to new members so
                they can join the org's server, which earns them a private thread
                instead of a DM. Optional. */}
            {(kind === 'discord' || kind === 'slack') && (
              <SettingsCard>
                <CardHeader
                  icon="users"
                  title="Community invite link (optional)"
                  sub={`Shown to new members so they can join your ${def.label} server.`}
                />
                <Divider />
                <input
                  type="url"
                  autoComplete="off"
                  value={inviteUrl}
                  onChange={e => setInviteUrl(e.target.value)}
                  placeholder={kind === 'discord' ? 'https://discord.gg/…' : 'https://join.slack.com/t/…'}
                  disabled={savingInvite}
                  style={textInputStyle}
                />
                <div style={{ display: 'flex', alignItems: 'center', gap: 12, marginTop: 14 }}>
                  <Button variant="primary" onClick={handleSaveInvite} loading={savingInvite}>
                    Save
                  </Button>
                  <span style={{ fontSize: 12.5, color: T.faint }}>
                    New members see a “Join our {def.label}” button during onboarding.
                  </span>
                </div>
              </SettingsCard>
            )}

            {/* Alert channels */}
            <SettingsCard>
              <CardHeader icon="send" title="Alert channels" sub="Editable without re-entering secrets" />
              <Divider />
              <input
                type="text"
                autoComplete="off"
                value={channelsInput}
                onChange={e => setChannelsInput(e.target.value)}
                placeholder="#orako-alerts, #eng-escalations"
                disabled={saving}
                style={textInputStyle}
              />
              <div style={{ display: 'flex', alignItems: 'center', gap: 12, marginTop: 14 }}>
                <Button variant="primary" onClick={handleSaveChannels} loading={saving}>
                  Save
                </Button>
                <span style={{ fontSize: 12.5, color: T.faint }}>
                  Updates only the channel list — secrets stay untouched.
                </span>
              </div>
            </SettingsCard>
          </div>
        )
      )}

      <Modal
        open={confirmDisconnect}
        onClose={() => { if (!disconnecting) setConfirmDisconnect(false) }}
        title={`Disconnect ${def.label}?`}
      >
        <p style={{ fontSize: 13.5, color: T.body, lineHeight: 1.55, margin: 0 }}>
          Teammates will stop receiving questions on {def.label}. You can reconnect it later.
        </p>
        <div style={{ display: 'flex', justifyContent: 'flex-end', gap: 10, marginTop: 20 }}>
          <Button variant="secondary" disabled={disconnecting} onClick={() => setConfirmDisconnect(false)}>Cancel</Button>
          <Button variant="danger" loading={disconnecting} onClick={handleDisconnect}>Disconnect</Button>
        </div>
      </Modal>
    </Page>
  )
}
