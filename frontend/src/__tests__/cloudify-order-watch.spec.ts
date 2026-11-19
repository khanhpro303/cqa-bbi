// @vitest-environment happy-dom
import { afterEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import { defineComponent, h } from 'vue'
import CloudifyOrderWatchCard from '../components/CloudifyOrderWatchCard.vue'

const puts: any[] = []
const payload = () => ({
  settings: {
    watch_enabled: true, notify_enabled: false, notify_kinds: ['created', 'status_changed'], window_days: 7, poll_seconds: 30,
    template_created: 'a', template_status_changed: 'b', template_updated: 'c',
  },
  status: { seeded_at: '2026-11-18T06:00:00Z', last_poll_at: '2026-11-18T06:10:00Z', last_ok_at: '2026-11-18T06:10:00Z', last_error: '', last_count: 12 },
  webhook: { has_secret: false, path: '/api/v1/webhooks/cloudify/tenant' },
  events: [
    { ID: 'e1', Kind: 'status_changed', OrderCode: 'ĐH1', CustomerCode: 'KH1', PrevStatus: '1', Status: '4', DeliveryStatus: 'skipped', DeliveryNote: 'notify_off', CreatedAt: '2026-11-18T06:10:00Z' },
    { ID: 'e2', Kind: 'created', OrderCode: 'ĐH2', CustomerCode: 'KH2', PrevStatus: '', Status: '1', DeliveryStatus: 'sent', DeliveryNote: '', CreatedAt: '2026-11-18T06:10:00Z' },
  ],
})
vi.mock('../api', () => ({ default: {
  get: async () => ({ data: payload() }),
  put: async (_url: string, body: any) => { puts.push(body); const d = payload(); Object.assign(d.settings, body); return { data: d } },
  post: async () => ({ data: { path: '/api/v1/webhooks/cloudify/tenant?secret=s' } }),
} }))

const Btn = defineComponent({ emits: ['click'], setup: (_, { slots, emit }) => () => h('button', { onClick: () => emit('click') }, slots.default?.()) })
const stubs = {
  ...Object.fromEntries(['VCard', 'VDialog', 'VIcon', 'VSpacer', 'VAlert', 'VRow', 'VCol', 'VSwitch', 'VTextField', 'VSelect', 'VTextarea', 'VDivider', 'VTable']
    .map(name => [name, true])),
  VBtn: Btn,
}

let wrapper: VueWrapper | undefined
afterEach(() => { wrapper?.unmount(); wrapper = undefined; puts.length = 0 })

describe('Cloudify order watch card', () => {
  it('shows status and recent changes, and saves the settings', async () => {
    wrapper = mount(CloudifyOrderWatchCard, { props: { tenantId: 'tenant' }, global: { stubs, renderStubDefaultSlot: true } })
    await flushPromises()
    expect(wrapper.get('[data-testid="watch-status"]').text()).toContain('12 đơn')
    const events = wrapper.get('[data-testid="watch-events"]').text()
    expect(events).toContain('Đang thực hiện → Đang giao')
    expect(events).toContain('Chưa bật nhắn khách')
    expect(events).toContain('Đã gửi')
    await wrapper.get('[data-testid="save-watch"]').trigger('click')
    await flushPromises()
    expect(puts).toHaveLength(1)
    expect(puts[0]).toMatchObject({ watch_enabled: true, notify_enabled: false, poll_seconds: 30, window_days: 7 })
  })
})
