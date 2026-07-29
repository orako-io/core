// Get started — role-adapted onboarding (from the "Guest Onboarding" mockup).
//
// One full page, rendered in the mockup's visual language and adapted by role:
//   - guest (pending) + team (active non-admin): Welcome → the loop → Connect
//     your agent → Try your first ask → (optional) Get pinged → "I'm all set".
//   - admin: the same visual language PLUS the owner steps they need (invite
//     team, connect a messaging app, plan/billing) and the trial banner.
// Dismiss is permanent and per-member (server-side, survives reloads and other
// devices); Undo brings it back. Once dismissed, a teammate lands on their Inbox
// and an admin on the KPI Dashboard.

import { useCallback, useEffect, useState, type ReactNode } from 'react'
import { Navigate, useNavigate } from 'react-router'
import { api, connectDiscord, setOnboardingDismissed, communityInvites, type CommunityInvites } from '../lib/client'
import { useIdentity } from '../lib/identity'
import { useBillingStatus } from '../lib/billing-status'
import { useEdition } from '../lib/edition'
import { useToast, toastMessage } from '../lib/toast'
import { Page, PageTitle, Card, btnStyle, inputStyle } from '../components/Layout'
import { ErrorBanner } from '../components/ErrorBanner'
import { Spinner } from '../components/Spinner'
import { Icon } from '../components/Icon'
import { ProviderLogo } from '../components/ProviderLogos'
import { DashboardPage } from './DashboardPage'
import { T, labelStyle } from '../lib/theme'

function cap(s: string): string {
  return s ? s.charAt(0).toUpperCase() + s.slice(1) : s
}

// "Val Brochard" → "Val"; "hi@valensto.com" → "Hi" (local part, capitalized).
function firstNameOf(displayName: string): string {
  const name = displayName.trim()
  if (!name) return ''
  if (name.includes('@')) return cap(name.split('@')[0].split(/[._-]/)[0])
  return name.split(/\s+/)[0]
}

// mcpOrigin is the instance's own origin, so the connect command/prompt use the
// real reachable URL instead of a hardcoded domain.
function mcpOrigin(): string {
  return typeof window !== 'undefined' ? window.location.origin : ''
}

interface Profile {
  firstName: string
  orgName: string
  discordConnected: boolean
}

