// Set at build time via VITE_ORAKO_AUTH_MODE (the server has no discovery endpoint):
//   oidc  → Supabase login (SaaS)
//   local → self-host email+password (POST /auth/login)
//   dev   → stub token in localStorage (default; local dev only)

export type AuthMode = 'oidc' | 'local' | 'dev'

const raw = import.meta.env.VITE_ORAKO_AUTH_MODE

export const authMode: AuthMode = raw === 'oidc' ? 'oidc' : raw === 'local' ? 'local' : 'dev'

export const isOidc = authMode === 'oidc'
export const isLocal = authMode === 'local'
export const isDev = authMode === 'dev'
