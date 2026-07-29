import { Component, type ErrorInfo, type ReactNode } from 'react'
import { Button } from './Button'
import { LogoTile } from './Icon'
import { T } from '../lib/theme'

interface Props {
  children: ReactNode
}

interface State {
  failed: boolean
}

export class AppErrorBoundary extends Component<Props, State> {
  state: State = { failed: false }

  static getDerivedStateFromError(): State {
    return { failed: true }
  }

  componentDidCatch(error: Error, info: ErrorInfo) {
    console.error('Orako dashboard render failed', error, info)
  }

  render() {
    if (!this.state.failed) return this.props.children

    return (
      <div
        style={{
          minHeight: '100vh',
          background: T.bg,
          display: 'flex',
          alignItems: 'center',
          justifyContent: 'center',
          padding: 24,
        }}
      >
        <div
          style={{
            width: '100%',
            maxWidth: 440,
            padding: 32,
            textAlign: 'center',
            background: T.surface,
            border: `1px solid ${T.border}`,
            borderRadius: 18,
            boxShadow: T.shadowModal,
          }}
        >
          <LogoTile />
          <h1 style={{ margin: '20px 0 0', fontSize: 21, color: T.text }}>The dashboard hit a problem</h1>
          <p style={{ margin: '10px 0 22px', fontSize: 14, lineHeight: 1.6, color: T.muted }}>
            Completed onboarding steps are saved. Reload the latest version of the app to continue.
          </p>
          <Button variant="primary" onClick={() => window.location.reload()}>
            Reload dashboard
          </Button>
        </div>
      </div>
    )
  }
}
