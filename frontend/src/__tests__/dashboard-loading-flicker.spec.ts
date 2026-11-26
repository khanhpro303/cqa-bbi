// @vitest-environment happy-dom
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { defineComponent, h, nextTick, reactive, ref } from 'vue'
import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import { createPinia } from 'pinia'
import Dashboard from '../views/Dashboard.vue'
import BannerCarousel from '../components/dashboard/BannerCarousel.vue'
import AiCostCarousel from '../components/dashboard/AiCostCarousel.vue'
import OnboardingWizard from '../components/OnboardingWizard.vue'
import DashboardChartSkeleton from '../components/dashboard/DashboardChartSkeleton.vue'

const mocks = vi.hoisted(() => ({
  get: vi.fn(),
  put: vi.fn(),
  delete: vi.fn(),
  push: vi.fn(),
}))

const route = reactive({ path: '/tenant-a', params: { tenantId: 'tenant-a' } })

vi.mock('../api', () => ({
  default: {
    get: mocks.get,
    put: mocks.put,
    delete: mocks.delete,
  },
}))
vi.mock('vue-router', () => ({
  useRoute: () => route,
  useRouter: () => ({ push: mocks.push }),
}))
vi.mock('vue-i18n', () => ({
  useI18n: () => ({ t: (key: string) => key, locale: { value: 'vi' } }),
}))
vi.mock('vuetify', () => ({
  useDisplay: () => ({ smAndDown: ref(false) }),
  useTheme: () => ({ global: { current: { value: { dark: false } } } }),
}))
vi.mock('vue-chartjs', () => ({
  Line: defineComponent({
    name: 'LineChartStub',
    props: ['data', 'options', 'ariaLabel'],
    setup: (props) => () => h('div', { class: 'line-chart-stub', 'data-points': props.data?.labels?.length ?? 0 }),
  }),
}))

const Container = defineComponent({
  setup: (_, { slots, attrs }) => () => h('div', attrs, slots.default?.()),
})

const vuetifyStubs = {
  VAlert: Container,
  VBtn: defineComponent({
    props: ['icon', 'loading', 'disabled'],
    setup: (_, { slots, attrs }) => () => h('button', attrs, slots.default?.()),
  }),
  VCard: Container,
  VCardActions: Container,
  VCardText: Container,
  VCardTitle: Container,
  VCarousel: Container,
  VCarouselItem: Container,
  VChip: Container,
  VChipGroup: Container,
  VCol: Container,
  VDialog: Container,
  VIcon: defineComponent({ setup: (_, { attrs }) => () => h('span', attrs) }),
  VList: Container,
  VListItem: Container,
  VListItemTitle: Container,
  VProgressCircular: Container,
  VProgressLinear: defineComponent({ setup: (_, { attrs }) => () => h('div', { ...attrs, class: 'v-progress-linear-stub' }) }),
  VRow: Container,
  VSheet: Container,
  VSpacer: Container,
  VSwitch: Container,
  VTable: defineComponent({ setup: (_, { slots, attrs }) => () => h('table', attrs, slots.default?.()) }),
  VTextField: Container,
}

function createMemoryStorage(): Storage {
  const map = new Map<string, string>()
  return {
    get length() { return map.size },
    clear: () => map.clear(),
    getItem: (key) => map.get(key) ?? null,
    key: (index) => [...map.keys()][index] ?? null,
    removeItem: (key) => map.delete(key),
    setItem: (key, value) => map.set(key, String(value)),
  }
}

function deferred<T>() {
  let resolve!: (value: T) => void
  let reject!: (reason?: unknown) => void
  const promise = new Promise<T>((res, rej) => {
    resolve = res
    reject = rej
  })
  return { promise, resolve, reject }
}

const wrappers: VueWrapper[] = []

function track<T extends VueWrapper>(wrapper: T): T {
  wrappers.push(wrapper)
  return wrapper
}

