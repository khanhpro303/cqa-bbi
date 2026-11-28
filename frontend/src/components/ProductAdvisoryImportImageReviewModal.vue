<template>
  <v-dialog
    :model-value="modelValue"
    max-width="1120"
    scrollable
    :persistent="approvingAll || Boolean(approvingImageId)"
    aria-labelledby="product-import-image-review-title"
    @update:model-value="setOpen"
  >
    <v-card>
      <v-card-title class="review-header d-flex align-center ga-3 flex-wrap py-4">
        <div class="flex-grow-1 min-width-0">
          <h2 id="product-import-image-review-title" class="review-title">
            {{ t('product_advisory_import_review_title') }}
          </h2>
          <p class="text-body-2 text-medium-emphasis mb-0">
            {{ t('product_advisory_import_review_desc') }}
          </p>
        </div>
        <v-btn
          color="primary"
          variant="flat"
          prepend-icon="mdi-check-all"
          :disabled="totalPending === 0 || Boolean(approvingImageId)"
          :loading="approvingAll"
          data-testid="approve-all-import-images"
          @click="confirmApproveAll = true"
        >
          {{ t('product_advisory_import_review_approve_all', { count: totalPending }) }}
        </v-btn>
        <v-btn
          icon="mdi-close"
          variant="text"
          class="review-close"
          :aria-label="t('product_advisory_import_close')"
          :disabled="approvingAll || Boolean(approvingImageId)"
          @click="setOpen(false)"
        />
      </v-card-title>

      <v-divider />

      <v-card-text class="pa-0">
        <v-alert v-if="groupsWithImages.length === 0" type="info" variant="tonal" class="ma-4">
          {{ t('product_advisory_import_review_no_groups') }}
        </v-alert>

        <template v-else>
          <div class="review-layout">
            <nav class="group-nav pa-3" :aria-label="t('product_advisory_import_review_groups')">
              <v-list density="compact" nav bg-color="transparent">
                <v-virtual-scroll ref="groupScroll" :items="groupsWithImages" :item-height="48" class="group-scroll">
                  <template #default="{ item: group, index }">
                    <v-list-item
                      :active="index === currentIndex"
                      :aria-current="index === currentIndex ? 'true' : undefined"
                      :disabled="approvingAll || Boolean(approvingImageId)"
                      color="primary"
                      link
                      :data-testid="`review-group-${group.id}`"
                      @click="selectGroup(index)"
                    >
                      <v-list-item-title>{{ group.web_name || group.parent_code }}</v-list-item-title>
                      <v-list-item-subtitle>{{ [group.parent_code, group.product_category].filter(Boolean).join(' · ') }}</v-list-item-subtitle>
                      <template #append>
                        <v-chip size="x-small" class="review-indicator" :color="group.pending_image_count > 0 ? 'warning' : 'success'" variant="tonal">
                          {{ group.pending_image_count }}/{{ group.image_count }}
                        </v-chip>
                      </template>
                    </v-list-item>
                  </template>
                </v-virtual-scroll>
              </v-list>
            </nav>

            <section v-if="currentGroup" class="group-content pa-4" aria-live="polite">
              <div class="d-flex align-start justify-space-between ga-3 flex-wrap mb-4">
                <div>
                  <div class="text-overline text-medium-emphasis">
                    {{ t('product_advisory_import_review_position', { current: currentIndex + 1, total: groupsWithImages.length }) }}
                  </div>
                  <h3 class="text-h6">{{ currentGroup.web_name || currentGroup.parent_code }}</h3>
                  <p class="text-body-2 text-medium-emphasis mb-0">{{ [currentGroup.parent_code, currentGroup.product_category].filter(Boolean).join(' · ') }}</p>
                </div>
                <v-chip v-if="!loading && !loadError && images.length" class="review-indicator" :color="currentPending > 0 ? 'warning' : 'success'" variant="tonal">
                  {{ currentPending > 0
                    ? t('product_advisory_import_review_pending_count', { count: currentPending })
                    : t('product_advisory_import_review_group_done') }}
                </v-chip>
              </div>

              <v-alert v-if="actionError" type="error" variant="tonal" closable class="mb-4" @click:close="actionError = false">
                {{ t('product_advisory_import_review_approve_failed') }}
              </v-alert>

              <div v-if="loading" class="d-flex justify-center align-center review-state">
                <v-progress-circular indeterminate color="primary" />
              </div>

              <v-alert v-else-if="loadError" type="error" variant="tonal" class="mb-4">
                <div class="d-flex align-center justify-space-between ga-3 flex-wrap">
                  <span>{{ t('product_advisory_import_review_load_failed') }}</span>
                  <v-btn size="small" variant="text" @click="loadImages">{{ t('retry') }}</v-btn>
                </div>
              </v-alert>

              <v-alert v-else-if="images.length === 0" type="info" variant="tonal">
                {{ t('product_advisory_import_review_no_images') }}
              </v-alert>

              <div v-else class="image-grid">
                <v-card v-for="(image, index) in images" :key="image.id" variant="outlined" class="image-card">
                  <v-img
                    :src="image.url"
                    :alt="image.caption || t('product_advisory_import_review_image_alt', { number: index + 1 })"
                    height="220"
                    referrerpolicy="no-referrer"
                  >
                    <template #placeholder>
                      <div class="d-flex fill-height align-center justify-center">
                        <v-progress-circular indeterminate size="28" color="primary" />
                      </div>
                    </template>
                    <template #error>
                      <div class="d-flex fill-height align-center justify-center pa-3 text-center text-medium-emphasis">
                        {{ t('product_advisory_import_review_image_unavailable') }}
                      </div>
                    </template>
                  </v-img>
                  <v-card-text class="pb-2">
                    <p v-if="image.caption" class="text-body-2 image-caption mb-2">{{ image.caption }}</p>
                    <v-chip :color="image.enabled ? 'success' : 'warning'" size="small" class="review-indicator" variant="tonal">
                      {{ t(image.enabled ? 'product_advisory_import_review_approved' : 'product_advisory_import_review_pending') }}
                    </v-chip>
                  </v-card-text>
                  <v-card-actions>
                    <v-btn
                      block
                      color="primary"
                      variant="outlined"
                      prepend-icon="mdi-check"
                      :disabled="image.enabled || image.source !== 'scraped' || approvingAll || Boolean(approvingImageId && approvingImageId !== image.id)"
                      :loading="approvingImageId === image.id"
                      :data-testid="`approve-image-${image.id}`"
                      @click="approveImage(image)"
                    >
                      {{ t(image.enabled ? 'product_advisory_import_review_approved' : 'product_advisory_import_review_approve_image') }}
                    </v-btn>
                  </v-card-actions>
                </v-card>
              </div>
            </section>
          </div>
        </template>
      </v-card-text>

      <v-divider />
      <v-card-actions class="justify-space-between px-4 py-3">
        <v-btn
          variant="text"
          prepend-icon="mdi-chevron-left"
          :disabled="currentIndex <= 0 || loading"
          @click="selectGroup(currentIndex - 1)"
        >
          {{ t('product_advisory_import_review_previous') }}
        </v-btn>
        <v-btn
          color="primary"
          variant="tonal"
          append-icon="mdi-chevron-right"
          :disabled="currentIndex >= groupsWithImages.length - 1 || loading"
          @click="selectGroup(currentIndex + 1)"
        >
          {{ t('next') }}
        </v-btn>
      </v-card-actions>
    </v-card>
  </v-dialog>

  <v-dialog v-model="confirmApproveAll" max-width="480" :persistent="approvingAll" aria-labelledby="product-import-confirm-approval">
    <v-card>
      <v-card-title id="product-import-confirm-approval" class="text-wrap">{{ t('product_advisory_import_review_confirm_title') }}</v-card-title>
      <v-card-text>
        {{ t('product_advisory_import_review_confirm_desc', { count: totalPending }) }}
      </v-card-text>
      <v-card-actions class="justify-end">
        <v-btn variant="text" :disabled="approvingAll" @click="confirmApproveAll = false">{{ t('cancel') }}</v-btn>
        <v-btn color="primary" variant="flat" :loading="approvingAll" data-testid="confirm-approve-all" @click="approveAll">
          {{ t('product_advisory_import_review_confirm_action') }}
        </v-btn>
      </v-card-actions>
    </v-card>
  </v-dialog>
