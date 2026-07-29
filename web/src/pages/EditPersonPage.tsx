// SPDX-License-Identifier: AGPL-3.0-or-later
//
// EditPersonPage — org-admin edit view for one member (design: "Edit Person").
// Identity, reachability + integrations, per-project expertise tags (no role
// selector — project roles were retired), org-admin toggle, projects,
// availability (on-leave), and a danger zone (deactivate = frees the seat,
// Option A; remove/purge). Backed by the org-member admin RPCs.

import { useEffect, useRef, useState } from 'react'
import { useNavigate, useParams } from 'react-router-dom'

import { Page } from '../components/Layout'
import { Button } from '../components/Button'
import { Icon } from '../components/Icon'
import { Modal } from '../components/Modal'
import { Spinner } from '../components/Spinner'
import { ChipMultiSelect } from '../components/ChipMultiSelect'
import { api, type OrgMember, type ProjectSummary } from '../lib/client'
import { EXPERTISE_TAGS } from '../lib/expertise'
import { useIdentity } from '../lib/identity'
import { useToast, toastMessage } from '../lib/toast'
import { DiscordIdHint } from '../components/DiscordIdHint'
import { T } from '../lib/theme'

const CHANNELS = ['dashboard', 'slack', 'teams', 'telegram', 'discord'] as const
const CHANNEL_LABEL: Record<string, string> = {
  dashboard: 'Dashboard',
  slack: 'Slack',
  teams: 'Teams',
  telegram: 'Telegram',
  discord: 'Discord',
}
const BINDING_FIELD: Record<string, { key: keyof BindingState; label: string; placeholder: string }> = {
  slack: { key: 'slackUserId', label: 'Slack user ID', placeholder: 'U01ABCD23' },
  teams: { key: 'teamsUserId', label: 'Teams user ID', placeholder: 'AAD object id' },
  telegram: { key: 'telegramChatId', label: 'Telegram chat ID', placeholder: 'e.g. 84920113' },
  discord: { key: 'discordUserId', label: 'Discord user ID', placeholder: 'snowflake id' },
}

type BindingState = {
  slackUserId: string
  teamsUserId: string
  telegramChatId: string
  discordUserId: string
}

type Form = {
  firstName: string
  lastName: string
  displayName: string
  gitHandle: string
  deliveryChannel: string
  bindings: BindingState
  isOrgAdmin: boolean
  status: string
  returnDate: string
  // per-project domains, keyed by projectId; the key set is the membership.
  domainsByProject: Record<string, string[]>
}

const AVAIL_META: Record<string, { label: string; dot: string; text: string; bg: string }> = {
  active: { label: 'Active', dot: '#16A34A', text: '#15803D', bg: '#E9F7EF' },
  on_leave: { label: 'On leave', dot: '#E0A320', text: '#B45309', bg: '#FDF3E0' },
  deactivated: { label: 'Deactivated', dot: '#9AA1AC', text: '#6B7280', bg: '#F1F3F5' },
  invited: { label: 'Invited', dot: '#B7BDC6', text: '#6B7280', bg: '#F1F3F5' },
}

function snapshot(f: Form): string {
  return JSON.stringify(f)
}

