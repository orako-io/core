// License (self-hosted) — replaces Billing in on-prem mode. Reflects the server's
// real resolved edition (GET /api/edition): the free Community caps, or a Licensed
// tier's limits + features. The license key is set IN-APP here — pasted into the
// dashboard and stored in the DB — and applies instantly (no restart, no env var).
// POST /api/license activates/replaces it (verified offline), DELETE clears it.

import { useState } from 'react'
import { Page } from '../components/Layout'
import { Icon } from '../components/Icon'
import { SettingsCard, CardHeader, Divider } from '../components/Settings'
import { useToast, toastMessage } from '../lib/toast'
import { T } from '../lib/theme'
import { useEdition, limitLabel, type EditionInfo } from '../lib/edition'
import { setLicense, clearLicense } from '../lib/client'
import { UPGRADE_URL } from '../components/UpgradeCard'

const label: React.CSSProperties = {
  fontSize: 12.5,
  fontWeight: 600,
  color: T.faint,
  textTransform: 'uppercase',
  letterSpacing: '.04em',
  fontFamily: T.mono,
}

// fmtDate renders an ISO timestamp as a short local date; empty for a bad/missing
// value (a perpetual license has no expiry).
function fmtDate(iso?: string): string {
  if (!iso) return ''
  const d = new Date(iso)
  return Number.isNaN(d.getTime()) ? '' : d.toLocaleDateString(undefined, { year: 'numeric', month: 'short', day: 'numeric' })
}

function StatCard({ title, value, hint }: { title: string; value: string; hint?: string }) {
  return (
    <SettingsCard style={{ padding: 20 }}>
      <div style={label}>{title}</div>
      <div style={{ fontSize: 20, fontWeight: 700, color: T.text, marginTop: 10 }}>{value}</div>
      {hint && <div style={{ fontSize: 12.5, color: T.subtle, marginTop: 4 }}>{hint}</div>}
    </SettingsCard>
  )
}

function Banner({ ok, title, subtitle }: { ok: boolean; title: string; subtitle: string }) {
  return (
    <div
      style={{
        display: 'flex',
        alignItems: 'center',
        gap: 12,
        background: ok ? T.successBg : T.accentSofter,
        border: `1px solid ${ok ? T.successBorder : T.accentBorder}`,
        borderRadius: T.rLg,
        padding: '14px 18px',
      }}
    >
      <Icon name={ok ? 'shield' : 'lock'} size={20} strokeWidth={2} color={ok ? T.success : T.accent} />
      <div>
        <div style={{ fontSize: 14.5, fontWeight: 700, color: ok ? T.successInk : T.accentInk }}>{title}</div>
        <div style={{ fontSize: 13, color: T.subtle }}>{subtitle}</div>
      </div>
    </div>
  )
}

function LimitsGrid({ e }: { e: EditionInfo }) {
  return (
    <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr 1fr', gap: 16 }}>
      <StatCard title="Members / org" value={limitLabel(e.limits.maxMembers)} />
      <StatCard title="Organizations" value={limitLabel(e.limits.maxOrgs)} />
      <StatCard title="Projects / org" value={limitLabel(e.limits.maxProjects)} />
    </div>
  )
}

