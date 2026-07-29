// Integrations home (mockup 1a empty / 1b populated): the "Your connections"
// gallery of already-configured providers, plus "Add a provider" for the rest.
// Detail flows (connect stepper, manage view) live at their own routes —
// this page only lists state and links out to them.

import { useCallback, useEffect, useRef, useState } from 'react'
import { useNavigate } from 'react-router'
import { useIdentity } from '../lib/identity'
import { useToast, toastMessage } from '../lib/toast'
import { api, type ConnectedChannel } from '../lib/client'
import { cacheGet, cacheSet } from '../lib/queryCache'
import { Page } from '../components/Layout'
import { Button } from '../components/Button'
import { Modal } from '../components/Modal'
import { Spinner } from '../components/Spinner'
import { ErrorBanner } from '../components/ErrorBanner'
import { Icon } from '../components/Icon'
import { ProviderLogo, providerTileBg } from '../components/ProviderLogos'
import { PROVIDERS, PROVIDER_ORDER, type ProviderKind } from '../lib/providers'
import { T } from '../lib/theme'

const cardStyle: React.CSSProperties = {
  background: T.surface,
  border: `1px solid ${T.border}`,
  borderRadius: 14,
  padding: '16px 20px',
  display: 'flex',
  alignItems: 'center',
  gap: 16,
}

const tileStyle = (bg: string): React.CSSProperties => ({
  width: 44,
  height: 44,
  borderRadius: 12,
  background: bg,
  display: 'flex',
  alignItems: 'center',
  justifyContent: 'center',
  flex: 'none',
})

function ConnectedPill() {
  return (
    <span
      style={{
        display: 'inline-flex',
        alignItems: 'center',
        gap: 5,
        fontSize: 11.5,
        fontWeight: 600,
        color: T.successInk,
        background: T.successBg,
        border: `1px solid ${T.successBorder}`,
        padding: '2px 9px',
        borderRadius: 20,
      }}
    >
      <span style={{ width: 6, height: 6, borderRadius: '50%', background: T.success }} />
      Connected
    </span>
  )
}

function KebabMenu({ onDisconnect }: { onDisconnect: () => void }) {
  const [open, setOpen] = useState(false)
  const ref = useRef<HTMLDivElement>(null)

  useEffect(() => {
    if (!open) return
    function onDown(e: MouseEvent) {
      if (!ref.current?.contains(e.target as Node)) setOpen(false)
    }
    document.addEventListener('mousedown', onDown)
    return () => document.removeEventListener('mousedown', onDown)
  }, [open])

  return (
    <div ref={ref} style={{ position: 'relative', flex: 'none' }}>
      <button
        onClick={() => setOpen(o => !o)}
        aria-label="More actions"
        style={{
          width: 38,
          height: 38,
          border: `1px solid ${T.borderStrong}`,
          borderRadius: 9,
          background: T.surface,
          display: 'flex',
          alignItems: 'center',
          justifyContent: 'center',
          cursor: 'pointer',
        }}
      >
        <Icon name="dots" size={17} color={T.subtle} />
      </button>
      {open && (
        <div
          style={{
            position: 'absolute',
            right: 0,
            top: 44,
            minWidth: 160,
            background: T.surface,
            border: `1px solid ${T.border}`,
            borderRadius: T.rLg,
            boxShadow: T.shadowModal,
            padding: 6,
            zIndex: 20,
          }}
        >
          <button
            onClick={() => {
              setOpen(false)
              onDisconnect()
            }}
            style={{
              display: 'flex',
              alignItems: 'center',
              gap: 9,
              width: '100%',
              padding: '8px 10px',
              borderRadius: 8,
              border: 'none',
              background: 'transparent',
              fontSize: 13.5,
              fontWeight: 500,
              color: T.dangerInk,
              cursor: 'pointer',
              fontFamily: 'inherit',
              textAlign: 'left',
            }}
          >
            <Icon name="trash" size={15} color={T.dangerInk} />
            Disconnect
          </button>
        </div>
      )}
    </div>
  )
}

