import { aggregateInsights, type AggregateItem, type InsightRow } from './service-quality'

export type PotentialLevel = 'high' | 'medium' | 'low'
export type PotentialFilter = PotentialLevel | 'all'
export const potentialLevels: PotentialLevel[] = ['high', 'medium', 'low']

export interface AIProductGroup {
  name: string
  members: string[]
}

/** Distinct source labels sent for AI grouping; same freshness/window as the report. */
export function collectProductNames(rows: InsightRow[], from: string, to: string): string[] {
  const start = new Date(from).getTime()
  const end = new Date(to).getTime()
  const names = new Set<string>()
  for (const row of rows) {
    const level = row.insight?.lead_quality?.level
    const at = row.insight_at ? new Date(row.insight_at).getTime() : Number.NaN
    if (!level || !potentialLevels.includes(level as PotentialLevel) || row.insight_stale || !Number.isFinite(at) || at < start || at >= end) continue
    for (const product of row.insight?.products || []) {
      if (product.name.trim()) names.add(product.name.trim())
    }
  }
  return [...names].sort()
}

/** AI decides canonical names and membership. Code only counts unique conversations. */
export function aggregateProductDemand(rows: InsightRow[], from: string, to: string, filter: PotentialFilter, groups: AIProductGroup[]): AggregateItem[] {
  const names = new Map(groups.flatMap(group => group.members.map(member => [member, group.name] as const)))
  const selected = rows.filter(row => {
    const level = row.insight?.lead_quality?.level
    return level && potentialLevels.includes(level as PotentialLevel) && (filter === 'all' || level === filter)
  }).map(row => ({
    ...row,
    insight: {
      ...row.insight,
      products: (row.insight?.products || []).map(product => ({
        ...product,
        name: names.get(product.name.trim()) || '',
        // AI groups models; inventory variants do not split a model's demand.
        sku: '',
      })).filter(product => product.name),
    },
  }))
  return aggregateInsights(selected, from, to).products
}

export interface TreemapTile extends AggregateItem {
  x: number
  y: number
  width: number
  height: number
}

/** Balanced binary treemap: each rectangle's area is proportional to its count. */
export function layoutTreemap(items: AggregateItem[]): TreemapTile[] {
  const tiles: TreemapTile[] = []
  function split(group: AggregateItem[], x: number, y: number, width: number, height: number) {
    if (!group.length) return
    if (group.length === 1) {
      tiles.push({ ...group[0]!, x, y: y / 0.6, width, height: height / 0.6 })
      return
    }
    const total = group.reduce((sum, item) => sum + item.count, 0)
    let index = 1
    let subtotal = group[0]!.count
    while (index < group.length - 1 && Math.abs(subtotal + group[index]!.count - total / 2) < Math.abs(subtotal - total / 2)) {
      subtotal += group[index]!.count
      index++
    }
    const ratio = subtotal / total
    if (width >= height) {
      split(group.slice(0, index), x, y, width * ratio, height)
      split(group.slice(index), x + width * ratio, y, width * (1 - ratio), height)
    } else {
      split(group.slice(0, index), x, y, width, height * ratio)
      split(group.slice(index), x, y + height * ratio, width, height * (1 - ratio))
    }
  }
  split(items.filter(item => item.count > 0), 0, 0, 100, 60)
  return tiles
}
