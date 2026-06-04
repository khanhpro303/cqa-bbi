<template>
  <div>
    <!-- Message body -->
    <v-textarea
      v-model="text"
      :label="$t('campaign_message_content')"
      variant="outlined"
      rows="4"
      auto-grow
      counter
      :rules="[v => !!(v && v.trim()) || $t('required')]"
      hide-details="auto"
    />

    <!-- Link + character counter row -->
    <div class="d-flex align-center flex-wrap ga-2 mt-2">
      <v-btn
        size="small"
        variant="tonal"
        color="primary"
        prepend-icon="mdi-link-variant"
        @click="showLinkField = !showLinkField"
      >
        {{ $t('campaign_insert_link') }}
      </v-btn>
      <v-spacer />
      <span class="text-caption" :class="overLimit ? 'text-error font-weight-bold' : 'text-grey-darken-1'">
        {{ runeCount.toLocaleString() }} / {{ ZALO_MAX_TEXT_RUNES.toLocaleString() }}
      </span>
    </div>

    <v-expand-transition>
      <v-text-field
        v-if="showLinkField"
        v-model="link"
        label="https://..."
        variant="outlined"
        density="compact"
        prepend-inner-icon="mdi-web"
        class="mt-2"
        hide-details="auto"
        clearable
      />
    </v-expand-transition>

    <v-alert
      v-if="overLimit"
      type="warning"
      variant="tonal"
      density="compact"
      class="mt-2 text-body-2"
    >
      {{ $t('campaign_over_limit_warn') }}
    </v-alert>

    <!-- Reminder message (text-only) for follow-up sends -->
    <div class="mt-3">
      <v-btn
        size="small"
        variant="tonal"
        color="primary"
        prepend-icon="mdi-bell-ring-outline"
        @click="showReminderField = !showReminderField"
      >
        {{ $t('campaign_reminder_add') }}
      </v-btn>
    </div>

    <v-expand-transition>
      <div v-if="showReminderField" class="mt-2">
        <v-textarea
          v-model="reminderText"
          :label="$t('campaign_reminder_label')"
          variant="outlined"
          rows="3"
          auto-grow
          counter
          hide-details="auto"
        />
        <div class="d-flex align-center flex-wrap ga-2 mt-1">
          <span class="text-caption text-grey-darken-1">
            <v-icon size="12" class="mr-1">mdi-information-outline</v-icon>
            {{ $t('campaign_reminder_hint') }}
          </span>
          <v-spacer />
          <span
            class="text-caption"
            :class="reminderOverLimit ? 'text-error font-weight-bold' : 'text-grey-darken-1'"
          >
            {{ reminderRuneCount.toLocaleString() }} / {{ ZALO_MAX_TEXT_RUNES.toLocaleString() }}
          </span>
        </div>
      </div>
    </v-expand-transition>

    <!-- Selected images (thumbnails + reorder/remove) -->
    <div v-if="items.length" class="image-grid mt-3">
      <div v-for="(it, i) in items" :key="it.key" class="image-chip">
        <v-img :src="it.url" cover height="72" width="72" class="image-thumb">
          <template #placeholder>
            <div class="img-ph"><v-icon size="18" color="grey">mdi-image-outline</v-icon></div>
          </template>
        </v-img>
        <div class="chip-actions">
          <v-btn
            :title="$t('campaign_image_move_up')"
            icon="mdi-chevron-left"
            size="x-small"
            variant="text"
            :disabled="i === 0"
            @click="move(i, -1)"
          />
          <v-btn
            :title="$t('campaign_image_move_down')"
            icon="mdi-chevron-right"
            size="x-small"
            variant="text"
            :disabled="i === items.length - 1"
            @click="move(i, 1)"
          />
          <v-btn
            :title="$t('campaign_image_remove')"
            icon="mdi-close"
            size="x-small"
            variant="text"
            color="error"
            @click="removeAt(i)"
          />
        </div>
      </div>
    </div>

    <!-- Image dropzone (multi-file) -->
    <div
      class="image-dropzone mt-3"
      :class="{ 'image-dropzone--active': dragging }"
      @dragover.prevent="dragging = true"
      @dragleave.prevent="dragging = false"
      @drop.prevent="onDrop"
    >
      <v-icon size="28" color="grey-darken-1">mdi-tray-arrow-up</v-icon>
      <div class="text-body-2 mt-1">{{ $t('campaign_image_attach') }}</div>
      <v-btn
        size="small"
        variant="text"
        color="primary"
        class="mt-1"
        :disabled="items.length >= MAX_IMAGES"
        @click="fileInput?.click()"
      >
        {{ $t('campaign_image_choose') }}
      </v-btn>
      <input
        ref="fileInput"
        type="file"
        accept="image/jpeg,image/png,image/webp"
        multiple
        hidden
        @change="onPick"
      />
    </div>
    <div class="text-caption text-grey-darken-1 mt-1">
      <v-icon size="12" class="mr-1">mdi-information-outline</v-icon>
      {{ $t('campaign_image_backend_note') }}
    </div>
    <div v-if="error" class="text-caption text-error mt-1">{{ error }}</div>

    <!-- Zalo preview bubble -->
    <div class="text-caption text-grey-darken-1 mt-4 mb-1">{{ $t('campaign_preview') }}</div>
    <div class="zalo-preview">
      <div class="zalo-bubble">
        <v-img
          v-for="it in items"
          :key="it.key"
          :src="it.url"
          cover
          max-height="160"
          class="zalo-img"
        />
        <div class="zalo-text" v-text="previewText || $t('campaign_preview_empty')" />
        <a v-if="link" class="zalo-link" :href="link" target="_blank" rel="noopener">{{ link }}</a>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, computed, watch, onBeforeUnmount } from 'vue'
