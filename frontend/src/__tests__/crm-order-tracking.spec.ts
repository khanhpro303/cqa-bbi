// @vitest-environment happy-dom
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import { defineComponent, h, inject, provide, ref } from 'vue'
import { createI18n } from 'vue-i18n'
import CRMOrderTracking from '../components/CRMOrderTracking.vue'
import vietnamese from '../i18n/vi'

const { get, post, subscribe } = vi.hoisted(() => ({ get: vi.fn(), post: vi.fn(), subscribe: vi.fn() }))
vi.mock('../api', () => ({ default: { get, post } }))
vi.mock('../composables/useTenantEvents', () => ({ useTenantEventRefresh: subscribe }))

const stubs = {
  ...Object.fromEntries(['VCard', 'VCardText', 'VTextField', 'VSelect', 'VAlert', 'VChip',
    'VExpansionPanelText', 'VSpacer', 'VTable', 'VPagination'].map(name => [name, true])),
  VBtn: defineComponent({
    setup: (_, { attrs, slots }) => () => h(attrs.href ? 'a' : 'button', slots.default?.()),
  }),
  VExpansionPanels: defineComponent({
    props: { modelValue: String },
    emits: ['update:modelValue'],
    setup(props, { emit, slots }) {
      provide('toggle-order', (id: string) => emit('update:modelValue', props.modelValue === id ? undefined : id))
      return () => h('div', slots.default?.())
    },
  }),
  VExpansionPanel: defineComponent({
    props: { value: { type: String, required: true } },
    setup(props, { slots }) {
      provide('order-id', props.value)
      return () => h('div', slots.default?.())
    },
  }),
  VExpansionPanelTitle: defineComponent({
    setup: (_, { slots }) => {
      const toggle = inject<(id: string) => void>('toggle-order')
      const id = inject<string>('order-id', '')
      return () => h('button', { onClick: () => toggle?.(id) }, slots.default?.())
    },
  }),
  VFileInput: defineComponent({
    emits: ['update:modelValue'],
    setup: (_, { emit }) => {
      const name = ref('')
      return () => h('label', [
        h('input', {
          type: 'file',
          onChange(event: Event) {
            const file = (event.target as HTMLInputElement).files?.[0]
            name.value = file?.name ?? ''
            emit('update:modelValue', file)
          },
        }),
        name.value,
      ])
    },
  }),
}

const endpoint = '/tenants/tenant-a/crm/order-tracking'
const baseOrder = {
  id: 'outbox-1',
  order_code: 'ĐH000016',
  customer_code: 'S084',
  customer_name: 'Chợ Bảo Hộ',
  group_name: 'Đại lý S084',
  status_name: 'Đang thực hiện',
  order_date: '2026-11-25',
  delivered_at: '2026-11-25T01:30:00Z',
}
const confirmation = {
  sender_name: 'Khách S084',
  sender_id: 'zalo-1',
  text: 'OK, chị duyệt đơn này',
  message_id: 'zalo-message-1',
  quote_message_id: 'zalo-notification-1',
  confirmed_at: '2026-11-25T02:00:00Z',
  has_evidence: true,
}
const detail = {
  status_name: 'Đang giao',
  total: 2910000,
  note: 'Giao buổi sáng',
  lines: [{ sku: 'AQ12-7.5', name: 'Bình ắc quy', unit: 'Bình', quantity: 6, unit_price: 485000, total: 2910000 }],
}
function list(order: typeof baseOrder & { confirmation?: typeof confirmation } = { ...baseOrder, confirmation }) {
  return { data: { data: [order], total: 1, page: 1, page_size: 20 } }
}

let wrapper: VueWrapper
const button = (label: string) => wrapper.findAll('button').find(item => item.text().includes(label))!

beforeEach(() => {
  get.mockReset()
  post.mockReset()
  subscribe.mockReset()
})

afterEach(() => {
  wrapper?.unmount()
  vi.restoreAllMocks()
})

async function render(canAttachEvidence = false) {
  wrapper = mount(CRMOrderTracking, {
    props: { tenantId: 'tenant-a', canAttachEvidence },
    global: {
      plugins: [createI18n({ legacy: false, locale: 'vi', messages: { vi: vietnamese } })],
      stubs,
      renderStubDefaultSlot: true,
    },
  })
  await flushPromises()
}

