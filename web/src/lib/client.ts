// Connect-JSON client: POST /orako.v1.OrakoService/{Method} with a Bearer token.
// Served same-origin in prod; vite's server.proxy covers local dev.

import { bearer } from './token'

export const BASE_URL = ''

export class ConnectError extends Error {
  code: string
  constructor(code: string, message: string) {
    super(message)
    this.name = 'ConnectError'
    this.code = code
  }
}

async function call<Req, Res>(method: string, req: Req, orgId?: string): Promise<Res> {
  const token = await bearer()
  const headers: HeadersInit = {
    'Content-Type': 'application/json',
    'Connect-Protocol-Version': '1',
  }
  if (token) {
    headers['Authorization'] = `Bearer ${token}`
  }
  // Multi-org: the active org the user switched to. The server honors it only
  // for orgs the caller participates in; identity still comes from the token.
  const activeOrg = orgId ?? (typeof localStorage !== 'undefined' ? localStorage.getItem('orako:activeOrg') : null)
  if (activeOrg) {
    headers['Orako-Org-Id'] = activeOrg
  }

  const resp = await fetch(`${BASE_URL}/orako.v1.OrakoService/${method}`, {
    method: 'POST',
    headers,
    body: JSON.stringify(req),
  })

  const body = await resp.json().catch(() => ({}))

  if (!resp.ok) {
    const code = (body as { code?: string }).code ?? String(resp.status)
    const msg = (body as { message?: string }).message ?? resp.statusText
    throw new ConnectError(code, msg)
  }

  return body as Res
}

// connectDiscord starts the member-level Discord OAuth `identify` self-bind in a
// NEW TAB, so the page/form the member is on is never replaced (they return to it
// intact). To dodge popup blockers the tab is opened SYNCHRONOUSLY on the click
// (before any await); the (authenticated) install endpoint is then asked for the
// authorize URL and the new tab is pointed at it. On any install failure the tab
// is closed and the error re-thrown for the caller to surface. A snowflake
// already linked to another member surfaces as a friendly 409 in the opened tab
// (the callback renders it); the opener reflects success by refetching the member
// on window focus. Throws when Discord OAuth is not configured for the project.
export async function connectDiscord(): Promise<void> {
  // Opened first, inside the user gesture, so the browser treats it as
  // user-initiated (not a blocked popup).
  const tab = window.open('about:blank', '_blank')
  try {
    const token = await bearer()
    const resp = await fetch(`${BASE_URL}/discord/oauth/install`, {
      headers: token ? { Authorization: `Bearer ${token}` } : {},
    })
    if (!resp.ok) {
      throw new ConnectError(String(resp.status), 'Discord connect is not available for this project')
    }
    const { authorize_url } = (await resp.json()) as { authorize_url: string }
    if (tab) tab.location.href = authorize_url
    // Popup blocked (tab === null): fall back to a same-tab redirect rather than
    // silently doing nothing.
    else window.location.href = authorize_url
  } catch (e) {
    if (tab) tab.close()
    throw e
  }
}

export async function connectSlack(projectId: string): Promise<void> {
  const token = await bearer()
  const resp = await fetch(
    `${BASE_URL}/slack/oauth/install?project_id=${encodeURIComponent(projectId)}`,
    { headers: token ? { Authorization: `Bearer ${token}` } : {} },
  )
  if (!resp.ok) {
    throw new ConnectError(String(resp.status), 'Slack connect is not available for this project')
  }

  const { authorize_url } = (await resp.json()) as { authorize_url: string }
  window.location.href = authorize_url
}

// OnboardingStatus is the GET /api/onboarding body: whether the caller has
// permanently dismissed the Get-started onboarding (server-side, per member).
export interface OnboardingStatus {
  dismissed: boolean
}

// fetchOnboardingDismissed reads the caller's permanent Get-started dismissal.
// Resolves to false on any failure so the dashboard shell never breaks on it.
export async function fetchOnboardingDismissed(): Promise<boolean> {
  try {
    const token = await bearer()
    const resp = await fetch(`${BASE_URL}/api/onboarding`, {
      headers: token ? { Authorization: `Bearer ${token}` } : {},
    })
    if (!resp.ok) return false
    const body = (await resp.json().catch(() => ({}))) as Partial<OnboardingStatus>
    return body.dismissed === true
  } catch {
    return false
  }
}

