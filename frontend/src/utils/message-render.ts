export interface ReminderMessageView {
  title?: string
  description?: string
  imageUrl?: string
}

export interface ImageMessageView {
  title?: string
  imageUrl: string
}

export interface ThirdPartyLinkMessageView {
  title?: string
  description?: string
  imageUrl?: string
  href: string
  source?: string
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null && !Array.isArray(value)
}

function parseJsonObject(value: unknown): Record<string, unknown> | null {
  if (isRecord(value)) return value
  if (typeof value !== 'string' || value.trim() === '') return null
  try {
    const parsed = JSON.parse(value)
    return isRecord(parsed) ? parsed : null
  } catch {
    return null
  }
}

function parseString(value: unknown): string {
  if (typeof value !== 'string') return ''
  return value.replace(/\r\n/g, '\n').trim()
}

function parseHttpUrl(value: unknown): string {
  const candidate = parseString(value)
  if (!candidate) return ''
  try {
    const url = new URL(candidate)
    if (url.protocol === 'http:' || url.protocol === 'https:') {
      return candidate
    }
    return ''
  } catch {
    return ''
  }
}

function isThirdPartyLinkAction(action: string): boolean {
  const normalized = action.toLowerCase()
  return normalized === 'recommened.link' || normalized === 'recommended.link'
}

function parseThirdPartyLinkRoot(root: Record<string, unknown>): ThirdPartyLinkMessageView | null {
  const action = parseString(root.action)
  if (!isThirdPartyLinkAction(action)) return null

  const params = parseJsonObject(root.params)
  const href = [root.href, root.description, params?.mediaTitle].map(parseHttpUrl).find(Boolean) || ''
  if (!href) return null

  const title = parseString(root.title) || (() => {
    const mediaTitle = parseString(params?.mediaTitle)
    return parseHttpUrl(mediaTitle) ? '' : mediaTitle
  })()

  const descriptionCandidate = parseString(root.description)
  const description = descriptionCandidate && descriptionCandidate !== href ? descriptionCandidate : ''

  const imageUrl = [root.thumb, params?.stream_icon].map(parseHttpUrl).find(Boolean) || ''
  const source = parseString(params?.src)

  return {
    title: title || undefined,
    description: description || undefined,
    imageUrl: imageUrl || undefined,
    href,
    source: source || undefined,
  }
}

function extractCalendarAction(params: Record<string, unknown> | null): boolean {
  if (!params) return false
  const actions = params.actions
  if (!Array.isArray(actions)) return false
  return actions.some((action) => isRecord(action) && action.actionType === 'action.open.calendar.event')
}

function parseType8Reminder(root: Record<string, unknown>): ReminderMessageView | null {
  const messageType = typeof root.type === 'string' || typeof root.type === 'number'
    ? String(root.type).trim()
    : ''
  if (messageType !== '8') return null

  const action = typeof root.action === 'string' ? root.action.trim() : ''
  if (action !== 'show.profile') return null

  const paramsRaw = typeof root.params === 'string' ? root.params : ''
  const reminderSignal = paramsRaw.includes('action.open.reminder') || paramsRaw.includes('remind_topic')
  if (!reminderSignal) return null

  const description = typeof root.description === 'string' ? root.description.trim() : ''
  const imageUrl = [root.thumb, root.href].find((value) => typeof value === 'string' && value.trim().length > 0) as string | undefined

  if (!description && !imageUrl) return null
  return { description, imageUrl }
}

function isLikelyImageUrl(url: string): boolean {
  const lower = url.toLowerCase()
  return (
    /\.(png|jpe?g|gif|webp|bmp|svg)(\?|#|$)/.test(lower) ||
    /\/(jpg|jpeg|png|gif|webp)\//.test(lower) ||
    (lower.includes('zdn.vn') && (lower.includes('/jpg/') || lower.includes('/jpeg/') || lower.includes('/png/') || lower.includes('/gif/') || lower.includes('/webp/')))
  )
}

export function archivedMediaCaption(content: string, contentType: string, attachments: string): string | undefined {
  if (contentType !== 'image' && contentType !== 'file') return undefined
  const payload = parseJsonObject(content)
  if (!payload) return undefined

  let files: unknown
  try {
    files = JSON.parse(attachments)
  } catch {
    return undefined
  }
  if (!Array.isArray(files) || !files.some(file =>
    isRecord(file) && file.type === contentType && parseString(file.local_path).split('/')[2] === 'personal-zalo',
  )) {
    return undefined
  }

  if (contentType === 'file') return ''
  const caption = parseString(payload.description) || parseString(payload.title)
  return parseHttpUrl(caption) ? '' : caption
}

export function parseImageMessagePayload(content: string): ImageMessageView | null {
  const root = parseJsonObject(content)
  if (!root) return null

  const action = parseString(root.action)
  if (action === 'rtf' || action === 'msginfo.actionlist') return null
  if (isThirdPartyLinkAction(action)) return null

  const messageType = typeof root.type === 'string' || typeof root.type === 'number'
    ? String(root.type).trim()
    : ''
  if (action === 'show.profile' && messageType === '8') return null

  const params = parseJsonObject(root.params)
  const candidates = [root.thumb, root.href, params?.hd].filter((value): value is string => typeof value === 'string' && value.trim().length > 0)
  const imageUrl = candidates.find((url) => isLikelyImageUrl(url.trim()))?.trim()
  if (!imageUrl) return null

  const title = typeof root.title === 'string' ? root.title.replace(/\r\n/g, '\n').trim() : ''
  return { title, imageUrl }
}

export function parseThirdPartyLinkMessagePayload(content: string): ThirdPartyLinkMessageView | null {
  const root = parseJsonObject(content)
  if (!root) return null
  return parseThirdPartyLinkRoot(root)
}

export function parseStructuredMessageText(content: string): string | null {
  const root = parseJsonObject(content)
  if (!root) return null

  const action = parseString(root.action)
  if (action === 'rtf') {
    const title = parseString(root.title)
    if (!title) return null
    return title
  }

  const thirdPartyLink = parseThirdPartyLinkRoot(root)
  if (thirdPartyLink) {
    if (thirdPartyLink.title && thirdPartyLink.title !== thirdPartyLink.href) {
      return `${thirdPartyLink.title}\n${thirdPartyLink.href}`
    }
    return thirdPartyLink.href
  }

  return null
}

export function parseReminderPayload(content: string): ReminderMessageView | null {
  const root = parseJsonObject(content)
  if (!root) return null

  const type8Reminder = parseType8Reminder(root)
  if (type8Reminder) return type8Reminder

  const title = typeof root.title === 'string' ? root.title.trim() : ''
  if (!title) return null

  const params = parseJsonObject(root.params)
  const hasCalendarAction = extractCalendarAction(params)
  const action = typeof root.action === 'string' ? root.action.trim() : ''
  const iconUrl = typeof params?.iconUrl === 'string' ? params.iconUrl : ''
  const titleLooksLikeReminder = /nhắc hẹn|reminder/i.test(title)
  const iconLooksLikeReminder = /alarm|calendar/i.test(iconUrl)
  const isReminderAction = action === 'msginfo.actionlist'

  if ((isReminderAction && (hasCalendarAction || titleLooksLikeReminder || iconLooksLikeReminder)) || hasCalendarAction) {
    return { title }
  }

  return null
}
