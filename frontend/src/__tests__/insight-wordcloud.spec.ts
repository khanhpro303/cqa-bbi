// @vitest-environment happy-dom
import { createI18n } from 'vue-i18n'
import qualityInsightVi from '../i18n/quality-insight-vi'
import qualityInsightEn from '../i18n/quality-insight-en'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { defineComponent, h } from 'vue'
import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import { createMemoryHistory, createRouter, RouterLink } from 'vue-router'
import InsightWordcloudPanel from '../components/InsightWordcloudPanel.vue'
import insightWordcloudPanelSource from '../components/InsightWordcloudPanel.vue?raw'
import api from '../api'
import type { AggregateItem, ConversationInsight } from '../utils/service-quality'

vi.mock('../api', () => ({ default: { get: vi.fn() } }))

// Preserve Vuetify slots and dialog visibility while exercising real router links.
const Container = defineComponent({ setup: (_, { slots }) => () => h('div', slots.default?.()) })
const Dialog = defineComponent({
  props: { modelValue: Boolean },
  setup: (props, { slots }) => () => props.modelValue ? h('div', { role: 'dialog' }, slots.default?.()) : null,
})
const Button = defineComponent({
  props: ['to'],
  setup: (props, { slots, attrs }) => () => props.to
    ? h(RouterLink, { ...attrs, to: props.to }, slots)
    : h('button', attrs, slots.default?.()),
})
const TextField = defineComponent({
  props: ['modelValue', 'label'],
  emits: ['update:modelValue'],
  setup: (props, { emit }) => () => h('input', {
    value: props.modelValue,
    'aria-label': props.label,
    onInput: (event: Event) => emit('update:modelValue', (event.target as HTMLInputElement).value),
  }),
})
const ShapeWordcloud = defineComponent({
  props: { items: { type: Array as () => AggregateItem[], required: true } },
  emits: ['select'],
  setup: (props, { emit }) => () => h('div', props.items.map(item => h('button', {
    class: 'cloud-word', onClick: () => emit('select', item.key),
  }, item.label))),
})
const stubs = Object.fromEntries([
  'VRow', 'VCol', 'VIcon', 'VCard', 'VCardTitle', 'VCardText', 'VSpacer', 'VDivider',
  'VAlert', 'VChip', 'VProgressCircular',
].map(name => [name, Container]))
const wrappers: VueWrapper[] = []
afterEach(() => { wrappers.splice(0).forEach(wrapper => wrapper.unmount()) })
beforeEach(() => {
  vi.mocked(api.get).mockReset()
  vi.mocked(api.get).mockImplementation(url => Promise.resolve(
    String(url).endsWith('/evaluations') ? { data: { groups: [] } } : { data: { messages: [] } }
  ))
})

const keyword = (key: string, label: string, conversationIds: string[]): AggregateItem => ({
  key, label, conversationIds, count: conversationIds.length,
})
const conversation = (id: string, insight: ConversationInsight = {}) => ({
  conversation_id: id, channel_id: `channel-${id}`, channel_name: 'Kênh bán hàng',
  customer_name: `Khách ${id}`, insight: { summary: `Tóm tắt ${id}`, ...insight },
})
const group = (items: AggregateItem[], kind = 'intent') => ({ kind, title: 'Ý định', icon: 'mdi-chat', items })
async function render(items: AggregateItem[], conversations = [conversation('a'), conversation('b'), conversation('c')]) {
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [{ name: 'messages', path: '/:tenantId/messages', component: Container }],
  })
  await router.push('/tenant-one/messages')
  await router.isReady()
  const i18n = createI18n({ legacy: false, locale: 'vi', messages: { vi: { qualityInsight: qualityInsightVi }, en: { qualityInsight: qualityInsightEn } } })
  const wrapper = mount(InsightWordcloudPanel, {
    props: { groups: [group(items)], conversations, tenantId: 'tenant-one' },
    global: { plugins: [router, i18n], stubs: { ...stubs, ShapeWordcloud, VDialog: Dialog, VBtn: Button, VTextField: TextField } },
  })
  wrappers.push(wrapper)
  return { wrapper, router, i18n }
}
const sourceIds = (wrapper: VueWrapper) => wrapper.findAll('.conversation-heading strong').map(name => name.text())

