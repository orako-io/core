// Slim dismissible reminder shown while the org has no external messaging
// provider (Slack/Discord/Teams): questions only reach teammates in the dashboard.

import { useEffect, useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { api } from '../lib/client'
import { Icon } from './Icon'
import { T } from '../lib/theme'

const DISMISS_KEY = 'orako:provider-nudge-dismissed'

export function ProviderNudge() {
  const navigate = useNavigate()
  const [show, setShow] = useState(false)

  useEffect(() => {
    if (localStorage.getItem(DISMISS_KEY)) return
    let cancelled = false
    api
      .listConnectedChannels()
      .then(res => {
        const external = (res.channels ?? []).filter(c => c !== 'dashboard')
        if (!cancelled && external.length === 0) setShow(true)
      })
      .catch(() => {})
    return () => {
      cancelled = true
    }
  }, [])

  if (!show) return null

  return (
    <div
      style={{
        display: 'flex',
        alignItems: 'center',
        gap: 10,
        background: T.surfaceAlt,
        border: `1px solid ${T.border}`,
        borderRadius: 10,
        padding: '9px 13px',
        marginBottom: 18,
        fontSize: 13,
        color: T.muted,
      }}
    >
      <Icon name="bell" size={15} color={T.subtle} />
      <span style={{ flex: 1, minWidth: 0 }}>
        No messaging app connected — teammates are only pinged in their dashboard Inbox.
      </span>
      <span
        onClick={() => navigate('/integrations')}
        style={{ fontWeight: 600, color: T.accent, cursor: 'pointer', whiteSpace: 'nowrap' }}
      >
        Connect a provider →
      </span>
      <button
        onClick={() => {
          localStorage.setItem(DISMISS_KEY, '1')
          setShow(false)
        }}
        aria-label="Dismiss"
        style={{
          border: 'none',
          background: 'transparent',
          color: T.faint,
          cursor: 'pointer',
          display: 'inline-flex',
          padding: 2,
        }}
      >
        <Icon name="x" size={14} />
      </button>
    </div>
  )
}