describe('Dashboard initial loading skeletons and zero-flicker banner', () => {
  beforeEach(() => {
    vi.stubGlobal('localStorage', createMemoryStorage())
    route.path = '/tenant-a'
    route.params.tenantId = 'tenant-a'
    mocks.get.mockReset()
    mocks.put.mockReset()
    mocks.delete.mockReset()
    mocks.push.mockReset()
  })

  afterEach(() => {
    wrappers.splice(0).forEach(w => w.unmount())
    vi.unstubAllGlobals()
    vi.restoreAllMocks()
  })

  it('shows subtle skeletons while dashboard loads instead of zero values or empty states, then preserves data during preset refresh', async () => {
    const dashboardResp = deferred<{ data: Record<string, unknown> }>()
    const refreshResp = deferred<{ data: Record<string, unknown> }>()
    let dashboardCalls = 0

    mocks.get.mockImplementation((url: string) => {
      if (url.endsWith('/dashboard')) {
        dashboardCalls += 1
        return dashboardCalls === 1 ? dashboardResp.promise : refreshResp.promise
      }
      if (url.endsWith('/demo/status')) {
        return Promise.resolve({ data: { has_data: true, is_demo: false } })
      }
      if (url.endsWith('/analytics/ai-cost')) {
        return Promise.resolve({ data: { rows: [] } })
      }
      return Promise.resolve({ data: {} })
    })

    const wrapper = track(mount(Dashboard, {
      global: {
        plugins: [createPinia()],
        mocks: { $t: (key: string) => key },
        stubs: {
          ...vuetifyStubs,
          AiCostCarousel: true,
        },
      },
    }))

    await nextTick()

    // While pending: skeletons shown, no zero/empty copy flashed, banner holds reveal until offsets arrive
    expect(wrapper.findAll('.dash-skel').length).toBeGreaterThan(8)
    expect(wrapper.findComponent(DashboardChartSkeleton).exists()).toBe(true)
    expect(wrapper.text()).not.toContain('Chưa có hoạt động nào')
    expect(wrapper.text()).not.toContain('no_data')
    expect(wrapper.findComponent(BannerCarousel).props('offsetsLoading')).toBe(true)
    expect(wrapper.findAll('.banner-img.is-pending')).toHaveLength(3)

    // Resolve initial payload
    dashboardResp.resolve({
      data: {
        total_conversations: 42,
        issues_today: 3,
        active_jobs: 2,
        active_channels: 5,
        cost_today: 1,
        cost_this_month: 10,
        cost_by_day: [{ date: '2026-11-25', input_tokens: 100, output_tokens: 50, total_cost: 1 }],
        exchange_rate: 26000,
        banner_offsets: '[15,0,75]',
        qc_alerts: [{ id: 'qc-1', severity: 'NGHIEM_TRONG', evidence: 'Chậm phản hồi', conversation_id: 'c1', created_at: new Date().toISOString() }],
        classification_recent: [],
        messages_by_day: [{ date: '2026-11-25', count: 120, chat_count: 15, reply_count: 60 }],
        conversations_by_channel: [
          { channel_type: 'facebook', count: 30 },
          { channel_type: 'zalo', count: 12 },
        ],
      },
    })
    await flushPromises()

    expect(wrapper.findComponent(DashboardChartSkeleton).exists()).toBe(false)
    expect(wrapper.find('.line-chart-stub').exists()).toBe(true)
    expect(wrapper.findComponent(BannerCarousel).props('offsetsLoading')).toBe(false)
    expect(wrapper.findAll('.banner-img.is-pending')).toHaveLength(0)
    expect(wrapper.findAll('.banner-img')[0]!.attributes('style')).toContain('50% 15%')
    expect(wrapper.text()).toContain('42')
    expect(wrapper.text()).toContain('Chậm phản hồi')
    expect(localStorage.getItem('cqa_banner_offsets:tenant-a')).toBe('[15,0,75]')

    // Switching presets keeps existing content visible with subtle refresh bar (no return to initial skeletons)
    ;(wrapper.vm as any).applyPreset('7days')
    await nextTick()
    expect(wrapper.find('.v-progress-linear-stub').exists()).toBe(true)
    expect(wrapper.findComponent(DashboardChartSkeleton).exists()).toBe(false)
    expect(wrapper.text()).toContain('42')

    refreshResp.resolve({
      data: {
        total_conversations: 10,
        issues_today: 1,
        active_jobs: 2,
        active_channels: 5,
        cost_today: 0.5,
        cost_this_month: 10,
        cost_by_day: [],
        exchange_rate: 26000,
        banner_offsets: '[15,0,75]',
        qc_alerts: [],
        classification_recent: [],
        messages_by_day: [{ date: '2026-11-25', count: 30, chat_count: 5, reply_count: 10 }],
        conversations_by_channel: [],
      },
    })
    await flushPromises()
    expect(wrapper.text()).toContain('10')
  })

  it('hydrates cached banner offsets synchronously on repeat visit with zero pending flash', async () => {
    localStorage.setItem('cqa_banner_offsets:tenant-a', '[18,0,62]')
    const dashboardResp = deferred<{ data: Record<string, unknown> }>()

    mocks.get.mockImplementation((url: string) => {
      if (url.endsWith('/dashboard')) return dashboardResp.promise
      return Promise.resolve({ data: {} })
    })

    const wrapper = track(mount(Dashboard, {
      global: {
        plugins: [createPinia()],
        mocks: { $t: (key: string) => key },
        stubs: {
          ...vuetifyStubs,
          AiCostCarousel: true,
        },
      },
    }))

    await nextTick()

    const banner = wrapper.findComponent(BannerCarousel)
    expect(banner.props('offsetsLoading')).toBe(false)
    expect(banner.findAll('.banner-img.is-pending')).toHaveLength(0)
    expect(banner.findAll('.banner-img')[0]!.attributes('style')).toContain('50% 18%')
    expect(banner.findAll('.banner-img')[1]!.attributes('style')).toContain('50% 0%')
    expect(banner.findAll('.banner-img')[2]!.attributes('style')).toContain('50% 62%')
  })

  it('preserves 0% focal offset and caches saved offsets on BannerCarousel edit', async () => {
    mocks.put.mockResolvedValueOnce({ data: {} })

    const wrapper = track(mount(BannerCarousel, {
      props: {
        tenantId: 'tenant-a',
        offsets: '[0,40,80]',
        offsetsLoading: false,
        canEdit: true,
      },
      global: {
        mocks: { $t: (key: string) => key },
        stubs: vuetifyStubs,
      },
    }))

    const images = wrapper.findAll('.banner-img')
    expect(images[0]!.attributes('style')).toContain('50% 0%')
    expect(images[0]!.classes()).not.toContain('is-pending')

    await wrapper.find('.banner-frame').trigger('dblclick')
    await wrapper.find('.banner-frame').trigger('dblclick')
    await flushPromises()

    expect(mocks.put).toHaveBeenCalledWith('/tenants/tenant-a/settings', {
      key: 'banner_offsets',
      value: '[0,40,80]',
    })
    expect(localStorage.getItem('cqa_banner_offsets:tenant-a')).toBe('[0,40,80]')
    expect(wrapper.emitted('update:offsets')).toEqual([['[0,40,80]']])
  })

  it('renders chart skeleton inside AiCostCarousel while loading', async () => {
    const costResp = deferred<{ data: { rows: Array<{ label: string; total_cost: number }> } }>()
    mocks.get.mockReturnValue(costResp.promise)

    const wrapper = track(mount(AiCostCarousel, {
      props: { tenantId: 'tenant-a' },
      global: {
        mocks: { $t: (key: string) => key },
        stubs: vuetifyStubs,
      },
    }))

    await nextTick()

    const skeleton = wrapper.findComponent(DashboardChartSkeleton)
    expect(skeleton.exists()).toBe(true)
    expect(skeleton.props('variant')).toBe('line')

    costResp.resolve({ data: { rows: [{ label: '2026-11-25', total_cost: 2 }] } })
    await flushPromises()

    expect(wrapper.findComponent(DashboardChartSkeleton).exists()).toBe(false)
    expect(wrapper.find('.line-chart-stub').exists()).toBe(true)
  })

  it('keeps OnboardingWizard visible without hiding during background route revalidation', async () => {
    localStorage.setItem('cqa_onboarding_status:tenant-a', JSON.stringify({
      dismissed: false,
      steps: [{ key: 'channel', title: 'Kết nối kênh chat', done: false, link: 'channels' }],
    }))
    const pendingRevalidate = deferred<{ data: unknown }>()
    mocks.get.mockReturnValue(pendingRevalidate.promise)

    const wrapper = track(mount(OnboardingWizard, {
      global: {
        mocks: { $t: (key: string) => key },
        stubs: vuetifyStubs,
      },
    }))

    await nextTick()
    expect(wrapper.text()).toContain('Kết nối kênh chat')

    route.path = '/tenant-a/messages'
    await nextTick()
    // Should still remain visible while revalidating in background
    expect(wrapper.text()).toContain('Kết nối kênh chat')

    pendingRevalidate.resolve({
      data: {
        dismissed: false,
        steps: [{ key: 'channel', title: 'Kết nối kênh chat', done: true, link: 'channels' }],
      },
    })
    await flushPromises()
    expect(wrapper.text()).toContain('Kết nối kênh chat')
  })
})
