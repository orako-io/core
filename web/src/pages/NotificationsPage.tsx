// Notifications (mockup 6a) — "Where should we reach you?". A member picks the
// channel an agent pings them on: GetMember (current), ListConnectedChannels
// (availability), UpdateMember (save). Dashboard is always available; Teams is
// shown disabled until it ships.

import { useCallback, useEffect, useState, type ReactNode } from 'react'
import { api, connectDiscord, type Member } from '../lib/client'
import { Card, Page } from '../components/Layout'
import { Button } from '../components/Button'
import { ErrorBanner } from '../components/ErrorBanner'
import { Spinner } from '../components/Spinner'
import { Icon } from '../components/Icon'
import { DiscordIdHint } from '../components/DiscordIdHint'
import { T } from '../lib/theme'
import { useToast } from '../lib/toast'

type ChannelKey = 'dashboard' | 'slack' | 'telegram' | 'teams' | 'discord'

const slackLogo = (
  <svg width="22" height="22" viewBox="0 0 24 24">
    <path fill="#36C5F0" d="M9 6a2 2 0 1 1 2 2H9z" />
    <path fill="#2EB67D" d="M18 9a2 2 0 1 1 2 2h-2zm-1 0a2 2 0 0 1-4 0V4a2 2 0 1 1 4 0z" />
    <path fill="#ECB22E" d="M15 18a2 2 0 1 1-2-2h2zm0-1a2 2 0 0 1 0-4h5a2 2 0 1 1 0 4z" />
    <path fill="#E01E5A" d="M6 15a2 2 0 1 1-2-2h2zm1 0a2 2 0 0 1 4 0v5a2 2 0 1 1-4 0z" />
    <path fill="#36C5F0" d="M9 7a2 2 0 0 1 0-4H4a2 2 0 1 0 0 4z" />
  </svg>
)

const telegramLogo = (
  <svg width="26" height="26" viewBox="0 0 24 24">
    <circle cx="12" cy="12" r="12" fill="#29A9EA" />
    <path
      d="M5.5 11.8l12-4.6c.6-.2 1 .1.8 1l-2 9.6c-.1.6-.5.8-1 .5l-2.8-2-1.4 1.3c-.2.2-.3.3-.6.3l.2-3 5.2-4.7c.2-.2 0-.3-.3-.1l-6.4 4-2.8-.9c-.6-.2-.6-.6.1-.9z"
      fill="#fff"
    />
  </svg>
)

const teamsLogo = (
  <svg width="22" height="22" viewBox="0 0 24 24">
    <rect x="2" y="6" width="13" height="12" rx="2.5" fill="#5059C9" />
    <circle cx="18" cy="8" r="3" fill="#7B83EB" />
    <path d="M15 11h6a1 1 0 0 1 1 1v4a3.5 3.5 0 0 1-7 0z" fill="#7B83EB" />
  </svg>
)

const discordLogo = (
  <svg width="22" height="22" viewBox="0 0 24 24" fill="#5865F2">
    <path d="M20.3 5.4A17.8 17.8 0 0 0 15.9 4c-.2.4-.5.9-.6 1.3a16.5 16.5 0 0 0-4.6 0A9 9 0 0 0 10 4a17.9 17.9 0 0 0-4.4 1.4C2.9 9.3 2.2 13 2.5 16.7a18 18 0 0 0 5.5 2.8c.4-.6.8-1.3 1.1-2a11.6 11.6 0 0 1-1.8-.9l.4-.3a12.9 12.9 0 0 0 10.6 0l.4.3c-.6.3-1.2.6-1.8.9.3.7.7 1.4 1.1 2a18 18 0 0 0 5.5-2.8c.4-4.3-.7-8-2.7-11.3zM9.7 14.3c-.9 0-1.6-.8-1.6-1.8s.7-1.8 1.6-1.8 1.6.8 1.6 1.8-.7 1.8-1.6 1.8zm5.6 0c-.9 0-1.6-.8-1.6-1.8s.7-1.8 1.6-1.8 1.6.8 1.6 1.8-.7 1.8-1.6 1.8z" />
  </svg>
)

