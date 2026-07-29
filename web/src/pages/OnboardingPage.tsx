// Invited-member onboarding wizard (cli-login phase 4): a first-run member
// walks Your info → Your expertise → How Orako reaches you → Connect your
// agent, then lands in the dashboard. Onboarding is mandatory: there is no skip —
// an invited member walks every step before reaching the app (a name + a contact
// channel are the minimum that make them routable). Every field stays re-editable
// later in Settings. Full-viewport, no <Layout> chrome, matching /welcome.

import { useCallback, useEffect, useState, type ReactNode } from 'react'
import { useNavigate } from 'react-router'
import { api, connectDiscord, communityInvites, type CommunityInvites, type Member } from '../lib/client'
import { useIdentity } from '../lib/identity'
import { markOnboardingRedirect } from '../lib/onboarding-redirect'
import { useToast, toastMessage } from '../lib/toast'
import { Button } from '../components/Button'
import { Input } from '../components/Input'
import { ChipMultiSelect } from '../components/ChipMultiSelect'
import { DiscordIdHint } from '../components/DiscordIdHint'
import { Icon, LogoTile } from '../components/Icon'
import { ProviderLogo } from '../components/ProviderLogos'
import { ConnectAgent } from './ConnectAgentPage'
import { EXPERTISE_TAGS } from '../lib/expertise'
import { T } from '../lib/theme'
import { validateDeliveryBinding } from '../lib/validation'

const STEP_TITLES = ['Your info', 'Your expertise', 'How Orako reaches you', 'Connect your agent']
const TOTAL_STEPS = STEP_TITLES.length
const STEP_STORAGE_PREFIX = 'orako:onboarding-step:'

type ChannelKey = 'dashboard' | 'slack' | 'telegram' | 'teams' | 'discord'

const CHANNEL_LABEL: Record<ChannelKey, string> = {
  dashboard: 'Dashboard',
  slack: 'Slack',
  telegram: 'Telegram',
  teams: 'Microsoft Teams',
  discord: 'Discord',
}

const BINDING_FIELD: Partial<Record<ChannelKey, keyof Pick<Member, 'slackUserId' | 'telegramChatId' | 'teamsUserId' | 'discordUserId'>>> = {
  slack: 'slackUserId',
  telegram: 'telegramChatId',
  teams: 'teamsUserId',
  discord: 'discordUserId',
}

const BINDING_META: Partial<Record<ChannelKey, { label: string; placeholder: string }>> = {
  slack: { label: 'Your Slack user ID', placeholder: 'U01ABC23DEF' },
  telegram: { label: 'Your Telegram chat ID', placeholder: '123456789' },
  teams: { label: 'Your Teams AAD object ID', placeholder: '29:1a2b3c4d...' },
  discord: { label: 'Your Discord user ID', placeholder: '123456789012345678' },
}

