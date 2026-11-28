<template>
  <v-chip
    v-if="localPath"
    tag="button"
    type="button"
    size="x-small"
    variant="tonal"
    class="mr-1"
    :disabled="downloading"
    @click="download"
  >
    <v-icon start size="12">mdi-paperclip</v-icon>
    {{ fileName }}
  </v-chip>
  <v-chip v-else size="x-small" variant="tonal" class="mr-1" :href="safeExternalUrl" :target="safeExternalUrl ? '_blank' : undefined" rel="noopener noreferrer">
    <v-icon start size="12">mdi-paperclip</v-icon>
    {{ fileName }}
  </v-chip>
  <span v-if="downloadFailed" role="status" class="text-caption ml-1">
    {{ $t('attachment_download_failed') }}
  </span>
</template>

<script setup lang="ts">
import { computed, ref } from 'vue'
import api from '../api'

const props = defineProps<{ fileName: string; localPath?: string; externalUrl?: string }>()
const safeExternalUrl = computed(() => {
  const value = props.externalUrl?.trim()
  if (!value || !/^https?:\/\//i.test(value)) return undefined
  try {
    const parsed = new URL(value)
    return parsed.username || parsed.password ? undefined : parsed.href
  } catch {
    return undefined
  }
})
const downloading = ref(false)
const downloadFailed = ref(false)

async function download() {
  if (!props.localPath || downloading.value) return
  const segments = props.localPath.split('/')
  if (segments.some(part => !part || part === '.' || part === '..')) {
    downloadFailed.value = true
    return
  }

  downloading.value = true
  downloadFailed.value = false
  const fileName = props.fileName
  try {
    const path = segments.map(part => encodeURIComponent(part)).join('/')
    const { data } = await api.get<Blob>('/files/' + path, { responseType: 'blob' })
    const url = URL.createObjectURL(data)
    try {
      const link = document.createElement('a')
      link.href = url
      link.download = fileName
      link.click()
    } finally {
      window.setTimeout(() => URL.revokeObjectURL(url), 0)
    }
  } catch {
    downloadFailed.value = true
  } finally {
    downloading.value = false
  }
}
</script>
