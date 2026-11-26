<template>
  <section :aria-label="t('kb_images_title')" :aria-busy="!!pending">
    <h3 ref="heading" tabindex="-1" class="text-subtitle-1 font-weight-bold mb-3">{{ t('kb_images_title') }}</h3>
    <div v-if="canEdit" class="d-flex flex-wrap align-center ga-2 mb-3">
      <input ref="picker" type="file" :accept="accept" multiple hidden @change="chooseFiles" />
      <v-btn
        variant="outlined" size="small" prepend-icon="mdi-image-plus-outline" :disabled="locked || drafts.length >= capacity"
        @click="picker?.click()"
      >{{ t('kb_images_choose') }}</v-btn>
      <span class="text-caption text-medium-emphasis">{{ t('kb_images_limits') }}</span>
    </div>
    <v-alert v-if="error && !deleteTarget" type="error" variant="tonal" density="compact" class="mb-3" role="alert">{{ t(error) }}</v-alert>
    <ul v-if="drafts.length" class="image-grid">
      <li v-for="draft in drafts" :key="draft.key" class="image-card">
        <KnowledgeImagePreview :file="draft.file" :alt="draft.caption || draft.file.name" />
        <div class="image-file text-caption">{{ draft.file.name }}</div>
        <v-text-field
          :model-value="draft.caption" :label="t('kb_images_caption')" :aria-label="`${t('kb_images_caption')} ${draft.file.name}`" :disabled="locked"
          maxlength="500" density="compact" variant="outlined" hide-details
          @update:model-value="(caption) => changeDraftCaption(draft.key, caption)"
        />
        <v-btn variant="text" size="small" :disabled="locked" @click="removeDraft(draft.key)">{{ t('kb_images_remove_draft') }}</v-btn>
      </li>
    </ul>
    <v-btn
      v-if="endpoint && canEdit && drafts.length" variant="flat" size="small" color="primary" class="mt-3"
      :disabled="locked || drafts.length > capacity" :loading="pending === 'upload'" @click="upload"
    >{{ t('kb_images_upload') }}</v-btn>

    <p v-if="endpoint && !images.length && !drafts.length" class="text-body-2 text-medium-emphasis">{{ t('kb_images_empty') }}</p>
    <ul v-if="images.length" class="image-grid mt-3">
      <li v-for="image in images" :key="image.id" class="image-card">
        <KnowledgeImagePreview :base="base" :image="image" :alt="image.caption || image.file_name" />
        <div class="image-file text-caption">{{ image.file_name }}</div>
        <template v-if="canEdit">
          <v-switch
            :model-value="image.enabled" :disabled="locked" color="success" density="compact" hide-details
            :label="t('kb_images_enabled')" :aria-label="`${t('kb_images_enabled')} ${image.file_name}`"
            @update:model-value="(enabled) => patch(image, { enabled: !!enabled })"
          />
          <v-text-field
            :model-value="captionEdits[image.id] ?? image.caption" :label="t('kb_images_caption')"
            :aria-label="`${t('kb_images_caption')} ${image.file_name}`" :disabled="locked"
            maxlength="500" density="compact" variant="outlined" hide-details
            @update:model-value="(caption) => { captionEdits[image.id] = caption ?? '' }"
          />
          <div class="d-flex flex-wrap ga-1">
            <v-btn
              variant="text" size="small" color="primary" :loading="pending === `patch-${image.id}`"
              :disabled="locked || (captionEdits[image.id] ?? image.caption).trim() === image.caption"
              @click="patch(image, { caption: captionEdits[image.id]?.trim() ?? '' })"
            >{{ t('kb_images_save_caption') }}</v-btn>
            <v-btn
              variant="text" size="small" prepend-icon="mdi-auto-fix" :disabled="locked"
              :loading="pending === `describe-${image.id}`" @click="describe(image)"
            >{{ t('kb_images_describe') }}</v-btn>
            <v-btn
              variant="text" size="small" color="error" icon="mdi-delete-outline"
              :disabled="locked" :aria-label="`${t('delete')} ${image.file_name}`" @click="confirmDelete(image, $event)"
            />
          </div>
        </template>
        <template v-else>
          <p v-if="image.caption" class="text-body-2 image-file">{{ image.caption }}</p>
          <span class="text-caption text-medium-emphasis">{{ t(image.enabled ? 'kb_images_enabled' : 'kb_images_disabled') }}</span>
        </template>
      </li>
    </ul>

    <v-dialog
      :model-value="!!deleteTarget" :aria-label="t('kb_images_delete_title')" :persistent="!!pending" max-width="440px"
      @update:model-value="closeDelete" @after-leave="restoreDeleteFocus"
    >
      <v-card v-if="deleteTarget">
        <v-card-title>{{ t('kb_images_delete_title') }}</v-card-title>
        <v-card-text>
          {{ t('kb_images_delete_confirm', { name: deleteTarget.file_name }) }}
          <v-alert v-if="error" type="error" variant="tonal" density="compact" class="mt-3" role="alert">{{ t(error) }}</v-alert>
        </v-card-text>
        <v-card-actions>
          <v-spacer />
          <v-btn variant="text" :disabled="locked" @click="closeDelete(false)">{{ t('cancel') }}</v-btn>
          <v-btn color="error" variant="flat" :loading="pending === 'delete'" :disabled="locked" @click="removeImage(deleteTarget)">
            {{ t('delete') }}
          </v-btn>
        </v-card-actions>
      </v-card>
    </v-dialog>
  </section>
