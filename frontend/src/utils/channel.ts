// Shared helpers for rendering a conversation/channel source by its `channel_type`.
// Valid types come from the backend Channel model: zalo_oa | facebook | personal_zalo_import.

type Translate = (key: string) => string

export const ZALO_LOGO_URL = '/assets/brand/zalo-logo.svg'

const LABEL_KEY_BY_TYPE: Record<string, string> = {
  facebook: 'channel_facebook',
  zalo_oa: 'channel_zalo',
  personal_zalo_import: 'channel_personal_zalo',
}

const COLOR_BY_TYPE: Record<string, string> = {
  facebook: 'blue',
  zalo_oa: 'green',
  personal_zalo_import: 'teal',
}

const ICON_BY_TYPE: Record<string, string> = {
  facebook: 'mdi-facebook-messenger',
  personal_zalo_import: 'mdi-cellphone-link',
}

/** Whether a channel type represents a Zalo OA channel (renders the Zalo brand logo). */
export function isZaloOaChannel(type?: string): boolean {
  return type === 'zalo_oa' || type === 'zalo'
}

/** Normalize an optional channel avatar URL. */
export function normalizeAvatarUrl(url?: string | null): string {
  return (url || '').trim()
}

/** Reject unsafe schemes before binding an external avatar URL to an image element. */
export function isSupportedAvatarUrl(url: string): boolean {
  return /^(https?:\/\/|data:image\/|blob:|\/(?!\/))/i.test(url)
}

/** Cache key for tracking broken avatar loads per channel + URL pair. */
export function avatarFailureKey(channelId: string | undefined, avatarUrl?: string | null): string {
  return `${channelId || ''}:${normalizeAvatarUrl(avatarUrl)}`
}

/** Human-readable label for a channel type, translated via the provided `t`. */
export function channelTypeLabel(type: string, t: Translate): string {
  const key = LABEL_KEY_BY_TYPE[type]
  return key ? t(key) : type
}

/** Vuetify color token for a channel type's avatar/chip. */
export function channelTypeColor(type: string): string {
  return COLOR_BY_TYPE[type] ?? 'green'
}

/** MDI fallback icon for a channel type's avatar. */
export function channelTypeIcon(type: string): string {
  return ICON_BY_TYPE[type] ?? 'mdi-chat'
}
