<template>
  <v-card>
    <v-card-text>
      <form class="tracking-filters" role="search" @submit.prevent="searchOrders">
        <v-text-field
          v-model="query"
          :label="$t('crm_order_tracking_search')"
          prepend-inner-icon="mdi-magnify"
          maxlength="120"
          density="compact"
          hide-details
        />
        <v-btn type="submit" variant="tonal">{{ $t('crm_order_tracking_find') }}</v-btn>
        <v-select
          v-model="filter"
          :items="filterOptions"
          :label="$t('crm_order_tracking_customer_status')"
          density="compact"
          hide-details
          @update:model-value="loadOrders(1)"
        />
        <v-btn variant="text" prepend-icon="mdi-refresh" @click="loadOrders()">
          {{ $t('crm_order_tracking_refresh') }}
        </v-btn>
      </form>

      <div v-if="loading" class="py-8 text-center" role="status">{{ $t('crm_order_tracking_loading') }}</div>
      <v-alert v-else-if="listError" type="error" variant="tonal" class="mt-4">
        {{ $t('crm_order_tracking_list_error') }}
        <v-btn variant="text" @click="loadOrders()">{{ $t('crm_order_tracking_retry') }}</v-btn>
      </v-alert>
      <p v-else-if="orders.length === 0" class="pa-8 text-center text-medium-emphasis" role="status">
        {{ $t('crm_order_tracking_empty') }}
      </p>

      <v-expansion-panels
        v-else
        :model-value="openId"
        variant="accordion"
        class="mt-4"
        @update:model-value="openOrder"
      >
        <v-expansion-panel v-for="order in orders" :key="order.id" :value="order.id">
          <v-expansion-panel-title>
            <span class="order-summary">
              <span>
                <strong>{{ order.order_code }}</strong>
                <span class="summary-meta">{{ formatDate(order.order_date) }}</span>
              </span>
              <span>
                <span>{{ order.customer_name || order.customer_code }}</span>
                <span v-if="order.customer_name || order.group_name" class="summary-meta">
                  {{ [order.customer_name && order.customer_code, order.group_name].filter(Boolean).join(' · ') }}
                </span>
              </span>
              <span>
                <span class="summary-meta">{{ $t('crm_order_tracking_erp_status') }}</span>
                <span>{{ openId === order.id && detail ? detail.status_name : order.status_name }}</span>
              </span>
              <v-chip :color="order.confirmation ? 'success' : 'warning'" size="small" variant="tonal">
                {{ $t(order.confirmation ? 'crm_order_tracking_confirmed' : 'crm_order_tracking_pending') }}
              </v-chip>
            </span>
          </v-expansion-panel-title>

          <v-expansion-panel-text v-if="openId === order.id">
            <p v-if="order.delivered_at" class="text-caption text-medium-emphasis mb-3">
              {{ $t('crm_order_tracking_sent') }} · {{ formatTimestamp(order.delivered_at) }}
            </p>
            <section v-if="order.confirmation" class="confirmation pa-4 mb-4">
              <h3 class="text-subtitle-2 font-weight-bold">{{ $t('crm_order_tracking_confirmed') }}</h3>
              <p class="text-caption text-medium-emphasis">
                {{ order.confirmation.sender_name || order.confirmation.sender_id }}
                · {{ formatTimestamp(order.confirmation.confirmed_at) }}
              </p>
              <blockquote class="confirmation-message mt-2 mb-1">{{ order.confirmation.text }}</blockquote>
              <p class="text-caption text-medium-emphasis break-id">
                {{ $t('crm_order_tracking_message_id') }} {{ order.confirmation.message_id }}
              </p>
              <p v-if="order.confirmation.quote_message_id" class="text-caption text-medium-emphasis break-id">
                {{ $t('crm_order_tracking_quote_id') }} {{ order.confirmation.quote_message_id }}
              </p>
              <v-btn
                v-if="order.confirmation.has_evidence && !evidenceUrl"
                class="mt-2"
                variant="tonal"
                prepend-icon="mdi-image-outline"
                :loading="evidenceLoading"
                @click="loadEvidence(order.id)"
              >
                {{ $t('crm_order_tracking_evidence_show') }}
              </v-btn>
              <div v-if="!order.confirmation.has_evidence" class="mt-3">
                <p class="text-caption text-medium-emphasis">{{ $t('crm_order_tracking_evidence_missing') }}</p>
                <div v-if="canAttachEvidence" class="mt-2">
                  <v-file-input
                    :label="$t('crm_order_tracking_evidence_select')"
                    accept="image/png,image/jpeg"
                    :hint="$t('crm_order_tracking_evidence_file_hint')"
                    :disabled="evidenceUploading"
                    density="compact"
                    persistent-hint
                    @update:model-value="selectEvidence"
                  />
                  <v-btn
                    class="mt-2"
                    variant="tonal"
                    prepend-icon="mdi-upload"
                    :loading="evidenceUploading"
                    :disabled="!evidenceFile"
                    @click="attachEvidence(order)"
                  >
                    {{ $t('crm_order_tracking_evidence_attach') }}
                  </v-btn>
                </div>
              </div>
              <p v-if="evidenceUploadError" class="text-error mt-2" role="alert">{{ $t(evidenceUploadError) }}</p>
              <p v-if="evidenceError" class="text-error mt-2" role="alert">{{ $t('crm_order_tracking_evidence_error') }}</p>
              <figure v-if="evidenceUrl" class="mt-3">
                <img :src="evidenceUrl" :alt="$t('crm_order_tracking_evidence_alt')" class="evidence-image" />
                <v-btn
                  :href="evidenceUrl"
                  :download="`zalo-${order.order_code}.${evidenceExtension}`"
                  class="mt-2"
                  variant="text"
                  prepend-icon="mdi-download"
                >
                  {{ $t('crm_order_tracking_evidence_download') }}
                </v-btn>
              </figure>
            </section>

            <section>
              <div class="d-flex align-center flex-wrap ga-2 mb-2">
                <h3 class="text-subtitle-2 font-weight-bold">{{ $t('crm_order_tracking_live') }}</h3>
                <v-spacer />
                <v-btn variant="text" size="small" :loading="detailLoading" @click="loadDetail(order.id)">
                  {{ $t(detailError ? 'crm_order_tracking_retry' : 'crm_order_tracking_refresh') }}
                </v-btn>
              </div>
              <p v-if="detailLoading" class="py-3" role="status">{{ $t('crm_order_tracking_loading') }}</p>
              <v-alert v-else-if="detailError" type="error" variant="tonal">
                {{ $t('crm_order_tracking_detail_error') }}
              </v-alert>
              <template v-else-if="detail">
                <v-table v-if="detail.lines?.length" density="compact" :aria-label="$t('crm_order_tracking_live')">
                  <thead>
                    <tr>
                      <th scope="col">{{ $t('crm_order_tracking_product') }}</th>
                      <th scope="col" class="text-end">{{ $t('crm_order_tracking_quantity') }}</th>
                      <th scope="col" class="text-end">{{ $t('crm_order_tracking_unit_price') }}</th>
                      <th scope="col" class="text-end">{{ $t('crm_order_tracking_line_total') }}</th>
                    </tr>
                  </thead>
                  <tbody>
                    <tr v-for="(line, index) in detail.lines" :key="index">
                      <td>
                        {{ line.name || line.sku }}
                        <span v-if="line.name && line.sku" class="summary-meta">{{ line.sku }}</span>
                      </td>
                      <td class="text-end text-no-wrap">{{ quantityFormatter.format(line.quantity) }} {{ line.unit }}</td>
                      <td class="text-end text-no-wrap">{{ moneyFormatter.format(line.unit_price) }}</td>
                      <td class="text-end text-no-wrap">{{ moneyFormatter.format(line.total) }}</td>
                    </tr>
                  </tbody>
                </v-table>
                <p v-else class="text-medium-emphasis">{{ $t('crm_order_tracking_no_lines') }}</p>
                <p class="text-end font-weight-bold mt-3">
                  {{ $t('crm_order_tracking_total') }} {{ moneyFormatter.format(detail.total) }}
                </p>
                <p v-if="detail.note" class="mt-2 confirmation-message">
                  {{ $t('crm_order_tracking_note') }} {{ detail.note }}
                </p>
              </template>
            </section>
          </v-expansion-panel-text>
        </v-expansion-panel>
      </v-expansion-panels>

      <v-pagination
        v-if="!loading && !listError && pageCount > 1"
        :model-value="page"
        :length="pageCount"
        :total-visible="5"
        density="compact"
        class="mt-4"
        @update:model-value="loadOrders"
      />
    </v-card-text>
  </v-card>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { isAxiosError } from 'axios'
