import { describe, expect, it } from 'vitest'
import { buildMetaLabelRules, classifiedCount, validateMetaLabelRules } from '../utils/meta-labels'

describe('Meta Inbox label helpers', () => {
  it('builds API rules from all three configured categories', () => {
    expect(buildMetaLabelRules({
      qualified: ['label-1'],
      unqualified: ['label-2'],
      potential: ['label-3', 'label-4'],
    })).toEqual([
      { label_id: 'label-1', category: 'qualified' },
      { label_id: 'label-2', category: 'unqualified' },
      { label_id: 'label-3', category: 'potential' },
      { label_id: 'label-4', category: 'potential' },
    ])
  })

  it('rejects missing, stale, and cross-category duplicate mappings when enabled', () => {
    const catalog = new Set(['label-1', 'label-2'])
    const intakeLabels = new Set(['label-2'])
    expect(validateMetaLabelRules(true, [], catalog, intakeLabels)).toContain('ít nhất một')
    expect(validateMetaLabelRules(true, [{ label_id: 'missing', category: 'qualified' }], catalog, intakeLabels)).toContain('không còn tồn tại')
    expect(validateMetaLabelRules(true, [{ label_id: 'label-2', category: 'qualified' }], catalog, intakeLabels)).toContain('Nhãn mặc định')
    expect(validateMetaLabelRules(true, [
      { label_id: 'label-1', category: 'qualified' },
      { label_id: 'label-1', category: 'potential' },
    ], catalog, intakeLabels)).toContain('chỉ được gán')
    expect(validateMetaLabelRules(false, [], catalog, intakeLabels)).toBe('')
  })

  it('counts only the three mapped classifications as classified', () => {
    expect(classifiedCount({ total: 12, unclassified: 3, qualified: 2, unqualified: 1, potential: 4, conflict: 1, unknown: 1 })).toBe(7)
    expect(classifiedCount(null)).toBe(0)
  })
})
