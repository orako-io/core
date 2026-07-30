import { NavLink, useLocation, useNavigate } from 'react-router'
import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useRef,
  useState,
  type ReactNode,
} from 'react'
import { clearToken } from '../lib/token'
import { isOidc } from '../lib/auth-mode'
import { useEdition } from '../lib/edition'
import { useBillingStatus } from '../lib/billing-status'
import { supabase } from '../lib/supabase'
import { useIdentity } from '../lib/identity'
import { useOnboardingComplete } from '../lib/onboarding'
import { useRealtime } from '../lib/realtime'
import { api } from '../lib/client'
import { T } from '../lib/theme'
import { Icon, LogoTile, type IconName } from './Icon'
import { OrgSwitcher } from './OrgSwitcher'
import { ProjectSwitcher } from './ProjectSwitcher'

type NavItem = { to: string; label: string; icon: IconName; badge?: 'inbox' | 'conversations' }
type NavSection = { title: string; subtitle?: string; items: NavItem[] }

// Two zones. Zone A = the active ORGANIZATION (re-scoped by the org switcher);
// Zone B = ACCOUNT (the signed-in person's own things, unchanged across orgs).
function navSections(
  isOrgAdmin: boolean,
  orgName: string,
  dashboardReady: boolean,
  onboardingDismissed: boolean,
): NavSection[] {
  const org = orgName || 'your org'
  // The first ORGANIZATION item:
  //  - admin keeps a home: the "Get started" checklist until onboarding is
  //    complete (or permanently dismissed), then the KPI "Dashboard".
  //  - a teammate/guest has no KPI dashboard, so once they permanently dismiss
  //    the Get-started onboarding the item disappears entirely (they land on
  //    their Inbox). Same /get-started route; the page branches on these signals.
  const firstItem: NavItem | null = isOrgAdmin
    ? dashboardReady || onboardingDismissed
      ? { to: '/get-started', label: 'Dashboard', icon: 'dashboard' }
      : { to: '/get-started', label: 'Get started', icon: 'sparkles' }
    : onboardingDismissed
      ? null
      : { to: '/get-started', label: 'Get started', icon: 'sparkles' }
  if (isOrgAdmin) {
    return [
      {
        title: 'ORGANIZATION',
        subtitle: `scoped to ${org}`,
        items: [
          ...(firstItem ? [firstItem] : []),
          { to: '/conversations', label: 'Conversations', icon: 'message', badge: 'conversations' },
          { to: '/history', label: 'History', icon: 'history' },
          { to: '/knowledge', label: 'Knowledge', icon: 'book' },
          { to: '/projects', label: 'Projects', icon: 'folder' },
          { to: '/members', label: 'Team', icon: 'users' },
          { to: '/integrations', label: 'Integrations', icon: 'grid' },
          __SAAS__
            ? { to: '/billing', label: 'Billing', icon: 'card' }
            : { to: '/license', label: 'License', icon: 'shield' },
          { to: '/organization', label: 'Organization', icon: 'building' },
          // Machine tokens (phase 1): org infrastructure, not a personal
          // session — admin-only, same as Organization above (the RPCs are
          // org-admin gated server-side too).
          { to: '/machine-tokens', label: 'Machine tokens', icon: 'lock' },
        ],
      },
      {
        title: 'ACCOUNT',
        subtitle: 'stays the same across orgs',
        items: [
          { to: '/connections', label: 'Connections', icon: 'terminal' },
          { to: '/account', label: 'Account', icon: 'user' },
        ],
      },
    ]
  }
  return [
    {
      title: 'ORGANIZATION',
      subtitle: `scoped to ${org}`,
      items: [
        ...(firstItem ? [firstItem] : []),
        { to: '/inbox', label: 'Inbox', icon: 'inbox', badge: 'inbox' },
        { to: '/conversations', label: 'Conversations', icon: 'message' },
        { to: '/history', label: 'History', icon: 'history' },
        { to: '/knowledge', label: 'Knowledge', icon: 'book' },
        { to: '/members', label: 'Team', icon: 'users' },
      ],
    },
    {
      title: 'ACCOUNT',
      subtitle: 'stays the same across orgs',
      items: [
        { to: '/connections', label: 'Connections', icon: 'terminal' },
        { to: '/account', label: 'Profile', icon: 'user' },
        { to: '/notifications', label: 'Notifications', icon: 'bell' },
      ],
    },
  ]
}

