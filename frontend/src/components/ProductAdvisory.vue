<template>
  <section aria-labelledby="product-advisory-heading">
    <div class="d-flex flex-wrap align-start justify-space-between ga-3 mb-4">
      <div>
        <h2 id="product-advisory-heading" class="text-h6 font-weight-bold">{{ $t('product_advisory_title') }}</h2>
        <p class="text-body-2 text-medium-emphasis mb-0">{{ $t('product_advisory_desc') }}</p>
      </div>
      <div class="d-flex flex-wrap align-center ga-2">
        <v-btn
          v-if="canEdit"
          color="primary"
          variant="outlined"
          prepend-icon="mdi-file-import-outline"
          data-testid="product-import-open"
          @click="openImportDialog"
        >
          {{ $t('product_advisory_import_open') }}
        </v-btn>
        <v-chip color="primary" variant="tonal" prepend-icon="mdi-package-variant-closed">
          {{ $t('product_advisory_groups', { count: total }) }}
        </v-chip>
      </div>
    </div>

    <v-alert
      v-if="latestImportJob?.status === 'running'"
      type="info"
      variant="tonal"
      density="compact"
      class="mb-4"
      data-testid="product-import-running"
    >
      <div class="d-flex flex-wrap align-center ga-2">
        <div class="flex-grow-1">
          {{ $t('product_advisory_import_running_banner', { processed: importProcessedCount, total: latestImportJob.total_rows }) }}
          <v-progress-linear :model-value="importProgress" height="5" rounded color="primary" class="mt-2" />
        </div>
        <v-btn variant="text" size="small" @click="openImportDialog">{{ $t('product_advisory_import_view_progress') }}</v-btn>
      </div>
    </v-alert>
    <v-alert
      v-else-if="canEdit && latestImportJob && latestImportJob.image_count > 0"
      :type="latestImportJob.pending_image_count > 0 ? 'warning' : 'success'"
      variant="tonal"
      density="compact"
      class="mb-4"
      data-testid="product-import-review-banner"
    >
      <div class="d-flex flex-wrap align-center ga-2">
        <span class="flex-grow-1">
          {{ $t(latestImportJob.pending_image_count > 0 ? 'product_advisory_import_review_pending_banner' : 'product_advisory_import_review_done_banner', { count: latestImportJob.pending_image_count || latestImportJob.image_count }) }}
        </span>
        <v-btn variant="text" size="small" @click="openImportReview">
          {{ $t(latestImportJob.pending_image_count > 0 ? 'product_advisory_import_review_images' : 'product_advisory_import_review_again') }}
        </v-btn>
      </div>
    </v-alert>

    <div class="d-flex flex-column flex-sm-row ga-3 mb-4">
      <v-text-field
        v-model="query"
        :label="$t('product_advisory_search')"
        prepend-inner-icon="mdi-magnify"
        density="compact"
        clearable
        hide-details
        class="flex-grow-1"
        data-testid="product-search"
        @update:model-value="debouncedLoad"
      />
      <v-select
        v-model="configured"
        :items="configuredOptions"
        :label="$t('product_advisory_filter')"
        density="compact"
        hide-details
        class="configured-filter"
        @update:model-value="loadProducts(1)"
      />
    </div>

    <v-alert v-if="loadError" type="error" variant="tonal" density="compact" class="mb-4">
      {{ $t('product_advisory_load_failed') }}
      <v-btn variant="text" size="small" @click="loadProducts(page)">{{ $t('retry') }}</v-btn>
    </v-alert>

    <v-card variant="outlined">
      <div class="product-table">
        <v-table density="comfortable" hover>
          <thead>
            <tr>
              <th>{{ $t('product_advisory_product') }}</th>
              <th>{{ $t('product_advisory_parent_code') }}</th>
              <th class="text-center">{{ $t('product_advisory_variants') }}</th>
              <th>{{ $t('product_advisory_context') }}</th>
              <th class="text-right" />
            </tr>
          </thead>
          <tbody>
            <tr v-for="product in products" :key="productIdentity(product)">
              <td class="py-2">
                <div class="d-flex align-center ga-3">
                  <v-avatar rounded="lg" size="48" color="surface-variant">
                    <v-img v-if="displayURL(product.thumbnail_url || product.default_image_url)" :src="displayURL(product.thumbnail_url || product.default_image_url)" cover :alt="product.web_name" />
                    <v-icon v-else icon="mdi-image-outline" />
                  </v-avatar>
                  <div>
                    <div class="font-weight-medium">{{ product.web_name || product.parent_code }}</div>
                    <div v-if="product.brand || product.product_category" class="text-caption text-medium-emphasis">{{ [product.brand, product.product_category].filter(Boolean).join(' · ') }}</div>
                  </div>
                </div>
              </td>
              <td><code>{{ product.parent_code }}</code></td>
              <td class="text-center">{{ product.variant_count }}</td>
              <td>
                <v-chip v-if="product.catalog_missing" color="warning" size="small" variant="tonal">{{ $t('product_advisory_catalog_missing') }}</v-chip>
                <v-chip v-else-if="product.has_content" color="success" size="small" variant="tonal">{{ $t('product_advisory_ready') }}</v-chip>
                <v-chip v-else color="warning" size="small" variant="tonal">{{ $t('product_advisory_missing') }}</v-chip>
              </td>
              <td class="text-right">
                <v-btn size="small" variant="text" prepend-icon="mdi-pencil-outline" @click="openProduct(product)">
                  {{ $t('edit') }}
                </v-btn>
              </td>
            </tr>
            <tr v-if="!loading && !loadError && products.length === 0">
              <td colspan="5" class="text-center text-medium-emphasis py-8">{{ $t('product_advisory_empty') }}</td>
            </tr>
          </tbody>
        </v-table>
      </div>
      <v-progress-linear v-if="loading" indeterminate color="primary" />
      <v-divider v-if="totalPages > 1" />
      <v-card-actions v-if="totalPages > 1" class="justify-center">
        <v-pagination v-model="page" :length="totalPages" :total-visible="5" density="compact" @update:model-value="loadProducts" />
      </v-card-actions>
    </v-card>

    <v-dialog v-model="dialog" max-width="960px" scrollable aria-labelledby="product-advisory-dialog-title" @after-leave="resetEditor">
      <v-card v-if="selected">
        <v-card-title class="editor-header d-flex align-center ga-3">
          <v-avatar class="editor-thumbnail" rounded="lg" color="surface-variant" aria-hidden="true">
            <v-img v-if="editorThumbnail" :src="editorThumbnail" :cover="false">
              <template #placeholder>
                <div class="d-flex h-100 align-center justify-center">
                  <v-icon icon="mdi-image-outline" />
                </div>
              </template>
            </v-img>
            <v-icon v-else icon="mdi-image-outline" />
          </v-avatar>
          <div class="editor-identity flex-grow-1">
            <h3 id="product-advisory-dialog-title" class="editor-name">
              {{ selected.web_name || selected.parent_code }}
            </h3>
            <div class="editor-meta text-medium-emphasis">
              <template v-if="selected.web_name && selected.web_name !== selected.parent_code">{{ selected.parent_code }} · </template>
              <template v-if="selected.product_category">{{ selected.product_category }} · </template>{{ $t('product_advisory_variant_count', { count: selected.variant_count }) }}
            </div>
          </div>
          <v-btn icon="mdi-close" variant="text" size="small" :aria-label="$t('cancel')" @click="dialog = false" />
        </v-card-title>
        <v-divider />
        <v-card-text>
          <v-alert v-if="detailError" type="error" variant="tonal" density="compact" class="mb-4">{{ $t('product_advisory_detail_failed') }}</v-alert>
          <template v-if="detail">
            <v-alert v-if="detail.catalog_missing" type="warning" variant="tonal" density="compact" class="mb-4">{{ $t('product_advisory_catalog_missing_hint') }}</v-alert>
            <v-textarea
              v-model="form.notes"
              :label="$t('product_advisory_notes')"
              :hint="$t('product_advisory_notes_hint')"
              persistent-hint
              rows="7"
              auto-grow
              maxlength="30000"
              variant="outlined"
              data-testid="product-notes"
              :readonly="!canEditSelected"
            />

            <div class="text-subtitle-2 font-weight-bold mt-6 mb-2">{{ $t('product_advisory_web_source') }}</div>
            <div class="d-flex flex-column flex-sm-row ga-2 align-sm-start">
              <v-text-field
                v-model="form.source_url"
                :label="$t('product_advisory_source_url')"
                :hint="$t('kb_link_public_hint')"
                persistent-hint
                type="url"
                :disabled="scraping || saving"
                maxlength="2048"
                variant="outlined"
                class="flex-grow-1"
                data-testid="product-source-url"
                :readonly="!canEditSelected"
              />
              <v-btn
                color="primary"
                variant="tonal"
                prepend-icon="mdi-web-sync"
                class="mt-sm-1"
                :loading="scraping"
                :disabled="!canEditSelected || !validSourceURL || saving || !!activeScrape"
                data-testid="product-scrape"
                @click="scrape"
              >
                {{ detail.source_url ? $t('product_advisory_rescrape') : $t('product_advisory_scrape') }}
              </v-btn>
            </div>
            <v-alert v-if="sourceError" type="error" variant="tonal" density="compact" class="mt-2">{{ $t(sourceError) }}</v-alert>
            <v-sheet v-if="scraping || detail.source_summary || detail.scraped_content || detail.source_title || detail.summary_error" border rounded="lg" class="pa-4 mt-3">
              <h4 class="text-subtitle-2 font-weight-bold">{{ $t('kb_summary_title') }}</h4>
              <div v-if="detail.source_title && !scraping" class="text-caption text-medium-emphasis mt-1">{{ detail.source_title }}</div>
              <div aria-live="polite">
                <p v-if="scraping" class="text-body-2 text-medium-emphasis mt-2">{{ $t('product_advisory_summary_reading') }}</p>
                <p v-else-if="detail.source_summary" class="text-body-2 source-summary mt-2">{{ detail.source_summary }}</p>
                <p v-else class="text-body-2 text-medium-emphasis mt-2">{{ $t(summaryMessage) }}</p>
              </div>
            </v-sheet>

            <div class="d-flex flex-wrap align-center justify-space-between ga-2 mt-6 mb-3">
              <div>
                <div class="text-subtitle-2 font-weight-bold">{{ $t('product_advisory_images') }}</div>
                <div class="text-caption text-medium-emphasis">{{ $t('product_advisory_images_hint') }}</div>
              </div>
              <template v-if="canEditSelected">
                <input ref="imageInput" type="file" accept="image/jpeg,image/png,image/webp,image/gif" multiple hidden @change="uploadImages" />
                <v-btn
                  color="primary"
                  variant="outlined"
                  prepend-icon="mdi-image-plus-outline"
                  :loading="uploadingImages"
                  :disabled="uploadingImages"
                  @click="imageInput?.click()"
                >
                  {{ $t('product_advisory_add_images') }}
                </v-btn>
              </template>
            </div>

            <div v-if="detail.images.length" class="product-images">
              <v-card v-for="image in detail.images" :key="image.id" variant="outlined" class="image-card">
                <v-img :src="displayURL(image.url)" :alt="image.caption || selected.web_name" height="140" cover />
                <div class="d-flex flex-wrap align-center ga-1 px-2 pt-2">
                  <v-chip size="x-small" variant="tonal">
                    {{ image.source === 'scraped' ? $t('product_advisory_scraped') : $t('product_advisory_manual') }}
                  </v-chip>
                  <v-chip v-if="!image.enabled" size="x-small" color="warning" variant="tonal">
                    <span class="image-review-label">{{ $t('product_advisory_pending_review') }}</span>
                  </v-chip>
                </div>
                <div class="d-flex align-center justify-space-between px-2">
                  <v-checkbox-btn
                    :model-value="image.enabled"
                    color="primary"
                    :aria-label="$t('product_advisory_use_image')"
                    :disabled="!canEditSelected || updatingImage === image.id"
                    @update:model-value="(enabled) => setImageEnabled(image, !!enabled)"
                  />
                  <v-btn v-if="canEditSelected" icon="mdi-delete-outline" size="x-small" variant="text" color="error" :aria-label="$t('delete')" @click="removeImage(image)" />
                </div>
                <v-text-field
                  v-model="image.caption"
                  :label="$t('product_advisory_image_caption')"
                  density="compact"
                  variant="underlined"
                  maxlength="500"
                  hide-details
                  class="px-2 pb-2"
                  :disabled="!canEditSelected || updatingImage === image.id"
                  @change="updateImageCaption(image)"
                />
              </v-card>
            </div>
            <v-sheet v-else color="surface-variant" rounded class="text-center text-medium-emphasis pa-6">
              <v-icon icon="mdi-image-off-outline" class="mb-2" />
              <div>{{ $t('product_advisory_no_images') }}</div>
            </v-sheet>
          </template>
          <div v-else-if="loadingDetail" class="d-flex justify-center pa-8"><v-progress-circular indeterminate color="primary" /></div>
        </v-card-text>
        <v-divider />
        <v-card-actions>
          <v-spacer />
          <v-btn variant="text" @click="dialog = false">{{ $t('cancel') }}</v-btn>
          <v-btn color="primary" variant="flat" :loading="saving" :disabled="!canEditSelected || !detail || !sourceURLAcceptable || scraping || saving" data-testid="product-save" @click="save">
            {{ $t('save') }}
          </v-btn>
        </v-card-actions>
      </v-card>
    </v-dialog>

    <ProductAdvisoryLinkImportModal
      v-model="importDialog"
      :tenant-id="tenantId"
      :firecrawl-enabled="firecrawlEnabled"
      :job="latestImportJob"
      :items="importItems"
      @started="handleImportStarted"
      @conflict="reloadLatestImport"
      @review="openImportReview"
    />
    <ProductAdvisoryImportImageReviewModal
      v-model="reviewDialog"
      :tenant-id="tenantId"
      :job-id="latestImportJob?.id || ''"
      :groups="reviewGroups"
      @updated="handleImportReviewUpdated"
    />

    <v-snackbar v-model="snack" :color="snackColor" timeout="4000">{{ snackText }}</v-snackbar>
  </section>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import api from '../api'
