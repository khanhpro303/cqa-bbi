export type MetaLabelCategory = 'qualified' | 'unqualified' | 'potential'

export interface MetaLabelRule {
  label_id: string
  category: MetaLabelCategory
}

export interface MetaLabelCounts {
  total: number
  unclassified: number
  qualified: number
  unqualified: number
  potential: number
  conflict: number
  unknown: number
}

export function buildMetaLabelRules(selections: Record<MetaLabelCategory, string[]>): MetaLabelRule[] {
  return (Object.entries(selections) as [MetaLabelCategory, string[]][]).flatMap(([category, ids]) =>
    ids.map(labelId => ({ label_id: labelId, category })),
  )
}

export function validateMetaLabelRules(enabled: boolean, rules: MetaLabelRule[], catalogIds: Set<string>, intakeLabelIds: Set<string> = new Set()): string {
  if (!enabled) return ''
  if (rules.length === 0) return 'Chọn ít nhất một nhãn để bật theo dõi.'

  const assigned = new Map<string, MetaLabelCategory>()
  for (const rule of rules) {
    if (!rule.label_id || !catalogIds.has(rule.label_id)) return 'Có nhãn không còn tồn tại trong danh mục Meta.'
    if (intakeLabelIds.has(rule.label_id)) return 'Nhãn mặc định đã có lúc tiếp nhận hội thoại không được dùng làm nhãn theo dõi.'
    const previous = assigned.get(rule.label_id)
    if (previous && previous !== rule.category) return 'Một nhãn chỉ được gán cho một nhóm trạng thái.'
    assigned.set(rule.label_id, rule.category)
  }
  return ''
}

export function classifiedCount(counts: MetaLabelCounts | null): number {
  if (!counts) return 0
  return counts.qualified + counts.unqualified + counts.potential
}
