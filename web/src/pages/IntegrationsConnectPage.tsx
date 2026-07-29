// Guided multi-step connect wizard (mockups ir_slack1-3, ir_teams1-5,
// ir_discord1-3): a dedicated route that walks an org admin through wiring up
// one messaging provider — OAuth fast path or console/credential steps,
// then optional alert channels, then a test ping. Renders its own in-content
// topbar + left progress rail; the outer app icon-rail/sidebar (Layout) stay.
//
// Note: Layout always renders its own 60px header above `<main>` (it has no
// opt-out); this page does not call usePageHeader, so that header falls back
// to the default "Integrations" title. The breadcrumb + "Exit setup" bar
// built here is the one described in the mockups.

import { useEffect, useState } from 'react'
import { useNavigate, useParams } from 'react-router'
import { useIdentity } from '../lib/identity'
import { useToast, toastMessage } from '../lib/toast'
import { api, connectSlack, type ProviderTestResult } from '../lib/client'
import { inputStyle } from '../components/Layout'
import { Button } from '../components/Button'
import { Icon } from '../components/Icon'
import { ProviderLogo, providerTileBg } from '../components/ProviderLogos'
import { PROVIDERS, isProviderKind, type ProviderKind, type Step } from '../lib/providers'
import { T, labelStyle } from '../lib/theme'

const RAIL_W = 296

const cardStyle: React.CSSProperties = {
  background: T.surface,
  border: `1px solid ${T.border}`,
  borderRadius: 14,
  padding: 22,
}

const doThisLabelStyle: React.CSSProperties = {
  fontSize: 12,
  fontWeight: 700,
  letterSpacing: '.05em',
  textTransform: 'uppercase',
  color: T.subtle,
}

function credInputStyle(): React.CSSProperties {
  return { ...inputStyle(), fontFamily: T.mono, fontSize: 13, background: T.surfaceAlt }
}

// StepLink deep-links to the provider's console so the user never has to hunt
// for it. Opens in a new tab.
function StepLink({ link }: { link?: { label: string; url: string } }) {
  if (!link) return null
  return (
    <a
      href={link.url}
      target="_blank"
      rel="noreferrer"
      style={{
        display: 'inline-flex',
        alignItems: 'center',
        gap: 8,
        height: 42,
        padding: '0 16px',
        marginTop: 14,
        border: `1px solid ${T.accentBorder}`,
        borderRadius: 10,
        background: T.accentSoft,
        color: T.accent,
        fontSize: 13.5,
        fontWeight: 600,
        textDecoration: 'none',
        width: 'fit-content',
      }}
    >
      {link.label}
      <span style={{ fontSize: 15, lineHeight: 1 }}>↗</span>
    </a>
  )
}

// StepLinks renders the extra `links` array (a row of deep-link buttons).
function StepLinks({ links }: { links?: { label: string; url: string }[] }) {
  if (!links?.length) return null
  return (
    <div style={{ display: 'flex', flexWrap: 'wrap', gap: 10 }}>
      {links.map(l => (
        <StepLink key={l.url + l.label} link={l} />
      ))}
    </div>
  )
}

// StepBullets renders ordered sub-steps as a numbered list. Words wrapped in
// straight double quotes in the copy render as subtle inline chips for scanning.
function StepBullets({ bullets, accent }: { bullets?: string[]; accent?: string }) {
  if (!bullets?.length) return null
  const dot = accent ?? T.accent
  return (
    <ol style={{ margin: '16px 0 0', padding: 0, listStyle: 'none', display: 'flex', flexDirection: 'column', gap: 4 }}>
      {bullets.map((b, i) => (
        <li key={i} style={{ display: 'flex', gap: 13, alignItems: 'flex-start', padding: '9px 0', borderTop: i === 0 ? 'none' : `1px solid ${T.borderSubtle}` }}>
          <span
            style={{
              flex: 'none',
              width: 24,
              height: 24,
              borderRadius: '50%',
              background: dot,
              color: '#fff',
              fontSize: 12.5,
              fontWeight: 700,
              display: 'flex',
              alignItems: 'center',
              justifyContent: 'center',
              marginTop: 1,
              boxShadow: `0 2px 6px -2px ${dot}`,
            }}
          >
            {i + 1}
          </span>
          <span style={{ fontSize: 14, lineHeight: 1.6, color: T.body, paddingTop: 2 }}>{renderChips(b)}</span>
        </li>
      ))}
    </ol>
  )
}