</template>

<script setup lang="ts">
import { isAxiosError } from 'axios'
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import api from '../api'
import { uploadKnowledgeImages, type KnowledgeImage, type KnowledgeImageDraft } from '../api/knowledgeImages'
import KnowledgeImagePreview from './KnowledgeImagePreview.vue'

const props = withDefaults(defineProps<{
  base?: string
  documentId?: string
  images?: KnowledgeImage[]
  canEdit: boolean
  disabled?: boolean
}>(), { images: () => [] })
const drafts = defineModel<KnowledgeImageDraft[]>('drafts', { required: true })
const emit = defineEmits<{ 'update:images': [images: KnowledgeImage[]]; busy: [pending: boolean] }>()
const { t } = useI18n()

const mediaTypes = ['image/jpeg', 'image/png', 'image/webp', 'image/gif']
const accept = [...mediaTypes, '.jpg', '.jpeg', '.png', '.webp', '.gif'].join(',')
const fileLimit = 5 * 1024 * 1024
const requestLimit = 12 * 1024 * 1024 - 32 * 1024 // Allow room for multipart headers and captions.
const capacity = computed(() => Math.min(8, 12 - props.images.length))
const endpoint = computed(() => props.base && props.documentId ? `${props.base}/${props.documentId}` : '')
const picker = ref<HTMLInputElement>()
const heading = ref<HTMLElement>()
const pending = ref('')
const locked = computed(() => props.disabled || !!pending.value)
const error = ref('')
const captionEdits = ref<Record<string, string>>({})
const deleteTarget = ref<KnowledgeImage>()
let deleteActivator: HTMLElement | undefined
let describeRequest: AbortController | undefined
let mounted = true
watch(pending, (value) => emit('busy', !!value && !value.startsWith('describe-')), { flush: 'sync' })
onBeforeUnmount(() => {
  mounted = false
  describeRequest?.abort()
  emit('busy', false)
})

function chooseFiles(event: Event) {
  const input = event.target as HTMLInputElement
  const files = Array.from(input.files ?? [])
  input.value = ''
  if (!files.length || !props.canEdit || locked.value) return
  error.value = ''
  if (files.some((file) => file.type ? !mediaTypes.includes(file.type) : !/\.(?:jpe?g|png|webp|gif)$/i.test(file.name))) {
    error.value = 'kb_images_invalid_type'
  } else if (files.some((file) => file.size === 0 || file.size > fileLimit)) {
    error.value = 'kb_images_invalid_size'
  } else if (drafts.value.length + files.length > capacity.value) {
    error.value = 'kb_images_count_limit'
  } else if (files.reduce((total, file) => total + file.size, 0) + drafts.value.reduce((total, draft) => total + draft.file.size, 0) > requestLimit) {
    error.value = 'kb_images_request_limit'
  } else {
    const nextKey = Math.max(0, ...drafts.value.map(({ key }) => key)) + 1
    drafts.value = [...drafts.value, ...files.map((file, index) => ({ key: nextKey + index, file, caption: '' }))]
  }
}

