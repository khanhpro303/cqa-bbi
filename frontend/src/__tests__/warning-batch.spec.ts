// @vitest-environment happy-dom
import { mount } from '@vue/test-utils'
import { describe, expect, it, vi } from 'vitest'
import WarningBatch from '../components/WarningBatch.vue'

vi.mock('vuetify/components/VAlert', () => ({ VAlert: { template: '<div><slot /></div>' } }))
vi.mock('vuetify/components/VIcon', () => ({ VIcon: { template: '<span />' } }))

const warnings = [
  { id: 'sync', title: 'Lỗi đồng bộ', detail: 'Chi tiết đồng bộ' },
  { id: 'intake', title: 'Lỗi tiếp nhận', detail: 'Chi tiết tiếp nhận' },
]
const global = { stubs: { VAlert: { template: '<div><slot /></div>' }, VIcon: true } }

describe('WarningBatch', () => {
  it('renders nothing when there are no warnings', () => {
    expect(mount(WarningBatch, { props: { warnings: [] }, global }).text()).toBe('')
  })

  it('shows a single warning immediately', () => {
    const wrapper = mount(WarningBatch, { props: { warnings: warnings.slice(0, 1) }, global })
    expect(wrapper.text()).toContain('Chi tiết đồng bộ')
    expect(wrapper.find('button').exists()).toBe(false)
  })

  it('expands and collapses multiple warnings and preserves actions', async () => {
    const wrapper = mount(WarningBatch, {
      props: { warnings }, global,
      slots: { action: '<button class="action">Xem hội thoại</button>' },
    })
    expect(wrapper.text()).toContain('Có 2 cảnh báo')
    expect(wrapper.text()).not.toContain('Chi tiết đồng bộ')
    await wrapper.get('.warning-toggle').trigger('click')
    expect(wrapper.get('.warning-toggle').attributes('aria-expanded')).toBe('true')
    expect(wrapper.text()).toContain('Chi tiết đồng bộ')
    expect(wrapper.text()).toContain('Chi tiết tiếp nhận')
    expect(wrapper.findAll('.action')).toHaveLength(2)
    await wrapper.get('.warning-toggle').trigger('click')
    expect(wrapper.text()).not.toContain('Chi tiết đồng bộ')
  })

  it('resets expansion when the warning group changes', async () => {
    const wrapper = mount(WarningBatch, { props: { warnings }, global })
    await wrapper.get('.warning-toggle').trigger('click')
    await wrapper.setProps({ warnings: [warnings[0]!, { id: 'disabled', title: 'Chưa bật', detail: 'Bật theo dõi' }] })
    expect(wrapper.get('.warning-toggle').attributes('aria-expanded')).toBe('false')
  })
})
