<template>
  <section aria-labelledby="product-advisory-heading">
    <div class="d-flex flex-wrap align-start justify-space-between ga-3 mb-4">
      <div>
        <h2 id="product-advisory-heading" class="text-h6 font-weight-bold">{{ $t('product_advisory_title') }}</h2>
        <p class="text-body-2 text-medium-emphasis mb-0">{{ $t('product_advisory_desc') }}</p>
      </div>
      <v-chip color="primary" variant="tonal" prepend-icon="mdi-package-variant-closed">
        {{ $t('product_advisory_groups', { count: total }) }}
      </v-chip>
    </div>

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
            <tr v-for="product in products" :key="product.parent_code">
              <td class="py-2">
                <div class="d-flex align-center ga-3">
                  <v-avatar rounded="lg" size="48" color="surface-variant">
                    <v-img v-if="displayURL(product.thumbnail_url || product.default_image_url)" :src="displayURL(product.thumbnail_url || product.default_image_url)" cover :alt="product.web_name" />
                    <v-icon v-else icon="mdi-image-outline" />
                  </v-avatar>
                  <div>
                    <div class="font-weight-medium">{{ product.web_name || product.parent_code }}</div>
                    <div v-if="product.brand" class="text-caption text-medium-emphasis">{{ product.brand }}</div>
                  </div>
                </div>
              </td>
              <td><code>{{ product.parent_code }}</code></td>
              <td class="text-center">{{ product.variant_count }}</td>
              <td>
                <v-chip v-if="product.has_content" color="success" size="small" variant="tonal">{{ $t('product_advisory_ready') }}</v-chip>
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

    <v-dialog v-model="dialog" max-width="960px" scrollable @after-leave="resetEditor">
      <v-card v-if="selected">
        <v-card-title class="d-flex align-start ga-3">
          <div class="flex-grow-1">
            <div>{{ selected.web_name || selected.parent_code }}</div>
            <div class="text-caption text-medium-emphasis">{{ selected.parent_code }} · {{ $t('product_advisory_variant_count', { count: selected.variant_count }) }}</div>
          </div>
          <v-btn icon="mdi-close" variant="text" size="small" :aria-label="$t('cancel')" @click="dialog = false" />
        </v-card-title>
        <v-divider />
        <v-card-text>
          <v-alert v-if="detailError" type="error" variant="tonal" density="compact" class="mb-4">{{ $t('product_advisory_detail_failed') }}</v-alert>
          <template v-if="detail">
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
            />

            <div class="text-subtitle-2 font-weight-bold mt-6 mb-2">{{ $t('product_advisory_web_source') }}</div>
            <div class="d-flex flex-column flex-sm-row ga-2 align-sm-start">
              <v-text-field
                v-model="form.source_url"
                :label="$t('product_advisory_source_url')"
                :hint="$t('kb_link_public_hint')"
                persistent-hint
                type="url"
                maxlength="2048"
                variant="outlined"
                class="flex-grow-1"
                data-testid="product-source-url"
              />
              <v-btn
                color="primary"
                variant="tonal"
                prepend-icon="mdi-web-sync"
                class="mt-sm-1"
                :loading="scraping"
                :disabled="!canEdit || !validSourceURL || scraping"
                data-testid="product-scrape"
                @click="scrape"
              >
                {{ detail.source_url ? $t('product_advisory_rescrape') : $t('product_advisory_scrape') }}
              </v-btn>
            </div>
            <v-alert v-if="sourceError" type="error" variant="tonal" density="compact" class="mt-2">{{ $t(sourceError) }}</v-alert>
            <v-sheet v-if="detail.scraped_content" color="surface-variant" rounded class="pa-3 mt-3">
              <div class="text-caption text-medium-emphasis mb-1">{{ detail.source_title || $t('product_advisory_scraped_preview') }}</div>
              <div class="scraped-preview">{{ detail.scraped_content }}</div>
            </v-sheet>

            <div class="d-flex flex-wrap align-center justify-space-between ga-2 mt-6 mb-3">
              <div>
                <div class="text-subtitle-2 font-weight-bold">{{ $t('product_advisory_images') }}</div>
                <div class="text-caption text-medium-emphasis">{{ $t('product_advisory_images_hint') }}</div>
              </div>
              <v-file-input
                v-if="canEdit"
                v-model="imageFiles"
                accept="image/jpeg,image/png,image/webp,image/gif"
                multiple
                hide-details
                density="compact"
                prepend-icon=""
                prepend-inner-icon="mdi-image-plus-outline"
                :label="$t('product_advisory_add_images')"
                class="image-input"
                :disabled="uploadingImages"
                @update:model-value="uploadImages"
              />
            </div>

            <div v-if="detail.images.length" class="product-images">
              <v-card v-for="image in detail.images" :key="image.id" variant="outlined" class="image-card">
                <v-img :src="displayURL(image.url)" :alt="image.caption || selected.web_name" height="140" cover />
                <div class="d-flex align-center px-2 py-1">
                  <v-checkbox-btn
                    :model-value="image.enabled"
                    color="primary"
                    :aria-label="$t('product_advisory_use_image')"
                    :disabled="!canEdit || updatingImage === image.id"
                    @update:model-value="(enabled) => setImageEnabled(image, !!enabled)"
                  />
                  <span class="text-caption">{{ image.source === 'scraped' ? $t('product_advisory_scraped') : $t('product_advisory_manual') }}</span>
                  <v-chip v-if="!image.enabled" size="x-small" color="warning" variant="tonal" class="ml-2">{{ $t('product_advisory_pending_review') }}</v-chip>
                  <v-spacer />
                  <v-btn v-if="image.source === 'upload' && canEdit" icon="mdi-delete-outline" size="x-small" variant="text" color="error" :aria-label="$t('delete')" @click="removeImage(image)" />
                </div>
                <v-text-field
                  v-model="image.caption"
                  :label="$t('product_advisory_image_caption')"
                  density="compact"
                  variant="underlined"
                  maxlength="500"
                  hide-details
                  class="px-2 pb-2"
                  :disabled="!canEdit || updatingImage === image.id"
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
          <v-btn color="primary" variant="flat" :loading="saving" :disabled="!canEdit || !detail || !sourceURLAcceptable" data-testid="product-save" @click="save">
            {{ $t('save') }}
          </v-btn>
        </v-card-actions>
      </v-card>
    </v-dialog>

    <v-snackbar v-model="snack" :color="snackColor" timeout="4000">{{ snackText }}</v-snackbar>
  </section>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import api from '../api'

