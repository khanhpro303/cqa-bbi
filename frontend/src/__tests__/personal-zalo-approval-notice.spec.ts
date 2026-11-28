// @vitest-environment happy-dom
import { enableAutoUnmount, flushPromises, mount } from '@vue/test-utils'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import { createI18n } from 'vue-i18n'
import { createMemoryHistory, createRouter } from 'vue-router'
import PersonalZaloApprovalNotice from '../components/channels/PersonalZaloApprovalNotice.vue'
import en from '../i18n/en'

const api = vi.hoisted(() => ({ get: vi.fn() }))
vi.mock('../api', () => ({ default: api }))
enableAutoUnmount(afterEach)

const start = '/workspace/messages'
const summary = '/tenants/workspace/personal-zalo/approvals/pending'
const streams: ReadableStreamDefaultController<Uint8Array>[] = []

async function mountNotice() {
  const blank = { template: '<div />' }
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [
      { path: '/:tenantId/messages', component: blank },
      { path: '/:tenantId/channels/:channelId', name: 'channel-detail', component: blank },
    ],
  })
  await router.push(start)
  const wrapper = mount(PersonalZaloApprovalNotice, {
    props: { tenantId: 'workspace' },
    slots: { icon: '<span data-test="notification-icon" />' },
    global: {
      plugins: [router, createI18n({ legacy: false, locale: 'en', messages: { en } })],
      stubs: {
        VAlert: { template: '<section><slot name="prepend" /><slot /></section>' },
        VBtn: { props: ['to'], template: '<RouterLink :to="to"><slot /></RouterLink>' },
      },
    },
  })
  await flushPromises()
  return { wrapper, router }
}

function notify(event = 'personal_zalo_approvals_updated') {
  streams.at(-1)!.enqueue(new TextEncoder().encode(`event: ${event}\ndata: {}\n\n`))
}

beforeEach(() => {
  streams.length = 0
  api.get.mockReset().mockResolvedValue({ data: { channels: [] } })
  vi.stubGlobal('fetch', vi.fn(async (_url: string, options: { signal: AbortSignal }) => ({
    ok: true,
    body: new ReadableStream<Uint8Array>({
      start(controller) {
        streams.push(controller)
        options.signal.addEventListener('abort', () => controller.close(), { once: true })
      },
    }),
  })))
})

afterEach(() => vi.unstubAllGlobals())

it('shows an app-wide notice after an SSE signal and links each count to its own review panel', async () => {
  const { wrapper, router } = await mountNotice()
  expect(api.get).toHaveBeenCalledWith(summary, { signal: expect.any(AbortSignal) })
  expect(fetch).toHaveBeenCalledWith('/api/v1/tenants/workspace/events', expect.anything())
  expect(wrapper.find('section').exists()).toBe(false)

  const requests = api.get.mock.calls.length
  notify('cloudify_orders_updated')
  await flushPromises()
  expect(api.get).toHaveBeenCalledTimes(requests)

  api.get.mockResolvedValue({ data: { channels: [
    { channel_id: 'sales', name: 'Sales', count: 2 },
    { channel_id: 'support', name: 'Support', count: 1 },
  ] } })
  notify()
  await flushPromises()
  expect(router.currentRoute.value.path).toBe(start)
  expect(wrapper.find('[data-test="notification-icon"]').exists()).toBe(true)
  expect(wrapper.text()).toContain('3 personal Zalo contacts or groups are pending review')
  const links = wrapper.findAll('a')
  expect(links.map((link) => [link.text(), link.attributes('href')])).toEqual([
    ['Review Sales (2)', '/workspace/channels/sales#personal-zalo-approvals'],
    ['Review Support (1)', '/workspace/channels/support#personal-zalo-approvals'],
  ])
  await links[1]!.trigger('click')
  await flushPromises()
  expect(router.currentRoute.value.fullPath).toBe('/workspace/channels/support#personal-zalo-approvals')

  api.get.mockResolvedValue({ data: { channels: [] } })
  notify()
  await flushPromises()
  expect(wrapper.find('section').exists()).toBe(false)
})

it('reloads on reconnect and clears pending counts when review access is revoked', async () => {
  const { wrapper } = await mountNotice()
  api.get.mockResolvedValue({ data: { channels: [{ channel_id: 'sales', name: 'Sales', count: 1 }] } })
  window.dispatchEvent(new Event('online'))
  await flushPromises()
  expect(streams).toHaveLength(2)
  expect(wrapper.text()).toContain('1 personal Zalo contact or group is pending review')

  api.get.mockRejectedValue({ response: { status: 403 } })
  notify()
  await flushPromises()
  expect(wrapper.find('section').exists()).toBe(false)
})
