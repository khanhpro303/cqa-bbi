// @vitest-environment happy-dom
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
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
let snapshot: ReturnType<typeof payload>
let saveResponse: Promise<{ data: ReturnType<typeof payload> }> | undefined
const streams: { controller: ReadableStreamDefaultController<Uint8Array>; signal: AbortSignal }[] = []
vi.mock('../api', () => ({ default: {
  get: async () => ({ data: snapshot }),
  put: async (_url: string, body: any) => {
    puts.push(body)
    if (saveResponse) return saveResponse
    const data = structuredClone(snapshot)
    Object.assign(data.settings, body)
    snapshot = data
    return { data }
  },
  post: async () => ({ data: { path: '/api/v1/webhooks/cloudify/tenant?secret=s' } }),
} }))

const Btn = defineComponent({ emits: ['click'], setup: (_, { slots, emit }) => () => h('button', { onClick: () => emit('click') }, slots.default?.()) })
const TextField = defineComponent({
  props: { modelValue: [Number, String], label: String },
  emits: ['update:modelValue'],
  setup: (props, { emit }) => () => h('input', {
    'aria-label': props.label,
    value: props.modelValue,
    onInput: (event: Event) => emit('update:modelValue', (event.target as HTMLInputElement).value),
  }),
})
const Switch = defineComponent({
  props: { modelValue: Boolean },
  emits: ['update:modelValue'],
  setup: (props, { emit }) => () => h('input', {
    type: 'checkbox',
    checked: props.modelValue,
    onChange: (event: Event) => emit('update:modelValue', (event.target as HTMLInputElement).checked),
  }),
})
const stubs = {
  ...Object.fromEntries(['VCard', 'VChip', 'VDialog', 'VIcon', 'VSpacer', 'VAlert', 'VRow', 'VCol', 'VSelect', 'VTextarea', 'VDivider', 'VTable']
    .map(name => [name, true])),
  VBtn: Btn,
  VTextField: TextField,
  VSwitch: Switch,
}

let wrapper: VueWrapper | undefined
beforeEach(() => {
  snapshot = payload()
  vi.stubGlobal('localStorage', { getItem: () => null })
  vi.stubGlobal('fetch', vi.fn(async (_url: string, { signal }: { signal: AbortSignal }) => {
    const body = new ReadableStream<Uint8Array>({
      start(controller) { streams.push({ controller, signal }) },
    })
    return { ok: true, body }
  }))
})

