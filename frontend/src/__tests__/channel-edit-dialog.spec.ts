// @vitest-environment happy-dom
import { flushPromises, mount } from '@vue/test-utils'
import { createPinia } from 'pinia'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { computed, defineComponent, h, inject, provide } from 'vue'
import { createI18n } from 'vue-i18n'
import { createMemoryHistory, createRouter } from 'vue-router'
import en from '../i18n/en'
import { useAuthStore } from '../stores/auth'
import { useChannelStore, type Channel } from '../stores/channels'
import App from '../App.vue'
import PersonalZaloBotModePicker from '../components/channels/PersonalZaloBotModePicker.vue'
import Channels from '../views/Channels.vue'
import ChannelDetail from '../views/Channels/ChannelDetail.vue'

const api = vi.hoisted(() => ({ get: vi.fn(), put: vi.fn(), post: vi.fn() }))
vi.mock('../api', () => ({ default: api }))

const activePanel = Symbol('active panel')
const tabs = Symbol('tabs')
const panelStubs = {
  VDialog: { props: ['modelValue'], template: '<div v-if="modelValue" role="dialog"><slot /></div>' },
  VTabs: defineComponent({
    props: { modelValue: String },
    emits: ['update:modelValue'],
    setup(_, { emit, slots }) {
      provide(tabs, (value: string) => emit('update:modelValue', value))
      return () => h('div', slots.default?.())
    },
  }),
  VTab: defineComponent({
    props: { value: { type: String, required: true } },
    setup(props, { slots }) {
      const selectTab = inject<(value: string) => void>(tabs)!
      return () => h('button', { role: 'tab', onClick: () => selectTab(props.value) }, slots.default?.())
    },
  }),
  VWindow: defineComponent({
    props: { modelValue: String },
    setup(props, { slots }) {
      provide(activePanel, computed(() => props.modelValue))
      return () => h('div', slots.default?.())
    },
  }),
  VWindowItem: defineComponent({
    props: { value: String },
    setup(props, { slots }) {
      const selected = inject(activePanel, computed(() => undefined as string | undefined))
      return () => selected.value === props.value ? h('div', slots.default?.()) : null
    },
  }),
}

const toggleStub = {
  props: ['modelValue', 'label', 'disabled'],
  emits: ['update:modelValue'],
  template: '<input type="checkbox" :aria-label="label" :checked="modelValue" :disabled="disabled" @change="$emit(\'update:modelValue\', $event.target.checked)">',
}

const stubs = {
  ...panelStubs,
  VBtn: { template: '<button><slot /></button>' },
  VBtnToggle: { template: '<div><slot /></div>' },
  VTextField: {
    props: ['modelValue', 'label', 'type'],
    emits: ['update:modelValue'],
    template: '<input :aria-label="label" :value="modelValue" :type="type || \'text\'" @input="$emit(\'update:modelValue\', $event.target.value)">',
  },
  VSelect: {
    props: ['modelValue', 'label', 'items'],
    emits: ['update:modelValue'],
    template: '<select :aria-label="label" :value="modelValue" @change="$emit(\'update:modelValue\', $event.target.value)"><option v-for="item in items" :key="item.value" :value="item.value">{{ item.title }}</option></select>',
  },
  VAutocomplete: {
    props: ['modelValue', 'label', 'items', 'disabled'],
    emits: ['update:modelValue'],
    template: '<select :aria-label="label" :value="modelValue" :disabled="disabled" @change="$emit(\'update:modelValue\', $event.target.value)"><option value=""></option><option v-for="item in items" :key="item.value" :value="item.value">{{ item.title }}</option></select>',
  },
  VSwitch: toggleStub,
  VCheckbox: toggleStub,
  FacebookPageConnect: true,
}

const tenant = '/tenants/tenant'
const channelPath = tenant + '/channels/personal'
const originalMetadata = { sync_scope: 'direct', session_keyword: 'Hello Bull', session_timeout_minutes: 45, account_owners: ['keep'] }
const personal = {
  id: 'personal', tenant_id: 'tenant', name: 'Personal Zalo', channel_type: 'personal_zalo_import',
  is_active: true, auto_reply_enabled: true, supports_auto_reply: true,
  metadata: JSON.stringify(originalMetadata),
} as Channel
const oa = { id: 'oa', tenant_id: 'tenant', channel_type: 'zalo_oa', is_active: true }
let storedChannel: Channel
let unmount: (() => void) | undefined

