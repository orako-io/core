// Brand SVG logos for the messaging providers, shared by the Integrations
// gallery, the connect stepper, and the manage view. Multi-color marks — not
// the single-color Icon set.

export function SlackLogo({ size = 24 }: { size?: number }) {
  return (
    <svg width={size} height={size} viewBox="0 0 24 24">
      <path fill="#36C5F0" d="M9 6a2 2 0 1 1 2 2H9z" />
      <path fill="#2EB67D" d="M18 9a2 2 0 1 1 2 2h-2zm-1 0a2 2 0 0 1-4 0V4a2 2 0 1 1 4 0z" />
      <path fill="#ECB22E" d="M15 18a2 2 0 1 1-2-2h2zm0-1a2 2 0 0 1 0-4h5a2 2 0 1 1 0 4z" />
      <path fill="#E01E5A" d="M6 15a2 2 0 1 1-2-2h2zm1 0a2 2 0 0 1 4 0v5a2 2 0 1 1-4 0z" />
      <path fill="#36C5F0" d="M9 7a2 2 0 0 1 0-4H4a2 2 0 1 0 0 4z" />
    </svg>
  )
}

export function TelegramLogo({ size = 26 }: { size?: number }) {
  return (
    <svg width={size} height={size} viewBox="0 0 24 24">
      <circle cx="12" cy="12" r="12" fill="#29A9EA" />
      <path
        d="M5.5 11.8l12-4.6c.6-.2 1 .1.8 1l-2 9.6c-.1.6-.5.8-1 .5l-2.8-2-1.4 1.3c-.2.2-.3.3-.6.3l.2-3 5.2-4.7c.2-.2 0-.3-.3-.1l-6.4 4-2.8-.9c-.6-.2-.6-.6.1-.9z"
        fill="#fff"
      />
    </svg>
  )
}

export function TeamsLogo({ size = 24 }: { size?: number }) {
  return (
    <svg width={size} height={size} viewBox="0 0 24 24">
      <rect x="2" y="6" width="13" height="12" rx="2.5" fill="#5059C9" />
      <circle cx="18" cy="8" r="3" fill="#7B83EB" />
      <path d="M15 11h6a1 1 0 0 1 1 1v4a3.5 3.5 0 0 1-7 0z" fill="#7B83EB" />
    </svg>
  )
}

export function DiscordLogo({ size = 24 }: { size?: number }) {
  return (
    <svg width={size} height={size} viewBox="0 0 24 24" fill="#5865F2">
      <path d="M20.3 5.4A17.8 17.8 0 0 0 15.9 4c-.2.4-.5.9-.6 1.3a16.5 16.5 0 0 0-4.6 0A9 9 0 0 0 10 4a17.9 17.9 0 0 0-4.4 1.4C2.9 9.3 2.2 13 2.5 16.7a18 18 0 0 0 5.5 2.8c.4-.6.8-1.3 1.1-2a11.6 11.6 0 0 1-1.8-.9l.4-.3a12.9 12.9 0 0 0 10.6 0l.4.3c-.6.3-1.2.6-1.8.9.3.7.7 1.4 1.1 2a18 18 0 0 0 5.5-2.8c.4-4.3-.7-8-2.7-11.3zM9.7 14.3c-.9 0-1.6-.8-1.6-1.8s.7-1.8 1.6-1.8 1.6.8 1.6 1.8-.7 1.8-1.6 1.8zm5.6 0c-.9 0-1.6-.8-1.6-1.8s.7-1.8 1.6-1.8 1.6.8 1.6 1.8-.7 1.8-1.6 1.8z" />
    </svg>
  )
}

export function ProviderLogo({ kind, size }: { kind: string; size?: number }) {
  switch (kind) {
    case 'slack':
      return <SlackLogo size={size} />
    case 'teams':
      return <TeamsLogo size={size} />
    case 'discord':
      return <DiscordLogo size={size} />
    case 'telegram':
      return <TelegramLogo size={size} />
    default:
      return null
  }
}

// Soft tile background per provider, matching the mockup's logo tiles.
export function providerTileBg(kind: string): string {
  switch (kind) {
    case 'slack':
      return '#F7F5F8'
    case 'teams':
      return '#F1F0FA'
    case 'discord':
      return '#EEEFFE'
    case 'telegram':
      return '#EAF6FD'
    default:
      return '#F1F2F5'
  }
}
