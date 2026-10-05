<template>
  <div>
    <v-select
      v-model="shape"
      :items="shapes"
      :label="t('qualityInsight.shapeLabel')"
      density="compact"
      variant="outlined"
      hide-details
      class="shape-select mt-3"
    />
    <div
      ref="host"
      class="wordcloud"
      role="group"
      :aria-label="t('qualityInsight.cloudTitle', { title })"
      @wordclouddrawn="decorateWord"
      @wordcloudstop="finishRender"
      @click="selectWord"
      @keydown="activateWord"
    />
    <p v-if="renderError" class="text-caption text-medium-emphasis">{{ t('qualityInsight.renderError') }}</p>
  </div>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import WordCloud from 'wordcloud'
import type { AggregateItem } from '../utils/service-quality'

const props = defineProps<{ items: AggregateItem[]; title: string }>()
const emit = defineEmits<{ select: [key: string] }>()
const { t, locale } = useI18n()
const shapes = computed(() => [
  { title: t('qualityInsight.circle'), value: 'circle' },
  { title: t('qualityInsight.diamond'), value: 'diamond' },
  { title: t('qualityInsight.star'), value: 'star' },
])
const shape = ref('circle')
const host = ref<HTMLDivElement | null>(null)
const renderError = ref(false)
let observer: ResizeObserver | undefined
let frame = 0
let disposed = false
let maskedRender = true

const GRID_SIZE = 4
// Estimated glyph box for a 600-weight UI font, in em.
const TEXT_WIDTH_EM = .58
const LINE_HEIGHT_EM = 1.05
// Share of the shape that packed words can cover; the spiral leaves gaps.
const FILL_RATIO = .65
const MIN_FONT = 11
const MAX_FONT = 96
// Keep a visible size gap between frequent and rare keywords in crowded clouds;
// shrinkToFit still reduces words that cannot be placed.
const MIN_TOP_FONT = 18

interface SizedLabel { length: number; rank: number }

function shapeRadius(width: number, height: number) {
  return Math.max(Math.min(width, height) / 2 - 6, 1)
}

function shapeArea(radius: number) {
  if (shape.value === 'circle') return Math.PI * radius ** 2
  if (shape.value === 'diamond') return 2 * radius ** 2
  // Five-point star whose inner radius is half of the outer radius.
  return 5 * radius * (radius * .5) * Math.sin(Math.PI / 5)
}

// Widest horizontal run near the shape centre that one word can occupy.
function shapeSpan(radius: number) {
  if (shape.value === 'circle') return 1.8 * radius
  if (shape.value === 'diamond') return 1.4 * radius
  return radius
}

function fontSize(top: number, label: SizedLabel) {
  return MIN_FONT + (top - MIN_FONT) * label.rank
}

/**
 * Largest top font size whose estimated text area fits inside the shape. It
 * grows when there are few or short keywords and shrinks as the cloud fills.
 */
function fitTopFont(labels: SizedLabel[], radius: number) {
  const budget = FILL_RATIO * shapeArea(radius)
  const span = shapeSpan(radius)
  const fits = (top: number) => {
    let area = 0
    for (const label of labels) {
      const size = fontSize(top, label)
      const width = size * label.length * TEXT_WIDTH_EM
      if (width > span) return false
      area += (width + GRID_SIZE) * (size * LINE_HEIGHT_EM + GRID_SIZE)
    }
    return area <= budget
  }
  let low = MIN_FONT
  let high = Math.max(MIN_FONT, Math.min(MAX_FONT, radius * .6))
  if (fits(high)) return high
  for (let step = 0; step < 20; step++) {
    const middle = (low + high) / 2
    if (fits(middle)) low = middle
    else high = middle
  }
  return Math.max(low, Math.min(MIN_TOP_FONT, high))
}

function renderCloud(useMask: boolean | number = true) {
  const element = host.value
  if (!element || disposed || !element.clientWidth) return
  renderError.value = !WordCloud.isSupported
  if (renderError.value) return
  maskedRender = useMask !== false
  const mask = createShapeMask(element.clientWidth, element.clientHeight)
  if (!mask) {
    renderError.value = true
    return
  }
  const counts = props.items.map(item => item.count)
  const max = Math.max(...counts, 1)
  const min = Math.min(...counts, 1)
  const labels: SizedLabel[] = props.items.map(item => ({
    length: Math.max([...item.label.normalize('NFC')].length, 1),
    rank: max === min ? 1 : (Math.sqrt(item.count) - Math.sqrt(min)) / (Math.sqrt(max) - Math.sqrt(min)),
  }))
  const top = fitTopFont(labels, shapeRadius(element.clientWidth, element.clientHeight))
  // Layout weights can be mutated by shrinkToFit; keep source counts untouched.
  const list: WordCloud.ListEntry[] = props.items.map((item, index) => [
    item.label,
    fontSize(top, labels[index]!),
    item.key,
  ])
  let colorIndex = 0
  element.dispatchEvent(new CustomEvent('wordcloudstart'))
  element.replaceChildren()
  WordCloud(maskedRender ? [mask, element] : element, {
    list,
    clearCanvas: !maskedRender,
    shape: shape.value,
    ellipticity: 1,
    gridSize: GRID_SIZE,
    fontFamily: getComputedStyle(element).fontFamily,
    fontWeight: '600',
    weightFactor: 1,
    minSize: 10,
    shrinkToFit: true,
    drawOutOfBound: false,
    rotateRatio: .25,
    minRotation: -Math.PI / 2,
    maxRotation: Math.PI / 2,
    rotationSteps: 2,
    shuffle: false,
    backgroundColor: 'transparent',
    color: () => `var(--cloud-${colorIndex++ % 5 + 1})`,
    classes: 'cloud-word',
    abortThreshold: 500,
    wait: 1,
  })
}