export function EditPersonPage() {
  const { id = '' } = useParams()
  const navigate = useNavigate()
  const toast = useToast()
  const { projects: orgProjectsCtx, isOrgAdmin } = useIdentity()

  const [member, setMember] = useState<OrgMember | null>(null)
  const [orgProjects, setOrgProjects] = useState<ProjectSummary[]>(orgProjectsCtx)
  const [connectedChannels, setConnectedChannels] = useState<string[]>([])
  const [form, setForm] = useState<Form | null>(null)
  const initial = useRef('')
  const [loading, setLoading] = useState(true)
  const [saving, setSaving] = useState(false)
  const [modal, setModal] = useState<'deactivate' | 'remove' | null>(null)
  const [purge, setPurge] = useState(false)
  const [managing, setManaging] = useState(false)

  useEffect(() => {
    void (async () => {
      try {
        const [m, projects, channels] = await Promise.all([
          api.getOrgMember(id),
          api.listProjects(),
          api.listConnectedChannels().catch(() => ({ channels: [] as string[] })),
        ])
        setOrgProjects(projects.projects)
        setConnectedChannels(channels.channels ?? [])
        const f = formFromMember(m.member)
        setMember(m.member)
        setForm(f)
        initial.current = snapshot(f)
      } catch (e) {
        toast.error(toastMessage(e))
      } finally {
        setLoading(false)
      }
    })()
  }, [id])

  const dirty = form !== null && snapshot(form) !== initial.current
  const patch = (p: Partial<Form>) => setForm(f => (f ? { ...f, ...p } : f))

  async function save() {
    if (!form || !member) return
    setSaving(true)
    try {
      const before = formFromMember(member)
      const nowIds = Object.keys(form.domainsByProject)
      const wasIds = Object.keys(before.domainsByProject)

      // Independent mutations run concurrently — one round-trip window, not one
      // per field, so a save with several projects isn't a long serial chain.
      // Wire convention: empty = "leave unchanged", "-" = clear. Emptying a
      // clearable field (git handle, channel bindings) must clear it for real.
      const orClear = (v: string) => (v.trim() === '' ? '-' : v.trim())
      const ops: Promise<unknown>[] = [
        api.updateMember({
          memberId: member.memberId,
          firstName: form.firstName,
          lastName: form.lastName,
          displayName: form.displayName,
          gitHandle: orClear(form.gitHandle),
          deliveryChannel: form.deliveryChannel,
          slackUserId: orClear(form.bindings.slackUserId),
          teamsUserId: orClear(form.bindings.teamsUserId),
          telegramChatId: orClear(form.bindings.telegramChatId),
          discordUserId: orClear(form.bindings.discordUserId),
        }),
      ]
      for (const pid of nowIds) {
        if (!wasIds.includes(pid)) {
          ops.push(api.addMember(pid, member.email, form.domainsByProject[pid]))
        } else if (JSON.stringify(form.domainsByProject[pid]) !== JSON.stringify(before.domainsByProject[pid])) {
          ops.push(api.setMemberExpertise(pid, member.memberId, form.domainsByProject[pid]))
        }
      }
      for (const pid of wasIds) {
        if (!nowIds.includes(pid)) ops.push(api.removeMember(pid, member.memberId, false))
      }
      if (form.isOrgAdmin !== before.isOrgAdmin) ops.push(api.setOrgAdmin(member.memberId, form.isOrgAdmin))
      if (form.status !== before.status || form.returnDate !== before.returnDate) {
        ops.push(api.setMemberAvailability(member.memberId, form.status, form.returnDate))
      }

      await Promise.all(ops)

      const fresh = await api.getOrgMember(id)
      setMember(fresh.member)
      const f = formFromMember(fresh.member)
      setForm(f)
      initial.current = snapshot(f)
      toast.success('Changes saved')
    } catch (e) {
      toast.error(toastMessage(e))
    } finally {
      setSaving(false)
    }
  }

  async function confirmModal() {
    if (!member) return
    try {
      if (modal === 'deactivate') {
        await api.setMemberActivation(member.memberId, false)
        toast.success('Member deactivated — their seat is freed')
        setModal(null)
        navigate('/members')
      } else if (modal === 'remove') {
        const pids = Object.keys(formFromMember(member).domainsByProject)
        const projectId = pids[0]
        if (!projectId) throw new Error('Member has no project in this organization')
        await api.removeMember(projectId, member.memberId, purge)
        toast.success('Member removed')
        setModal(null)
        navigate('/members')
      }
    } catch (e) {
      toast.error(toastMessage(e))
    }
  }

  async function reactivate() {
    if (!member) return
    try {
      await api.setMemberActivation(member.memberId, true)
      const fresh = await api.getOrgMember(id)
      setMember(fresh.member)
      const f = formFromMember(fresh.member)
      setForm(f)
      initial.current = snapshot(f)
      toast.success('Member reactivated')
    } catch (e) {
      toast.error(toastMessage(e))
    }
  }

  // Re-pull the member after an immediate management action (admin toggle,
  // promote) so the header, tier badge and form reflect the new state without a
  // manual Save. Keeps `initial` in sync so nothing shows as "unsaved".
  async function refetchMember() {
    const fresh = await api.getOrgMember(id)
    setMember(fresh.member)
    const f = formFromMember(fresh.member)
    setForm(f)
    initial.current = snapshot(f)
  }

  // Immediate promote/demote. The server refuses to strip the last remaining
  // admin — that guard surfaces here as an error toast.
  async function toggleAdmin() {
    if (!member || managing) return
    const makeAdmin = !member.isOrgAdmin
    setManaging(true)
    try {
      await api.setOrgAdmin(member.memberId, makeAdmin)
      await refetchMember()
      toast.success(makeAdmin ? `${fullName} is now an admin` : `${fullName} is now Team`)
    } catch (e) {
      toast.error(toastMessage(e))
    } finally {
      setManaging(false)
    }
  }

  // Promote a join-code redeemer (status "pending") to Team by activating them.
  async function promoteToTeam() {
    if (!member || managing) return
    setManaging(true)
    try {
      await api.setMemberActivation(member.memberId, true)
      await refetchMember()
      toast.success(`${fullName} promoted to Team`)
    } catch (e) {
      toast.error(toastMessage(e))
    } finally {
      setManaging(false)
    }
  }

  if (loading) {
    return (
      <Page width={1000}>
        <div style={{ display: 'flex', justifyContent: 'center', padding: 60 }}>
          <Spinner />
        </div>
      </Page>
    )
  }

  if (!form || !member) {
    return (
      <Page width={1000}>
        <p style={{ color: T.muted }}>Member not found.</p>
        <Button variant="secondary" onClick={() => navigate('/members')} style={{ marginTop: 14 }}>
          Back to Team
        </Button>
      </Page>
    )
  }

  const st = AVAIL_META[form.status] ?? AVAIL_META.active
  const availBadge =
    form.status === 'on_leave' && form.returnDate ? `On leave · back ${form.returnDate}` : st.label
  const fullName = `${form.firstName} ${form.lastName}`.trim() || form.displayName || member.email
  const bindingChannels = connectedChannels.filter(c => BINDING_FIELD[c])
  // Tier is derived, never stored: pending = Invited, org-admin flag = Admin,
  // otherwise Team.
  const memberTier: TierName = member.status === 'pending' ? 'Invited' : member.isOrgAdmin ? 'Admin' : 'Team'

  return (
    <Page width={1000}>
      {/* header */}
      <button
        onClick={() => navigate('/members')}
        style={{ display: 'inline-flex', alignItems: 'center', gap: 6, fontSize: 12.5, fontWeight: 600, color: T.faint, background: 'none', border: 'none', cursor: 'pointer', marginBottom: 14 }}
      >
        <Icon name="chevronRight" size={14} style={{ transform: "rotate(180deg)" }} /> Team
      </button>
      <div style={{ display: 'flex', alignItems: 'center', gap: 16, flexWrap: 'wrap', marginBottom: 24 }}>
        <div style={{ minWidth: 0, marginRight: 'auto' }}>
          <div style={{ display: 'flex', alignItems: 'center', gap: 10, flexWrap: 'wrap' }}>
            <h1 style={{ fontSize: 22, fontWeight: 700, letterSpacing: '-.02em', color: T.text }}>{fullName}</h1>
            <span style={{ display: 'inline-flex', alignItems: 'center', gap: 6, fontSize: 12, fontWeight: 600, color: st.text, background: st.bg, padding: '4px 10px', borderRadius: 20 }}>
              <span style={{ width: 7, height: 7, borderRadius: '50%', background: st.dot }} />
              {availBadge}
            </span>
            {form.isOrgAdmin && (
              <span style={{ fontSize: 11.5, fontWeight: 600, color: T.accent, background: T.accentSofter, border: `1px solid ${T.accentBorder}`, padding: '3px 9px', borderRadius: 20 }}>
                Org admin
              </span>
            )}
          </div>
          <div style={{ fontSize: 13, color: T.faint, marginTop: 3 }}>
            {member.email || 'External expert'}
            {form.gitHandle ? ` · git @${form.gitHandle}` : ''}
          </div>
        </div>
        {dirty && <span style={{ fontSize: 12.5, color: '#B45309', fontWeight: 500 }}>Unsaved changes</span>}
        <Button variant="secondary" disabled={!dirty || saving} onClick={() => { const f = formFromMember(member); setForm(f) }}>
          Discard
        </Button>
        <Button variant="primary" loading={saving} disabled={!dirty} onClick={() => void save()}>
          Save changes
        </Button>
      </div>

      <div style={{ display: 'flex', flexDirection: 'column', gap: 20 }}>
        {/* 1. Identity */}
        <Section title="Identity" sub="Name and how the agent matches this person to their work.">
          <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 16 }}>
            <Field label="First name" value={form.firstName} onChange={v => patch({ firstName: v })} />
            <Field label="Last name" value={form.lastName} onChange={v => patch({ lastName: v })} />
            <Field label="Display name" value={form.displayName} onChange={v => patch({ displayName: v })} />
            <Field label="Git handle" mono value={form.gitHandle} onChange={v => patch({ gitHandle: v })} />
          </div>
          <Note>
            The <strong>git handle</strong> is how the agent links this person to their commits, PRs and @-mentions. Keep it exact — a wrong handle breaks routing.
          </Note>
          {member.email && (
            <div style={{ marginTop: 14, maxWidth: 340 }}>
              <Label>Email</Label>
              <div style={{ display: 'flex', alignItems: 'center', height: 42, border: `1px solid ${T.borderSubtle}`, borderRadius: 10, padding: '0 13px', background: T.sunken, fontSize: 14, color: T.muted }}>
                {member.email}
                <span style={{ marginLeft: 'auto', fontSize: 11.5, fontWeight: 600, color: T.faint }}>Login-linked</span>
              </div>
            </div>
          )}
        </Section>

        {/* 2. Reachability & integrations */}
        <Section title="Reachability & integrations" sub="Where Orako sends this person their questions.">
          <Label>Delivery channel</Label>
          <div style={{ display: 'grid', gridTemplateColumns: 'repeat(3,1fr)', gap: 9, marginBottom: bindingChannels.length ? 22 : 0 }}>
            {[...CHANNELS].filter(c => c === 'dashboard' || connectedChannels.includes(c)).map(c => {
              const on = form.deliveryChannel === c
              return (
                <button
                  key={c}
                  onClick={() => patch({ deliveryChannel: c })}
                  style={{ display: 'flex', alignItems: 'center', gap: 10, height: 46, padding: '0 14px', border: `1px solid ${on ? T.accent : T.borderStrong}`, background: on ? T.accentSofter : T.surface, borderRadius: 11, cursor: 'pointer', fontSize: 13.5, fontWeight: 600, color: on ? T.accentInk : T.body }}
                >
                  {CHANNEL_LABEL[c]}
                  {on && <Icon name="check" size={16} color={T.accent} style={{ marginLeft: 'auto' }} />}
                </button>
              )
            })}
          </div>
          {bindingChannels.length > 0 && (
            <>
              <Label>Identity bindings</Label>
              <div style={{ fontSize: 12.5, color: T.faint, marginBottom: 12 }}>Only the integrations your org has connected are shown.</div>
              <div style={{ display: 'flex', flexDirection: 'column', gap: 12 }}>
                {bindingChannels.map(c => {
                  const bf = BINDING_FIELD[c]
                  return (
                    <div key={c}>
                      <Field
                        label={bf.label}
                        mono
                        placeholder={bf.placeholder}
                        value={form.bindings[bf.key]}
                        onChange={v => patch({ bindings: { ...form.bindings, [bf.key]: v } })}
                      />
                      {bf.key === 'discordUserId' && <DiscordIdHint />}
                    </div>
                  )
                })}
              </div>
              {member.bindingError && (
                <div style={{ fontSize: 12, color: '#C43C3C', marginTop: 8 }}>
                  Last message failed to deliver — re-save to retry.
                </div>
              )}
            </>
          )}
        </Section>

        {/* 3. Expertise */}
        <Section title="Expertise" sub="Tags route questions to this person — set per project.">
          <div style={{ display: 'flex', flexDirection: 'column', gap: 14 }}>
            {Object.keys(form.domainsByProject).length === 0 && (
              <div style={{ fontSize: 13, color: T.faint }}>Not a member of any project yet — add one below.</div>
            )}
            {Object.entries(form.domainsByProject).map(([pid, domains]) => (
              <div key={pid} style={{ border: `1px solid ${T.borderSubtle}`, borderRadius: 13, padding: 16, background: T.surfaceAlt }}>
                <div style={{ display: 'inline-flex', alignItems: 'center', gap: 7, fontSize: 13.5, fontWeight: 700, color: T.body, marginBottom: 12 }}>
                  <Icon name="folder" size={15} color={T.faint} />
                  {projectName(orgProjects, pid)}
                </div>
                <ChipMultiSelect
                  options={EXPERTISE_TAGS as unknown as string[]}
                  value={domains}
                  onChange={next => patch({ domainsByProject: { ...form.domainsByProject, [pid]: next } })}                />
              </div>
            ))}
          </div>
        </Section>

        {/* 4. Projects */}
        <Section title="Projects" sub="Which projects this member belongs to.">
          <div style={{ display: 'flex', flexDirection: 'column', gap: 9 }}>
            {orgProjects.map(p => {
              const on = p.id in form.domainsByProject
              return (
                <button
                  key={p.id}
                  onClick={() => {
                    const next = { ...form.domainsByProject }
                    if (on) delete next[p.id]
                    else next[p.id] = []
                    patch({ domainsByProject: next })
                  }}
                  style={{ display: 'flex', alignItems: 'center', gap: 12, padding: '13px 15px', border: `1px solid ${on ? T.accentBorder : T.borderStrong}`, background: on ? T.accentSofter : T.surface, borderRadius: 11, cursor: 'pointer', textAlign: 'left' }}
                >
                  <span style={{ width: 19, height: 19, flex: 'none', borderRadius: 6, border: `1.5px solid ${on ? T.accent : T.borderStrong}`, background: on ? T.accent : T.surface, display: 'flex', alignItems: 'center', justifyContent: 'center' }}>
                    {on && <Icon name="check" size={12} color="#fff" strokeWidth={3.4} />}
                  </span>
                  <span style={{ display: 'inline-flex', alignItems: 'center', gap: 7, fontSize: 14, fontWeight: 600, color: T.body }}>
                    <Icon name="folder" size={15} color={T.faint} />
                    {p.name}
                  </span>
                  <span style={{ marginLeft: 'auto', fontSize: 12, color: T.faint }}>
                    {on ? `${form.domainsByProject[p.id].length} tag${form.domainsByProject[p.id].length === 1 ? '' : 's'}` : 'Not a member'}
                  </span>
                </button>
              )
            })}
          </div>
        </Section>

        {/* 5. Availability */}
        <Section title="Availability" sub="Pause routing while this person is away — they stay on the team.">
          {form.status === 'deactivated' ? (
            <div style={{ display: 'flex', alignItems: 'center', gap: 14, flexWrap: 'wrap' }}>
              <div style={{ fontSize: 13, color: T.muted, flex: 1, minWidth: 220 }}>
                This member is deactivated — off billing and not routable. Reactivate to restore them (re-adds a seat).
              </div>
              <Button variant="secondary" onClick={() => void reactivate()}>Reactivate</Button>
            </div>
          ) : (
            <>
              <div style={{ display: 'flex', gap: 5, background: T.sunken, borderRadius: 11, padding: 4, maxWidth: 320, marginBottom: 16 }}>
                {[
                  { id: 'active', label: 'Active', dot: '#16A34A' },
                  { id: 'on_leave', label: 'On leave', dot: '#E0A320' },
                ].map(a => {
                  const on = form.status === a.id
                  return (
                    <button
                      key={a.id}
                      onClick={() => patch({ status: a.id, returnDate: a.id === 'on_leave' ? form.returnDate : '' })}
                      style={{ flex: 1, height: 38, border: 'none', borderRadius: 8, background: on ? T.surface : 'transparent', color: on ? T.text : T.subtle, fontSize: 13.5, fontWeight: 600, cursor: 'pointer', display: 'inline-flex', alignItems: 'center', justifyContent: 'center', gap: 7, boxShadow: on ? '0 1px 3px rgba(20,28,44,.14)' : 'none' }}
                    >
                      <span style={{ width: 8, height: 8, borderRadius: '50%', background: a.dot }} />
                      {a.label}
                    </button>
                  )
                })}
              </div>
              {form.status === 'on_leave' && (
                <div style={{ display: 'flex', flexDirection: 'column', gap: 14 }}>
                  <div style={{ maxWidth: 240 }}>
                    <Label>Expected return date</Label>
                    <input
                      type="date"
                      value={form.returnDate}
                      onChange={e => patch({ returnDate: e.target.value })}
                      style={{ height: 42, width: '100%', border: `1px solid ${T.borderStrong}`, borderRadius: 10, padding: '0 13px', fontSize: 14, color: T.body, background: T.surface }}
                    />
                  </div>
                  <div style={{ fontSize: 12.5, color: '#8A6516', background: '#FDF3E0', border: '1px solid #F3E2BC', borderRadius: 10, padding: '12px 14px', lineHeight: 1.5 }}>
                    While on leave, the agent won't route any questions to {form.firstName || 'them'} and suggests someone else instead. This is temporary — <strong>their seat and billing are unchanged.</strong> The return date is a reminder; switch them back to Active when they're ready.
                  </div>
                </div>
              )}
            </>
          )}
        </Section>

        {/* 6. Manage (admin only) — tier + membership actions. */}
        {isOrgAdmin && (
          <Section title="Manage" sub="This member's access tier and how they're managed.">
            <div style={{ display: 'flex', alignItems: 'center', gap: 16, padding: '15px 16px', border: `1px solid ${T.borderSubtle}`, borderRadius: 12, background: T.surface, flexWrap: 'wrap' }}>
              <div style={{ flex: 1, minWidth: 220 }}>
                <div style={{ display: 'flex', alignItems: 'center', gap: 9 }}>
                  <span style={{ fontSize: 13.5, fontWeight: 600, color: T.text }}>Access tier</span>
                  <TierBadge tier={memberTier} />
                </div>
                <div style={{ fontSize: 12.5, color: T.muted, marginTop: 2 }}>
                  {memberTier === 'Invited'
                    ? 'Joined via a code and is awaiting approval. Promote them to add them to the Team.'
                    : memberTier === 'Admin'
                      ? 'Can manage members, projects, integrations and billing.'
                      : 'A regular teammate — routable, without org-admin access.'}
                </div>
              </div>
              {memberTier === 'Invited' ? (
                <Button variant="primary" loading={managing} onClick={() => void promoteToTeam()}>Promote to Team</Button>
              ) : (
                <Button variant="secondary" disabled={managing} onClick={() => void toggleAdmin()}>
                  {member.isOrgAdmin ? 'Remove admin' : 'Make admin'}
                </Button>
              )}
            </div>
          </Section>
        )}

        {/* 7. Danger zone (admin only) */}
        {isOrgAdmin && (
          <Section title="Danger zone" sub="Billing and access changes." danger>
            <Row
              title="Deactivate member"
              body="Frees their seat (you can reassign it without buying more) and stops all routing. Reversible — reactivate anytime. To pay for fewer seats, lower the quantity on the Billing page."
              action={<Button variant="secondary" onClick={() => setModal('deactivate')} style={{ color: '#C43C3C', borderColor: '#E2A6A6' }}>Deactivate</Button>}
            />
            <Row
              danger
              title="Remove from organization"
              body="Permanently revokes access to all projects. Cannot be undone."
              action={<Button variant="primary" onClick={() => { setPurge(false); setModal('remove') }} style={{ background: '#DC2626' }}>Remove</Button>}
            />
          </Section>
        )}
      </div>

      {modal && (
        <Modal open onClose={() => setModal(null)} title={modal === 'deactivate' ? 'Deactivate member' : 'Remove member'}>
          {modal === 'deactivate' ? (
            <p style={{ fontSize: 13.5, color: T.body, lineHeight: 1.55, margin: 0 }}>
              Deactivate <strong>{fullName}</strong>? They stop receiving questions and their seat is freed for reuse. No
              billing change — to pay for fewer seats, lower the quantity on the Billing page. Reversible anytime.
            </p>
          ) : (
            <div style={{ display: 'flex', flexDirection: 'column', gap: 14 }}>
              <p style={{ fontSize: 13.5, color: T.body, lineHeight: 1.55, margin: 0 }}>
                Remove <strong>{fullName}</strong>? They lose access to every project and this <strong>can't be undone</strong>. Prefer <em>Deactivate</em> if this might be temporary.
              </p>
              <label style={{ display: 'flex', alignItems: 'center', gap: 10, fontSize: 13, color: T.body, cursor: 'pointer' }}>
                <input type="checkbox" checked={purge} onChange={e => setPurge(e.target.checked)} />
                Also purge their personal data (PII) from conversation history
              </label>
            </div>
          )}
          <div style={{ display: 'flex', justifyContent: 'flex-end', gap: 10, marginTop: 20 }}>
            <Button variant="secondary" onClick={() => setModal(null)}>Cancel</Button>
            <Button variant="primary" onClick={() => void confirmModal()} style={{ background: modal === 'deactivate' ? '#D97706' : '#DC2626' }}>
              {modal === 'deactivate' ? 'Deactivate' : 'Remove member'}
            </Button>
          </div>
        </Modal>
      )}
    </Page>
  )
}

