// CapNudge — the in-product upgrade prompt shown when a self-host instance hits a
// Community cap (cal.com-style). Self-gating: it fetches the real edition and
// renders nothing on SaaS, on an uncapped axis, or below the limit. On-prem only.

import { useNavigate } from 'react-router-dom'
import { useEdition } from '../lib/edition'
import { T } from '../lib/theme'

// Where "Upgrade" sends a self-hoster: Orako's hosted self-host purchase page.
export const UPGRADE_URL = 'https://app.orako.io/billing/self-host'

type Resource = 'projects' | 'members'

const COPY: Record<Resource, { noun: string; unlock: string }> = {
  projects: { noun: 'projects', unlock: 'unlimited projects' },
  members: { noun: 'members', unlock: 'more seats' },
}

export function CapNudge({ resource, used }: { resource: Resource; used: number }) {
  const navigate = useNavigate()
  const { data } = useEdition()

  // SaaS is governed by billing, not caps; an uncapped (0) axis or being under
  // the limit means nothing to nudge about.
  if (!data || data.edition === 'saas') return null

  const limit = resource === 'projects' ? data.limits.maxProjects : data.limits.maxMembers
  if (limit <= 0 || used < limit) return null

  const { noun, unlock } = COPY[resource]

  return (
    <div
      style={{
        display: 'flex',
        alignItems: 'center',
        justifyContent: 'space-between',
        gap: 16,
        flexWrap: 'wrap',
        background: T.accentSofter,
        border: `1px solid ${T.accentBorder}`,
        borderRadius: T.rLg,
        padding: '14px 18px',
        marginBottom: 16,
      }}
    >
      <div>
        <div style={{ fontSize: 14, fontWeight: 700, color: T.accentInk }}>
          You've reached the Community limit
        </div>
        <div style={{ fontSize: 13, color: T.subtle, marginTop: 2 }}>
          {used} of {limit} {noun} used. Upgrade to an Orako license for {unlock}.
        </div>
      </div>
      <div style={{ display: 'flex', gap: 10, flexShrink: 0 }}>
        <button
          onClick={() => navigate('/license')}
          style={{
            height: 38,
            padding: '0 14px',
            border: `1px solid ${T.border}`,
            borderRadius: T.rMd,
            background: T.surface,
            color: T.body,
            fontSize: 13.5,
            fontWeight: 600,
            cursor: 'pointer',
            fontFamily: 'inherit',
          }}
        >
          Have a key?
        </button>
        <a
          href={UPGRADE_URL}
          target="_blank"
          rel="noreferrer"
          style={{
            display: 'inline-flex',
            alignItems: 'center',
            height: 38,
            padding: '0 16px',
            borderRadius: T.rMd,
            background: T.accent,
            color: '#fff',
            fontSize: 13.5,
            fontWeight: 600,
            textDecoration: 'none',
          }}
        >
          Upgrade
        </a>
      </div>
    </div>
  )
}