// setOnboardingDismissed marks (dismissed=true) or undoes (false) the caller's
// permanent Get-started dismissal. Throws ConnectError on any non-2xx.
export async function setOnboardingDismissed(dismissed: boolean): Promise<void> {
  const token = await bearer()
  const headers: HeadersInit = {}
  if (token) headers['Authorization'] = `Bearer ${token}`

  const resp = await fetch(`${BASE_URL}/api/onboarding/dismiss`, {
    method: dismissed ? 'POST' : 'DELETE',
    headers,
  })

  if (!resp.ok) {
    const body = (await resp.json().catch(() => ({}))) as { error?: string }
    throw new ConnectError(String(resp.status), body.error ?? resp.statusText)
  }
}

// JoinResult is the POST /join success body. orgName is only ever populated on a
// successful redeem (the server never reveals it for an invalid/revoked code).
export interface JoinResult {
  orgName: string
  alreadyMember: boolean
}

// redeemJoinCode redeems an org join code for the authenticated account — which
// may have no member yet (a brand-new joiner). It hits the plain POST /join
// endpoint rather than a Connect-RPC because the dashboard RPC interceptor gates
// every method on an existing membership. Resolves on 2xx; throws ConnectError
// (code = HTTP status) with the server's detail-free message otherwise.
export async function redeemJoinCode(code: string): Promise<JoinResult> {
  const token = await bearer()
  const headers: HeadersInit = { 'Content-Type': 'application/json' }
  if (token) headers['Authorization'] = `Bearer ${token}`

  const resp = await fetch(`${BASE_URL}/join`, {
    method: 'POST',
    headers,
    body: JSON.stringify({ code: code.trim() }),
  })

  const body = await resp.json().catch(() => ({}))

  if (!resp.ok) {
    const msg = (body as { error?: string }).error ?? resp.statusText
    throw new ConnectError(String(resp.status), msg)
  }

  return body as JoinResult
}

// fetchJoinOrg looks up the org name behind a join code WITHOUT redeeming or
// authenticating — it backs the invite-aware auth page's "You've been invited to
// {org}" copy. The server returns the org name ONLY for a valid, non-revoked
// code (a bad/revoked code is a detail-free 404). Resolves to null on any
// non-2xx so the auth page falls back to generic copy instead of hard-failing.
export async function fetchJoinOrg(code: string): Promise<string | null> {
  try {
    const resp = await fetch(`${BASE_URL}/join/${encodeURIComponent(code.trim())}/org`)
    if (!resp.ok) return null
    const body = (await resp.json().catch(() => ({}))) as { orgName?: string }
    return body.orgName?.trim() || null
  } catch {
    return null
  }
}

// CommunityInvites are the org's community-server invite URLs, set by an admin on
// the Discord/Slack integration. Either may be absent when unset.
export interface CommunityInvites {
  discord?: string
  slack?: string
}

// communityInvites returns the caller's org community-server invite URLs, for
// the onboarding "Join your team's server" step. Authenticated; succeeds for a
// pending member (they have an identity after redeem). Resolves to {} on any
// failure so onboarding never stalls on an optional step.
export async function communityInvites(): Promise<CommunityInvites> {
  try {
    const token = await bearer()
    const resp = await fetch(`${BASE_URL}/org/community-invites`, {
      headers: token ? { Authorization: `Bearer ${token}` } : {},
    })
    if (!resp.ok) return {}
    return (await resp.json().catch(() => ({}))) as CommunityInvites
  } catch {
    return {}
  }
}

// setLicense activates (or replaces) the self-host license key. It hits the plain
// POST /api/license endpoint (not a Connect-RPC) — the key is verified offline
// server-side and applied instantly, no restart. Org-admin only. Throws
// ConnectError (code = HTTP status) with the server's message on any non-2xx —
// notably 400 for an invalid/expired key and 409 when billing manages the edition.
export async function setLicense(key: string): Promise<void> {
  const token = await bearer()
  const headers: HeadersInit = { 'Content-Type': 'application/json' }
  if (token) headers['Authorization'] = `Bearer ${token}`

  const resp = await fetch(`${BASE_URL}/api/license`, {
    method: 'POST',
    headers,
    body: JSON.stringify({ key: key.trim() }),
  })

  if (!resp.ok) {
    const body = (await resp.json().catch(() => ({}))) as { error?: string }
    throw new ConnectError(String(resp.status), body.error ?? resp.statusText)
  }
}

// clearLicense removes the stored license (revert to Community at runtime).
// Org-admin only; DELETE /api/license. Throws ConnectError on any non-2xx.
export async function clearLicense(): Promise<void> {
  const token = await bearer()
  const headers: HeadersInit = {}
  if (token) headers['Authorization'] = `Bearer ${token}`

  const resp = await fetch(`${BASE_URL}/api/license`, { method: 'DELETE', headers })

  if (!resp.ok) {
    const body = (await resp.json().catch(() => ({}))) as { error?: string }
    throw new ConnectError(String(resp.status), body.error ?? resp.statusText)
  }
}

