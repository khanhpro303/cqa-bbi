// @vitest-environment happy-dom
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import { enableAutoUnmount, flushPromises, shallowMount } from '@vue/test-utils'
import AiCostCarousel from '../components/dashboard/AiCostCarousel.vue'

vi.mock('../api', () => ({
  default: {
    get: async (_url: string, { params }: { params: { group_by: string } }) => ({
      data: {
        rows: params.group_by === 'channel' ? [
          { label: 'Hệ thống / Job', total_cost: 30 },
          { label: 'BBI', total_cost: 1 },
          { label: 'Kênh chưa phát sinh', total_cost: 0 },
        ] : [{ label: '2026-11-25', total_cost: 1 }],
      },
    }),
  },
}))
vi.mock('vue-i18n', () => ({
  useI18n: () => ({ t: (key: string) => key, locale: { value: 'vi' } }),
}))

enableAutoUnmount(afterEach)
beforeEach(() => vi.useFakeTimers())
afterEach(() => {
  vi.useRealTimers()
  vi.restoreAllMocks()
})

async function mountSlideshow() {
  const wrapper = shallowMount(AiCostCarousel, {
    props: { tenantId: 'test-tenant' },
    global: {
      mocks: { $t: (key: string) => key },
      stubs: {
        VCard: { template: '<div><slot /></div>' },
        VBtn: { template: '<button><slot /></button>' },
        VIcon: true,
        VProgressCircular: true,
      },
    },
  })
  await flushPromises()
  return wrapper
}

it('rotates through the four views every six seconds and wraps to day', async () => {
  const wrapper = await mountSlideshow()
  for (const title of ['cost_by_day_chart', 'cost_by_channel_chart', 'cost_by_employee_chart', 'cost_by_agent_chart', 'cost_by_day_chart']) {
    expect(wrapper.get('h2').text()).toBe(title)
    await vi.advanceTimersByTimeAsync(6000)
  }
})

it('holds the selected view during hover, keyboard focus, and manual pause', async () => {
  const wrapper = await mountSlideshow()
  await wrapper.trigger('mouseenter')
  await vi.advanceTimersByTimeAsync(6000)
  expect(wrapper.get('h2').text()).toBe('cost_by_day_chart')
  await wrapper.trigger('mouseleave')

  const channel = wrapper.get('.cost-views button:nth-child(2)')
  await channel.trigger('click')
  await channel.trigger('focusin')
  await vi.advanceTimersByTimeAsync(6000)
  expect(wrapper.get('h2').text()).toBe('cost_by_channel_chart')
  await channel.trigger('focusout')

  await wrapper.get('button[aria-label="cost_slideshow_pause"]').trigger('click')
  await vi.advanceTimersByTimeAsync(6000)
  expect(wrapper.get('h2').text()).toBe('cost_by_channel_chart')
  await wrapper.get('button[aria-label="cost_slideshow_play"]').trigger('click')
  await vi.advanceTimersByTimeAsync(6000)
  expect(wrapper.get('h2').text()).toBe('cost_by_employee_chart')
})

it('starts paused with reduced motion and leaves manual selection available', async () => {
  vi.spyOn(window, 'matchMedia').mockReturnValue({ matches: true } as MediaQueryList)
  const wrapper = await mountSlideshow()
  expect(wrapper.find('button[aria-label="cost_slideshow_play"]').exists()).toBe(true)
  await vi.advanceTimersByTimeAsync(12000)
  expect(wrapper.get('h2').text()).toBe('cost_by_day_chart')
  await wrapper.get('.cost-views button:nth-child(2)').trigger('click')
  expect(wrapper.get('h2').text()).toBe('cost_by_channel_chart')
})

it('plots grouped costs on a shared zero-based scale and shows the exact VND values', async () => {
  const wrapper = await mountSlideshow()
  await wrapper.get('.cost-views button:nth-child(2)').trigger('click')

  const chart = wrapper.get('[role="region"][aria-label="cost_by_channel_chart"]')
  const heights = chart.findAll<HTMLElement>('.cost-bars__bar').map(bar => parseFloat(bar.element.style.height))
  const values = chart.findAll('.cost-bars__value').map(value => value.text())
  const formatVnd = new Intl.NumberFormat('vi', { style: 'currency', currency: 'VND' })

  expect(chart.findAll('.cost-bars__name').map(name => name.text())).toEqual(['Hệ thống / Job', 'BBI', 'Kênh chưa phát sinh'])
  expect(values).toEqual([780000, 26000, 0].map(value => formatVnd.format(value)))
  expect(heights[0] / heights[1]).toBeCloseTo(30)
  expect(heights[2]).toBe(0)
  expect(wrapper.findAll('.cost-bars__axis span').at(-1)?.text()).toBe('0')
})
