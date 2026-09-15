export type ServiceQualityStatus = 'answered' | 'waiting' | 'overdue' | 'resolved' | 'no_request'

export interface ProductInsight {
  name: string
  sku?: string
  evidence?: string
}

export interface FeedbackInsight {
  category: string
  sentiment: string
  evidence?: string
}

export interface LeadQualityInsight {
  level: 'high' | 'medium' | 'low' | 'spam' | 'unknown'
  evidence?: string
  reason?: string
}

export interface ConversationInsight {
  summary?: string
  intents?: string[]
  products?: ProductInsight[]
  feedback?: FeedbackInsight[]
  lead_quality?: LeadQualityInsight
}

export interface InsightRow {
  conversation_id: string
  insight?: ConversationInsight | null
  insight_at?: string | null
  insight_stale?: boolean
}

export interface AggregateItem {
  key: string
  label: string
  count: number
  conversationIds: string[]
}

export interface InsightAggregates {
  eligibleConversations: number
  excludedStale: number
  excludedOutsideWindow: number
  intents: AggregateItem[]
  products: AggregateItem[]
  feedback: AggregateItem[]
  leadQuality: AggregateItem[]
}

export function formatDuration(seconds: number | null | undefined): string {
  if (seconds == null || !Number.isFinite(seconds)) return '—'
  const total = Math.max(0, Math.round(seconds))
  if (total < 60) return `${total} giây`
  const minutes = Math.floor(total / 60)
  const remainingSeconds = total % 60
  if (minutes < 60) return remainingSeconds ? `${minutes} phút ${remainingSeconds} giây` : `${minutes} phút`
  const hours = Math.floor(minutes / 60)
  const remainingMinutes = minutes % 60
  return remainingMinutes ? `${hours} giờ ${remainingMinutes} phút` : `${hours} giờ`
}

export function normalizeInsightLabel(value: string): string {
  return value.trim().replace(/\s+/g, ' ').toLocaleLowerCase('vi')
}

function addUnique(target: Map<string, AggregateItem>, values: Array<{ key: string; label: string }>, conversationId: string) {
  const seen = new Set<string>()
  for (const value of values) {
    if (!value.key || seen.has(value.key)) continue
    seen.add(value.key)
    const current = target.get(value.key)
    if (current) {
      if (!current.conversationIds.includes(conversationId)) {
        current.conversationIds.push(conversationId)
        current.count = current.conversationIds.length
      }
    } else target.set(value.key, { ...value, count: 1, conversationIds: [conversationId] })
  }
}

function sorted(items: Map<string, AggregateItem>): AggregateItem[] {
  return [...items.values()].sort((a, b) => b.count - a.count || a.label.localeCompare(b.label, 'vi'))
}

/**
 * Counts each conversation once per label. Only fresh analyses generated inside
 * the selected report window are included, so an old pending conversation can
 * appear in the live queue without distorting the windowed content report.
 */
export function aggregateInsights(rows: InsightRow[], from: string | Date, to: string | Date): InsightAggregates {
  const start = new Date(from).getTime()
  const end = new Date(to).getTime()
  const intents = new Map<string, AggregateItem>()
  const products = new Map<string, AggregateItem>()
  const feedback = new Map<string, AggregateItem>()
  const leadQuality = new Map<string, AggregateItem>()
  let eligibleConversations = 0
  let excludedStale = 0
  let excludedOutsideWindow = 0

  for (const row of rows) {
    if (!row.insight) continue
    if (row.insight_stale) {
      excludedStale += 1
      continue
    }
    const insightAt = row.insight_at ? new Date(row.insight_at).getTime() : Number.NaN
    if (!Number.isFinite(insightAt) || insightAt < start || insightAt >= end) {
      excludedOutsideWindow += 1
      continue
    }

    eligibleConversations += 1
    addUnique(intents, (row.insight.intents || []).map(label => ({ key: normalizeInsightLabel(label), label: label.trim() })), row.conversation_id)
    addUnique(products, (row.insight.products || []).map(product => {
      const sku = product.sku?.trim()
      const name = product.name.trim()
      return sku
        ? { key: `sku:${normalizeInsightLabel(sku)}`, label: `${name} · ${sku}` }
        : { key: `name:${normalizeInsightLabel(name)}`, label: name }
    }), row.conversation_id)
    addUnique(feedback, (row.insight.feedback || []).map(item => {
      const category = item.category.trim()
      const sentiment = item.sentiment.trim().toLowerCase()
      return { key: `${normalizeInsightLabel(category)}:${sentiment}`, label: `${category} · ${sentiment}` }
    }), row.conversation_id)
    const lead = row.insight.lead_quality?.level
    if (lead) addUnique(leadQuality, [{ key: lead, label: lead }], row.conversation_id)
  }

  return {
    eligibleConversations,
    excludedStale,
    excludedOutsideWindow,
    intents: sorted(intents),
    products: sorted(products),
    feedback: sorted(feedback),
    leadQuality: sorted(leadQuality),
  }
}