import ProductAdvisoryImportImageReviewModal from './ProductAdvisoryImportImageReviewModal.vue'
import ProductAdvisoryLinkImportModal from './ProductAdvisoryLinkImportModal.vue'
import {
  importReviewGroups,
  type ProductAdvisoryImportItem,
  type ProductAdvisoryImportJob,
  type ProductAdvisoryImportResponse,
} from './productAdvisoryImport'

type ProductGroup = {
  group_key?: string
  parent_code: string
  web_name: string
  product_category?: string
  catalog_missing?: boolean
  brand?: string
  variant_count: number
  has_content: boolean
  default_image_url?: string
  thumbnail_url?: string
  updated_at?: string
}

type ProductImage = {
  id: string
  url: string
  source: 'scraped' | 'upload'
  enabled: boolean
  sort_order: number
  caption: string
}

type ProductDetail = ProductGroup & {
  notes: string
  source_url: string
  source_title: string
  scraped_content: string
  source_summary?: string
  summary_error?: string
  images: ProductImage[]
}

const props = defineProps<{ tenantId: string; canEdit: boolean }>()
const { t } = useI18n()
const base = computed(() => `/tenants/${props.tenantId}/product-advisory`)
const products = ref<ProductGroup[]>([])
const total = ref(0)
const page = ref(1)
const perPage = 20
const totalPages = computed(() => Math.ceil(total.value / perPage))
const query = ref('')
const configured = ref<'all' | 'yes' | 'no'>('all')
const configuredOptions = computed(() => [
  { title: t('product_advisory_filter_all'), value: 'all' },
  { title: t('product_advisory_filter_ready'), value: 'yes' },
  { title: t('product_advisory_filter_missing'), value: 'no' },
])
const loading = ref(false)
const loadError = ref(false)
let listRequest = 0
let searchTimer: ReturnType<typeof setTimeout> | undefined

