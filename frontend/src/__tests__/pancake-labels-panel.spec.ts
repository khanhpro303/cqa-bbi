// @vitest-environment happy-dom
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { defineComponent, h } from 'vue'
import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import { createI18n } from 'vue-i18n'
import MetaLabelsPanel from '../components/MetaLabelsPanel.vue'
import viMessages from '../i18n/vi'
import enMessages from '../i18n/en'

const calls: string[] = []
vi.mock('../api', () => ({ default: { get: async (url: string, options: any) => {
  calls.push(`${url}?${options?.params?.channel_id || ''}`)
  return { data: {
    source: 'pancake',
    pages: [{ id: 'page-a', name: 'Gara', is_active: true }], channel_id: options?.params?.channel_id || '', enabled: true,
    meta_enabled: false, has_page_token: true, reconciled_at: '2026-11-07T08:00:00Z', unmatched: 2,
    connection: { has_user_token: true, socket_enabled: true, socket_status: 'connected', socket_error: '', socket_connected_at: null, socket_event_at: '2026-11-07T08:10:00Z' },
    sync: { status: 'success' }, intake: { total: 0 }, rules: [], catalog: [], intake_label_ids: [],
    counts: { total: 1, unclassified: 0, qualified: 0, unqualified: 0, potential: 1, conflict: 0, unknown: 0 },
    rows: [{ conversation_id: 'c1', customer_name: 'Khách', channel_id: 'page-a', channel_name: 'Gara', classification: 'potential', error: '',
      labels: [{ id: '7', page_label_name: 'Hỏi giá' }], intake_labels: [], tracking_labels: [{ id: '7', page_label_name: 'Hỏi giá' }], intake_captured: true }],
  } }
} } }))
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
let wrapper: VueWrapper | undefined
beforeEach(() => { calls.length = 0; localStorage.setItem('cqa.labelSource', 'pancake') })
afterEach(() => { wrapper?.unmount(); wrapper = undefined; localStorage.clear() })

describe('Pancake label source', () => {
  it('reads the Pancake report and reuses the Meta layout with Pancake texts', async () => {
    const i18n = createI18n({ legacy: false, locale: 'vi', messages: { vi: viMessages, en: enMessages } })
    const stubs = Object.fromEntries(['VCard', 'VCardTitle', 'VCardText', 'VRow', 'VCol', 'VAvatar', 'VChip', 'VBtn', 'VBtnToggle', 'VAlert'].map(name => [name, Container]))
    wrapper = mount(MetaLabelsPanel, { global: { plugins: [i18n], stubs: {
      ...stubs, VDataTable: Table, VTooltip: true, VDialog: true, WarningBatch: true, VSelect: true, VTextField: true, VIcon: true,
    } } })
    await flushPromises()
    expect(calls[0]).toBe('/tenants/tenant/pancake-labels?')
    // All-pages view asks to pick one Page.
    expect(wrapper.text()).toContain('Chọn một Fanpage')
    ;(wrapper.vm as any).channelId = 'page-a'
    await flushPromises()
    expect(calls.at(-1)).toBe('/tenants/tenant/pancake-labels?page-a')
    expect(wrapper.text()).toContain('Phân loại thủ công theo tag Pancake')
    expect(wrapper.text()).toContain('Websocket đang kết nối')
    expect(wrapper.text()).toContain('2 khách có tag trên Pancake')
    expect(wrapper.findAll('th').map(th => th.text())).toContain('Tag trên Pancake')
    expect(wrapper.get('[data-column="labels"]').text()).toContain('Hỏi giá')
    expect(wrapper.text()).not.toContain('Meta Inbox')
  })
})