afterEach(() => {
  wrapper?.unmount()
  wrapper = undefined
  streams.length = 0
  puts.length = 0
  saveResponse = undefined
  vi.useRealTimers()
  vi.unstubAllGlobals()
})

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

  it('loads the VietQR account and only submits payment fields on a dialog save', async () => {
    Object.assign(snapshot.settings, {
      payment_bank_bin: '970436',
      payment_account_no: '00123456789',
      payment_account_name: 'Công Ty Bình Minh',
    })
    wrapper = mount(CloudifyOrderWatchCard, { props: { tenantId: 'tenant' }, global: { stubs, renderStubDefaultSlot: true } })
    await flushPromises()

    const bin = wrapper.get<HTMLInputElement>('input[aria-label="Mã BIN ngân hàng"]')
    const account = wrapper.get<HTMLInputElement>('input[aria-label="Số tài khoản hoặc bí danh"]')
    const beneficiary = wrapper.get<HTMLInputElement>('input[aria-label="Tên chủ tài khoản"]')
    expect(bin.element.value).toBe('970436')
    expect(account.element.value).toBe('00123456789')
    expect(beneficiary.element.value).toBe('Công Ty Bình Minh')

    await account.setValue('001BBI')
    await wrapper.get('input[data-testid="notify-switch"]').setValue(true)
    await flushPromises()
    expect(puts[0]).toMatchObject({ notify_enabled: true })
    expect(puts[0]).not.toHaveProperty('payment_account_no')
    expect(account.element.value).toBe('001BBI')

    await wrapper.get('[data-testid="save-watch"]').trigger('click')
    await flushPromises()
    expect(puts[1]).toMatchObject({
      payment_bank_bin: '970436',
      payment_account_no: '001BBI',
      payment_account_name: 'Công Ty Bình Minh',
    })

    await bin.setValue('')
    await account.setValue('')
    await beneficiary.setValue('')
    await wrapper.get('[data-testid="save-watch"]').trigger('click')
    await flushPromises()
    expect(puts[2]).toMatchObject({
      payment_bank_bin: '',
      payment_account_no: '',
      payment_account_name: '',
    })
  })

  it('preserves another editor’s payment account when saving unrelated settings', async () => {
    Object.assign(snapshot.settings, {
      payment_bank_bin: '970436',
      payment_account_no: '00123456789',
      payment_account_name: 'Công Ty Bình Minh',
    })
    wrapper = mount(CloudifyOrderWatchCard, { props: { tenantId: 'tenant' }, global: { stubs, renderStubDefaultSlot: true } })
    await flushPromises()
    await wrapper.get('input[aria-label="Chu kỳ quét (giây)"]').setValue('45')

    snapshot = payload()
    Object.assign(snapshot.settings, {
      payment_bank_bin: '970422',
      payment_account_no: '987654321',
      payment_account_name: 'Công Ty Cầu Vồng',
    })
    streams[0]!.controller.enqueue(new TextEncoder().encode('event: cloudify_orders_updated\ndata: {}\n\n'))
    await flushPromises()

    await wrapper.get('[data-testid="save-watch"]').trigger('click')
    await flushPromises()
    expect(puts[0]).toMatchObject({ poll_seconds: 45 })
    expect(Object.keys(puts[0]).filter(key => key.startsWith('payment_'))).toEqual([])
    expect(wrapper.get<HTMLInputElement>('input[aria-label="Mã BIN ngân hàng"]').element.value).toBe('970422')
  })

  it('updates from the scan event, preserves an edit, and resyncs after the stream reconnects', async () => {
    vi.useFakeTimers({ toFake: ['setTimeout', 'clearTimeout'] })
    wrapper = mount(CloudifyOrderWatchCard, { props: { tenantId: 'tenant' }, global: { stubs, renderStubDefaultSlot: true } })
    await flushPromises()
    expect(streams).toHaveLength(1)
    const original = streams[0]!
    await wrapper.get('input[aria-label="Chu kỳ quét (giây)"]').setValue('45')

    snapshot = payload()
    snapshot.status.last_count = 16
    snapshot.status.last_error = 'Cloudify báo lỗi'
    snapshot.settings.poll_seconds = 60
    original.controller.enqueue(new TextEncoder().encode('event: cloudify_orders_updated\ndata: {}\n\n'))
    await flushPromises()
    expect(wrapper.get('[data-testid="watch-status"]').text()).toContain('16 đơn')
    expect(wrapper.get('[data-testid="watch-error"]').text()).toContain('Cloudify báo lỗi')

    original.controller.close()
    await flushPromises()
    snapshot = payload()
    snapshot.status.last_count = 21
    snapshot.settings.poll_seconds = 90
    await vi.advanceTimersByTimeAsync(1000)
    await flushPromises()
    expect(streams).toHaveLength(2)
    expect(wrapper.get('[data-testid="watch-status"]').text()).toContain('21 đơn')
    expect(wrapper.find('[data-testid="watch-error"]').exists()).toBe(false)

    await wrapper.get('[data-testid="save-watch"]').trigger('click')
    await flushPromises()
    expect(puts[0]).toMatchObject({ poll_seconds: 45 })
  })

  it('keeps the latest scan when a settings save returns an older snapshot', async () => {
    let finishSave!: (result: { data: ReturnType<typeof payload> }) => void
    saveResponse = new Promise(resolve => { finishSave = resolve })
    wrapper = mount(CloudifyOrderWatchCard, { props: { tenantId: 'tenant' }, global: { stubs, renderStubDefaultSlot: true } })
    await flushPromises()
    await wrapper.get('[data-testid="save-watch"]').trigger('click')

    snapshot = payload()
    snapshot.status.last_count = 23
    streams[0]!.controller.enqueue(new TextEncoder().encode('event: cloudify_orders_updated\ndata: {}\n\n'))
    await flushPromises()
    expect(wrapper.get('[data-testid="watch-status"]').text()).toContain('23 đơn')

    finishSave({ data: payload() })
    await flushPromises()
    expect(wrapper.get('[data-testid="watch-status"]').text()).toContain('23 đơn')
  })
})