const RAIL_W = 60
const SIDEBAR_W = 256
const SIDEBAR_W_COLLAPSED = 64
const SIDEBAR_COLLAPSED_KEY = 'orako:sidebarCollapsed'

// ── Page header slot ─────────────────────────────────────────────────────────
// Pages push a title / breadcrumb / right-side actions into the top bar.

export interface PageHeader {
  title?: ReactNode
  crumb?: { to: string; label: string }
  actions?: ReactNode
}

const HeaderContext = createContext<{ set: (h: PageHeader | null) => void }>({ set: () => {} })

export function usePageHeader(header: PageHeader, deps: unknown[]) {
  const { set } = useContext(HeaderContext)
  useEffect(() => {
    set(header)
    return () => set(null)
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, deps)
}

const DEFAULT_TITLES: [string, string][] = [
  ['/get-started', 'Get started'],
  ['/conversations', 'Conversations'],
  ['/history', 'History'],
  ['/knowledge', 'Knowledge'],
  ['/inbox', 'Inbox'],
  ['/projects', 'Projects'],
  ['/members', 'Team'],
  ['/integrations', 'Integrations'],
  ['/connect-agent', 'Connect your agent'],
  ['/connections', 'Connections'],
  ['/organization', 'Organization'],
  ['/machine-tokens', 'Machine tokens'],
  // Billing is SaaS-only; its title (and page) never ship in the community build.
  ...(__SAAS__ ? ([['/billing', 'Billing']] as [string, string][]) : []),
  ['/license', 'License'],
  ['/account', 'Account'],
  ['/notifications', 'Notifications'],
]

function initials(seed: string): string {
  const s = seed.replace(/[^a-zA-Z0-9]/g, '')
  return (s.slice(0, 2) || 'ME').toUpperCase()
}

export function Layout({ children }: { children: ReactNode }) {
  const navigate = useNavigate()
  const location = useLocation()
  const { authed, memberID, isOrgAdmin, onboardingDismissed } = useIdentity()
  // Onboarding completeness drives the first ORGANIZATION nav item (Dashboard vs
  // Get started). Null while it loads → keep "Get started" (no flash to Dashboard).
  const dashboardReady = useOnboardingComplete() === true
  // Self-host edition for the sidebar license card (skipped on SaaS/oidc, where
  // the card is not shown). Null until loaded → falls back to a neutral label.
  const { data: edition } = useEdition()
  // SaaS subscription status (null on non-SaaS / 404 / before load). Drives the
  // org badge subtitle and the bottom-left trial/upgrade card.
  const billingStatus = useBillingStatus()

  const [header, setHeader] = useState<PageHeader | null>(null)
  const headerCtx = useMemo(() => ({ set: setHeader }), [])

  // Collapsed rail: icon-only nav, persisted across reloads.
  const [collapsed, setCollapsed] = useState<boolean>(() => {
    try {
      return localStorage.getItem(SIDEBAR_COLLAPSED_KEY) === '1'
    } catch {
      return false
    }
  })
  useEffect(() => {
    try {
      localStorage.setItem(SIDEBAR_COLLAPSED_KEY, collapsed ? '1' : '0')
    } catch {
      /* localStorage unavailable (private mode, etc.) — collapse still works this session */
    }
  }, [collapsed])
  const [hoveredNav, setHoveredNav] = useState('')

  // Real organization name for the workspace switcher (empty until loaded;
  // falls back to a neutral label rather than a hardcoded placeholder).
  const [orgName, setOrgName] = useState('')
  useEffect(() => {
    if (!authed) return
    api
      .getOrganization()
      .then(res => setOrgName(res.name))
      .catch(() => {})
  }, [authed])

  const [inboxCount, setInboxCount] = useState(0)
  const [openCount, setOpenCount] = useState(0)
  const fetchInboxCount = useCallback(() => {
    if (!authed) return
    api
      .listInbox()
      .then(res => setInboxCount(res.items?.length ?? 0))
      .catch(() => {})
  }, [authed])
  // Fetch once (on auth); the SSE stream below keeps it fresh. NOT re-fetched on
  // every navigation — that added a redundant round-trip to each page change.
  useEffect(() => {
    fetchInboxCount()
  }, [fetchInboxCount])
  // Push, not poll: the SSE stream invalidates the badge the moment a
  // conversation event lands.
  useRealtime(['conversation_opened', 'message_posted', 'conversation_closed'], fetchInboxCount)

  // Dashboard presence: a fresh heartbeat every 30s while the tab is visible
  // marks this member online for agent routing; presence decays server-side
  // (90s), so closing the tab is enough to go offline.
  useEffect(() => {
    if (!authed || !memberID) return
    const beat = () => {
      if (!document.hidden) void api.heartbeat().catch(() => {})
    }
    beat()
    const interval = window.setInterval(beat, 30_000)
    const onVisible = () => {
      if (!document.hidden) beat()
    }
    document.addEventListener('visibilitychange', onVisible)
    return () => {
      window.clearInterval(interval)
      document.removeEventListener('visibilitychange', onVisible)
    }
  }, [authed, memberID])
  // Org-wide (projectIds: []) since Conversations is cross-project by default now
  // — the badge counts every open conversation the caller may see, not just one
  // project's.
  const fetchOpenCount = useCallback(() => {
    if (!authed) return
    api
      .listConversations([], 'open')
      .then(res => setOpenCount(res.conversations?.length ?? 0))
      .catch(() => {})
  }, [authed])
  // Fetch once (on auth); the SSE stream below keeps it fresh.
  useEffect(() => {
    fetchOpenCount()
  }, [fetchOpenCount])
  useRealtime(['conversation_opened', 'conversation_closed'], fetchOpenCount)

  const [menuOpen, setMenuOpen] = useState(false)
  const menuRef = useRef<HTMLDivElement>(null)
  useEffect(() => {
    if (!menuOpen) return
    function onDown(e: MouseEvent) {
      if (!menuRef.current?.contains(e.target as Node)) setMenuOpen(false)
    }
    document.addEventListener('mousedown', onDown)
    return () => document.removeEventListener('mousedown', onDown)
  }, [menuOpen])

  async function handleSignOut() {
    if (isOidc) {
      await supabase?.auth.signOut()
    } else {
      clearToken()
    }
    navigate('/auth')
  }

  const defaultTitle =
    DEFAULT_TITLES.find(([prefix]) => location.pathname.startsWith(prefix))?.[1] ?? 'Orako'
  const title = header?.title ?? defaultTitle

  // Org badge subtitle. Teammates and self-host have fixed labels; a SaaS admin's
  // label reflects the real subscription status (empty while it loads → no flash).
  const orgSubtitle = !isOrgAdmin
    ? 'Teammate'
    : !isOidc
      ? 'Self-hosted'
      : billingStatus === 'active'
        ? 'Team plan'
        : billingStatus === 'past_due'
          ? 'Payment due'
          : billingStatus === 'canceled'
            ? 'Subscription ended'
            : billingStatus === 'trialing'
              ? 'Free trial'
              : ''

  // The bottom-left trial/upgrade card only makes sense while the subscription
  // is not active. Hidden when active or still loading (null) → no flash.
  const showTrialCard =
    billingStatus === 'trialing' || billingStatus === 'past_due' || billingStatus === 'canceled'

  // The connect-agent stepper is a dedicated full-height flow with its own
  // in-content topbar (breadcrumb + Exit setup), so suppress the app header
  // there to avoid a double bar.
  const hideChromeHeader = location.pathname.startsWith('/integrations/connect/')

  return (
    <HeaderContext.Provider value={headerCtx}>
      <div style={{ display: 'flex', minHeight: '100vh', background: T.bg }}>
        {/* Icon rail */}
        <div
          style={{
            width: RAIL_W,
            flexShrink: 0,
            background: T.surface,
            borderRight: `1px solid ${T.borderSubtle}`,
            display: 'flex',
            flexDirection: 'column',
            alignItems: 'center',
            padding: '16px 0',
            position: 'sticky',
            top: 0,
            height: '100vh',
            zIndex: 10,
          }}
        >
          <LogoTile size={34} radius={10} />
          <div
            style={{
              marginTop: 'auto',
              display: 'flex',
              flexDirection: 'column',
              gap: 20,
              alignItems: 'center',
              color: T.faint,
            }}
          >
            <button
              onClick={() => navigate(isOrgAdmin ? '/conversations' : '/inbox')}
              title="Notifications"
              style={railBtn}
            >
              <span style={{ position: 'relative', display: 'inline-flex' }}>
                <Icon name="bell" size={20} />
                {inboxCount > 0 && (
                  <span
                    style={{
                      position: 'absolute',
                      top: -1,
                      right: 0,
                      width: 7,
                      height: 7,
                      borderRadius: '50%',
                      background: '#EC5850',
                      border: '1.5px solid #fff',
                    }}
                  />
                )}
              </span>
            </button>
            <a
              href="https://orako.io"
              target="_blank"
              rel="noreferrer"
              title="Help & docs"
              style={{ ...railBtn, color: 'inherit' }}
            >
              <Icon name="help" size={20} />
            </a>
            <div ref={menuRef} style={{ position: 'relative' }}>
              <button
                onClick={() => setMenuOpen(o => !o)}
                title="Account"
                style={{ ...railBtn, padding: 0 }}
              >
                <div
                  style={{
                    width: 30,
                    height: 30,
                    borderRadius: '50%',
                    background: '#F3D9A4',
                    display: 'flex',
                    alignItems: 'center',
                    justifyContent: 'center',
                    fontSize: 11,
                    fontWeight: 700,
                    color: '#7A5A16',
                  }}
                >
                  {initials(memberID || 'ME')}
                </div>
              </button>
              {menuOpen && (
                <div
                  style={{
                    position: 'absolute',
                    left: 44,
                    bottom: 0,
                    width: 180,
                    background: T.surface,
                    border: `1px solid ${T.border}`,
                    borderRadius: T.rLg,
                    boxShadow: T.shadowModal,
                    padding: 6,
                    zIndex: 50,
                  }}
                >
                  <button
                    onClick={() => {
                      setMenuOpen(false)
                      navigate('/account')
                    }}
                    style={menuItem}
                  >
                    <Icon name="user" size={15} color={T.subtle} />
                    Account
                  </button>
                  <button onClick={handleSignOut} style={menuItem}>
                    <Icon name="logout" size={15} color={T.subtle} />
                    Sign out
                  </button>
                </div>
              )}
            </div>
          </div>
        </div>

        {/* Sidebar */}
        <nav
          style={{
            width: collapsed ? SIDEBAR_W_COLLAPSED : SIDEBAR_W,
            flexShrink: 0,
            background: T.surface,
            borderRight: `1px solid ${T.borderSubtle}`,
            display: 'flex',
            flexDirection: 'column',
            padding: collapsed ? '16px 8px' : '16px 12px',
            position: 'sticky',
            top: 0,
            height: '100vh',
            transition: 'width .15s ease',
          }}
        >
          {/* Collapse toggle: icon-only rail vs full labels, persisted in
              localStorage so it survives a reload. */}
          <div style={{ display: 'flex', justifyContent: collapsed ? 'center' : 'flex-end', marginBottom: 10 }}>
            <button
              onClick={() => setCollapsed(c => !c)}
              title={collapsed ? 'Expand sidebar' : 'Collapse sidebar'}
              aria-label={collapsed ? 'Expand sidebar' : 'Collapse sidebar'}
              style={{
                display: 'inline-flex',
                alignItems: 'center',
                justifyContent: 'center',
                width: 26,
                height: 26,
                flex: 'none',
                border: `1px solid ${T.borderStrong}`,
                borderRadius: 8,
                background: T.surface,
                color: T.subtle,
                cursor: 'pointer',
              }}
            >
              <Icon name="chevronRight" size={13} style={{ transform: collapsed ? 'none' : 'rotate(180deg)' }} />
            </button>
          </div>

          {/* Org switcher: lists the orgs the caller participates in and switches
              the active one (top isolation boundary: team, projects, integrations).
              Shrinks to just its avatar when collapsed. */}
          <OrgSwitcher orgName={orgName} subtitle={orgSubtitle} collapsed={collapsed} />

          {/* Project switcher: scopes the dashboard (team, conversations) to the
              active project. Hidden on the collapsed rail; has its own admin/empty
              guards. */}
          {!collapsed && <ProjectSwitcher />}

          <div style={{ display: 'flex', flexDirection: 'column', overflowY: 'auto' }}>
            {navSections(isOrgAdmin, orgName, dashboardReady, onboardingDismissed).map((section, i) => (
              <div key={section.title}>
                {!collapsed ? (
                  <div
                    style={{
                      padding: '0 10px',
                      margin: i === 0 ? '0 0 6px' : '18px 0 6px',
                    }}
                  >
                    <div style={{ fontSize: 11, fontWeight: 600, letterSpacing: '.06em', color: T.label }}>{section.title}</div>
                    {section.subtitle && (
                      <div style={{ fontSize: 10.5, fontWeight: 500, color: T.faint, marginTop: 1 }}>{section.subtitle}</div>
                    )}
                  </div>
                ) : (
                  i > 0 && <div style={{ height: 1, background: T.borderSubtle, margin: '10px 6px' }} />
                )}
                {section.items.map(({ to, label, icon, badge }) => (
                  <div
                    key={to}
                    style={{ position: 'relative' }}
                    onMouseEnter={() => setHoveredNav(to)}
                    onMouseLeave={() => setHoveredNav(h => (h === to ? '' : h))}
                  >
                    <NavLink
                      to={to}
                      style={({ isActive }) => ({
                        display: 'flex',
                        alignItems: 'center',
                        justifyContent: collapsed ? 'center' : 'flex-start',
                        gap: collapsed ? 0 : 11,
                        padding: collapsed ? '10px 0' : '8px 10px',
                        borderRadius: 9,
                        textDecoration: 'none',
                        fontSize: 14,
                        fontWeight: isActive ? 600 : 500,
                        color: isActive ? T.body : '#5C6470',
                        background: isActive ? T.sunken : 'transparent',
                      })}
                    >
                      {({ isActive }) => (
                        <>
                          <Icon name={icon} size={18} color={isActive ? T.body : T.subtle} />
                          {!collapsed && <span>{label}</span>}
                          {!collapsed && badge === 'inbox' && inboxCount > 0 && (
                            <span
                              style={{
                                marginLeft: 'auto',
                                minWidth: 18,
                                height: 18,
                                padding: '0 5px',
                                borderRadius: 9,
                                background: T.accent,
                                color: '#fff',
                                fontSize: 11,
                                fontWeight: 700,
                                display: 'flex',
                                alignItems: 'center',
                                justifyContent: 'center',
                              }}
                            >
                              {inboxCount}
                            </span>
                          )}
                          {!collapsed && badge === 'conversations' && openCount > 0 && (
                            <span
                              style={{
                                marginLeft: 'auto',
                                fontSize: 11,
                                fontWeight: 600,
                                color: T.subtle,
                                background: T.sunken,
                                padding: '1px 7px',
                                borderRadius: 10,
                              }}
                            >
                              {openCount}
                            </span>
                          )}
                        </>
                      )}
                    </NavLink>
                    {collapsed && hoveredNav === to && (
                      <div
                        role="tooltip"
                        style={{
                          position: 'absolute',
                          left: '100%',
                          top: '50%',
                          transform: 'translateY(-50%)',
                          marginLeft: 10,
                          background: '#1D2430',
                          color: '#fff',
                          fontSize: 12.5,
                          fontWeight: 600,
                          padding: '6px 11px',
                          borderRadius: 7,
                          whiteSpace: 'nowrap',
                          zIndex: 60,
                          boxShadow: T.shadowModal,
                          pointerEvents: 'none',
                        }}
                      >
                        {label}
                      </div>
                    )}
                  </div>
                ))}
              </div>
            ))}
          </div>

          {/* Plan / info card */}
          {collapsed || !isOrgAdmin ? null : __SAAS__ ? (
            showTrialCard ? (
              <div
                style={{
                  marginTop: 'auto',
                  border: '1px solid #E6E4F7',
                  borderRadius: T.rLg,
                  padding: 14,
                  background: '#F7F6FE',
                }}
              >
                <div style={{ marginBottom: 9 }}>
                  <div style={{ fontSize: 13, fontWeight: 600, color: '#211E3B' }}>Free trial</div>
                  <div style={{ fontSize: 12, color: T.subtle, marginTop: 3, lineHeight: 1.5 }}>
                    Subscribe to keep Orako running when your trial ends.
                  </div>
                </div>
                <button
                  onClick={() => navigate('/billing')}
                  style={{
                    width: '100%',
                    height: 36,
                    border: 'none',
                    borderRadius: 8,
                    background: T.accent,
                    color: '#fff',
                    fontSize: 13,
                    fontWeight: 600,
                    fontFamily: 'inherit',
                    cursor: 'pointer',
                  }}
                >
                  Upgrade plan
                </button>
              </div>
            ) : null
          ) : (
            <button
              onClick={() => navigate(isOrgAdmin ? '/license' : '/notifications')}
              style={{
                marginTop: 'auto',
                border: `1px solid ${T.border}`,
                borderRadius: T.rLg,
                padding: 14,
                background: T.surfaceAlt,
                textAlign: 'left',
                cursor: 'pointer',
                fontFamily: 'inherit',
              }}
            >
              <div style={{ display: 'flex', alignItems: 'center', gap: 7, marginBottom: 5 }}>
                <Icon name="shield" size={14} color={edition?.edition === 'licensed' ? T.successInk : T.accentInk} />
                <span style={{ fontSize: 13, fontWeight: 600, color: T.body }}>
                  {edition?.edition === 'licensed' ? 'License active' : 'Community edition'}
                </span>
              </div>
              <div style={{ fontSize: 11.5, color: T.subtle, lineHeight: 1.5 }}>
                Self-hosted deployment
              </div>
            </button>
          )}
        </nav>

        {/* Content column */}
        <div style={{ flex: 1, display: 'flex', flexDirection: 'column', minWidth: 0 }}>
          {!hideChromeHeader && (
          <header
            style={{
              height: 60,
              flex: 'none',
              borderBottom: `1px solid ${T.borderSubtle}`,
              background: T.surface,
              display: 'flex',
              alignItems: 'center',
              justifyContent: 'space-between',
              padding: '0 28px',
              position: 'sticky',
              top: 0,
              zIndex: 5,
            }}
          >
            <div style={{ display: 'flex', alignItems: 'center', gap: 9, minWidth: 0 }}>
              {header?.crumb && (
                <>
                  <span
                    onClick={() => navigate(header.crumb!.to)}
                    style={{ fontSize: 14, color: T.subtle, cursor: 'pointer' }}
                  >
                    {header.crumb.label}
                  </span>
                  <Icon name="chevronRight" size={14} color={T.faint} />
                </>
              )}
              <span
                style={{
                  fontSize: 15,
                  fontWeight: 600,
                  color: T.body,
                  overflow: 'hidden',
                  textOverflow: 'ellipsis',
                  whiteSpace: 'nowrap',
                }}
              >
                {title}
              </span>
            </div>
            {header?.actions && <div style={{ display: 'flex', alignItems: 'center', gap: 10 }}>{header.actions}</div>}
          </header>
          )}

          <main style={{ flex: 1, overflowY: 'auto' }}>{children}</main>
        </div>
      </div>
    </HeaderContext.Provider>
  )
}

const railBtn: React.CSSProperties = {
  display: 'inline-flex',
  alignItems: 'center',
  justifyContent: 'center',
  border: 'none',
  background: 'transparent',
  color: 'inherit',
  cursor: 'pointer',
  padding: 2,
}

const menuItem: React.CSSProperties = {
  display: 'flex',
  alignItems: 'center',
  gap: 9,
  width: '100%',
  padding: '8px 10px',
  borderRadius: 8,
  border: 'none',
  background: 'transparent',
  fontSize: 13.5,
  fontWeight: 500,
  color: '#3A414D',
  cursor: 'pointer',
  fontFamily: 'inherit',
  textAlign: 'left',
}

// ── Shared primitives ────────────────────────────────────────────────────────

export function btnStyle(variant: 'primary' | 'danger' | 'ghost' | 'secondary'): React.CSSProperties {
  const base: React.CSSProperties = {
    display: 'inline-flex',
    alignItems: 'center',
    justifyContent: 'center',
    gap: 8,
    border: 'none',
    borderRadius: T.rSm,
    padding: '9px 16px',
    fontSize: 13.5,
    lineHeight: 1,
    cursor: 'pointer',
    fontWeight: 600,
    fontFamily: 'inherit',
  }
  if (variant === 'primary') return { ...base, background: T.accent, color: '#fff', boxShadow: T.shadowBtn }
  if (variant === 'danger') return { ...base, background: T.danger, color: '#fff' }
  if (variant === 'ghost')
    return { ...base, background: 'transparent', color: T.muted, padding: '6px 8px', fontWeight: 500 }
  return { ...base, background: T.surface, color: T.body, border: `1px solid ${T.borderStrong}`, fontWeight: 500 }
}

export function inputStyle(): React.CSSProperties {
  return {
    background: T.surface,
    border: `1px solid ${T.borderStrong}`,
    borderRadius: T.rMd,
    color: T.body,
    padding: '10px 13px',
    fontSize: 14,
    fontFamily: 'inherit',
    width: '100%',
  }
}

export function Card({ children, style }: { children: ReactNode; style?: React.CSSProperties }) {
  return (
    <div
      style={{
        background: T.surface,
        border: `1px solid ${T.border}`,
        borderRadius: T.rXl,
        padding: 24,
        boxShadow: T.shadowCard,
        ...style,
      }}
    >
      {children}
    </div>
  )
}

// Standard page body wrapper: mockup content area padding + max width.
export function Page({ children, width = 900 }: { children: ReactNode; width?: number }) {
  return (
    <div style={{ padding: '30px 40px' }}>
      <div style={{ maxWidth: width, margin: '0 auto' }}>{children}</div>
    </div>
  )
}

export function PageTitle({ children, subtitle }: { children: ReactNode; subtitle?: ReactNode }) {
  return (
    <div style={{ margin: '0 0 24px' }}>
      <h1 style={{ fontSize: 27, fontWeight: 700, letterSpacing: '-.02em', color: T.text }}>{children}</h1>
      {subtitle && <p style={{ fontSize: 15, color: T.muted, marginTop: 7 }}>{subtitle}</p>}
    </div>
  )
}

export function Pill({
  children,
  tone = 'neutral',
}: {
  children: ReactNode
  tone?: 'neutral' | 'accent' | 'success' | 'danger' | 'warn'
}) {
  const tones: Record<string, React.CSSProperties> = {
    neutral: { color: T.muted, background: T.sunken, border: `1px solid ${T.borderSubtle}` },
    accent: { color: T.accent, background: T.accentSoft, border: `1px solid ${T.accentBorder}` },
    success: { color: T.successInk, background: T.successBg, border: `1px solid ${T.successBorder}` },
    danger: { color: T.dangerInk, background: T.dangerBg, border: `1px solid ${T.dangerBorder}` },
    warn: { color: T.warn, background: T.warnBg, border: `1px solid ${T.warnBorder}` },
  }
  return (
    <span
      style={{
        display: 'inline-flex',
        alignItems: 'center',
        gap: 6,
        fontSize: 12,
        fontWeight: 600,
        padding: '3px 9px',
        borderRadius: 20,
        ...tones[tone],
      }}
    >
      {children}
    </span>
  )
}
