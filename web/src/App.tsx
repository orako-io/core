import { useEffect, Suspense } from 'react'
import { Routes, Route, Navigate, useNavigate, useLocation } from 'react-router-dom'
import { AppErrorBoundary } from './components/AppErrorBoundary'
import { Layout } from './components/Layout'
import { Spinner } from './components/Spinner'
import { lazyWithRetry } from './lib/lazy-with-retry'
import { SaasRoutes } from './lib/saas-routes'
const AuthPage = lazyWithRetry('AuthPage', () => import('./pages/AuthPage').then(m => ({ default: m.AuthPage })))
const AuthCallbackPage = lazyWithRetry('AuthCallbackPage', () => import('./pages/AuthCallbackPage').then(m => ({ default: m.AuthCallbackPage })))
const InvitePage = lazyWithRetry('InvitePage', () => import('./pages/InvitePage').then(m => ({ default: m.InvitePage })))
const JoinPage = lazyWithRetry('JoinPage', () => import('./pages/JoinPage').then(m => ({ default: m.JoinPage })))
const WelcomePage = lazyWithRetry('WelcomePage', () => import('./pages/WelcomePage').then(m => ({ default: m.WelcomePage })))
const GetStartedPage = lazyWithRetry('GetStartedPage', () => import('./pages/GetStartedPage').then(m => ({ default: m.GetStartedPage })))
const MembersPage = lazyWithRetry('MembersPage', () => import('./pages/MembersPage').then(m => ({ default: m.MembersPage })))
const EditPersonPage = lazyWithRetry('EditPersonPage', () => import('./pages/EditPersonPage').then(m => ({ default: m.EditPersonPage })))
const ConversationsPage = lazyWithRetry('ConversationsPage', () => import('./pages/ConversationsPage').then(m => ({ default: m.ConversationsPage })))
const ConversationDetailPage = lazyWithRetry('ConversationDetailPage', () => import('./pages/ConversationDetailPage').then(m => ({ default: m.ConversationDetailPage })))
const HistoryPage = lazyWithRetry('HistoryPage', () => import('./pages/HistoryPage').then(m => ({ default: m.HistoryPage })))
const KnowledgePage = lazyWithRetry('KnowledgePage', () => import('./pages/KnowledgePage').then(m => ({ default: m.KnowledgePage })))
const InboxPage = lazyWithRetry('InboxPage', () => import('./pages/InboxPage').then(m => ({ default: m.InboxPage })))
const IntegrationsPage = lazyWithRetry('IntegrationsPage', () => import('./pages/IntegrationsPage').then(m => ({ default: m.IntegrationsPage })))
const IntegrationsConnectPage = lazyWithRetry('IntegrationsConnectPage', () => import('./pages/IntegrationsConnectPage').then(m => ({ default: m.IntegrationsConnectPage })))
const IntegrationsManagePage = lazyWithRetry('IntegrationsManagePage', () => import('./pages/IntegrationsManagePage').then(m => ({ default: m.IntegrationsManagePage })))
const ConnectAgentPage = lazyWithRetry('ConnectAgentPage', () => import('./pages/ConnectAgentPage').then(m => ({ default: m.ConnectAgentPage })))
const ConnectionsPage = lazyWithRetry('ConnectionsPage', () => import('./pages/ConnectionsPage').then(m => ({ default: m.ConnectionsPage })))
const MachineTokensPage = lazyWithRetry('MachineTokensPage', () => import('./pages/MachineTokensPage').then(m => ({ default: m.MachineTokensPage })))
const AuthorizePage = lazyWithRetry('AuthorizePage', () => import('./pages/AuthorizePage').then(m => ({ default: m.AuthorizePage })))
const OnboardingPage = lazyWithRetry('OnboardingPage', () => import('./pages/OnboardingPage').then(m => ({ default: m.OnboardingPage })))
const OrganizationPage = lazyWithRetry('OrganizationPage', () => import('./pages/OrganizationPage').then(m => ({ default: m.OrganizationPage })))
const ProjectsPage = lazyWithRetry('ProjectsPage', () => import('./pages/ProjectsPage').then(m => ({ default: m.ProjectsPage })))
const ProjectDetailPage = lazyWithRetry('ProjectDetailPage', () => import('./pages/ProjectDetailPage').then(m => ({ default: m.ProjectDetailPage })))
const LicensePage = lazyWithRetry('LicensePage', () => import('./pages/LicensePage').then(m => ({ default: m.LicensePage })))
const AccountPage = lazyWithRetry('AccountPage', () => import('./pages/AccountPage').then(m => ({ default: m.AccountPage })))
const NotificationsPage = lazyWithRetry('NotificationsPage', () => import('./pages/NotificationsPage').then(m => ({ default: m.NotificationsPage })))
import { IdentityProvider, useIdentity } from './lib/identity'
import { RealtimeProvider } from './lib/realtime'
import { ToastProvider } from './lib/toast'
import { markOnboardingRedirect, onboardingRedirectAttempted } from './lib/onboarding-redirect'

