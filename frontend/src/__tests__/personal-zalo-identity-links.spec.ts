// @vitest-environment happy-dom
import { enableAutoUnmount, flushPromises, mount } from '@vue/test-utils'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { defineComponent, watch } from 'vue'
import { createI18n } from 'vue-i18n'
import PersonalZaloIdentityLinks from '../components/channels/PersonalZaloIdentityLinks.vue'
import en from '../i18n/en'

const api = vi.hoisted(() => ({ get: vi.fn(), post: vi.fn(), delete: vi.fn() }))
vi.mock('../api', () => ({ default: api }))
enableAutoUnmount(afterEach)

const endpoint = '/tenants/tenant/channels/personal/personal-zalo/identity-links'
const expiresAt = '2099-11-27T00:00:00Z'
const verify = 'verify example-one-use-code'
const select = {
  name: 'TestSelect',
  props: ['modelValue', 'label', 'items'],
  emits: ['update:modelValue', 'update:search'],
  template: '<select :aria-label="label" :value="modelValue" @change="$emit(\'update:modelValue\', $event.target.value)"><option v-for="item in items" :key="item.value" :value="item.value">{{ item.title }}</option></select>',
}
const dialog = defineComponent({
  props: { modelValue: Boolean },
  emits: ['afterLeave'],
  setup(props, { emit }) {
    watch(() => props.modelValue, (open) => { if (!open) emit('afterLeave') }, { flush: 'post' })
  },
  template: '<div v-if="modelValue" role="dialog"><slot /></div>',
})

function mountLinks(accountBound = true, channelAllowsPairing = true) {
  return mount(PersonalZaloIdentityLinks, {
    attachTo: document.body,
    props: { tenantId: 'tenant', channelId: 'personal', accountBound, channelAllowsPairing },
    global: {
      plugins: [createI18n({ legacy: false, locale: 'en', messages: { en } })],
      stubs: {
        VSelect: select,
        VAutocomplete: select,
        VBtn: { template: '<button><slot /></button>' },
        VTextField: { props: ['label', 'modelValue'], template: '<input :aria-label="label" :value="modelValue">' },
        VListItem: { props: ['title'], template: '<div>{{ title }}<slot name="append" /></div>' },
        VDialog: dialog,
        VCard: { props: ['title'], template: '<div>{{ title }}<slot /></div>' },
      },
    },
  })
}

beforeEach(() => {
  vi.resetAllMocks()
  api.get.mockImplementation(async (path: string) => ({ data: path.endsWith('/options') ? {
    pairing_enabled: true,
    staff: [{ id: 'staff', name: 'An', channel_name: 'BBI OA' }],
    customers: [{ id: 'customer', name: 'Bình', customer_code: 'C01' }],
  } : [] }))
})

afterEach(() => vi.unstubAllGlobals())