function productIdentity(product: ProductGroup) {
  return product.group_key || `${product.parent_code}\u0000${product.web_name}`
}

function productRouteKey(product: ProductGroup) {
  return product.group_key || product.parent_code
}

const importDialog = ref(false)
const reviewDialog = ref(false)
const firecrawlEnabled = ref<boolean | null>(null)
const latestImportJob = ref<ProductAdvisoryImportJob | null>(null)
const importItems = ref<ProductAdvisoryImportItem[]>([])
const importBase = computed(() => `/tenants/${props.tenantId}/product-advisory-imports`)
const importProcessedCount = computed(() => latestImportJob.value
  ? latestImportJob.value.succeeded_count + latestImportJob.value.failed_count + latestImportJob.value.unmatched_count + latestImportJob.value.skipped_count
  : 0)
const importProgress = computed(() => latestImportJob.value?.total_rows
  ? (importProcessedCount.value / latestImportJob.value.total_rows) * 100
  : 0)
const reviewGroups = computed(() => importReviewGroups(importItems.value))
let importRequest = 0
let importPollTimer: ReturnType<typeof setTimeout> | undefined

function stopImportPolling() {
  clearTimeout(importPollTimer)
  importPollTimer = undefined
}

function scheduleImportPoll() {
  stopImportPolling()
  if (latestImportJob.value?.status === 'running') {
    importPollTimer = setTimeout(() => { void pollImportJob() }, 2000)
  }
}

