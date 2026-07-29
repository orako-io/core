// Team / Members — org-global roster (design: "Team List"), presented as three
// tiers derived from the two existing axes (member status + org-admin flag):
//   • Admin   — active member, org-admin flag set.
//   • Team    — active member, no admin flag.
//   • Invited — status "pending": a join-code redeemer awaiting admin approval.
//     Non-routable; hidden from the roster everyone sees and surfaced only to
//     admins (the server appends pending members to ListMembers for admins).
// One rich row per active member from ListMembers (identity, projects, expertise,
// channel, availability) carries an Admin/Team tier badge. Admins get inline
// promote/demote (Make admin / Remove admin) and an Invited group with
// Promote to Team / Remove. The row links to /members/:id (Edit Person).

import { Fragment, useEffect, useMemo, useRef, useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { useIdentity } from '../lib/identity'
import { useToast, toastMessage } from '../lib/toast'
import { api, type OrgMember, type ProjectSummary, type JoinCode } from '../lib/client'
import { Page, usePageHeader } from '../components/Layout'
import { Button } from '../components/Button'
import { Modal } from '../components/Modal'
import { Spinner } from '../components/Spinner'
import { Icon } from '../components/Icon'
import { CapNudge } from '../components/UpgradeCard'
import { useEdition, atCap } from '../lib/edition'
import { useBillingOverview } from '../lib/billing-status'
import { cacheGet, cacheSet } from '../lib/queryCache'
import { T } from '../lib/theme'

const ACCENT = '#5850EC'
// Custom dropdown chevron so the arrow isn't glued to the right border of the
// native <select> (webkit ignores padding-right on the native marker). Paired
// with appearance:none + right padding below.
const CHEVRON_BG =
  "url(\"data:image/svg+xml,%3Csvg xmlns='http://www.w3.org/2000/svg' width='12' height='12' viewBox='0 0 24 24' fill='none' stroke='%236B7280' stroke-width='2.5' stroke-linecap='round' stroke-linejoin='round'%3E%3Cpolyline points='6 9 12 15 18 9'/%3E%3C/svg%3E\")"

const AVAIL: Record<string, { label: string; dot: string; text: string; bg: string }> = {
  active: { label: 'Active', dot: '#16A34A', text: '#15803D', bg: '#E9F7EF' },
  on_leave: { label: 'On leave', dot: '#E0A320', text: '#B45309', bg: '#FDF3E0' },
  deactivated: { label: 'Deactivated', dot: '#9AA1AC', text: '#6B7280', bg: '#F1F3F5' },
  invited: { label: 'Invited', dot: '#B7BDC6', text: '#6B7280', bg: '#F1F3F5' },
}
const CHANNEL_LABEL: Record<string, string> = {
  dashboard: 'Dashboard',
  slack: 'Slack',
  teams: 'Teams',
  telegram: 'Telegram',
  discord: 'Discord',
}
const AV_COLORS: [string, string][] = [
  ['#F3D9A4', '#7A5A16'], ['#D9E5F7', '#2C5C99'], ['#F7DAD9', '#99342C'],
  ['#E5DAF3', '#6A429E'], ['#DBEAD9', '#3A6B33'], ['#EEE1D3', '#8A5A2B'],
]

function initials(seed: string): string {
  const parts = seed.trim().split(/\s+/)
  const s = parts.length > 1 ? parts[0][0] + parts[parts.length - 1][0] : seed.replace(/[^a-zA-Z0-9]/g, '').slice(0, 2)
  return (s || 'ME').toUpperCase()
}
function avatarColor(seed: string): [string, string] {
  let h = 0
  for (let i = 0; i < seed.length; i++) h = (h * 31 + seed.charCodeAt(i)) & 0xffff
  return AV_COLORS[h % AV_COLORS.length]
}
function memberName(m: OrgMember): string {
  return m.displayName || `${m.firstName ?? ''} ${m.lastName ?? ''}`.trim() || m.email || 'Untitled'
}
function memberDomains(m: OrgMember): string[] {
  return [...new Set((m.projects ?? []).flatMap(p => p.domains ?? []))]
}

// Tier is derived, never stored: an active member with the org-admin flag is
// Admin, otherwise Team. (Pending members are the Invited tier, handled apart.)
type Tier = 'admin' | 'team'
function tierOf(m: OrgMember): Tier {
  return m.isOrgAdmin ? 'admin' : 'team'
}
const TIER_BADGE: Record<Tier, { label: string; text: string; bg: string }> = {
  admin: { label: 'Admin', text: '#5B3DBD', bg: '#EFEBFB' },
  team: { label: 'Team', text: '#2C5C99', bg: '#E7EFFA' },
}
function TierBadge({ tier }: { tier: Tier }) {
  const t = TIER_BADGE[tier]
  return (
    <span style={{ fontSize: 11, fontWeight: 600, color: t.text, background: t.bg, padding: '1px 7px', borderRadius: 20, whiteSpace: 'nowrap' }}>
      {t.label}
    </span>
  )
}

// Person, Projects, Expertise, Reachable on. The Availability and Manage
// columns were retired: availability now shows as an inline badge in the Person
// cell, and all member management moved to the member detail page.
const COLS = 'minmax(220px,1.7fr) minmax(150px,1.1fr) minmax(160px,1.3fr) minmax(140px,1fr)'

export function MembersPage() {
  const navigate = useNavigate()
  const toast = useToast()
  const { memberID, isOrgAdmin } = useIdentity()

  const [members, setMembers] = useState<OrgMember[]>(() => cacheGet<OrgMember[]>('org-members') ?? [])
  const [projects, setProjects] = useState<ProjectSummary[]>(() => cacheGet<ProjectSummary[]>('projects-list') ?? [])
  const firstLoad = useRef(cacheGet('org-members') === undefined)
  const [loading, setLoading] = useState(firstLoad.current)

  const [projectFilter, setProjectFilter] = useState('all')
  const [expertiseFilter, setExpertiseFilter] = useState('all')
  const [availFilter, setAvailFilter] = useState('all')
  const [search, setSearch] = useState('')

  async function fetchMembers() {
    try {
      const [r, pr] = await Promise.all([api.listMembers(), api.listProjects()])
      const rows = r.members ?? []
      setMembers(rows)
      setProjects(pr.projects)
      cacheSet('org-members', rows)
      cacheSet('projects-list', pr.projects)
    } catch (e) {
      if (firstLoad.current) toast.error(toastMessage(e))
    } finally {
      setLoading(false)
    }
  }

  useEffect(() => {
    void fetchMembers()
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])

  const projName = (id: string) => projects.find(p => p.id === id)?.name ?? id.slice(0, 8)
  // Three buckets: the active roster (Admin + Team tiers), emailed-but-not-joined
  // invites, and the Invited tier — join-code redeemers awaiting an admin's
  // approval (status "pending"). The server only returns pending members to an
  // admin, so pendingApprovals is always empty for a non-admin viewer.
  const roster = members.filter(m => m.status !== 'invited' && m.status !== 'pending')
  const pending = members.filter(m => m.status === 'invited')
  const pendingApprovals = members.filter(m => m.status === 'pending')

  // Real billable seat count (active roster + pending invites) — never clamped
  // to a bogus cap; the cap itself is dynamic (see seatCap below).
  const seatsUsed = roster.filter(m => m.status !== 'deactivated').length + pending.length
  const onLeaveCount = roster.filter(m => m.status === 'on_leave').length

  const expertiseOptions = useMemo(() => {
    const counts = new Map<string, number>()
    for (const m of roster) for (const d of memberDomains(m)) counts.set(d, (counts.get(d) ?? 0) + 1)
    return [...counts.entries()].sort((a, b) => a[0].localeCompare(b[0]))
  }, [roster])

  const anyFilter = projectFilter !== 'all' || expertiseFilter !== 'all' || availFilter !== 'all' || search.trim() !== ''
  const rows = useMemo(() => {
    const q = search.trim().toLowerCase()
    return roster
      .filter(m => {
        if (projectFilter !== 'all' && !(m.projects ?? []).some(p => p.projectId === projectFilter)) return false
        if (expertiseFilter !== 'all' && !memberDomains(m).includes(expertiseFilter)) return false
        if (availFilter !== 'all' && m.status !== availFilter) return false
        if (q && !memberName(m).toLowerCase().includes(q) && !(m.email ?? '').toLowerCase().includes(q)) return false
        return true
      })
      // Admins first, then Team; alphabetical within each tier.
      .sort((a, b) => {
        if (!!a.isOrgAdmin !== !!b.isOrgAdmin) return a.isOrgAdmin ? -1 : 1
        return memberName(a).localeCompare(memberName(b))
      })
  }, [roster, projectFilter, expertiseFilter, availFilter, search])

  // --- invite ---
  const [inviteOpen, setInviteOpen] = useState(false)
  const [inviteEmail, setInviteEmail] = useState('')
  const [inviteSending, setInviteSending] = useState(false)
  const [busyIds, setBusyIds] = useState<Set<string>>(new Set())

  // --- Invite by code (org join code) ---
  const [joinCode, setJoinCode] = useState<JoinCode | null>(null)
  const [joinCodeLoading, setJoinCodeLoading] = useState(true)
  const [joinCodeBusy, setJoinCodeBusy] = useState(false)
  const [copied, setCopied] = useState<string | null>(null)

  async function fetchJoinCode() {
    try {
      const c = await api.getJoinCode()
      setJoinCode(c)
    } catch {
      // Non-fatal: the panel simply shows the "generate" empty state.
      setJoinCode({ active: false })
    } finally {
      setJoinCodeLoading(false)
    }
  }

  useEffect(() => {
    if (!isOrgAdmin) {
      setJoinCodeLoading(false)
      return
    }
    void fetchJoinCode()
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [isOrgAdmin])

  async function generateCode() {
    if (joinCodeBusy) return
    setJoinCodeBusy(true)
    try {
      const c = await api.generateJoinCode()
      setJoinCode({ ...c, active: true })
      toast.success('Join code ready')
    } catch (e) {
      toast.error(toastMessage(e))
    } finally {
      setJoinCodeBusy(false)
    }
  }

  async function revokeCode() {
    if (joinCodeBusy) return
    setJoinCodeBusy(true)
    try {
      await api.revokeJoinCode()
      await fetchJoinCode()
      toast.info('Join code revoked')
    } catch (e) {
      toast.error(toastMessage(e))
    } finally {
      setJoinCodeBusy(false)
    }
  }

  const mcpUrl = `${window.location.origin}/mcp`
  const mcpCommand = `claude mcp add --transport http orako ${mcpUrl}`
  const joinLink = (code: string) => `${window.location.origin}/join/${code}`
  // Link-first: the invite link is the primary share (they click, sign in, and
  // join — no code to type). The MCP command is step 2, once they're in.
  const joinSnippet = (code: string) =>
    `Join our team on Orako — click this link, sign in, and you're in:\n` +
    `${joinLink(code)}\n\n` +
    `Then connect your coding agent:\n` +
    `claude mcp add --transport http orako ${mcpUrl}`

  async function copyText(text: string, label: string, key?: string) {
    try {
      await navigator.clipboard.writeText(text)
      toast.success(`${label} copied`)
      if (key) {
        setCopied(key)
        setTimeout(() => setCopied(c => (c === key ? null : c)), 1400)
      }
    } catch {
      toast.error('Copy failed')
    }
  }

  // Member management (promote/demote admin, promote a join-code redeemer to
  // Team, remove) now lives on the member detail page (EditPersonPage). The
  // roster here is read-only and navigates to that page on click.

  // Invited teammates always land on the org's default project (resolved by the
  // project literally named "default"; the identity itself falls back to the
  // first project). Expertise is no longer chosen here — each member picks it
  // during their own onboarding, and an admin can still change it later.
  const defaultProjectId = useMemo(
    () => (projects.find(p => p.name.trim().toLowerCase() === 'default') ?? projects[0])?.id ?? '',
    [projects],
  )

  function openInvite() {
    setInviteEmail('')
    setInviteOpen(true)
  }

  const { data: edition } = useEdition()
  const membersCapped = atCap(edition, 'members', roster.length)

  // Dynamic seat cap for the "Seats used N of N" stat + progress bar:
  //  - SaaS: the purchased seat count from billing (e.g. 100). While the
  //    overview is still loading, leave it undefined so we show a bare count
  //    instead of clamping against a wrong number.
  //  - Self-host: the edition's maxMembers limit. 0 means UNLIMITED → undefined,
  //    which renders "Seats used N" with no "of N" and no progress bar.
  const billingOverview = useBillingOverview()
  const seatCap: number | undefined = __SAAS__
    ? billingOverview && billingOverview.seats > 0
      ? billingOverview.seats
      : undefined
    : edition && edition.limits.maxMembers > 0
      ? edition.limits.maxMembers
      : undefined

  usePageHeader(
    {
      actions: isOrgAdmin ? (
        <Button
          variant="primary"
          size="sm"
          disabled={membersCapped}
          title={membersCapped ? 'Community seat limit reached — upgrade to add more members' : undefined}
          onClick={openInvite}
        >
          <Icon name="plus" size={15} color="#fff" strokeWidth={2.4} />
          Invite member
        </Button>
      ) : undefined,
    },
    [isOrgAdmin, membersCapped],
  )

  async function sendInvite() {
    // Accept a pasted list: split on comma / newline / space / semicolon.
    const emails = Array.from(new Set(inviteEmail.split(/[\s,;]+/).map(e => e.trim().toLowerCase()).filter(Boolean)))
    if (emails.length === 0 || !defaultProjectId || inviteSending) return
    setInviteSending(true)
    try {
      let invited = 0
      let already = 0
      let bad = 0
      // Everyone lands on the default project with EMPTY expertise — they set
      // their own during onboarding.
      const res = await api.inviteMembers(defaultProjectId, emails, [])
      for (const r of res.results ?? []) {
        if (r.status === 'invited') invited++
        else if (r.status === 'already') already++
        else bad++
      }
      const parts = [`${invited} invited`]
      if (already) parts.push(`${already} already members`)
      if (bad) parts.push(`${bad} skipped`)
      toast.success(parts.join(' · '))
      setInviteOpen(false)
      await fetchMembers()
    } catch (e) {
      toast.error(toastMessage(e))
    } finally {
      setInviteSending(false)
    }
  }

  // Optimistic UI: confirm immediately, send in the background, and only speak
  // up again if the server disagrees. Busy-ness is PER MEMBER (a Set) — acting
  // on Nathan must never swallow a click on Barbara (the old global flag
  // silently dropped the second click, so its toast never appeared).
  function markBusy(id: string, on: boolean) {
    setBusyIds(prev => {
      const next = new Set(prev)
      if (on) next.add(id)
      else next.delete(id)
      return next
    })
  }

  async function resendInvite(m: OrgMember) {
    if (!m.email || busyIds.has(m.memberId)) return
    const first = m.projects?.[0]
    if (!first) return
    markBusy(m.memberId, true)
    toast.success(`Invitation email resent to ${m.email}`)
    try {
      // One explicit resend on a single membership — the memberships already
      // exist; resend:true is what re-triggers the invitation email (exactly one).
      await api.addMember(first.projectId, m.email, first.domains ?? [], '', true)
    } catch (e) {
      toast.error(`Couldn't resend to ${m.email}: ${toastMessage(e)}`)
    } finally {
      markBusy(m.memberId, false)
    }
  }

  async function revokeInvite(m: OrgMember) {
    if (busyIds.has(m.memberId)) return
    markBusy(m.memberId, true)
    try {
      const projectId = m.projects?.[0]?.projectId
      if (!projectId) throw new Error('Invitation has no project in this organization')
      await api.removeMember(projectId, m.memberId, false)
      toast.info(`Invitation for ${m.email || memberName(m)} revoked`)
      await fetchMembers()
    } catch (e) {
      toast.error(toastMessage(e))
    } finally {
      markBusy(m.memberId, false)
    }
  }

  if (loading) {
    return (
      <Page width={1200}>
        <div style={{ display: 'flex', justifyContent: 'center', padding: 60 }}>
          <Spinner />
        </div>
      </Page>
    )
  }

  return (
    <Page width={1200}>
      <CapNudge resource="members" used={roster.length} />

      {/* invite by code (admin) */}
      {isOrgAdmin && !joinCodeLoading && (
        <div style={{ background: '#fff', border: '1px solid #E9EBEE', borderRadius: 16, padding: '20px 22px', marginBottom: 24, boxShadow: '0 1px 2px rgba(20,28,44,.04)' }}>
          <div style={{ display: 'flex', alignItems: 'flex-start', gap: 13 }}>
            <span style={{ width: 36, height: 36, flex: 'none', borderRadius: 11, background: '#F1F0FE', display: 'flex', alignItems: 'center', justifyContent: 'center' }}>
              <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke={ACCENT} strokeWidth="1.9" strokeLinecap="round" strokeLinejoin="round"><path d="M10 13a5 5 0 0 0 7 0l3-3a5 5 0 0 0-7-7l-1 1" /><path d="M14 11a5 5 0 0 0-7 0l-3 3a5 5 0 0 0 7 7l1-1" /></svg>
            </span>
            <div style={{ flex: 1, minWidth: 0 }}>
              <div style={{ fontSize: 15, fontWeight: 700, letterSpacing: '-.01em', color: '#171B24' }}>Invite by code</div>
              <div style={{ fontSize: 13, color: '#8A94A2', marginTop: 2 }}>
                Share one link — they sign in and land in{' '}
                <strong style={{ color: '#5C6470', fontWeight: 600 }}>{joinCode?.projectName || 'default'}</strong> as pending for your approval.
              </div>
            </div>
            {joinCode?.active && joinCode.code && (
              <div style={{ display: 'flex', alignItems: 'center', gap: 6, flex: 'none' }}>
                <button disabled={joinCodeBusy} onClick={() => void generateCode()} style={{ display: 'inline-flex', alignItems: 'center', gap: 6, height: 32, padding: '0 11px', border: '1px solid #E4E7EB', borderRadius: 8, background: '#fff', color: '#5C6470', fontFamily: 'inherit', fontSize: 12.5, fontWeight: 600, cursor: 'pointer', opacity: joinCodeBusy ? 0.5 : 1 }}>
                  <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.1" strokeLinecap="round" strokeLinejoin="round"><path d="M3 12a9 9 0 1 0 3-6.7L3 8" /><path d="M3 3v5h5" /></svg>
                  Regenerate
                </button>
                <button disabled={joinCodeBusy} onClick={() => void revokeCode()} title="Revoke this link" style={{ width: 32, height: 32, border: '1px solid #F0DADA', borderRadius: 8, background: '#fff', color: '#C0392B', display: 'flex', alignItems: 'center', justifyContent: 'center', cursor: 'pointer', opacity: joinCodeBusy ? 0.5 : 1 }}>
                  <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.1" strokeLinecap="round" strokeLinejoin="round"><path d="M3 6h18M8 6V4h8v2M6 6l1 14h10l1-14" /></svg>
                </button>
              </div>
            )}
          </div>

          {joinCode?.active && joinCode.code ? (
            <>
              <div style={{ display: 'grid', gridTemplateColumns: 'minmax(0,1fr) minmax(0,1fr)', gap: 12, marginTop: 18 }}>
                <div style={{ minWidth: 0 }}>
                  <div style={{ display: 'flex', alignItems: 'center', gap: 7, fontFamily: T.mono, fontSize: 10.5, fontWeight: 600, letterSpacing: '.07em', textTransform: 'uppercase', color: '#9AA1AC', marginBottom: 8 }}>
                    <span style={{ width: 18, height: 18, flex: 'none', borderRadius: 6, background: '#F1F0FE', color: ACCENT, display: 'inline-flex', alignItems: 'center', justifyContent: 'center', fontSize: 9.5 }}>1</span>
                    Invite link
                  </div>
                  <div style={{ display: 'flex', alignItems: 'center', gap: 8, height: 44, border: '1px solid #E2E5EA', borderRadius: 11, background: '#FBFBFC', padding: '0 6px 0 13px' }}>
                    <span style={{ flex: 1, minWidth: 0, fontFamily: T.mono, fontSize: 12.5, color: '#3A414D', whiteSpace: 'nowrap', overflow: 'hidden', textOverflow: 'ellipsis' }}>{joinLink(joinCode.code)}</span>
                    <button onClick={() => void copyText(joinLink(joinCode.code!), 'Invite link', 'link')} style={{ flex: 'none', height: 32, padding: '0 14px', border: 'none', borderRadius: 8, background: '#EEEDFD', color: ACCENT, fontFamily: 'inherit', fontSize: 12.5, fontWeight: 600, cursor: 'pointer' }}>{copied === 'link' ? 'Copied' : 'Copy'}</button>
                  </div>
                </div>
                <div style={{ minWidth: 0 }}>
                  <div style={{ display: 'flex', alignItems: 'center', gap: 7, fontFamily: T.mono, fontSize: 10.5, fontWeight: 600, letterSpacing: '.07em', textTransform: 'uppercase', color: '#9AA1AC', marginBottom: 8 }}>
                    <span style={{ width: 18, height: 18, flex: 'none', borderRadius: 6, background: '#F1F0FE', color: ACCENT, display: 'inline-flex', alignItems: 'center', justifyContent: 'center', fontSize: 9.5 }}>2</span>
                    Connect their agent
                  </div>
                  <div style={{ display: 'flex', alignItems: 'center', gap: 8, height: 44, border: '1px solid #23262F', borderRadius: 11, background: '#12151C', padding: '0 6px 0 13px' }}>
                    <span style={{ flex: 1, minWidth: 0, fontFamily: T.mono, fontSize: 12.5, color: '#C7CBD4', whiteSpace: 'nowrap', overflow: 'hidden', textOverflow: 'ellipsis' }}><span style={{ color: '#5B6472' }}>$ </span>{mcpCommand}</span>
                    <button onClick={() => void copyText(mcpCommand, 'Command', 'cmd')} style={{ flex: 'none', height: 32, padding: '0 14px', border: 'none', borderRadius: 8, background: '#23262F', color: '#C7CBD4', fontFamily: 'inherit', fontSize: 12.5, fontWeight: 600, cursor: 'pointer' }}>{copied === 'cmd' ? 'Copied' : 'Copy'}</button>
                  </div>
                </div>
              </div>

              <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', gap: 14, marginTop: 16, paddingTop: 14, borderTop: '1px solid #F1F3F5' }}>
                <div style={{ display: 'flex', alignItems: 'center', gap: 8, minWidth: 0, fontSize: 12.5, color: '#9AA1AC' }}>
                  <span style={{ display: 'inline-flex', alignItems: 'center', gap: 5, flex: 'none', whiteSpace: 'nowrap', fontSize: 11.5, fontWeight: 600, color: '#15803D', background: '#EBF6EF', border: '1px solid #CDE9D8', padding: '3px 9px', borderRadius: 20 }}>
                    <span style={{ width: 6, height: 6, flex: 'none', borderRadius: '50%', background: '#22A55B' }} />
                    Link active
                  </span>
                  <span style={{ minWidth: 0, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>Anyone with the link can request to join.</span>
                </div>
                <button onClick={() => void copyText(joinSnippet(joinCode.code!), 'Full message', 'msg')} style={{ display: 'inline-flex', alignItems: 'center', gap: 7, flex: 'none', height: 34, padding: '0 13px', border: '1px solid #E4E7EB', borderRadius: 8, background: '#fff', color: '#5C6470', fontFamily: 'inherit', fontSize: 12.5, fontWeight: 600, cursor: 'pointer' }}>
                  <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round"><rect x="9" y="9" width="12" height="12" rx="2" /><path d="M5 15V5a2 2 0 0 1 2-2h10" /></svg>
                  {copied === 'msg' ? 'Copied' : 'Copy full message'}
                </button>
              </div>
            </>
          ) : (
            <div style={{ display: 'flex', alignItems: 'center', gap: 14, flexWrap: 'wrap', marginTop: 18 }}>
              <Button variant="primary" size="sm" loading={joinCodeBusy} onClick={() => void generateCode()}>
                Generate invite link
              </Button>
              <span style={{ fontSize: 12.5, color: '#8A94A2' }}>
                Creates a shareable link that invites people to your default project.
              </span>
            </div>
          )}
        </div>
      )}

      {/* stats */}
      <div style={{ display: 'grid', gridTemplateColumns: 'repeat(3,1fr)', gap: 16, marginBottom: 24 }}>
        <Stat label="Members" value={roster.length} />
        <Stat label="Seats used" value={seatsUsed} of={seatCap} />
        <Stat label="On leave" value={onLeaveCount} />
      </div>

      {/* filters */}
      <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', gap: 16, flexWrap: 'wrap', marginBottom: 14 }}>
        <div style={{ display: 'flex', alignItems: 'center', gap: 10, flexWrap: 'wrap' }}>
          <FilterSelect value={projectFilter} onChange={setProjectFilter}>
            <option value="all">All projects</option>
            {projects.map(p => <option key={p.id} value={p.id}>{p.name}</option>)}
          </FilterSelect>
          <FilterSelect value={expertiseFilter} onChange={setExpertiseFilter}>
            <option value="all">All expertise</option>
            {expertiseOptions.map(([t, n]) => <option key={t} value={t}>{t} ({n})</option>)}
          </FilterSelect>
          <FilterSelect value={availFilter} onChange={setAvailFilter}>
            <option value="all">All availability</option>
            <option value="active">Active</option>
            <option value="on_leave">On leave</option>
            <option value="deactivated">Deactivated</option>
          </FilterSelect>
          {anyFilter && (
            <button
              onClick={() => { setProjectFilter('all'); setExpertiseFilter('all'); setAvailFilter('all'); setSearch('') }}
              style={{ display: 'flex', alignItems: 'center', gap: 5, height: 38, padding: '0 12px', border: 'none', background: 'transparent', color: ACCENT, fontSize: 13, fontWeight: 600, cursor: 'pointer' }}
            >
              <Icon name="x" size={13} /> Clear
            </button>
          )}
        </div>
        <div style={{ display: 'flex', alignItems: 'center', gap: 9, height: 38, border: `1px solid ${T.borderStrong}`, borderRadius: 9, padding: '0 13px', background: T.surface, width: 250 }}>
          <Icon name="search" size={15} color={T.faint} />
          <input
            value={search}
            onChange={e => setSearch(e.target.value)}
            placeholder="Search members…"
            style={{ border: 'none', background: 'transparent', fontSize: 13.5, color: T.text, width: '100%', outline: 'none' }}
          />
        </div>
      </div>

      <div style={{ fontSize: 12.5, color: T.faint, marginBottom: 14 }}>
        <span style={{ fontWeight: 600, color: T.body }}>{rows.length}</span> of {roster.length} members
      </div>

      {/* table */}
      {rows.length > 0 ? (
        <div style={{ background: T.surface, border: `1px solid ${T.border}`, borderRadius: 16, overflowX: 'auto' }}>
          <div style={{ display: 'grid', gridTemplateColumns: COLS, gap: 16, padding: '13px 24px', borderBottom: `1px solid ${T.borderSubtle}`, background: T.surfaceAlt, fontSize: 11.5, fontWeight: 600, letterSpacing: '.04em', color: T.faint, textTransform: 'uppercase' }}>
            <span>Person</span><span>Projects</span><span>Expertise</span><span>Reachable on</span>
          </div>
          {rows.map((m, i) => {
            const [avBg, avFg] = avatarColor(m.memberId)
            const st = AVAIL[m.status] ?? AVAIL.active
            const projs = m.projects ?? []
            const doms = memberDomains(m)
            // Rows are sorted admins-first; a group label is emitted at each tier
            // boundary so the Admin and Team tiers read as distinct groups.
            const showAdminHdr = i === 0 && !!m.isOrgAdmin
            const showTeamHdr = !m.isOrgAdmin && (i === 0 || !!rows[i - 1].isOrgAdmin)
            return (
              <Fragment key={m.memberId}>
              {showAdminHdr && <GroupHeader label="Admin" cols={COLS} />}
              {showTeamHdr && <GroupHeader label="Team" cols={COLS} />}
              <div
                role="link"
                tabIndex={0}
                onClick={() => navigate(`/members/${m.memberId}`)}
                onKeyDown={e => { if (e.key === 'Enter') navigate(`/members/${m.memberId}`) }}
                style={{ display: 'grid', gridTemplateColumns: COLS, gap: 16, padding: '15px 24px', borderTop: `1px solid ${T.borderSubtle}`, alignItems: 'center', cursor: 'pointer' }}
              >
                {/* person */}
                <div style={{ display: 'flex', alignItems: 'center', gap: 12, minWidth: 0 }}>
                  <div style={{ width: 38, height: 38, flex: 'none', borderRadius: '50%', background: avBg, display: 'flex', alignItems: 'center', justifyContent: 'center', fontSize: 12.5, fontWeight: 700, color: avFg }}>
                    {initials(memberName(m))}
                  </div>
                  <div style={{ minWidth: 0 }}>
                    <div style={{ display: 'flex', alignItems: 'center', gap: 7, fontSize: 14, fontWeight: 600, color: T.text, whiteSpace: 'nowrap', overflow: 'hidden' }}>
                      <span style={{ overflow: 'hidden', textOverflow: 'ellipsis' }}>{memberName(m)}</span>
                      <TierBadge tier={tierOf(m)} />
                      {/* Availability shows inline only when it carries signal (on
                          leave / deactivated) — an "Active" badge on every row is
                          noise, so nothing renders for active members. */}
                      {m.status !== 'active' && (
                        <span style={{ display: 'inline-flex', alignItems: 'center', gap: 5, flex: 'none', fontSize: 11, fontWeight: 600, color: st.text, background: st.bg, padding: '1px 7px', borderRadius: 20, whiteSpace: 'nowrap' }}>
                          <span style={{ width: 6, height: 6, borderRadius: '50%', background: st.dot }} />
                          {st.label}
                        </span>
                      )}
                      {m.memberId === memberID && <span style={{ fontSize: 11, fontWeight: 600, color: ACCENT, background: T.accentSofter, padding: '1px 7px', borderRadius: 20 }}>You</span>}
                    </div>
                    <div style={{ fontSize: 12.5, color: T.faint, whiteSpace: 'nowrap', overflow: 'hidden', textOverflow: 'ellipsis' }}>
                      {m.external ? 'External expert' : m.email}
                    </div>
                  </div>
                </div>
                {/* projects */}
                <ChipRow items={projs.slice(0, 3).map(p => projName(p.projectId))} overflow={projs.length - 3} />
                {/* expertise */}
                {doms.length ? <ChipRow items={doms.slice(0, 2)} overflow={doms.length - 2} pill /> : <span style={{ fontSize: 13, color: T.faint }}>—</span>}
                {/* reachable — deliveryChannel is redacted for non-admin viewers
                    (empty), which must render as a plain dash, not an empty chip */}
                {m.deliveryChannel ? (
                  <div style={{ display: 'flex', alignItems: 'center', gap: 8, minWidth: 0 }}>
                    <span style={{ position: 'relative', width: 9, height: 9, borderRadius: 3, background: m.deliveryChannel === 'dashboard' ? T.accent : '#8A94A2', flex: 'none' }}>
                      {m.bindingError && <span title="Delivery failed" style={{ position: 'absolute', top: -4, right: -4, width: 9, height: 9, borderRadius: '50%', background: '#E5484D', border: '2px solid #fff' }} />}
                    </span>
                    <span style={{ fontSize: 13, color: m.bindingError ? '#C43C3C' : T.muted, whiteSpace: 'nowrap' }}>{CHANNEL_LABEL[m.deliveryChannel] ?? m.deliveryChannel}</span>
                  </div>
                ) : (
                  <span style={{ fontSize: 13, color: T.faint }}>—</span>
                )}
              </div>
              </Fragment>
            )
          })}
        </div>
      ) : (
        <div style={{ background: T.surface, border: `1px solid ${T.border}`, borderRadius: 16, padding: '56px 24px', textAlign: 'center' }}>
          <div style={{ fontSize: 15, fontWeight: 600, color: T.text }}>No members match your filters</div>
          <div style={{ fontSize: 13, color: T.faint, marginTop: 5 }}>Try a different project, expertise or availability.</div>
        </div>
      )}

      {/* Invited tier (join-code redeemers, status "pending") — admin-only,
          read-only rows. Approve (Promote to Team) or remove them from the
          member detail page. */}
      {isOrgAdmin && pendingApprovals.length > 0 && (
        <>
          <div style={{ fontSize: 11.5, fontWeight: 600, letterSpacing: '.04em', color: T.faint, textTransform: 'uppercase', margin: '28px 0 12px' }}>Invited · awaiting approval</div>
          <div style={{ display: 'flex', flexDirection: 'column', gap: 10 }}>
            {pendingApprovals.map(m => (
              <div
                key={m.memberId}
                role="link"
                tabIndex={0}
                onClick={() => navigate(`/members/${m.memberId}`)}
                onKeyDown={e => { if (e.key === 'Enter') navigate(`/members/${m.memberId}`) }}
                style={{ background: T.surface, border: `1px solid ${T.border}`, borderRadius: 14, padding: '14px 20px', display: 'flex', alignItems: 'center', gap: 16, flexWrap: 'wrap', cursor: 'pointer' }}
              >
                <div style={{ width: 34, height: 34, flex: 'none', borderRadius: '50%', background: T.sunken, display: 'flex', alignItems: 'center', justifyContent: 'center' }}>
                  <Icon name="user" size={16} color={T.faint} />
                </div>
                <div style={{ flex: 1, minWidth: 180 }}>
                  <div style={{ fontSize: 14, fontWeight: 600, color: T.text }}>{memberName(m)}</div>
                  <div style={{ fontSize: 12.5, color: T.faint }}>{m.email || (m.projects ?? []).map(p => projName(p.projectId)).join(', ')}</div>
                </div>
                <span style={{ display: 'inline-flex', alignItems: 'center', gap: 6, fontSize: 12, fontWeight: 600, color: '#B45309', background: '#FDF3E0', padding: '4px 10px', borderRadius: 20 }}>
                  <span style={{ width: 7, height: 7, borderRadius: '50%', background: '#E0A320' }} />Invited
                </span>
                <Icon name="chevronRight" size={16} color={T.faint} />
              </div>
            ))}
          </div>
        </>
      )}

      {/* pending */}
      {pending.length > 0 && (
        <>
          <div style={{ fontSize: 11.5, fontWeight: 600, letterSpacing: '.04em', color: T.faint, textTransform: 'uppercase', margin: '28px 0 12px' }}>Pending invitations</div>
          <div style={{ display: 'flex', flexDirection: 'column', gap: 10 }}>
            {pending.map(m => (
              <div key={m.memberId} style={{ background: T.surface, border: `1px solid ${T.border}`, borderRadius: 14, padding: '14px 20px', display: 'flex', alignItems: 'center', gap: 16, flexWrap: 'wrap' }}>
                <div style={{ width: 34, height: 34, flex: 'none', borderRadius: '50%', background: T.sunken, display: 'flex', alignItems: 'center', justifyContent: 'center' }}>
                  <Icon name="mail" size={16} color={T.faint} />
                </div>
                <div style={{ flex: 1, minWidth: 180 }}>
                  <div style={{ fontSize: 14, fontWeight: 600, color: T.text }}>{m.email || memberName(m)}</div>
                  <div style={{ fontSize: 12.5, color: T.faint }}>{(m.projects ?? []).map(p => projName(p.projectId)).join(', ')}</div>
                </div>
                <span style={{ display: 'inline-flex', alignItems: 'center', gap: 6, fontSize: 12, fontWeight: 600, color: '#6B7280', background: T.sunken, padding: '4px 10px', borderRadius: 20 }}>
                  <span style={{ width: 7, height: 7, borderRadius: '50%', background: '#B7BDC6' }} />Invited
                </span>
                {isOrgAdmin && (
                  <>
                    <button disabled={busyIds.has(m.memberId)} onClick={() => void resendInvite(m)} style={{ border: 'none', background: 'transparent', fontSize: 13, fontWeight: 600, color: ACCENT, cursor: 'pointer', opacity: busyIds.has(m.memberId) ? 0.5 : 1 }}>Resend</button>
                    <button disabled={busyIds.has(m.memberId)} onClick={() => void revokeInvite(m)} style={{ border: 'none', background: 'transparent', fontSize: 13, fontWeight: 500, color: T.faint, cursor: 'pointer', opacity: busyIds.has(m.memberId) ? 0.5 : 1 }}>Revoke</button>
                  </>
                )}
              </div>
            ))}
          </div>
        </>
      )}

      {/* invite modal */}
      <Modal open={inviteOpen} onClose={() => setInviteOpen(false)} title="Invite your team" subtitle="Paste one or many emails — everyone gets an invite to your default project. They pick their expertise when they join.">
        <div style={{ display: 'flex', flexDirection: 'column', gap: 18 }}>
          <label style={{ display: 'flex', flexDirection: 'column', gap: 7 }}>
            <span style={{ fontSize: 13, fontWeight: 500, color: T.body }}>Email address(es)</span>
            <textarea value={inviteEmail} onChange={e => setInviteEmail(e.target.value)} placeholder={'alice@company.com, bob@company.com\ncarol@company.com'} rows={3} style={{ minHeight: 72, border: `1px solid ${T.borderStrong}`, borderRadius: 10, padding: '10px 14px', fontSize: 14, color: T.text, background: T.surface, outline: 'none', resize: 'vertical', fontFamily: 'inherit' }} />
            <span style={{ fontSize: 12, color: T.faint }}>Separate with commas, spaces, or new lines.</span>
          </label>
          <div style={{ display: 'flex', justifyContent: 'flex-end', gap: 10 }}>
            <Button variant="secondary" onClick={() => setInviteOpen(false)}>Cancel</Button>
            <Button variant="primary" loading={inviteSending} disabled={!inviteEmail.trim() || !defaultProjectId} onClick={() => void sendInvite()}>Send invitation</Button>
          </div>
        </div>
      </Modal>
    </Page>
  )
}

function Stat({ label, value, of }: { label: string; value: number; of?: number }) {
  return (
    <div style={{ background: T.surface, border: `1px solid ${T.border}`, borderRadius: 14, padding: '18px 20px' }}>
      <div style={{ fontSize: 12, fontWeight: 600, color: T.faint, textTransform: 'uppercase', letterSpacing: '.04em' }}>{label}</div>
      <div style={{ display: 'flex', alignItems: 'baseline', gap: 5, marginTop: 6 }}>
        <span style={{ fontSize: 26, fontWeight: 700, color: T.text }}>{value}</span>
        {of !== undefined && <span style={{ fontSize: 13, color: T.subtle }}>of {of}</span>}
      </div>
      {of !== undefined && (
        <div style={{ height: 5, borderRadius: 3, background: T.sunken, overflow: 'hidden', marginTop: 10 }}>
          <div style={{ width: `${Math.min(100, (value / of) * 100)}%`, height: '100%', background: ACCENT, borderRadius: 3 }} />
        </div>
      )}
    </div>
  )
}

// GroupHeader is a full-width tier label row inside the roster grid (Admin / Team).
function GroupHeader({ label, cols }: { label: string; cols: string }) {
  return (
    <div style={{ display: 'grid', gridTemplateColumns: cols, padding: '10px 24px 6px', borderTop: `1px solid ${T.borderSubtle}`, background: T.surfaceAlt }}>
      <span style={{ gridColumn: '1 / -1', fontSize: 11, fontWeight: 700, letterSpacing: '.05em', color: T.faint, textTransform: 'uppercase' }}>{label}</span>
    </div>
  )
}

function ChipRow({ items, overflow, pill = false }: { items: string[]; overflow: number; pill?: boolean }) {
  const radius = pill ? 20 : 6
  return (
    <div style={{ display: 'flex', flexWrap: 'wrap', gap: 5, alignContent: 'center' }}>
      {items.map(t => (
        <span key={t} style={{ fontSize: pill ? 11.5 : 11, fontWeight: pill ? 600 : 500, color: pill ? '#4A4F5A' : '#6B7280', background: T.sunken, border: `1px solid ${T.borderSubtle}`, padding: pill ? '3px 9px' : '2px 8px', borderRadius: radius, whiteSpace: 'nowrap' }}>{t}</span>
      ))}
      {overflow > 0 && (
        <span style={{ fontSize: pill ? 11.5 : 11, fontWeight: 600, color: T.faint, background: T.sunken, border: `1px solid ${T.borderSubtle}`, padding: pill ? '3px 9px' : '2px 8px', borderRadius: radius }}>+{overflow}</span>
      )}
    </div>
  )
}

function FilterSelect({ value, onChange, children }: { value: string; onChange: (v: string) => void; children: React.ReactNode }) {
  return (
    <select
      value={value}
      onChange={e => onChange(e.target.value)}
      style={{
        height: 38,
        border: `1px solid ${T.borderStrong}`,
        borderRadius: 9,
        padding: '0 34px 0 13px',
        fontSize: 13.5,
        fontWeight: 500,
        color: T.body,
        backgroundColor: T.surface,
        backgroundImage: CHEVRON_BG,
        backgroundRepeat: 'no-repeat',
        backgroundPosition: 'right 12px center',
        appearance: 'none',
        WebkitAppearance: 'none',
        MozAppearance: 'none',
        cursor: 'pointer',
      }}
    >
      {children}
    </select>
  )
}
