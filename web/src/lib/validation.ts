export const MAX_TAG_LENGTH = 60
export const MAX_TAG_COUNT = 20

const EMAIL_PATTERN = /^[^\s@]+@[^\s@]+\.[^\s@]+$/

export function validateEmail(value: string): string | null {
  const email = value.trim()
  if (!email) return 'Email is required.'
  if (!EMAIL_PATTERN.test(email)) return 'Enter a valid email address.'
  return null
}

export function validateDeliveryBinding(channel: string, value: string): string | null {
  const id = value.trim()
  if (!id) return `Enter your ${channel === 'teams' ? 'Microsoft Teams' : channel} ID.`

  switch (channel) {
    case 'discord':
      return /^\d{17,20}$/.test(id) ? null : 'Discord IDs contain 17 to 20 digits.'
    case 'slack':
      return /^[UW][A-Z0-9]{8,}$/.test(id) ? null : 'Slack member IDs start with U or W.'
    case 'telegram':
      return /^-?\d{5,20}$/.test(id) ? null : 'Enter a valid Telegram chat ID.'
    case 'teams':
      return /^(?:29:[A-Za-z0-9._-]{8,}|[0-9a-fA-F]{8}(?:-[0-9a-fA-F]{4}){3}-[0-9a-fA-F]{12})$/.test(id)
        ? null
        : 'Enter a Teams conversation ID or AAD object ID.'
    default:
      return null
  }
}