it('looks up live Cloudify details only when the order opens and loads evidence separately', async () => {
  get.mockResolvedValueOnce(list())
    .mockRejectedValueOnce(new Error('Cloudify unavailable'))
    .mockResolvedValueOnce({ data: new Blob(['png'], { type: 'image/png' }) })
    .mockResolvedValueOnce({ data: { data: detail } })
  const createURL = vi.spyOn(URL, 'createObjectURL').mockReturnValue('blob:confirmation')
  const revokeURL = vi.spyOn(URL, 'revokeObjectURL')
  await render()
  expect(wrapper.text()).toContain('Khách đã xác nhận')
  expect(wrapper.text()).toContain('Đang thực hiện')
  expect(get).toHaveBeenCalledTimes(1)

  await button('ĐH000016').trigger('click')
  await flushPromises()
  expect(get).toHaveBeenNthCalledWith(2, `${endpoint}/outbox-1`, expect.anything())
  expect(wrapper.text()).toContain('Không tải được chi tiết đơn hàng trực tiếp từ Cloudify.')
  expect(wrapper.text()).toContain(confirmation.text)
  expect(wrapper.text()).toContain(confirmation.quote_message_id)

  await button('Xem ảnh chụp Zalo').trigger('click')
  await flushPromises()
  expect(get).toHaveBeenNthCalledWith(3, `${endpoint}/outbox-1/evidence`, expect.objectContaining({ responseType: 'blob' }))
  expect(createURL).toHaveBeenCalledTimes(1)
  expect(wrapper.get('img').attributes('src')).toBe('blob:confirmation')
  expect(wrapper.get('img').attributes('alt')).toBe('Ảnh chụp màn hình tin xác nhận trên Zalo')

  await button('Thử lại').trigger('click')
  await flushPromises()
  expect(wrapper.text()).toContain('Đang giao')
  expect(wrapper.text()).toContain('Bình ắc quy')
  expect(wrapper.text()).toContain('Giao buổi sáng')

  await button('ĐH000016').trigger('click')
  expect(revokeURL).toHaveBeenCalledWith('blob:confirmation')
  expect(wrapper.find('img').exists()).toBe(false)
})

it('refreshes confirmation from tenant events while keeping the open order', async () => {
  get.mockResolvedValueOnce(list(baseOrder))
    .mockResolvedValueOnce({ data: { data: detail } })
    .mockResolvedValueOnce(list({ ...baseOrder, status_name: 'Hoàn thành', confirmation }))
  await render()
  expect(wrapper.text()).toContain('Chờ khách xác nhận')
  await button('ĐH000016').trigger('click')
  await flushPromises()

  expect(subscribe).toHaveBeenCalledWith(expect.any(Function), 'cloudify_orders_updated', expect.any(Function))
  await subscribe.mock.calls[0]![2]()
  await flushPromises()
  expect(wrapper.text()).toContain('Hoàn thành')
  expect(wrapper.text()).toContain(confirmation.text)
  expect(wrapper.text()).toContain('Bình ắc quy')
  expect(get).toHaveBeenCalledTimes(3)
})

it('requires settings write permission to attach an actual screenshot', async () => {
  get.mockResolvedValueOnce(list({ ...baseOrder, confirmation: { ...confirmation, has_evidence: false } }))
    .mockResolvedValueOnce({ data: { data: detail } })
  await render()
  await button('ĐH000016').trigger('click')
  await flushPromises()
  expect(wrapper.text()).toContain('Chưa có ảnh chụp màn hình Zalo.')
  expect(wrapper.find('input[type=file]').exists()).toBe(false)
  expect(button('Xem ảnh chụp Zalo')).toBeUndefined()
})

it('uploads the selected JPEG as multipart, then previews and downloads it unchanged', async () => {
  get.mockResolvedValueOnce(list({ ...baseOrder, confirmation: { ...confirmation, has_evidence: false } }))
    .mockResolvedValueOnce({ data: { data: detail } })
    .mockResolvedValueOnce({ data: new Blob(['jpeg'], { type: 'image/jpeg' }) })
  post.mockResolvedValueOnce({ status: 204 })
  vi.spyOn(URL, 'createObjectURL').mockReturnValue('blob:screenshot')
  await render(true)
  await button('ĐH000016').trigger('click')
  await flushPromises()
  const fileInput = wrapper.get('input[type=file]')
  const file = new File(['jpeg'], 'confirmation.jpg', { type: 'image/jpeg' })
  Object.defineProperty(fileInput.element, 'files', { value: [file] })
  await fileInput.trigger('change')
  expect(wrapper.text()).toContain('confirmation.jpg')
  await button('Lưu ảnh chụp').trigger('click')
  await flushPromises()

  expect(post).toHaveBeenCalledWith(`${endpoint}/outbox-1/evidence`, expect.any(FormData), expect.anything())
  const uploaded = post.mock.calls[0]![1].get('image') as File
  expect(uploaded).toMatchObject({ name: file.name, type: file.type, size: file.size })
  expect(await uploaded.arrayBuffer()).toEqual(await file.arrayBuffer())
  expect(wrapper.get('img').attributes('src')).toBe('blob:screenshot')
  expect(wrapper.get('a[download]').attributes('download')).toBe('zalo-ĐH000016.jpg')
  expect(wrapper.find('input[type=file]').exists()).toBe(false)
})
