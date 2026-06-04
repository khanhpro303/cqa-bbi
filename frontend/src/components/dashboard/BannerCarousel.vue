<template>
  <v-card class="banner-carousel mb-4 rounded-lg overflow-hidden" elevation="2">
    <div @mouseenter="paused = true" @mouseleave="paused = false">
      <v-carousel
        v-model="slide"
        :height="bannerHeight"
        :cycle="!paused && editingIndex === null"
        :interval="5000"
        :touch="editingIndex === null"
        progress="primary"
        hide-delimiter-background
        show-arrows="hover"
      >
        <v-carousel-item
          v-for="(banner, index) in banners"
          :key="banner.src"
        >
          <div
            class="banner-frame"
            :class="{ 'is-editing': editingIndex === index, 'can-edit': canEdit }"
            @dblclick="toggleEdit(index)"
            @pointerdown="onPointerDown($event, index)"
            @pointermove="onPointerMove"
            @pointerup="onPointerUp"
            @pointercancel="onPointerUp"
          >
            <img
              class="banner-img"
              :src="banner.src"
              :alt="banner.alt"
              :style="{ objectPosition: `50% ${offsets[index]}%` }"
              draggable="false"
            />

            <div v-if="editingIndex === index" class="banner-edit-hint">
              <v-icon size="16" class="mr-1">mdi-cursor-move</v-icon>
              Kéo lên/xuống để chỉnh vị trí · Double-click để lưu
              <button class="banner-reset" type="button" @click.stop="resetOffset(index)">
                Giữa
              </button>
            </div>

            <div v-if="saving" class="banner-saving">
              <v-icon size="14" class="mr-1">mdi-loading mdi-spin</v-icon>Đang lưu…
            </div>
          </div>
        </v-carousel-item>
      </v-carousel>
    </div>
  </v-card>
</template>

<script setup lang="ts">
import { ref, computed, reactive, watch } from 'vue'
import { useDisplay } from 'vuetify'
import api from '../../api'
import doiNguG20 from '../../assets/banners/doi-ngu-g20.jpg'
import doiNguBbi from '../../assets/banners/doi-ngu-bbi.jpg'
import khoArthur from '../../assets/banners/kho-arthur.jpg'

// Shared banner: the crop mask stays fixed (object-fit cover); only the vertical
// focal point (objectPosition Y, 0-100%) is adjustable. Admins double-click + drag
// to reposition, and the offsets persist to the tenant settings store so every
// dashboard viewer sees the same crop.
const props = defineProps<{
  // JSON array string of percentages, e.g. "[50,30,70]". Comes from GET /dashboard.
  offsets?: string | null
  // Only admins (settings:w) may reposition and save.
  canEdit?: boolean
  // Required to PUT the shared setting.
  tenantId: string
}>()

const banners = [
  { src: doiNguBbi, alt: 'Đội ngũ BBI' },
  { src: doiNguG20, alt: 'Đội ngũ G20' },
  { src: khoArthur, alt: 'Kho Arthur' },
]

const DEFAULT_OFFSET = 50
const DRAG_SENSITIVITY = 0.4 // % of object-position per pixel dragged

const slide = ref(0)
const paused = ref(false)
const saving = ref(false)
const editingIndex = ref<number | null>(null)

// Per-banner vertical focal point (%). Index-aligned with `banners`.
const offsets = reactive<number[]>(parseOffsets(props.offsets))

const { smAndDown } = useDisplay()
const bannerHeight = computed(() => (smAndDown.value ? 160 : 220))

function clamp(value: number): number {
  return Math.min(100, Math.max(0, value))
}

function parseOffsets(raw?: string | null): number[] {
  if (raw) {
    try {
      const parsed = JSON.parse(raw)
      if (Array.isArray(parsed)) {
        return banners.map((_, i) => clamp(Number(parsed[i]) || DEFAULT_OFFSET))
      }
    } catch {
      // ignore malformed value, fall back to defaults
    }
  }
  return banners.map(() => DEFAULT_OFFSET)
}