function setImportResponse(response: ProductAdvisoryImportResponse) {
  latestImportJob.value = response.job
  importItems.value = response.items || []
  scheduleImportPoll()
}

async function reloadLatestImport() {
  if (!props.canEdit) return
  const request = importRequest
  try {
    const { data } = await api.get<ProductAdvisoryImportResponse>(`${importBase.value}/latest`, { params: { review_queue: 1 } })
    if (request === importRequest) setImportResponse({ job: data?.job || null, items: data?.items || [] })
  } catch {
    if (request === importRequest) scheduleImportPoll()
  }
}

async function openImportDialog() {
  importDialog.value = true
  const request = importRequest
  try {
    const { data } = await api.get<{ enabled: boolean }>(`/tenants/${props.tenantId}/knowledge/firecrawl`)
    if (request === importRequest) firecrawlEnabled.value = !!data?.enabled
  } catch {
    if (request === importRequest) firecrawlEnabled.value = null
  }
}

async function pollImportJob() {
  const active = latestImportJob.value
  if (!active || active.status !== 'running') return
  const request = importRequest
  const baseURL = importBase.value
  const summaryOnly = !importDialog.value
  try {
    const { data } = await api.get<ProductAdvisoryImportResponse>(`${baseURL}/${encodeURIComponent(active.id)}`, {
      params: summaryOnly ? { summary: 1 } : undefined,
    })
    if (request !== importRequest || baseURL !== importBase.value || latestImportJob.value?.id !== active.id) return
    const summaryJob = data?.job
    if (!summaryJob) {
      scheduleImportPoll()
      return
    }
    if (summaryJob.status === 'running') {
      setImportResponse({ job: summaryJob, items: summaryOnly ? importItems.value : data.items })
      return
    }
    const completed = summaryOnly
      ? (await api.get<ProductAdvisoryImportResponse>(`${baseURL}/${encodeURIComponent(active.id)}`)).data
      : data
    if (request !== importRequest || baseURL !== importBase.value || latestImportJob.value?.id !== active.id) return
    setImportResponse({ job: completed?.job || summaryJob, items: completed?.items || [] })
    void loadProducts(page.value)
    if (!dialog.value && latestImportJob.value?.pending_image_count) openImportReview()
  } catch {
    if (request === importRequest && baseURL === importBase.value) scheduleImportPoll()
  }
}

