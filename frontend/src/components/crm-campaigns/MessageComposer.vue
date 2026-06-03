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

    <!-- Image dropzone -->
    <div
      class="image-dropzone mt-3"
      :class="{ 'image-dropzone--active': dragging }"
      @dragover.prevent="dragging = true"
      @dragleave.prevent="dragging = false"
      @drop.prevent="onDrop"
    >
      <template v-if="imageName">
        <v-icon color="success" class="mr-2">mdi-image-check-outline</v-icon>
        <span class="text-body-2 font-weight-medium">{{ imageName }}</span>
        <v-btn
          icon="mdi-close"
          size="x-small"
          variant="text"
          class="ml-2"
          @click="clearImage"
        />
      </template>
      <template v-else>
        <v-icon size="28" color="grey-darken-1">mdi-tray-arrow-up</v-icon>
        <div class="text-body-2 mt-1">{{ $t('campaign_image_attach') }}</div>
        <v-btn size="small" variant="text" color="primary" class="mt-1" @click="fileInput?.click()">
          {{ $t('campaign_image_choose') }}
        </v-btn>
        <input ref="fileInput" type="file" accept="image/*" hidden @change="onPick" />
      </template>
    </div>
    <div class="text-caption text-error mt-1">
      <v-icon size="12" class="mr-1">mdi-information-outline</v-icon>
      {{ $t('campaign_image_backend_note') }}
    </div>

    <!-- Zalo preview bubble -->
    <div class="text-caption text-grey-darken-1 mt-4 mb-1">{{ $t('campaign_preview') }}</div>
    <div class="zalo-preview">
      <div class="zalo-bubble">
        <div v-if="imageName" class="zalo-img-placeholder">
          <v-icon size="20" color="grey">mdi-image-outline</v-icon>
          <span class="text-caption ml-1">{{ imageName }}</span>
        </div>
        <div class="zalo-text" v-text="previewText || $t('campaign_preview_empty')" />
        <a v-if="link" class="zalo-link" :href="link" target="_blank" rel="noopener">{{ link }}</a>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, computed } from 'vue'
import { ZALO_MAX_TEXT_RUNES, type CampaignMessage } from './types'

const model = defineModel<CampaignMessage>({ required: true })

const fileInput = ref<HTMLInputElement | null>(null)
const showLinkField = ref<boolean>(!!model.value.link)
const dragging = ref(false)

// Two-way proxies so we mutate the model immutably via spread.
const text = computed({
  get: () => model.value.text,
  set: (v) => { model.value = { ...model.value, text: v ?? '' } },
})
const link = computed({
  get: () => model.value.link ?? '',
  set: (v) => { model.value = { ...model.value, link: v || undefined } },
})
const imageName = computed(() => model.value.imageName)

// Rune-accurate count (matches backend chunking which counts runes, not bytes).
const runeCount = computed(() => [...(text.value || '')].length)
const overLimit = computed(() => runeCount.value > ZALO_MAX_TEXT_RUNES)

const previewText = computed(() => text.value?.trim() || '')

function setImage(file: File | null | undefined) {
  model.value = { ...model.value, imageName: file ? file.name : undefined }
}

function onPick(e: Event) {
  const target = e.target as HTMLInputElement
  setImage(target.files?.[0])
}

function onDrop(e: DragEvent) {
  dragging.value = false
  const file = e.dataTransfer?.files?.[0]
  if (file && file.type.startsWith('image/')) setImage(file)
}

function clearImage() {
  setImage(null)
  if (fileInput.value) fileInput.value.value = ''
}
</script>

<style scoped>
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
.zalo-img-placeholder {
  display: flex;
  align-items: center;
  background: #f3f4f6;
  border-radius: 6px;
  padding: 6px 8px;
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