export function OnboardingPage() {
  const navigate = useNavigate()
  const { refresh, memberID, selectedProjectId } = useIdentity()
  const toast = useToast()

  const [step, setStep] = useState(() => loadSavedStep(memberID))

  useEffect(() => {
    if (!memberID) return
    setStep(loadSavedStep(memberID))
  }, [memberID])

  useEffect(() => {
    if (!memberID) return
    saveStep(memberID, step)
  }, [memberID, step])

  // Finishing the wizard sends the member to their home. Mark the one-time
  // onboarding bounce handled FIRST so RequireToken never forces them back here.
  // Everyone lands on the role-adapted Get Started page instead of being
  // dropped into an operational queue with no context.
  function goHome() {
    clearSavedStep(memberID)
    markOnboardingRedirect()
    navigate('/get-started', { replace: true })
  }

  async function completeStep1AndAdvance(patch: {
    firstName: string
    lastName: string
    displayName: string
    gitHandle: string
  }) {
    await api.updateMember(patch)
    // Re-fetch identity so `needsOnboarding` recomputes off the fresh first
    // name — this is what makes the wizard stop auto-showing once the
    // member has given a name (the heuristic's "don't nag forever" intent).
    await refresh()
    setStep(1)
  }

  async function completeExpertiseAndAdvance(domains: string[]) {
    // Persist the member's own expertise via the self-serve SetOwnExpertise RPC:
    // it sets the CALLER's domains across every project they belong to and is NOT
    // admin-gated, so it SUCCEEDS for a plain invited member (unlike the old
    // AssignRole loop, which the server rejected for non-admins). Still
    // NON-BLOCKING: a failure never stalls onboarding — expertise is optional and
    // adjustable later from Edit Person.
    if (domains.length > 0) {
      try {
        await api.setOwnExpertise(domains)
      } catch {
        // non-blocking — onboarding never stalls on optional expertise.
      }
    }
    setStep(2)
  }

  async function completeStep2AndAdvance(patch: {
    deliveryChannel: string
    slackUserId?: string
    telegramChatId?: string
    teamsUserId?: string
    discordUserId?: string
  }) {
    await api.updateMember(patch)
    setStep(3)
  }

  return (
    <div style={{ minHeight: '100vh', background: T.bg, display: 'flex', flexDirection: 'column' }}>
      <div
        style={{
          height: 64,
          flex: 'none',
          display: 'flex',
          alignItems: 'center',
          padding: '0 32px',
          borderBottom: `1px solid ${T.borderSubtle}`,
          background: T.surface,
        }}
      >
        <div style={{ display: 'flex', alignItems: 'center', gap: 10 }}>
          <LogoTile size={30} radius={9} />
          <span style={{ fontSize: 17, fontWeight: 700, letterSpacing: '-.02em', color: T.text }}>Orako</span>
        </div>
      </div>

      <div style={{ flex: 1, display: 'flex', alignItems: 'flex-start', justifyContent: 'center', padding: '48px 40px' }}>
        <div style={{ width: '100%', maxWidth: 640 }}>
          <div
            style={{
              background: T.surface,
              border: `1px solid ${T.border}`,
              borderRadius: 16,
              padding: 34,
              boxShadow: T.shadowCard,
            }}
          >
            <WizardHeader step={step} />

            {step === 0 && (
              <Step1Info
                onContinue={completeStep1AndAdvance}
                onError={e => toast.error(toastMessage(e))}
              />
            )}
            {step === 1 && (
              <StepExpertise
                memberID={memberID}
                projectID={selectedProjectId}
                onContinue={completeExpertiseAndAdvance}
              />
            )}
            {step === 2 && (
              <Step2Reach
                onContinue={completeStep2AndAdvance}
                onError={e => toast.error(toastMessage(e))}
              />
            )}
            {step === 3 && <Step3Agent onFinish={goHome} />}
          </div>

          <p style={{ fontSize: 13, color: T.faint, textAlign: 'center', marginTop: 18 }}>
            Everything here stays editable later in Settings.
          </p>
        </div>
      </div>
    </div>
  )
}

function loadSavedStep(memberID: string): number {
  if (!memberID) return 0
  try {
    const saved = Number(localStorage.getItem(STEP_STORAGE_PREFIX + memberID))
    return Number.isInteger(saved) && saved >= 0 && saved < TOTAL_STEPS ? saved : 0
  } catch {
    return 0
  }
}

function saveStep(memberID: string, step: number): void {
  try {
    localStorage.setItem(STEP_STORAGE_PREFIX + memberID, String(step))
  } catch {}
}

function clearSavedStep(memberID: string): void {
  if (!memberID) return
  try {
    localStorage.removeItem(STEP_STORAGE_PREFIX + memberID)
  } catch {}
}

function WizardHeader({ step }: { step: number }) {
  return (
    <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', marginBottom: 22 }}>
      <h3 style={{ fontSize: 20, fontWeight: 700, letterSpacing: '-.02em', color: T.text, margin: 0 }}>
        Welcome to Orako
      </h3>
      <div style={{ display: 'flex', alignItems: 'center', gap: 10 }}>
        <div style={{ display: 'flex', gap: 6 }}>
          {STEP_TITLES.map((title, i) => (
            <span
              key={title}
              title={title}
              style={{
                width: 7,
                height: 7,
                borderRadius: '50%',
                background: i <= step ? T.accent : T.borderStrong,
              }}
            />
          ))}
        </div>
        <span style={{ fontSize: 12.5, fontWeight: 600, color: T.subtle, whiteSpace: 'nowrap' }}>
          Step {step + 1}/{TOTAL_STEPS}
        </span>
      </div>
    </div>
  )
}

