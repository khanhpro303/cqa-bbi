<template>
  <v-dialog :model-value="modelValue" max-width="1080" scrollable :persistent="uploading" aria-labelledby="product-link-import-title" @update:model-value="emit('update:modelValue', $event)">
    <v-card>
      <v-card-title class="d-flex align-center ga-2">
        <v-icon icon="mdi-file-import-outline" color="primary" />
        <h2 id="product-link-import-title" class="text-h6">{{ t('product_advisory_import_title') }}</h2>
        <v-spacer />
        <v-btn icon="mdi-close" size="small" variant="text" :disabled="uploading" :aria-label="t('cancel')" @click="close" />
      </v-card-title>
      <v-divider />

      <v-card-text>
        <v-alert v-if="firecrawlEnabled === false" type="warning" variant="tonal" density="compact" class="mb-4">
          {{ t('product_advisory_import_firecrawl_required') }}
          <router-link :to="`/${tenantId}/ai-engines`">{{ t('nav_ai_engines') }}</router-link>
        </v-alert>

        <v-row>
          <v-col cols="12" md="5">
            <v-sheet border rounded="lg" class="pa-4 h-100">
              <div class="d-flex align-center ga-2 mb-2">
                <h3 class="text-subtitle-1 font-weight-bold">{{ t('product_advisory_import_template_step') }}</h3>
              </div>
              <p class="text-body-2 text-medium-emphasis mb-3">
                {{ t('product_advisory_import_template_hint') }}
              </p>
              <v-alert type="info" variant="tonal" density="compact" class="mb-3">
                {{ t('product_advisory_import_existing_links_hint') }}
              </v-alert>
              <v-btn
                variant="outlined"
                color="primary"
                prepend-icon="mdi-download-outline"
                :loading="downloading"
                :disabled="uploading"
                data-testid="product-import-template"
                @click="downloadTemplate"
              >
                {{ t('product_advisory_import_download_template') }}
              </v-btn>
            </v-sheet>
          </v-col>
          <v-col cols="12" md="7">
            <v-sheet border rounded="lg" class="pa-4 h-100">
              <div class="d-flex align-center ga-2 mb-2">
                <h3 class="text-subtitle-1 font-weight-bold">{{ t('product_advisory_import_upload_step') }}</h3>
              </div>
              <p class="text-body-2 text-medium-emphasis mb-3">{{ t('product_advisory_import_upload_hint') }}</p>
              <v-file-input
                v-model="selectedFile"
                accept=".xlsx,.csv"
                :label="t('product_advisory_import_choose_file')"
                show-size
                clearable
                variant="outlined"
                density="comfortable"
                prepend-icon=""
                prepend-inner-icon="mdi-paperclip"
                :disabled="uploading || job?.status === 'running' || firecrawlEnabled === false"
                :error-messages="fileError"
                data-testid="product-import-file"
              />
              <div class="d-flex justify-end">
                <v-btn
                  color="primary"
                  variant="flat"
                  prepend-icon="mdi-web-sync"
                  :loading="uploading"
                  :disabled="!selectedFile || job?.status === 'running' || firecrawlEnabled === false"
                  data-testid="product-import-submit"
                  @click="upload"
                >
                  {{ t('product_advisory_import_submit') }}
                </v-btn>
              </div>
            </v-sheet>
          </v-col>
        </v-row>

        <v-alert v-if="errorMessage" type="error" variant="tonal" density="compact" class="mt-4">
          {{ errorMessage }}
        </v-alert>

        <section v-if="job" class="mt-5" aria-live="polite">
          <div class="d-flex flex-wrap align-center ga-2 mb-3">
            <h3 class="text-subtitle-1 font-weight-bold mr-auto">{{ t('product_advisory_import_progress_step') }}</h3>
            <v-chip :color="job.status === 'running' ? 'primary' : job.status === 'failed' ? 'error' : 'success'" variant="tonal" size="small">
              {{ t(`product_advisory_import_job_${job.status}`) }}
            </v-chip>
            <v-btn v-if="job.status !== 'running' && job.image_count > 0" variant="tonal" color="primary" size="small" @click="emit('review')">
              {{ t('product_advisory_import_review_images') }}
            </v-btn>
          </div>
          <v-progress-linear
            :model-value="progress"
            :indeterminate="job.status === 'running' && job.total_rows === 0"
            height="8"
            rounded
            color="primary"
            class="mb-2"
          />
          <div class="text-caption text-medium-emphasis mb-3">
            {{ t('product_advisory_import_progress_count', { processed: processedCount, total: job.total_rows }) }}
          </div>
          <div class="d-flex flex-wrap ga-2 mb-4">
            <v-chip color="success" variant="tonal" size="small">{{ t('product_advisory_import_succeeded', { count: job.succeeded_count }) }}</v-chip>
            <v-chip color="primary" variant="tonal" size="small">{{ t('product_advisory_import_processing', { count: pendingCount }) }}</v-chip>
            <v-chip color="error" variant="tonal" size="small">{{ t('product_advisory_import_failed', { count: job.failed_count }) }}</v-chip>
            <v-chip color="warning" variant="tonal" size="small">{{ t('product_advisory_import_unmatched', { count: job.unmatched_count }) }}</v-chip>
            <v-chip variant="tonal" size="small">{{ t('product_advisory_import_skipped', { count: job.skipped_count }) }}</v-chip>
          </div>

          <template v-if="items.length">
            <div class="d-flex flex-column flex-sm-row align-sm-center ga-2 mb-3">
              <v-btn-toggle v-model="filter" mandatory density="compact" color="primary" variant="outlined">
                <v-btn value="all" size="small">{{ t('product_advisory_import_filter_all') }}</v-btn>
                <v-btn value="success" size="small">{{ t('product_advisory_import_filter_success') }}</v-btn>
                <v-btn value="issues" size="small">{{ t('product_advisory_import_filter_issues') }}</v-btn>
              </v-btn-toggle>
              <v-spacer />
              <span class="text-caption text-medium-emphasis">{{ job.file_name }}</span>
            </div>
            <div class="import-results-table">
              <v-table density="compact" hover>
                <thead>
                  <tr>
                    <th>{{ t('product_advisory_import_row') }}</th>
                    <th>{{ t('product_advisory_product') }}</th>
                    <th>{{ t('product_advisory_import_link') }}</th>
                    <th>{{ t('product_advisory_import_status') }}</th>
                  </tr>
                </thead>
                <tbody>
                  <tr v-for="item in pagedItems" :key="item.id">
                    <td>{{ item.row_number }}</td>
                    <td>
                      <div class="font-weight-medium">{{ item.web_name || item.input_web_name || '—' }}</div>
                      <code>{{ item.parent_code || item.input_parent_code || '—' }}</code>
                      <span v-if="item.product_category" class="text-caption text-medium-emphasis ml-2">{{ item.product_category }}</span>
                      <v-chip v-if="item.overwrite" size="x-small" color="warning" variant="tonal" class="ml-2">
                        {{ t('product_advisory_import_overwrite') }}
                      </v-chip>
                    </td>
                    <td class="import-link">
                      <a v-if="item.error_code !== 'invalid_url' && safeLink(item.source_url)" :href="safeLink(item.source_url)" target="_blank" rel="noopener noreferrer">{{ item.source_url }}</a>
                      <span v-else>{{ item.source_url || '—' }}</span>
                    </td>
                    <td>
                      <v-chip size="x-small" variant="tonal" :color="statusColor(item.status)">
                        {{ t(`product_advisory_import_item_${item.status}`) }}
                      </v-chip>
                      <div v-if="item.error_code || item.summary_error" class="text-caption text-medium-emphasis mt-1">
                        {{ t(`product_advisory_import_error_${item.error_code || item.summary_error}`) }}
                        <span v-if="(item.error_code === 'ambiguous_web_name' || item.error_code === 'ambiguous_parent_code') && item.error_message">({{ item.error_message }})</span>
                      </div>
                    </td>
                  </tr>
                </tbody>
              </v-table>
            </div>
            <v-pagination v-if="pageCount > 1" v-model="resultPage" :length="pageCount" :total-visible="5" density="compact" class="mt-3" />
          </template>
        </section>
      </v-card-text>

      <v-divider />
      <v-card-actions>
        <v-spacer />
        <v-btn variant="text" :disabled="uploading" @click="close">{{ t('product_advisory_import_close') }}</v-btn>
      </v-card-actions>
    </v-card>
  </v-dialog>
