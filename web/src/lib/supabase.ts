// Supabase browser client (oidc mode). Null when the env vars are absent —
// callers must guard on it.

import { createClient, type SupabaseClient } from '@supabase/supabase-js'

const url = import.meta.env.VITE_SUPABASE_URL
const publishableKey = import.meta.env.VITE_SUPABASE_PUBLISHABLE_KEY

export const supabase: SupabaseClient | null =
  url && publishableKey
    ? createClient(url, publishableKey, {
        auth: { persistSession: true, autoRefreshToken: true },
      })
    : null

// requireSupabase returns the client or throws a clear error when the oidc-mode
// env is missing — surfaced in the login UI rather than a cryptic runtime crash.
export function requireSupabase(): SupabaseClient {
  if (!supabase) {
    throw new Error(
      'Supabase is not configured. Set VITE_SUPABASE_URL and VITE_SUPABASE_PUBLISHABLE_KEY in web/.env.',
    )
  }
  return supabase
}

// Current session access_token, or null when signed out / not configured.
export async function currentAccessToken(): Promise<string | null> {
  if (!supabase) return null
  const { data } = await supabase.auth.getSession()
  return data.session?.access_token ?? null
}
