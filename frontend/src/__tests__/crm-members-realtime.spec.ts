// @vitest-environment happy-dom
import { afterEach, expect, it, vi } from 'vitest'
import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import { createPinia } from 'pinia'
import { defineComponent, h } from 'vue'
import api from '../api'
import CRM from '../views/CRM.vue'

vi.mock('../api', () => ({ default: { get: vi.fn() } }))
vi.mock('vue-router', async (original) => ({
  ...await original(),
  useRoute: () => ({ params: { tenantId: 'tenant' }, query: {} }),
}))
vi.mock('vue-i18n', () => ({ useI18n: () => ({ t: (key: string) => key }) }))

const stubs = {
  ...Object.fromEntries([
    'VAutocomplete', 'VAvatar', 'VBtnToggle', 'VCard', 'VCardActions', 'VCardText', 'VCardTitle',
    'VCheckbox', 'VChip', 'VCol', 'VDivider', 'VForm', 'VIcon', 'VImg', 'VList', 'VListItem',
    'VListItemSubtitle', 'VListItemTitle', 'VPagination', 'VProgressCircular', 'VRow', 'VSelect',
    'VSnackbar', 'VSpacer', 'VSwitch', 'VTab', 'VTable', 'VTabs', 'VTextField', 'VTextarea',
    'VWindow', 'VWindowItem', 'CampaignList', 'CampaignDashboard', 'ProductGroupRequiredModal',
  ].map(name => [name, true])),
  VBtn: defineComponent({
    emits: ['click'],
    setup: (_, { emit, slots }) => () => h('button', { onClick: () => emit('click') }, slots.default?.()),
  }),
  VDialog: defineComponent({
    props: { modelValue: Boolean },
    setup: (props, { slots }) => () => props.modelValue ? h('div', slots.default?.()) : null,
  }),
}

let wrapper: VueWrapper | undefined
afterEach(() => {
  wrapper?.unmount()
  wrapper = undefined
  vi.unstubAllGlobals()
})

it('refreshes the group count and open member dialog after an addition and after coming online', async () => {
  const customer = { id: 'customer', name: 'Khách mới', zalo_user_id: 'zalo-user', customer_code: 'KH01' }
  let groupCustomers: typeof customer[] = []
  let zaloMembers: typeof customer[] = []
  vi.mocked(api.get).mockImplementation(async (url) => {
    if (url.endsWith('/crm/groups')) {
      return { data: [{ id: 'group', name: 'Nhóm thử', zalo_group_id: 'gmf', customers: groupCustomers }] }
    }
    if (url.endsWith('/members')) return { data: { customers: zaloMembers } }
    return { data: [] }
  })

  const streams: { controller: ReadableStreamDefaultController<Uint8Array>; signal: AbortSignal }[] = []
  vi.stubGlobal('localStorage', { getItem: () => null })
  vi.stubGlobal('fetch', vi.fn(async (_url: string, { signal }: { signal: AbortSignal }) => ({
    ok: true,
    body: new ReadableStream<Uint8Array>({ start(controller) { streams.push({ controller, signal }) } }),
  })))

  wrapper = mount(CRM, {
    global: { plugins: [createPinia()], mocks: { $t: (key: string) => key }, stubs, renderStubDefaultSlot: true },
  })
  await flushPromises()
  expect(wrapper.find('[aria-label="0 Khách hàng"]').exists()).toBe(true)
  await wrapper.get('[title="Quản lý thành viên"]').trigger('click')
  await flushPromises()

  groupCustomers = [customer]
  const original = streams[0]!
  original.controller.enqueue(new TextEncoder().encode('event: crm_group_members_updated\ndata: {}\n\n'))
  await flushPromises()
  expect(wrapper.find('[aria-label="1 Khách hàng"]').exists()).toBe(true)
  expect(wrapper.text()).toContain('Khách mới')
  expect(wrapper.text()).toContain('Đã thêm vào CRM')

  zaloMembers = [customer]
  window.dispatchEvent(new Event('online'))
  await flushPromises()
  expect(original.signal.aborted).toBe(true)
  expect(streams).toHaveLength(2)
  expect(wrapper.text()).toContain('Khách mới')
  expect(wrapper.text()).toContain('Zalo ID: zalo-user')
  expect(wrapper.text()).not.toContain('Đã thêm vào CRM')
})
