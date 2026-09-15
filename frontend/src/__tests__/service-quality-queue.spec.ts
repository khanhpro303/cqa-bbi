// @vitest-environment happy-dom
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { defineComponent, h, nextTick } from 'vue'
import { createI18n } from 'vue-i18n'
import qualityVi from '../i18n/service-quality-vi'
import qualityEn from '../i18n/service-quality-en'
import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import ServiceQuality from '../views/ServiceQuality.vue'

const mocks = vi.hoisted(() => ({ get: vi.fn() }))
vi.mock('../api', () => ({ default: { get: mocks.get } }))
vi.mock('vue-router', () => ({ useRoute: () => ({ params: { tenantId: 'tenant-one' } }) }))
vi.mock('../stores/auth', () => ({ useAuthStore: () => ({ canEdit: () => false, canView: () => false }) }))

const Container = defineComponent({ setup: (_, { slots }) => () => h('div', slots.default?.()) })
const Button = defineComponent({ setup: (_, { slots, attrs }) => () => h('button', attrs, slots.default?.()) })
const Field = defineComponent({
  props: ['modelValue', 'label'],
  emits: ['update:modelValue'],
  setup: (props, { emit }) => () => h('input', {
    value: props.modelValue, 'aria-label': props.label,
    onInput: (event: Event) => emit('update:modelValue', (event.target as HTMLInputElement).value),
  }),
})
const Table = defineComponent({
  props: ['items', 'page', 'itemsPerPage', 'headers'],
  emits: ['update:page'],
  setup: (props, { emit }) => () => h('div', { class: 'queue-table', 'data-page': props.page }, [
    ...(props.items as { conversation_id: string; customer_name: string }[]).map(row => h('div', { class: 'queue-row', 'data-id': row.conversation_id }, row.customer_name)),
    h('button', { class: 'next-page', onClick: () => emit('update:page', Number(props.page) + 1) }, 'Trang tiếp'),
  ]),
})
const Hidden = defineComponent({ setup: () => () => null })
const stubs = Object.fromEntries([
  'VIcon', 'VAlert', 'VCard', 'VCardText', 'VCardTitle', 'VCardActions', 'VSpacer', 'VDivider',
  'VRow', 'VCol', 'VAvatar', 'VChip', 'VProgressLinear', 'VSkeletonLoader', 'VSnackbar',
].map(name => [name, Container]))
const wrappers: VueWrapper[] = []
const scrollIntoView = vi.fn()

function report(statuses: string[]) {
  return {
    policy: { timezone: 'Asia/Ho_Chi_Minh', work_start: '08:00', work_end: '22:00', all_day: false, target_minutes: 5, overdue_minutes: 15 },
    generated_at: '2026-09-15T03:00:00Z', from: '2026-09-09T00:00:00Z', to: '2026-09-16T00:00:00Z',
    pages: [{ id: 'page-one', name: 'Fanpage A', last_sync_at: null, last_sync_status: '', is_active: true }],
    summary: { answered: 1, on_time: 1, on_time_percent: 100, median_seconds: 60, p90_seconds: 60, first_median_seconds: 60, waiting: statuses.filter(s => s === 'waiting').length, overdue: statuses.filter(s => s === 'overdue').length, resolved: 0 },
    rows: statuses.map((status, index) => ({ conversation_id: `c${index}`, customer_name: `Khách ${index}`, channel_id: 'page-one', channel_name: 'Fanpage A', last_message_at: null, status, turns: [], insight_stale: false })),
    invalid_timestamps: 0, conversations_scanned: statuses.length,
  }
}

let i18n: ReturnType<typeof createI18n>
async function render(statuses: string[]) {
  i18n = createI18n({ legacy: false, locale: 'vi', messages: { vi: qualityVi, en: qualityEn } })
  mocks.get.mockResolvedValue({ data: report(statuses) })
  const wrapper = mount(ServiceQuality, {
    attachTo: document.body,
    global: { plugins: [i18n], stubs: { ...stubs, VBtn: Button, VSelect: Field, VTextField: Field, VTextarea: Field, VSwitch: Field, VDataTable: Table, VDialog: Hidden, MetaLabelsPanel: Hidden, InsightWordcloudPanel: Hidden } },
  })
  wrappers.push(wrapper)
  await flushPromises()
  return wrapper
}
const queueButton = (wrapper: VueWrapper) => wrapper.findAll('button').find(button => button.text() === 'Xem hàng chờ')!
const rowIds = (wrapper: VueWrapper) => wrapper.findAll('.queue-row').map(row => row.attributes('data-id'))

beforeEach(() => {
  mocks.get.mockReset()
  scrollIntoView.mockReset()
  vi.spyOn(HTMLElement.prototype, 'scrollIntoView').mockImplementation(scrollIntoView)
})
afterEach(() => {
  wrappers.splice(0).forEach(wrapper => wrapper.unmount())
  vi.restoreAllMocks()
  vi.unstubAllGlobals()
})