import { useI18n } from 'vue-i18n'
import { ZALO_MAX_TEXT_RUNES, type CampaignMessage } from './types'
import { uploadCampaignImages } from './campaignsApi'

// One image in the composer: either an already-uploaded server path or a local
// File pending upload. `url` is a previewable blob/object URL.
interface ImageItem {
  key: string
  path?: string
  file?: File
  url: string
}

const MAX_IMAGES = 5
const MAX_IMAGE_BYTES = 2 * 1024 * 1024
const ALLOWED_TYPES = ['image/jpeg', 'image/png', 'image/webp']

const model = defineModel<CampaignMessage>({ required: true })
const { t } = useI18n()

const fileInput = ref<HTMLInputElement | null>(null)
const showLinkField = ref<boolean>(!!model.value.link)
const showReminderField = ref<boolean>(!!model.value.reminderText)
const dragging = ref(false)
const items = ref<ImageItem[]>([])
const error = ref('')

// Two-way proxies so we mutate the model immutably via spread.
const text = computed({
  get: () => model.value.text,
  set: (v) => { model.value = { ...model.value, text: v ?? '' } },
})
const link = computed({
  get: () => model.value.link ?? '',
  set: (v) => { model.value = { ...model.value, link: v || undefined } },
})
const reminderText = computed({
  get: () => model.value.reminderText ?? '',
  set: (v) => { model.value = { ...model.value, reminderText: v || undefined } },
})

// Rune-accurate count (matches backend chunking which counts runes, not bytes).
const runeCount = computed(() => [...(text.value || '')].length)
const overLimit = computed(() => runeCount.value > ZALO_MAX_TEXT_RUNES)
const reminderRuneCount = computed(() => [...(reminderText.value || '')].length)
const reminderOverLimit = computed(() => reminderRuneCount.value > ZALO_MAX_TEXT_RUNES)
const previewText = computed(() => text.value?.trim() || '')

// Re-init the thumbnail list whenever the model's saved image paths change
// (e.g. the dialog loads a different campaign). Text/link edits keep the paths
// identical so this never clobbers pending (not-yet-uploaded) picks.
watch(
  () => model.value.images,
  (imgs) => {
    const incoming = (imgs ?? []).join('|')
    const current = items.value.filter((i) => i.path).map((i) => i.path).join('|')
    if (incoming !== current) initItems(imgs ?? [])
  },
  { immediate: true },
)

function initItems(paths: string[]) {
  revokeLocalUrls()
  items.value = paths.map((p) => ({ key: p, path: p, url: '' }))
  for (const it of items.value) void loadAuthPreview(it)
}