// --- helpers & small components ---

function formFromMember(m: OrgMember): Form {
  const domainsByProject: Record<string, string[]> = {}
  for (const p of m.projects ?? []) domainsByProject[p.projectId] = [...(p.domains ?? [])]
  return {
    firstName: m.firstName ?? '',
    lastName: m.lastName ?? '',
    displayName: m.displayName ?? '',
    gitHandle: m.gitHandle ?? '',
    deliveryChannel: m.deliveryChannel || 'dashboard',
    bindings: {
      slackUserId: m.slackUserId ?? '',
      teamsUserId: m.teamsUserId ?? '',
      telegramChatId: m.telegramChatId ?? '',
      discordUserId: m.discordUserId ?? '',
    },
    isOrgAdmin: !!m.isOrgAdmin,
    status: m.status === 'invited' ? 'active' : m.status,
    returnDate: m.returnDate ?? '',
    domainsByProject,
  }
}

function projectName(projects: ProjectSummary[], id: string): string {
  return projects.find(p => p.id === id)?.name ?? id.slice(0, 8)
}

function Section({ title, sub, danger = false, children }: { title: string; sub?: string; danger?: boolean; children: React.ReactNode }) {
  return (
    <div style={{ background: T.surface, border: `1px solid ${danger ? '#F1D9D9' : T.border}`, borderRadius: 16, padding: '22px 24px' }}>
      <div style={{ marginBottom: 18 }}>
        <h2 style={{ fontSize: 15.5, fontWeight: 700, color: T.text }}>{title}</h2>
        {sub && <p style={{ fontSize: 12.5, color: T.faint, marginTop: 2 }}>{sub}</p>}
      </div>
      {children}
    </div>
  )
}