beforeEach(() => {
  vi.stubGlobal('localStorage', { getItem: () => null })
  storedChannel = { ...personal }
  api.get.mockReset().mockImplementation(async (url: string) => {
    if (url === tenant + '/channels') return { data: [{ ...storedChannel }, oa] }
    if (url === channelPath) return { data: { ...storedChannel } }
    if (url.endsWith('/account-owners')) return { data: { account_owners: [] } }
    if (url.endsWith('/personal-zalo-gateway')) return { data: { gateway_configured: true, gateway_reachable: true, account_exists: false } }
    if (url.endsWith('/zalo-whitelist')) return { data: [{ id: 'staff', name: 'An', zalo_user_id: '123', status: 'active' }] }
    if (url.endsWith('/sync-history')) return { data: { data: [], total: 0 } }
    return { data: [] }
  })
  api.put.mockReset().mockImplementation(async (_url: string, { metadata, ...fields }: Partial<Channel>) => {
    storedChannel = {
      ...storedChannel,
      ...fields,
      metadata: JSON.stringify({ ...JSON.parse(storedChannel.metadata), ...JSON.parse(metadata ?? '{}') }),
    }
    return { data: { message: 'updated' } }
  })
})

afterEach(() => {
  unmount?.()
  unmount = undefined
  vi.unstubAllGlobals()
})

async function mountPage(detail: boolean, canEdit = true, cachedChannel?: Channel, role = 'member') {
  const pinia = createPinia()
  const channelStore = useChannelStore(pinia)
  if (cachedChannel) channelStore.currentChannel = cachedChannel
  useAuthStore(pinia).tenantPerms = { role, permissions: { channels: canEdit ? 'rw' : 'r' } }
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [
      { path: '/:tenantId/channels', component: Channels },
      { path: '/:tenantId/channels/:channelId', component: ChannelDetail },
    ],
  })
  await router.push(detail ? '/tenant/channels/personal' : '/tenant/channels')
  const i18n = createI18n({ legacy: false, locale: 'en', messages: { en } })
  const wrapper = mount(detail ? ChannelDetail : Channels, { global: { plugins: [pinia, router, i18n], stubs } })
  unmount = () => wrapper.unmount()
  await flushPromises()
  return { wrapper, channelStore }
}

const editButton = 'button[aria-label="' + en.edit_channel + '"]'
const field = (label: string) => '[aria-label="' + label + '"]'

describe.each([false, true])('channel editor on detail page = %s', (detail) => {
  it('opens the same four tabs and saves sync, session, handover, and complaint settings', async () => {
    const { wrapper, channelStore } = await mountPage(detail)
    if (detail) expect(channelStore.channels).toEqual([])

    await wrapper.get(editButton).trigger('click')
    const dialog = wrapper.get('[role="dialog"]')
    const tabs = dialog.findAll('button[role="tab"]')
    expect(tabs.map(tab => tab.text())).toEqual([en.general_settings, en.chatbot_session, en.zalo_group_settings_tab, en.complaint_settings_tab])
    expect((dialog.get(field(en.sync_scope)).element as HTMLSelectElement).value).toBe('direct')
    await dialog.get(field(en.sync_scope)).setValue('all')

    await tabs[1]!.trigger('click')
    expect((dialog.get(field(en.session_keyword)).element as HTMLInputElement).value).toBe('Hello Bull')
    expect((dialog.get(field(en.session_timeout)).element as HTMLInputElement).value).toBe('45')
    await dialog.get(field(en.session_keyword)).setValue('Call Bull')
    await dialog.get(field(en.session_timeout)).setValue('60')

    await tabs[2]!.trigger('click')
    await flushPromises()
    await dialog.get('[data-testid="personal-zalo-group-reply-enabled"]').setValue(true)
    const staffSelect = dialog.get('[data-testid="personal-zalo-handover-staff"]')
    expect(staffSelect.text()).toContain('An · 123')
    await staffSelect.setValue('staff')

    await tabs[3]!.trigger('click')
    await flushPromises()
    await dialog.get('[data-testid="personal-zalo-complaint-cases"]').setValue(true)

    await dialog.findAll('button').find(button => button.text() === en.save_settings)!.trigger('click')
    await flushPromises()

    expect(api.put).toHaveBeenCalledExactlyOnceWith(channelPath, { metadata: expect.any(String) })
    expect(JSON.parse(api.put.mock.calls[0]![1].metadata)).toEqual({
      sync_scope: 'all', session_keyword: 'Call Bull', session_timeout_minutes: 60,
      personal_zalo_group_reply_enabled: true, personal_zalo_handover_staff_id: 'staff', handover_alert_group_id: '',
      complaint_cases: true,
    })
    expect(wrapper.find('[role="dialog"]').exists()).toBe(false)
    const refreshed = detail ? channelStore.currentChannel : channelStore.channels[0]
    expect(JSON.parse(refreshed!.metadata)).toMatchObject({ account_owners: ['keep'], session_keyword: 'Call Bull' })

    await wrapper.get(editButton).trigger('click')
    await wrapper.findAll('button[role="tab"]')[1]!.trigger('click')
    expect((wrapper.get(field(en.session_keyword)).element as HTMLInputElement).value).toBe('Call Bull')
  })

  it('hides the edit button for read-only users', async () => {
    const { wrapper } = await mountPage(detail, false)
    expect(wrapper.find(editButton).exists()).toBe(false)
  })
})

