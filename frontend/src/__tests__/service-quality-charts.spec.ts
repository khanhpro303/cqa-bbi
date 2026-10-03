// @vitest-environment happy-dom
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { defineComponent, h } from 'vue'
import { mount, flushPromises, type VueWrapper } from '@vue/test-utils'
import { createI18n } from 'vue-i18n'
import ServiceQualityCharts from '../components/ServiceQualityCharts.vue'
import viMessages from '../i18n/service-quality-vi'
import enMessages from '../i18n/service-quality-en'
import type { InsightRow } from '../utils/service-quality'
import { PRODUCT_GROUPING_REQUEST_TIMEOUT_MS } from '../utils/product-grouping-report'

const mocks = vi.hoisted(() => ({ post: vi.fn() }))
vi.mock('../api', () => ({ default: { post: mocks.post } }))
const Container = defineComponent({ setup: (_, { slots, attrs }) => () => h('div', attrs, slots.default?.()) })
const Button = defineComponent({ setup: (_, { slots, attrs }) => () => h('button', attrs, slots.default?.()) })
const Select = defineComponent({
  props: ['modelValue', 'items', 'label'], emits: ['update:modelValue'],
  setup: (props, { emit }) => () => h('select', { value: props.modelValue, 'aria-label': props.label, onChange: (event: Event) => emit('update:modelValue', (event.target as HTMLSelectElement).value) }, props.items.map((item: { title: string; value: string }) => h('option', { value: item.value }, item.title))),
})
const rows: InsightRow[] = [
  { conversation_id: 'high', insight_at: '2026-09-15T00:00:00Z', insight: { lead_quality: { level: 'high' }, products: [{ name: 'Mũ bảo hiểm E-24' }, { name: 'Mũ E-24' }] } },
  { conversation_id: 'medium', insight_at: '2026-09-15T00:00:00Z', insight: { lead_quality: { level: 'medium' }, products: [{ name: 'LS2 FF 818' }] } },
  { conversation_id: 'unknown', insight_at: '2026-09-15T00:00:00Z', insight: { lead_quality: { level: 'unknown' }, products: [{ name: 'LS2 FF 818' }] } },
]
const groups = [{ name: 'E-24', members: ['Mũ bảo hiểm E-24', 'Mũ E-24'] }, { name: 'FF818', members: ['LS2 FF 818'] }]
const scope = { from: '2026-09-01', to: '2026-09-30', channel_id: 'page-one' }
const wrappers: VueWrapper[] = []
function render(source = rows, enabled = true) {
  const i18n = createI18n({ legacy: false, locale: 'vi', messages: { vi: viMessages, en: enMessages } })
  const wrapper = mount(ServiceQualityCharts, { props: { rows: source, from: '2026-09-01T00:00:00Z', to: '2026-10-01T00:00:00Z', scope, tenantId: 'tenant-one', productGroupingEnabled: enabled }, global: { plugins: [i18n], stubs: { VCard: Container, VIcon: Container, VDivider: Container, VProgressCircular: Container, VBtn: Button, VSelect: Select, RouterLink: defineComponent({ props: ['to'], setup: (props, { slots }) => () => h('a', { href: props.to }, slots.default?.()) }) } } })
  wrappers.push(wrapper)
  return { wrapper, i18n }
}
beforeEach(() => { mocks.post.mockReset(); mocks.post.mockResolvedValue({ data: { enabled: true, groups } }) })
afterEach(() => {
  wrappers.splice(0).forEach(wrapper => wrapper.unmount())
  vi.restoreAllMocks()
  vi.unstubAllGlobals()
})