const PUBLIC_PATHS = ['/auth', '/auth/callback', '/invite']

// The "redirect once" guard for the onboarding bounce now lives in
// ./lib/onboarding-redirect (module-scoped, not component state: the /welcome,
// /authorize, /onboarding and catch-all routes each mount a distinct
// RequireToken instance, so a per-instance ref would reset on every navigation).
// Once we've sent the member to /onboarding, Skip (or any other navigation) away
// from it must never bounce them back for the rest of this app session, even
// though needsOnboarding can stay true. The /join flow marks it SEEN so a fresh
// joiner lands on the role-adapted Get-started instead of the legacy wizard.

// Session guard: bounce to /auth without a principal, route freshly
// authenticated accounts with no organization yet through /welcome, and send
// a first-run member (empty profile) through /onboarding once. Nothing is
// rendered until the first identity fetch settles, so the dashboard shell never
// flashes placeholder data before redirecting.
function RequireToken({ children }: { children: React.ReactNode }) {
  const navigate = useNavigate()
  const location = useLocation()
  const { authed, loading, accountOnly, needsOnboarding, bootstrapped } = useIdentity()

  const blockedByOnboarding =
    authed && !accountOnly && needsOnboarding && !onboardingRedirectAttempted() && location.pathname !== '/onboarding'

  useEffect(() => {
    if (loading) return
    if (!authed && !PUBLIC_PATHS.includes(location.pathname)) {
      // Preserve the path + query (e.g. /authorize?client_id=…&state=…) so
      // AuthPage can send the browser back here once login completes.
      const returnTo = encodeURIComponent(location.pathname + location.search)
      navigate(`/auth?returnTo=${returnTo}`, { replace: true })
    } else if (authed && accountOnly && location.pathname !== '/welcome') {
      navigate('/welcome', { replace: true })
    } else if (blockedByOnboarding) {
      markOnboardingRedirect()
      navigate('/onboarding', { replace: true })
    }
  }, [authed, loading, accountOnly, blockedByOnboarding, location.pathname, location.search, navigate])

  if (loading && !authed) return null
  if (authed && !bootstrapped) return <BootSplash />
  if (authed && accountOnly && location.pathname !== '/welcome') return <BootSplash />
  if (blockedByOnboarding) return <BootSplash />
  return <>{children}</>
}

function BootSplash() {
  return (
    <div
      style={{
        minHeight: '100vh',
        display: 'flex',
        alignItems: 'center',
        justifyContent: 'center',
        background: '#F7F8FA',
      }}
    >
      <div
        style={{
          width: 44,
          height: 44,
          borderRadius: 13,
          background: '#5850EC',
          display: 'flex',
          alignItems: 'center',
          justifyContent: 'center',
          boxShadow: '0 6px 16px -6px rgba(88,80,236,.7)',
          animation: 'orako-pulse 1.2s ease-in-out infinite',
        }}
      >
        <svg width="22" height="22" viewBox="0 0 24 24" fill="none" stroke="#fff" strokeWidth="2.2" strokeLinecap="round" strokeLinejoin="round">
          <path d="M4 8h13l-4-4" />
          <path d="M20 16H7l4 4" />
        </svg>
      </div>
      <style>{'@keyframes orako-pulse{0%,100%{opacity:1;transform:scale(1)}50%{opacity:.6;transform:scale(.92)}}'}</style>
    </div>
  )
}

// PageFallback is the Suspense fallback for lazily-loaded dashboard pages. It
// renders INSIDE the Layout shell (sidebar + header stay put), so navigating
// between pages shows a small centred spinner in the content area instead of
// flashing the full-screen splash.
function PageFallback() {
  return (
    <div style={{ display: 'flex', justifyContent: 'center', alignItems: 'center', minHeight: '50vh' }}>
      <Spinner />
    </div>
  )
}

