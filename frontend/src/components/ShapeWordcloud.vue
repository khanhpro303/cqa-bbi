<template>
  <div>
    <v-select
      v-model="shape"
      :items="shapes"
      label="Hình dạng wordcloud"
      density="compact"
      variant="outlined"
      hide-details
      class="shape-select mt-3"
    />
    <div
      ref="host"
      class="wordcloud"
      role="group"
      :aria-label="`Wordcloud ${title}`"
      @wordclouddrawn="decorateWord"
      @click="selectWord"
      @keydown="activateWord"
    />
    <p v-if="renderError" class="text-caption text-medium-emphasis">Không thể vẽ wordcloud. Bấm tên segment để xem keyword.</p>
  </div>
</template>

<script setup lang="ts">
import { onBeforeUnmount, onMounted, ref, watch } from 'vue'
import WordCloud from 'wordcloud'
import type { AggregateItem } from '../utils/service-quality'

const props = defineProps<{ items: AggregateItem[]; title: string }>()
const emit = defineEmits<{ select: [key: string] }>()
const shapes = [
  { title: 'Hình tròn', value: 'circle' },
  { title: 'Kim cương', value: 'diamond' },
  { title: 'Ngôi sao', value: 'star' },
]
const shape = ref('circle')
const host = ref<HTMLDivElement | null>(null)
const renderError = ref(false)
let observer: ResizeObserver | undefined
let frame = 0
let disposed = false

function renderCloud() {
  const element = host.value
  if (!element || disposed || !element.clientWidth) return
  renderError.value = !WordCloud.isSupported
  if (renderError.value) return
  const mask = createShapeMask(element.clientWidth, element.clientHeight)
  if (!mask) {
    renderError.value = true
    return
  }
  const counts = props.items.map(item => item.count)
  const max = Math.max(...counts, 1)
  const min = Math.min(...counts, 1)
  const largestFont = Math.min(32, element.clientWidth / 8)
  // Layout weights can be mutated by shrinkToFit; keep source counts untouched.
  const list: WordCloud.ListEntry[] = props.items.map(item => [
    item.label,
    max === min ? 22 : 12 + (largestFont - 12) * (Math.sqrt(item.count) - Math.sqrt(min)) / (Math.sqrt(max) - Math.sqrt(min)),
    item.key,
  ])
  let colorIndex = 0
  element.dispatchEvent(new CustomEvent('wordcloudstart'))
  element.replaceChildren()
  WordCloud([mask, element], {
    list,
    clearCanvas: false,
    shape: shape.value,
    ellipticity: 1,
    gridSize: 4,
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
  const radius = Math.min(width, height) / 2 - 6
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
  span.setAttribute('aria-label', `${source.label}: ${source.count} hội thoại, xem nguồn phân loại`)
  span.title = `${source.label}: ${source.count} hội thoại`
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

watch([() => props.items, shape], scheduleRender, { deep: true })
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