it('shows the dedicated complaint settings tab for a Zalo OA channel', async () => {
  storedChannel = {
    ...personal,
    channel_type: 'zalo_oa',
    name: 'Zalo OA',
    metadata: JSON.stringify({ complaint_cases: false }),
  }
  const { wrapper } = await mountPage(true)
  await wrapper.get(editButton).trigger('click')
  const dialog = wrapper.get('[role="dialog"]')
  expect(dialog.findComponent(PersonalZaloBotModePicker).exists()).toBe(false)
  const tabs = dialog.findAll('button[role="tab"]')
  expect(tabs.map(tab => tab.text())).toContain(en.complaint_settings_tab)
  await tabs.find(tab => tab.text() === en.complaint_settings_tab)!.trigger('click')
  await flushPromises()
  expect(dialog.get('[data-testid="complaint-cases"]').attributes('aria-label')).toBe(en.complaint_cases_switch)
})

it('creates an Internbot channel in setup mode before connecting Zalo', async () => {
  api.post.mockReset().mockResolvedValueOnce({ data: { ...personal, id: 'new-internbot' } })
  const { wrapper } = await mountPage(false, true, undefined, 'admin')
  await wrapper.findAll('button').find(button => button.text() === en.connect_channel)!.trigger('click')
  const dialog = wrapper.get('[role="dialog"]')
  expect(dialog.findComponent(PersonalZaloBotModePicker).exists()).toBe(false)
  await dialog.get(field(en.channel_type)).setValue('personal_zalo_import')
  dialog.getComponent(PersonalZaloBotModePicker).vm.$emit('update:modelValue', 'internbot')
  await dialog.get(field(en.channel_name)).setValue('Intern account')
  await dialog.findAll('button').find(button => button.text() === en.create_personal_zalo)!.trigger('click')
  await flushPromises()

  expect(api.post).toHaveBeenCalledExactlyOnceWith(tenant + '/channels', {
    name: 'Intern account', channel_type: 'personal_zalo_import', credentials: {},
    metadata: JSON.stringify({ sync_scope: 'all', chatbot_mode: 'internbot' }),
  })
})