</template>

<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import api from '../api'
import type { ProductAdvisoryImportReviewGroup } from './productAdvisoryImport'

interface ImportImage {
  id: string
  url: string
  enabled: boolean
  sort_order: number
  source: string
  caption: string
}

const props = defineProps<{
  modelValue: boolean
  tenantId: string
  jobId: string
  groups: ProductAdvisoryImportReviewGroup[]
}>()

const emit = defineEmits<{
  (event: 'update:modelValue', value: boolean): void
  (event: 'updated'): void
}>()

const { t } = useI18n()
const currentIndex = ref(0)
const images = ref<ImportImage[]>([])
const loading = ref(false)
const loadError = ref(false)
const actionError = ref(false)
const approvingImageId = ref('')
const approvingAll = ref(false)
const confirmApproveAll = ref(false)
const groupScroll = ref<{ scrollToIndex(index: number): void } | null>(null)
let requestVersion = 0
let requestController: AbortController | null = null
const pendingByGroup = ref<Record<string, number>>({})

const groupsWithImages = computed(() => props.groups
  .filter(group => group.image_count > 0)
  .map(group => ({ ...group, pending_image_count: pendingByGroup.value[group.id] ?? group.pending_image_count })))
const currentGroup = computed(() => groupsWithImages.value[currentIndex.value])
const totalPending = computed(() => groupsWithImages.value.reduce((sum, group) => sum + Math.max(0, group.pending_image_count), 0))
const currentPending = computed(() => images.value.filter(image => image.source === 'scraped' && !image.enabled).length)
const endpoint = computed(() => `/tenants/${encodeURIComponent(props.tenantId)}/product-advisory-imports/${encodeURIComponent(props.jobId)}/images`)

