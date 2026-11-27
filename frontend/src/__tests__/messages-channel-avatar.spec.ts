// @vitest-environment happy-dom
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { createPinia, setActivePinia } from 'pinia'
import { defineComponent, h, nextTick, ref } from 'vue'
import { flushPromises, mount } from '@vue/test-utils'
import Messages from '../views/Messages.vue'
import type { Channel } from '../stores/channels'
import type { Conversation } from '../stores/conversations'

const mocks = vi.hoisted(() => ({
  get: vi.fn(),
  replace: vi.fn(),
}))

vi.mock('../api', () => ({
  default: {
    get: mocks.get,
  },
}))
vi.mock('vue-router', () => ({
  useRoute: () => ({ params: { tenantId: 'tenant-1' }, query: {} }),
  useRouter: () => ({ replace: mocks.replace }),
}))
vi.mock('vue-i18n', () => ({
  useI18n: () => ({ t: (key: string) => key }),
}))
vi.mock('vuetify', () => ({
  useDisplay: () => ({ mdAndUp: ref(true) }),
}))
vi.mock('../stores/auth', () => ({
  useAuthStore: () => ({ canEdit: () => true }),
}))
vi.mock('../components/MessengerJourneyPanel.vue', () => ({
  default: defineComponent({ setup: () => () => null }),
}))

const Container = defineComponent({
  setup: (_, { slots, attrs }) => () => h('div', attrs, slots.default?.()),
})

const VAvatarStub = defineComponent({
  name: 'VAvatar',
  props: ['size', 'color', 'rounded'],
  setup: (props, { slots, attrs }) => () =>
    h('div', { ...attrs, class: ['v-avatar-stub', attrs.class], 'data-size': props.size, 'data-color': props.color }, slots.default?.()),
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
      { ...attrs, class: ['v-icon-stub', attrs.class], 'data-color': props.color, 'data-size': props.size },
      props.icon || slots.default?.(),
    ),
})

const VListItemStub = defineComponent({
  name: 'VListItem',
  emits: ['click'],
  setup: (_, { slots, attrs, emit }) => () =>
    h('div', { ...attrs, class: ['v-list-item-stub', attrs.class], onClick: () => emit('click') }, [
      slots.prepend ? h('div', { class: 'list-item-prepend' }, slots.prepend()) : null,
      slots.default?.(),
    ]),
})

const VCardTitleStub = defineComponent({
  name: 'VCardTitle',
  setup: (_, { slots, attrs }) => () => h('div', { ...attrs, class: ['v-card-title-stub', attrs.class] }, slots.default?.()),
})

const HiddenStub = defineComponent({ setup: () => () => null })

function mountMessages(channels: Channel[], conversations: Conversation[]) {
  const pinia = createPinia()
  setActivePinia(pinia)

  mocks.get.mockImplementation((url: string) => {
    if (url === '/tenants/tenant-1/channels') {
      return Promise.resolve({ data: channels })
    }
    if (url === '/tenants/tenant-1/conversations') {
      return Promise.resolve({ data: { data: conversations, total: conversations.length } })
    }
    if (url === '/tenants/tenant-1/conversations/evaluated') {
      return Promise.resolve({ data: {} })
    }
    if (url.endsWith('/messages')) {
      return Promise.resolve({
        data: {
          conversation: { id: conversations[0]?.id || 'conv-1', customer_name: 'Khách hàng', message_count: 1 },
          messages: [],
        },
      })
    }
    if (url.endsWith('/evaluations')) {
      return Promise.resolve({ data: null })
    }
    return Promise.resolve({ data: {} })
  })

  return mount(Messages, {
    global: {
      plugins: [pinia],
      mocks: { $t: (key: string) => key },
      stubs: {
        VRow: Container,
        VCol: Container,
        VCard: Container,
        VCardText: Container,
        VCardTitle: VCardTitleStub,
        VCardActions: HiddenStub,
        VList: Container,
        VListItem: VListItemStub,
        VListItemTitle: Container,
        VListItemSubtitle: Container,
        VAvatar: VAvatarStub,
        VImg: VImgStub,
        VIcon: VIconStub,
        VChip: Container,
        VSelect: HiddenStub,
        VTextField: HiddenStub,
        VBtn: HiddenStub,
        VDivider: HiddenStub,
        VSpacer: HiddenStub,
        VDialog: HiddenStub,
        VTabs: HiddenStub,
        VTab: HiddenStub,
        VProgressCircular: HiddenStub,
        VPagination: HiddenStub,
        VSnackbar: HiddenStub,
      },
    },
  })
}

