// DiscordIdHint — the "how do I find my Discord user ID?" mini-tutorial shown
// under every Discord-user-ID input (onboarding, notifications, account, admin
// edit). The ID is not discoverable without Developer Mode, so an input without
// this hint is a dead end for most users.

import { T } from '../lib/theme'

export function DiscordIdHint() {
  return (
    <div
      style={{
        marginTop: 8,
        fontSize: 12.5,
        lineHeight: 1.6,
        color: T.muted,
        background: T.surfaceAlt,
        border: `1px solid ${T.borderSubtle}`,
        borderRadius: 9,
        padding: '9px 12px',
      }}
    >
      <strong style={{ color: T.body }}>Where to find it:</strong> in Discord, open{' '}
      <strong>Settings → Advanced</strong> and enable <strong>Developer Mode</strong>. Then
      right-click your name or avatar anywhere and choose{' '}
      <strong>Copy User ID</strong> — paste the number here.
    </div>
  )
}