// ---- Types (mirroring proto messages as plain TS, camelCase) ----

export interface ProjectSummary {
  id: string
  name: string
  role: number   // proto Role enum value
  orgId?: string // owning organization, for cross-org grouping
}

export interface OrganizationSummary {
  id: string
  name: string
  isCurrent: boolean
}

// Participant is one person on a conversation, name resolved server-side.
export interface Participant {
  memberId: string
  displayName: string
  // role: 'asker' | 'responder' | 'added'
  role: string
}

export interface ConversationSummary {
  id: string
  status: string
  question: string
  title?: string
  responderMemberId: string
  createdAt?: string   // ISO timestamp
  // projectId/projectName: which project this conversation belongs to, echoed
  // back so a cross-project (multi-select "All") list can show a per-row chip.
  projectId?: string
  projectName?: string
  // participants = asker, responder (when assigned), then added members.
  participants?: Participant[]
}

// HistoryHit mirrors orako.v1.HistoryHit: one conversation matched by the
// History-tab search. summary doubles as the resolved answer snippet (there is
// no separate answer field on the read side).
export interface HistoryHit {
  conversationId: string
  projectId: string
  projectName?: string
  title: string
  summary?: string
  status: string          // open | answered | resolved | timed_out | dismissed
  // Connect-JSON omits empty repeated fields, so these are undefined (not [])
  // for a hit with no tags/entities. Guard reads.
  tags?: string[]
  entities?: string[]
  askerMemberId?: string
  createdAt?: string      // ISO timestamp
  ageDays?: number
  score?: number
}

// HistoryStatusCounts mirrors orako.v1.HistoryStatusCounts: the per-status
// conversation breakdown for the KPI/facet pills. timedOut is the "expired"
// bucket; "leads without an answer" = timedOut + dismissed. Connect-JSON omits
// a zero int, so every field is optional — treat undefined as 0.
export interface HistoryStatusCounts {
  all?: number
  resolved?: number
  answered?: number
  open?: number
  timedOut?: number
  dismissed?: number
}

export interface SearchHistoryResponse {
  hits?: HistoryHit[]
  total?: number
  counts?: HistoryStatusCounts
}

// ---- Curated knowledge (dashboard Knowledge section) ----

// KnowledgeEntry mirrors orako.v1.KnowledgeEntry: one curated knowledge entry,
// searchable alongside conversation history but attributed to the org. Connect-
// JSON omits empty fields, so most are optional.
export interface KnowledgeEntry {
  id: string
  projectId?: string
  projectName?: string
  question: string
  answer: string
  summary?: string
  tags?: string[]
  entities?: string[]
  authorMemberId?: string
  source?: string   // curated | agent_suggested
  status?: string   // active | stale | pending
  createdAt?: string
  updatedAt?: string
}

// ---- Dashboard metrics (org KPI overview) ----

// MetricCard mirrors orako.v1.MetricCard. Connect-JSON omits zero/empty fields,
// so every field is optional — treat undefined as 0 / []. The unit of value and
// deltaPct depends on the card (count & %, % & points, seconds & seconds). An
// empty/absent series means the sparkline is hidden (the median card).
export interface MetricCard {
  value?: number
  deltaPct?: number
  series?: number[]
}

// ReuseCard mirrors orako.v1.ReuseCard. BETA: available is false until the reuse
// signal is instrumented; the UI renders a BETA placeholder while it is false.
export interface ReuseCard {
  available?: boolean
  pct?: number
  deltaPts?: number
}

// ConversationRef mirrors orako.v1.ConversationRef: one "to handle" row.
export interface ConversationRef {
  conversationId: string
  projectId?: string
  title: string
  status: string        // open | timed_out
  askerMemberId?: string
  createdAt?: string
}

// HistoryRef mirrors orako.v1.HistoryRef: one "recently added to history" row.
export interface HistoryRef {
  conversationId: string
  title: string
  tags?: string[]
  resolverMemberId?: string
  resolvedAt?: string
}

// Responder mirrors orako.v1.Responder: one top-responders leaderboard row.
export interface Responder {
  memberId: string
  displayName?: string
  answerCount?: number
}

// TopicCount mirrors orako.v1.TopicCount: one top-topics leaderboard row.
export interface TopicCount {
  label: string
  count?: number
}

