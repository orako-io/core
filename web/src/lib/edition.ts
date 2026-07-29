// Reads the server's resolved edition (GET /api/edition) so the dashboard can
// show the real self-host state — Community caps or a Licensed tier — instead of
// a hardcoded mockup. The edition is fixed at server boot; no auth is required
// (it exposes only the edition label, resource caps, and feature flags).

import { useCallback, useEffect, useState } from 'react'

export type EditionKind = 'community' | 'licensed' | 'saas'

export type EditionInfo = {
  edition: EditionKind
  limits: { maxMembers: number; maxOrgs: number; maxProjects: number }
  features: string[]
  // managed is true when the edition is governed by billing (SaaS): the in-app
  // license activation is then hidden because entitlements are managed remotely.
  managed?: boolean
  // Populated only for a Licensed edition (non-secret license metadata).
  subject?: string
  expiresAt?: string // ISO timestamp; absent = perpetual
  setAt?: string // ISO timestamp the key was set
  setBy?: string // member id who set it (empty for an automatic renewal)
}

export async function fetchEdition(): Promise<EditionInfo> {
  const resp = await fetch('/api/edition', { headers: { Accept: 'application/json' } })
  if (!resp.ok) throw new Error(`edition: ${resp.status}`)
  return (await resp.json()) as EditionInfo
}

// A 0 limit means unlimited (SaaS, or an uncapped axis of a license).
export function limitLabel(n: number): string {
  return n > 0 ? String(n) : 'Unlimited'
}

type CapResource = 'members' | 'orgs' | 'projects'

function limitFor(e: EditionInfo, resource: CapResource): number {
  switch (resource) {
    case 'members':
      return e.limits.maxMembers
    case 'orgs':
      return e.limits.maxOrgs
    case 'projects':
      return e.limits.maxProjects
  }
}

// atCap reports whether an enforced edition has hit (or exceeded) its cap for a
// resource — used to disable create/invite actions before they fail server-side.
// Never true on SaaS or an uncapped (0) axis.
export function atCap(e: EditionInfo | null | undefined, resource: CapResource, used: number): boolean {
  if (!e || e.edition === 'saas') return false
  const limit = limitFor(e, resource)
  return limit > 0 && used >= limit
}

// useEdition fetches the edition and exposes loading/error/data plus a reload()
// so the License page can refresh the status after activating/removing a key
// (the change applies instantly server-side — no restart).
export function useEdition(): {
  data: EditionInfo | null
  loading: boolean
  error: boolean
  reload: () => Promise<void>
} {
  const [data, setData] = useState<EditionInfo | null>(null)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState(false)

  const load = useCallback(async () => {
    setLoading(true)
    setError(false)
    try {
      setData(await fetchEdition())
    } catch {
      setError(true)
    } finally {
      setLoading(false)
    }
  }, [])

  useEffect(() => {
    let alive = true
    fetchEdition()
      .then(e => {
        if (alive) setData(e)
      })
      .catch(() => {
        if (alive) setError(true)
      })
      .finally(() => {
        if (alive) setLoading(false)
      })
    return () => {
      alive = false
    }
  }, [])

  return { data, loading, error, reload: load }
}