const CHANNELS: {
  key: ChannelKey
  label: string
  logo: ReactNode
  tileBg: string
  bindingLabel?: string
  bindingField?: 'slackUserId' | 'telegramChatId' | 'teamsUserId' | 'discordUserId'
  bindingPlaceholder?: string
}[] = [
  { key: 'slack', label: 'Slack', logo: slackLogo, tileBg: '#F7F5F8', bindingLabel: 'Your Slack user ID', bindingField: 'slackUserId', bindingPlaceholder: 'U01ABC23DEF' },
  { key: 'telegram', label: 'Telegram', logo: telegramLogo, tileBg: '#EAF6FD', bindingLabel: 'Your Telegram chat ID', bindingField: 'telegramChatId', bindingPlaceholder: '123456789' },
  { key: 'teams', label: 'Microsoft Teams', logo: teamsLogo, tileBg: '#F1F0FA', bindingLabel: 'Your Teams AAD object ID', bindingField: 'teamsUserId', bindingPlaceholder: '29:1a2b3c4d...' },
  { key: 'discord', label: 'Discord', logo: discordLogo, tileBg: '#EEF0FE', bindingLabel: 'Your Discord user ID', bindingField: 'discordUserId', bindingPlaceholder: '123456789012345678' },
  { key: 'dashboard', label: 'Dashboard', logo: <Icon name="inbox" size={20} strokeWidth={1.9} color={T.accent} />, tileBg: T.accentSoft },
]

