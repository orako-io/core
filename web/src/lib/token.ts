// Bearer source per auth mode: dev → localStorage stub token
// (memberID:projectID:role), oidc → Supabase session access_token.

import { isOidc } from './auth-mode'
import { currentAccessToken } from './supabase'

const TOKEN_KEY = 'orako:token'

// Fired on dev-token save/clear so the identity provider re-evaluates auth.
export const TOKEN_EVENT = 'orako:token'

function emitTokenChange(): void {
  if (typeof window !== 'undefined') {
    window.dispatchEvent(new Event(TOKEN_EVENT))
  }
}

export interface ParsedToken {
  memberID: string
  projectID: string
  role: 'dev' | 'specialist' | 'lead' | 'admin'
  raw: string
}

export function saveToken(raw: string): void {
  localStorage.setItem(TOKEN_KEY, raw)
  emitTokenChange()
}

export function loadToken(): string | null {
  return localStorage.getItem(TOKEN_KEY)
}

export function clearToken(): void {
  localStorage.removeItem(TOKEN_KEY)
  emitTokenChange()
}

export async function bearer(): Promise<string | null> {
  if (isOidc) return currentAccessToken()
  return loadToken()
}

// Dev-mode only; in oidc the token is an opaque JWT and identity comes from the server.
export function parseToken(raw: string): ParsedToken | null {
  const stripped = raw.startsWith('Bearer ') ? raw.slice(7) : raw
  const parts = stripped.split(':')
  if (parts.length !== 3) return null
  const [memberID, projectID, role] = parts
  if (!memberID || !projectID || !role) return null
  if (!['dev', 'specialist', 'lead', 'admin'].includes(role)) return null
  return { memberID, projectID, role: role as ParsedToken['role'], raw: stripped }
}
