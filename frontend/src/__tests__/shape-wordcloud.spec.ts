// @vitest-environment happy-dom
import { createI18n } from 'vue-i18n'
import qualityInsightVi from '../i18n/quality-insight-vi'
import qualityInsightEn from '../i18n/quality-insight-en'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { defineComponent, h, nextTick } from 'vue'
import { mount, type VueWrapper } from '@vue/test-utils'
import type WordCloud from 'wordcloud'

const mocks = vi.hoisted(() => ({ render: vi.fn(), stop: vi.fn() }))
// Avoid running the library's canvas support probe in happy-dom.
vi.mock('wordcloud', () => ({ default: Object.assign(mocks.render, { isSupported: true, stop: mocks.stop }) }))
import ShapeWordcloud from '../components/ShapeWordcloud.vue'

const Select = defineComponent({
  props: ['modelValue', 'items'], emits: ['update:modelValue'],
  setup: (props, { emit }) => () => h('select', {
    value: props.modelValue,
    onChange: (event: Event) => emit('update:modelValue', (event.target as HTMLSelectElement).value),
  }, props.items.map((item: { title: string; value: string }) => h('option', { value: item.value }, item.title))),
})
let width = 320
let nextFrame = 0
let frames: Map<number, FrameRequestCallback>
let observers: Observer[]
const wrappers: VueWrapper[] = []
class Observer {
  observe = vi.fn()
  disconnect = vi.fn()
  callback: ResizeObserverCallback
  constructor(callback: ResizeObserverCallback) { this.callback = callback; observers.push(this) }
  resize() { this.callback([], this as unknown as ResizeObserver) }
}
const items = [
  { key: 'sku:first', label: 'Áo xanh', count: 9, conversationIds: ['a'] },
  { key: 'name:second', label: 'Áo xanh', count: 2, conversationIds: ['b'] },
]
beforeEach(() => {
  width = 320
  frames = new Map()
  observers = []
  mocks.render.mockReset()
  mocks.stop.mockReset()
  vi.spyOn(HTMLElement.prototype, 'clientWidth', 'get').mockImplementation(() => width)
  vi.spyOn(HTMLElement.prototype, 'clientHeight', 'get').mockReturnValue(280)
  vi.spyOn(HTMLCanvasElement.prototype, 'getContext').mockReturnValue({
    fillRect: vi.fn(), beginPath: vi.fn(), arc: vi.fn(), moveTo: vi.fn(),
    lineTo: vi.fn(), closePath: vi.fn(), fill: vi.fn(),
  } as unknown as CanvasRenderingContext2D)
  vi.stubGlobal('ResizeObserver', Observer)
  vi.stubGlobal('requestAnimationFrame', (callback: FrameRequestCallback) => {
    const id = ++nextFrame
    frames.set(id, callback)
    return id
  })
  vi.stubGlobal('cancelAnimationFrame', (id: number) => frames.delete(id))
})
afterEach(() => {
  wrappers.splice(0).forEach(wrapper => wrapper.unmount())
  vi.restoreAllMocks()
  vi.unstubAllGlobals()
})
async function runFrames() {
  await nextTick()
  const pending = [...frames.values()]
  frames.clear()
  pending.forEach(callback => callback(0))
  await nextTick()
}
async function render(i18n = createI18n({ legacy: false, locale: 'vi', messages: { vi: { qualityInsight: qualityInsightVi }, en: { qualityInsight: qualityInsightEn } } })) {
  const wrapper = mount(ShapeWordcloud, { props: { items, title: 'Sản phẩm' }, global: { plugins: [i18n], stubs: { VSelect: Select } } })
  wrappers.push(wrapper)
  await runFrames()
  return wrapper
}
function lastRender(): [HTMLElement, WordCloud.Options] {
  const [targets, options] = mocks.render.mock.calls.at(-1) as [[HTMLCanvasElement, HTMLElement], WordCloud.Options]
  return [Array.isArray(targets) ? targets[1] : targets, options]
}
function draw(host: HTMLElement, item: WordCloud.ListEntry, drawn = true) {
  const span = document.createElement('span')
  span.textContent = item[0]
  host.appendChild(span)
  host.dispatchEvent(new CustomEvent('wordclouddrawn', { detail: { item, drawn } }))
  return span
}