export function GetStartedPage() {
  const { isOrgAdmin, accountOnly, onboardingDismissed, refresh } = useIdentity()
  const toast = useToast()

  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<unknown>(null)
  const [profile, setProfile] = useState<Profile | null>(null)
  const [invites, setInvites] = useState<CommunityInvites | null>(null)
  // Shown right after clicking "I'm all set", so the confirmation banner + Undo
  // are visible even though identity.onboardingDismissed has flipped true (which
  // would otherwise redirect a teammate to their Inbox).
  const [dismissedNow, setDismissedNow] = useState(false)

  const loadProfile = useCallback(async () => {
    const [m, org, inv] = await Promise.all([
      api.getMember().catch(() => null),
      api.getOrganization().catch(() => null),
      communityInvites().catch(() => null),
    ])
    setInvites(inv)
    setProfile({
      firstName: firstNameOf(m?.member?.firstName || m?.member?.displayName || ''),
      orgName: org?.name ?? '',
      discordConnected: !!m?.member?.discordUserId,
    })
  }, [])

  useEffect(() => {
    let alive = true
    ;(async () => {
      setLoading(true)
      setError(null)
      try {
        await loadProfile()
      } catch (e) {
        if (alive) setError(e)
      } finally {
        if (alive) setLoading(false)
      }
    })()
    return () => {
      alive = false
    }
  }, [loadProfile])

  // Reflect a Discord bind completed in the other tab: on window focus, refetch
  // the member so "Discord connected ✓" appears without losing this page.
  const refetchDiscord = useCallback(() => {
    api
      .getMember()
      .then(m => setProfile(p => (p ? { ...p, discordConnected: !!m.member?.discordUserId } : p)))
      .catch(() => {})
  }, [])
  useEffect(() => {
    window.addEventListener('focus', refetchDiscord)
    return () => window.removeEventListener('focus', refetchDiscord)
  }, [refetchDiscord])

  // A brand-new account with no org yet still creates one here (the legitimate
  // org-creator path — an invited joiner is never account-only, so this never
  // shows the create-org screen to a guest).
  if (accountOnly) {
    return <OrgOnboarding onCreated={refresh} />
  }

  async function dismiss() {
    try {
      await setOnboardingDismissed(true)
      setDismissedNow(true)
      await refresh()
    } catch (e) {
      toast.error(toastMessage(e))
    }
  }

  async function undo() {
    try {
      await setOnboardingDismissed(false)
      setDismissedNow(false)
      await refresh()
    } catch (e) {
      toast.error(toastMessage(e))
    }
  }

  if (loading) {
    return (
      <Page width={860}>
        <div style={{ display: 'flex', justifyContent: 'center', padding: '60px 0' }}>
          <Spinner size={22} />
        </div>
      </Page>
    )
  }

  // Just dismissed → the confirmation banner + Undo (stays on this page once).
  if (dismissedNow) {
    return (
      <Page width={860}>
        <DismissedBanner onUndo={() => void undo()} />
      </Page>
    )
  }

  // Already dismissed on a prior visit (reload / other device) → no onboarding.
  // A teammate lands on their Inbox; an admin keeps the KPI Dashboard as home.
  if (onboardingDismissed) {
    return isOrgAdmin ? <DashboardPage /> : <Navigate to="/inbox" replace />
  }

  const origin = mcpOrigin()

  return (
    <Page width={860}>
      {error != null && <ErrorBanner error={error} />}

      <AdminTrialBanner show={isOrgAdmin} />

      <Header orgName={profile?.orgName ?? ''} firstName={profile?.firstName ?? ''} />

      <LoopStrip />

      <div style={{ display: 'flex', flexDirection: 'column', gap: 16, marginTop: 22 }}>
        <StepCard n={1} title="Connect your agent">
          <p style={stepLead}>
            Register Orako&rsquo;s remote MCP server in your agent — one command, then authorize once inside the agent.
          </p>
          <Terminal command={`claude mcp add --transport http orako ${origin}/mcp`} />
          <p style={{ fontSize: 12.5, color: T.subtle, marginTop: 12, lineHeight: 1.55 }}>
            Codex, Cursor, VS Code? Same URL — add <code style={inlineCode}>{origin}/mcp</code> as a remote MCP
            server.{' '}
            <a href="https://orako.io" target="_blank" rel="noreferrer" style={{ color: T.accent, fontWeight: 600, textDecoration: 'none' }}>
              See per-client steps
            </a>
          </p>
        </StepCard>

        <StepCard n={2} title="Try your first ask">
          <p style={stepLead}>Paste this into your agent — it searches your team&rsquo;s history, then asks the right specialist if it&rsquo;s not there.</p>
          <PromptBlock
            topic="how Orako works"
            text="Use Orako: search our knowledge for {TOPIC}. If it's not there, ask the right specialist and wait for the answer."
          />
          <div style={{ marginTop: 16 }}>
            <div style={orTry}>OR TRY</div>
            <VariantRow text="How do I set up Discord for Orako?" />
            <VariantRow text="How does a teammate join my Orako org, and what can they do?" />
          </div>
        </StepCard>

        {isOrgAdmin && <AdminOwnerSteps />}

        <GetPinged
          n={3}
          discordConnected={profile?.discordConnected ?? false}
          onConnectError={e => toast.error(toastMessage(e))}
        />

        <JoinServerCard invites={invites} />
      </div>

      <Footer onDismiss={() => void dismiss()} />
    </Page>
  )
}

// ── Header ───────────────────────────────────────────────────────────────────

function Header({ orgName, firstName }: { orgName: string; firstName: string }) {
  const org = orgName || 'Orako'
  return (
    <div style={{ marginBottom: 20 }}>
      <h3 style={{ fontSize: 27, fontWeight: 700, letterSpacing: '-.02em', color: T.text, margin: 0 }}>
        Welcome to {org}{firstName ? `, ${firstName}` : ''} 👋
      </h3>
      <p style={{ fontSize: 15, color: T.muted, marginTop: 8 }}>
        You&rsquo;re in. Two minutes to connect your agent and try it.
      </p>
    </div>
  )
}

