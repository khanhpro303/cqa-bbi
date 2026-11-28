// @vitest-environment happy-dom
import { enableAutoUnmount, flushPromises, mount } from '@vue/test-utils'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import { createI18n } from 'vue-i18n'
import PersonalZaloApprovals from '../components/channels/PersonalZaloApprovals.vue'
import en from '../i18n/en'

const api = vi.hoisted(() => ({ get: vi.fn(), put: vi.fn() }))
const eventRefresh = vi.hoisted(() => vi.fn())
vi.mock('../api', () => ({ default: api }))
vi.mock('../composables/useTenantEvents', () => ({ useTenantEventRefresh: eventRefresh }))
enableAutoUnmount(afterEach)

const endpoint = '/tenants/tenant/channels/personal/personal-zalo/approvals'
const group = {
  thread_type: 'group', thread_id: '1234', status: 'pending',
  display_name: 'Khách BBI', first_seen_at: '2026-11-27T00:00:00Z',
}

function mountApprovals() {
  return mount(PersonalZaloApprovals, {
    props: { tenantId: 'tenant', channelId: 'personal' },
    global: {
      plugins: [createI18n({ legacy: false, locale: 'en', messages: { en } })],
      stubs: {
        VBtn: { template: '<button><slot /></button>' },
        VCard: { template: '<section><slot /></section>' },
        VTabs: { template: '<div><slot /></div>' },
        VTab: { template: '<span><slot /></span>' },
        VAlert: { template: '<div role="alert"><slot /></div>' },
        VIcon: true,
        VSpacer: true,
        VProgressLinear: true,
        VList: { template: '<div><slot /></div>' },
        VListItem: { template: '<div><slot /><slot name="append" /></div>' },
        VListItemTitle: { template: '<div><slot /></div>' },
        VListItemSubtitle: { template: '<div><slot /></div>' },
      },
    },
  })
}

beforeEach(() => {
  vi.resetAllMocks()
  api.get.mockResolvedValue({ data: { items: [group], total: 1 } })
})

it('loads pending approvals, refreshes on SSE, and submits an explicit decision', async () => {
  const wrapper = mountApprovals()
  await flushPromises()
  expect(api.get).toHaveBeenCalledWith(endpoint, { params: { status: 'pending', limit: 50, offset: 0 } })
  expect(wrapper.text()).toContain(group.display_name)
  const [tenant, signal, refresh] = eventRefresh.mock.calls[0]!
  expect(tenant()).toBe('tenant')
  expect(signal).toBe('personal_zalo_approvals_updated')
  await refresh()
  expect(api.get).toHaveBeenCalledTimes(2)

  api.put.mockResolvedValue({ status: 204 })
  api.get.mockResolvedValue({ data: { items: [], total: 0 } })
  await wrapper.findAll('button').find((button) => button.text() === en.personal_zalo_approval_approve)!.trigger('click')
  await flushPromises()
  expect(api.put).toHaveBeenCalledWith(`${endpoint}/group/1234`, { status: 'approved', expected_status: 'pending' })
  expect(wrapper.text()).toContain(en.personal_zalo_approval_empty)
})

it('refreshes and keeps the conflict notice when another admin already made a decision', async () => {
  const wrapper = mountApprovals()
  await flushPromises()
  api.put.mockRejectedValue({ response: { status: 409 } })
  api.get.mockResolvedValue({ data: { items: [], total: 0 } })
  await wrapper.findAll('button').find((button) => button.text() === en.personal_zalo_approval_reject)!.trigger('click')
  await flushPromises()
  expect(api.get).toHaveBeenCalledTimes(2)
  expect(wrapper.get('[role=alert]').text()).toContain(en.personal_zalo_approval_stale)
})

it('focuses the review heading when opened from a notification', async () => {
  const wrapper = mountApprovals()
  const heading = wrapper.get('h2').element
  const focus = vi.spyOn(heading, 'focus')
  const scroll = vi.spyOn(heading, 'scrollIntoView')
  await wrapper.setProps({ focusReview: true })
  expect(focus).toHaveBeenCalledOnce()
  expect(scroll).toHaveBeenCalledWith({ block: 'center' })
  expect(wrapper.emitted('reviewFocused')).toHaveLength(1)
})
