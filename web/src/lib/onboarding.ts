// Org-wide onboarding completeness: the same signal GetStartedPage's checklist
// uses (org created + a teammate invited + a first conversation). Once complete,
// the "Get started" nav item and page become the KPI "Dashboard".
//
// Completeness = more than one member AND at least one conversation in the org.
// That mirrors GetStartedPage's required steps (org=always done, team=members>1,
// agent+question=conversations>0), so the nav label and the page branch agree.

import { useEffect, useState } from 'react'
import { api } from './client'
import { useIdentity } from './identity'
import { useRealtime } from './realtime'

// deriveOnboardingComplete is the single definition of "onboarded", shared by
// the nav (Layout) and the page branch (GetStartedPage) so they never diverge.
export function deriveOnboardingComplete(members: number, conversations: number): boolean {
  return members > 1 && conversations > 0
}

// useOnboardingComplete returns null while the signal loads, then a boolean.
// It refetches when a conversation event lands (a first question can flip an
// org from onboarding to complete live, without a reload).
export function useOnboardingComplete(): boolean | null {
  const { authed, accountOnly, selectedProjectId } = useIdentity()
  const [complete, setComplete] = useState<boolean | null>(null)

  const [tick, setTick] = useState(0)
  // A new/closed conversation can change the conversation count; re-derive.
  useRealtime(['conversation_opened', 'conversation_closed'], () => setTick(t => t + 1))

  useEffect(() => {
    // Account-only users (no org yet) are never "complete" — they still need to
    // create an organization on Get started.
    if (!authed || accountOnly || !selectedProjectId) {
      setComplete(accountOnly ? false : null)
      return
    }
    let alive = true
    Promise.all([
      // listExperts is callable by any project member (listMembers is admin-only),
      // and its count is exactly GetStartedPage's "team" signal.
      api.listExperts(selectedProjectId).catch(() => ({ experts: [] })),
      // Org-wide (projectIds: []): a conversation in ANY project counts.
      api.listConversations([]).catch(() => ({ conversations: [] })),
    ])
      .then(([e, c]) => {
        if (!alive) return
        const members = e.experts?.length ?? 0
        const conversations = c.conversations?.length ?? 0
        setComplete(deriveOnboardingComplete(members, conversations))
      })
      .catch(() => {
        if (alive) setComplete(false)
      })
    return () => {
      alive = false
    }
  }, [authed, accountOnly, selectedProjectId, tick])

  return complete
}
