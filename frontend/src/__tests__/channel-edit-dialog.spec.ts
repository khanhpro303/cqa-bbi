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

const stubs = {
  ...panelStubs,
  VBtn: { template: '<button><slot /></button>' },
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
  VSwitch: {
    props: ['modelValue', 'label', 'disabled'],
    emits: ['update:modelValue'],
    template: '<input type="checkbox" :aria-label="label" :checked="modelValue" :disabled="disabled" @change="$emit(\'update:modelValue\', $event.target.checked)">',
  },
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

async function mountPage(detail: boolean, canEdit = true, cachedChannel?: Channel) {
  const pinia = createPinia()
  const channelStore = useChannelStore(pinia)
  if (cachedChannel) channelStore.currentChannel = cachedChannel
  useAuthStore(pinia).tenantPerms = { role: 'member', permissions: { channels: canEdit ? 'rw' : 'r' } }
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
  it('opens the same three tabs and saves sync, session, and handover settings', async () => {
    const { wrapper, channelStore } = await mountPage(detail)
    if (detail) expect(channelStore.channels).toEqual([])

    await wrapper.get(editButton).trigger('click')
    const dialog = wrapper.get('[role="dialog"]')
    const tabs = dialog.findAll('button[role="tab"]')
    expect(tabs.map(tab => tab.text())).toEqual([en.general_settings, en.chatbot_session, en.zalo_group_settings_tab])
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

    await dialog.findAll('button').find(button => button.text() === en.save_settings)!.trigger('click')
    await flushPromises()

    expect(api.put).toHaveBeenCalledExactlyOnceWith(channelPath, { metadata: expect.any(String) })
    expect(JSON.parse(api.put.mock.calls[0]![1].metadata)).toEqual({
      sync_scope: 'all', session_keyword: 'Call Bull', session_timeout_minutes: 60,
      personal_zalo_group_reply_enabled: true, personal_zalo_handover_staff_id: 'staff', handover_alert_group_id: '',
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