function StepFooter({
  onContinue,
  continueLabel = 'Continue',
  continueLoading = false,
  continueDisabled = false,
}: {
  onContinue: () => void
  continueLabel?: string
  continueLoading?: boolean
  continueDisabled?: boolean
}) {
  return (
    <div style={{ display: 'flex', justifyContent: 'flex-end', marginTop: 26 }}>
      <Button variant="primary" loading={continueLoading} disabled={continueDisabled} onClick={onContinue}>
        {continueLabel}
        {!continueLoading && <Icon name="arrowRight" size={15} color="#fff" />}
      </Button>
    </div>
  )
}

function Reassurance({ children }: { children: ReactNode }) {
  return (
    <p
      style={{
        fontSize: 13,
        color: T.subtle,
        lineHeight: 1.55,
        margin: '16px 0 0',
        background: T.surfaceAlt,
        border: `1px solid ${T.borderSubtle}`,
        borderRadius: T.rMd,
        padding: '11px 14px',
      }}
    >
      {children}
    </p>
  )
}

// ── Step 1 · Your info ───────────────────────────────────────────────────────

function Step1Info({
  onContinue,
  onError,
}: {
  onContinue: (patch: { firstName: string; lastName: string; displayName: string; gitHandle: string }) => Promise<void>
  onError: (e: unknown) => void
}) {
  const [loading, setLoading] = useState(true)
  const [saving, setSaving] = useState(false)
  const [firstName, setFirstName] = useState('')
  const [lastName, setLastName] = useState('')
  const [displayName, setDisplayName] = useState('')
  const [gitHandle, setGitHandle] = useState('')

  useEffect(() => {
    let cancelled = false
    api
      .getMember()
      .then(res => {
        if (cancelled) return
        setFirstName(res.member?.firstName ?? '')
        setLastName(res.member?.lastName ?? '')
        // Prefill display name with the invite email so the field never
        // looks blank; the member overwrites it with a real name here.
        setDisplayName(res.member?.displayName ?? '')
        setGitHandle(res.member?.gitHandle ?? '')
      })
      .catch(() => {})
      .finally(() => {
        if (!cancelled) setLoading(false)
      })
    return () => {
      cancelled = true
    }
  }, [])

  async function handleContinue() {
    if (saving) return
    setSaving(true)
    try {
      await onContinue({
        firstName: firstName.trim(),
        lastName: lastName.trim(),
        // Display name is what shows in every conversation — never let it be
        // blank now that the step is mandatory: fall back to the typed name.
        displayName: displayName.trim() || `${firstName.trim()} ${lastName.trim()}`.trim(),
        gitHandle: gitHandle.trim(),
      })
    } catch (e) {
      onError(e)
    } finally {
      setSaving(false)
    }
  }

  return (
    <div>
      <h4 style={{ fontSize: 16, fontWeight: 700, color: T.text, margin: '0 0 6px' }}>Tell us who you are</h4>
      <p style={{ fontSize: 13.5, color: T.muted, margin: 0 }}>
        Your teammates — and the agent, which routes questions to the right person — see this.
      </p>

      {loading ? (
        <div style={{ marginTop: 22, fontSize: 13, color: T.subtle }}>Loading…</div>
      ) : (
        <div style={{ marginTop: 22, display: 'flex', flexDirection: 'column', gap: 16 }}>
          <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 14 }}>
            <Input label="First name" placeholder="Ellen" value={firstName} onChange={e => setFirstName(e.target.value)} autoFocus />
            <Input label="Last name" placeholder="Ripley" value={lastName} onChange={e => setLastName(e.target.value)} />
          </div>
          <Input
            label="Display name"
            placeholder="How your name shows up in conversations"
            value={displayName}
            onChange={e => setDisplayName(e.target.value)}
          />
          <Input
            label="Github handle"
            placeholder="nostromo"
            value={gitHandle}
            onChange={e => setGitHandle(e.target.value)}
            hint="Lets the agent match commits to you when picking a teammate."
            style={{ fontFamily: T.mono, fontSize: 13 }}
          />
        </div>
      )}

      <Reassurance>Your first name is required — it&apos;s how teammates and the agent identify you. The rest is optional and editable later.</Reassurance>

      <StepFooter
        onContinue={() => void handleContinue()}
        continueLoading={saving}
        continueDisabled={loading || firstName.trim() === ''}
      />
    </div>
  )
}

