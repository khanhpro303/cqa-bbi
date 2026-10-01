import { describe, expect, it } from 'vitest'
import { aggregateProductDemand, collectProductNames, layoutTreemap, type AIProductGroup } from '../utils/quality-charts'
import { aggregateInsights, type InsightRow } from '../utils/service-quality'

const from = '2026-09-01T00:00:00Z'
const to = '2026-10-01T00:00:00Z'
const groups: AIProductGroup[] = [{ name: 'E-24', members: ['Mũ bảo hiểm E-24', 'EGO E-24', 'Mũ E-24'] }, { name: 'FF818', members: ['LS2 FF 818', 'LS2 FF818', 'FF818'] }, { name: 'FF800', members: ['FF800'] }]
function row(id: string, level: 'high' | 'medium' | 'low' | 'unknown' | 'spam', names: string[], extra: Partial<InsightRow> = {}): InsightRow {
  return { conversation_id: id, insight_at: '2026-09-15T00:00:00Z', insight: { lead_quality: { level }, products: names.map(name => ({ name })) }, ...extra }
}

describe('CSKH chart data', () => {
  it('counts only the groups returned by AI, without locally inferring model names', () => {
    const rows = [row('a', 'high', ['Mũ bảo hiểm E-24', 'LS2 FF 818'])]
    expect(aggregateProductDemand(rows, from, to, 'all', [])).toEqual([])
    const result = aggregateProductDemand(rows, from, to, 'all', [{ name: 'Model decided by AI', members: ['Mũ bảo hiểm E-24', 'LS2 FF 818'] }])
    expect(result.map(item => [item.label, item.count])).toEqual([['Model decided by AI', 1]])
    expect(collectProductNames(rows, from, to)).toEqual(['LS2 FF 818', 'Mũ bảo hiểm E-24'])
  })

  it('groups historical names, counts each conversation once, and filters each potential level', () => {
    const rows = [row('a', 'high', ['Mũ bảo hiểm E-24', 'EGO E-24']), row('b', 'medium', ['Mũ E-24', 'LS2 FF 818']), row('c', 'low', ['FF818'])]
    const all = aggregateProductDemand(rows, from, to, 'all', groups)
    expect(all.map(item => [item.label, item.count])).toEqual([['E-24', 2], ['FF818', 2]])
    expect(aggregateProductDemand([...rows, rows[0]!], from, to, 'all', groups)).toEqual(all)
    for (const [level, label] of [['high', 'E-24'], ['medium', 'E-24'], ['low', 'FF818']] as const) {
      expect(aggregateProductDemand(rows, from, to, level, groups)[0]?.label).toBe(label)
      expect(aggregateProductDemand(rows, from, to, level, groups)[0]?.count).toBe(1)
    }
  })

  it('preserves audited E-24 conversation totals after AI groups its raw aliases', () => {
    const aliases = [
      { name: 'mũ bảo hiểm nửa đầu EGO E-24', levels: { high: 3, medium: 7, unknown: 5 } },
      { name: 'mũ bảo hiểm EGO E-24', levels: { high: 1, medium: 2, unknown: 2 } },
      { name: 'E-24', levels: { high: 0, medium: 3, unknown: 0 } },
      { name: 'EGO E-24', levels: { high: 2, medium: 1, unknown: 0 } },
    ] as const
    const rows = aliases.flatMap((alias, aliasIndex) =>
      (Object.entries(alias.levels) as Array<['high' | 'medium' | 'unknown', number]>).flatMap(([level, count]) =>
        Array.from({ length: count }, (_, index) => row(`e24-${aliasIndex}-${level}-${index}`, level, [alias.name])),
      ),
    )
    const e24Group: AIProductGroup[] = [{ name: 'E-24', members: aliases.map(alias => alias.name) }]

    expect(aggregateInsights(rows, from, to).products.map(item => [item.label, item.count])).toEqual([
      ['mũ bảo hiểm nửa đầu EGO E-24', 15],
      ['mũ bảo hiểm EGO E-24', 5],
      ['E-24', 3],
      ['EGO E-24', 3],
    ])
    expect(aggregateProductDemand(rows, from, to, 'all', e24Group)).toMatchObject([{ label: 'E-24', count: 19 }])
    expect(aggregateProductDemand(rows, from, to, 'high', e24Group)).toMatchObject([{ label: 'E-24', count: 6 }])
    expect(aggregateProductDemand(rows, from, to, 'medium', e24Group)).toMatchObject([{ label: 'E-24', count: 13 }])
    expect(aggregateProductDemand(rows, from, to, 'low', e24Group)).toEqual([])
  })

  it('keeps demand and pie aligned with report freshness and dates, excluding unclassified leads', () => {
    const rows = [row('a', 'high', ['FF818']), row('b', 'low', ['FF800']), row('old', 'high', ['FF818'], { insight_stale: true }), row('end', 'medium', ['FF818'], { insight_at: to }), row('unknown', 'unknown', ['FF818']), row('spam', 'spam', ['FF818'])]
    expect(aggregateProductDemand(rows, from, to, 'all', groups).map(item => item.count)).toEqual([1, 1])
    const leads = aggregateInsights(rows, from, to).leadQuality.filter(item => ['high', 'medium', 'low'].includes(item.key))
    expect(leads.map(item => [item.key, item.count])).toEqual([['high', 1], ['low', 1]])
    expect(aggregateProductDemand(rows, from, to, 'medium', groups)).toEqual([])
  })

  it('merges model demand across SKU variants without mutating source data', () => {
    const source = row('a', 'high', [])
    source.insight!.products = [{ name: 'LS2 FF818', sku: 'RED-L' }, { name: 'LS2 FF 818', sku: 'BLUE-M' }]
    expect(aggregateProductDemand([source], from, to, 'all', groups)[0]).toMatchObject({ label: 'FF818', count: 1 })
    expect(source.insight!.products[0]?.sku).toBe('RED-L')
  })
})

describe('treemap geometry', () => {
  it('preserves count proportions with bounded, non-overlapping rectangles', () => {
    const items = [30, 12, 8, 4, 1].map((count, i) => ({ key: String(i), label: String(i), count, conversationIds: [] }))
    const tiles = layoutTreemap(items)
    const total = items.reduce((sum, item) => sum + item.count, 0)
    expect(tiles).toHaveLength(items.length)
    for (const tile of tiles) {
      expect(tile.width * tile.height / 10000).toBeCloseTo(tile.count / total)
      expect(tile.x).toBeGreaterThanOrEqual(0)
      expect(tile.y).toBeGreaterThanOrEqual(0)
      expect(tile.x + tile.width).toBeLessThanOrEqual(100.00001)
      expect(tile.y + tile.height).toBeLessThanOrEqual(100.00001)
    }
    for (let i = 0; i < tiles.length; i++) for (let j = i + 1; j < tiles.length; j++) {
      const a = tiles[i]!, b = tiles[j]!
      expect(a.x + a.width <= b.x + 1e-8 || b.x + b.width <= a.x + 1e-8 || a.y + a.height <= b.y + 1e-8 || b.y + b.height <= a.y + 1e-8).toBe(true)
    }
    expect(layoutTreemap([])).toEqual([])
  })
})
