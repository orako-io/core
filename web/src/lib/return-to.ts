// Shared guard for the `returnTo` query param threaded through every login
// path (email/password, dev-stub token, Google OAuth). Only ever navigate to
// a same-app relative path — never let a query param drive an off-site or
// protocol-relative redirect (reject `//…`, which browsers treat as
// scheme-relative).
export function safeReturnTo(raw: string | null): string {
  if (!raw || !raw.startsWith('/') || raw.startsWith('//')) return '/get-started'
  return raw
}