function handleImportStarted(response: ProductAdvisoryImportResponse) {
  ++importRequest
  setImportResponse(response)
  if (response.job?.status !== 'running') {
    void loadProducts(page.value)
    if (response.job?.pending_image_count) openImportReview()
  }
}

function openImportReview() {
  if (!props.canEdit || !latestImportJob.value?.id || reviewGroups.value.length === 0) return
  importDialog.value = false
  reviewDialog.value = true
}

async function handleImportReviewUpdated() {
  const jobID = latestImportJob.value?.id
  const baseURL = importBase.value
  if (!jobID) return
  const request = importRequest
  try {
    const { data } = await api.get<ProductAdvisoryImportResponse>(`${baseURL}/${encodeURIComponent(jobID)}`)
    if (request !== importRequest || baseURL !== importBase.value || latestImportJob.value?.id !== jobID) return
    setImportResponse({ job: data?.job || latestImportJob.value, items: data?.items || [] })
    if (!reviewDialog.value && latestImportJob.value?.pending_image_count === 0) void reloadLatestImport()
  } catch {
    // The review modal keeps its own error state. The banner refreshes on the next page load.
  }
  if (request === importRequest) void loadProducts(page.value)
}

function safeURL(raw?: string) {
  const value = raw?.trim()
  if (!value) return ''
  try {
    const parsed = new URL(value)
    return ['https:', 'http:'].includes(parsed.protocol) ? parsed.toString() : ''
  } catch {
    return ''
  }
}

