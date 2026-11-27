// @vitest-environment happy-dom
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { createPinia, setActivePinia } from 'pinia'
import { defineComponent, h, nextTick } from 'vue'
import { flushPromises, mount } from '@vue/test-utils'
import Channels from '../views/Channels.vue'
import { useChannelStore, type Channel } from '../stores/channels'

const mocks = vi.hoisted(() => ({
  get: vi.fn(),
  post: vi.fn(),
  put: vi.fn(),
  delete: vi.fn(),
  push: vi.fn(),
  tenantPermissions: vi.fn(),
}))

vi.mock('../api', () => ({
  default: {
    get: mocks.get,
    post: mocks.post,
    put: mocks.put,
    delete: mocks.delete,
  },
}))
vi.mock('vue-router', () => ({
  useRoute: () => ({ params: { tenantId: 'tenant-1' } }),
  useRouter: () => ({ push: mocks.push }),
}))
vi.mock('vue-i18n', () => ({
  useI18n: () => ({ t: (key: string) => key }),
}))
vi.mock('../stores/auth', () => ({
  useAuthStore: () => ({ canEdit: () => true, tenantPerms: mocks.tenantPermissions() }),
}))

const Container = defineComponent({
  setup: (_, { slots, attrs }) => () => h('div', attrs, slots.default?.()),
})

const VAvatarStub = defineComponent({
  name: 'VAvatar',
  props: ['size'],
  setup: (props, { slots, attrs }) => () =>
    h('div', { ...attrs, class: ['v-avatar-stub', attrs.class], 'data-size': props.size }, slots.default?.()),
})

const VImgStub = defineComponent({
  name: 'VImg',
  props: ['src', 'alt', 'cover', 'eager'],
  emits: ['error'],
  setup: (props, { slots }) => () =>
    h('div', { class: 'v-img-stub', 'data-src': props.src, 'data-alt': props.alt }, [
      h('img', { src: props.src, alt: props.alt }),
      slots.placeholder ? h('div', { class: 'v-img-placeholder-stub' }, slots.placeholder()) : null,
    ]),
})

const VIconStub = defineComponent({
  name: 'VIcon',
  props: ['color', 'size', 'icon'],
  setup: (props, { slots, attrs }) => () =>
    h(
      'span',
      {
        ...attrs,
        class: ['v-icon-stub', attrs.class],
        'data-color': props.color,
        'data-size': props.size,
      },
      props.icon || slots.default?.(),
    ),
})

const VCardStub = defineComponent({
  name: 'VCard',
  setup: (_, { slots, attrs }) => () => h('article', { ...attrs, class: ['v-card-stub', attrs.class] }, slots.default?.()),
})

const VSwitchStub = defineComponent({
  name: 'VSwitch',
  props: { disabled: Boolean },
  setup: (props) => () => h('input', { type: 'checkbox', disabled: props.disabled }),
})

const HiddenStub = defineComponent({ setup: () => () => null })

function makeChannel(overrides: Partial<Channel> = {}): Channel {
  return {
    id: 'ch-pz-1',
    tenant_id: 'tenant-1',
    channel_type: 'personal_zalo_import',
    name: 'Test Khải',
    external_id: '',
    is_active: false,
    supports_auto_reply: true,
    auto_reply_enabled: false,
    metadata: '{}',
    last_sync_at: null,
    last_sync_status: '',
    conversation_count: 0,
    created_at: '2026-11-26T00:00:00Z',
    ...overrides,
  }
}

function mountChannels(channels: Channel[]) {
  const pinia = createPinia()
  setActivePinia(pinia)

  mocks.get.mockImplementation((url: string) => {
    if (url === '/tenants/tenant-1/channels') {
      return Promise.resolve({ data: channels })
    }
    if (url === '/tenants/tenant-1/settings') {
      return Promise.resolve({ data: { settings: { chatbot_active: 'true' } } })
    }
    return Promise.resolve({ data: {} })
  })

  return mount(Channels, {
    global: {
      plugins: [pinia],
      mocks: { $t: (key: string) => key },
      stubs: {
        VRow: Container,
        VCol: Container,
        VCard: VCardStub,
        VCardTitle: HiddenStub,
        VCardText: HiddenStub,
        VCardActions: HiddenStub,
        VAvatar: VAvatarStub,
        VImg: VImgStub,
        VIcon: VIconStub,
        VChip: Container,
        VSwitch: VSwitchStub,
        VBtn: HiddenStub,
        VDivider: HiddenStub,
        VSpacer: HiddenStub,
        VProgressCircular: HiddenStub,
        VDialog: HiddenStub,
        VSnackbar: HiddenStub,
        VSelect: HiddenStub,
        VTextField: HiddenStub,
        VAlert: HiddenStub,
        FacebookPageConnect: HiddenStub,
        ChannelEditDialog: HiddenStub,
      },
    },
  })
}

