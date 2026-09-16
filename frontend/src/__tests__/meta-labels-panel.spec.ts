// @vitest-environment happy-dom
import { afterEach, describe, expect, it, vi } from 'vitest'
import { defineComponent, h, nextTick } from 'vue'
import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import { createI18n } from 'vue-i18n'
import MetaLabelsPanel from '../components/MetaLabelsPanel.vue'
import viMessages from '../i18n/vi'
import enMessages from '../i18n/en'

vi.mock('../api', () => ({ default: { get: async () => ({ data: {
  pages: [{ id: 'page', name: 'Page', is_active: true }], channel_id: 'page', enabled: true,
  sync: { status: 'success' }, intake: { total: 0 }, rules: [], catalog: [],
  counts: { total: 3, unclassified: 0, qualified: 3, unqualified: 0, potential: 0, conflict: 0, unknown: 0 },
  rows: ['An', 'A customer with a much longer name', 'No error'].map((customer_name, i) => ({
    conversation_id: String(i), customer_name, classification: 'qualified', error: i < 2 ? 'Meta error' : '',
    intake_labels: [], tracking_labels: [], intake_captured: true,
  })),
} }) } }))
vi.mock('vue-router', () => ({ useRoute: () => ({ params: { tenantId: 'tenant' } }) }))
vi.mock('../stores/auth', () => ({ useAuthStore: () => ({ canEdit: () => false }) }))
const Container = defineComponent({ setup: (_, { slots }) => () => h('div', slots.default?.()) })
const Table = defineComponent({
  props: ['items', 'headers'],
  setup: (props, { slots }) => () => h('table', [
    h('thead', h('tr', props.headers.map((header: any) => h('th', header.title)))),
    h('tbody', props.items.map((item: any) => h('tr', props.headers.map((header: any) =>
      h('td', { 'data-column': header.key }, slots[`item.${header.key}`]?.({ item })),
    )))),
  ]),
})
const Tooltip = defineComponent({ setup: (_, { slots }) => () => slots.activator?.({ props: {} }) })
let wrapper: VueWrapper | undefined
afterEach(() => { wrapper?.unmount(); wrapper = undefined })

describe('Meta labels conversation table', () => {
  it('reserves a separate error column independent of customer name and updates translations live', async () => {
    const i18n = createI18n({ legacy: false, locale: 'vi', messages: { vi: viMessages, en: enMessages } })
    const stubs = Object.fromEntries(['VCard', 'VCardTitle', 'VCardText', 'VRow', 'VCol', 'VAvatar', 'VChip', 'VBtn'].map(name => [name, Container]))
    wrapper = mount(MetaLabelsPanel, { global: { plugins: [i18n], stubs: {
      ...stubs, VDataTable: Table, VTooltip: Tooltip, VDialog: true, WarningBatch: true,
      VSelect: true, VTextField: true, VIcon: true,
    } } })
    await flushPromises()
    const rows = wrapper.findAll('tbody tr')
    expect(rows).toHaveLength(3)
    for (const row of rows) {
      expect(row.find('[data-column="customer_name"] v-icon-stub').exists()).toBe(false)
      expect(row.findAll('td')[1]!.attributes('data-column')).toBe('customer_error')
    }
    expect(rows[0]!.get('[data-column="customer_error"] v-icon-stub').attributes('icon')).toBe('mdi-close-circle-outline')
    expect(rows[1]!.get('[data-column="customer_error"] v-icon-stub').attributes('aria-label')).toBe('Meta error')
    expect(rows[2]!.find('[data-column="customer_error"] v-icon-stub').exists()).toBe(false)
    expect(wrapper.text()).toContain('Hội thoại theo nhãn Meta')
    const titleIcon = wrapper.get('.meta-conversations-title v-icon-stub')
    expect(titleIcon.attributes('icon')).toBe('mdi-message-text-outline')
    expect(titleIcon.attributes('color')).toBe('primary')
    i18n.global.locale.value = 'en'
    await nextTick()
    expect(wrapper.text()).toContain('Meta')
    expect(wrapper.text()).not.toContain('Hội thoại theo nhãn Meta')
    expect(wrapper.findAll('th')[0]!.text()).toBe('Customer')
    expect(wrapper.text()).not.toContain('Phù hợp')
  })
})
