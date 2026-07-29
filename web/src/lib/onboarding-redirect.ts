// Module-scoped "redirect once" guard for the first-run /onboarding wizard.
//
// RequireToken bounces a member with an empty profile to the /onboarding wizard
// exactly once per app session. This lived as a bare module var in App.tsx; it
// is hoisted here so other entry points (notably the /join flow) can mark it
// SEEN and thereby route a freshly-joined member straight into the role-adapted
// Get-started (item 5) instead of the legacy wizard, without an import cycle.
//
// A full page reload resets it (the heuristic re-evaluates fresh), which is
// intended — the durable, permanent signal is the server-side onboarding
// dismissal, not this session guard.

let attempted = false

// onboardingRedirectAttempted reports whether the one-time wizard bounce has
// already been decided this session.
export function onboardingRedirectAttempted(): boolean {
  return attempted
}

// markOnboardingRedirect records that the wizard bounce has been handled (or is
// being deliberately skipped), so RequireToken never forces it again this session.
export function markOnboardingRedirect(): void {
  attempted = true
}