function invalidateRequests() {
  requestVersion += 1
  requestController?.abort()
  requestController = null
}

function setOpen(value: boolean) {
  if (!value) {
    invalidateRequests()
    confirmApproveAll.value = false
  }
  emit('update:modelValue', value)
}

function selectGroup(index: number) {
  if (approvingAll.value || approvingImageId.value || index < 0 || index >= groupsWithImages.value.length || index === currentIndex.value) return
  currentIndex.value = index
  groupScroll.value?.scrollToIndex(index)
  void loadImages()
}

async function loadImages() {
  const group = currentGroup.value
  if (!props.modelValue || !group || !props.tenantId || !props.jobId) {
    images.value = []
    return
  }

  invalidateRequests()
  const version = requestVersion
  const controller = new AbortController()
  requestController = controller
  loading.value = true
  loadError.value = false
  actionError.value = false
  images.value = []
  try {
    const { data } = await api.get<{ images: ImportImage[] }>(endpoint.value, {
      params: { item_id: group.id },
      signal: controller.signal,
    })
    if (version !== requestVersion || group.id !== currentGroup.value?.id || !props.modelValue) return
    images.value = [...(data.images || [])].sort((a, b) => a.sort_order - b.sort_order)
  } catch {
    if (version === requestVersion && !controller.signal.aborted) loadError.value = true
  } finally {
    if (version === requestVersion) loading.value = false
  }
}

async function approveImage(image: ImportImage) {
  if (image.enabled || image.source !== 'scraped' || approvingImageId.value || approvingAll.value) return
  const tenantID = props.tenantId
  const jobID = props.jobId
  approvingImageId.value = image.id
  actionError.value = false
  try {
    const { data } = await api.post<{ approved_count: number }>(`${endpoint.value}/approve`, { image_id: image.id })
    if (tenantID !== props.tenantId || jobID !== props.jobId) return
    if (!data.approved_count) {
      void loadImages()
      emit('updated')
      return
    }
    image.enabled = true
    const group = currentGroup.value
    if (group) pendingByGroup.value[group.id] = Math.max(0, group.pending_image_count - 1)
    emit('updated')
  } catch {
    if (tenantID === props.tenantId && jobID === props.jobId) actionError.value = true
  } finally {
    approvingImageId.value = ''
  }
}