// DashboardMetrics mirrors orako.v1.GetDashboardMetricsResponse.
export interface DashboardMetrics {
  conversations?: MetricCard
  responseRate?: MetricCard
  medianFirstResponseSeconds?: MetricCard
  reuse?: ReuseCard
  openCount?: number
  leadsCount?: number
  toHandle?: ConversationRef[]
  recentlyResolved?: HistoryRef[]
  topResponders?: Responder[]
  topTopics?: TopicCount[]
}

export interface Expert {
  memberId: string
  displayName: string
  domains: string[]     // expertise tags the agent routes on
  online: boolean
  email?: string
  status?: string       // invited | active | suspended
  invitedAt?: string
}

export interface ConversationMessage {
  messageId: string
  authorMemberId: string
  role: number
  body: string
  at?: string
  // source: 'human' (typed by a person), 'agent' (posted by a coding agent via
  // the MCP tools on the member's behalf), or 'system'. Drives the "(via agent)"
  // marker so a reader knows a message was not typed by the person directly.
  source?: string
  // agentClient: which coding agent authored an agent message (e.g.
  // 'claude-code'); empty for human/system. Drives the "· Claude Code" label + logo.
  agentClient?: string
}

// ConnectedChannel is one configured external provider and its stored alert
// channel (empty when none is set). "dashboard" is never included: it has no
// config. Lets the dashboard prefill each provider's alert-channel input so a
// credential re-save doesn't blank it.
export interface ConnectedChannel {
  kind: string
  // Optional: Connect-JSON omits a repeated field when it is empty, so this is
  // undefined (not []) for a provider with no alert channels. Guard reads.
  alertChannelIds?: string[]
  // The provider bot's PUBLIC application id (Discord only), derived
  // server-side from the stored token — lets the manage page rebuild the
  // invite/permissions URL without re-entering credentials.
  botClientId?: string
}

// ProviderTestResult mirrors orako.v1.ProviderTestResult: one target's real
// delivery outcome from SendProviderTest.
export interface ProviderTestResult {
  label: string
  ok?: boolean
  error?: string
}

export interface InboxItem {
  conversationId: string
  projectId: string
  question: string
  title?: string
  askerMemberId: string
  status: string
  createdAt?: string
}

// OrganizationSettings mirrors orako.v1.OrganizationSettings. int64 fields
// cross Connect-JSON as strings — normalize with Number() at the call site.
export interface OrganizationSettings {
  // claimTimeoutSeconds is the unanswered-nudge delay (the wire name keeps
  // the legacy "claim" spelling; the claim model itself is gone).
  claimTimeoutSeconds?: number | string
  claimTimeoutIsDefault?: boolean
  // alertTimeoutSeconds is the channel-alert rung: a pool conversation still
  // unanswered after this delay posts once to the alert channel.
  alertTimeoutSeconds?: number | string
  alertTimeoutIsDefault?: boolean
  // defaultAlertChannelId is the org-wide fallback alert channel, used when a
  // project's provider has no alert_channel_id of its own.
  defaultAlertChannelId?: string
}

export interface Member {
  memberId: string
  email: string
  displayName: string
  firstName?: string
  lastName?: string
  gitHandle?: string
  deliveryChannel: string   // slack | teams | telegram | discord | dashboard
  slackUserId: string
  telegramChatId: string
  // teamsUserId is the member's Azure AD object id (Bot Framework
  // from.aadObjectId), the Teams DM binding.
  teamsUserId: string
  // discordUserId is the member's Discord user snowflake, the Discord DM binding.
  discordUserId: string
  // bindingError is the last delivery failure on the member's bound channel
  // (empty = healthy). Cleared when the binding is re-saved.
  bindingError: string
  status: string
}

// ProjectExpertise is a member's expertise tags within one project.
export interface ProjectExpertise {
  projectId: string
  domains?: string[]
}

// ProviderAlertRouting is one provider's per-project alert-channel override
// (from ListProjectsDetailed). Only providers with an explicit override row
// for the project appear here — an org-connected provider with no override
// yet is simply absent (falls back to the org default channel server-side).
export interface ProviderAlertRouting {
  kind: string
  alertChannelIds?: string[]
}

// ProjectDetail backs the Projects tab (identity + per-project stats + routing).
export interface ProjectDetail {
  projectId: string
  name: string
  archived: boolean
  memberCount: number
  conversationCount: number
  resolvedCount: number
  routing?: ProviderAlertRouting[]
}

