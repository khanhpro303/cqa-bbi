// @vitest-environment happy-dom
import { afterEach, expect, it, vi } from 'vitest'
import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import { createPinia } from 'pinia'
import { defineComponent, h } from 'vue'
import api from '../api'
import { useAuthStore } from '../stores/auth'
import CRM from '../views/CRM.vue'

vi.mock('../api', () => ({ default: { get: vi.fn(), post: vi.fn() } }))
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
  expect(streams).toHaveLength(4)
  expect(wrapper.text()).toContain('Khách mới')
  expect(wrapper.text()).toContain('Zalo ID: zalo-user')
  expect(wrapper.text()).not.toContain('Đã thêm vào CRM')
})

it('links a synced personal Zalo group with the existing CRM customer code selector', async () => {
  const source = { id: 'conversation', name: 'Nhóm khách S084', channel_id: 'personal', channel_name: 'Zalo cá nhân' }
  const personalChannel = { id: source.channel_id, channel_type: 'personal_zalo_import', name: source.channel_name, is_active: true }
  vi.mocked(api.get).mockImplementation(async (url) => {
    if (url.endsWith('/channels')) return { data: [personalChannel] }
    if (url.endsWith('/crm/groups/personal-zalo-conversations')) return { data: [source] }
    if (url.endsWith('/crm/customer-profiles')) return { data: ['S084'] }
    return { data: [] }
  })
  vi.mocked(api.post).mockResolvedValue({ data: {} })
  vi.stubGlobal('localStorage', { getItem: () => null })
  vi.stubGlobal('fetch', vi.fn(async () => ({ ok: false, status: 401 })))
  const form = defineComponent({
    setup(_, { expose, slots }) {
      expose({ validate: async () => ({ valid: true }) })
      return () => h('div', slots.default?.())
    },
  })
  const autocomplete = defineComponent({
    props: ['modelValue', 'items'],
    emits: ['update:modelValue', 'update:search'],
    setup: props => () => h('div', { 'data-model': String(props.modelValue ?? '') }),
  })
  wrapper = mount(CRM, {
    global: {
      plugins: [createPinia()], mocks: { $t: (key: string) => key },
      stubs: { ...stubs, VForm: form, VAutocomplete: autocomplete }, renderStubDefaultSlot: true,
    },
  })
  await flushPromises()
  await wrapper.findAll('button').find(button => button.text() === 'Thêm nhóm mới')!.trigger('click')
  await vi.waitFor(() => expect(api.get).toHaveBeenCalledWith('/tenants/tenant/crm/groups/personal-zalo-conversations', expect.any(Object)))
  await flushPromises()
  const pickGroup = wrapper.findAllComponents(autocomplete).find(control => control.attributes('data-testid') === 'crm-personal-group-conversation')!
  expect(pickGroup.props('items')).toEqual([{ value: source.id, title: `${source.name} · ${source.channel_name}` }])
  pickGroup.vm.$emit('update:modelValue', source.id)
  const pickCustomer = wrapper.findAllComponents(autocomplete).find(control => control.attributes('data-testid') === 'crm-group-customer-codes')!
  pickCustomer.vm.$emit('update:modelValue', ['S084'])
  await wrapper.findAll('button').find(button => button.text() === 'Lưu lại')!.trigger('click')
  await flushPromises()
  expect(api.post).toHaveBeenCalledWith('/tenants/tenant/crm/groups', {
    name: source.name, description: '', customer_codes: ['S084'], personal_conversation_id: source.id,
  })
})

it('shows a newly discovered personal Zalo group in CRM and assigns a customer before any message', async () => {
  const source = { zalo_group_id: '9001', name: 'Nhóm vừa tạo', channel_id: 'personal', channel_name: 'Zalo cá nhân' }
  const personalChannel = { id: source.channel_id, channel_type: 'personal_zalo_import', name: source.channel_name, is_active: true }
  let roster: typeof source[] = []
  vi.mocked(api.get).mockImplementation(async (url) => {
    if (url.endsWith('/channels')) return { data: [personalChannel] }
    if (url.endsWith('/crm/groups/personal-zalo-conversations')) return { data: roster }
    return { data: [] }
  })
  vi.mocked(api.post).mockImplementation(async () => {
    roster = []
    return { data: {} }
  })
  const streams: ReadableStreamDefaultController<Uint8Array>[] = []
  vi.stubGlobal('localStorage', { getItem: () => null })
  vi.stubGlobal('fetch', vi.fn(async () => ({
    ok: true,
    body: new ReadableStream<Uint8Array>({ start(controller) { streams.push(controller) } }),
  })))
  const form = defineComponent({
    setup(_, { expose, slots }) {
      expose({ validate: async () => ({ valid: true }) })
      return () => h('div', slots.default?.())
    },
  })
  const autocomplete = defineComponent({ emits: ['update:modelValue'], setup: () => () => h('div') })
  const listItem = defineComponent({
    props: ['title', 'subtitle'],
    setup: (props, { slots }) => () => h('div', [props.title, props.subtitle, slots.append?.()]),
  })
  const pinia = createPinia()
  useAuthStore(pinia).tenantPerms = { role: 'admin', permissions: {} }
  wrapper = mount(CRM, {
    global: {
      plugins: [pinia], mocks: { $t: (key: string) => key },
      stubs: { ...stubs, VForm: form, VAutocomplete: autocomplete, VListItem: listItem }, renderStubDefaultSlot: true,
    },
  })
  await vi.waitFor(() => expect(wrapper!.text()).toContain('Chưa có nhóm Zalo cá nhân cần gắn mã khách.'))
  roster = [source]
  for (const stream of streams) {
    stream.enqueue(new TextEncoder().encode('event: crm_personal_zalo_groups_updated\ndata: {}\n\n'))
  }
  const action = '[aria-label="Gắn mã khách cho Nhóm vừa tạo"]'
  await vi.waitFor(() => expect(wrapper!.find(action).exists()).toBe(true))
  await wrapper.get(action).trigger('click')
  expect(wrapper.find('[data-testid="crm-personal-group-conversation"]').exists()).toBe(false)
  const customer = wrapper.findAllComponents(autocomplete).find(control => control.attributes('data-testid') === 'crm-group-customer-codes')!
  customer.vm.$emit('update:modelValue', ['S084'])
  await wrapper.findAll('button').find(button => button.text() === 'Lưu lại')!.trigger('click')
  expect(api.post).toHaveBeenLastCalledWith('/tenants/tenant/crm/groups', {
    name: source.name, description: '', customer_codes: ['S084'], personal_zalo_group_id: source.zalo_group_id, channel_id: source.channel_id,
  })
  await vi.waitFor(() => expect(wrapper!.find(action).exists()).toBe(false))
})