const protectedImages = ref<Record<string, string>>({})
const pendingImages = new Set<string>()
let imageGeneration = 0
function displayURL(raw?: string) {
  return safeURL(raw) || (raw ? protectedImages.value[raw] || '' : '')
}

async function prepareImage(raw?: string) {
  const prefix = `/api/v1${base.value}/`
  if (!raw?.startsWith(prefix) || protectedImages.value[raw] || pendingImages.has(raw)) return
  const generation = imageGeneration
  pendingImages.add(raw)
  try {
    const { data } = await api.get(raw.slice('/api/v1'.length), { responseType: 'blob' })
    if (generation !== imageGeneration) return
    protectedImages.value = { ...protectedImages.value, [raw]: URL.createObjectURL(data) }
  } catch {
    // The card keeps its image placeholder when the protected image cannot be read.
  } finally {
    if (generation === imageGeneration) pendingImages.delete(raw)
  }
}

function prepareImages(urls: Array<string | undefined>) {
  urls.forEach((url) => { void prepareImage(url) })
}

function releaseProtectedImages() {
  ++imageGeneration
  pendingImages.clear()
  Object.values(protectedImages.value).forEach((url) => URL.revokeObjectURL(url))
  protectedImages.value = {}
}

async function loadProducts(targetPage = page.value) {
  const request = ++listRequest
  page.value = targetPage
  loading.value = true
  try {
    const { data } = await api.get(base.value, {
      params: { page: targetPage, per_page: perPage, q: (query.value || '').trim() || undefined, configured: configured.value },
    })
    if (request !== listRequest) return
    products.value = data?.items || []
    total.value = data?.total || 0
    prepareImages(products.value.map((product) => product.thumbnail_url || product.default_image_url))
    loadError.value = false
  } catch {
    if (request === listRequest) loadError.value = true
  } finally {
    if (request === listRequest) loading.value = false
  }
}

function debouncedLoad() {
  clearTimeout(searchTimer)
  searchTimer = setTimeout(() => loadProducts(1), 300)
}

const dialog = ref(false)
const selected = ref<ProductGroup | null>(null)
const canEditSelected = computed(() => props.canEdit && !!selected.value && !selected.value.catalog_missing)
const detail = ref<ProductDetail | null>(null)
const summaryMessage = computed(() => {
  switch (detail.value?.summary_error) {
    case 'summary_no_ai': return 'kb_summary_no_ai'
    case 'summary_no_text': return 'kb_summary_no_text'
    case 'summary_failed': return 'kb_summary_failed'
    default: return 'product_advisory_summary_missing'
  }
})
const loadingDetail = ref(false)
const detailError = ref(false)
const form = ref({ notes: '', source_url: '' })
const editorThumbnail = computed(() => {
  const thumbnail = detail.value
    ? detail.value.images.find((image) => image.enabled)?.url
    : selected.value?.thumbnail_url
  return displayURL(thumbnail) || displayURL(detail.value?.default_image_url || selected.value?.default_image_url)
})
let detailRequest = 0

async function openProduct(product: ProductGroup) {
  selected.value = product
  detail.value = null
  sourceError.value = ''
  detailError.value = false
  dialog.value = true
  const request = ++detailRequest
  const routeKey = productRouteKey(product)
  loadingDetail.value = true
  try {
    const { data } = await api.get(`${base.value}/${encodeURIComponent(routeKey)}`)
    if (request !== detailRequest || !dialog.value) return
    const loaded = { ...product, ...data, images: data?.images || [] } as ProductDetail
    detail.value = loaded
    form.value = { notes: loaded.notes || '', source_url: loaded.source_url || '' }
    prepareImages(loaded.images.map((image) => image.url))
  } catch {
    if (request === detailRequest) detailError.value = true
  } finally {
    if (request === detailRequest) loadingDetail.value = false
  }
}

function resetEditor() {
  ++detailRequest
  selected.value = null
  detail.value = null
  sourceError.value = ''
}

