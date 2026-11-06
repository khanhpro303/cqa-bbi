// @vitest-environment happy-dom
import { afterEach, describe, expect, it, vi } from 'vitest'
import { defineComponent, h, type PropType } from 'vue'
import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import { createI18n } from 'vue-i18n'
import MessengerJourneyPanel from '../components/MessengerJourneyPanel.vue'
import en from '../i18n/en'
import viMessages from '../i18n/vi'
import { normalizeMessengerJourneyPage, journeyPayloadLabels } from '../utils/messenger-journey'

const apiGet = vi.hoisted(() => vi.fn())
vi.mock('../api', () => ({ default: { get: apiGet } }))

const Container = defineComponent({ setup: (_, { slots }) => () => h('div', slots.default?.()) })
const Button = defineComponent({
  emits: ['click'],
  setup: (_, { slots, emit }) => () => h('button', { onClick: () => emit('click') }, slots.default?.()),
})
const Chip = defineComponent({ setup: (_, { slots }) => () => h('span', slots.default?.()) })
const Transition = defineComponent({ setup: (_, { slots }) => () => slots.default?.() })
const Icon = defineComponent({
  props: { icon: { type: String as PropType<string>, default: '' } },
  setup: (props, { slots }) => () => h('i', props.icon || slots.default?.()),
})

function response(conversation: string, hasMore = false, cursor = '') {
  return { data: {
    summary: {
      latest_referral: { occurred_at: '2026-11-06T02:00:00Z', observed_at: '2026-11-06T02:01:00Z', payload: { ad_id: `ad-${conversation}` } },
      first_referral: null,
      labels: [`label-${conversation}`],
      classification: 'potential',
      last_observed_at: '2026-11-06T02:01:00Z',
    },
    events: [{
      event_id: `event-${conversation}`,
      kind: 'referral',
      observed_at: '2026-11-06T02:01:00Z',
      occurred_at: '2026-11-06T02:00:00Z',
      payload: { ad_id: `ad-${conversation}` },
    }],
    next_cursor: cursor,
    has_more: hasMore,
  } }
}

function mountPanel(conversationId = 'one') {
  const i18n = createI18n({ legacy: false, locale: 'vi', messages: { vi: viMessages, en } })
  return mount(MessengerJourneyPanel, {
    props: { tenantId: 'tenant', conversationId },
    global: { plugins: [i18n], stubs: {
      VCard: Container,
      VBtn: Button,
      VChip: Chip,
      VIcon: Icon,
      VProgressCircular: true,
      VExpandTransition: Transition,
    } },
  })
}

let wrapper: VueWrapper | undefined
afterEach(() => {
  wrapper?.unmount()
  wrapper = undefined
  apiGet.mockReset()
})

describe('MessengerJourneyPanel', () => {
  it('ignores a late response after the selected conversation changes', async () => {
    let resolveFirst!: (value: unknown) => void
    apiGet
      .mockReturnValueOnce(new Promise(resolve => { resolveFirst = resolve }))
      .mockResolvedValueOnce(response('two'))
    wrapper = mountPanel('one')
    await wrapper.setProps({ conversationId: 'two' })
    await flushPromises()
    expect(wrapper.text()).toContain('label-two')
    resolveFirst(response('one'))
    await flushPromises()
    expect(wrapper.text()).toContain('label-two')
    expect(wrapper.text()).not.toContain('label-one')
  })

  it('loads older events with the opaque cursor and avoids duplicates', async () => {
    apiGet
      .mockResolvedValueOnce(response('one', true, 'next-page'))
      .mockResolvedValueOnce({ data: {
        summary: {},
        events: [
          { event_id: 'event-one', kind: 'referral', observed_at: '2026-11-06T02:01:00Z', payload: { ad_id: 'duplicate' } },
          { event_id: 'older', kind: 'labels', observed_at: '2026-11-05T02:01:00Z', payload: { labels: ['Cũ'] } },
        ],
        next_cursor: '', has_more: false,
      } })
    wrapper = mountPanel()
    await flushPromises()
    await wrapper.get('.journey-toggle').trigger('click')
    await wrapper.get('.journey-detail button').trigger('click')
    await flushPromises()
    expect(apiGet).toHaveBeenLastCalledWith('/tenants/tenant/conversations/one/journey', { params: { limit: 20, after: 'next-page' } })
    expect(wrapper.findAll('.journey-event')).toHaveLength(2)
    expect(wrapper.text()).toContain('Cũ')
  })

  it('hides an unregistered feature and offers retry for other failures', async () => {
    apiGet.mockRejectedValueOnce({ isAxiosError: true, response: { status: 404 } })
    wrapper = mountPanel()
    await flushPromises()
    expect(wrapper.html()).toBe('<!--v-if-->')
    wrapper.unmount()

    apiGet.mockRejectedValueOnce({ isAxiosError: true, response: { status: 503 } }).mockResolvedValueOnce(response('one'))
    wrapper = mountPanel()
    await flushPromises()
    await wrapper.get('.journey-toggle').trigger('click')
    expect(wrapper.text()).toContain('Chưa tải được hành trình khách hàng')
    await wrapper.get('.journey-detail button').trigger('click')
    await flushPromises()
    expect(wrapper.text()).toContain('label-one')
  })
})

describe('normalizeMessengerJourneyPage', () => {
  it('reads the locked event envelope without exposing arbitrary payload fields', () => {
    const page = normalizeMessengerJourneyPage(response('one').data)
    expect(page.summary.latest_referral?.ad_id).toBe('ad-one')
    expect(page.events[0]).toMatchObject({ event_id: 'event-one', kind: 'referral' })
  })
})


it('renders actual Meta label display names from saved history', () => {
  expect(journeyPayloadLabels({ labels: [{ id: '1', page_label_name: 'Tiềm năng' }] })).toEqual(['Tiềm năng'])
})