type ProductGroup = {
  parent_code: string
  web_name: string
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
const detail = ref<ProductDetail | null>(null)
const loadingDetail = ref(false)
const detailError = ref(false)
const form = ref({ notes: '', source_url: '' })
let detailRequest = 0

async function openProduct(product: ProductGroup) {
  selected.value = product
  detail.value = null
  detailError.value = false
  dialog.value = true
  const request = ++detailRequest
  loadingDetail.value = true
  try {
    const { data } = await api.get(`${base.value}/${encodeURIComponent(product.parent_code)}`)
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
  imageFiles.value = []
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
  if (!selected.value || !detail.value || !props.canEdit || !sourceURLAcceptable.value) return
  const requestBase = base.value
  const parentCode = selected.value.parent_code
  saving.value = true
  try {
    const { data } = await api.put(`${requestBase}/${encodeURIComponent(parentCode)}`, {
      notes: form.value.notes.trim(),
      source_url: form.value.source_url.trim(),
    })
    if (base.value !== requestBase || selected.value?.parent_code !== parentCode || !dialog.value || !detail.value) return
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
const scraping = ref(false)
const sourceError = ref('')
async function scrape() {
  if (!selected.value || !validSourceURL.value || !props.canEdit) return
  const requestBase = base.value
  const parentCode = selected.value.parent_code
  scraping.value = true
  sourceError.value = ''
  try {
    const { data } = await api.post(`${requestBase}/${encodeURIComponent(parentCode)}/scrape`, {
      url: form.value.source_url.trim(),
    })
    if (base.value !== requestBase || selected.value?.parent_code !== parentCode || !dialog.value) return
    const updated = { ...detail.value!, ...data, images: data?.images || detail.value!.images } as ProductDetail
    detail.value = updated
    form.value.source_url = updated.source_url || form.value.source_url
    prepareImages(updated.images.map((image) => image.url))
    notify(t('product_advisory_scraped_ok'))
    await loadProducts(page.value)
  } catch (error: any) {
    const code = error?.response?.data?.error || error?.response?.data?.message
    sourceError.value = code === 'firecrawl_not_configured' ? 'product_advisory_firecrawl_missing' : 'product_advisory_scrape_failed'
  } finally {
    scraping.value = false
  }
}

const imageFiles = ref<File[] | File | null>([])
const uploadingImages = ref(false)
async function uploadImages() {
  if (!selected.value || !props.canEdit) return
  const files = Array.isArray(imageFiles.value) ? imageFiles.value : imageFiles.value ? [imageFiles.value] : []
  if (!files.length) return
  if (files.length > 8 || files.some((file) => file.size > 5 * 1024 ** 2) || files.reduce((sum, file) => sum + file.size, 0) > 11 * 1024 ** 2) {
    imageFiles.value = []
    notify(t('product_advisory_image_size'), 'error')
    return
  }
  const body = new FormData()
  files.forEach((file) => body.append('images', file))
  const requestBase = base.value
  const parentCode = selected.value.parent_code
  uploadingImages.value = true
  try {
    const { data } = await api.post(`${requestBase}/${encodeURIComponent(parentCode)}/images`, body, {
      headers: { 'Content-Type': 'multipart/form-data' },
    })
    if (base.value !== requestBase || selected.value?.parent_code !== parentCode || !detail.value) return
    const added = data?.images || []
    detail.value!.images.push(...added)
    prepareImages(added.map((image: ProductImage) => image.url))
    imageFiles.value = []
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
  const parentCode = selected.value.parent_code
  updatingImage.value = image.id
  try {
    await api.patch(`${requestBase}/${encodeURIComponent(parentCode)}/images/${encodeURIComponent(image.id)}`, { enabled })
    if (base.value !== requestBase || selected.value?.parent_code !== parentCode) return
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
  const parentCode = selected.value.parent_code
  updatingImage.value = image.id
  try {
    await api.patch(`${requestBase}/${encodeURIComponent(parentCode)}/images/${encodeURIComponent(image.id)}`, {
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
  const parentCode = selected.value.parent_code
  try {
    await api.delete(`${requestBase}/${encodeURIComponent(parentCode)}/images/${encodeURIComponent(image.id)}`)
    if (base.value !== requestBase || selected.value?.parent_code !== parentCode || !detail.value) return
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
onBeforeUnmount(() => {
  ++listRequest
  ++detailRequest
  clearTimeout(searchTimer)
  releaseProtectedImages()
})

defineExpose({ loadProducts, products, openProduct, detail, form, save, scrape })
</script>

<style scoped>
.product-table { overflow-x: auto; -webkit-overflow-scrolling: touch; }
.product-table table { min-width: 720px; }
.configured-filter { max-width: 260px; }
.image-input { max-width: 330px; }
.product-images { display: grid; grid-template-columns: repeat(auto-fill, minmax(150px, 1fr)); gap: 12px; }
.image-card { min-width: 0; overflow: hidden; }
.scraped-preview { display: -webkit-box; overflow: hidden; -webkit-box-orient: vertical; -webkit-line-clamp: 6; white-space: pre-wrap; }
code { overflow-wrap: anywhere; }
@media (max-width: 600px) {
  .configured-filter, .image-input { max-width: none; width: 100%; }
}
</style>
