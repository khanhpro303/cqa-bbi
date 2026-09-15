import { describe, expect, it } from 'vitest'
import { aggregateInsights, formatDuration } from '../utils/service-quality'

describe('service quality utilities', () => {
  it('formats response durations for KPI and queue display', () => {
    expect(formatDuration(null)).toBe('—')
    expect(formatDuration(42)).toBe('42 giây')
    expect(formatDuration(125)).toBe('2 phút 5 giây')
    expect(formatDuration(7320)).toBe('2 giờ 2 phút')
  })

  it('counts a conversation once per label and prefers real SKU product keys', () => {
    const result = aggregateInsights([
      {
        conversation_id: 'c1', insight_at: '2026-09-10T03:00:00Z', insight: {
          intents: ['Hỏi hàng', ' hỏi  hàng '],
          products: [
            { name: 'Sữa A', sku: 'SKU-1' },
            { name: 'Sữa A đổi tên', sku: 'SKU-1' },
            { name: 'Sữa B' },
          ],
          feedback: [{ category: 'Giao hàng', sentiment: 'negative' }],
          lead_quality: { level: 'high' },
        },
      },
      {
        conversation_id: 'c2', insight_at: '2026-09-11T03:00:00Z', insight: {
          intents: ['Hỏi hàng'], products: [{ name: 'Sữa B' }],
          feedback: [{ category: 'Giao hàng', sentiment: 'negative' }],
          lead_quality: { level: 'high' },
        },
      },
    ], '2026-09-09T00:00:00Z', '2026-09-12T00:00:00Z')

    expect(result.intents).toEqual([{ key: 'hỏi hàng', label: 'Hỏi hàng', count: 2, conversationIds: ['c1', 'c2'] }])
    expect(result.products.map(item => [item.key, item.count, item.conversationIds])).toEqual([
      ['name:sữa b', 2, ['c1', 'c2']],
      ['sku:sku-1', 1, ['c1']],
    ])
    expect(result.feedback[0].count).toBe(2)
    expect(result.feedback[0].conversationIds).toEqual(['c1', 'c2'])
    expect(result.leadQuality[0].count).toBe(2)
    expect(result.leadQuality[0].conversationIds).toEqual(['c1', 'c2'])
  })

  it('keeps keyword counts aligned with unique source conversations across duplicate rows', () => {
    const insight = {
      intents: ['Hỏi giá', ' hỏi  giá '],
      products: [{ name: 'Sữa A', sku: 'SKU-1' }, { name: 'Tên khác', sku: ' sku-1 ' }],
      feedback: [{ category: 'Giao hàng', sentiment: 'negative' }, { category: ' giao  hàng ', sentiment: 'Negative' }],
      lead_quality: { level: 'high' as const },
    }
    const result = aggregateInsights([
      { conversation_id: 'c1', insight_at: '2026-09-10T03:00:00Z', insight },
      { conversation_id: 'c1', insight_at: '2026-09-11T03:00:00Z', insight },
      { conversation_id: 'c2', insight_at: '2026-09-11T03:00:00Z', insight },
    ], '2026-09-09T00:00:00Z', '2026-09-12T00:00:00Z')

    for (const segment of [result.intents, result.products, result.feedback, result.leadQuality]) {
      expect(segment).toHaveLength(1)
      expect(segment[0].conversationIds).toEqual(['c1', 'c2'])
      expect(segment[0].count).toBe(segment[0].conversationIds.length)
    }
  })

  it('excludes stale and out-of-window AI analyses from aggregates', () => {
    const result = aggregateInsights([
      { conversation_id: 'stale', insight_at: '2026-09-10T03:00:00Z', insight_stale: true, insight: { intents: ['Mua hàng'] } },
      { conversation_id: 'old', insight_at: '2026-08-01T03:00:00Z', insight: { intents: ['Mua hàng'] } },
      { conversation_id: 'end', insight_at: '2026-09-12T00:00:00Z', insight: { intents: ['Mua hàng'] } },
      { conversation_id: 'missing-date', insight: { intents: ['Mua hàng'] } },
      { conversation_id: 'fresh', insight_at: '2026-09-10T03:00:00Z', insight: { intents: ['Hỏi giá'] } },
    ], '2026-09-09T00:00:00Z', '2026-09-12T00:00:00Z')

    expect(result.eligibleConversations).toBe(1)
    expect(result.excludedStale).toBe(1)
    expect(result.excludedOutsideWindow).toBe(3)
    expect(result.intents.map(item => item.label)).toEqual(['Hỏi giá'])
    expect(result.intents[0].conversationIds).toEqual(['fresh'])
  })
})