// ── Loop strip ───────────────────────────────────────────────────────────────

function LoopStrip() {
  return (
    <div style={{ background: '#F4F3FD', border: '1px solid #E3E0FA', borderRadius: 13, padding: '16px 18px' }}>
      <div style={{ display: 'flex', alignItems: 'center', flexWrap: 'wrap', gap: 10 }}>
        <LoopItem>Your agent asks what it can&rsquo;t figure out</LoopItem>
        <Arrow />
        <LoopItem>a teammate answers</LoopItem>
        <Arrow />
        <LoopItem>it becomes searchable team history</LoopItem>
      </div>
      <div style={{ fontSize: 12.5, color: T.accentInk, marginTop: 12, lineHeight: 1.55 }}>
        Questions can be routed to you too — you answer, the agent on the other side unblocks.
      </div>
    </div>
  )
}

function LoopItem({ children }: { children: ReactNode }) {
  return <span style={{ fontSize: 13, fontWeight: 600, color: '#211E3B' }}>{children}</span>
}

function Arrow() {
  return <Icon name="arrowRight" size={15} color={T.accent} strokeWidth={2.2} />
}

// ── Step card ────────────────────────────────────────────────────────────────

function StepCard({ n, title, badge, children }: { n: number; title: string; badge?: string; children: ReactNode }) {
  return (
    <div style={{ background: '#fff', border: '1px solid #E9EBEE', borderRadius: 16, padding: '20px 22px' }}>
      <div style={{ display: 'flex', alignItems: 'center', gap: 12, marginBottom: 12 }}>
        <NumberCircle n={n} />
        <span style={{ fontSize: 16, fontWeight: 700, color: T.text }}>{title}</span>
        {badge && (
          <span style={{ fontSize: 10.5, fontWeight: 700, letterSpacing: '.04em', color: T.subtle, background: T.sunken, padding: '2px 8px', borderRadius: 10 }}>
            {badge}
          </span>
        )}
      </div>
      <div style={{ paddingLeft: 38 }}>{children}</div>
    </div>
  )
}

function NumberCircle({ n }: { n: number }) {
  return (
    <div
      style={{
        width: 26,
        height: 26,
        borderRadius: '50%',
        background: T.accent,
        color: '#fff',
        fontSize: 13,
        fontWeight: 700,
        display: 'flex',
        alignItems: 'center',
        justifyContent: 'center',
        flex: 'none',
      }}
    >
      {n}
    </div>
  )
}

// ── Terminal + copy ──────────────────────────────────────────────────────────

function Terminal({ command }: { command: string }) {
  return (
    <div
      style={{
        background: '#1B1F2A',
        borderRadius: 11,
        padding: '13px 14px',
        display: 'flex',
        alignItems: 'center',
        gap: 12,
      }}
    >
      <code style={{ flex: 1, fontFamily: T.mono, fontSize: 12.5, color: '#E7EAF0', overflowX: 'auto', whiteSpace: 'nowrap' }}>
        <span style={{ color: '#6B7280', userSelect: 'none' }}>$ </span>
        {command}
      </code>
      <CopyButton text={command} dark />
    </div>
  )
}

function CopyButton({ text, dark = false }: { text: string; dark?: boolean }) {
  const [copied, setCopied] = useState(false)
  function copy() {
    navigator.clipboard?.writeText(text).then(
      () => {
        setCopied(true)
        window.setTimeout(() => setCopied(false), 1600)
      },
      () => {},
    )
  }
  const base: React.CSSProperties = {
    display: 'inline-flex',
    alignItems: 'center',
    gap: 6,
    flex: 'none',
    height: 30,
    padding: '0 11px',
    borderRadius: 8,
    fontSize: 12.5,
    fontWeight: 600,
    fontFamily: 'inherit',
    cursor: 'pointer',
    border: dark ? '1px solid #333A48' : `1px solid ${T.borderStrong}`,
    background: dark ? '#242A36' : T.surface,
    color: copied ? (dark ? '#6EE7A6' : T.successInk) : dark ? '#C7CCD6' : T.body,
  }
  return (
    <button onClick={copy} style={base} aria-label="Copy">
      <Icon name={copied ? 'check' : 'copy'} size={13} color={copied ? (dark ? '#6EE7A6' : T.success) : dark ? '#C7CCD6' : T.subtle} strokeWidth={2.2} />
      {copied ? 'Copied ✓' : 'Copy'}
    </button>
  )
}

