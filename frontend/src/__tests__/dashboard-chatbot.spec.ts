// @vitest-environment happy-dom
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { shallowMount, flushPromises } from '@vue/test-utils'
import { createPinia } from 'pinia'
import { reactive } from 'vue'
import Dashboard from '../views/Dashboard.vue'
import { useAuthStore } from '../stores/auth'
import { useChatbotStore } from '../stores/chatbot'

const mocks = vi.hoisted(() => ({ get: vi.fn() }))
const route = reactive({ params: { tenantId: 'tenant-1' } })
vi.mock('../api', () => ({ default: { get: mocks.get } }))
vi.mock('vue-router', () => ({ useRoute: () => route, useRouter: () => ({ push: vi.fn() }) }))
vi.mock('vue-i18n', () => ({ useI18n: () => ({ t: (key: string) => key }) }))
vi.mock('vue-chartjs', () => ({ Line: { template: '<div />' } }))
vi.mock('../components/dashboard/BannerCarousel.vue', () => ({ default: { template: '<div />' } }))
vi.mock('../components/dashboard/AiCostCarousel.vue', () => ({ default: { template: '<div />' } }))

function mountDashboard(role = '') {
  const pinia = createPinia()
  const auth = useAuthStore(pinia)
  auth.tenantPerms = { role, permissions: {} }
  const wrapper = shallowMount(Dashboard, {
    global: { plugins: [pinia], mocks: { $t: (key: string) => key } },
  })
  return { wrapper, auth, chatbot: useChatbotStore(pinia) }
}

const settingsCalls = () => mocks.get.mock.calls.filter(([url]) => url.endsWith('/settings'))

describe('Dashboard chatbot status lifecycle', () => {
  beforeEach(() => {
    vi.stubGlobal('localStorage', { getItem: () => null })
    route.params.tenantId = 'tenant-1'
    mocks.get.mockReset().mockImplementation(async (url: string) => ({
      data: url.endsWith('/settings') ? { settings: { chatbot_active: 'false' } } : {},
    }))
  })

  it('loads when admin permissions arrive after mounting without remounting', async () => {
    const { wrapper, auth, chatbot } = mountDashboard()
    await flushPromises()
    expect(settingsCalls()).toHaveLength(0)
    auth.tenantPerms = { role: 'admin', permissions: {} }
    await flushPromises()
    expect(settingsCalls()).toEqual([['/tenants/tenant-1/settings']])
    expect(chatbot.isLoaded('tenant-1')).toBe(true)
    expect(chatbot.isActive('tenant-1')).toBe(false)
    expect(chatbot.isLoading('tenant-1')).toBe(false)
    wrapper.unmount()
  })

  it('loads once for an owner and refreshes when the tenant changes', async () => {
    const { wrapper, chatbot } = mountDashboard('owner')
    await flushPromises()
    expect(settingsCalls()).toEqual([['/tenants/tenant-1/settings']])
    route.params.tenantId = 'tenant-2'
    await flushPromises()
    expect(settingsCalls()).toEqual([['/tenants/tenant-1/settings'], ['/tenants/tenant-2/settings']])
    expect(chatbot.isLoaded('tenant-2')).toBe(true)
    wrapper.unmount()
  })

  it('does not fetch settings for a non-admin', async () => {
    const { wrapper, auth } = mountDashboard()
    auth.tenantPerms = { role: 'member', permissions: {} }
    await flushPromises()
    expect(settingsCalls()).toHaveLength(0)
    wrapper.unmount()
  })
})
