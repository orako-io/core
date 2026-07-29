// Server-provided identity: once auth is established (Supabase session in oidc,
// stub token in dev) the caller's projects and member record are fetched from
// the server and the selected project is held here.

import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useState,
  type ReactNode,
} from 'react'
import { api, fetchOnboardingDismissed, ROLE_LABELS, type ProjectSummary } from './client'
import { isOidc } from './auth-mode'
import { supabase } from './supabase'
import { loadToken, parseToken, TOKEN_EVENT } from './token'

export interface Identity {
  // Whether an auth principal is established (session in oidc, token in dev).
  authed: boolean
  // Initial auth resolution or an identity fetch is in flight.
  loading: boolean
  error: unknown

  memberID: string
  projects: ProjectSummary[]
  selectedProjectId: string
  role: number // numeric proto Role enum of the selected project (0 if none)
  roleLabel: string
  // Fails closed: false until the server confirms admin.
  isOrgAdmin: boolean
  accountOnly: boolean // authed but zero projects → onboarding
  // Profile-completeness heuristic (no schema flag): true once we know the
  // member exists and has never set a first name — AddMember never collects
  // one (display name falls back to the invite email, so it's never a
  // reliable "empty" signal) and the org-creator path leaves it blank too.
  // Drives the one-time /onboarding redirect; flips false the moment the
  // member saves a first name from the wizard or Account settings.
  needsOnboarding: boolean
  // Permanent, per-member Get-started dismissal (server-side, survives reloads
  // and follows the member across devices). Once true the "Get started" nav item
  // and page are gone for good; Undo flips it back. Read from GET /api/onboarding.
  onboardingDismissed: boolean
  // True once the first identity fetch has settled — gate the app shell on this
  // so placeholder data never flashes before we know who the caller is.
  bootstrapped: boolean

  setSelectedProjectId: (id: string) => void
  refresh: () => Promise<void>
}

const IdentityContext = createContext<Identity | null>(null)

// Reads the current dev-stub identity (dev mode only). Returns null in oidc.
function devIdentity(): ReturnType<typeof parseToken> {
  if (isOidc) return null
  const t = loadToken()
  return t ? parseToken(t) : null
}

export function IdentityProvider({ children }: { children: ReactNode }) {
  const [authed, setAuthed] = useState(false)
  // True until the initial auth state (session/token) has been resolved.
  const [authResolved, setAuthResolved] = useState(false)
  const [fetching, setFetching] = useState(false)
  const [error, setError] = useState<unknown>(null)

  const [projects, setProjects] = useState<ProjectSummary[]>([])
  const [memberID, setMemberID] = useState('')
  const [isOrgAdmin, setIsOrgAdmin] = useState(false)
  const [selectedProjectId, setSelectedProjectId] = useState('')
  const [bootstrapped, setBootstrapped] = useState(false)
  const [firstName, setFirstName] = useState('')
  const [onboardingDismissed, setOnboardingDismissed] = useState(false)

  // Pull identity from the server once auth is established.
  const load = useCallback(async () => {
    setFetching(true)
    setError(null)
    try {
      const res = await api.listProjects()
      const list = res.projects ?? []
      setProjects(list)

      const dev = devIdentity()
      setSelectedProjectId(prev => {
        if (prev && list.some(p => p.id === prev)) return prev
        // Only honor the dev token's project when it is in the active org's list;
        // after an org switch the token still points at the previous org.
        if (dev?.projectID && list.some(p => p.id === dev.projectID)) return dev.projectID
        return list[0]?.id ?? ''
      })

      // Best-effort: account-only users have no member record yet.
      try {
        const m = await api.getMember()
        setMemberID(m.member?.memberId || dev?.memberID || '')
        setIsOrgAdmin(!!m.isOrgAdmin)
        setFirstName(m.member?.firstName ?? '')
      } catch {
        setMemberID(dev?.memberID || '')
        setIsOrgAdmin(false)
        setFirstName('')
      }

      // Get-started dismissal — best-effort, never blocks identity resolution.
      setOnboardingDismissed(await fetchOnboardingDismissed())
    } catch (e) {
      setError(e)
      // Deliberately KEEP the last-known projects: a transient fetch failure
      // (server restarting, network blip) must not make an onboarded account
      // look account-only — that used to bounce users to the "create your
      // first organization" page mid-session.
    } finally {
      setFetching(false)
      setBootstrapped(true)
    }
  }, [])

  // Track the auth principal and (re)load identity when it appears/changes.
  useEffect(() => {
    let cancelled = false

    function apply(next: boolean) {
      if (cancelled) return
      setAuthed(next)
      setAuthResolved(true)
      if (next) {
        void load()
      } else {
        setProjects([])
        setMemberID('')
        setIsOrgAdmin(false)
        setSelectedProjectId('')
        setFirstName('')
        setError(null)
        setBootstrapped(true)
      }
    }

    if (isOidc) {
      supabase?.auth.getSession().then(({ data }) => apply(!!data.session))
      const { data: sub } = supabase?.auth.onAuthStateChange((_event, session) => {
        apply(!!session)
      }) ?? { data: { subscription: null } }
      return () => {
        cancelled = true
        sub?.subscription?.unsubscribe()
      }
    }

    // dev mode: the stub token is the principal.
    apply(!!loadToken())
    const onToken = () => apply(!!loadToken())
    window.addEventListener(TOKEN_EVENT, onToken)
    return () => {
      cancelled = true
      window.removeEventListener(TOKEN_EVENT, onToken)
    }
  }, [load])

  const value = useMemo<Identity>(() => {
    const selected = projects.find(p => p.id === selectedProjectId)
    const role = selected?.role ?? 0
    return {
      authed,
      loading: !authResolved || fetching,
      error,
      memberID,
      projects,
      selectedProjectId,
      role,
      roleLabel: ROLE_LABELS[role] ?? String(role),
      isOrgAdmin,
      // error==null is required: "zero projects" only means "needs onboarding"
      // when the fetch actually SUCCEEDED — an unreachable API must never be
      // read as a blank account (the redirect-to-/welcome bug). `bootstrapped`
      // is required too: create-org must only show AFTER identity is fully
      // resolved, never mid-load — otherwise an invited member (who does have a
      // pending membership) briefly looks account-only and the create-org page
      // flashes on the email-confirmation tab before their membership settles.
      accountOnly: authed && bootstrapped && !fetching && error == null && projects.length === 0,
      // Only meaningful once a member record exists and the fetch has
      // settled — never true for accountOnly (no member yet) or mid-fetch.
      needsOnboarding: authed && bootstrapped && !fetching && !!memberID && !firstName.trim(),
      onboardingDismissed,
      bootstrapped,
      setSelectedProjectId,
      refresh: load,
    }
  }, [authed, authResolved, fetching, error, memberID, isOrgAdmin, projects, selectedProjectId, bootstrapped, firstName, onboardingDismissed, load])

  return <IdentityContext.Provider value={value}>{children}</IdentityContext.Provider>
}

export function useIdentity(): Identity {
  const ctx = useContext(IdentityContext)
  if (!ctx) throw new Error('useIdentity must be used within an IdentityProvider')
  return ctx
}