</template>

<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import api from '../api'
import type { ProductAdvisoryImportItem, ProductAdvisoryImportJob, ProductAdvisoryImportResponse } from './productAdvisoryImport'

const props = defineProps<{
  modelValue: boolean
  tenantId: string
  firecrawlEnabled: boolean | null
  job: ProductAdvisoryImportJob | null
  items: ProductAdvisoryImportItem[]
}>()
const emit = defineEmits<{
  (event: 'update:modelValue', value: boolean): void
  (event: 'started', response: ProductAdvisoryImportResponse): void
  (event: 'conflict'): void
  (event: 'review'): void
}>()
const { t } = useI18n()

const selectedFile = ref<File | null>(null)
const fileError = ref('')
const errorMessage = ref('')
const uploading = ref(false)
const downloading = ref(false)
const filter = ref<'all' | 'success' | 'issues'>('all')
const resultPage = ref(1)
const perPage = 25

const processedCount = computed(() => props.job
  ? props.job.succeeded_count + props.job.failed_count + props.job.unmatched_count + props.job.skipped_count
  : 0)
const pendingCount = computed(() => Math.max(0, (props.job?.total_rows || 0) - processedCount.value))
const progress = computed(() => props.job?.total_rows ? (processedCount.value / props.job.total_rows) * 100 : 0)
const filteredItems = computed(() => props.items.filter((item) => {
  if (filter.value === 'success') return item.status === 'succeeded'
  if (filter.value === 'issues') return ['failed', 'unmatched', 'skipped'].includes(item.status)
  return true
}))
const pageCount = computed(() => Math.ceil(filteredItems.value.length / perPage))
const pagedItems = computed(() => filteredItems.value.slice((resultPage.value - 1) * perPage, resultPage.value * perPage))