function Label({ children }: { children: React.ReactNode }) {
  return <div style={{ fontSize: 13, fontWeight: 500, color: T.body, marginBottom: 7 }}>{children}</div>
}

function Field({ label, value, onChange, mono = false, placeholder }: { label: string; value: string; onChange: (v: string) => void; mono?: boolean; placeholder?: string }) {
  return (
    <label style={{ display: 'flex', flexDirection: 'column', gap: 7 }}>
      <span style={{ fontSize: 13, fontWeight: 500, color: T.body }}>{label}</span>
      <input
        value={value}
        placeholder={placeholder}
        onChange={e => onChange(e.target.value)}
        style={{ height: 42, border: `1px solid ${T.borderStrong}`, borderRadius: 10, padding: '0 13px', fontSize: 14, color: T.text, background: T.surface, fontFamily: mono ? T.mono : T.sans }}
      />
    </label>
  )
}

function Note({ children }: { children: React.ReactNode }) {
  return (
    <div style={{ display: 'flex', alignItems: 'flex-start', gap: 9, background: T.sunken, border: `1px solid ${T.borderSubtle}`, borderRadius: 10, padding: '11px 13px', marginTop: 16, fontSize: 12.5, color: T.muted, lineHeight: 1.5 }}>
      <Icon name="info" size={16} color={T.faint} style={{ flex: 'none', marginTop: 1 }} />
      <span>{children}</span>
    </div>
  )
}

