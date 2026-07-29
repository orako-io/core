// SaaS billing overview for CORE UI (trial/upgrade prompts + seat cap). This is
// a PLAIN fetch against GET /billing/overview — it deliberately does NOT import
// the saas overlay's billing.ts (absent in the community build). Returns the
// parsed overview ({status, seats, seatUsage}), or null on 404 / non-SaaS /
// before load, so callers can hide chrome (or a seat cap) without a flash.
// `status` is the raw string ('trialing' | 'active' | 'past_due' | 'canceled');
// `seats` is the purchased seat count (0 = unknown/uncapped), `seatUsage` the
// billable seats currently in use.

import { useEffect, useState } from 'react'
import { bearer } from './token'
import { useIdentity } from './identity'

export type BillingOverview = { status: string; seats: number; seatUsage: number }

export function useBillingOverview(): BillingOverview | null {
  const { authed, selectedProjectId } = useIdentity()
  const [overview, setOverview] = useState<BillingOverview | null>(null)

  useEffect(() => {
    if (!__SAAS__ || !authed) {
      setOverview(null)
      return
    }
    let alive = true
    ;(async () => {
      const token = await bearer()
      const headers: HeadersInit = {}
      if (token) headers['Authorization'] = `Bearer ${token}`
      // Mirror the RPC client: scope to the active org the user switched to.
      const activeOrg =
        typeof localStorage !== 'undefined' ? localStorage.getItem('orako:activeOrg') : null
      if (activeOrg) headers['Orako-Org-Id'] = activeOrg
      try {
        const resp = await fetch('/billing/overview', { headers })
        if (!alive) return
        if (!resp.ok) {
          setOverview(null)
          return
        }
        const body = (await resp.json().catch(() => ({}))) as {
          status?: string
          seats?: number
          seatUsage?: number
        }
        if (!alive) return
        setOverview(
          body.status
            ? { status: body.status, seats: body.seats ?? 0, seatUsage: body.seatUsage ?? 0 }
            : null,
        )
      } catch {
        if (alive) setOverview(null)
      }
    })()
    return () => {
      alive = false
    }
    // selectedProjectId changes on org switch → re-fetch for the new org.
  }, [authed, selectedProjectId])

  return overview
}

// useBillingStatus keeps the original string-only shape for callers that only
// gate chrome on the subscription status (Layout, GetStartedPage).
export function useBillingStatus(): string | null {
  return useBillingOverview()?.status ?? null
}