import { useI18n } from 'vue-i18n'
import api from '../api'
import { useTenantEventRefresh } from '../composables/useTenantEvents'

interface Confirmation {
  sender_id: string
  sender_name?: string
  text: string
  message_id: string
  quote_message_id?: string
  confirmed_at: string
  has_evidence: boolean
}

interface TrackedOrder {
  id: string
  order_code: string
  customer_code: string
  customer_name: string
  group_name: string
  order_date: string
  delivered_at?: string
  status_name: string
  confirmation?: Confirmation
}

interface LiveOrder {
  status_name: string
  total: number
  note: string
  lines: { sku: string; name: string; unit: string; quantity: number; unit_price: number; total: number }[]
}

const props = defineProps<{ tenantId: string; canAttachEvidence?: boolean }>()
const { locale, t } = useI18n()
const path = computed(() => `/tenants/${encodeURIComponent(props.tenantId)}/crm/order-tracking`)
const query = ref('')
let appliedQuery = ''
const filter = ref('all')
const filterOptions = computed(() => [
  { title: t('crm_order_tracking_all'), value: 'all' },
  { title: t('crm_order_tracking_pending'), value: 'pending' },
  { title: t('crm_order_tracking_confirmed'), value: 'confirmed' },
])
const page = ref(1)
const pageCount = ref(1)
const orders = ref<TrackedOrder[]>([])
const loading = ref(false)
const listError = ref(false)
const openId = ref<string>()
const detail = ref<LiveOrder>()
const detailLoading = ref(false)
const detailError = ref(false)
const evidenceUrl = ref('')
const evidenceExtension = ref('')
const evidenceLoading = ref(false)
const evidenceError = ref(false)
const evidenceFile = ref<File>()
const evidenceUploading = ref(false)
const evidenceUploadError = ref('')