function changeDraftCaption(key: number, caption: string | null) {
  drafts.value = drafts.value.map((draft) => draft.key === key ? { ...draft, caption: caption ?? '' } : draft)
}

function removeDraft(key: number) {
  error.value = ''
  drafts.value = drafts.value.filter((draft) => draft.key !== key)
}

const requestErrors: Record<string, string> = {
  image_too_large: 'kb_images_invalid_size',
  request_too_large: 'kb_images_request_limit',
  invalid_image_count: 'kb_images_count_limit',
  too_many_images: 'kb_images_count_limit',
  unsupported_image_type: 'kb_images_invalid_type',
  invalid_image: 'kb_images_invalid_file',
  image_dimensions_too_large: 'kb_images_invalid_dimensions',
  image_caption_no_vision: 'kb_images_no_vision',
}

async function runChange(name: string, fallback: string, change: () => Promise<void>) {
  if (!endpoint.value || !props.canEdit || locked.value) return
  pending.value = name
  error.value = ''
  try {
    await change()
  } catch (cause) {
    if (!mounted) return
    const code = isAxiosError(cause) ? cause.response?.data?.error : undefined
    error.value = requestErrors[code] || fallback
  } finally {
    if (mounted) pending.value = ''
  }
}

function upload() {
  if (!drafts.value.length || drafts.value.length > capacity.value) return
  return runChange('upload', 'kb_images_upload_failed', async () => {
    const saved = await uploadKnowledgeImages(endpoint.value, drafts.value)
    if (!mounted) return
    emit('update:images', [...props.images, ...saved])
    drafts.value = []
  })
}

function patch(image: KnowledgeImage, body: Partial<Pick<KnowledgeImage, 'caption' | 'enabled'>>) {
  const action = body.caption !== undefined ? `patch-${image.id}` : `toggle-${image.id}`
  return runChange(action, 'kb_images_save_failed', async () => {
    const { data } = await api.patch<KnowledgeImage>(`${endpoint.value}/images/${image.id}`, body)
    if (!mounted) return
    emit('update:images', props.images.map((item) => item.id === image.id ? data : item))
    if (body.caption !== undefined) delete captionEdits.value[image.id]
  })
}

function describe(image: KnowledgeImage) {
  return runChange(`describe-${image.id}`, 'kb_images_describe_failed', async () => {
    const request = new AbortController()
    describeRequest = request
    try {
      const { data } = await api.post<{ caption: string }>(`${endpoint.value}/images/${image.id}/describe`, undefined, { signal: request.signal })
      if (!request.signal.aborted) captionEdits.value[image.id] = data.caption
    } finally {
      describeRequest = undefined
    }
  })
}

function confirmDelete(image: KnowledgeImage, event: MouseEvent) {
  error.value = ''
  deleteActivator = event.currentTarget instanceof HTMLElement ? event.currentTarget : undefined
  deleteTarget.value = image
}

function closeDelete(open: boolean) {
  if (open || pending.value) return
  error.value = ''
  deleteTarget.value = undefined
}

function restoreDeleteFocus() {
  const target = deleteActivator?.isConnected ? deleteActivator : heading.value
  target?.focus()
  deleteActivator = undefined
}

function removeImage(image: KnowledgeImage) {
  return runChange('delete', 'kb_images_delete_failed', async () => {
    await api.delete(`${endpoint.value}/images/${image.id}`)
    if (!mounted) return
    delete captionEdits.value[image.id]
    emit('update:images', props.images.filter((item) => item.id !== image.id))
    deleteTarget.value = undefined
  })
}
</script>

<style scoped>
.image-grid {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(min(100%, 15rem), 1fr));
  gap: 0.75rem;
  margin: 0;
  padding: 0;
  list-style: none;
}
.image-card {
  display: flex;
  flex-direction: column;
  gap: 0.5rem;
  min-inline-size: 0;
  padding: 0.75rem;
  border: 1px solid rgba(var(--v-border-color), var(--v-border-opacity));
  border-radius: 0.625rem;
}
.image-file { overflow-wrap: anywhere; }
.image-card :deep(.knowledge-image-preview) {
  inline-size: 100%;
  block-size: 8.5rem;
  background: rgba(var(--v-theme-on-surface), 0.04);
}
</style>