async function approveAll() {
  if (totalPending.value === 0 || approvingAll.value || approvingImageId.value) return
  const tenantID = props.tenantId
  const jobID = props.jobId
  const pending = totalPending.value
  approvingAll.value = true
  actionError.value = false
  try {
    const { data } = await api.post<{ approved_count: number }>(`${endpoint.value}/approve`, { all: true })
    if (tenantID !== props.tenantId || jobID !== props.jobId) return
    for (const image of images.value) {
      if (image.source === 'scraped') image.enabled = true
    }
    pendingByGroup.value = Object.fromEntries(props.groups.map(group => [group.id, 0]))
    confirmApproveAll.value = false
    if (data.approved_count < pending || loading.value) void loadImages()
    emit('updated')
  } catch {
    if (tenantID === props.tenantId && jobID === props.jobId) actionError.value = true
  } finally {
    approvingAll.value = false
  }
}

watch(
  () => [props.modelValue, props.tenantId, props.jobId] as const,
  ([open], previous) => {
    invalidateRequests()
    confirmApproveAll.value = false
    images.value = []
    loadError.value = false
    actionError.value = false
    if (!open) return
    pendingByGroup.value = Object.fromEntries(props.groups.map(group => [group.id, group.pending_image_count]))
    const firstPending = groupsWithImages.value.findIndex(group => group.pending_image_count > 0)
    currentIndex.value = firstPending >= 0 ? firstPending : 0
    void nextTick(() => groupScroll.value?.scrollToIndex(currentIndex.value))
    if (!previous || previous[1] !== props.tenantId || previous[2] !== props.jobId || !previous[0]) void loadImages()
  },
  { immediate: true },
)

watch(
  () => props.groups.map(group => `${group.id}:${group.image_count}:${group.pending_image_count}`).join('|'),
  () => {
    pendingByGroup.value = Object.fromEntries(props.groups.map(group => [group.id, group.pending_image_count]))
    if (currentIndex.value >= groupsWithImages.value.length) currentIndex.value = Math.max(0, groupsWithImages.value.length - 1)
  },
)
onBeforeUnmount(invalidateRequests)

defineExpose({ loadImages, selectGroup, approveImage, approveAll, images, currentIndex })
</script>

<style scoped>
.min-width-0 {
  min-width: 0;
}

.review-header {
  padding-inline-end: 4.25rem;
  position: relative;
  white-space: normal;
}

.review-title {
  font-size: 1.375rem;
  line-height: 1.35;
  margin-bottom: 0.375rem;
  overflow-wrap: anywhere;
}

.review-close {
  position: absolute;
  inset-inline-end: 0.75rem;
  top: 0.75rem;
}

.review-indicator :deep(.v-chip__content) {
  color: rgb(var(--v-theme-on-surface));
}

.review-layout {
  display: grid;
  grid-template-columns: minmax(220px, 280px) minmax(0, 1fr);
  min-height: 500px;
}

.group-nav {
  border-right: 1px solid rgba(var(--v-border-color), var(--v-border-opacity));
  min-width: 0;
}

.group-scroll {
  max-height: 66vh;
}

.group-content {
  min-width: 0;
}

.review-state {
  min-height: 240px;
}

.image-grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(220px, 1fr));
  gap: 16px;
}

.image-card {
  min-width: 0;
}

.image-caption {
  overflow-wrap: anywhere;
}

@media (max-width: 700px) {
  .review-layout {
    grid-template-columns: minmax(0, 1fr);
  }

  .group-nav {
    border-right: 0;
    border-bottom: 1px solid rgba(var(--v-border-color), var(--v-border-opacity));
  }

  .group-scroll { max-height: 160px; }

  .image-grid {
    grid-template-columns: 1fr;
  }
}

</style>