let listRequest: AbortController | undefined
let detailRequest: AbortController | undefined
let evidenceRequest: AbortController | undefined
let uploadRequest: AbortController | undefined

const moneyFormatter = computed(() => new Intl.NumberFormat(locale.value, { style: 'currency', currency: 'VND' }))
const quantityFormatter = computed(() => new Intl.NumberFormat(locale.value))
const dateFormatter = computed(() => new Intl.DateTimeFormat(locale.value, { dateStyle: 'short', timeZone: 'Asia/Ho_Chi_Minh' }))
const dateTimeFormatter = computed(() => new Intl.DateTimeFormat(locale.value, {
  dateStyle: 'short', timeStyle: 'short', timeZone: 'Asia/Ho_Chi_Minh',
}))

function formatDate(value: string) {
  const date = new Date(value)
  return Number.isNaN(date.getTime()) ? value : dateFormatter.value.format(date)
}

function formatTimestamp(value: string) {
  const date = new Date(value)
  return Number.isNaN(date.getTime()) ? value : dateTimeFormatter.value.format(date)
}

function clearOpenOrder() {
  detailRequest?.abort()
  evidenceRequest?.abort()
  uploadRequest?.abort()
  if (evidenceUrl.value) URL.revokeObjectURL(evidenceUrl.value)
  openId.value = undefined
  detail.value = undefined
  detailLoading.value = false
  detailError.value = false
  evidenceUrl.value = ''
  evidenceExtension.value = ''
  evidenceLoading.value = false
  evidenceError.value = false
  evidenceFile.value = undefined
  evidenceUploading.value = false
  evidenceUploadError.value = ''
}

function searchOrders() {
  appliedQuery = query.value.trim()
  void loadOrders(1)
}

async function loadOrders(requestedPage = page.value, background = false) {
  listRequest?.abort()
  const foreground = !background || loading.value || listError.value
  if (foreground) {
    clearOpenOrder()
    page.value = requestedPage
    loading.value = true
    listError.value = false
  }
  const request = new AbortController()
  listRequest = request
  try {
    const { data } = await api.get<{ data: TrackedOrder[]; total: number; page: number; page_size: number }>(path.value, {
      params: { q: appliedQuery, confirmation: filter.value, page: requestedPage },
      signal: request.signal,
    })
    if (request.signal.aborted) return
    const nextOrders = data.data ?? []
    if (openId.value) {
      const previous = orders.value.find(order => order.id === openId.value)
      const next = nextOrders.find(order => order.id === openId.value)
      if (!next) clearOpenOrder()
      else if (detail.value && next.status_name !== previous?.status_name) detail.value.status_name = next.status_name
    }
    orders.value = nextOrders
    page.value = data.page
    pageCount.value = Math.max(1, Math.ceil(data.total / data.page_size))
    if (page.value > pageCount.value) await loadOrders(pageCount.value)
  } catch {
    if (!request.signal.aborted && foreground) listError.value = true
  } finally {
    if (!request.signal.aborted) loading.value = false
  }
}

function openOrder(id: unknown) {
  clearOpenOrder()
  if (typeof id === 'string') {
    openId.value = id
    void loadDetail(id)
  }
}

async function loadDetail(id: string) {
  detailRequest?.abort()
  const request = new AbortController()
  detailRequest = request
  detailLoading.value = true
  detailError.value = false
  try {
    const { data } = await api.get<{ data: LiveOrder }>(`${path.value}/${encodeURIComponent(id)}`, { signal: request.signal })
    if (!request.signal.aborted) detail.value = data.data
  } catch {
    if (!request.signal.aborted) detailError.value = true
  } finally {
    if (!request.signal.aborted) detailLoading.value = false
  }
}