// Re-sync if the parent loads the dashboard payload after mount.
watch(
  () => props.offsets,
  (raw) => {
    if (editingIndex.value !== null) return // don't clobber an in-progress edit
    const next = parseOffsets(raw)
    next.forEach((v, i) => { offsets[i] = v })
  },
)

async function persistOffsets() {
  if (!props.canEdit) return
  saving.value = true
  try {
    await api.put(`/tenants/${props.tenantId}/settings`, {
      key: 'banner_offsets',
      value: JSON.stringify(offsets),
    })
  } catch (e: any) {
    alert('Không lưu được vị trí banner: ' + (e?.response?.data?.error || e?.message || 'lỗi'))
  } finally {
    saving.value = false
  }
}

function toggleEdit(index: number) {
  if (!props.canEdit) return
  if (editingIndex.value === index) {
    // Exiting edit mode → persist the shared position.
    editingIndex.value = null
    persistOffsets()
  } else {
    editingIndex.value = index
  }
}

function resetOffset(index: number) {
  offsets[index] = DEFAULT_OFFSET
}

// Drag state
const dragStartY = ref(0)
const dragStartOffset = ref(DEFAULT_OFFSET)
const dragging = ref(false)

function onPointerDown(event: PointerEvent, index: number) {
  if (editingIndex.value !== index) return
  dragging.value = true
  dragStartY.value = event.clientY
  dragStartOffset.value = offsets[index]
  ;(event.target as HTMLElement).setPointerCapture?.(event.pointerId)
  event.preventDefault()
}

function onPointerMove(event: PointerEvent) {
  if (!dragging.value || editingIndex.value === null) return
  // Drag down = reveal upper part of image (object-position decreases).
  const delta = event.clientY - dragStartY.value
  offsets[editingIndex.value] = clamp(dragStartOffset.value - delta * DRAG_SENSITIVITY)
}

function onPointerUp() {
  dragging.value = false
}
</script>

<style scoped>
.banner-carousel {
  /* Keep the banner readable as a band, never a full-bleed hero. */
  max-height: 220px;
}

.banner-frame {
  position: relative;
  width: 100%;
  height: 100%;
  overflow: hidden;
}

.banner-frame.can-edit {
  cursor: default;
}

.banner-frame.is-editing {
  cursor: ns-resize;
}

.banner-frame.is-editing::after {
  /* Subtle frame to signal active edit mode */
  content: '';
  position: absolute;
  inset: 0;
  box-shadow: inset 0 0 0 2px rgba(var(--v-theme-primary), 0.9);
  pointer-events: none;
}

.banner-img {
  display: block;
  width: 100%;
  height: 100%;
  object-fit: cover; /* fixed crop mask */
  user-select: none;
  -webkit-user-drag: none;
}

.banner-edit-hint {
  position: absolute;
  left: 50%;
  bottom: 12px;
  transform: translateX(-50%);
  display: flex;
  align-items: center;
  gap: 4px;
  padding: 4px 12px;
  border-radius: 999px;
  background: rgba(0, 0, 0, 0.6);
  color: #fff;
  font-size: 12px;
  white-space: nowrap;
  pointer-events: auto;
}

.banner-reset {
  margin-left: 8px;
  padding: 1px 8px;
  border-radius: 999px;
  background: rgba(255, 255, 255, 0.2);
  color: #fff;
  font-size: 11px;
  cursor: pointer;
}

.banner-reset:hover {
  background: rgba(255, 255, 255, 0.35);
}

.banner-saving {
  position: absolute;
  top: 8px;
  right: 10px;
  display: flex;
  align-items: center;
  padding: 2px 10px;
  border-radius: 999px;
  background: rgba(0, 0, 0, 0.55);
  color: #fff;
  font-size: 11px;
}
</style>
