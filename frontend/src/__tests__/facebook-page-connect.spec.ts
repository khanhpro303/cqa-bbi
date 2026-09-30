// @vitest-environment happy-dom
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { defineComponent, h } from 'vue'
import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import { createI18n } from 'vue-i18n'
import FacebookPageConnect from '../components/channels/FacebookPageConnect.vue'
import enMessages from '../i18n/en'
import viMessages from '../i18n/vi'

const { getMock, postMock } = vi.hoisted(() => ({
  getMock: vi.fn(),
  postMock: vi.fn(),
}))

vi.mock('../api', () => ({ default: { get: getMock, post: postMock } }))

const PassThrough = defineComponent({
  inheritAttrs: false,
  setup(_, { attrs, slots }) {
    return () => h('div', attrs, slots.default?.())
  },
})

const Button = defineComponent({
  inheritAttrs: false,
  emits: ['click'],
  setup(_, { attrs, slots, emit }) {
    return () => h('button', { ...attrs, disabled: attrs.disabled as boolean, onClick: () => emit('click') }, slots.default?.())
  },
})

const Dialog = defineComponent({
  props: { modelValue: Boolean },
  setup(props, { slots }) {
    return () => props.modelValue ? h('div', { 'data-testid': 'dialog' }, slots.default?.()) : null
  },
})

const ListItem = defineComponent({
  inheritAttrs: false,
  emits: ['click'],
  setup(_, { attrs, slots, emit }) {
    return () => h('button', { ...attrs, disabled: attrs.disabled as boolean, onClick: () => emit('click') }, [
      slots.prepend?.(),
      slots.default?.(),
    ])
  },
})

const stubs = {
  VAlert: PassThrough,
  VBtn: Button,
  VIcon: PassThrough,
  VDialog: Dialog,
  VCard: PassThrough,
  VCardTitle: PassThrough,
  VCardText: PassThrough,
  VCardActions: PassThrough,
  VList: PassThrough,
  VListItem: ListItem,
  VListItemTitle: PassThrough,
  VListItemSubtitle: PassThrough,
  VRadio: PassThrough,
  VSelect: PassThrough,
  VSwitch: PassThrough,
  VSpacer: PassThrough,
}

let wrapper: VueWrapper | undefined

function mountComponent(canEdit = true) {
  const i18n = createI18n({ legacy: false, locale: 'en', messages: { en: enMessages, vi: viMessages } })
  wrapper = mount(FacebookPageConnect, {
    props: {
      tenantId: 'tenant-1',
      canEdit,
      syncIntervalOptions: [{ title: '15 minutes', value: 15 }],
    },
    global: { plugins: [i18n], stubs },
  })
  return wrapper
}

beforeEach(() => {
  getMock.mockReset()
  postMock.mockReset()
  window.history.replaceState({}, '', '/tenant-1/channels')
})

afterEach(() => {
  wrapper?.unmount()
  wrapper = undefined
})

describe('Facebook Page OAuth connection', () => {
  it('loads the callback session, lets the user choose a Page, and never handles a token', async () => {
    window.history.replaceState({}, '', '/tenant-1/channels?facebook_connect=session-1')
    getMock.mockImplementation(async (url: string) => {
      if (url.endsWith('/config')) return { data: { enabled: true, callback_url: '/oauth/facebook/callback' } }
      return { data: { pages: [{ id: 'page-1', name: 'Page One' }, { id: 'page-2', name: 'Page Two' }], expires_at: '2099-01-01T00:00:00Z' } }
    })
    postMock.mockResolvedValue({ data: { id: 'channel-2', channel_type: 'facebook', name: 'Page Two' } })

    const view = mountComponent()
    await flushPromises()

    expect(getMock).toHaveBeenCalledWith('/tenants/tenant-1/facebook/connect/session-1')
    expect(view.text()).toContain('Page One')
    expect(view.text()).toContain('Page Two')
    await view.get('[data-testid="facebook-page-page-2"]').trigger('click')
    await view.get('[data-testid="facebook-page-confirm"]').trigger('click')
    await flushPromises()

    expect(postMock).toHaveBeenCalledWith('/tenants/tenant-1/facebook/connect/session-1/select', {
      page_id: 'page-2',
      sync_interval: 15,
      sync_files: false,
    })
    expect(JSON.stringify(postMock.mock.calls)).not.toContain('access_token')
    expect(view.emitted('connected')?.[0]?.[0]).toMatchObject({ id: 'channel-2' })
    expect(window.location.search).toBe('')
  })

  it('keeps the Page picker open and explains a subscription failure', async () => {
    window.history.replaceState({}, '', '/tenant-1/channels?facebook_connect=session-2')
    getMock.mockImplementation(async (url: string) => {
      if (url.endsWith('/config')) return { data: { enabled: true } }
      return { data: { pages: [{ id: 'page-1', name: 'Page One' }] } }
    })
    postMock.mockRejectedValue({ response: { data: { error: 'subscription_failed' } } })

    const view = mountComponent()
    await flushPromises()
    await view.get('[data-testid="facebook-page-confirm"]').trigger('click')
    await flushPromises()

    expect(view.get('[data-testid="facebook-page-selection-error"]').text()).toContain('could not subscribe')
    expect(view.find('[data-testid="dialog"]').exists()).toBe(true)
    expect(window.location.search).toContain('facebook_connect=session-2')
  })

  it('maps callback permission errors to a clear retry message', async () => {
    window.history.replaceState({}, '', '/tenant-1/channels?facebook_connect_error=missing_permissions')
    getMock.mockResolvedValue({ data: { enabled: true } })

    const view = mountComponent()
    await flushPromises()

    expect(view.get('[data-testid="facebook-connect-error"]').text()).toContain('did not grant all required Page permissions')
    expect(window.location.search).toBe('')
  })

  it('retries loading server configuration before starting OAuth', async () => {
    getMock
      .mockRejectedValueOnce(new Error('network_error'))
      .mockResolvedValueOnce({ data: { enabled: true } })

    const view = mountComponent()
    await flushPromises()
    expect(view.find('[data-testid="facebook-connect-error"]').exists()).toBe(true)

    await view.get('[data-testid="facebook-connect-retry"]').trigger('click')
    await flushPromises()

    expect(getMock).toHaveBeenCalledTimes(2)
    expect(postMock).not.toHaveBeenCalled()
    expect(view.find('[data-testid="facebook-connect-error"]').exists()).toBe(false)
    expect(view.get('[data-testid="facebook-connect-start"]').attributes('disabled')).toBeUndefined()
  })

  it('blocks OAuth controls when the user cannot edit channels', async () => {
    getMock.mockResolvedValue({ data: { enabled: true } })
    const view = mountComponent(false)
    await flushPromises()

    expect(view.get('[data-testid="facebook-connect-start"]').attributes('disabled')).toBeDefined()
    await view.get('[data-testid="facebook-connect-start"]').trigger('click')
    expect(postMock).not.toHaveBeenCalled()
  })
})
