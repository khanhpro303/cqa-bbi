// @vitest-environment happy-dom
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import { shallowMount, flushPromises } from '@vue/test-utils'
import { createPinia } from 'pinia'
import Dashboard from '../views/Dashboard.vue'

const mocks = vi.hoisted(() => ({ get: vi.fn() }))
vi.mock('../api', () => ({ default: { get: mocks.get } }))
vi.mock('vue-router', () => ({ useRoute: () => ({ params: { tenantId: 'tenant-a' } }), useRouter: () => ({ push: vi.fn() }) }))
vi.mock('vue-i18n', () => ({ useI18n: () => ({ t: (key: string) => key }) }))
vi.mock('vue-chartjs', () => ({ Line: { template: '<div />' } }))
vi.mock('../components/dashboard/BannerCarousel.vue', () => ({ default: { template: '<div />' } }))
vi.mock('../components/dashboard/AiCostCarousel.vue', () => ({ default: { template: '<div />' } }))

beforeEach(() => {
  vi.useFakeTimers()
  vi.setSystemTime(new Date(2026, 8, 16, 3))
  vi.stubGlobal('localStorage', { getItem: () => null })
  mocks.get.mockReset().mockResolvedValue({ data: { issues_today: 3 } })
})
afterEach(() => { vi.useRealTimers(); vi.unstubAllGlobals() })

it('requests exactly 28 calendar days initially and preserves the API count', async () => {
  const wrapper = shallowMount(Dashboard, { global: { plugins: [createPinia()], mocks: { $t: (key: string) => key } } })
  await flushPromises()
  expect(mocks.get).toHaveBeenCalledWith('/tenants/tenant-a/dashboard', { params: { from: '2026-08-20', to: '2026-09-16' } })
  expect((wrapper.vm as any).stats[1].value).toBe(3)
  wrapper.unmount()
})

it.each([['today', '2026-09-16'], ['7days', '2026-09-10'], ['28days', '2026-08-20'], ['month', '2026-09-01']])('uses local dates for %s, including before 07:00', async (preset, from) => {
  const wrapper = shallowMount(Dashboard, { global: { plugins: [createPinia()], mocks: { $t: (key: string) => key } } })
  await flushPromises()
  mocks.get.mockClear()
  ;(wrapper.vm as any).applyPreset(preset)
  await flushPromises()
  expect(mocks.get).toHaveBeenCalledWith('/tenants/tenant-a/dashboard', { params: { from, to: '2026-09-16' } })
  wrapper.unmount()
})