// ── Step 2 · Your expertise ──────────────────────────────────────────────────

function StepExpertise({
  memberID,
  projectID,
  onContinue,
}: {
  memberID: string
  projectID: string
  onContinue: (domains: string[]) => Promise<void>
}) {
  const [loading, setLoading] = useState(true)
  const [saving, setSaving] = useState(false)
  const [tags, setTags] = useState<string[]>([])

  useEffect(() => {
    let cancelled = false
    if (!memberID || !projectID) {
      setLoading(false)
      return
    }

    api
      .listExperts(projectID)
      .then(res => {
        if (cancelled) return
        setTags(res.experts?.find(expert => expert.memberId === memberID)?.domains ?? [])
      })
      .catch(() => {})
      .finally(() => {
        if (!cancelled) setLoading(false)
      })

    return () => {
      cancelled = true
    }
  }, [memberID, projectID])

  async function handleContinue() {
    if (saving) return
    setSaving(true)
    try {
      await onContinue(tags)
    } finally {
      setSaving(false)
    }
  }

  return (
    <div>
      <h4 style={{ fontSize: 16, fontWeight: 700, color: T.text, margin: '0 0 6px' }}>What are you good at?</h4>
      <p style={{ fontSize: 13.5, color: T.muted, margin: 0 }}>
        Orako routes questions that match your expertise straight to you. Pick any that fit — you can change these
        anytime.
      </p>

      {loading ? (
        <div style={{ marginTop: 22, fontSize: 13, color: T.subtle }}>Loading…</div>
      ) : (
        <div style={{ marginTop: 22 }}>
          <ChipMultiSelect options={EXPERTISE_TAGS} value={tags} onChange={setTags} disabled={saving} />
        </div>
      )}

      <Reassurance>Optional — an admin can adjust your expertise later, and you can update it in Settings.</Reassurance>

      <StepFooter onContinue={() => void handleContinue()} continueLoading={saving} continueDisabled={loading} />
    </div>
  )
}

// ── Step 3 · How Orako reaches you ───────────────────────────────────────────