async function loadEvidence(id: string) {
  evidenceRequest?.abort()
  const request = new AbortController()
  evidenceRequest = request
  evidenceLoading.value = true
  evidenceError.value = false
  try {
    const { data } = await api.get<Blob>(`${path.value}/${encodeURIComponent(id)}/evidence`, {
      responseType: 'blob', signal: request.signal,
    })
    if (request.signal.aborted) return
    if (!['image/png', 'image/jpeg'].includes(data.type)) throw new Error('Invalid evidence image type')
    evidenceExtension.value = data.type === 'image/png' ? 'png' : 'jpg'
    evidenceUrl.value = URL.createObjectURL(data)
  } catch {
    if (!request.signal.aborted) evidenceError.value = true
  } finally {
    if (!request.signal.aborted) evidenceLoading.value = false
  }
}

function selectEvidence(selection: File | File[] | null) {
  const file = Array.isArray(selection) ? selection[0] : selection
  evidenceFile.value = undefined
  evidenceUploadError.value = ''
  if (!file) return
  if (file.size === 0 || file.size > 5 * 1024 * 1024 || (file.type && !['image/png', 'image/jpeg'].includes(file.type))) {
    evidenceUploadError.value = 'crm_order_tracking_evidence_file_invalid'
    return
  }
  evidenceFile.value = file
}

async function attachEvidence(order: TrackedOrder) {
  const file = evidenceFile.value
  if (!props.canAttachEvidence || !file || evidenceUploading.value) return
  const request = new AbortController()
  uploadRequest = request
  evidenceUploading.value = true
  evidenceUploadError.value = ''
  const form = new FormData()
  form.append('image', file)
  try {
    await api.post(`${path.value}/${encodeURIComponent(order.id)}/evidence`, form, {
      headers: { 'Content-Type': 'multipart/form-data' }, signal: request.signal,
    })
    if (request.signal.aborted) return
    const confirmation = orders.value.find(item => item.id === order.id)?.confirmation
    if (confirmation) confirmation.has_evidence = true
    evidenceFile.value = undefined
    await loadEvidence(order.id)
  } catch (error) {
    if (request.signal.aborted) return
    if (isAxiosError(error) && error.response?.status === 409) {
      evidenceUploadError.value = 'crm_order_tracking_evidence_exists'
      await loadOrders(page.value, true)
    } else {
      evidenceUploadError.value = 'crm_order_tracking_evidence_upload_error'
    }
  } finally {
    if (!request.signal.aborted) evidenceUploading.value = false
  }
}

onMounted(() => {
  void loadOrders(1)
})
useTenantEventRefresh(() => props.tenantId, 'cloudify_orders_updated', () => loadOrders(page.value, true))

onBeforeUnmount(() => {
  listRequest?.abort()
  clearOpenOrder()
})
</script>

<style scoped>
.tracking-filters {
  display: grid;
  grid-template-columns: minmax(13rem, 1fr) auto minmax(11rem, 16rem) auto;
  gap: 0.5rem;
  align-items: center;
}

.order-summary {
  display: grid;
  grid-template-columns: minmax(6rem, 0.8fr) minmax(10rem, 1.7fr) minmax(8rem, 1fr) auto;
  gap: 0.75rem;
  align-items: center;
  width: 100%;
  margin-inline-end: 1rem;
  line-height: 1.4;
  text-align: start;
  overflow-wrap: anywhere;
}

.summary-meta {
  display: block;
  color: rgba(var(--v-theme-on-surface), var(--v-medium-emphasis-opacity));
  font-size: 0.75rem;
}

.confirmation {
  background-color: rgb(var(--v-theme-surface-variant) / 0.12);
  border: 1px solid rgba(var(--v-border-color), var(--v-border-opacity));
  border-radius: 0.5rem;
}

.confirmation-message {
  white-space: pre-wrap;
  overflow-wrap: anywhere;
}

.break-id {
  overflow-wrap: anywhere;
}

.evidence-image {
  display: block;
  max-width: 100%;
  height: auto;
}

@media (max-width: 800px) {
  .tracking-filters,
  .order-summary {
    grid-template-columns: minmax(0, 1fr) auto;
  }

  .order-summary {
    grid-template-columns: repeat(2, minmax(0, 1fr));
  }

  .order-summary :deep(.v-chip) {
    justify-self: start;
    max-width: 100%;
    height: auto;
    white-space: normal;
  }
}

@media (max-width: 370px) {
  .tracking-filters,
  .order-summary {
    grid-template-columns: minmax(0, 1fr);
  }
}
</style>