// OrgMember mirrors orako.v1.OrgMember: the admin-facing rich view backing the
// Team list and Edit Person page.
export interface OrgMember {
  memberId: string
  email: string
  firstName?: string
  lastName?: string
  displayName: string
  gitHandle?: string
  deliveryChannel: string
  slackUserId?: string
  teamsUserId?: string
  telegramChatId?: string
  discordUserId?: string
  bindingError?: string
  status: string          // invited | active | on_leave | deactivated | …
  returnDate?: string     // ISO date, set only when on_leave
  isOrgAdmin?: boolean
  external?: boolean      // no linked login account
  projects?: ProjectExpertise[]
}

// ---- RPC wrappers ----

// JoinCode mirrors the org's shareable join-code messages (GetJoinCode /
// GenerateJoinCode). code is empty when active is false. projectId/projectName
// name the project a redeemer lands on (the org default unless one was chosen).
export interface JoinCode {
  code?: string
  projectId?: string
  projectName?: string
  active?: boolean
}

// McpConnection mirrors orako.v1.McpConnection: one connected MCP client
// (one grant_id), never the access/refresh secrets.
export interface McpConnection {
  grantId: string
  clientName: string
  resource: string
  // projectIds is the connection's scope (ordered, first = primary).
  // Empty/absent means it reaches all the caller's projects.
  projectIds?: string[]
  connectedAt?: string
  lastUsedAt?: string
  expiresAt?: string
}

// MachineToken mirrors orako.v1.MachineToken: the dashboard-facing metadata
// row for one minted machine token (phase 1) — never the secret or its hash.
// Org-scoped: every org admin sees every machine token in the org, not just
// the ones they personally minted (ListMachineTokens/RevokeMachineToken are
// org-scoped server-side).
export interface MachineToken {
  id: string
  label: string
  // projectIds is the token's scope (ordered, first = primary). Empty/absent
  // means it reaches every project the owning member can.
  projectIds?: string[]
  createdAt?: string
  expiresAt?: string
  lastUsedAt?: string
  // revokedAt is absent while the token is live.
  revokedAt?: string
}

// CreateMachineTokenResult mirrors orako.v1.CreateMachineTokenResponse. secret
// is the raw `mcp_at_…` bearer value, present ONLY in this response — it is
// never re-readable via listMachineTokens.
export interface CreateMachineTokenResult {
  token: MachineToken
  secret: string
}