describe('InsightWordcloudPanel', () => {
  it('updates open keyword details when the application language switches', async () => {
    const { wrapper, i18n } = await render([keyword('order', 'Đặt hàng', ['a'])])
    await wrapper.get('.segment-heading').trigger('click')
    i18n.global.locale.value = 'en'
    await flushPromises()
    expect(wrapper.get('input').attributes('aria-label')).toBe('Search keywords')
    expect(wrapper.text()).toContain('Đặt hàng · 1 conversations')
    expect(wrapper.text()).toContain('Keywords and counts (1)')
    expect(sourceIds(wrapper)).toEqual(['Khách a'])
  })

  it('caps the cloud at 40 words but shows every keyword and its count in segment detail', async () => {
    const items = Array.from({ length: 43 }, (_, index) => keyword(`keyword-${index}`, `Keyword ${index}`, index === 42 ? ['a', 'b'] : ['a']))
    const { wrapper } = await render(items)
    expect(wrapper.findAll('.cloud-word')).toHaveLength(40)
    expect(wrapper.find('[role="dialog"]').exists()).toBe(false)
    await wrapper.get('.segment-heading').trigger('click')
    expect(wrapper.findAll('.keyword-row')).toHaveLength(43)
    const last = wrapper.findAll('.keyword-row')[42]!
    expect(last.get('span').text()).toBe('Keyword 42')
    expect(last.get('strong').text()).toBe('2')
    await last.trigger('click')
    expect(sourceIds(wrapper)).toEqual(['Khách a', 'Khách b'])
  })

  it('keeps a multi-digit keyword count on one line when the label wraps', async () => {
    const { wrapper } = await render([
      keyword('helmet', 'Mũ Bảo Hiểm Fullface Lật Hàm 180độ LS2 FF901 Advant X', Array.from({ length: 47 }, (_, index) => `conversation-${index}`)),
    ], [])
    await wrapper.get('.segment-heading').trigger('click')
    const count = wrapper.get('.keyword-count')
    expect(count.text()).toBe('47')
    expect(insightWordcloudPanelSource).toMatch(/\.keyword-count\s*\{[^}]*flex:\s*0 0 auto;[^}]*white-space:\s*nowrap;/)
  })

  it('keeps the keyword search field at its compact intrinsic height', () => {
    expect(insightWordcloudPanelSource).toContain('class="keyword-search mb-2"')
    expect(insightWordcloudPanelSource).toMatch(/\.keyword-search\s*\{[^}]*flex:\s*0 0 auto;/)
  })

  it('opens the clicked word and switches exact source conversations when selecting another keyword', async () => {
    const { wrapper } = await render([
      keyword('first', 'Hỏi giá', ['a', 'c']), keyword('second', 'Đặt hàng', ['b']),
    ])
    await wrapper.findAll('.cloud-word')[1]!.trigger('click')
    expect(wrapper.get('.keyword-row.selected').text()).toBe('Đặt hàng1')
    expect(sourceIds(wrapper)).toEqual(['Khách b'])
    expect(wrapper.get('.conversation-list').text()).toContain('Tóm tắt b')
    await wrapper.findAll('.keyword-row')[0]!.trigger('click')
    expect(sourceIds(wrapper)).toEqual(['Khách a', 'Khách c'])
    expect(wrapper.get('.keyword-row.selected').attributes('aria-pressed')).toBe('true')
  })

  it('navigates a source link to the messages tab with tenant, conversation and channel', async () => {
    const { wrapper, router } = await render([keyword('order', 'Đặt hàng', ['b'])])
    await wrapper.get('.cloud-word').trigger('click')
    await wrapper.get('.conversation-link').trigger('click')
    await flushPromises()
    expect(router.currentRoute.value.name).toBe('messages')
    expect(router.currentRoute.value.params).toEqual({ tenantId: 'tenant-one' })
    expect(router.currentRoute.value.query).toEqual({ conv: 'b', channel_id: 'channel-b', tab: 'messages' })
  })

  it('expands a conversation card, loads its transcript once, and keeps the evaluation beside it', async () => {
    vi.mocked(api.get).mockImplementation(url => Promise.resolve(
      String(url).endsWith('/evaluations')
        ? { data: { groups: [] } }
        : { data: { messages: [
          { id: 'm1', sender_type: 'customer', sender_name: 'Khách a', content: 'Cho mình hỏi giá', content_type: 'text', sent_at: '2026-09-16T09:00:00+07:00' },
          { id: 'm2', sender_type: 'agent', sender_name: 'Tư vấn viên', content: 'Dạ sản phẩm có giá 500.000đ', content_type: 'text', sent_at: '2026-09-16T09:01:00+07:00' },
        ] } }
    ))
    const { wrapper } = await render([keyword('price', 'Hỏi giá', ['a'])])
    await wrapper.get('.cloud-word').trigger('click')

    const toggle = wrapper.get('.conversation-toggle')
    expect(toggle.attributes('aria-expanded')).toBe('false')
    await toggle.trigger('click')
    await flushPromises()

    expect(api.get).toHaveBeenCalledWith('/tenants/tenant-one/conversations/a/messages')
    expect(api.get).toHaveBeenCalledWith('/tenants/tenant-one/conversations/a/evaluations')
    expect(toggle.attributes('aria-expanded')).toBe('true')
    expect(wrapper.get('.chat-transcript').text()).toContain('Cho mình hỏi giá')
    expect(wrapper.get('.chat-transcript').text()).toContain('Dạ sản phẩm có giá 500.000đ')
    expect(wrapper.get('.conversation-detail').text()).toContain('Đánh giá chi tiết')
    expect(wrapper.get('.conversation-detail').text()).toContain('Tóm tắt a')

    await toggle.trigger('click')
    await toggle.trigger('click')
    await flushPromises()
    expect(api.get).toHaveBeenCalledTimes(2)
  })

  it('renders the intent citation, explanation, and orange border on its source message', async () => {
    vi.mocked(api.get).mockImplementation(url => Promise.resolve(
      String(url).endsWith('/evaluations')
        ? { data: { groups: [{ job_type: 'classification', results: [
          { result_type: 'conversation_insight', detail: JSON.stringify({ summary: 'Tóm tắt a' }) },
          {
            result_type: 'classification_tag',
            rule_name: 'Nhu cầu - Hỏi giá/khuyến mãi',
            evidence: 'giá sao',
            detail: JSON.stringify({ explanation: 'Khách hỏi giá của sản phẩm.' }),
          },
        ] }] } }
        : { data: { messages: [
          { id: 'm1', sender_type: 'agent', sender_name: 'LS2 Helmets Vietnam', content: 'Xin chào bạn', content_type: 'text', sent_at: '2026-09-16T09:00:00+07:00' },
          { id: 'm2', sender_type: 'customer', sender_name: 'Khách a', content: 'giá sao', content_type: 'text', sent_at: '2026-09-16T09:01:00+07:00' },
        ] } }
    ))
    const { wrapper } = await render([keyword('hỏi giá', 'hỏi giá', ['a'])])
    await wrapper.get('.cloud-word').trigger('click')
    await wrapper.get('.conversation-toggle').trigger('click')
    await flushPromises()

    expect(wrapper.get('.classification-evidence').text()).toBe('giá sao')
    expect(wrapper.get('.classification-explanation').text()).toBe('Khách hỏi giá của sản phẩm.')
    expect(wrapper.findAll('.chat-message')[0]!.classes()).not.toContain('chat-message-highlight')
    expect(wrapper.findAll('.chat-message')[1]!.classes()).toContain('chat-message-highlight')
    expect(insightWordcloudPanelSource).toMatch(/\.chat-message-highlight\s*\{[^}]*border:\s*2px solid rgb\(var\(--v-theme-warning\)\)/)
  })

  it('refreshes open detail counts and sources, selects a remaining keyword, and closes removed groups', async () => {
    const { wrapper } = await render([keyword('order', 'Đặt hàng', ['a', 'b']), keyword('price', 'Hỏi giá', ['c'])])
    await wrapper.get('.segment-heading').trigger('click')
    await wrapper.setProps({ groups: [group([keyword('order', 'Đặt hàng', ['b']), keyword('price', 'Hỏi giá', ['c'])])] })
    expect(wrapper.get('.keyword-row.selected').get('strong').text()).toBe('1')
    expect(sourceIds(wrapper)).toEqual(['Khách b'])
    await wrapper.setProps({ groups: [group([keyword('price', 'Hỏi giá', ['c'])])] })
    expect(wrapper.get('.keyword-row.selected').get('span').text()).toBe('Hỏi giá')
    expect(sourceIds(wrapper)).toEqual(['Khách c'])
    await wrapper.setProps({ groups: [] })
    expect(wrapper.find('[role="dialog"]').exists()).toBe(false)
  })

  it('closes detail when tenant changes and when refreshed segment becomes empty', async () => {
    const { wrapper } = await render([keyword('order', 'Đặt hàng', ['a'])])
    await wrapper.get('.cloud-word').trigger('click')
    await wrapper.setProps({ tenantId: 'tenant-two' })
    expect(wrapper.find('[role="dialog"]').exists()).toBe(false)
    await wrapper.get('.cloud-word').trigger('click')
    await wrapper.setProps({ groups: [group([])] })
    expect(wrapper.find('[role="dialog"]').exists()).toBe(false)
  })

  it('renders an empty segment without cloud words or source links', async () => {
    const { wrapper } = await render([])
    expect(wrapper.get('.empty-cloud').text()).toBe('Chưa có dữ liệu trong kỳ')
    expect(wrapper.find('.cloud-word').exists()).toBe(false)
    expect(wrapper.find('.segment-footer').exists()).toBe(false)
    await wrapper.get('.segment-heading').trigger('click')
    expect(wrapper.findAll('.keyword-row')).toHaveLength(0)
    expect(sourceIds(wrapper)).toEqual([])
    expect(wrapper.find('[role="dialog"]').exists()).toBe(false)
  })

  it('shows only matching product evidence and filters the keyword list without changing the selected source', async () => {
    const { wrapper } = await render([
      keyword('sku:sp1', 'Áo xanh · SP1', ['a']), keyword('name:quần', 'Quần', ['b']),
    ], [conversation('a', { products: [
      { name: 'Áo xanh', sku: ' SP1 ', evidence: 'Khách hỏi áo xanh' },
      { name: 'Khác', sku: 'SP2', evidence: 'Không thuộc keyword được chọn' },
    ] }), conversation('b')])
    await wrapper.setProps({ groups: [group([
      keyword('sku:sp1', 'Áo xanh · SP1', ['a']), keyword('name:quần', 'Quần', ['b']),
    ], 'product')] })
    await wrapper.get('.cloud-word').trigger('click')
    await wrapper.get('.conversation-toggle').trigger('click')
    await flushPromises()
    expect(wrapper.get('.conversation-list').text()).toContain('Khách hỏi áo xanh')
    expect(wrapper.get('.conversation-list').text()).not.toContain('Không thuộc keyword được chọn')
    await wrapper.get('input').setValue(' QUẦN ')
    expect(wrapper.findAll('.keyword-row')).toHaveLength(1)
    expect(wrapper.get('.keyword-row').get('span').text()).toBe('Quần')
    expect(sourceIds(wrapper)).toEqual(['Khách a'])
  })
})