type TierName = 'Admin' | 'Team' | 'Invited'
const TIER_BADGE: Record<TierName, { text: string; bg: string }> = {
  Admin: { text: '#5B3DBD', bg: '#EFEBFB' },
  Team: { text: '#2C5C99', bg: '#E7EFFA' },
  Invited: { text: '#B45309', bg: '#FDF3E0' },
}
function TierBadge({ tier }: { tier: TierName }) {
  const t = TIER_BADGE[tier]
  return (
    <span style={{ fontSize: 11.5, fontWeight: 600, color: t.text, background: t.bg, padding: '2px 9px', borderRadius: 20, whiteSpace: 'nowrap' }}>
      {tier}
    </span>
  )
}

function Row({ title, body, action, danger = false }: { title: string; body: string; action: React.ReactNode; danger?: boolean }) {
  return (
    <div style={{ display: 'flex', alignItems: 'center', gap: 16, padding: '15px 16px', border: `1px solid ${danger ? '#F1D9D9' : T.borderSubtle}`, borderRadius: 12, background: danger ? '#FDF6F6' : T.surface, flexWrap: 'wrap', marginTop: 10 }}>
      <div style={{ flex: 1, minWidth: 220 }}>
        <div style={{ fontSize: 13.5, fontWeight: 600, color: danger ? '#8A2E2E' : T.text }}>{title}</div>
        <div style={{ fontSize: 12.5, color: danger ? '#B08282' : T.muted, lineHeight: 1.5 }}>{body}</div>
      </div>
      {action}
    </div>
  )
}
