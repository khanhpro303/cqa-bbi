<template>
  <img
    v-if="source && !failed" :src="source" :alt="alt" :width="image?.width" :height="image?.height"
    class="knowledge-image-preview" @load="$emit('load')" @error="failed = true"
  />
  <div v-else class="knowledge-image-placeholder text-caption text-medium-emphasis pa-2" role="status">
    {{ t(failed ? 'kb_images_preview_failed' : 'kb_images_preview_loading') }}
  </div>
</template>

<script setup lang="ts">
import { ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import api from '../api'
import type { KnowledgeImage } from '../api/knowledgeImages'

const props = defineProps<{ alt: string; file?: File; base?: string; image?: KnowledgeImage }>()
defineEmits<{ load: [] }>()
const { t } = useI18n()
const source = ref('')
const failed = ref(false)

watch([() => props.file, () => props.base, () => props.image?.url], async ([file, base, url], _, onCleanup) => {
  source.value = ''
  failed.value = false
  let objectURL = ''
  const request = new AbortController()
  onCleanup(() => {
    request.abort()
    if (objectURL) URL.revokeObjectURL(objectURL)
  })
  try {
    if (file) {
      objectURL = URL.createObjectURL(file)
    } else {
      const image = props.image
      if (!base || !image) throw new Error('Missing protected image')
      const path = `${base}/${encodeURIComponent(image.document_id)}/images/${encodeURIComponent(image.id)}/content`
      if (url !== `/api/v1${path}`) throw new Error('Invalid protected image URL')
      const { data } = await api.get<Blob>(path, { responseType: 'blob', signal: request.signal })
      if (request.signal.aborted) return
      objectURL = URL.createObjectURL(data)
    }
    source.value = objectURL
  } catch {
    if (!request.signal.aborted) failed.value = true
  }
}, { immediate: true })
</script>

<style scoped>
.knowledge-image-preview {
  display: block;
  max-inline-size: 100%;
  max-block-size: 22rem;
  inline-size: auto;
  block-size: auto;
  object-fit: contain;
  border-radius: 0.5rem;
}
.knowledge-image-placeholder {
  display: grid;
  min-block-size: 4rem;
  place-items: center;
  text-align: center;
}
</style>