// ── Prompt block + variants ──────────────────────────────────────────────────

function PromptBlock({ topic, text }: { topic: string; text: string }) {
  const [before, after] = text.split('{TOPIC}')
  const full = text.replace('{TOPIC}', topic)
  return (
    <div style={{ background: '#F7F8FA', borderLeft: `3px solid ${T.accent}`, borderRadius: 10, padding: '13px 15px', display: 'flex', alignItems: 'flex-start', gap: 12 }}>
      <div style={{ flex: 1, fontFamily: T.mono, fontSize: 12.5, color: T.body, lineHeight: 1.6 }}>
        {before}
        <span style={{ color: T.accent, fontWeight: 600 }}>{topic}</span>
        {after}
      </div>
      <CopyButton text={full} />
    </div>
  )
}

function VariantRow({ text }: { text: string }) {
  return (
    <div style={{ display: 'flex', alignItems: 'center', gap: 12, padding: '9px 0', borderTop: `1px solid ${T.line}` }}>
      <code style={{ flex: 1, fontFamily: T.mono, fontSize: 12, color: T.muted, lineHeight: 1.5 }}>{text}</code>
      <CopyButton text={text} />
    </div>
  )
}

// ── Step 3 · Get pinged where you work ───────────────────────────────────────

function GetPinged({
  n,
  discordConnected,
  onConnectError,
}: {
  n: number
  discordConnected: boolean
  onConnectError: (e: unknown) => void
}) {
  const [choice, setChoice] = useState<'discord' | 'email'>(discordConnected ? 'discord' : 'email')

  const note =
    choice === 'discord'
      ? 'Questions will DM you on Discord. Change it anytime in Notifications.'
      : "We'll email you when a question is routed to you. Change it anytime in Notifications."

  return (
    <StepCard n={n} title="Get pinged where you work" badge="OPTIONAL">
      {discordConnected ? (
        <div style={{ display: 'flex', alignItems: 'center', gap: 8, fontSize: 13.5, fontWeight: 600, color: T.successInk, background: T.successBg, border: `1px solid ${T.successBorder}`, borderRadius: T.rMd, padding: '11px 14px' }}>
          <Icon name="check" size={16} color={T.success} strokeWidth={2.6} />
          Discord connected ✓
        </div>
      ) : (
        <div style={{ display: 'flex', gap: 10, flexWrap: 'wrap' }}>
          <ToggleButton
            selected={choice === 'discord'}
            onClick={() => {
              setChoice('discord')
              // New tab (client.connectDiscord) so this form is never replaced.
              connectDiscord().catch(onConnectError)
            }}
          >
            <ProviderLogo kind="discord" size={16} />
            Connect Discord
          </ToggleButton>
          <ToggleButton selected={choice === 'email'} onClick={() => setChoice('email')}>
            Keep email
          </ToggleButton>
        </div>
      )}
      <p style={{ fontSize: 12.5, color: T.subtle, marginTop: 12, lineHeight: 1.55 }}>{discordConnected ? 'Questions will DM you on Discord. Change it anytime in Notifications.' : note}</p>
    </StepCard>
  )
}

function ToggleButton({ selected, onClick, children }: { selected: boolean; onClick: () => void; children: ReactNode }) {
  return (
    <button
      onClick={onClick}
      style={{
        display: 'inline-flex',
        alignItems: 'center',
        gap: 8,
        height: 40,
        padding: '0 16px',
        borderRadius: T.rMd,
        fontSize: 13.5,
        fontWeight: 600,
        fontFamily: 'inherit',
        cursor: 'pointer',
        border: selected ? `1.5px solid ${T.accent}` : `1px solid ${T.borderStrong}`,
        background: selected ? T.accent : T.surface,
        color: selected ? '#fff' : T.body,
      }}
    >
      {children}
    </button>
  )
}