export function IntegrationsPage() {
  const { selectedProjectId: projectId } = useIdentity()
  const navigate = useNavigate()
  const toast = useToast()

  // Stale-while-revalidate: paint the last cached integrations instantly on
  // return, revalidate in the background.
  const [connected, setConnected] = useState<ConnectedChannel[]>(() => cacheGet<ConnectedChannel[]>('integrations') ?? [])
  const [loading, setLoading] = useState(cacheGet('integrations') === undefined)
  const [error, setError] = useState<unknown>(null)
  // Which provider a disconnect is pending confirmation for (null = no modal).
  // Held in state so the confirm modal opens instantly on click.
  const [confirmKind, setConfirmKind] = useState<ProviderKind | null>(null)
  const [disconnecting, setDisconnecting] = useState(false)

  const fetchChannels = useCallback(async () => {
    setError(null)
    try {
      const res = await api.listConnectedChannels()
      setConnected(res.connected ?? [])
      cacheSet('integrations', res.connected ?? [])
    } catch (e) {
      setError(e)
    } finally {
      setLoading(false)
    }
  }, [])

  useEffect(() => {
    void fetchChannels()
  }, [fetchChannels])

  async function handleDisconnect(kind: ProviderKind) {
    setDisconnecting(true)
    try {
      await api.disconnectProvider(projectId, kind)
      toast.success(`${PROVIDERS[kind].label} disconnected.`)
      await fetchChannels()
      setConfirmKind(null)
    } catch (e) {
      toast.error(toastMessage(e))
    } finally {
      setDisconnecting(false)
    }
  }

  return (
    <Page width={820}>
      <h1 style={{ fontSize: 24, fontWeight: 700, letterSpacing: '-.02em', color: T.text, margin: 0 }}>
        Integrations
      </h1>
      <p style={{ fontSize: 14.5, lineHeight: 1.6, color: T.muted, marginTop: 6 }}>
        Where Orako reaches your teammates. Manage a connection, or add another.
      </p>

      {error != null && (
        <div style={{ marginTop: 22 }}>
          <ErrorBanner error={error} />
        </div>
      )}

      {loading ? (
        <div style={{ display: 'flex', justifyContent: 'center', padding: 40 }}>
          <Spinner />
        </div>
      ) : (
        <>
          {connected.length > 0 && (
            <>
              <div
                style={{
                  display: 'flex',
                  alignItems: 'baseline',
                  justifyContent: 'space-between',
                  marginTop: 28,
                }}
              >
                <h4
                  style={{
                    fontSize: 13,
                    fontWeight: 700,
                    letterSpacing: '.05em',
                    textTransform: 'uppercase',
                    color: T.subtle,
                    margin: 0,
                  }}
                >
                  Your connections
                </h4>
                <span style={{ fontSize: 12.5, color: T.faint }}>{connected.length}</span>
              </div>
              <div style={{ display: 'flex', flexDirection: 'column', gap: 12, marginTop: 14 }}>
                {connected.map(c => {
                  const kind = c.kind as ProviderKind
                  const def = PROVIDERS[kind]
                  const label = def?.label ?? c.kind
                  return (
                    <div key={c.kind} style={cardStyle}>
                      <div style={tileStyle(providerTileBg(kind))}>
                        <ProviderLogo kind={kind} size={24} />
                      </div>
                      <div style={{ flex: 1, minWidth: 0 }}>
                        <div style={{ display: 'flex', alignItems: 'center', gap: 9 }}>
                          <span style={{ fontSize: 15.5, fontWeight: 700, color: T.text }}>{label}</span>
                          <ConnectedPill />
                        </div>
                        <div style={{ fontSize: 13, color: T.subtle, marginTop: 3 }}>
                          {(c.alertChannelIds ?? []).length} alert channels · connected
                        </div>
                      </div>
                      <Button
                        variant="secondary"
                        style={{ height: 38 }}
                        onClick={() => navigate(`/integrations/manage/${kind}`)}
                      >
                        Manage
                      </Button>
                      <KebabMenu onDisconnect={() => setConfirmKind(kind)} />
                    </div>
                  )
                })}
              </div>
            </>
          )}

          <div style={{ display: 'flex', alignItems: 'baseline', justifyContent: 'space-between', marginTop: 30 }}>
            <h4
              style={{
                fontSize: 13,
                fontWeight: 700,
                letterSpacing: '.05em',
                textTransform: 'uppercase',
                color: T.subtle,
                margin: 0,
              }}
            >
              Add a provider
            </h4>
          </div>
          <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 16, marginTop: 14 }}>
            {PROVIDER_ORDER.map(kind => {
              const def = PROVIDERS[kind]
              const isConnected = connected.some(c => c.kind === kind)
              return (
                <div key={kind} style={{ ...cardStyle, opacity: isConnected || def.comingSoon ? 0.55 : 1 }}>
                  <div style={tileStyle(providerTileBg(kind))}>
                    <ProviderLogo kind={kind} size={24} />
                  </div>
                  <div style={{ flex: 1, minWidth: 0 }}>
                    <div style={{ fontSize: 15.5, fontWeight: 700, color: T.text }}>{def.label}</div>
                    {def.comingSoon && !isConnected ? (
                      <div style={{ fontSize: 12, color: T.faint, marginTop: 4 }}>Not available yet</div>
                    ) : isConnected ? (
                      <div style={{ fontSize: 12, color: T.faint, marginTop: 4 }}>Already connected</div>
                    ) : (
                      <div style={{ display: 'flex', alignItems: 'center', gap: 6, marginTop: 4 }}>
                        <span
                          style={{
                            display: 'inline-flex',
                            alignItems: 'center',
                            gap: 4,
                            fontSize: 11.5,
                            fontWeight: 600,
                            color: T.warn,
                            background: T.warnBg,
                            borderRadius: 20,
                            padding: '2px 8px',
                          }}
                        >
                          {def.timeLabel}
                        </span>
                        <span style={{ fontSize: 12, color: T.faint }}>{def.who}</span>
                      </div>
                    )}
                  </div>
                  {isConnected ? (
                    <span
                      style={{
                        display: 'inline-flex',
                        alignItems: 'center',
                        gap: 5,
                        fontSize: 12,
                        fontWeight: 600,
                        color: T.successInk,
                        flex: 'none',
                      }}
                    >
                      <Icon name="check" size={15} color={T.success} strokeWidth={2.6} />
                      Added
                    </span>
                  ) : def.comingSoon ? (
                    <span
                      style={{
                        display: 'inline-flex',
                        alignItems: 'center',
                        fontSize: 12,
                        fontWeight: 600,
                        color: T.subtle,
                        background: T.sunken,
                        borderRadius: 20,
                        padding: '4px 12px',
                        flex: 'none',
                      }}
                    >
                      Coming soon
                    </span>
                  ) : (
                    <Button
                      variant="secondary"
                      style={{ height: 38 }}
                      onClick={() => navigate(`/integrations/connect/${kind}`)}
                    >
                      Connect
                    </Button>
                  )}
                </div>
              )
            })}
          </div>
        </>
      )}

      <Modal
        open={confirmKind !== null}
        onClose={() => { if (!disconnecting) setConfirmKind(null) }}
        title={confirmKind ? `Disconnect ${PROVIDERS[confirmKind].label}?` : ''}
      >
        <p style={{ fontSize: 13.5, color: T.body, lineHeight: 1.55, margin: 0 }}>
          Teammates will stop receiving questions on {confirmKind ? PROVIDERS[confirmKind].label : 'this provider'}.
          You can reconnect it later.
        </p>
        <div style={{ display: 'flex', justifyContent: 'flex-end', gap: 10, marginTop: 20 }}>
          <Button variant="secondary" disabled={disconnecting} onClick={() => setConfirmKind(null)}>Cancel</Button>
          <Button variant="danger" loading={disconnecting} onClick={() => confirmKind && void handleDisconnect(confirmKind)}>
            Disconnect
          </Button>
        </div>
      </Modal>
    </Page>
  )
}