// KeyForm is the paste-a-key panel shared by the Community "activate" card and the
// Licensed "replace" flow. On success it refetches the edition (Community →
// Licensed, or a new expiry) — the change applies instantly, no restart.
function KeyForm({ cta, onDone, onCancel }: { cta: string; onDone: () => Promise<void>; onCancel?: () => void }) {
  const toast = useToast()
  const [key, setKey] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')

  async function activate() {
    const trimmed = key.trim()
    if (!trimmed || busy) return
    setBusy(true)
    setError('')
    try {
      await setLicense(trimmed)
      toast.success('License activated')
      setKey('')
      await onDone()
      onCancel?.()
    } catch (err) {
      setError(toastMessage(err))
    } finally {
      setBusy(false)
    }
  }

  return (
    <div>
      <textarea
        value={key}
        onChange={e => setKey(e.target.value)}
        placeholder="Paste your license key…"
        spellCheck={false}
        rows={4}
        style={{
          width: '100%',
          resize: 'vertical',
          boxSizing: 'border-box',
          background: '#12151C',
          color: '#E6E8EC',
          border: `1px solid ${error ? T.dangerBorder : '#2A2F3A'}`,
          borderRadius: T.rMd,
          padding: '12px 14px',
          fontFamily: T.mono,
          fontSize: 12.5,
          lineHeight: 1.5,
          outline: 'none',
        }}
      />
      {error && (
        <div style={{ display: 'flex', alignItems: 'center', gap: 7, marginTop: 10, color: T.dangerInk, fontSize: 13 }}>
          <Icon name="alertTriangle" size={15} strokeWidth={2} color={T.danger} />
          {error}
        </div>
      )}
      <div style={{ display: 'flex', gap: 10, marginTop: 14 }}>
        <button
          type="button"
          onClick={activate}
          disabled={busy || key.trim() === ''}
          style={{
            display: 'inline-flex',
            alignItems: 'center',
            gap: 8,
            height: 42,
            padding: '0 20px',
            borderRadius: T.rMd,
            border: 'none',
            background: T.accent,
            color: '#fff',
            fontSize: 14,
            fontWeight: 600,
            cursor: busy || key.trim() === '' ? 'default' : 'pointer',
            opacity: busy || key.trim() === '' ? 0.6 : 1,
          }}
        >
          <Icon name="lock" size={16} strokeWidth={2} color="#fff" />
          {busy ? 'Activating…' : cta}
        </button>
        {onCancel && (
          <button
            type="button"
            onClick={onCancel}
            disabled={busy}
            style={{
              height: 42,
              padding: '0 18px',
              borderRadius: T.rMd,
              border: `1px solid ${T.border}`,
              background: T.surface,
              color: T.body,
              fontSize: 14,
              fontWeight: 600,
              cursor: 'pointer',
            }}
          >
            Cancel
          </button>
        )}
      </div>
      <div style={{ fontSize: 12.5, color: T.subtle, marginTop: 12, lineHeight: 1.6 }}>
        The signing public key ships baked into Orako — this key is the only value you paste. Verified offline, no
        phone-home. It applies instantly; no server restart.
      </div>
    </div>
  )
}

function FeatureTags({ features }: { features: string[] }) {
  if (features.length === 0) return null
  return (
    <div style={{ marginTop: 16 }}>
      <div style={label}>Features</div>
      <div style={{ display: 'flex', flexWrap: 'wrap', gap: 8, marginTop: 10 }}>
        {features.map(f => (
          <span
            key={f}
            style={{
              fontSize: 12.5,
              fontWeight: 600,
              color: T.accentInk,
              background: T.accentSofter,
              border: `1px solid ${T.accentBorder}`,
              borderRadius: T.rSm,
              padding: '3px 10px',
            }}
          >
            {f}
          </span>
        ))}
      </div>
    </div>
  )
}

function CommunityView({ e, reload }: { e: EditionInfo; reload: () => Promise<void> }) {
  return (
    <>
      <Banner
        ok={false}
        title="Community edition"
        subtitle="Free self-host. No license key set — running with the community caps below."
      />
      <LimitsGrid e={e} />
      <SettingsCard style={{ padding: 24 }}>
        <CardHeader icon="lock" title="Activate a license" sub="Paste a signed license key to lift the community caps." />
        <Divider />
        <KeyForm cta="Activate" onDone={reload} />
        <div style={{ fontSize: 13, color: T.subtle, marginTop: 16, lineHeight: 1.6 }}>
          Don’t have one yet?{' '}
          <a href={UPGRADE_URL} target="_blank" rel="noreferrer" style={{ color: T.accent, fontWeight: 600, textDecoration: 'none' }}>
            Buy a license
          </a>{' '}
          — your key is emailed instantly. Questions?{' '}
          <a href="mailto:sales@orako.io" style={{ color: T.accent, fontWeight: 600, textDecoration: 'none' }}>
            Contact sales
          </a>
          .
        </div>
      </SettingsCard>
    </>
  )
}