describe('ShapeWordcloud', () => {
  it('updates shape options and rendered accessibility labels when language switches', async () => {
    const i18n = createI18n({ legacy: false, locale: 'vi', messages: { vi: { qualityInsight: qualityInsightVi }, en: { qualityInsight: qualityInsightEn } } })
    const wrapper = await render(i18n)
    i18n.global.locale.value = 'en'
    await runFrames()
    expect(wrapper.findAll('option').map(option => option.text())).toEqual(['Circle', 'Diamond', 'Star'])
    const [host, options] = lastRender()
    const span = draw(host, options.list![0]!)
    expect(span.getAttribute('aria-label')).toBe('Áo xanh: 9 conversations, view classification sources')
  })

  it('retries an empty canvas mask once with library shape placement and keeps keyword clicks', async () => {
    const wrapper = await render()
    await wrapper.get('select').setValue('star')
    await runFrames()
    const calls = mocks.render.mock.calls.length
    const host = wrapper.get('.wordcloud').element as HTMLElement
    host.dispatchEvent(new CustomEvent('wordcloudstop'))
    await runFrames()
    expect(mocks.render).toHaveBeenCalledTimes(calls + 1)
    expect(mocks.render.mock.calls.at(-1)![0]).toBe(host)
    expect(lastRender()[1]).toMatchObject({ shape: 'star', clearCanvas: true })
    const span = draw(host, lastRender()[1].list![0]!)
    host.dispatchEvent(new CustomEvent('wordcloudstop'))
    await runFrames()
    span.click()
    expect(wrapper.emitted('select')).toEqual([['sku:first']])
    expect(mocks.render).toHaveBeenCalledTimes(calls + 1)
  })

  it('shows an error instead of endlessly retrying when both layouts are empty', async () => {
    const wrapper = await render()
    const host = wrapper.get('.wordcloud').element
    host.dispatchEvent(new CustomEvent('wordcloudstop'))
    await runFrames()
    const calls = mocks.render.mock.calls.length
    host.dispatchEvent(new CustomEvent('wordcloudstop'))
    await runFrames()
    expect(wrapper.text()).toContain('Không thể vẽ wordcloud')
    expect(mocks.render).toHaveBeenCalledTimes(calls)
  })

  it('packs the selected shape and remeasures after resize', async () => {
    const wrapper = await render()
    expect((mocks.render.mock.calls.at(-1)![0] as HTMLCanvasElement[])[0]).toMatchObject({ width: 320, height: 280 })
    expect(lastRender()[1]).toMatchObject({ clearCanvas: false, shape: 'circle', shrinkToFit: true, drawOutOfBound: false, ellipticity: 1 })
    expect(observers[0]!.observe).toHaveBeenCalledWith(wrapper.get('.wordcloud').element)
    for (const shape of ['star', 'diamond']) {
      await wrapper.get('select').setValue(shape)
      await runFrames()
      expect(lastRender()[1].shape).toBe(shape)
    }
    const previousWeight = lastRender()[1].list![0]![1]
    const calls = mocks.render.mock.calls.length
    width = 160
    observers[0]!.resize()
    observers[0]!.resize()
    await runFrames()
    expect(mocks.render).toHaveBeenCalledTimes(calls + 1)
    expect(lastRender()[1].list![0]![1]).toBeLessThan(previousWeight)
  })

  it('preserves distinct source keys and true counts when duplicate labels shrink', async () => {
    const wrapper = await render()
    const [host, options] = lastRender()
    const list = options.list!
    expect(list.map(item => item[2])).toEqual(['sku:first', 'name:second'])
    list[0]![1] = 1
    const first = draw(host, list[0]!)
    const second = draw(host, list[1]!)
    expect(first.getAttribute('aria-label')).toBe('Áo xanh: 9 hội thoại, xem nguồn phân loại')
    expect(first.title).toBe('Áo xanh: 9 hội thoại')
    expect(second.getAttribute('aria-label')).toContain('2 hội thoại')
    expect(first.getAttribute('role')).toBe('button')
    expect(first.tabIndex).toBe(0)
    first.click()
    second.click()
    expect(wrapper.emitted('select')).toEqual([['sku:first'], ['name:second']])
    expect(items.map(item => item.count)).toEqual([9, 2])
  })

  it('selects with Enter and Space and ignores undrawn words and other keys', async () => {
    const wrapper = await render()
    const [host, options] = lastRender()
    const span = draw(host, options.list![0]!)
    for (const key of ['Enter', ' ']) {
      const event = new KeyboardEvent('keydown', { key, bubbles: true, cancelable: true })
      span.dispatchEvent(event)
      expect(event.defaultPrevented).toBe(true)
    }
    span.dispatchEvent(new KeyboardEvent('keydown', { key: 'ArrowDown', bubbles: true }))
    draw(host, options.list![1]!, false).click()
    expect(wrapper.emitted('select')).toEqual([['sku:first'], ['sku:first']])
  })

  it('cleans only the unmounted host without globally stopping other segments', async () => {
    const first = await render()
    const second = await render()
    const firstStart = vi.fn()
    const secondStart = vi.fn()
    first.get('.wordcloud').element.addEventListener('wordcloudstart', firstStart)
    second.get('.wordcloud').element.addEventListener('wordcloudstart', secondStart)
    observers[0]!.resize()
    first.unmount()
    wrappers.splice(wrappers.indexOf(first), 1)
    expect(firstStart).toHaveBeenCalledOnce()
    expect(secondStart).not.toHaveBeenCalled()
    expect(observers[0]!.disconnect).toHaveBeenCalledOnce()
    expect(observers[1]!.disconnect).not.toHaveBeenCalled()
    const calls = mocks.render.mock.calls.length
    observers[0]!.resize()
    await runFrames()
    expect(mocks.render).toHaveBeenCalledTimes(calls)
    observers[1]!.resize()
    await runFrames()
    expect(mocks.render).toHaveBeenCalledTimes(calls + 1)
    expect(mocks.stop).not.toHaveBeenCalled()
  })
})
