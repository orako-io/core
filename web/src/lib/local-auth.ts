// Self-host email+password login (auth mode 'local'). Calls the server's
// /auth/login (which forges an HS256 session JWT) and stores the token. No
// Supabase — this is the self-host path.

import { BASE_URL } from './client'
import { saveToken } from './token'

// localLogin exchanges email+password for a session token and persists it.
// Throws a user-facing message on failure.
export async function localLogin(email: string, password: string): Promise<void> {
  const resp = await fetch(`${BASE_URL}/auth/login`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ email: email.trim(), password }),
  })

  if (!resp.ok) {
    throw new Error(
      resp.status === 401 ? 'Invalid email or password.' : 'Login failed. Please try again.',
    )
  }

  const data = (await resp.json()) as { token?: string }
  if (!data.token) {
    throw new Error('Login failed: the server returned no token.')
  }

  saveToken(data.token)
}

// acceptInvite sets a password for an invited email (auth mode 'local'). token is
// the invite token from the email link (?token_hash=…). On success the invitee
// can sign in with their email + this password.
export async function acceptInvite(token: string, password: string): Promise<void> {
  const resp = await fetch(`${BASE_URL}/auth/accept-invite`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ token, password }),
  })

  if (!resp.ok) {
    const text = await resp.text().catch(() => '')
    throw new Error(text.trim() || 'Could not accept the invitation. The link may have expired.')
  }
}

// requestPasswordReset asks the server to email a reset link (auth mode 'local').
// The server answers 204 whether or not the email has an account, so this never
// reveals which emails exist — callers show a neutral "check your inbox".
export async function requestPasswordReset(email: string): Promise<void> {
  const resp = await fetch(`${BASE_URL}/auth/forgot-password`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ email: email.trim() }),
  })

  if (!resp.ok) {
    throw new Error('Could not send the reset email. Please try again.')
  }
}

// resetPassword spends a reset token to set a new password (auth mode 'local').
// token is from the email link (?token_hash=…). On success the user signs in with
// their email + this password.
export async function resetPassword(token: string, password: string): Promise<void> {
  const resp = await fetch(`${BASE_URL}/auth/reset-password`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ token, password }),
  })

  if (!resp.ok) {
    const text = await resp.text().catch(() => '')
    throw new Error(text.trim() || 'Could not reset your password. The link may have expired.')
  }
}