// Stored images live behind the JWT-guarded /api/v1/files route, so fetch them
// with the bearer token and turn the blob into a previewable object URL.
async function loadAuthPreview(item: ImageItem): Promise<void> {
  if (!item.path) return
  try {
    const token = localStorage.getItem('cqa_access_token')
    const resp = await fetch(`/api/v1/files/${item.path}`, {
      headers: token ? { Authorization: `Bearer ${token}` } : {},
    })
    if (resp.ok) item.url = URL.createObjectURL(await resp.blob())
  } catch {
    // Leave url empty → the v-img placeholder icon shows.
  }
}

function addFiles(files: File[]): void {
  error.value = ''
  for (const file of files) {
    if (items.value.length >= MAX_IMAGES) {
      error.value = t('campaign_image_too_many')
      break
    }
    if (!ALLOWED_TYPES.includes(file.type)) {
      error.value = t('campaign_image_bad_type', { name: file.name })
      continue
    }
    if (file.size > MAX_IMAGE_BYTES) {
      error.value = t('campaign_image_too_large', { name: file.name })
      continue
    }
    items.value = [
      ...items.value,
      { key: `${file.name}-${Date.now()}-${Math.random()}`, file, url: URL.createObjectURL(file) },
    ]
  }
}

function onPick(e: Event): void {
  const target = e.target as HTMLInputElement
  if (target.files) addFiles([...target.files])
  target.value = '' // allow re-picking the same file
}

function onDrop(e: DragEvent): void {
  dragging.value = false
  const files = [...(e.dataTransfer?.files ?? [])].filter((f) => f.type.startsWith('image/'))
  if (files.length) addFiles(files)
}

function removeAt(i: number): void {
  const it = items.value[i]
  if (it.url.startsWith('blob:')) URL.revokeObjectURL(it.url)
  items.value = items.value.filter((_, idx) => idx !== i)
}

function move(i: number, dir: -1 | 1): void {
  const j = i + dir
  if (j < 0 || j >= items.value.length) return
  const next = [...items.value]
  ;[next[i], next[j]] = [next[j], next[i]]
  items.value = next
}

// commit uploads any pending files, then writes the ordered server paths back
// onto the model. The dialog awaits this before saving the campaign.
async function commit(): Promise<void> {
  const pending = items.value.filter((i) => i.file && !i.path)
  if (pending.length > 0) {
    const paths = await uploadCampaignImages(pending.map((i) => i.file as File))
    pending.forEach((item, idx) => {
      item.path = paths[idx]
      item.file = undefined
    })
  }
  const paths = items.value.filter((i) => i.path).map((i) => i.path as string)
  model.value = { ...model.value, images: paths }
}

function revokeLocalUrls(): void {
  for (const it of items.value) {
    if (it.url.startsWith('blob:')) URL.revokeObjectURL(it.url)
  }
}

onBeforeUnmount(revokeLocalUrls)

defineExpose({ commit })
</script>

<style scoped>
.image-grid {
  display: flex;
  flex-wrap: wrap;
  gap: 10px;
}
.image-chip {
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 2px;
}
.image-thumb {
  border-radius: 8px;
  border: 1px solid rgba(0, 0, 0, 0.12);
  background: #f3f4f6;
}
.img-ph {
  display: flex;
  align-items: center;
  justify-content: center;
  width: 100%;
  height: 100%;
}
.chip-actions {
  display: flex;
  align-items: center;
}
.image-dropzone {
  border: 1.5px dashed rgba(0, 0, 0, 0.22);
  border-radius: 8px;
  padding: 16px;
  text-align: center;
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  min-height: 96px;
  transition: border-color var(--duration-fast, 150ms) ease, background-color 150ms ease;
}
.image-dropzone--active {
  border-color: rgb(var(--v-theme-primary));
  background-color: rgba(var(--v-theme-primary), 0.04);
}
.zalo-preview {
  background: #e9eef5;
  border-radius: 10px;
  padding: 12px;
}
.zalo-bubble {
  background: #fff;
  border-radius: 4px 14px 14px 14px;
  padding: 10px 12px;
  max-width: 320px;
  box-shadow: 0 1px 2px rgba(0, 0, 0, 0.12);
}
.zalo-img {
  border-radius: 6px;
  margin-bottom: 6px;
}
.zalo-text {
  white-space: pre-wrap;
  word-break: break-word;
  font-size: 0.875rem;
  line-height: 1.45;
}
.zalo-link {
  display: inline-block;
  margin-top: 6px;
  font-size: 0.8125rem;
  color: #1976d2;
  word-break: break-all;
}
</style>