export const api = {
  listProjects(allOrgs = false) {
    // allOrgs=true returns projects across every org the caller belongs to
    // (each with orgId) — used by the agent-connection consent screens.
    return call<object, { projects: ProjectSummary[] }>('ListProjects', { allOrgs })
  },

  listProjectsForOrg(orgId: string) {
    return call<object, { projects: ProjectSummary[] }>('ListProjects', { allOrgs: false }, orgId)
  },

  listOrganizations() {
    return call<object, { organizations?: OrganizationSummary[] }>('ListOrganizations', {})
  },

  // Connected MCP clients (OAuth grants), one entry per grant_id.
  listMcpConnections() {
    return call<object, { connections?: McpConnection[] }>('ListMcpConnections', {})
  },

  revokeMcpConnection(grantId: string) {
    return call<object, Record<string, never>>('RevokeMcpConnection', { grantId })
  },

  // --- Machine tokens (phase 1: non-interactive, admin-minted long-lived
  // tokens for a headless agent). All three RPCs are org-admin gated;
  // list/revoke are scoped to the caller's org, not just the tokens they
  // personally minted. ---

  // Mints a durable access token scoped to projectIds (empty = unscoped,
  // every project the owning member can reach). The raw secret is returned
  // exactly once, here — it is never re-readable via listMachineTokens.
  createMachineToken(label: string, projectIds: string[] = []) {
    return call<object, CreateMachineTokenResult>('CreateMachineToken', { label, projectIds })
  },

  // Every machine token minted in the caller's org, newest first — metadata
  // only, never the secret or its hash.
  listMachineTokens() {
    return call<object, { tokens?: MachineToken[] }>('ListMachineTokens', {})
  },

  // Immediately invalidates a machine token belonging to the caller's org.
  // Idempotent at the RPC boundary in the sense that a second call on an
  // already-revoked id surfaces a ConnectError (not a silent success).
  revokeMachineToken(id: string) {
    return call<object, Record<string, never>>('RevokeMachineToken', { id })
  },

  // --- Org join code (admin-only writes; read is admin-facing on the Team page) ---

  // Current live join code for the caller's org, or { active: false } when none.
  getJoinCode() {
    return call<object, JoinCode>('GetJoinCode', {})
  },

  // Rotates the org's join code (revokes any prior one) and returns the new code.
  // projectId is optional — empty resolves to the org's default project.
  generateJoinCode(projectId = '') {
    return call<object, JoinCode>('GenerateJoinCode', { projectId })
  },

  // Revokes the org's live join code. Idempotent.
  revokeJoinCode() {
    return call<object, Record<string, never>>('RevokeJoinCode', {})
  },

  // projectIds scopes the read to a chosen set of the caller's org projects;
  // empty = every non-archived project the caller may see ("All").
  listConversations(projectIds: string[], status = '') {
    return call<object, { conversations: ConversationSummary[] }>('ListConversations', {
      projectIds,
      status,
    })
  },

  listExperts(projectId: string) {
    return call<object, { experts: Expert[] }>('ListExperts', {
      projectId,
    })
  },

  // History tab: deterministic history search + per-status counts. An empty
  // query browses the most recent conversations in scope. projectIds empty =
  // every non-archived project the caller may see; status '' or 'all' = no
  // status filter; tags overlap each conversation's tags/entities.
  searchHistory(opts: {
    query?: string
    projectIds?: string[]
    status?: string
    tags?: string[]
    topK?: number
  }) {
    return call<object, SearchHistoryResponse>('SearchHistory', {
      query: opts.query ?? '',
      projectIds: opts.projectIds ?? [],
      status: opts.status ?? '',
      tags: opts.tags ?? [],
      topK: opts.topK ?? 0,
    })
  },

  // Dashboard KPI overview. period is "7d" | "30d" | "all" (server defaults to
  // "30d"); projectIds empty = every non-archived project the caller may see.
  getDashboardMetrics(period: string, projectIds: string[] = []) {
    return call<object, DashboardMetrics>('GetDashboardMetrics', { period, projectIds })
  },

  // --- Curated knowledge (Knowledge section) ---

  // List the org's curated entries (every status), newest first. Any member may
  // read; projectIds empty = org-wide read (org-wide entries always included).
  listKnowledgeEntries(projectIds: string[] = []) {
    return call<object, { entries?: KnowledgeEntry[] }>('ListKnowledgeEntries', { projectIds })
  },

  // Author a curated entry (org-admin only). project_id empty = org-wide. Org +
  // author come from the token, never these fields.
  createKnowledgeEntry(input: {
    question: string
    answer: string
    summary?: string
    tags?: string[]
    entities?: string[]
    projectId?: string
  }) {
    return call<object, { entryId: string }>('CreateKnowledgeEntry', {
      projectId: input.projectId ?? '',
      question: input.question,
      answer: input.answer,
      summary: input.summary ?? '',
      tags: input.tags ?? [],
      entities: input.entities ?? [],
    })
  },

  // Edit a curated entry (org-admin only). status '' leaves it unchanged.
  updateKnowledgeEntry(input: {
    entryId: string
    question: string
    answer: string
    summary?: string
    tags?: string[]
    entities?: string[]
    status?: string
  }) {
    return call<object, Record<string, never>>('UpdateKnowledgeEntry', {
      entryId: input.entryId,
      question: input.question,
      answer: input.answer,
      summary: input.summary ?? '',
      tags: input.tags ?? [],
      entities: input.entities ?? [],
      status: input.status ?? '',
    })
  },

  // Flag a curated entry stale so it drops out of search (org-admin only).
  markKnowledgeStale(entryId: string) {
    return call<object, Record<string, never>>('MarkKnowledgeStale', { entryId })
  },

  // Revalidate a stale entry: status back to active + updated_at bumped ("last
  // reviewed"). Org-admin only.
  revalidateKnowledgeEntry(entryId: string) {
    return call<object, Record<string, never>>('RevalidateKnowledgeEntry', { entryId })
  },

  // --- Agent-suggested approval queue (Phase 2, org-admin only) ---

  // List the agent-suggested entries awaiting review (pending), newest first.
  // projectIds empty = org-wide (org-wide entries always included).
  listPendingKnowledge(projectIds: string[] = []) {
    return call<object, { entries?: KnowledgeEntry[] }>('ListPendingKnowledge', { projectIds })
  },

  // Approve a pending agent-suggested entry → active (now searchable). Provenance
  // (source=agent_suggested) is kept.
  approveKnowledgeEntry(entryId: string) {
    return call<object, Record<string, never>>('ApproveKnowledgeEntry', { entryId })
  },

  // Dismiss a pending agent-suggested entry: deletes the row (no 'dismissed' state).
  dismissKnowledgeEntry(entryId: string) {
    return call<object, Record<string, never>>('DismissKnowledgeEntry', { entryId })
  },

  // Promote a RESOLVED conversation into a curated knowledge entry authored by
  // the caller. Returns the new entry id. Org-admin only.
  promoteConversationToKnowledge(conversationId: string) {
    return call<object, { entryId: string }>('PromoteConversationToKnowledge', { conversationId })
  },

  createProject(name: string) {
    return call<object, { projectId: string }>('CreateProject', { name })
  },

  // --- Projects tab (org-admin only) ---

  listProjectsDetailed(includeArchived = false) {
    return call<object, { projects?: ProjectDetail[] }>('ListProjectsDetailed', { includeArchived })
  },

  renameProject(projectId: string, name: string) {
    return call<object, Record<string, never>>('RenameProject', { projectId, name })
  },

  // archived=true is a reversible freeze (routing stops, data kept);
  // archived=false reactivates instantly.
  setProjectArchived(projectId: string, archived: boolean) {
    return call<object, Record<string, never>>('SetProjectArchived', { projectId, archived })
  },

  // Irreversible hard delete — no server-side guard beyond org membership.
  deleteProject(projectId: string) {
    return call<object, Record<string, never>>('DeleteProject', { projectId })
  },

  // resend explicitly re-sends the invitation email to a still-pending member;
  // without it, re-adding an existing member (e.g. to another project) is silent.
  addMember(projectId: string, email: string, domains: string[], slackUserId = '', resend = false) {
    return call<object, { memberId: string }>('AddMember', {
      projectId,
      email,
      domains,
      slackUserId,
      resend,
    })
  },

  // Bulk invite: paste a list of emails, everyone lands on the project. Returns
  // a per-email status (invited | already | invalid | error).
  inviteMembers(projectId: string, emails: string[], domains: string[] = []) {
    return call<object, { results?: { email: string; status: string }[] }>('InviteMembers', {
      projectId,
      emails,
      domains,
    })
  },

  // Backfills Slack ids for already-invited members by resolving their emails
  // against the org's Slack workspace. Returns { scanned, bound } counts.
  syncChatDirectory(projectId: string) {
    return call<object, { scanned?: number; bound?: number }>('SyncChatDirectory', { projectId })
  },

  // Backed by the AssignRole RPC; `domains` replaces the member's tags wholesale.
  setMemberExpertise(projectId: string, memberId: string, domains: string[]) {
    return call<object, Record<string, never>>('AssignRole', {
      projectId,
      memberId,
      domains,
    })
  },

  // Self-serve expertise: sets the CALLER's own domains across every project
  // they belong to. Backed by the SetOwnExpertise RPC, which is NOT admin-gated
  // (identity comes from the auth token), so a plain member can set their own
  // expertise during onboarding — unlike setMemberExpertise/AssignRole.
  setOwnExpertise(domains: string[]) {
    return call<object, Record<string, never>>('SetOwnExpertise', { domains })
  },

  removeMember(projectId: string, memberId: string, purgePii: boolean) {
    return call<object, Record<string, never>>('RemoveMember', {
      projectId,
      memberId,
      purgePii,
    })
  },

  // --- Org-admin member administration (org roster + Edit Person) ---

  listMembers() {
    return call<object, { members?: OrgMember[] }>('ListMembers', {})
  },

  getOrgMember(memberId: string) {
    return call<object, { member: OrgMember }>('GetOrgMember', { memberId })
  },

  // status: 'active' | 'on_leave'; returnDate is an ISO date honored for on_leave.
  setMemberAvailability(memberId: string, status: string, returnDate = '') {
    return call<object, Record<string, never>>('SetMemberAvailability', { memberId, status, returnDate })
  },

  // active=false deactivates and frees the seat; true reactivates.
  setMemberActivation(memberId: string, active: boolean) {
    return call<object, Record<string, never>>('SetMemberActivation', { memberId, active })
  },

  setOrgAdmin(memberId: string, isAdmin: boolean) {
    return call<object, Record<string, never>>('SetOrgAdmin', { memberId, isAdmin })
  },

  getConversation(conversationId: string) {
    return call<object, {
      conversationId: string
      status: string
      messages: ConversationMessage[]
      title?: string
      participants?: Participant[]
    }>('GetConversation', { conversationId })
  },

  followUp(conversationId: string, body: string) {
    return call<object, Record<string, never>>('FollowUp', { conversationId, body })
  },

  resolveConversation(conversationId: string, resolution: string) {
    return call<object, Record<string, never>>('ResolveConversation', { conversationId, resolution })
  },

  // Close an unanswered conversation without recording a resolution. Backend
  // rejects ResolveConversation with an empty resolution (a resolution needs an
  // answer), so a give-up close goes through DismissConversation instead.
  dismissConversation(conversationId: string) {
    return call<object, { status: string }>('DismissConversation', { conversationId })
  },

  // Hard-delete a conversation and everything derived from it (org-admin only).
  deleteConversation(conversationId: string) {
    return call<object, object>('DeleteConversation', { conversationId })
  },

  listInbox() {
    return call<object, { items: InboxItem[] }>('ListInbox', {})
  },

  getMember() {
    return call<object, { member: Member; isOrgAdmin?: boolean }>('GetMember', {})
  },

  updateMember(patch: {
    displayName?: string
    deliveryChannel?: string
    slackUserId?: string
    telegramChatId?: string
    teamsUserId?: string
    discordUserId?: string
    email?: string
    firstName?: string
    lastName?: string
    gitHandle?: string
    // memberId targets another member (org-admin only). Empty/omitted = self.
    memberId?: string
  }) {
    return call<object, { member: Member }>('UpdateMember', patch)
  },

  listConnectedChannels() {
    return call<object, { channels: string[]; connected?: ConnectedChannel[] }>('ListConnectedChannels', {})
  },

  heartbeat() {
    return call<object, Record<string, never>>('Heartbeat', {})
  },

  createOrganization(name: string) {
    return call<object, { organizationId: string; globalProjectId: string }>('CreateOrganization', { name })
  },

  // alertChannelIds are the provider-native channels for escalation alerts on
  // this project — the sweep posts to each. Empty falls back to the
  // organization's default alert channel. Credentials are always required (see
  // requiredCredentials server-side) — there is no way to update the alert
  // channels alone without resending valid credentials for the kind.
  configureProvider(projectId: string, kind: string, credentials: Record<string, string>, alertChannelIds?: string[]) {
    return call<object, Record<string, never>>('ConfigureProvider', {
      projectId,
      kind,
      credentials,
      alertChannelIds,
    })
  },

  // updateProviderAlertChannels edits only a connected provider's alert channels
  // — an empty credentials map tells the server to leave the stored secrets
  // untouched (which the client never receives back). Fails if not yet configured.
  updateProviderAlertChannels(projectId: string, kind: string, alertChannelIds: string[]) {
    return call<object, Record<string, never>>('ConfigureProvider', {
      projectId,
      kind,
      credentials: {},
      alertChannelIds,
    })
  },

  // disconnectProvider removes a project's messaging provider (admin-only).
  disconnectProvider(projectId: string, kind: string) {
    return call<object, Record<string, never>>('DisconnectProvider', { projectId, kind })
  },

  // sendProviderTest runs a REAL delivery test (DM to you + a post to each alert
  // channel) and returns a per-target result with the actual provider error.
  sendProviderTest(kind: string) {
    return call<object, { results?: ProviderTestResult[] }>('SendProviderTest', { kind })
  },

  // Organization identity (id + name) for the caller's org. Any member may read
  // it; the dashboard derives the workspace slug from the name client-side.
  getOrganization() {
    return call<object, { organizationId: string; name: string }>('GetOrganization', {})
  },

  // Rename the caller's organization (org-admin only). The org is resolved from
  // the auth token server-side; only the new name crosses the wire. Returns the
  // saved (trimmed) name.
  renameOrganization(name: string) {
    return call<object, { name: string }>('RenameOrganization', { name })
  },

  // deleteOrganization hard-deletes the caller's org and all its data. confirmName
  // must match the org's current name (the typed-name guard); the org itself is
  // resolved server-side from the caller identity, never sent here.
  deleteOrganization(confirmName: string) {
    return call<object, Record<string, never>>('DeleteOrganization', { confirmName })
  },

  // Escalation settings (org-admin only). Seconds; 0 disables the rule.
  getOrganizationSettings() {
    return call<object, { settings?: OrganizationSettings }>('GetOrganizationSettings', {})
  },

  // alertTimeoutSeconds: 0 disables the channel alert. defaultAlertChannelId:
  // the literal "-" clears the org fallback channel; "" leaves it unchanged.
  updateOrganizationSettings(
    claimTimeoutSeconds: number,
    alertTimeoutSeconds: number,
    defaultAlertChannelId: string | undefined,
  ) {
    return call<object, { settings?: OrganizationSettings }>('UpdateOrganizationSettings', {
      claimTimeoutSeconds,
      alertTimeoutSeconds,
      defaultAlertChannelId,
    })
  },
}

export const ROLE_LABELS: Record<number, string> = {
  0: 'unspecified',
  1: 'dev',
  2: 'specialist',
  3: 'lead',
  4: 'admin',
}