// ── Join the community server ────────────────────────────────────────────────

// JoinServerCard surfaces the org's community invite link (Discord/Slack) so a
// new member joins the actual server — which is what lets Orako open a private
// thread for them instead of a DM. Hidden entirely until an admin sets an invite
// URL (Integrations → Discord/Slack). Shown to every role.
function JoinServerCard({ invites }: { invites: CommunityInvites | null }) {
  const targets: { key: 'discord' | 'slack'; label: string; url: string }[] = []
  if (invites?.discord) targets.push({ key: 'discord', label: 'Join our Discord', url: invites.discord })
  if (invites?.slack) targets.push({ key: 'slack', label: 'Join our Slack', url: invites.slack })
  if (targets.length === 0) return null

  return (
    <div style={{ background: '#fff', border: '1px solid #E9EBEE', borderRadius: 16, padding: '20px 22px' }}>
      <span style={{ fontSize: 16, fontWeight: 700, color: T.text }}>Join your team&rsquo;s server</span>
      <p style={{ fontSize: 14, color: T.muted, lineHeight: 1.55, margin: '6px 0 0' }}>
        Join the server so a question opens a private thread for you — not a DM.
      </p>
      <div style={{ display: 'flex', gap: 10, flexWrap: 'wrap', marginTop: 14 }}>
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
              fontSize: 13.5,
              fontWeight: 600,
              textDecoration: 'none',
              border: `1px solid ${T.borderStrong}`,
              background: T.surface,
              color: T.body,
            }}
          >
            <ProviderLogo kind={t.key} size={16} />
            {t.label}
          </a>
        ))}
      </div>
    </div>
  )
}

// ── Admin owner steps ────────────────────────────────────────────────────────

function AdminOwnerSteps() {
  const navigate = useNavigate()
  const billingStatus = useBillingStatus()
  const { data: edition } = useEdition()
  const [membersReady, setMembersReady] = useState(false)
  const [messagingReady, setMessagingReady] = useState(false)

  useEffect(() => {
    let alive = true
    void Promise.all([api.listMembers(), api.listConnectedChannels()])
      .then(([members, channels]) => {
        if (!alive) return
        setMembersReady((members.members ?? []).length > 1)
        setMessagingReady((channels.channels ?? []).some(channel => channel !== 'dashboard'))
      })
      .catch(() => {})
    return () => {
      alive = false
    }
  }, [])

  const billingReady = __SAAS__ ? billingStatus === 'active' : edition?.edition === 'licensed'
  const rows: { title: string; sub: string; to: string; cta: string; done: boolean }[] = [
    {
      title: 'Invite your team',
      sub: membersReady ? 'Your organization already has teammates.' : 'Add the teammates who answer questions — PMs, designers, backend devs.',
      to: '/members',
      cta: membersReady ? 'View team' : 'Invite teammates',
      done: membersReady,
    },
    {
      title: 'Connect a messaging app',
      sub: messagingReady ? 'A messaging integration is connected.' : 'Ping teammates as Slack or Discord DMs. Without it, questions land in the dashboard Inbox.',
      to: '/integrations',
      cta: messagingReady ? 'Manage' : 'Connect',
      done: messagingReady,
    },
    {
      title: __SAAS__ ? 'Choose your plan' : 'Manage your license',
      sub: billingReady
        ? (__SAAS__ ? 'Your paid plan is active.' : 'Your self-hosted license is active.')
        : (__SAAS__ ? 'Start your subscription so Orako keeps running past the trial.' : 'Activate a license key to lift the community limits.'),
      to: __SAAS__ ? '/billing' : '/license',
      cta: billingReady ? 'Manage' : (__SAAS__ ? 'View plans' : 'Open license'),
      done: billingReady,
    },
  ]
  const complete = rows.every(row => row.done)
  return (
    <div style={{ background: '#fff', border: '1px solid #E9EBEE', borderRadius: 16, padding: '20px 22px' }}>
      <div style={{ fontSize: 12, fontWeight: 700, letterSpacing: '.05em', color: T.label, marginBottom: 14 }}>
        {complete ? 'ORGANIZATION READY' : 'SET UP YOUR ORGANIZATION'}
      </div>
      <div style={{ display: 'flex', flexDirection: 'column', gap: 10 }}>
        {rows.map(r => (
          <div
            key={r.to}
            onClick={() => navigate(r.to)}
            style={{ display: 'flex', alignItems: 'center', gap: 14, border: `1px solid ${T.border}`, borderRadius: 12, padding: '13px 16px', cursor: 'pointer' }}
          >
            <span style={{ width: 24, height: 24, borderRadius: '50%', flex: 'none', display: 'inline-flex', alignItems: 'center', justifyContent: 'center', background: r.done ? T.successBg : T.bg, border: `1px solid ${r.done ? T.successBorder : T.border}` }}>
              <Icon name={r.done ? 'check' : 'chevronRight'} size={14} color={r.done ? T.success : T.faint} strokeWidth={2.3} />
            </span>
            <div style={{ flex: 1 }}>
              <div style={{ fontSize: 14.5, fontWeight: 600, color: r.done ? T.successInk : T.body }}>{r.title}</div>
              <div style={{ fontSize: 13, color: T.subtle, marginTop: 2 }}>{r.sub}</div>
            </div>
            <span style={{ fontSize: 13, fontWeight: 600, color: T.accent, whiteSpace: 'nowrap' }}>{r.cta}</span>
            <Icon name="chevronRight" size={16} color="#C4C9D0" />
          </div>
        ))}
      </div>
    </div>
  )
}