// renderChips turns quoted segments (straight OR curly quotes) into subtle
// accent chips so multi-click instructions read at a glance — no markdown parser.
function renderChips(text: string): React.ReactNode {
  const parts = text.split(/[“”"]([^“”"]+)[“”"]/g)
  return parts.map((p, i) =>
    i % 2 === 1 ? (
      <code
        key={i}
        style={{ fontFamily: T.mono, fontSize: 12.5, fontWeight: 600, background: T.accentSofter, color: T.accentInk, padding: '1px 7px', borderRadius: 6, whiteSpace: 'nowrap' }}
      >
        {p}
      </code>
    ) : (
      <span key={i}>{p}</span>
    ),
  )
}

// botClientId decodes a Discord application (client) id from a bot token: its
// first dot-segment is the base64url-encoded app id. Returns null if it doesn't
// look like a snowflake, so the UI can fall back to manual instructions.
function botClientId(token?: string): string | null {
  if (!token) return null
  try {
    const decoded = atob(token.split('.')[0].replace(/-/g, '+').replace(/_/g, '/'))
    return /^\d{17,20}$/.test(decoded) ? decoded : null
  } catch {
    return null
  }
}

// BotInviteButton builds the exact OAuth2 invite URL from the saved token and
// opens it — the admin never touches Discord's URL Generator by hand.
// permissions=361314126849 = Create Instant Invite (1<<0) + View Channels (1024)
// + Send Messages (2048) + Manage Webhooks (1<<29) + Manage Threads (1<<34)
// + Create Private Threads (1<<36) + Send Messages in Threads (1<<38) — the thread
// bits back the per-conversation private discussion threads; Manage Webhooks backs
// the per-message identity mirroring; Create Instant Invite lets the bot add a
// teammate to the server on one-click "Connect Discord" (guilds.join).
function BotInviteButton({ token, brand }: { token?: string; brand?: string }) {
  const clientId = botClientId(token)
  const color = brand ?? T.accent
  if (!clientId) {
    return (
      <div style={{ marginTop: 16, padding: '12px 14px', background: T.warnBg, border: `1px solid ${T.warnBorder}`, borderRadius: 11, fontSize: 13, color: T.warn, lineHeight: 1.5 }}>
        Save your bot token in step 1 first — then this button builds the one-click invite link for you.
      </div>
    )
  }
  const url = `https://discord.com/oauth2/authorize?client_id=${clientId}&scope=bot&permissions=361314126849`
  return (
    <a
      href={url}
      target="_blank"
      rel="noreferrer"
      style={{
        display: 'inline-flex',
        alignItems: 'center',
        gap: 9,
        height: 48,
        padding: '0 20px',
        marginTop: 18,
        borderRadius: 12,
        background: color,
        color: '#fff',
        fontSize: 15,
        fontWeight: 600,
        textDecoration: 'none',
        boxShadow: `0 8px 20px -8px ${color}`,
        width: 'fit-content',
      }}
    >
      Add the bot to your server
      <span style={{ fontSize: 17, lineHeight: 1 }}>↗</span>
    </a>
  )
}

// SelfDiscordBind binds the signed-in admin's own Discord user ID inline.
function SelfDiscordBind({
  value,
  onChange,
  onSave,
  saving,
  saved,
}: {
  value: string
  onChange: (v: string) => void
  onSave: () => void
  saving: boolean
  saved: boolean
}) {
  return (
    <div style={{ marginTop: 16, padding: '16px 18px', background: T.surfaceAlt, border: `1px solid ${T.border}`, borderRadius: 13 }}>
      <div style={{ fontSize: 13, fontWeight: 600, color: T.body, marginBottom: 8 }}>Your Discord user ID</div>
      <div style={{ display: 'flex', gap: 10, flexWrap: 'wrap' }}>
        <input
          value={value}
          onChange={e => onChange(e.target.value)}
          placeholder="e.g. 849201134567890123"
          style={{ flex: 1, minWidth: 200, height: 44, border: `1px solid ${T.borderStrong}`, borderRadius: 10, padding: '0 13px', fontSize: 14, fontFamily: T.mono, color: T.text, background: T.surface, outline: 'none' }}
        />
        <Button variant="primary" loading={saving} disabled={!value.trim()} onClick={onSave}>
          Save
        </Button>
        {saved && (
          <span style={{ display: 'inline-flex', alignItems: 'center', gap: 6, fontSize: 13, fontWeight: 600, color: T.successInk }}>
            <Icon name="check" size={15} color={T.success} strokeWidth={2.6} />
            Saved
          </span>
        )}
      </div>
      <div style={{ fontSize: 12.5, color: T.faint, marginTop: 8 }}>
        This binds <strong>your</strong> id so the test in the last step DMs you. Set teammates’ IDs on the Team page.
      </div>
    </div>
  )
}

type StepStatus = 'done' | 'current' | 'pending'

function RailStepItem({ step, status, isLast, index }: { step: Step; status: StepStatus; isLast: boolean; index: number }) {
  const circleBase: React.CSSProperties = {
    width: 30,
    height: 30,
    borderRadius: '50%',
    display: 'flex',
    alignItems: 'center',
    justifyContent: 'center',
    fontSize: 13,
    fontWeight: 700,
    flex: 'none',
  }
  const circleStyle: React.CSSProperties =
    status === 'done'
      ? { ...circleBase, background: T.successBg, border: `2px solid ${T.success}`, color: T.success }
      : status === 'current'
        ? { ...circleBase, background: T.accent, color: '#fff', boxShadow: `0 0 0 4px ${T.accentSoft}` }
        : { ...circleBase, background: T.surface, border: `2px solid ${T.borderStrong}`, color: T.faint }

  return (
    <div style={{ display: 'flex', gap: 13 }}>
      <div style={{ display: 'flex', flexDirection: 'column', alignItems: 'center', flex: 'none' }}>
        <span style={circleStyle}>
          {status === 'done' ? <Icon name="check" size={15} color={T.success} strokeWidth={3} /> : index + 1}
        </span>
        {!isLast && (
          <span
            style={{ flex: 1, width: 2, background: status === 'done' ? T.success : T.borderSubtle, margin: '5px 0' }}
          />
        )}
      </div>
      <div style={{ paddingBottom: isLast ? 0 : 22 }}>
        <div
          style={{
            fontSize: 14.5,
            fontWeight: status === 'pending' ? 600 : 700,
            color: status === 'pending' ? T.subtle : T.body,
          }}
        >
          {step.title}
        </div>
        {step.sub && (
          <div style={{ fontSize: 12.5, color: status === 'pending' ? T.faint : T.subtle, marginTop: 2 }}>
            {step.sub}
          </div>
        )}
      </div>
    </div>
  )
}

export function IntegrationsConnectPage() {
  const { kind: kindParam } = useParams<{ kind: string }>()
  const navigate = useNavigate()
  const { selectedProjectId: projectId } = useIdentity()
  const toast = useToast()
  const origin = window.location.origin

  const kind: ProviderKind | null = kindParam && isProviderKind(kindParam) ? kindParam : null
  const def = kind ? PROVIDERS[kind] : null

  const [stepIndex, setStepIndex] = useState(0)
  const [credValues, setCredValues] = useState<Record<string, string>>({})
  const [savedCreds, setSavedCreds] = useState(false)
  const [manualOpen, setManualOpen] = useState(false)
  const [channelsInput, setChannelsInput] = useState('')
  const [channelsSaved, setChannelsSaved] = useState(false)
  const [channelsSavedCount, setChannelsSavedCount] = useState(0)
  const [testSent, setTestSent] = useState(false)
  const [testing, setTesting] = useState(false)
  const [testResults, setTestResults] = useState<ProviderTestResult[] | null>(null)

  // Inline self-binding: the admin's own Discord user ID, so the DM test reaches
  // them without leaving the flow. Prefilled from their member record.
  const [selfDiscordId, setSelfDiscordId] = useState('')
  const [savingSelf, setSavingSelf] = useState(false)
  const [selfDiscordSaved, setSelfDiscordSaved] = useState(false)
  useEffect(() => {
    api
      .getMember()
      .then(r => setSelfDiscordId(r.member?.discordUserId ?? ''))
      .catch(() => {})
  }, [])

  async function saveSelfDiscord() {
    setSavingSelf(true)
    try {
      await api.updateMember({ discordUserId: selfDiscordId.trim() })
      setSelfDiscordSaved(true)
      toast.success('Your Discord ID is saved.')
    } catch (e) {
      toast.error(toastMessage(e))
    } finally {
      setSavingSelf(false)
    }
  }
  const [saving, setSaving] = useState(false)

  // Redirect out of the wizard for an unknown provider kind, or one that is
  // not ready for users yet (coming soon).
  useEffect(() => {
    if (!kind || PROVIDERS[kind].comingSoon) navigate('/integrations', { replace: true })
  }, [kind, navigate])

  // Reset the wizard whenever the provider kind changes (e.g. Back to the
  // gallery then into a different provider without a full unmount).
  useEffect(() => {
    setStepIndex(0)
    setCredValues({})
    setSavedCreds(false)
    setManualOpen(false)
    setChannelsInput('')
    setChannelsSaved(false)
    setChannelsSavedCount(0)
    setTestSent(false)
  }, [kind])

  if (!def || !kind) return null

  const steps = def.steps
  const step = steps[stepIndex]
  const isLastStep = stepIndex === steps.length - 1
  const isFinalChannelsTest = step.type === 'channels' && isLastStep

  function setCredField(key: string, value: string) {
    setCredValues(v => ({ ...v, [key]: value }))
  }

  async function handleOAuthConnect() {
    try {
      await connectSlack(projectId)
    } catch (error) {
      toast.error(toastMessage(error))
    }
  }

  async function handleManualSave() {
    setSaving(true)
    try {
      await api.configureProvider(projectId, kind as ProviderKind, credValues)
      toast.success('Credentials saved.')
      setStepIndex(i => i + 1)
    } catch (e) {
      toast.error(toastMessage(e))
    } finally {
      setSaving(false)
    }
  }

  async function handleCredentialsSave() {
    setSaving(true)
    try {
      await api.configureProvider(projectId, kind as ProviderKind, credValues)
      setSavedCreds(true)
      toast.success('Credentials saved.')
    } catch (e) {
      toast.error(toastMessage(e))
    } finally {
      setSaving(false)
    }
  }

  async function handleChannelsSave() {
    const ids = channelsInput
      .split(',')
      .map(s => s.trim())
      .filter(Boolean)
    setSaving(true)
    try {
      await api.updateProviderAlertChannels(projectId, kind as ProviderKind, ids)
      setChannelsSaved(true)
      setChannelsSavedCount(ids.length)
      toast.success('Alert channels saved.')
    } catch (e) {
      toast.error(toastMessage(e))
    } finally {
      setSaving(false)
    }
  }

  async function handleTestPing() {
    if (testing) return
    setTesting(true)
    try {
      const res = await api.sendProviderTest(kind as ProviderKind)
      const results = res.results ?? []
      setTestResults(results)
      setTestSent(true)
      if (results.length > 0 && results.every(r => r.ok)) toast.success('Test delivered ✓')
      else toast.error('Some targets failed — see the results below')
    } catch (e) {
      toast.error(toastMessage(e))
    } finally {
      setTesting(false)
    }
  }

  async function handleCopy(text: string) {
    try {
      await navigator.clipboard.writeText(text)
      toast.success('Copied.')
    } catch {
      toast.error('Could not copy — copy it manually.')
    }
  }

  function exitSetup() {
    navigate('/integrations')
  }

  return (
    <div style={{ display: 'flex', flexDirection: 'column', minHeight: '100vh', background: T.bg }}>
      {/* In-content topbar */}
      <div
        style={{
          height: 60,
          flex: 'none',
          borderBottom: `1px solid ${T.borderSubtle}`,
          background: T.surface,
          display: 'flex',
          alignItems: 'center',
          justifyContent: 'space-between',
          padding: '0 24px',
        }}
      >
        <div style={{ display: 'flex', alignItems: 'center', gap: 10, minWidth: 0 }}>
          <span onClick={exitSetup} style={{ fontSize: 13.5, color: T.subtle, cursor: 'pointer' }}>
            Integrations
          </span>
          <span style={{ fontSize: 13, color: T.faint }}>/</span>
          <span style={{ fontSize: 13.5, fontWeight: 600, color: T.body }}>Connect {def.label}</span>
          <code style={{ fontSize: 11.5, color: T.faint, marginLeft: 4, fontFamily: T.mono }}>
            /integrations/connect/{kind}
          </code>
        </div>
        <Button variant="secondary" size="sm" onClick={exitSetup}>
          <Icon name="x" size={14} strokeWidth={2.2} />
          Exit setup
        </Button>
      </div>

      <div style={{ flex: 1, display: 'flex' }}>
        {/* Progress rail */}
        <div
          style={{
            width: RAIL_W,
            flex: 'none',
            background: T.surface,
            borderRight: `1px solid ${T.borderSubtle}`,
            padding: '26px 22px',
            display: 'flex',
            flexDirection: 'column',
          }}
        >
          <div style={{ display: 'flex', alignItems: 'center', gap: 12 }}>
            <div
              style={{
                width: 40,
                height: 40,
                borderRadius: 11,
                background: providerTileBg(kind),
                display: 'flex',
                alignItems: 'center',
                justifyContent: 'center',
                flex: 'none',
              }}
            >
              <ProviderLogo kind={kind} size={22} />
            </div>
            <div>
              <div style={{ fontSize: 16, fontWeight: 700, color: T.text }}>Connect {def.label}</div>
              <div
                style={{
                  display: 'inline-flex',
                  alignItems: 'center',
                  gap: 5,
                  fontSize: 11.5,
                  fontWeight: 600,
                  color: T.accent,
                  background: T.accentSoft,
                  border: `1px solid ${T.accentBorder}`,
                  borderRadius: 20,
                  padding: '2px 8px',
                  marginTop: 4,
                }}
              >
                <Icon name="clock" size={11} color={T.accent} strokeWidth={2.2} />
                {def.timeLabel} · {def.who}
              </div>
            </div>
          </div>

          <div style={{ height: 1, background: T.borderSubtle, margin: '22px 0' }} />

          <div style={{ display: 'flex', flexDirection: 'column' }}>
            {steps.map((s, i) => (
              <RailStepItem
                key={i}
                step={s}
                index={i}
                isLast={i === steps.length - 1}
                status={i < stepIndex ? 'done' : i === stepIndex ? 'current' : 'pending'}
              />
            ))}
          </div>

          <div
            style={{
              marginTop: 'auto',
              display: 'flex',
              alignItems: 'flex-start',
              gap: 9,
              background: T.accentSofter,
              border: `1px solid ${T.accentBorder}`,
              borderRadius: 11,
              padding: '12px 13px',
            }}
          >
            <Icon name="lock" size={15} color={T.accent} strokeWidth={1.9} style={{ flex: 'none', marginTop: 1 }} />
            <span style={{ fontSize: 12, lineHeight: 1.5, color: T.accentInk }}>{def.reassurance}</span>
          </div>
        </div>

        {/* Step content */}
        <div style={{ flex: 1, padding: '34px 40px', display: 'flex', flexDirection: 'column' }}>
          <div style={{ maxWidth: 580, flex: 1 }}>
            {step.type === 'test' ? (
              <div style={{ display: 'flex', alignItems: 'center', gap: 12 }}>
                <span
                  style={{
                    width: 52,
                    height: 52,
                    borderRadius: 14,
                    background: T.successBg,
                    display: 'flex',
                    alignItems: 'center',
                    justifyContent: 'center',
                    flex: 'none',
                  }}
                >
                  <Icon name="check" size={26} color={T.success} strokeWidth={2.4} />
                </span>
                <div>
                  <h3 style={{ fontSize: 24, fontWeight: 700, letterSpacing: '-.02em', color: T.text, margin: 0 }}>
                    {step.heading}
                  </h3>
                  {step.why && <p style={{ fontSize: 14.5, color: T.muted, marginTop: 4 }}>{step.why}</p>}
                </div>
              </div>
            ) : (
              <>
                <div
                  style={{
                    fontSize: 12,
                    fontWeight: 700,
                    letterSpacing: '.06em',
                    textTransform: 'uppercase',
                    color: T.subtle,
                  }}
                >
                  Step {stepIndex + 1} of {steps.length}
                  {step.eyebrow ? ` · ${step.eyebrow}` : ''}
                </div>
                <h3 style={{ fontSize: 24, fontWeight: 700, letterSpacing: '-.02em', color: T.text, marginTop: 8 }}>
                  {step.heading}
                </h3>
                {step.why && (
                  <p style={{ fontSize: 15, lineHeight: 1.6, color: T.muted, marginTop: 10 }}>{step.why}</p>
                )}
              </>
            )}

            {/* oauth */}
            {step.type === 'oauth' && (
              <>
                <div style={{ ...cardStyle, marginTop: 24 }}>
                  <div style={doThisLabelStyle}>Do this</div>
                  {step.doThis && (
                    <p style={{ fontSize: 14.5, lineHeight: 1.6, color: T.body, marginTop: 10, marginBottom: 0 }}>{step.doThis}</p>
                  )}
                  <Button
                    variant="primary"
                    style={{ height: 46, marginTop: 16, background: def.brand ?? T.accent, boxShadow: 'none' }}
                    onClick={handleOAuthConnect}
                  >
                    <ProviderLogo kind={kind} size={18} />
                    Connect {def.label}
                  </Button>
                  <div style={{ fontSize: 12.5, color: T.faint, marginTop: 11 }}>
                    Scopes requested:{' '}
                    {['chat:write', 'im:write', 'users:read'].map(scope => (
                      <code
                        key={scope}
                        style={{
                          fontSize: 12,
                          color: T.accent,
                          background: T.accentSofter,
                          border: `1px solid ${T.accentBorder}`,
                          borderRadius: 5,
                          padding: '1px 6px',
                          marginRight: 5,
                        }}
                      >
                        {scope}
                      </code>
                    ))}
                  </div>
                </div>

                <div style={{ marginTop: 16, background: T.surface, border: `1px solid ${T.border}`, borderRadius: 13, overflow: 'hidden' }}>
                  <button
                    onClick={() => setManualOpen(o => !o)}
                    style={{
                      display: 'flex',
                      alignItems: 'center',
                      gap: 10,
                      width: '100%',
                      padding: '15px 18px',
                      border: 'none',
                      background: 'transparent',
                      cursor: 'pointer',
                      fontFamily: 'inherit',
                      textAlign: 'left',
                    }}
                  >
                    <Icon
                      name="chevronRight"
                      size={15}
                      color={T.subtle}
                      strokeWidth={2.4}
                      style={{ transform: manualOpen ? 'rotate(90deg)' : 'none', transition: 'transform .15s' }}
                    />
                    <span style={{ fontSize: 14.5, fontWeight: 600, color: T.body }}>
                      Set it up manually / self-hosted
                    </span>
                    <span
                      style={{
                        marginLeft: 'auto',
                        fontSize: 12,
                        fontWeight: 600,
                        color: T.subtle,
                        background: T.sunken,
                        borderRadius: 20,
                        padding: '3px 10px',
                      }}
                    >
                      Optional
                    </span>
                  </button>
                  {manualOpen && (
                    <div style={{ padding: '0 18px 18px', display: 'flex', flexDirection: 'column', gap: 14 }}>
                      {step.note && <p style={{ fontSize: 13, lineHeight: 1.55, color: T.muted, margin: 0 }}>{step.note}</p>}
                      {def.credentialFields.map(f => (
                        <label key={f.key} style={{ display: 'flex', flexDirection: 'column', gap: 6 }}>
                          <span style={labelStyle}>{f.label}</span>
                          <input
                            type={f.secret ? 'password' : 'text'}
                            placeholder={f.placeholder}
                            value={credValues[f.key] ?? ''}
                            onChange={e => setCredField(f.key, e.target.value)}
                            style={credInputStyle()}
                          />
                        </label>
                      ))}
                      <div>
                        <Button variant="primary" loading={saving} onClick={handleManualSave}>
                          Save &amp; continue
                        </Button>
                      </div>
                    </div>
                  )}
                </div>
              </>
            )}

            {/* console */}
            {step.type === 'console' && (
              <div style={{ ...cardStyle, marginTop: 22 }}>
                <div style={doThisLabelStyle}>Do this</div>
                {step.doThis && (
                  <p style={{ fontSize: 14.5, lineHeight: 1.6, color: T.body, marginTop: 10 }}>{step.doThis}</p>
                )}
                {step.botInvite && <BotInviteButton token={credValues.bot_token} brand={def.brand} />}
                {step.bindSelfDiscord && (
                  <SelfDiscordBind
                    value={selfDiscordId}
                    onChange={setSelfDiscordId}
                    onSave={saveSelfDiscord}
                    saving={savingSelf}
                    saved={selfDiscordSaved}
                  />
                )}
                <StepBullets bullets={step.bullets} accent={def.brand} />
                <div style={{ display: 'flex', flexWrap: 'wrap', gap: 10, marginTop: 4 }}>
                  <StepLink link={step.link} />
                  <StepLinks links={step.links} />
                </div>
                {step.copyBlock && (
                  <div style={{ marginTop: 16 }}>
                    <div style={{ fontSize: 13, fontWeight: 600, color: T.body, marginBottom: 7 }}>
                      {step.copyBlockLabel ?? 'Copy this'}{' '}
                      <span style={{ fontWeight: 400, color: T.faint }}>· Orako gives you this</span>
                    </div>
                    <div style={{ position: 'relative', background: '#1B1F2A', borderRadius: 11, padding: '13px 15px' }}>
                      <pre
                        style={{
                          fontSize: 12,
                          color: '#C7CBD4',
                          fontFamily: T.mono,
                          margin: 0,
                          whiteSpace: 'pre-wrap',
                          wordBreak: 'break-word',
                          maxHeight: 300,
                          overflowY: 'auto',
                        }}
                      >
                        {step.copyBlock(origin, projectId)}
                      </pre>
                      <button
                        onClick={() => handleCopy(step.copyBlock!(origin, projectId))}
                        style={{
                          position: 'absolute',
                          top: 10,
                          right: 10,
                          display: 'flex',
                          alignItems: 'center',
                          gap: 6,
                          border: '1px solid #343B4A',
                          background: '#252B38',
                          color: '#C7CBD4',
                          borderRadius: 8,
                          padding: '5px 10px',
                          fontSize: 12,
                          fontWeight: 600,
                          cursor: 'pointer',
                          fontFamily: 'inherit',
                        }}
                      >
                        <Icon name="copy" size={13} color="#C7CBD4" strokeWidth={2.2} />
                        Copy
                      </button>
                    </div>
                  </div>
                )}
                {step.copyUrl && (
                  <div style={{ marginTop: 16 }}>
                    <div style={{ fontSize: 13, fontWeight: 600, color: T.body, marginBottom: 7 }}>
                      {step.copyLabel ?? 'Copy this'}{' '}
                      <span style={{ fontWeight: 400, color: T.faint }}>· Orako gives you this</span>
                    </div>
                    <div
                      style={{
                        display: 'flex',
                        alignItems: 'center',
                        gap: 10,
                        background: '#1B1F2A',
                        borderRadius: 11,
                        padding: '13px 15px',
                      }}
                    >
                      <code style={{ fontSize: 12.5, color: '#C7CBD4', flex: 1, wordBreak: 'break-all', fontFamily: T.mono }}>
                        {step.copyUrl(origin, projectId)}
                      </code>
                      <button
                        onClick={() => handleCopy(step.copyUrl!(origin, projectId))}
                        style={{
                          display: 'flex',
                          alignItems: 'center',
                          gap: 6,
                          height: 30,
                          padding: '0 11px',
                          border: 'none',
                          borderRadius: 7,
                          background: '#2A2E38',
                          color: '#C7CBD4',
                          fontSize: 12,
                          fontWeight: 600,
                          fontFamily: 'inherit',
                          cursor: 'pointer',
                        }}
                      >
                        <Icon name="copy" size={13} color="#C7CBD4" />
                        Copy
                      </button>
                    </div>
                  </div>
                )}
                {step.note && (
                  <div
                    style={{
                      display: 'flex',
                      alignItems: 'flex-start',
                      gap: 9,
                      marginTop: 16,
                      padding: '12px 14px',
                      background: T.warnBg,
                      border: `1px solid ${T.warnBorder}`,
                      borderRadius: 11,
                    }}
                  >
                    <Icon name="alertTriangle" size={16} color={T.warn} strokeWidth={2} style={{ flex: 'none', marginTop: 1 }} />
                    <span style={{ fontSize: 13, lineHeight: 1.5, color: T.warn }}>{step.note}</span>
                  </div>
                )}

                {/* Credential field(s) attached to a console step — e.g. Discord's
                    optional OAuth2 client secret, which lives here (next to the
                    redirect-URL instructions) rather than back in step 1. Shares
                    credValues, so Save persists it alongside the earlier bot token. */}
                {step.credentialKeys && (
                  <div style={{ marginTop: 16, display: 'flex', flexDirection: 'column', gap: 12 }}>
                    {def.credentialFields
                      .filter(f => step.credentialKeys!.includes(f.key))
                      .map(f => (
                        <label key={f.key} style={{ display: 'flex', flexDirection: 'column', gap: 6 }}>
                          <span style={labelStyle}>{f.label}</span>
                          <input
                            type={f.secret ? 'password' : 'text'}
                            placeholder={f.placeholder}
                            value={credValues[f.key] ?? ''}
                            onChange={e => setCredField(f.key, e.target.value)}
                            style={credInputStyle()}
                          />
                        </label>
                      ))}
                    <div style={{ display: 'flex', alignItems: 'center', gap: 12 }}>
                      <Button variant="primary" loading={saving} onClick={handleCredentialsSave}>
                        Save
                      </Button>
                      <span style={{ fontSize: 12.5, color: T.faint }}>
                        Saved with the bot token from step 1 — the set is stored together.
                      </span>
                    </div>
                  </div>
                )}
              </div>
            )}

            {/* credentials */}
            {step.type === 'credentials' && (
              <div style={{ ...cardStyle, marginTop: 20, display: 'flex', flexDirection: 'column', gap: 15 }}>
                {step.doThis && (
                  <div>
                    <div style={doThisLabelStyle}>Do this</div>
                    <p style={{ fontSize: 14.5, lineHeight: 1.6, color: T.body, margin: '10px 0 0' }}>{step.doThis}</p>
                    <StepBullets bullets={step.bullets} accent={def.brand} />
                    <div style={{ display: 'flex', flexWrap: 'wrap', gap: 10 }}>
                      <StepLink link={step.link} />
                      <StepLinks links={step.links} />
                    </div>
                  </div>
                )}
                {(step.credentialKeys
                  ? def.credentialFields.filter(f => step.credentialKeys!.includes(f.key))
                  : def.credentialFields
                ).map(f => (
                  <label key={f.key} style={{ display: 'flex', flexDirection: 'column', gap: 6 }}>
                    <span style={labelStyle}>{f.label}</span>
                    <input
                      type={f.secret ? 'password' : 'text'}
                      placeholder={f.placeholder}
                      value={credValues[f.key] ?? ''}
                      onChange={e => setCredField(f.key, e.target.value)}
                      style={credInputStyle()}
                    />
                  </label>
                ))}
                <div style={{ display: 'flex', alignItems: 'center', gap: 12 }}>
                  <Button variant="primary" loading={saving} onClick={handleCredentialsSave}>
                    Save
                  </Button>
                  {savedCreds ? (
                    <span
                      style={{
                        display: 'inline-flex',
                        alignItems: 'center',
                        gap: 6,
                        fontSize: 13,
                        fontWeight: 600,
                        color: T.successInk,
                      }}
                    >
                      <Icon name="check" size={15} color={T.success} strokeWidth={2.6} />
                      Saved ✓
                    </span>
                  ) : (
                    <span style={{ fontSize: 12.5, color: T.faint }}>Saved together — you can’t save half a set.</span>
                  )}
                </div>
              </div>
            )}

            {/* channels (+ test when it's the final step) */}
            {step.type === 'channels' && (
              <>
                <div style={{ ...cardStyle, marginTop: 22 }}>
                  <label style={labelStyle}>Alert channels</label>
                  <input
                    type="text"
                    value={channelsInput}
                    onChange={e => setChannelsInput(e.target.value)}
                    placeholder="Empty = no channel alerts · comma-separate for several"
                    style={inputStyle()}
                  />
                  <p style={{ fontSize: 12.5, color: T.faint, lineHeight: 1.5, margin: '9px 0 0' }}>
                    {kind === 'discord' ? (
                      <>
                        How to get a channel ID: turn on <strong>Developer Mode</strong> (User Settings →
                        Advanced), then right-click the channel → <strong>Copy Channel ID</strong>. Invite the
                        bot to that channel first.
                      </>
                    ) : kind === 'slack' ? (
                      <>
                        How to get a channel ID: right-click the channel → <strong>Copy link</strong> — the ID is
                        the last path segment (starts with <code>C</code>). Invite the bot first with{' '}
                        <code>/invite @Orako</code>.
                      </>
                    ) : (
                      <>Add one or more channel IDs, comma-separated.</>
                    )}
                  </p>
                  <div style={{ display: 'flex', alignItems: 'center', gap: 12, marginTop: 14 }}>
                    <Button variant="primary" loading={saving} onClick={handleChannelsSave}>
                      Save
                    </Button>
                    {channelsSaved && (
                      <span
                        style={{
                          display: 'inline-flex',
                          alignItems: 'center',
                          gap: 6,
                          fontSize: 13,
                          fontWeight: 600,
                          color: T.successInk,
                        }}
                      >
                        <Icon name="check" size={15} color={T.success} strokeWidth={2.6} />
                        Saved{channelsSavedCount > 0 ? ` · ${channelsSavedCount} channel${channelsSavedCount > 1 ? 's' : ''}` : ''}
                      </span>
                    )}
                  </div>
                </div>

                {isFinalChannelsTest && (
                  <>
                    <div style={{ ...cardStyle, marginTop: 16 }}>
                      <span style={{ fontSize: 14.5, fontWeight: 600, color: T.body }}>Send a real test</span>
                      <p style={{ fontSize: 13, color: T.subtle, margin: '6px 0 0' }}>
                        Orako DMs you and posts to each alert channel for real. Each target below reports whether it
                        actually went through — a failure is the exact reason it isn’t working yet.
                      </p>
                      <Button variant="secondary" style={{ marginTop: 12 }} loading={testing} onClick={handleTestPing}>
                        {testSent ? 'Run the test again' : 'Send a test'}
                      </Button>
                      {testResults && (
                        <div style={{ marginTop: 14, display: 'flex', flexDirection: 'column', gap: 8 }}>
                          {testResults.length === 0 && (
                            <span style={{ fontSize: 13, color: T.faint }}>Nothing to test — bind your Discord ID or add an alert channel first.</span>
                          )}
                          {testResults.map((r, i) => (
                            <div
                              key={i}
                              style={{
                                display: 'flex',
                                alignItems: 'flex-start',
                                gap: 10,
                                padding: '10px 12px',
                                borderRadius: 10,
                                border: `1px solid ${r.ok ? T.successBorder : T.warnBorder}`,
                                background: r.ok ? T.successBg : T.warnBg,
                              }}
                            >
                              <Icon
                                name={r.ok ? 'check' : 'alertTriangle'}
                                size={16}
                                color={r.ok ? T.success : T.warn}
                                strokeWidth={2.2}
                                style={{ flex: 'none', marginTop: 1 }}
                              />
                              <div style={{ minWidth: 0 }}>
                                <div style={{ fontSize: 13.5, fontWeight: 600, color: r.ok ? T.successInk : T.warn }}>{r.label}</div>
                                {!r.ok && r.error && (
                                  <div style={{ fontSize: 12.5, color: T.warn, marginTop: 2, wordBreak: 'break-word' }}>{r.error}</div>
                                )}
                              </div>
                            </div>
                          ))}
                        </div>
                      )}
                    </div>

                    <div
                      style={{
                        marginTop: 16,
                        display: 'flex',
                        alignItems: 'flex-start',
                        gap: 13,
                        background: T.accentSofter,
                        border: `1px solid ${T.accentBorder}`,
                        borderRadius: 14,
                        padding: '18px 20px',
                      }}
                    >
                      <span
                        style={{
                          width: 38,
                          height: 38,
                          borderRadius: 11,
                          background: T.surface,
                          border: `1px solid ${T.accentBorder}`,
                          display: 'flex',
                          alignItems: 'center',
                          justifyContent: 'center',
                          flex: 'none',
                        }}
                      >
                        <Icon name="users" size={19} color={T.accent} strokeWidth={1.9} />
                      </span>
                      <div style={{ flex: 1 }}>
                        <div style={{ fontSize: 14.5, fontWeight: 700, color: T.accentInk }}>
                          One more thing — tell your team
                        </div>
                        <p style={{ fontSize: 13.5, lineHeight: 1.55, color: T.accent, marginTop: 4 }}>
                          Tell your team to add their {def.label} handle in Account settings so Orako can reach them.
                        </p>
                      </div>
                    </div>
                  </>
                )}
              </>
            )}

            {/* test (standalone final step, e.g. Slack) */}
            {step.type === 'test' && (
              <>
                <div style={{ ...cardStyle, marginTop: 22 }}>
                  <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between' }}>
                    <span style={{ fontSize: 14.5, fontWeight: 600, color: T.body }}>Send a test ping</span>
                    {testSent && (
                      <span
                        style={{
                          display: 'inline-flex',
                          alignItems: 'center',
                          gap: 6,
                          fontSize: 12.5,
                          fontWeight: 600,
                          color: T.successInk,
                          background: T.successBg,
                          border: `1px solid ${T.successBorder}`,
                          borderRadius: 20,
                          padding: '3px 10px',
                        }}
                      >
                        <span style={{ width: 6, height: 6, borderRadius: '50%', background: T.success }} />
                        Delivered
                      </span>
                    )}
                  </div>
                  {testSent ? (
                    <p style={{ fontSize: 13.5, color: T.subtle, marginTop: 10 }}>
                      Check your {def.label} DM and reply to it to confirm the round-trip works.
                    </p>
                  ) : (
                    <Button variant="secondary" style={{ marginTop: 14 }} onClick={handleTestPing}>
                      Send a test ping
                    </Button>
                  )}
                </div>

                <div
                  style={{
                    marginTop: 16,
                    display: 'flex',
                    alignItems: 'flex-start',
                    gap: 13,
                    background: T.accentSofter,
                    border: `1px solid ${T.accentBorder}`,
                    borderRadius: 14,
                    padding: '18px 20px',
                  }}
                >
                  <span
                    style={{
                      width: 38,
                      height: 38,
                      borderRadius: 11,
                      background: T.surface,
                      border: `1px solid ${T.accentBorder}`,
                      display: 'flex',
                      alignItems: 'center',
                      justifyContent: 'center',
                      flex: 'none',
                    }}
                  >
                    <Icon name="users" size={19} color={T.accent} strokeWidth={1.9} />
                  </span>
                  <div style={{ flex: 1 }}>
                    <div style={{ fontSize: 14.5, fontWeight: 700, color: T.accentInk }}>
                      One more thing — tell your team
                    </div>
                    <p style={{ fontSize: 13.5, lineHeight: 1.55, color: T.accent, marginTop: 4 }}>
                      Tell your team to add their {def.label} handle in Account settings so Orako can reach them.
                    </p>
                  </div>
                </div>
              </>
            )}
          </div>

          {/* Footer nav */}
          <div
            style={{
              display: 'flex',
              alignItems: 'center',
              justifyContent: 'space-between',
              paddingTop: 20,
              marginTop: 8,
              borderTop: `1px solid ${T.borderSubtle}`,
              maxWidth: 580,
            }}
          >
            <Button variant="secondary" disabled={stepIndex === 0} onClick={() => setStepIndex(i => i - 1)}>
              Back
            </Button>

            <div style={{ display: 'flex', alignItems: 'center', gap: 12 }}>
              {step.type === 'oauth' && (
                <>
                  <span style={{ fontSize: 12.5, color: T.faint }}>Fast path skips ahead automatically</span>
                  <Button variant="secondary" onClick={() => setStepIndex(i => i + 1)}>
                    Skip · I’ll do it manually
                    <Icon name="arrowRight" size={16} color={T.accent} strokeWidth={2.2} />
                  </Button>
                </>
              )}

              {step.type === 'console' && (
                <Button variant="primary" onClick={() => setStepIndex(i => i + 1)}>
                  {step.nextLabel ?? 'Next'}
                  {!step.nextLabel && <Icon name="arrowRight" size={16} color="#fff" strokeWidth={2.2} />}
                </Button>
              )}

              {step.type === 'credentials' && (
                <Button variant="primary" disabled={!savedCreds} onClick={() => setStepIndex(i => i + 1)}>
                  Next
                  <Icon name="arrowRight" size={16} color="#fff" strokeWidth={2.2} />
                </Button>
              )}

              {step.type === 'channels' && !isFinalChannelsTest && (
                <>
                  <Button variant="ghost" onClick={() => setStepIndex(i => i + 1)}>
                    Skip
                  </Button>
                  <Button variant="primary" onClick={() => setStepIndex(i => i + 1)}>
                    Next
                    <Icon name="arrowRight" size={16} color="#fff" strokeWidth={2.2} />
                  </Button>
                </>
              )}

              {(isFinalChannelsTest || step.type === 'test') && (
                <Button variant="primary" style={{ background: T.success, boxShadow: 'none' }} onClick={exitSetup}>
                  Finish · back to Integrations
                  <Icon name="arrowRight" size={16} color="#fff" strokeWidth={2.2} />
                </Button>
              )}
            </div>
          </div>
        </div>
      </div>
    </div>
  )
}