describe('ServiceQuality queue banner navigation', () => {
  it('updates the module language and duration when the locale changes without reloading', async () => {
    const wrapper = await render(['answered', 'overdue'])
    expect(wrapper.text()).toContain('Chất lượng CSKH Messenger')
    expect(wrapper.text()).toContain('1 phút')
    ;(i18n.global.locale as unknown as { value: string }).value = 'en'
    await nextTick()
    expect(wrapper.text()).toContain('Messenger Customer Service Quality')
    expect(wrapper.text()).toContain('1 min')
    expect(wrapper.text()).toContain('1 overdue conversation')
    expect(wrapper.find('input[aria-label="Status"]').exists()).toBe(true)
    expect(wrapper.findComponent(Table).props('headers')[0].title).toBe('Customer / Page')
    expect(wrapper.get('#conversation-queue h2').text()).toContain('Conversations')
    expect(mocks.get).toHaveBeenCalledTimes(1)
  })

  it('takes the user to the overdue section with matching rows and an accessible heading', async () => {
    const wrapper = await render(['answered', 'waiting', 'overdue', 'resolved', 'overdue'])
    expect(wrapper.get('#conversation-queue h2').text()).toContain('Hội thoại')
    expect(wrapper.get('#conversation-queue [role="status"]').text()).toContain('5 hội thoại')
    await queueButton(wrapper).trigger('click')
    await flushPromises()
    const section = wrapper.get('#conversation-queue')
    expect(queueButton(wrapper).attributes('aria-controls')).toBe('conversation-queue')
    expect(rowIds(wrapper)).toEqual(['c2', 'c4'])
    expect(section.text()).toContain('Hàng chờ quá hạn')
    expect(section.text()).toContain('2 hội thoại')
    expect(section.attributes('tabindex')).toBe('-1')
    const headingId = section.attributes('aria-labelledby')
    expect(headingId).toBeTruthy()
    expect(wrapper.get(`#${headingId}`).text()).toContain('Hàng chờ quá hạn')
    expect(scrollIntoView).toHaveBeenCalledWith({ behavior: 'smooth', block: 'start' })
    expect(scrollIntoView.mock.instances[0]).toBe(section.element)
    expect(document.activeElement).toBe(section.element)
  })

  it('uses the waiting queue when no conversation is overdue', async () => {
    const wrapper = await render(['answered', 'waiting', 'resolved'])
    await queueButton(wrapper).trigger('click')
    await flushPromises()
    expect(rowIds(wrapper)).toEqual(['c1'])
    expect(wrapper.get('#conversation-queue').text()).toContain('Hàng chờ phản hồi')
    expect(wrapper.get('#conversation-queue').text()).toContain('1 hội thoại')
  })

  it('respects reduced motion while focusing the destination without a second scroll', async () => {
    const matchMedia = vi.fn().mockReturnValue({ matches: true })
    vi.stubGlobal('matchMedia', matchMedia)
    const wrapper = await render(['overdue'])
    const section = wrapper.get('#conversation-queue')
    const focus = vi.spyOn(section.element as HTMLElement, 'focus')
    await queueButton(wrapper).trigger('click')
    await flushPromises()
    expect(matchMedia).toHaveBeenCalledWith('(prefers-reduced-motion: reduce)')
    expect(scrollIntoView).toHaveBeenCalledWith({ behavior: 'auto', block: 'start' })
    expect(focus).toHaveBeenCalledWith({ preventScroll: true })
  })

  it('clears search and resets pagination on every click without changing channel or dates', async () => {
    const wrapper = await render([...Array(23).fill('overdue'), 'answered'])
    const channel = wrapper.get('input[aria-label="Fanpage"]')
    const from = wrapper.get('input[aria-label="Từ ngày"]')
    const to = wrapper.get('input[aria-label="Đến ngày"]')
    const search = wrapper.get('input[aria-label="Tìm khách hàng"]')
    await channel.setValue('page-one')
    await from.setValue('2026-09-01')
    await to.setValue('2026-09-12')
    await search.setValue('Không tồn tại')
    await wrapper.get('.next-page').trigger('click')
    expect(rowIds(wrapper)).toEqual([])
    expect(wrapper.get('.queue-table').attributes('data-page')).toBe('2')

    for (let click = 0; click < 2; click++) {
      await queueButton(wrapper).trigger('click')
      await flushPromises()
      expect((search.element as HTMLInputElement).value).toBe('')
      expect(wrapper.get('.queue-table').attributes('data-page')).toBe('1')
      expect(rowIds(wrapper)).toHaveLength(23)
      expect((channel.element as HTMLInputElement).value).toBe('page-one')
      expect((from.element as HTMLInputElement).value).toBe('2026-09-01')
      expect((to.element as HTMLInputElement).value).toBe('2026-09-12')
      if (click === 0) {
        await search.setValue('Không tồn tại')
        await wrapper.get('.next-page').trigger('click')
      }
    }
    expect(scrollIntoView).toHaveBeenCalledTimes(2)
    expect(mocks.get).toHaveBeenCalledTimes(1)
  })
})