function LicensedView({ e, reload }: { e: EditionInfo; reload: () => Promise<void> }) {
  const toast = useToast()
  const [replacing, setReplacing] = useState(false)
  const [removing, setRemoving] = useState(false)
  const expiry = fmtDate(e.expiresAt)
  const setAt = fmtDate(e.setAt)

  async function remove() {
    if (removing) return
    if (!window.confirm('Remove the license key? The server reverts to the free community caps immediately.')) return
    setRemoving(true)
    try {
      await clearLicense()
      toast.success('License removed')
      await reload()
    } catch (err) {
      toast.error(toastMessage(err))
    } finally {
      setRemoving(false)
    }
  }

  return (
    <>
      <Banner ok title="License active" subtitle="On-premise deployment · limits come from your signed license key." />
      <LimitsGrid e={e} />
      <SettingsCard style={{ padding: 24 }}>
        <CardHeader icon="shield" title="License" sub={e.subject ? `Issued to ${e.subject}` : undefined} />
        <Divider />
        <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 18 }}>
          <div>
            <div style={label}>Expires</div>
            <div style={{ fontSize: 14, fontWeight: 600, color: T.text, marginTop: 8 }}>{expiry || 'Never (perpetual)'}</div>
          </div>
          {setAt && (
            <div>
              <div style={label}>Set</div>
              <div style={{ fontSize: 14, fontWeight: 600, color: T.text, marginTop: 8 }}>{setAt}</div>
            </div>
          )}
        </div>
        <FeatureTags features={e.features} />
        <Divider />
        {replacing ? (
          <KeyForm cta="Replace key" onDone={reload} onCancel={() => setReplacing(false)} />
        ) : (
          <div style={{ display: 'flex', gap: 10, flexWrap: 'wrap' }}>
            <button
              type="button"
              onClick={() => setReplacing(true)}
              style={{
                display: 'inline-flex',
                alignItems: 'center',
                gap: 8,
                height: 40,
                padding: '0 18px',
                borderRadius: T.rMd,
                border: `1px solid ${T.border}`,
                background: T.surface,
                color: T.body,
                fontSize: 14,
                fontWeight: 600,
                cursor: 'pointer',
              }}
            >
              <Icon name="refresh" size={15} strokeWidth={2} color={T.body} />
              Replace key
            </button>
            <button
              type="button"
              onClick={remove}
              disabled={removing}
              style={{
                display: 'inline-flex',
                alignItems: 'center',
                gap: 8,
                height: 40,
                padding: '0 18px',
                borderRadius: T.rMd,
                border: `1px solid ${T.dangerBorder}`,
                background: T.dangerBg,
                color: T.dangerInk,
                fontSize: 14,
                fontWeight: 600,
                cursor: removing ? 'default' : 'pointer',
                opacity: removing ? 0.6 : 1,
              }}
            >
              <Icon name="trash" size={15} strokeWidth={2} color={T.dangerInk} />
              {removing ? 'Removing…' : 'Remove license'}
            </button>
          </div>
        )}
      </SettingsCard>
    </>
  )
}

// ManagedView: under SaaS the edition is governed by billing, not a pasted key —
// the activation surface is hidden and the page points at billing.
function ManagedView() {
  return (
    <SettingsCard style={{ padding: 24 }}>
      <CardHeader icon="card" title="Managed by billing" sub="Your plan and seats are governed by your subscription, not a license key." />
    </SettingsCard>
  )
}

export function LicensePage() {
  const { data, loading, error, reload } = useEdition()

  return (
    <Page width={720}>
      <div style={{ display: 'flex', flexDirection: 'column', gap: 16 }}>
        {loading && <div style={{ fontSize: 14, color: T.subtle }}>Loading license…</div>}
        {error && !loading && (
          <Banner ok={false} title="License status unavailable" subtitle="Could not reach the server. Try reloading." />
        )}
        {data && (data.edition === 'saas' || data.managed) && <ManagedView />}
        {data && data.edition === 'licensed' && <LicensedView e={data} reload={reload} />}
        {data && data.edition === 'community' && <CommunityView e={data} reload={reload} />}
      </div>
    </Page>
  )
}