// Warm the route chunks during idle time so the first navigation to each page
// has its code already in the browser cache — no Suspense fallback flash.
const PREFETCH = [
  () => import('./pages/ConversationsPage'),
  () => import('./pages/InboxPage'),
  () => import('./pages/MembersPage'),
  () => import('./pages/IntegrationsPage'),
  () => import('./pages/GetStartedPage'),
  () => import('./pages/ConnectAgentPage'),
]
function prefetchRoutesOnIdle() {
  const run = () => PREFETCH.forEach(load => void load().catch(() => {}))
  const ric = (window as unknown as { requestIdleCallback?: (cb: () => void) => void }).requestIdleCallback
  if (ric) ric(run)
  else window.setTimeout(run, 1500)
}

export default function App() {
  useEffect(() => {
    prefetchRoutesOnIdle()
  }, [])

  return (
    <AppErrorBoundary>
      <IdentityProvider>
        <RealtimeProvider>
        <ToastProvider>
          <Suspense fallback={<BootSplash />}>
          <Routes>
          <Route path="/auth" element={<AuthPage />} />
          <Route path="/auth/callback" element={<AuthCallbackPage />} />
          <Route path="/invite" element={<InvitePage />} />
          {/* Standalone, NOT behind RequireToken/Layout: a brand-new invited
              person with no membership must reach it. The page manages its own
              auth and self-serve join redemption. */}
          <Route path="/join/:code" element={<JoinPage />} />
          <Route
            path="/welcome"
            element={
              <RequireToken>
                <WelcomePage />
              </RequireToken>
            }
          />
          {/* No Layout chrome: the OAuth AS's browser hand-off for remote MCP
              clients (Claude Code, Cursor, and other MCP clients), not a
              dashboard screen. */}
          <Route
            path="/authorize"
            element={
              <RequireToken>
                <AuthorizePage />
              </RequireToken>
            }
          />
          {/* No Layout chrome: full-viewport first-run wizard, like /welcome. */}
          <Route
            path="/onboarding"
            element={
              <RequireToken>
                <OnboardingPage />
              </RequireToken>
            }
          />
          <Route
            path="/*"
            element={
              <RequireToken>
                <Layout>
                  <Suspense fallback={<PageFallback />}>
                  <Routes>
                    <Route path="/get-started" element={<GetStartedPage />} />
                    <Route path="/conversations" element={<ConversationsPage />} />
                    <Route path="/conversations/:id" element={<ConversationDetailPage />} />
                    <Route path="/history" element={<HistoryPage />} />
                    <Route path="/knowledge" element={<KnowledgePage />} />
                    <Route path="/inbox" element={<InboxPage />} />
                    <Route path="/inbox/:id" element={<InboxPage />} />
                    <Route path="/members" element={<MembersPage />} />
                    <Route path="/members/:id" element={<EditPersonPage />} />
                    <Route path="/integrations" element={<IntegrationsPage />} />
                    <Route path="/integrations/connect/:kind" element={<IntegrationsConnectPage />} />
                    <Route path="/integrations/manage/:kind" element={<IntegrationsManagePage />} />
                    <Route path="/connect-agent" element={<ConnectAgentPage />} />
                    <Route path="/connections" element={<ConnectionsPage />} />
                    <Route path="/machine-tokens" element={<MachineTokensPage />} />
                    <Route path="/organization" element={<OrganizationPage />} />
                    <Route path="/projects" element={<ProjectsPage />} />
                    <Route path="/projects/:id" element={<ProjectDetailPage />} />
                    {SaasRoutes()}
                    <Route path="/license" element={<LicensePage />} />
                    <Route path="/account" element={<AccountPage />} />
                    <Route path="/notifications" element={<NotificationsPage />} />
                    {/* Legacy paths */}
                    <Route path="/slack" element={<Navigate to="/integrations" replace />} />
                    <Route path="/provider" element={<Navigate to="/integrations" replace />} />
                    <Route path="/mcp" element={<Navigate to="/connect-agent" replace />} />
                    <Route path="/contact-channel" element={<Navigate to="/notifications" replace />} />
                    <Route path="*" element={<Navigate to="/get-started" replace />} />
                  </Routes>
                  </Suspense>
                </Layout>
              </RequireToken>
            }
          />
          </Routes>
          </Suspense>
        </ToastProvider>
        </RealtimeProvider>
      </IdentityProvider>
    </AppErrorBoundary>
  )
}