it('switches modes from a personal Zalo card without opening the editor or enabling the bot', async () => {
  storedChannel.metadata = JSON.stringify({ ...originalMetadata, supervisor_group_id: '101' })
  const { wrapper, channelStore } = await mountPage(false, true, undefined, 'admin')
  const picker = wrapper.getComponent(PersonalZaloBotModePicker)
  expect(picker.props('compact')).toBe(true)
  expect(picker.props('modelValue')).toBe('chatbot')

  await picker.get('button[value="internbot"]').trigger('click')
  picker.vm.$emit('update:modelValue', 'internbot')
  await flushPromises()

  expect(wrapper.vm.$route.path).toBe('/tenant/channels')
  expect(wrapper.find('[role="dialog"]').exists()).toBe(false)
  expect(api.put).toHaveBeenNthCalledWith(1, channelPath, {
    auto_reply_enabled: false,
    metadata: JSON.stringify({ chatbot_mode: 'internbot', internbot_enabled: false }),
  })
  expect(picker.props('modelValue')).toBe('internbot')
  expect(JSON.parse(channelStore.channels[0]!.metadata)).toMatchObject({
    supervisor_group_id: '101', account_owners: ['keep'],
  })

  picker.vm.$emit('update:modelValue', 'chatbot')
  await flushPromises()
  expect(api.put).toHaveBeenNthCalledWith(2, channelPath, {
    auto_reply_enabled: false,
    metadata: JSON.stringify({ chatbot_mode: 'chatbot', internbot_enabled: false }),
  })
  expect(picker.props('modelValue')).toBe('chatbot')
  expect(channelStore.channels[0]!.auto_reply_enabled).toBe(false)
})

it('retains the saved mode when the inline update fails', async () => {
  api.put.mockRejectedValueOnce(new Error('update failed'))
  const { wrapper, channelStore } = await mountPage(false, true, undefined, 'owner')
  const picker = wrapper.getComponent(PersonalZaloBotModePicker)
  picker.vm.$emit('update:modelValue', 'internbot')
  await flushPromises()

  expect(picker.props('modelValue')).toBe('chatbot')
  expect(picker.props('disabled')).toBe(false)
  expect(channelStore.channels[0]!.auto_reply_enabled).toBe(true)
  expect(wrapper.text()).toContain(en.personal_zalo_bot_mode_update_failed)
})

it('keeps the newly saved mode when an earlier channel list response arrives late', async () => {
  const { wrapper, channelStore } = await mountPage(false, true, undefined, 'admin')
  const staleChannel = { ...storedChannel }
  let finishEarlierList!: () => void
  api.get.mockImplementationOnce(() => new Promise(resolve => {
    finishEarlierList = () => resolve({ data: [staleChannel, oa] })
  }))
  const earlierList = channelStore.fetchChannels('tenant')

  const picker = wrapper.getComponent(PersonalZaloBotModePicker)
  picker.vm.$emit('update:modelValue', 'internbot')
  await flushPromises()
  finishEarlierList()
  await earlierList
  await flushPromises()

  expect(picker.props('modelValue')).toBe('internbot')
})

it.each([
  ['personal_zalo_import', 'member', true],
  ['personal_zalo_import', 'member', false],
  ['zalo_oa', 'admin', true],
])('does not show the card mode picker for %s with role %s and write permission %s', async (type, role, writable) => {
  storedChannel.channel_type = type
  const { wrapper } = await mountPage(false, writable, undefined, role)
  expect(wrapper.findComponent(PersonalZaloBotModePicker).exists()).toBe(false)
})

it('saves the mode change before exposing Internbot enablement', async () => {
  const { wrapper } = await mountPage(false, true, undefined, 'admin')
  await wrapper.get(editButton).trigger('click')
  const dialog = wrapper.get('[role="dialog"]')
  dialog.getComponent(PersonalZaloBotModePicker).vm.$emit('update:modelValue', 'internbot')
  await flushPromises()

  expect(dialog.text()).toContain(en.personal_zalo_internbot_save_mode_first)
  expect(dialog.find(field(en.personal_zalo_internbot_enable)).exists()).toBe(false)
  await dialog.findAll('button').find(button => button.text() === en.save_settings)!.trigger('click')
  await flushPromises()
  expect(api.put).toHaveBeenCalledExactlyOnceWith(channelPath, { metadata: JSON.stringify({ chatbot_mode: 'internbot' }) })
  expect(wrapper.text()).toContain('Internbot')
  expect(wrapper.text()).toContain(en.personal_zalo_internbot_no_group_selected)
})