const snack = ref(false)
const snackText = ref('')
const snackColor = ref('success')
function notify(message: string, color = 'success') {
  snackText.value = message
  snackColor.value = color
  snack.value = true
}

const saving = ref(false)
async function save() {
  if (!selected.value || !detail.value || !props.canEdit || !sourceURLAcceptable.value || saving.value || scraping.value) return
  const requestBase = base.value
  const routeKey = productRouteKey(selected.value)
  saving.value = true
  try {
    const { data } = await api.put(`${requestBase}/${encodeURIComponent(routeKey)}`, {
      notes: form.value.notes.trim(),
      source_url: form.value.source_url.trim(),
    })
    if (base.value !== requestBase || !selected.value || productRouteKey(selected.value) !== routeKey || !dialog.value || !detail.value) return
    const updated: ProductDetail = { ...detail.value, ...data }
    detail.value = updated
    form.value.source_url = updated.source_url
    notify(t('product_advisory_saved'))
    await loadProducts(page.value)
  } catch {
    notify(t('product_advisory_save_failed'), 'error')
  } finally {
    saving.value = false
  }
}

const validSourceURL = computed(() => !!safeURL(form.value.source_url))
const sourceURLAcceptable = computed(() => !form.value.source_url.trim() || validSourceURL.value)
const activeScrape = ref<{ base: string; routeKey: string } | null>(null)
const scraping = computed(() => !!selected.value && activeScrape.value?.base === base.value && activeScrape.value?.routeKey === productRouteKey(selected.value))
const sourceError = ref('')
async function scrape() {
  if (!selected.value || !detail.value || !validSourceURL.value || !props.canEdit || saving.value || activeScrape.value) return
  const requestBase = base.value
  const routeKey = productRouteKey(selected.value)
  const request = detailRequest
  activeScrape.value = { base: requestBase, routeKey }
  sourceError.value = ''
  try {
    const { data } = await api.post(`${requestBase}/${encodeURIComponent(routeKey)}/scrape`, {
      url: form.value.source_url.trim(),
    })
    if (base.value !== requestBase || !selected.value || productRouteKey(selected.value) !== routeKey || !dialog.value || !detail.value) return
    const updated: ProductDetail = {
      ...detail.value,
      ...data,
      summary_error: data?.summary_error,
      images: data?.images || detail.value.images,
    }
    detail.value = updated
    form.value.source_url = updated.source_url || form.value.source_url
    prepareImages(updated.images.map((image) => image.url))
    notify(t('product_advisory_scraped_ok'))
    void loadProducts(page.value)
  } catch (error: any) {
    if (request !== detailRequest || !dialog.value) return
    const code = error?.response?.data?.error || error?.response?.data?.message
    sourceError.value = code === 'firecrawl_not_configured' ? 'product_advisory_firecrawl_missing' : 'product_advisory_scrape_failed'
  } finally {
    activeScrape.value = null
  }
}

const imageInput = ref<HTMLInputElement | null>(null)
const uploadingImages = ref(false)
async function uploadImages(event: Event) {
  const input = event.currentTarget as HTMLInputElement
  const files = Array.from(input.files ?? [])
  input.value = ''
  if (!files.length || !selected.value || !props.canEdit || uploadingImages.value) return
  if (files.length > 8 || files.some((file) => file.size > 5 * 1024 ** 2) || files.reduce((sum, file) => sum + file.size, 0) > 11 * 1024 ** 2) {
    notify(t('product_advisory_image_size'), 'error')
    return
  }
  const body = new FormData()
  files.forEach((file) => body.append('images', file))
  const requestBase = base.value
  const routeKey = productRouteKey(selected.value)
  uploadingImages.value = true
  try {
    const { data } = await api.post(`${requestBase}/${encodeURIComponent(routeKey)}/images`, body, {
      headers: { 'Content-Type': 'multipart/form-data' },
    })
    if (base.value !== requestBase || !selected.value || productRouteKey(selected.value) !== routeKey || !detail.value) return
    const added = data?.images || []
    detail.value!.images.push(...added)
    prepareImages(added.map((image: ProductImage) => image.url))
    notify(t('product_advisory_images_added'))
    await loadProducts(page.value)
  } catch {
    notify(t('product_advisory_image_failed'), 'error')
  } finally {
    uploadingImages.value = false
  }
}