export function NotificationsPage() {
  const toast = useToast()

  const [member, setMember] = useState<Member | null>(null)
  const [connected, setConnected] = useState<string[]>([])
  const [loading, setLoading] = useState(true)
  const [loadError, setLoadError] = useState<unknown>(null)

  const [selected, setSelected] = useState<ChannelKey>('dashboard')
  const [slackUserId, setSlackUserId] = useState('')
  const [telegramChatId, setTelegramChatId] = useState('')
  const [teamsUserId, setTeamsUserId] = useState('')
  const [discordUserId, setDiscordUserId] = useState('')

  const [saving, setSaving] = useState(false)
  const [saveError, setSaveError] = useState<unknown>(null)

  const load = useCallback(async () => {
    setLoading(true)
    setLoadError(null)
    try {
      const [m, c] = await Promise.all([api.getMember(), api.listConnectedChannels()])
      setMember(m.member)
      setConnected(c.channels ?? [])
      setSelected((m.member.deliveryChannel as ChannelKey) || 'dashboard')
      setSlackUserId(m.member.slackUserId ?? '')
      setTelegramChatId(m.member.telegramChatId ?? '')
      setTeamsUserId(m.member.teamsUserId ?? '')
      setDiscordUserId(m.member.discordUserId ?? '')
    } catch (e) {
      setLoadError(e)
    } finally {
      setLoading(false)
    }
  }, [])

  useEffect(() => {
    load()
  }, [load])

  // A channel is available if the org connected it (dashboard is always in the
  // ListConnectedChannels result; teams never is until it ships).
  function available(key: ChannelKey): boolean {
    return connected.includes(key)
  }

  async function save() {
    setSaving(true)
    setSaveError(null)
    try {
      const res = await api.updateMember({
        deliveryChannel: selected,
        slackUserId: selected === 'slack' ? slackUserId.trim() : undefined,
        telegramChatId: selected === 'telegram' ? telegramChatId.trim() : undefined,
        teamsUserId: selected === 'teams' ? teamsUserId.trim() : undefined,
        discordUserId: selected === 'discord' ? discordUserId.trim() : undefined,
      })
      setMember(res.member)
      toast.success('Contact channel saved.')
    } catch (e) {
      setSaveError(e)
    } finally {
      setSaving(false)
    }
  }

  function sub(key: ChannelKey): ReactNode {
    switch (key) {
      case 'slack':
        return available('slack') ? (
          <>
            Connected as <strong style={{ color: '#3A414D' }}>@{member?.slackUserId || 'you'}</strong> · your Slack
            workspace
          </>
        ) : (
          'Get pinged in Slack and answer in the thread.'
        )
      case 'telegram':
        return 'Link your Telegram to get questions there.'
      case 'teams':
        return available('teams') ? 'Get pinged in Teams and answer in the chat.' : 'Not connected by your organization yet.'
      case 'discord':
        return available('discord') ? 'Get pinged as a Discord DM and answer there.' : 'Not connected by your organization yet.'
      case 'dashboard':
        return (
          <>
            Questions land in your Orako <strong style={{ color: '#3A414D' }}>Inbox</strong>. Always available — no
            setup.
          </>
        )
    }
  }

  return (
    <Page width={1060}>
      <div style={{ display: 'flex', gap: 36, alignItems: 'flex-start' }}>
        <div style={{ flex: 1, minWidth: 0, maxWidth: 680 }}>
      <h2 style={{ fontSize: 20, fontWeight: 700, letterSpacing: '-.02em', color: T.text, margin: 0 }}>
        Where should we reach you?
      </h2>
      <p style={{ fontSize: 14, color: T.muted, marginTop: 6 }}>
        When an agent needs your expertise, Orako pings you here. Pick one channel — you can change it anytime.
      </p>

      {loadError != null && (
        <div style={{ marginTop: 20 }}>
          <ErrorBanner error={loadError} />
        </div>
      )}

      {loading ? (
        <div style={{ color: T.muted, fontSize: 13, marginTop: 22 }}>
          <Spinner size={14} /> Loading…
        </div>
      ) : (
        <>
          <div style={{ display: 'flex', flexDirection: 'column', gap: 12, marginTop: 22 }}>
            {CHANNELS.map(ch => {
              const disabled = !available(ch.key)
              const isSelected = selected === ch.key
              return (
                <button
                  key={ch.key}
                  disabled={disabled}
                  onClick={() => !disabled && setSelected(ch.key)}
                  style={{
                    display: 'flex',
                    alignItems: 'center',
                    gap: 14,
                    textAlign: 'left',
                    width: '100%',
                    fontFamily: 'inherit',
                    background: disabled ? T.surfaceAlt : T.surface,
                    border: isSelected ? `1.5px solid ${T.accent}` : `1px solid ${disabled ? T.borderSubtle : T.border}`,
                    borderRadius: 13,
                    padding: '16px 18px',
                    cursor: disabled ? 'not-allowed' : 'pointer',
                    opacity: disabled ? 0.65 : 1,
                    boxShadow: isSelected ? T.shadowPop : 'none',
                  }}
                >
                  <div
                    style={{
                      width: 40,
                      height: 40,
                      borderRadius: 11,
                      background: ch.tileBg,
                      display: 'flex',
                      alignItems: 'center',
                      justifyContent: 'center',
                      flex: 'none',
                    }}
                  >
                    {ch.logo}
                  </div>
                  <div style={{ flex: 1, minWidth: 0 }}>
                    <div style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
                      <span style={{ fontSize: 15, fontWeight: 600, color: T.text }}>{ch.label}</span>
                      {ch.key === 'slack' && (
                        <span
                          style={{
                            fontSize: 11,
                            fontWeight: 600,
                            color: T.accent,
                            background: T.accentSoft,
                            padding: '2px 8px',
                            borderRadius: 10,
                          }}
                        >
                          Recommended
                        </span>
                      )}
                    </div>
                    <div style={{ fontSize: 13, color: T.subtle, marginTop: 3 }}>{sub(ch.key)}</div>
                  </div>
                  {isSelected ? (
                    <div style={{ width: 20, height: 20, borderRadius: '50%', border: `6px solid ${T.accent}`, background: '#fff', flex: 'none' }} />
                  ) : (
                    <div style={{ width: 20, height: 20, borderRadius: '50%', border: '1.5px solid #D5D8DE', background: '#fff', flex: 'none' }} />
                  )}
                </button>
              )
            })}
          </div>

          {/* Binding input for the selected external channel */}
          {(() => {
            const ch = CHANNELS.find(c => c.key === selected)
            if (!ch?.bindingField) return null
            const bindings: Record<NonNullable<typeof ch.bindingField>, [string, (v: string) => void]> = {
              slackUserId: [slackUserId, setSlackUserId],
              telegramChatId: [telegramChatId, setTelegramChatId],
              teamsUserId: [teamsUserId, setTeamsUserId],
              discordUserId: [discordUserId, setDiscordUserId],
            }
            const [value, setValue] = bindings[ch.bindingField]
            return (
              <Card style={{ marginTop: 16, padding: 18 }}>
                {ch.bindingField === 'discordUserId' && (
                  <div style={{ marginBottom: 14 }}>
                    <Button
                      variant="primary"
                      size="sm"
                      onClick={() => {
                        connectDiscord().catch(() => toast.error('Discord connect is not available for this project yet.'))
                      }}
                    >
                      Connect Discord (1 click)
                    </Button>
                    <div style={{ fontSize: 12, color: T.subtle, marginTop: 8 }}>
                      One click links your account — no ID to find. Or paste it manually below.
                    </div>
                  </div>
                )}
                <label style={{ display: 'block', fontSize: 13, fontWeight: 500, color: '#3A414D', marginBottom: 6 }}>
                  {ch.bindingLabel}
                </label>
                <input
                  type="text"
                  value={value}
                  onChange={e => setValue(e.target.value)}
                  placeholder={ch.bindingPlaceholder}
                  style={{
                    width: '100%',
                    background: T.surface,
                    border: `1px solid ${T.borderStrong}`,
                    borderRadius: T.rMd,
                    color: T.body,
                    padding: '10px 13px',
                    fontFamily: T.mono,
                    fontSize: 13,
                  }}
                />
                <div style={{ fontSize: 12, color: T.subtle, marginTop: 8 }}>
                  Required so we can route questions to your {ch.label} account.
                </div>
                {ch.bindingField === 'discordUserId' && <DiscordIdHint />}
              </Card>
            )
          })()}

          {member?.bindingError && (
            <div
              style={{
                display: 'flex',
                alignItems: 'flex-start',
                gap: 9,
                marginTop: 16,
                background: T.warnBg,
                border: `1px solid ${T.warnBorder}`,
                borderRadius: 10,
                padding: '11px 14px',
                fontSize: 13,
                color: T.warn,
                lineHeight: 1.5,
              }}
            >
              <span aria-hidden style={{ flex: 'none' }}>⚠</span>
              <span>
                Last delivery to your bound channel failed: <strong>{member.bindingError}</strong>. Re-save your
                channel above to clear this warning once it's fixed.
              </span>
            </div>
          )}

          {saveError != null && (
            <div style={{ marginTop: 16 }}>
              <ErrorBanner error={saveError} />
            </div>
          )}

          <div style={{ marginTop: 22 }}>
            <Button variant="primary" loading={saving} onClick={save}>
              <Icon name="check" size={15} color="#fff" />
              Save channel
            </Button>
          </div>
        </>
      )}
        </div>

        <HowItWorksAside />
      </div>
    </Page>
  )
}