it('requires a current group and confirmation before enabling Internbot', async () => {
  storedChannel.metadata = JSON.stringify({ chatbot_mode: 'internbot' })
  const get = api.get.getMockImplementation()!
  api.get.mockImplementation((url: string, params?: unknown) => url.endsWith('/personal-zalo/groups') ? Promise.resolve({ data: {
    status: 'ready', captured_at: '2026-11-27T03:15:00Z', total: 2,
    items: [{ id: '101', name: 'Supervisors' }, { id: '102', name: 'Supervisors' }],
  } }) : get(url, params))
  const { wrapper } = await mountPage(false, true, undefined, 'admin')
  await wrapper.get(editButton).trigger('click')
  await flushPromises()
  const dialog = wrapper.get('[role="dialog"]')
  const enableSwitch = dialog.get<HTMLInputElement>(field(en.personal_zalo_internbot_enable))
  expect(enableSwitch.element.disabled).toBe(true)
  const group = dialog.get(field(en.personal_zalo_internbot_group))
  expect(group.text()).toContain('Supervisors · 101')
  expect(group.text()).toContain('Supervisors · 102')
  expect(api.get).toHaveBeenCalledWith(channelPath + '/personal-zalo/groups', { params: expect.objectContaining({ refresh: true }) })

  await group.setValue('102')
  expect(enableSwitch.element.disabled).toBe(true)
  await dialog.get(field(en.personal_zalo_internbot_confirm_group)).setValue(true)
  expect(enableSwitch.element.disabled).toBe(false)
  await enableSwitch.setValue(true)
  await group.setValue('101')
  expect(enableSwitch.element.checked).toBe(false)
  expect(enableSwitch.element.disabled).toBe(true)
  await dialog.get(field(en.personal_zalo_internbot_confirm_group)).setValue(true)
  await enableSwitch.setValue(true)
  await dialog.findAll('button').find(button => button.text() === en.save_settings)!.trigger('click')
  await flushPromises()

  expect(api.put).toHaveBeenCalledExactlyOnceWith(channelPath, { metadata: expect.any(String) })
  expect(JSON.parse(api.put.mock.calls[0]![1].metadata)).toEqual({ supervisor_group_id: '101', internbot_enabled: true })
})

it('allows pausing Internbot when the existing supervisor binding becomes unavailable', async () => {
  storedChannel.metadata = JSON.stringify({ chatbot_mode: 'internbot', supervisor_group_id: '102', internbot_enabled: true })
  const get = api.get.getMockImplementation()!
  api.get.mockImplementation((url: string) => url.endsWith('/personal-zalo/groups') ? Promise.resolve({ data: {
    status: 'ready', total: 1, items: [{ id: '102', name: 'Supervisors' }], supervisor_available: false,
  } }) : get(url))
  const { wrapper } = await mountPage(false, true, undefined, 'admin')
  await wrapper.get(editButton).trigger('click')
  await flushPromises()
  const dialog = wrapper.get('[role="dialog"]')
  expect(dialog.get('[role="status"]').text()).toBe(en.personal_zalo_internbot_group_unavailable)
  const enableSwitch = dialog.get<HTMLInputElement>(field(en.personal_zalo_internbot_enable))
  expect(enableSwitch.element.disabled).toBe(false)
  await enableSwitch.setValue(false)
  await dialog.findAll('button').find(button => button.text() === en.save_settings)!.trigger('click')
  await flushPromises()

  expect(api.put).toHaveBeenCalledExactlyOnceWith(channelPath, { metadata: JSON.stringify({ internbot_enabled: false }) })
})

it('keeps the edit dialog open with the original draft after a validation error', async () => {
  const { wrapper } = await mountPage(true)
  api.put.mockRejectedValueOnce({ isAxiosError: true, response: { data: { details: 'Session keywords must differ.' } } })
  await wrapper.get(editButton).trigger('click')
  const dialog = wrapper.get('[role="dialog"]')
  await dialog.get(field(en.channel_name)).setValue('New name')
  await dialog.findAll('button').find(button => button.text() === en.save_settings)!.trigger('click')
  await flushPromises()
  expect(dialog.get('[role="alert"]').text()).toBe('Session keywords must differ.')
  expect((dialog.get(field(en.channel_name)).element as HTMLInputElement).value).toBe('New name')
})