// ── Admin trial banner ───────────────────────────────────────────────────────

function AdminTrialBanner({ show }: { show: boolean }) {
  const navigate = useNavigate()
  const billingStatus = useBillingStatus()
  // Trial/upgrade is ADMIN-ONLY and SaaS-only, and only while not yet active.
  if (!show || !__SAAS__ || billingStatus === 'active') return null
  return (
    <div
      style={{
        display: 'flex',
        alignItems: 'center',
        justifyContent: 'space-between',
        background: T.accentSofter,
        border: '1px solid #E3E0FA',
        borderRadius: T.rLg,
        padding: '13px 16px',
        marginBottom: 22,
      }}
    >
      <div style={{ display: 'flex', alignItems: 'center', gap: 11 }}>
        <Icon name="clock" size={18} strokeWidth={1.9} color={T.accent} />
        <span style={{ fontSize: 14, color: T.accentInk }}>
          <strong style={{ color: '#211E3B' }}>You&rsquo;re on a free trial</strong> — subscribe to keep Orako running when it ends.
        </span>
      </div>
      <button
        onClick={() => navigate('/billing')}
        style={{ height: 34, padding: '0 16px', border: 'none', borderRadius: 8, background: T.accent, color: '#fff', fontSize: 13, fontWeight: 600, fontFamily: 'inherit', cursor: 'pointer' }}
      >
        View plans
      </button>
    </div>
  )
}

// ── Footer ───────────────────────────────────────────────────────────────────

function Footer({ onDismiss }: { onDismiss: () => void }) {
  return (
    <div style={{ marginTop: 26, borderTop: `1px solid ${T.borderSubtle}`, paddingTop: 22 }}>
      <div style={{ fontSize: 11.5, fontWeight: 700, letterSpacing: '.05em', color: T.label, marginBottom: 10 }}>
        WHAT YOU CAN DO HERE
      </div>
      <div style={{ display: 'flex', flexWrap: 'wrap', gap: 8, marginBottom: 20 }}>
        {['Ask via your agent', 'Answer teammates', 'Search history'].map(c => (
          <span key={c} style={{ fontSize: 12.5, fontWeight: 600, color: T.accentInk, background: T.accentSofter, border: `1px solid ${T.accentBorder}`, borderRadius: 20, padding: '5px 12px' }}>
            {c}
          </span>
        ))}
      </div>
      <div style={{ display: 'flex', alignItems: 'center', gap: 14, flexWrap: 'wrap' }}>
        <button onClick={onDismiss} style={{ ...btnStyle('primary'), height: 40, padding: '0 20px' }}>
          I&rsquo;m all set
        </button>
        <span style={{ fontSize: 12.5, color: T.subtle }}>Closes this page for good — you can reopen every setup area from the sidebar.</span>
      </div>
    </div>
  )
}