const HOW_IT_WORKS = [
  {
    title: 'A question lands in your chat',
    body: 'An AI assistant sends you a short question on the channel you picked when it needs your expertise.',
  },
  {
    title: 'You reply like normal',
    body: 'Just type your answer back. No special commands, no jargon — a sentence or two is plenty.',
  },
  {
    title: 'Your answer is saved & reused',
    body: 'Orako remembers it, so nobody has to ask you the same thing twice.',
  },
]

function HowItWorksAside() {
  return (
    <div style={{ width: 300, flex: 'none' }}>
      <span
        style={{
          display: 'inline-flex',
          alignItems: 'center',
          gap: 6,
          background: '#EDF5EC',
          border: '1px solid #CFE6CC',
          padding: '5px 11px',
          borderRadius: 20,
          marginBottom: 12,
        }}
      >
        <span style={{ width: 6, height: 6, borderRadius: '50%', background: '#3A9A34' }} />
        <span style={{ fontSize: 12, fontWeight: 600, color: '#3A6B33' }}>You're a Teammate</span>
      </span>
      <h3 style={{ fontSize: 19, fontWeight: 700, letterSpacing: '-.02em', color: T.text, lineHeight: 1.25, margin: 0 }}>
        How Orako works for you
      </h3>
      <p style={{ fontSize: 13.5, lineHeight: 1.55, color: T.muted, marginTop: 8 }}>
        No apps to install, no setup. You just answer questions where you already chat.
      </p>
      <div style={{ display: 'flex', flexDirection: 'column', gap: 12, marginTop: 18 }}>
        {HOW_IT_WORKS.map((step, i) => (
          <div
            key={step.title}
            style={{
              background: T.surface,
              border: `1px solid ${T.border}`,
              borderRadius: 14,
              padding: '14px 15px',
              display: 'flex',
              gap: 12,
            }}
          >
            <div
              style={{
                width: 32,
                height: 32,
                borderRadius: 10,
                background: T.accentSoft,
                color: T.accent,
                display: 'flex',
                alignItems: 'center',
                justifyContent: 'center',
                flex: 'none',
                fontSize: 14,
                fontWeight: 700,
              }}
            >
              {i + 1}
            </div>
            <div>
              <div style={{ fontSize: 13.5, fontWeight: 700, color: T.text }}>{step.title}</div>
              <p style={{ fontSize: 12.5, lineHeight: 1.5, color: T.muted, marginTop: 3 }}>{step.body}</p>
            </div>
          </div>
        ))}
      </div>
      <p style={{ fontSize: 12.5, lineHeight: 1.6, color: T.muted, marginTop: 18 }}>
        Pick a channel on the left and save — that's the only step. Which apps are available here
        (Slack, Discord, Teams…) is managed by your org's admin in Integrations.
      </p>
    </div>
  )
}