function Step2Reach({
  onContinue,
  onError,
}: {
  onContinue: (patch: {
    deliveryChannel: string
    slackUserId?: string
    telegramChatId?: string
    teamsUserId?: string
    discordUserId?: string
  }) => Promise<void>
  onError: (e: unknown) => void
}) {
  const [loading, setLoading] = useState(true)
  const [saving, setSaving] = useState(false)
  const [connected, setConnected] = useState<string[]>([])
  const [selected, setSelected] = useState<ChannelKey>('dashboard')
  const [invites, setInvites] = useState<CommunityInvites>({})
  // Manual Discord id entry stays available as a collapsible fallback so nothing
  // regresses where OAuth self-bind isn't configured.
  const [manualDiscord, setManualDiscord] = useState(false)
  const [fieldError, setFieldError] = useState<string | null>(null)
  const [connectedBindings, setConnectedBindings] = useState<Record<string, string>>({
    slackUserId: '',
    telegramChatId: '',
    teamsUserId: '',
    discordUserId: '',
  })
  const [bindings, setBindings] = useState<Record<string, string>>({
    slackUserId: '',
    telegramChatId: '',
    teamsUserId: '',
    discordUserId: '',
  })

  const load = useCallback(async () => {
    setLoading(true)
    try {
      const [m, c, inv] = await Promise.all([api.getMember(), api.listConnectedChannels(), communityInvites()])
      setConnected(c.channels ?? [])
      setInvites(inv)
      setSelected((m.member.deliveryChannel as ChannelKey) || 'dashboard')
      const nextBindings = {
        slackUserId: m.member.slackUserId ?? '',
        telegramChatId: m.member.telegramChatId ?? '',
        teamsUserId: m.member.teamsUserId ?? '',
        discordUserId: m.member.discordUserId ?? '',
      }
      setBindings(nextBindings)
      setConnectedBindings(nextBindings)
    } catch (e) {
      onError(e)
    } finally {
      setLoading(false)
    }
    // onError is stable enough for a one-shot load; omitting it from deps
    // avoids re-fetching on every toast.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])

  useEffect(() => {
    void load()
  }, [load])

  // Reflect a Discord bind completed in the popup tab: on window focus, refetch
  // the member so the "Discord connected ✓" state appears here without losing
  // this step's selections. The user reported connecting with NO feedback — the
  // opener tab never re-read the member — and this is the fix.
  useEffect(() => {
    function onFocus() {
      api
        .getMember()
        .then(m => {
          const discordUserId = m.member.discordUserId ?? ''
          setConnectedBindings(current => ({ ...current, discordUserId }))
          if (discordUserId) {
            setBindings(current => ({ ...current, discordUserId }))
            setManualDiscord(false)
          }
        })
        .catch(() => {})
    }
    window.addEventListener('focus', onFocus)
    return () => window.removeEventListener('focus', onFocus)
  }, [])

  const externalChannels = connected.filter(c => c !== 'dashboard') as ChannelKey[]
  const hasExternal = externalChannels.length > 0
  const options: ChannelKey[] = hasExternal ? ['dashboard', ...externalChannels] : ['dashboard']

  async function handleContinue() {
    if (saving) return
    const bindingField = BINDING_FIELD[selected]
    if (bindingField) {
      const validationError = validateDeliveryBinding(selected, bindings[bindingField])
      if (validationError) {
        setFieldError(validationError)
        return
      }
    }
    setFieldError(null)
    setSaving(true)
    try {
      await onContinue({
        deliveryChannel: selected,
        slackUserId: bindingField === 'slackUserId' ? bindings.slackUserId.trim() : undefined,
        telegramChatId: bindingField === 'telegramChatId' ? bindings.telegramChatId.trim() : undefined,
        teamsUserId: bindingField === 'teamsUserId' ? bindings.teamsUserId.trim() : undefined,
        discordUserId: bindingField === 'discordUserId' ? bindings.discordUserId.trim() : undefined,
      })
    } catch (e) {
      onError(e)
    } finally {
      setSaving(false)
    }
  }

  const bindingMeta = BINDING_META[selected]
  const bindingField = BINDING_FIELD[selected]
  const bindingIsValid =
    !bindingField || validateDeliveryBinding(selected, bindings[bindingField]) === null

  return (
    <div>
      <h4 style={{ fontSize: 16, fontWeight: 700, color: T.text, margin: '0 0 6px' }}>How should Orako reach you?</h4>
      <p style={{ fontSize: 13.5, color: T.muted, margin: 0 }}>
        When an agent needs your expertise, Orako pings you here. Pick one — you can change it anytime.
      </p>

      {loading ? (
        <div style={{ marginTop: 22, fontSize: 13, color: T.subtle }}>Loading…</div>
      ) : (
        <div style={{ marginTop: 22, display: 'flex', flexDirection: 'column', gap: 10 }}>
          {options.map(key => {
            const isSelected = selected === key
            return (
              <button
                key={key}
                onClick={() => {
                  setSelected(key)
                  setFieldError(null)
                }}
                style={{
                  display: 'flex',
                  alignItems: 'center',
                  gap: 12,
                  width: '100%',
                  textAlign: 'left',
                  fontFamily: 'inherit',
                  background: T.surface,
                  border: isSelected ? `1.5px solid ${T.accent}` : `1px solid ${T.border}`,
                  borderRadius: 12,
                  padding: '13px 16px',
                  cursor: 'pointer',
                }}
              >
                <span style={{ fontSize: 14.5, fontWeight: 600, color: T.text, flex: 1 }}>{CHANNEL_LABEL[key]}</span>
                {isSelected ? (
                  <div style={{ width: 18, height: 18, borderRadius: '50%', border: `5px solid ${T.accent}`, background: '#fff', flex: 'none' }} />
                ) : (
                  <div style={{ width: 18, height: 18, borderRadius: '50%', border: '1.5px solid #D5D8DE', background: '#fff', flex: 'none' }} />
                )}
              </button>
            )
          })}

          {/* Discord: self-bind via OAuth (no hand-typed snowflake), with a
              collapsible manual fallback. selected === 'discord' only when the
              org has Discord configured, so the button always has a flow. */}
          {selected === 'discord' ? (
            <DiscordBind
              connectedId={connectedBindings.discordUserId}
              manualValue={bindings.discordUserId}
              manualOpen={manualDiscord}
              error={fieldError}
              onToggleManual={() => {
                setManualDiscord(o => !o)
                setFieldError(null)
              }}
              onManualChange={v => {
                setBindings(b => ({ ...b, discordUserId: v }))
                setFieldError(null)
              }}
              onError={onError}
            />
          ) : (
            bindingField &&
            bindingMeta && (
              <div style={{ marginTop: 4 }}>
                <Input
                  label={bindingMeta.label}
                  placeholder={bindingMeta.placeholder}
                  value={bindings[bindingField]}
                  error={fieldError}
                  onChange={e => {
                    setBindings(b => ({ ...b, [bindingField]: e.target.value }))
                    setFieldError(null)
                  }}
                  style={{ fontFamily: T.mono, fontSize: 13 }}
                />
              </div>
            )
          )}
        </div>
      )}

      <Reassurance>
        {hasExternal
          ? 'Orako messages you here when a question matches your expertise. Dashboard always works; a linked channel is just faster.'
          : "Your team hasn't connected Slack/Discord/Teams yet — you'll get questions in the dashboard, and can switch later in Settings."}
      </Reassurance>

      <JoinServerCard invites={invites} discordConnected={!!connectedBindings.discordUserId} />

      <StepFooter
        onContinue={() => void handleContinue()}
        continueLoading={saving}
        continueDisabled={loading || !bindingIsValid}
      />
    </div>
  )
}

// DiscordBind lets the member self-bind their Discord id via the existing
// `/discord/oauth/install` flow — the same one ConnectionsPage/Notifications use
// — instead of pasting a snowflake. A set id shows as "Connected ✓"; the manual
// text entry stays available under "Enter it manually instead" so nothing
// regresses where OAuth isn't configured.
function DiscordBind({
  connectedId,
  manualValue,
  manualOpen,
  error,
  onToggleManual,
  onManualChange,
  onError,
}: {
  connectedId: string
  manualValue: string
  manualOpen: boolean
  error: string | null
  onToggleManual: () => void
  onManualChange: (v: string) => void
  onError: (e: unknown) => void
}) {
  return (
    <div style={{ marginTop: 4, display: 'flex', flexDirection: 'column', gap: 12 }}>
      {connectedId && !manualOpen ? (
        <div
          style={{
            display: 'flex',
            alignItems: 'center',
            gap: 8,
            fontSize: 13.5,
            fontWeight: 600,
            color: T.successInk,
            background: T.successBg,
            border: `1px solid ${T.successBorder}`,
            borderRadius: T.rMd,
            padding: '11px 14px',
          }}
        >
          <Icon name="check" size={16} color={T.success} strokeWidth={2.6} />
          Discord connected ✓
        </div>
      ) : (
        <Button
          variant="secondary"
          onClick={() =>
            connectDiscord().catch(() =>
              onError('Discord one-click connect isn’t set up yet — enter your ID manually below.'),
            )
          }
        >
          <ProviderLogo kind="discord" size={16} />
          Connect Discord
        </Button>
      )}

      <button
        type="button"
        onClick={onToggleManual}
        style={{
          alignSelf: 'flex-start',
          background: 'none',
          border: 'none',
          padding: 0,
          fontSize: 12.5,
          fontWeight: 600,
          color: T.accent,
          cursor: 'pointer',
          fontFamily: 'inherit',
        }}
      >
        {manualOpen ? 'Hide manual entry' : 'Enter it manually instead'}
      </button>

      {manualOpen && (
        <div>
          <Input
            label={BINDING_META.discord?.label ?? 'Your Discord user ID'}
            placeholder={BINDING_META.discord?.placeholder ?? '123456789012345678'}
            value={manualValue}
            error={error}
            onChange={e => onManualChange(e.target.value)}
            style={{ fontFamily: T.mono, fontSize: 13 }}
          />
          <DiscordIdHint />
        </div>
      )}
    </div>
  )
}

// JoinServerCard is the PREREQUISITE nudge into the org's community server:
// being in the server is what lets Orako open a private thread and @-mention the
// member instead of leaving them dashboard-only. For Discord the primary path is
// the server-side auto-join (guilds.join) that fires on Connect Discord; this
// manual invite is the safety net when the auto-join is unavailable or fails —
// and, for Slack, the only path. Hidden entirely when no invite URL is set.
function JoinServerCard({ invites, discordConnected }: { invites: CommunityInvites; discordConnected: boolean }) {
  const targets: { key: 'discord' | 'slack'; label: string; url: string }[] = []
  if (invites.discord) targets.push({ key: 'discord', label: 'Join our Discord', url: invites.discord })
  if (invites.slack) targets.push({ key: 'slack', label: 'Join our Slack', url: invites.slack })

  if (targets.length === 0) return null

  // When Discord is already connected the auto-join likely already added them, so
  // the manual Discord link is framed as a fallback rather than a fresh ask.
  const lead =
    discordConnected && invites.discord
      ? 'Connecting Discord also adds you to the server. If Orako still can’t reach you there, use the invite below.'
      : 'Join the server so Orako can open a private thread and @-mention you. Without it, you’ll only see questions in the dashboard.'

  return (
    <div
      style={{
        marginTop: 16,
        background: T.surfaceAlt,
        border: `1px solid ${T.border}`,
        borderRadius: T.rMd,
        padding: '16px 18px',
      }}
    >
      <div style={{ fontSize: 14.5, fontWeight: 700, color: T.text }}>Join your team’s server</div>
      <p style={{ fontSize: 13, color: T.subtle, lineHeight: 1.55, margin: '6px 0 0' }}>{lead}</p>
      <div style={{ display: 'flex', flexWrap: 'wrap', gap: 10, marginTop: 14 }}>
        {targets.map(t => (
          <a
            key={t.key}
            href={t.url}
            target="_blank"
            rel="noreferrer"
            style={{
              display: 'inline-flex',
              alignItems: 'center',
              gap: 8,
              height: 40,
              padding: '0 16px',
              borderRadius: T.rMd,
              background: T.surface,
              border: `1px solid ${T.borderStrong}`,
              color: T.body,
              fontSize: 13.5,
              fontWeight: 600,
              textDecoration: 'none',
            }}
          >
            <ProviderLogo kind={t.key} size={16} />
            {t.label}
            <span style={{ fontSize: 14 }}>↗</span>
          </a>
        ))}
      </div>
    </div>
  )
}

// ── Step 4 · Connect your agent ──────────────────────────────────────────────

function Step3Agent({ onFinish }: { onFinish: () => void }) {
  return (
    <div>
      <h4 style={{ fontSize: 16, fontWeight: 700, color: T.text, margin: '0 0 6px' }}>Connect your agent</h4>
      <p style={{ fontSize: 13.5, color: T.muted, margin: 0 }}>
        Register Orako's remote MCP server in Claude Code, Codex or any MCP client, then authorize once inside the
        agent. Takes about a minute.
      </p>

      <div style={{ marginTop: 22 }}>
        <ConnectAgent />
      </div>

      <Reassurance>
        You can always come back to this from Connect agent in the sidebar — nothing here is a one-time offer.
      </Reassurance>

      <StepFooter onContinue={onFinish} continueLabel="Finish" />
    </div>
  )
}