describe('ServiceQuality AI chart carousel', () => {
  it('renders the pie and AI grouped treemap, filters without new AI calls, and avoids calls on unchanged refresh', async () => {
    const { wrapper } = render()
    await flushPromises()
    expect(wrapper.get('svg').attributes('aria-label')).toContain('Tiềm năng cao: 1 (50%)')
    expect(wrapper.get('svg').attributes('aria-label')).toContain('Tiềm năng vừa: 1 (50%)')
    expect(wrapper.text()).toContain('Không tính 1 hội thoại')
    expect(mocks.post).toHaveBeenCalledWith('/tenants/tenant-one/service-quality/product-groups', { ...scope, product_names: ['LS2 FF 818', 'Mũ E-24', 'Mũ bảo hiểm E-24'] }, expect.objectContaining({ signal: expect.any(AbortSignal), timeout: PRODUCT_GROUPING_REQUEST_TIMEOUT_MS }))
    await wrapper.get('button[aria-label="Biểu đồ tiếp theo"]').trigger('click')
    expect(wrapper.findAll('.treemap-tile').map(tile => tile.text())).toEqual(['E-24 1', 'FF818 1'])
    await wrapper.get('select').setValue('high')
    expect(wrapper.findAll('.treemap-tile').map(tile => tile.text())).toEqual(['E-24 1'])
    await wrapper.get('select').setValue('medium')
    expect(wrapper.findAll('.treemap-tile').map(tile => tile.text())).toEqual(['FF818 1'])
    await wrapper.get('select').setValue('low')
    expect(wrapper.text()).toContain('Chưa có sản phẩm')
    await wrapper.setProps({ rows: structuredClone(rows), reportVersion: '2026-10-01T02:00:00Z' })
    expect(mocks.post).toHaveBeenCalledTimes(1)
  })

  it('shrinks every narrow tile label until the full product name and count fit on one line', async () => {
    vi.spyOn(HTMLElement.prototype, 'clientWidth', 'get').mockImplementation(function (this: HTMLElement) {
      return this.classList.contains('treemap-tile') ? 4 : 0
    })
    vi.spyOn(HTMLElement.prototype, 'clientHeight', 'get').mockImplementation(function (this: HTMLElement) {
      return this.classList.contains('treemap-tile') ? 18 : 0
    })
    vi.spyOn(HTMLElement.prototype, 'scrollWidth', 'get').mockImplementation(function (this: HTMLElement) {
      if (!this.classList.contains('tile-text')) return 0
      return Math.ceil(100 * ((Number.parseFloat(this.style.fontSize) || 13) / 13))
    })
    vi.spyOn(HTMLElement.prototype, 'scrollHeight', 'get').mockImplementation(function (this: HTMLElement) {
      if (!this.classList.contains('tile-text')) return 0
      return 16 * ((Number.parseFloat(this.style.fontSize) || 13) / 13)
    })

    const { wrapper } = render()
    await flushPromises()
    await wrapper.get('button[aria-label="Biểu đồ tiếp theo"]').trigger('click')
    await flushPromises()

    const labels = wrapper.findAll<HTMLElement>('.tile-text')
    expect(labels.map(label => label.text())).toEqual(['E-24 1', 'FF818 1'])
    for (const label of labels) {
      const fittedSize = Number.parseFloat(label.element.style.fontSize)
      expect(fittedSize).toBeLessThan(1)
      expect(Math.ceil(100 * (fittedSize / 13))).toBeLessThanOrEqual(4)
      expect(16 * (fittedSize / 13)).toBeLessThanOrEqual(16)
    }
  })

  it('scales the complete label when browser font rounding prevents an ultra-small tile from fitting', async () => {
    vi.spyOn(HTMLElement.prototype, 'clientWidth', 'get').mockImplementation(function (this: HTMLElement) {
      return this.classList.contains('treemap-tile') ? 0 : 0
    })
    vi.spyOn(HTMLElement.prototype, 'clientHeight', 'get').mockImplementation(function (this: HTMLElement) {
      return this.classList.contains('treemap-tile') ? 0 : 0
    })
    vi.spyOn(HTMLElement.prototype, 'scrollWidth', 'get').mockImplementation(function (this: HTMLElement) {
      return this.classList.contains('tile-text') ? 5 : 0
    })
    vi.spyOn(HTMLElement.prototype, 'scrollHeight', 'get').mockImplementation(function (this: HTMLElement) {
      return this.classList.contains('tile-text') ? 4 : 0
    })

    const { wrapper } = render()
    await flushPromises()
    await wrapper.get('button[aria-label="Biểu đồ tiếp theo"]').trigger('click')
    await flushPromises()

    for (const label of wrapper.findAll<HTMLElement>('.tile-text')) {
      expect(label.element.style.fontSize).toBe('0.1px')
      expect(label.element.style.transform).toBe('scale(0.2)')
      expect(5 * 0.2).toBeLessThanOrEqual(1)
      expect(4 * 0.2).toBeLessThanOrEqual(1)
    }
  })

  it('refits labels after tile resize and releases the observer lifecycle', async () => {
    let tileWidth = 46
    let resizeCallback: ResizeObserverCallback | undefined
    let frameCallback: FrameRequestCallback | undefined
    const observe = vi.fn()
    const unobserve = vi.fn()
    const disconnect = vi.fn()
    class ResizeObserverStub {
      constructor(callback: ResizeObserverCallback) { resizeCallback = callback }
      observe = observe
      unobserve = unobserve
      disconnect = disconnect
    }
    vi.stubGlobal('ResizeObserver', ResizeObserverStub)
    vi.stubGlobal('requestAnimationFrame', (callback: FrameRequestCallback) => { frameCallback = callback; return 1 })
    vi.stubGlobal('cancelAnimationFrame', vi.fn())
    vi.spyOn(Node.prototype, 'isConnected', 'get').mockReturnValue(true)
    vi.spyOn(HTMLElement.prototype, 'clientWidth', 'get').mockImplementation(function (this: HTMLElement) {
      return this.classList.contains('treemap-tile') ? tileWidth : 0
    })
    vi.spyOn(HTMLElement.prototype, 'clientHeight', 'get').mockImplementation(function (this: HTMLElement) {
      return this.classList.contains('treemap-tile') ? 18 : 0
    })
    vi.spyOn(HTMLElement.prototype, 'scrollWidth', 'get').mockImplementation(function (this: HTMLElement) {
      if (!this.classList.contains('tile-text')) return 0
      return Math.ceil(100 * ((Number.parseFloat(this.style.fontSize) || 13) / 13))
    })
    vi.spyOn(HTMLElement.prototype, 'scrollHeight', 'get').mockImplementation(function (this: HTMLElement) {
      if (!this.classList.contains('tile-text')) return 0
      return Math.ceil(16 * ((Number.parseFloat(this.style.fontSize) || 13) / 13))
    })

    const { wrapper } = render()
    await flushPromises()
    await wrapper.get('button[aria-label="Biểu đồ tiếp theo"]').trigger('click')
    await flushPromises()
    expect(observe).toHaveBeenCalledTimes(2)
    const firstTile = wrapper.get<HTMLElement>('.treemap-tile').element
    const firstLabel = wrapper.get<HTMLElement>('.tile-text').element
    const initialSize = Number.parseFloat(firstLabel.style.fontSize)

    tileWidth = 20
    resizeCallback!([{ target: firstTile } as unknown as ResizeObserverEntry], {} as ResizeObserver)
    frameCallback!(0)
    expect(Number.parseFloat(firstLabel.style.fontSize)).toBeLessThan(initialSize)

    await wrapper.get('select').setValue('high')
    expect(unobserve).toHaveBeenCalled()
    wrappers.splice(wrappers.indexOf(wrapper), 1)
    wrapper.unmount()
    expect(disconnect).toHaveBeenCalledOnce()
  })

  it('offers retry on AI failure and never displays locally grouped fallback names', async () => {
    mocks.post.mockRejectedValueOnce(new Error('AI unavailable'))
    const { wrapper } = render()
    await flushPromises()
    await wrapper.get('button[aria-label="Biểu đồ tiếp theo"]').trigger('click')
    expect(wrapper.findAll('.treemap-tile')).toHaveLength(0)
    expect(wrapper.text()).toContain('Không thể gom sản phẩm bằng AI')
    await wrapper.findAll('button').find(button => button.text() === 'Thử lại')!.trigger('click')
    await flushPromises()
    expect(wrapper.findAll('.treemap-tile')).toHaveLength(2)
    expect(mocks.post).toHaveBeenCalledTimes(2)
  })

  it('ignores obsolete responses after tenant changes and sends the new scope', async () => {
    let finish!: (value: unknown) => void
    mocks.post.mockImplementationOnce(() => new Promise(resolve => { finish = resolve }))
    const { wrapper } = render()
    const oldSignal = mocks.post.mock.calls[0]![2].signal as AbortSignal
    await wrapper.setProps({ tenantId: 'tenant-two', scope: { ...scope, channel_id: 'page-two' } })
    await flushPromises()
    expect(oldSignal.aborted).toBe(true)
    finish({ data: { enabled: true, groups: [{ name: 'Wrong tenant', members: groups.flatMap(group => group.members) }] } })
    await flushPromises()
    await wrapper.get('button[aria-label="Biểu đồ tiếp theo"]').trigger('click')
    expect(wrapper.text()).not.toContain('Wrong tenant')
    expect(wrapper.findAll('.treemap-tile')).toHaveLength(2)
    expect(mocks.post.mock.calls[1]![0]).toBe('/tenants/tenant-two/service-quality/product-groups')
  })

  it('explains how to narrow an oversized report rather than offering an ineffective retry', async () => {
    mocks.post.mockRejectedValueOnce({ response: { status: 422 } })
    const { wrapper } = render()
    await flushPromises()
    await wrapper.get('button[aria-label="Biểu đồ tiếp theo"]').trigger('click')
    expect(wrapper.text()).toContain('Chọn khoảng ngày ngắn hơn hoặc một Fanpage')
    expect(wrapper.findAll('button').some(button => button.text() === 'Thử lại')).toBe(false)
    expect(wrapper.findAll('.treemap-tile')).toHaveLength(0)
  })

  it('reloads the displayed report on a snapshot conflict and groups the updated source names', async () => {
    mocks.post.mockRejectedValueOnce({ response: { status: 409 } })
    const { wrapper } = render()
    await flushPromises()
    expect(wrapper.emitted('refresh-report')).toHaveLength(1)
    await wrapper.get('button[aria-label="Biểu đồ tiếp theo"]').trigger('click')
    expect(wrapper.text()).toContain('Dữ liệu vừa thay đổi')
    const updated = structuredClone(rows)
    updated[0]!.insight!.products = [{ name: 'E-24' }]
    mocks.post.mockResolvedValueOnce({ data: { enabled: true, groups: [{ name: 'E-24', members: ['E-24'] }, groups[1]] } })
    await wrapper.setProps({ rows: updated })
    await flushPromises()
    expect(wrapper.findAll('.treemap-tile')).toHaveLength(2)
    expect(wrapper.find('[role="alert"]').exists()).toBe(false)
    expect(mocks.post.mock.calls[1]![1].product_names).toEqual(['E-24', 'LS2 FF 818'])
  })

  it('retries grouping after a conflicted report reload even if the labels settle back to the same set', async () => {
    mocks.post.mockRejectedValueOnce({ response: { status: 409 } })
    const { wrapper } = render()
    await flushPromises()
    await wrapper.setProps({ reportVersion: '2026-10-01T02:00:00Z' })
    await flushPromises()
    await wrapper.get('button[aria-label="Biểu đồ tiếp theo"]').trigger('click')
    expect(wrapper.findAll('.treemap-tile')).toHaveLength(2)
    expect(mocks.post).toHaveBeenCalledTimes(2)
  })

  it('shows empty states without AI calls and updates translations', async () => {
    const { wrapper, i18n } = render([])
    await flushPromises()
    expect(wrapper.text()).toContain('Chưa có dữ liệu tiềm năng')
    expect(mocks.post).not.toHaveBeenCalled()
    i18n.global.locale.value = 'en'
    await flushPromises()
    expect(wrapper.text()).toContain('No lead potential data')
    await wrapper.get('button[aria-label="Next chart"]').trigger('click')
    expect(wrapper.text()).toContain('No products')
  })
  it('shows only the disabled notice and task link when there is no grouping job, while keeping the pie', async () => {
    const { wrapper } = render(rows, false)
    await flushPromises()
    expect(wrapper.find('svg').exists()).toBe(true)
    expect(mocks.post).not.toHaveBeenCalled()
    await wrapper.get('button[aria-label="Biểu đồ tiếp theo"]').trigger('click')
    expect(wrapper.text()).toContain('Đã tắt tính năng tổng hợp sản phẩm, để bật vui lòng đến Tác vụ AI')
    expect(wrapper.get('a').attributes('href')).toBe('/tenant-one/jobs')
    expect(wrapper.find('select').exists()).toBe(false)
    expect(wrapper.find('.chart-note').exists()).toBe(false)
    await wrapper.setProps({ productGroupingEnabled: true })
    await flushPromises()
    expect(wrapper.findAll('.treemap-tile')).toHaveLength(2)
    expect(mocks.post).toHaveBeenCalledTimes(1)
  })
  it('ignores in-flight results after the job is deleted', async () => {
    let finish!: (value: unknown) => void
    mocks.post.mockImplementationOnce(() => new Promise(resolve => { finish = resolve }))
    const { wrapper } = render()
    const signal = mocks.post.mock.calls[0]![2].signal as AbortSignal
    await wrapper.setProps({ productGroupingEnabled: false })
    expect(signal.aborted).toBe(true)
    finish({ data: { enabled: true, groups } })
    await flushPromises()
    await wrapper.get('button[aria-label="Biểu đồ tiếp theo"]').trigger('click')
    expect(wrapper.findAll('.treemap-tile')).toHaveLength(0)
    expect(wrapper.text()).toContain('Đã tắt tính năng')
  })
  it('uses the disabled notice if the server reports deletion before the report refreshes', async () => {
    mocks.post.mockResolvedValueOnce({ data: { enabled: false, groups: [] } })
    const { wrapper } = render()
    await flushPromises()
    await wrapper.get('button[aria-label="Biểu đồ tiếp theo"]').trigger('click')
    expect(wrapper.text()).toContain('Đã tắt tính năng')
    expect(wrapper.find('select').exists()).toBe(false)
    expect(wrapper.find('[role="alert"]').exists()).toBe(false)
    expect(wrapper.emitted('refresh-report')).toHaveLength(1)
  })
  it('regroups when the effective prompt version changes without changing the products', async () => {
    const { wrapper } = render()
    await flushPromises()
    await wrapper.setProps({ productGroupingVersion: 'new-prompt' })
    await flushPromises()
    expect(mocks.post).toHaveBeenCalledTimes(2)
  })

})