describe('personal Zalo identity link management', () => {
  it('links an existing 1:1 chat to OA staff with pairing off and no verification command', async () => {
    api.get.mockImplementation(async (path: string) => {
      if (path.endsWith('/options')) {
        return { data: { pairing_enabled: false, staff: [{ id: 'staff', name: 'Lâm Khải', channel_name: 'BBI' }] } }
      }
      if (path.endsWith('/direct-conversations')) {
        return { data: [{ id: 'direct-chat', name: 'Lâm Khải', sender_id: '1024' }] }
      }
      return { data: [{ id: 'pending-link', kind: 'staff', target_id: 'staff', expires_at: expiresAt }] }
    })
    api.post.mockResolvedValue({ data: {
      id: 'pending-link', kind: 'staff', target_id: 'staff', expires_at: expiresAt,
      verified_at: '2026-11-27T00:00:00Z', sender_id: '1024',
    } })
    const wrapper = mountLinks(true, false)
    await flushPromises()
    await wrapper.findAll('button').find((button) => button.text() === en.personal_zalo_identity_quick_link)!.trigger('click')
    const directChat = wrapper.get(`select[aria-label="${en.personal_zalo_identity_direct_chat}"]`)
    const staff = wrapper.get(`select[aria-label="${en.personal_zalo_identity_staff}"]`)
    await vi.waitFor(() => expect(directChat.text()).toContain('Lâm Khải · 1024'))
    expect(staff.text()).toContain('Lâm Khải · BBI')
    await directChat.setValue('direct-chat')
    await staff.setValue('staff')
    await wrapper.get('form').trigger('submit')
    await flushPromises()
    expect(api.post).toHaveBeenCalledWith(`${endpoint}/approve-staff`, { conversation_id: 'direct-chat', target_id: 'staff' })
    expect(wrapper.text()).toContain('Lâm Khải is now linked to this channel.')
    expect(wrapper.text()).toContain(en.personal_zalo_identity_verified)
    expect(wrapper.text()).not.toContain(en.personal_zalo_identity_pending)
    expect(wrapper.find(`input[aria-label="${en.personal_zalo_identity_command}"]`).exists()).toBe(false)
  })

  it('requires a new selection when the admin edits a selected chat or staff search', async () => {
    api.get.mockImplementation(async (path: string) => {
      if (path.endsWith('/options')) return { data: { staff: [{ id: 'staff', name: 'An' }] } }
      if (path.endsWith('/direct-conversations')) return { data: [{ id: 'chat', name: 'An', sender_id: '1024' }] }
      return { data: [] }
    })
    const wrapper = mountLinks()
    await flushPromises()
    await wrapper.findAll('button').find((button) => button.text() === en.personal_zalo_identity_quick_link)!.trigger('click')
    const [chat, staff] = wrapper.findAllComponents({ name: 'TestSelect' })
    const approve = () => wrapper.get('form').trigger('submit')
    await vi.waitFor(() => expect(chat.text()).toContain('An · 1024'))
    await chat.setValue('chat')
    await staff.setValue('staff')
    chat.vm.$emit('update:search', 'different chat')
    await approve()
    expect(api.post).not.toHaveBeenCalled()
    expect(chat.text()).not.toContain('An · 1024')
    await vi.waitFor(() => expect(api.get).toHaveBeenCalledWith(`${endpoint}/direct-conversations`,
      expect.objectContaining({ params: { q: 'different chat' } })))
    await chat.setValue('chat')
    chat.vm.$emit('update:search', 'An · 1024')
    staff.vm.$emit('update:search', 'different staff')
    await approve()
    expect(api.post).not.toHaveBeenCalled()
  })

  it('waits for the channel to bind its Zalo account', async () => {
    const wrapper = mountLinks(false)
    await flushPromises()
    expect(wrapper.text()).toContain(en.personal_zalo_identity_connect_first)
    expect(wrapper.find('form').exists()).toBe(false)
    expect(api.get).not.toHaveBeenCalled()
  })

  it('issues a code for a selected approved customer and only exposes it in the result', async () => {
    api.get.mockResolvedValueOnce({ data: [{
      id: 'expired-link', kind: 'customer', target_id: 'customer', expires_at: '2020-01-01T00:00:00Z',
    }] })
    api.post.mockResolvedValue({ data: {
      id: 'link-1', kind: 'customer', target_id: 'customer', expires_at: expiresAt, verification_command: verify,
    } })
    const copy = vi.fn().mockResolvedValue(undefined)
    vi.stubGlobal('navigator', { clipboard: { writeText: copy } })
    const wrapper = mountLinks()
    await flushPromises()
    expect(api.get).toHaveBeenCalledWith(endpoint)
    expect(api.get).toHaveBeenCalledWith(`${endpoint}/options`)
    expect(wrapper.get(`select[aria-label="${en.personal_zalo_identity_person}"]`).text()).toContain('An · BBI OA')
    await wrapper.get(`select[aria-label="${en.personal_zalo_identity_kind}"]`).setValue('customer')
    const target = wrapper.get(`select[aria-label="${en.personal_zalo_identity_person}"]`)
    expect(target.text()).toBe('Bình · C01')
    await target.setValue('customer')
    await wrapper.get('form').trigger('submit')
    await flushPromises()
    expect(api.post).toHaveBeenCalledWith(endpoint, { kind: 'customer', target_id: 'customer' })
    expect(wrapper.text()).not.toContain(en.personal_zalo_identity_expired)
    const commandField = `input[aria-label="${en.personal_zalo_identity_command}"]`
    expect((wrapper.get(commandField).element as HTMLInputElement).value).toBe(verify)
    await wrapper.findAll('button').find((button) => button.text() === en.personal_zalo_identity_copy)!.trigger('click')
    await flushPromises()
    expect(copy).toHaveBeenCalledWith(verify)
    await wrapper.get(`button[aria-label="${en.personal_zalo_identity_hide}"]`).trigger('click')
    expect(wrapper.find(commandField).exists()).toBe(false)
    expect(wrapper.text()).toContain(en.personal_zalo_identity_pending)
  })

  it.each([[false, true], [true, false]])('blocks issuance when channel ready = %s and backend ready = %s', async (channelReady, enabled) => {
    api.get.mockImplementation(async (path: string) => ({ data: path.endsWith('/options') ? {
      pairing_enabled: enabled, staff: [{ id: 'staff', name: 'An' }], customers: [],
    } : [] }))
    const wrapper = mountLinks(true, channelReady)
    await flushPromises()
    expect(wrapper.text()).toContain(en.personal_zalo_identity_prerequisites)
    expect((wrapper.get(`select[aria-label="${en.personal_zalo_identity_person}"]`).element as HTMLSelectElement).disabled).toBe(true)
    await wrapper.get('form').trigger('submit')
    expect(api.post).not.toHaveBeenCalled()
  })

  it('allows revoking a staff link while pairing is disabled and restores focus', async () => {
    api.get.mockImplementation(async (path: string) => ({ data: path.endsWith('/options') ? {
      pairing_enabled: false, staff: [{ id: 'staff', name: 'An' }], customers: [],
    } : [{ id: 'link-2', kind: 'staff', target_id: 'staff', sender_id: '1024', verified_at: '2026-11-27T00:00:00Z', expires_at: expiresAt }] }))
    api.delete.mockResolvedValue({ data: {} })
    const wrapper = mountLinks()
    await flushPromises()
    expect(wrapper.text()).toContain(en.personal_zalo_identity_verified)
    const revokeButton = wrapper.get('button[aria-label="Revoke the link for An"]')
    await revokeButton.trigger('click')
    expect(wrapper.get('[role="dialog"]').attributes('aria-label')).toBe('Revoke the link for An')
    await wrapper.get('[role="dialog"]').findAll('button').find((button) => button.text() === en.cancel)!.trigger('click')
    expect(document.activeElement).toBe(revokeButton.element)
    await revokeButton.trigger('click')
    await wrapper.get('[role="dialog"]').findAll('button').find((button) => button.text() === en.revoke)!.trigger('click')
    await flushPromises()
    expect(api.delete).toHaveBeenCalledWith(`${endpoint}/link-2`)
    expect(wrapper.text()).not.toContain(en.personal_zalo_identity_verified)
    expect(document.activeElement).toBe(wrapper.get('h2').element)
  })

  it('keeps active links revocable when identity options cannot load', async () => {
    api.get.mockImplementation(async (path: string) => {
      if (path.endsWith('/options')) throw new Error('options unavailable')
      return { data: [{
        id: 'link-3', kind: 'staff', target_id: 'staff', sender_id: '2048',
        verified_at: '2026-11-27T00:00:00Z', expires_at: expiresAt,
      }] }
    })
    api.delete.mockResolvedValue({ data: {} })

    const wrapper = mountLinks()
    await flushPromises()

    expect(wrapper.text()).toContain(en.personal_zalo_identity_load_error)
    expect(wrapper.text()).toContain(en.personal_zalo_identity_prerequisites)
    expect(wrapper.text()).toContain(en.personal_zalo_identity_verified)
    expect((wrapper.get(`select[aria-label="${en.personal_zalo_identity_person}"]`).element as HTMLSelectElement).disabled).toBe(true)

    await wrapper.get('button[aria-label="Revoke the link for staff"]').trigger('click')
    await wrapper.get('[role="dialog"]').findAll('button').find((button) => button.text() === en.revoke)!.trigger('click')
    await flushPromises()

    expect(api.delete).toHaveBeenCalledWith(`${endpoint}/link-3`)
    expect(wrapper.text()).not.toContain(en.personal_zalo_identity_verified)
  })
})