// ── After dismiss ────────────────────────────────────────────────────────────

function DismissedBanner({ onUndo }: { onUndo: () => void }) {
  const navigate = useNavigate()
  return (
    <div style={{ background: T.successBg, border: `1px solid ${T.successBorder}`, borderRadius: T.rLg, padding: '18px 20px' }}>
      <div style={{ display: 'flex', alignItems: 'center', gap: 10 }}>
        <Icon name="check" size={18} color={T.success} strokeWidth={2.6} />
        <div style={{ fontSize: 14.5, fontWeight: 700, color: T.successInk }}>Get started is done and won&rsquo;t come back.</div>
      </div>
      <p style={{ fontSize: 13.5, color: T.body, margin: '8px 0 0', paddingLeft: 28 }}>
        Everything you need is in the sidebar.
      </p>
      <div style={{ display: 'flex', gap: 12, marginTop: 16, paddingLeft: 28, flexWrap: 'wrap' }}>
        <button onClick={() => navigate('/conversations')} style={{ ...btnStyle('primary'), height: 36 }}>
          Go to conversations
        </button>
        <button onClick={onUndo} style={{ ...btnStyle('secondary'), height: 36 }}>
          Undo
        </button>
      </div>
    </div>
  )
}

// ── Shared styles ────────────────────────────────────────────────────────────

const stepLead: React.CSSProperties = { fontSize: 13.5, color: T.muted, margin: '0 0 12px', lineHeight: 1.55 }
const orTry: React.CSSProperties = { fontSize: 11, fontWeight: 700, letterSpacing: '.06em', color: T.faint, margin: '0 0 2px' }
const inlineCode: React.CSSProperties = { fontFamily: T.mono, fontSize: 12, color: T.accent, background: T.accentSoft, padding: '1px 5px', borderRadius: 5 }

// ── Org creation (account-only first run) ────────────────────────────────────

// First-run onboarding for an authenticated account with no organization yet.
// Creates the org (and its default project) server-side, then refreshes identity.
function OrgOnboarding({ onCreated }: { onCreated: () => Promise<void> }) {
  const [name, setName] = useState('')
  const [creating, setCreating] = useState(false)
  const [error, setError] = useState<unknown>(null)

  async function handleCreate() {
    if (creating || !name.trim()) return
    setCreating(true)
    setError(null)
    try {
      const res = await api.createOrganization(name.trim())
      // Make the new org the active one before identity re-resolves.
      localStorage.setItem('orako:activeOrg', res.organizationId)
      await onCreated()
    } catch (e) {
      setError(e)
    } finally {
      setCreating(false)
    }
  }

  return (
    <Page width={560}>
      <PageTitle subtitle="One more step: name your organization. It holds your team, projects, conversations and subscription.">
        Create your organization
      </PageTitle>

      <Card>
        {error != null && <ErrorBanner error={error} />}
        <label style={labelStyle}>Organization name</label>
        <input
          type="text"
          placeholder="Acme Inc."
          value={name}
          onChange={e => setName(e.target.value)}
          onKeyDown={e => e.key === 'Enter' && handleCreate()}
          style={inputStyle()}
          disabled={creating}
          autoFocus
        />
        <div style={{ marginTop: 16 }}>
          <button onClick={handleCreate} style={btnStyle('primary')} disabled={creating || !name.trim()}>
            {creating ? <Spinner color="#fff" /> : <><Icon name="arrowRight" size={16} strokeWidth={2.2} color="#fff" />Create organization</>}
          </button>
        </div>
        <p style={{ fontSize: 12.5, color: T.subtle, margin: '12px 0 0' }}>
          You&rsquo;ll become the admin, with a first project ready to go — add more any time from the sidebar.
        </p>
      </Card>
    </Page>
  )
}
