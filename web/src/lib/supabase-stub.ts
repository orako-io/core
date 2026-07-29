// Build-time stand-in for @supabase/supabase-js. Vite aliases the real SDK to
// this module in every non-oidc build (dev, local self-host), keeping the
// ~100 KB Supabase client out of the community bundle. In those modes
// VITE_SUPABASE_URL is unset, so `createClient` below is never actually called
// (supabase.ts short-circuits to null) — it only needs to exist for the import
// to resolve. tsc still type-checks against the real package.

export type SupabaseClient = unknown

export function createClient(): never {
  throw new Error(
    'Supabase is not bundled in this build (auth mode is not "oidc"). ' +
      'Rebuild with VITE_ORAKO_AUTH_MODE=oidc to use Supabase auth.',
  )
}