it('keeps newer settings on the server when saving only a name change', async () => {
  const { wrapper } = await mountPage(true)
  await wrapper.get(editButton).trigger('click')
  storedChannel.metadata = JSON.stringify({ ...originalMetadata, session_keyword: 'Newer session keyword' })
  const dialog = wrapper.get('[role="dialog"]')
  await dialog.get(field(en.channel_name)).setValue('New name')
  await dialog.findAll('button').find(button => button.text() === en.save_settings)!.trigger('click')
  await flushPromises()
  expect(api.put).toHaveBeenCalledExactlyOnceWith(channelPath, { name: 'New name' })
  expect(JSON.parse(storedChannel.metadata).session_keyword).toBe('Newer session keyword')
})

it('only exposes the edit button for the channel loaded for the current URL', async () => {
  const get = api.get.getMockImplementation()!
  let respond!: (value: { data: Channel }) => void
  const pending = new Promise<{ data: Channel }>((resolve) => { respond = resolve })
  api.get.mockImplementation((url: string) => url === channelPath ? pending : get(url))

  const { wrapper } = await mountPage(true, true, { ...personal, id: 'previous' })
  expect(wrapper.find(editButton).exists()).toBe(false)

  respond({ data: personal })
  await flushPromises()
  expect(wrapper.find(editButton).exists()).toBe(true)
})

it('keeps OA details and history when personal Zalo requests finish after navigation', async () => {
  const get = api.get.getMockImplementation()!
  const personalHistory = { data: { data: [{
    id: 'personal-log', created_at: '2026-11-26T15:19:00Z', action: 'import.personal_zalo.error',
    detail: 'Personal Zalo import', error_message: 'invalid Messenger journey input',
  }], total: 1 } }
  const oaHistory = { data: { data: [{
    id: 'oa-log', created_at: '2026-11-26T15:20:00Z', action: 'sync.completed', detail: 'OA sync',
  }], total: 1 } }
  let finishPersonalHistory!: (response: typeof personalHistory) => void
  const latePersonalHistory = new Promise<typeof personalHistory>((resolve) => { finishPersonalHistory = resolve })
  const gateway = { data: { gateway_configured: true, gateway_reachable: true, account_exists: true, account: { id: 'personal-account', status: 'connected' } } }
  let finishPersonalSync!: (response: typeof gateway) => void
  api.post.mockReset().mockReturnValue(new Promise<typeof gateway>((resolve) => { finishPersonalSync = resolve }))
  const oaPath = tenant + '/channels/oa'
  api.get.mockImplementation((url: string) => {
    if (url === channelPath + '/sync-history') return latePersonalHistory
    if (url === channelPath + '/personal-zalo-gateway') return Promise.resolve(gateway)
    if (url === oaPath) return Promise.resolve({ data: { ...personal, ...oa, name: 'Zalo OA' } })
    if (url === oaPath + '/sync-history') return Promise.resolve(oaHistory)
    return get(url)
  })

  const router = createRouter({
    history: createMemoryHistory(),
    routes: [{ path: '/:tenantId/channels/:channelId', component: ChannelDetail }],
  })
  await router.push('/tenant/channels/personal')
  const i18n = createI18n({ legacy: false, locale: 'en', messages: { en } })
  const pinia = createPinia()
  useAuthStore(pinia).tenantPerms = { role: 'member', permissions: { channels: 'rw' } }
  const wrapper = mount(App, {
    global: {
      plugins: [pinia, router, i18n],
      stubs: { ...stubs, DefaultLayout: { template: '<main><slot /></main>' } },
    },
  })
  unmount = () => wrapper.unmount()
  await flushPromises()
  expect(api.get).toHaveBeenCalledWith(channelPath + '/sync-history', expect.anything())
  const syncButton = wrapper.findAll('button').find(button => button.text() === en.personal_zalo_fetch_messages)
  expect(syncButton).toBeDefined()
  await syncButton!.trigger('click')
  expect(api.post).toHaveBeenCalledOnce()
  await router.push('/tenant/channels/oa')
  await flushPromises()
  expect(wrapper.get('h1').text()).toBe('Zalo OA')
  expect(wrapper.text()).toContain('OA sync')

  finishPersonalHistory(personalHistory)
  finishPersonalSync(gateway)
  await flushPromises()
  expect(wrapper.get('h1').text()).toBe('Zalo OA')
  expect(wrapper.text()).toContain('OA sync')
  expect(wrapper.text()).not.toContain('invalid Messenger journey input')
})