describe('Messages channel placeholders and avatars', () => {
  beforeEach(() => {
    Object.values(mocks).forEach((mock) => mock.mockReset())
  })

  it('renders Zalo logo for zalo_oa, personal avatar for personal_zalo_import, and messenger icon for facebook', async () => {
    const channels: Channel[] = [
      {
        id: 'ch-oa',
        tenant_id: 'tenant-1',
        channel_type: 'zalo_oa',
        name: 'Zalo OA BBI',
        external_id: 'oa-1',
        is_active: true,
        supports_auto_reply: true,
        auto_reply_enabled: true,
        metadata: '{}',
        last_sync_at: null,
        last_sync_status: '',
        conversation_count: 1,
        created_at: '2026-11-26T00:00:00Z',
      },
      {
        id: 'ch-pz',
        tenant_id: 'tenant-1',
        channel_type: 'personal_zalo_import',
        name: 'Zalo Cá Nhân Khải',
        external_id: 'pz-1',
        avatar_url: 'https://s120-ava-talk.zadn.vn/personal.jpg',
        is_active: true,
        supports_auto_reply: true,
        auto_reply_enabled: true,
        metadata: '{}',
        last_sync_at: null,
        last_sync_status: '',
        conversation_count: 1,
        created_at: '2026-11-26T00:00:00Z',
      },
      {
        id: 'ch-fb',
        tenant_id: 'tenant-1',
        channel_type: 'facebook',
        name: 'Fanpage BBI',
        external_id: 'fb-1',
        is_active: true,
        supports_auto_reply: false,
        auto_reply_enabled: false,
        metadata: '{}',
        last_sync_at: null,
        last_sync_status: '',
        conversation_count: 1,
        created_at: '2026-11-26T00:00:00Z',
      },
    ]

    const conversations: Conversation[] = [
      {
        id: 'conv-oa',
        channel_id: 'ch-oa',
        channel_name: 'Zalo OA BBI',
        channel_type: 'zalo_oa',
        customer_name: 'Khách OA',
        last_message_at: '2026-11-27T08:00:00Z',
        message_count: 3,
        created_at: '2026-11-27T08:00:00Z',
      },
      {
        id: 'conv-pz',
        channel_id: 'ch-pz',
        channel_name: 'Zalo Cá Nhân Khải',
        channel_type: 'personal_zalo_import',
        customer_name: 'Khách Cá Nhân',
        last_message_at: '2026-11-27T08:00:00Z',
        message_count: 2,
        created_at: '2026-11-27T08:00:00Z',
      },
      {
        id: 'conv-fb',
        channel_id: 'ch-fb',
        channel_name: 'Fanpage BBI',
        channel_type: 'facebook',
        customer_name: 'Khách FB',
        last_message_at: '2026-11-27T08:00:00Z',
        message_count: 1,
        created_at: '2026-11-27T08:00:00Z',
      },
    ]

    const wrapper = mountMessages(channels, conversations)
    await flushPromises()

    const items = wrapper.findAll('.v-list-item-stub')
    expect(items).toHaveLength(3)

    // 1. Zalo OA uses official Zalo logo instead of chat icon
    const oaImg = items[0]!.find('.list-item-prepend').getComponent(VImgStub)
    expect(oaImg.props('src')).toBe('/assets/brand/zalo-logo.svg')
    expect(items[0]!.find('.list-item-prepend').findAll('.v-icon-stub')).toHaveLength(0)

    // 2. Personal Zalo uses the linked channel avatar
    const pzImg = items[1]!.find('.list-item-prepend').getComponent(VImgStub)
    expect(pzImg.props('src')).toBe('https://s120-ava-talk.zadn.vn/personal.jpg')

    // 3. Facebook keeps messenger icon
    const fbIcon = items[2]!.find('.list-item-prepend').getComponent(VIconStub)
    expect(fbIcon.text().trim()).toBe('mdi-facebook-messenger')

    // Selecting Zalo OA conversation shows Zalo logo in detail header
    await items[0]!.trigger('click')
    await flushPromises()
    const headerTitle = wrapper.find('.v-card-title-stub')
    expect(headerTitle.getComponent(VImgStub).props('src')).toBe('/assets/brand/zalo-logo.svg')

    // Selecting Personal Zalo conversation shows personal avatar in detail header
    await items[1]!.trigger('click')
    await flushPromises()
    await nextTick()
    expect(wrapper.find('.v-card-title-stub').getComponent(VImgStub).props('src')).toBe('https://s120-ava-talk.zadn.vn/personal.jpg')

    wrapper.unmount()
  })

  it('falls back to mdi-cellphone-link badge for personal_zalo_import when avatar_url is missing or broken', async () => {
    const channels: Channel[] = [
      {
        id: 'ch-pz-no-ava',
        tenant_id: 'tenant-1',
        channel_type: 'personal_zalo_import',
        name: 'Zalo Cá Nhân',
        external_id: 'pz-2',
        is_active: true,
        supports_auto_reply: true,
        auto_reply_enabled: true,
        metadata: '{}',
        last_sync_at: null,
        last_sync_status: '',
        conversation_count: 1,
        created_at: '2026-11-26T00:00:00Z',
      },
    ]

    const conversations: Conversation[] = [
      {
        id: 'conv-pz-no-ava',
        channel_id: 'ch-pz-no-ava',
        channel_name: 'Zalo Cá Nhân',
        channel_type: 'personal_zalo_import',
        customer_name: 'Khách A',
        last_message_at: '2026-11-27T08:00:00Z',
        message_count: 1,
        created_at: '2026-11-27T08:00:00Z',
      },
    ]

    const wrapper = mountMessages(channels, conversations)
    await flushPromises()

    const prepend = wrapper.find('.v-list-item-stub .list-item-prepend')
    expect(prepend.findComponent(VImgStub).exists()).toBe(false)
    const icon = prepend.getComponent(VIconStub)
    expect(icon.text().trim()).toBe('mdi-cellphone-link')
    expect(prepend.getComponent(VAvatarStub).props('color')).toBe('teal')

    wrapper.unmount()
  })
})