const updatingImage = ref('')
async function setImageEnabled(image: ProductImage, enabled: boolean) {
  if (!selected.value || !props.canEdit) return
  const requestBase = base.value
  const routeKey = productRouteKey(selected.value)
  updatingImage.value = image.id
  try {
    await api.patch(`${requestBase}/${encodeURIComponent(routeKey)}/images/${encodeURIComponent(image.id)}`, { enabled })
    if (base.value !== requestBase || !selected.value || productRouteKey(selected.value) !== routeKey) return
    image.enabled = enabled
    await loadProducts(page.value)
  } catch {
    notify(t('product_advisory_image_failed'), 'error')
  } finally {
    updatingImage.value = ''
  }
}

async function updateImageCaption(image: ProductImage) {
  if (!selected.value || !props.canEdit) return
  const requestBase = base.value
  const routeKey = productRouteKey(selected.value)
  updatingImage.value = image.id
  try {
    await api.patch(`${requestBase}/${encodeURIComponent(routeKey)}/images/${encodeURIComponent(image.id)}`, {
      caption: image.caption.trim(),
    })
  } catch {
    notify(t('product_advisory_image_failed'), 'error')
  } finally {
    updatingImage.value = ''
  }
}

async function removeImage(image: ProductImage) {
  if (!selected.value || !props.canEdit) return
  const requestBase = base.value
  const routeKey = productRouteKey(selected.value)
  try {
    await api.delete(`${requestBase}/${encodeURIComponent(routeKey)}/images/${encodeURIComponent(image.id)}`)
    if (base.value !== requestBase || !selected.value || productRouteKey(selected.value) !== routeKey || !detail.value) return
    detail.value!.images = detail.value!.images.filter((item) => item.id !== image.id)
    await loadProducts(page.value)
  } catch {
    notify(t('product_advisory_image_failed'), 'error')
  }
}

watch(() => props.tenantId, () => {
  ++listRequest
  ++detailRequest
  releaseProtectedImages()
  dialog.value = false
  products.value = []
  total.value = 0
  page.value = 1
  query.value = ''
  void loadProducts(1)
})
onMounted(() => loadProducts(1))
watch(() => [props.tenantId, props.canEdit] as const, () => {
  ++importRequest
  stopImportPolling()
  importDialog.value = false
  reviewDialog.value = false
  firecrawlEnabled.value = null
  setImportResponse({ job: null, items: [] })
  void reloadLatestImport()
}, { immediate: true })
watch(reviewDialog, (open) => {
  if (!open && latestImportJob.value?.pending_image_count === 0) void reloadLatestImport()
})
onBeforeUnmount(() => {
  ++listRequest
  ++detailRequest
  ++importRequest
  stopImportPolling()
  clearTimeout(searchTimer)
  releaseProtectedImages()
})

defineExpose({ loadProducts, products, openProduct, detail, form, save, scrape })
</script>

<style scoped>
.product-table { overflow-x: auto; -webkit-overflow-scrolling: touch; }
.product-table table { min-width: 720px; }
.configured-filter { max-width: 260px; }
.editor-header {
  padding: 1rem 1.5rem;
  white-space: normal;
}
.editor-thumbnail {
  flex: none;
  width: 6rem;
  height: 6rem;
}
.editor-identity {
  min-width: 0;
  overflow-wrap: anywhere;
}
.editor-name {
  font-size: 1.125rem;
  font-weight: 700;
  line-height: 1.35;
}
.editor-meta {
  margin-top: 0.25rem;
  font-size: 0.875rem;
  line-height: 1.4;
}
.product-images { display: grid; grid-template-columns: repeat(auto-fill, minmax(150px, 1fr)); gap: 12px; }
.image-card { min-width: 0; overflow: hidden; }
.image-review-label { color: rgb(var(--v-theme-on-surface)); }
.source-summary { white-space: pre-line; overflow-wrap: anywhere; }
code { overflow-wrap: anywhere; }
@media (max-width: 600px) {
  .configured-filter { max-width: none; width: 100%; }
  .editor-header { padding-inline: 1rem; }
  .editor-thumbnail { width: 4.5rem; height: 4.5rem; }
}
</style>