function finishRender() {
  const element = host.value
  if (!element || disposed || !props.items.length || element.children.length) return
  if (maskedRender) {
    // Some browsers alter canvas pixels. The mask can then reserve every grid
    // cell; retry using the library's shape placement without pixel comparison.
    maskedRender = false
    cancelAnimationFrame(frame)
    frame = requestAnimationFrame(() => renderCloud(false))
  } else {
    renderError.value = true
  }
}

function createShapeMask(width: number, height: number): HTMLCanvasElement | null {
  const canvas = document.createElement('canvas')
  canvas.width = width
  canvas.height = height
  const context = canvas.getContext('2d')
  if (!context) return null
  // Opaque pixels reserve the area outside the shape in the library's grid.
  context.fillStyle = '#000'
  context.fillRect(0, 0, width, height)
  context.globalCompositeOperation = 'destination-out'
  context.beginPath()
  const radius = shapeRadius(width, height)
  const x = width / 2
  const y = height / 2
  if (shape.value === 'circle') {
    context.arc(x, y, radius, 0, 2 * Math.PI)
  } else {
    const points = shape.value === 'star' ? 10 : 4
    for (let index = 0; index < points; index++) {
      const angle = -Math.PI / 2 + index * 2 * Math.PI / points
      const distance = shape.value === 'star' && index % 2 ? radius * .5 : radius
      const px = x + Math.cos(angle) * distance
      const py = y + Math.sin(angle) * distance
      if (index === 0) context.moveTo(px, py)
      else context.lineTo(px, py)
    }
    context.closePath()
  }
  context.fill()
  return canvas
}

function scheduleRender() {
  if (disposed) return
  cancelAnimationFrame(frame)
  frame = requestAnimationFrame(renderCloud)
}

function decorateWord(event: Event) {
  const { item, drawn } = (event as CustomEvent<{ item: WordCloud.ListEntry; drawn: boolean }>).detail
  if (!drawn) return
  const source = props.items.find(source => source.key === item[2])
  const span = host.value?.lastElementChild
  if (!source || !(span instanceof HTMLElement)) return
  span.dataset.keyword = source.key
  span.tabIndex = 0
  span.setAttribute('role', 'button')
  span.setAttribute('aria-label', t('qualityInsight.wordSource', { label: source.label, count: source.count }))
  span.title = t('qualityInsight.wordCount', { label: source.label, count: source.count })
}

function selectWord(event: Event) {
  const target = (event.target as HTMLElement).closest<HTMLElement>('[data-keyword]')
  const key = target?.dataset.keyword
  if (key && host.value?.contains(target)) emit('select', key)
}

function activateWord(event: KeyboardEvent) {
  if (event.key !== 'Enter' && event.key !== ' ') return
  event.preventDefault()
  selectWord(event)
}

watch([() => props.items, shape, locale], scheduleRender, { deep: true })
onMounted(() => {
  observer = new ResizeObserver(scheduleRender)
  if (host.value) observer.observe(host.value)
  scheduleRender()
  // Re-measure after the application font has loaded.
  document.fonts?.ready.then(scheduleRender)
})
onBeforeUnmount(() => {
  disposed = true
  cancelAnimationFrame(frame)
  observer?.disconnect()
  // Stop this host's layout and listeners without stopping other segments.
  host.value?.dispatchEvent(new CustomEvent('wordcloudstart'))
})
</script>

<style scoped>
.shape-select { max-width: 200px; }
.wordcloud { position: relative; width: 100%; height: 280px; overflow: hidden; margin-top: 8px; }
.wordcloud :deep(.cloud-word) { cursor: pointer; }
.wordcloud :deep(.cloud-word:hover) { text-decoration: underline; }
.wordcloud :deep(.cloud-word:focus-visible) { outline: 2px solid rgb(var(--v-theme-primary)); outline-offset: 2px; }
</style>