describe('Channels personal Zalo avatar rendering', () => {
  beforeEach(() => {
    Object.values(mocks).forEach((mock) => mock.mockReset())
  })

  it.each([
    { role: undefined, disabled: true },
    { role: 'member', disabled: true },
    { role: 'admin', disabled: false },
    { role: 'owner', disabled: false },
  ])('sets the personal Zalo auto-reply switch for role $role', async ({ role, disabled }) => {
    mocks.tenantPermissions.mockReturnValue(role ? { role } : undefined)
    const wrapper = mountChannels([makeChannel({ is_active: true })])
    await flushPromises()

    const switches = wrapper.get('article.v-card-stub').findAllComponents(VSwitchStub)
    expect(switches).toHaveLength(2)
    expect(switches[1]?.props('disabled')).toBe(disabled)

    wrapper.unmount()
  })

  it('renders personal Zalo avatar inside v-avatar with placeholder fallback icon', async () => {
    const avatarUrl = 'https://s120-ava-talk.zadn.vn/avatar.jpg'
    const wrapper = mountChannels([makeChannel({ avatar_url: ` ${avatarUrl} ` })])
    await flushPromises()

    const card = wrapper.get('article.v-card-stub')
    const avatar = card.findComponent(VAvatarStub)
    expect(avatar.exists()).toBe(true)
    expect(avatar.props('size')).toBe('32')

    const img = avatar.getComponent(VImgStub)
    expect(img.props('src')).toBe(avatarUrl)
    expect(img.props('alt')).toBe('Test Khải')
    expect(img.get('.v-img-placeholder-stub .v-icon-stub').text().trim()).toBe('mdi-cellphone-link')

    // Bare fallback icon is not rendered outside v-avatar while the image is active
    const directIcons = card.findAll('.v-icon-stub').filter((icon) => !avatar.element.contains(icon.element))
    expect(directIcons).toHaveLength(0)

    wrapper.unmount()
  })

  it('falls back to mdi-cellphone-link icon when avatar image fails to load and recovers on a new URL', async () => {
    const brokenUrl = 'https://s120-ava-talk.zadn.vn/broken.jpg'
    const fixedUrl = 'https://s120-ava-talk.zadn.vn/fixed.jpg'
    const wrapper = mountChannels([makeChannel({ avatar_url: brokenUrl })])
    await flushPromises()

    const card = wrapper.get('article.v-card-stub')
    card.getComponent(VImgStub).vm.$emit('error', brokenUrl)
    await nextTick()

    expect(card.findComponent(VAvatarStub).exists()).toBe(false)
    const fallbackIcon = card.getComponent(VIconStub)
    expect(fallbackIcon.text().trim()).toBe('mdi-cellphone-link')
    expect(fallbackIcon.props('color')).toBe('teal')
    expect(fallbackIcon.props('size')).toBe('32')

    // A refreshed channel payload with a new avatar_url should attempt rendering again
    const store = useChannelStore()
    store.channels[0] = makeChannel({ avatar_url: fixedUrl })
    await nextTick()

    expect(card.findComponent(VAvatarStub).exists()).toBe(true)
    expect(card.getComponent(VImgStub).props('src')).toBe(fixedUrl)

    wrapper.unmount()
  })

  it.each([
    { label: 'missing avatar_url', channel: makeChannel({ avatar_url: undefined }), icon: 'mdi-cellphone-link', color: 'teal' },
    { label: 'unsafe avatar_url scheme', channel: makeChannel({ avatar_url: 'javascript:alert(1)' }), icon: 'mdi-cellphone-link', color: 'teal' },
    { label: 'facebook channel', channel: makeChannel({ id: 'ch-fb', channel_type: 'facebook', avatar_url: 'https://cdn.example.com/fb.jpg' }), icon: 'mdi-facebook-messenger', color: 'indigo' },
  ])('renders fallback icon for $label', async ({ channel, icon, color }) => {
    const wrapper = mountChannels([channel])
    await flushPromises()

    const card = wrapper.get('article.v-card-stub')
    expect(card.findComponent(VAvatarStub).exists()).toBe(false)
    const renderedIcon = card.getComponent(VIconStub)
    expect(renderedIcon.text().trim()).toBe(icon)
    expect(renderedIcon.props('color')).toBe(color)

    wrapper.unmount()
  })

  it('renders official Zalo logo for zalo_oa channels even when avatar_url is set', async () => {
    const wrapper = mountChannels([
      makeChannel({ id: 'ch-oa', channel_type: 'zalo_oa', name: 'BBI Official', avatar_url: 'https://cdn.example.com/oa.jpg' }),
    ])
    await flushPromises()

    const card = wrapper.get('article.v-card-stub')
    const avatar = card.getComponent(VAvatarStub)
    expect(avatar.props('size')).toBe('32')
    const img = avatar.getComponent(VImgStub)
    expect(img.props('src')).toBe('/assets/brand/zalo-logo.svg')
    expect(img.props('alt')).toBe('BBI Official')
    expect(card.findAll('.v-icon-stub')).toHaveLength(0)

    wrapper.unmount()
  })
})
