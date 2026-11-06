export type MessengerJourneyKind = 'referral' | 'labels' | 'classification' | 'order' | 'deletion'

export interface MessengerJourneyReferral {
  ad_id?: string
  ref?: string
  source?: string
  occurred_at?: string
  observed_at?: string
}

export interface MessengerJourneyEvent {
  event_id: string
  kind: MessengerJourneyKind | string
  occurred_at?: string
  observed_at: string
  source?: string
  policy_version?: string
  version?: string
  payload?: Record<string, unknown>
}

export interface MessengerJourneySummary {
  first_referral?: MessengerJourneyReferral | null
  latest_referral?: MessengerJourneyReferral | null
  labels?: string[]
  classification?: string
  classification_observed_at?: string
  last_observed_at?: string
}

export interface MessengerJourneyPage {
  summary: MessengerJourneySummary
  events: MessengerJourneyEvent[]
  next_cursor: string
  has_more: boolean
}

function object(value: unknown): Record<string, unknown> {
  return value !== null && typeof value === 'object' && !Array.isArray(value)
    ? value as Record<string, unknown>
    : {}
}

function string(value: unknown): string {
  return typeof value === 'string' ? value : ''
}

function stringArray(value: unknown): string[] {
  if (!Array.isArray(value)) return []
  return value.flatMap((item) => {
    if (typeof item === 'string' && item.trim() !== '') return [item]
    const raw = object(item)
    const label = string(raw.page_label_name ?? raw.name ?? raw.title ?? raw.id)
    return label ? [label] : []
  })
}

function referral(value: unknown): MessengerJourneyReferral | null {
  const raw = object(value)
  if (!Object.keys(raw).length) return null
  const payload = object(raw.payload)
  return {
    ad_id: string(payload.ad_id ?? raw.ad_id),
    ref: string(payload.ref ?? raw.ref),
    source: string(payload.source ?? raw.source),
    occurred_at: string(raw.occurred_at),
    observed_at: string(raw.observed_at),
  }
}

export function normalizeMessengerJourneyPage(value: unknown): MessengerJourneyPage {
  const root = object(value)
  const body = object(root.data ?? root)
  const rawSummary = object(body.summary ?? body.current)
  const rawEvents = Array.isArray(body.events) ? body.events : []
  return {
    summary: {
      first_referral: referral(rawSummary.first_referral),
      latest_referral: referral(rawSummary.latest_referral),
      labels: stringArray(rawSummary.labels ?? rawSummary.current_labels),
      classification: string(rawSummary.classification),
      classification_observed_at: string(rawSummary.classification_observed_at),
      last_observed_at: string(rawSummary.last_observed_at),
    },
    events: rawEvents.map((entry) => {
      const raw = object(entry)
      return {
        event_id: string(raw.event_id),
        kind: string(raw.kind ?? raw.type ?? raw.event_type),
        occurred_at: string(raw.occurred_at),
        observed_at: string(raw.observed_at),
        source: string(raw.source),
        policy_version: string(raw.policy_version),
        version: string(raw.version),
        payload: object(raw.payload),
      }
    }).filter(event => event.event_id && event.kind && event.observed_at),
    next_cursor: string(body.next_cursor),
    has_more: body.has_more === true,
  }
}

export function journeyPayloadLabels(payload: Record<string, unknown> | undefined): string[] {
  return stringArray(payload?.labels ?? payload?.current_labels)
}

export function journeyPayloadText(payload: Record<string, unknown> | undefined, key: string): string {
  return string(payload?.[key])
}

export function journeyPayloadNumber(payload: Record<string, unknown> | undefined, key: string): number | null {
  const value = payload?.[key]
  return typeof value === 'number' && Number.isFinite(value) ? value : null
}