watch([filter, () => props.job?.id], () => { resultPage.value = 1 })
watch(pageCount, (count) => { resultPage.value = Math.min(resultPage.value, Math.max(1, count)) })
watch(() => props.tenantId, reset)

function reset() {
  selectedFile.value = null
  fileError.value = ''
  errorMessage.value = ''
  uploading.value = false
  downloading.value = false
  filter.value = 'all'
  resultPage.value = 1
}

function close() {
  emit('update:modelValue', false)
}

function safeLink(raw: string) {
  try {
    const url = new URL(raw)
    return ['http:', 'https:'].includes(url.protocol) ? url.toString() : undefined
  } catch {
    return undefined
  }
}

function statusColor(status: ProductAdvisoryImportItem['status']) {
  if (status === 'succeeded') return 'success'
  if (status === 'failed' || status === 'unmatched') return 'error'
  if (status === 'processing' || status === 'pending') return 'primary'
  return 'default'
}

function readError(error: unknown, fallback: string) {
  if (!error || typeof error !== 'object' || !('response' in error)) return fallback
  const response = (error as { response?: { data?: { error?: string; message?: string } } }).response
  const code = response?.data?.error
  return response?.data?.message || (code ? t(`product_advisory_import_error_${code}`) : fallback)
}

async function downloadTemplate() {
  downloading.value = true
  errorMessage.value = ''
  const tenant = props.tenantId
  try {
    const response = await api.get(`/tenants/${tenant}/product-advisory-imports/template`, { responseType: 'blob' })
    if (tenant !== props.tenantId) return
    const objectURL = URL.createObjectURL(new Blob([response.data]))
    const anchor = document.createElement('a')
    anchor.href = objectURL
    anchor.download = `product-links-template-${Date.now()}.xlsx`
    document.body.appendChild(anchor)
    anchor.click()
    anchor.remove()
    URL.revokeObjectURL(objectURL)
  } catch (error) {
    if (tenant === props.tenantId) errorMessage.value = readError(error, t('product_advisory_import_template_failed'))
  } finally {
    if (tenant === props.tenantId) downloading.value = false
  }
}

async function upload() {
  const file = selectedFile.value
  if (!file || uploading.value || props.firecrawlEnabled === false) return
  const extension = file.name.toLowerCase().split('.').pop()
  if (!['xlsx', 'csv'].includes(extension || '') || file.size > 10 * 1024 ** 2) {
    fileError.value = t('product_advisory_import_file_invalid')
    return
  }
  const tenant = props.tenantId
  const body = new FormData()
  body.append('file', file)
  uploading.value = true
  fileError.value = ''
  errorMessage.value = ''
  try {
    const { data } = await api.post<ProductAdvisoryImportResponse>(`/tenants/${tenant}/product-advisory-imports`, body, {
      headers: { 'Content-Type': 'multipart/form-data' },
    })
    if (tenant !== props.tenantId) return
    emit('started', { job: data.job, items: data.items || [] })
    selectedFile.value = null
  } catch (error) {
    if (tenant !== props.tenantId) return
    errorMessage.value = readError(error, t('product_advisory_import_upload_failed'))
    if ((error as { response?: { status?: number } })?.response?.status === 409) emit('conflict')
  } finally {
    if (tenant === props.tenantId) uploading.value = false
  }
}
</script>

<style scoped>
.import-results-table { overflow-x: auto; -webkit-overflow-scrolling: touch; }
.import-results-table table { min-width: 780px; }
.import-link { max-width: 320px; overflow-wrap: anywhere; }
code { overflow-wrap: anywhere; }
</style>
