import { describe, expect, it } from 'vitest'
import {
  diffLines, diffStats, groupTools, moveItem, normalizeTags, pickAgent, silentAudiences, slugify, unusableTools,
} from '../components/ai-agents/agentRules'
import type { ToolInfo } from '../components/ai-agents/types'

const both = ['public', 'private'] as const
const catalog: ToolInfo[] = [
  { name: 'complaint_record', group: 'complaints', tiers: [...both], writes: true },
  { name: 'search_products', group: 'products', tiers: [...both], writes: false },
  { name: 'lookup_debt', group: 'debt', tiers: [...both], writes: false },
  { name: 'draft_order_create', group: 'orders', tiers: [...both], writes: true, hidden: true },
  { name: 'staff_only', group: 'orders', tiers: ['private'], writes: false },
]

describe('agent rules', () => {
  it('makes agent keys from Vietnamese names', () => {
    expect(slugify('Bot công nợ Đà Nẵng')).toBe('bot-cong-no-da-nang')
    expect(slugify('  Trợ lý — sản phẩm/kho  ')).toBe('tro-ly-san-pham-kho')
    expect(slugify('a'.repeat(80))).toHaveLength(63)
  })

  it('groups tools in a fixed order and hides hidden tools unless selected', () => {
    const groups = groupTools(catalog, [], 'customer')
    expect(groups.map(g => g.group)).toEqual(['products', 'orders', 'debt', 'complaints'])
    expect(groups.find(g => g.group === 'orders')!.tools.map(t => t.info.name)).toEqual(['staff_only'])
    const withHidden = groupTools(catalog, ['draft_order_create'], 'customer')
    expect(withHidden.find(g => g.group === 'orders')!.tools.map(t => t.info.name)).toContain('draft_order_create')
  })

  it('marks tools a customer-facing agent may not use', () => {
    const orders = groupTools(catalog, [], 'all').find(g => g.group === 'orders')!
    expect(orders.tools[0]).toMatchObject({ usable: false })
    expect(groupTools(catalog, [], 'staff').find(g => g.group === 'orders')!.tools[0].usable).toBe(true)
    expect(unusableTools(catalog, ['staff_only', 'lookup_debt', 'gone'], 'customer')).toEqual(['staff_only', 'gone'])
  })

  it('finds the audiences a team leaves silent', () => {
    const customer = { audience: 'customer' as const, is_active: true }
    const staffOff = { audience: 'staff' as const, is_active: false }
    expect(silentAudiences([{ agent: customer, enabled: true }, { agent: staffOff, enabled: true }])).toEqual(['staff'])
    expect(silentAudiences([{ agent: customer, enabled: false }])).toEqual(['customer', 'staff'])
    expect(silentAudiences([{ agent: { audience: 'all', is_active: true }, enabled: true }])).toEqual([])
  })

  it('picks the only agent, else the team default, else the first', () => {
    const a = { id: 'a', name: 'A', audience: 'customer' as const, is_active: true }
    const b = { id: 'b', name: 'B', audience: 'all' as const, is_active: true }
    const s = { id: 's', name: 'S', audience: 'staff' as const, is_active: true }
    const members = [a, b, s].map(agent => ({ agent, enabled: true }))
    expect(pickAgent(members, '', 'customer')?.id).toBe('a')
    expect(pickAgent(members, 'b', 'customer')?.id).toBe('b')
    expect(pickAgent(members, 's', 'customer')?.id).toBe('a')
    expect(pickAgent(members, 'a', 'staff')?.id).toBe('b')
    expect(pickAgent([{ agent: a, enabled: true }], '', 'staff')).toBeNull()
  })

  it('normalizes document tags like the knowledge package', () => {
    // 'Bảo hành' typed in decomposed form (NFD) still matches the stored tag.
    expect(normalizeTags(['  Bảo   Hành '.normalize('NFD'), 'bảo hành', 'Giao hàng', '', null])).toEqual(['bảo hành', 'giao hàng'])
  })

  it('moves members without mutating the list', () => {
    const list = ['a', 'b', 'c']
    expect(moveItem(list, 2, 0)).toEqual(['c', 'a', 'b'])
    expect(moveItem(list, 0, -1)).toEqual(list)
    expect(list).toEqual(['a', 'b', 'c'])
  })

  it('diffs prompts line by line and collapses unchanged runs', () => {
    const before = ['l1', 'l2', 'l3', 'l4', 'l5', 'l6', 'l7', 'old', 'l9'].join('\n')
    const after = ['l1', 'l2', 'l3', 'l4', 'l5', 'l6', 'l7', 'new', 'l9', 'added'].join('\n')
    const lines = diffLines(before, after, 1)
    expect(lines[0]).toEqual({ kind: 'gap', text: '6' })
    expect(lines.filter(l => l.kind === 'del').map(l => l.text)).toEqual(['old'])
    expect(lines.filter(l => l.kind === 'add').map(l => l.text)).toEqual(['new', 'added'])
    expect(diffStats(lines)).toEqual({ added: 2, removed: 1 })
    expect(diffLines('same', 'same')).toEqual([{ kind: 'gap', text: '1' }])
  })
})
