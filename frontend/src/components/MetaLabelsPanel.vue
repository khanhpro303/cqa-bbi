<template>
  <section class="meta-labels-panel">
    <v-card variant="outlined" class="mb-4">
      <v-card-title class="d-flex align-center flex-wrap ga-3">
        <div class="title-block">
          <div class="text-subtitle-1 font-weight-bold">
            <v-icon start color="primary">mdi-label-multiple-outline</v-icon>
            Phân loại thủ công trên Meta Inbox
          </div>
          <div class="panel-description text-medium-emphasis mt-1">
            Đếm hội thoại theo nhãn mà nhân viên gắn trực tiếp trong Meta Inbox. Kết quả này không dùng AI và không đọc được mục giai đoạn khách hàng có sẵn của Meta.
          </div>
        </div>
        <v-spacer />
        <v-btn
          v-if="canEditSettings"
          variant="outlined"
          prepend-icon="mdi-tune-variant"
          :disabled="!channelId"
          @click="openSettings"
        >
          Cấu hình nhãn
        </v-btn>
        <v-btn
          v-if="canSync"
          color="primary"
          variant="tonal"
          prepend-icon="mdi-cloud-sync-outline"
          :loading="syncing"
          :disabled="!channelId || data?.sync.status === 'syncing'"
          @click="startSync"
        >
          Đồng bộ nhãn
        </v-btn>
      </v-card-title>
      <v-divider />
      <v-card-text>
        <div class="d-flex flex-wrap align-center ga-3">
          <v-select
            v-model="channelId"
            :items="pageOptions"
            item-title="title"
            item-value="value"
            label="Fanpage"
            density="compact"
            variant="outlined"
            hide-details
            :disabled="loading && !pages.length"
            style="min-width: 240px; max-width: 360px"
          />
          <v-btn prepend-icon="mdi-refresh" variant="text" :loading="loading" @click="loadData(true)">
            Làm mới số liệu
          </v-btn>
          <v-spacer />
          <span v-if="data?.generated_at" class="text-caption text-medium-emphasis">
            Cập nhật {{ formatDateTime(data.generated_at) }} · tự làm mới {{ refreshSeconds }} giây
          </span>
        </div>
      </v-card-text>
    </v-card>

    <v-alert v-if="errorMessage" type="error" variant="tonal" class="mb-4" closable @click:close="errorMessage = ''">
      {{ errorMessage }}
    </v-alert>

    <v-alert v-if="!pages.length && !loading" type="info" variant="tonal" class="mb-4">
      Chưa có Fanpage Facebook để theo dõi nhãn.
    </v-alert>

    <template v-if="channelId">
      <v-alert v-if="data && !data.enabled" type="warning" variant="tonal" class="mb-4" border="start">
        Theo dõi nhãn chưa được bật cho Fanpage này. Hãy đồng bộ danh mục nhãn, sau đó ánh xạ nhãn vào ba nhóm trạng thái.
      </v-alert>

      <v-alert v-else-if="data?.sync.status === 'error' || data?.sync.status === 'partial'" type="warning" variant="tonal" class="mb-4" border="start">
        <strong>{{ syncStatusLabel(data.sync.status) }}.</strong>
        {{ data.sync.error || 'Một phần hội thoại chưa lấy được nhãn từ Meta.' }}
        Các hội thoại lỗi hoặc dữ liệu cũ hơn {{ data.freshness_minutes }} phút được tính riêng vào “Chưa xác định”.
      </v-alert>

      <v-alert v-if="data?.sync.status === 'syncing'" type="info" variant="tonal" class="mb-4">
        Đang đọc nhãn từ Meta. Số phân loại và cảnh báo sẽ được tính khi lượt đồng bộ hoàn tất; hội thoại đang chờ được tính vào “Chưa xác định”.
      </v-alert>

      <v-progress-linear v-if="loading && data" indeterminate color="primary" class="mb-3" />

      <template v-if="loading && !data">
        <v-row class="mb-2">
          <v-col v-for="n in 4" :key="n" cols="12" sm="6" lg="3"><v-skeleton-loader type="article" /></v-col>
        </v-row>
        <v-skeleton-loader type="table" />
      </template>

      <template v-else-if="data?.counts">
        <v-alert v-if="data.counts.unclassified" type="warning" variant="tonal" border="start" class="mb-4">
          <div class="d-flex align-center justify-space-between flex-wrap ga-3">
            <div>
              <strong>{{ data.counts.unclassified }} hội thoại chưa có nhãn phân loại</strong>
              <div class="text-body-2 mt-1">Nhân viên cần kiểm tra và gắn nhãn trong Meta Inbox. Cảnh báo chỉ dựa trên dữ liệu nhãn đã đọc thành công.</div>
            </div>
            <v-btn size="small" variant="outlined" @click="statusFilter = 'unclassified'">Xem chưa phân loại</v-btn>
          </div>
        </v-alert>
        <v-row class="mb-2">
          <v-col v-for="kpi in kpis" :key="kpi.label" cols="12" sm="6" lg="3">
            <v-card variant="outlined" class="h-100 kpi-card">
              <v-card-text class="d-flex align-center ga-3">
                <v-avatar :color="kpi.color" variant="tonal" size="44"><v-icon :icon="kpi.icon" /></v-avatar>
                <div class="min-width-0">
                  <div class="text-caption text-medium-emphasis">{{ kpi.label }}</div>
                  <div class="text-h6 font-weight-bold">{{ kpi.value.toLocaleString('vi-VN') }}</div>
                  <div class="text-caption text-medium-emphasis">{{ kpi.hint }}</div>
                </div>
              </v-card-text>
            </v-card>
          </v-col>
        </v-row>

        <div class="d-flex flex-wrap ga-2 mb-4" aria-label="Số lượng theo nhóm nhãn">
          <v-chip color="success" variant="tonal" @click="statusFilter = 'qualified'">
            Phù hợp <strong class="ml-1">{{ data.counts.qualified.toLocaleString('vi-VN') }}</strong>
          </v-chip>
          <v-chip color="grey" variant="tonal" @click="statusFilter = 'unqualified'">
            Chưa phù hợp <strong class="ml-1">{{ data.counts.unqualified.toLocaleString('vi-VN') }}</strong>
          </v-chip>
          <v-chip color="primary" variant="tonal" @click="statusFilter = 'potential'">
            Tiềm năng <strong class="ml-1">{{ data.counts.potential.toLocaleString('vi-VN') }}</strong>
          </v-chip>
        </div>

        <v-card variant="outlined">
          <v-card-title class="d-flex align-center flex-wrap ga-3">
            <div class="title-block">
              <div class="text-subtitle-1 font-weight-bold">Hội thoại theo nhãn Meta</div>
              <div class="text-caption text-medium-emphasis mt-1">
                Tổng {{ data.counts.total.toLocaleString('vi-VN') }} hội thoại đã lưu cục bộ của Fanpage. Nhãn chỉ thay đổi sau lần đồng bộ gần nhất.
              </div>
            </div>
            <v-spacer />
            <v-select
              v-model="statusFilter"
              :items="statusOptions"
              item-title="title"
              item-value="value"
              label="Trạng thái"
              density="compact"
              variant="outlined"
              hide-details
              style="min-width: 190px; max-width: 230px"
            />
            <v-text-field
              v-model="search"
              label="Tìm khách hàng hoặc nhãn"
              prepend-inner-icon="mdi-magnify"
              clearable
              density="compact"
              variant="outlined"
              hide-details
              style="min-width: 220px; max-width: 300px"
            />
          </v-card-title>
          <v-divider />
          <v-data-table
            :headers="headers"
            :items="filteredRows"
            item-value="conversation_id"
            :items-per-page="20"
            no-data-text="Không có hội thoại phù hợp"
            hover
          >
            <template #item.customer_name="{ item }">
              <div class="font-weight-medium">{{ item.customer_name || 'Khách hàng chưa có tên' }}</div>
              <div v-if="item.error" class="text-caption text-error mt-1">{{ item.error }}</div>
            </template>
            <template #item.classification="{ item }">
              <v-chip :color="classificationColor(item.classification)" size="small" variant="tonal">
                {{ classificationLabel(item.classification) }}
              </v-chip>
            </template>
            <template #item.labels="{ item }">
              <div v-if="item.labels.length" class="d-flex flex-wrap ga-1 py-1">
                <v-chip v-for="label in item.labels" :key="label.id" size="x-small" variant="outlined">
                  {{ label.page_label_name }}
                </v-chip>
              </div>
              <span v-else class="text-medium-emphasis">{{ item.classification === 'unknown' ? 'Chưa xác định nhãn' : 'Không có nhãn' }}</span>
            </template>
            <template #item.checked_at="{ item }">
              <span>{{ item.checked_at ? formatDateTime(item.checked_at) : 'Chưa kiểm tra' }}</span>
            </template>
            <template #item.actions="{ item }">
              <v-btn size="small" variant="text" color="primary" :to="messageLink(item)">Mở hội thoại</v-btn>
            </template>
          </v-data-table>
        </v-card>
      </template>
    </template>

    <v-dialog v-model="settingsDialog" max-width="760" persistent>
      <v-card>
        <v-card-title class="d-flex align-center flex-wrap ga-2">
          Cấu hình nhãn Meta Inbox
          <v-spacer />
          <v-btn icon="mdi-close" variant="text" :disabled="savingPolicy || refreshingCatalog" @click="settingsDialog = false" />
        </v-card-title>
        <v-divider />
        <v-card-text>
          <v-alert type="info" variant="tonal" density="compact" class="mb-4">
            Nhân viên tiếp tục gắn nhãn trong Meta Inbox. Hệ thống chỉ đọc nhãn và lưu bản chụp để đếm; không tự gắn, sửa hoặc xóa nhãn trên Meta.
          </v-alert>

          <div class="d-flex align-center justify-space-between flex-wrap ga-2 mb-4">
            <v-switch v-model="policyEnabled" label="Bật theo dõi phân loại bằng nhãn" color="primary" hide-details />
            <div class="text-right">
              <v-btn
                variant="outlined"
                prepend-icon="mdi-download-outline"
                :loading="refreshingCatalog"
                @click="refreshCatalog"
              >
                Lấy danh mục nhãn từ Meta
              </v-btn>
              <div class="text-caption text-medium-emphasis mt-1">
                {{ catalogSyncedAt ? `Danh mục cập nhật ${formatDateTime(catalogSyncedAt)}` : 'Chưa lấy danh mục nhãn' }}
              </div>
            </div>
          </div>

          <v-alert v-if="!catalog.length" type="warning" variant="tonal" density="compact" class="mb-4">
            Chưa có danh mục nhãn. Bấm “Lấy danh mục nhãn từ Meta” trước khi bật theo dõi.
          </v-alert>

          <v-select
            v-model="policySelections.qualified"
            :items="catalog"
            item-title="page_label_name"
            item-value="id"
            label="Phù hợp"
            hint="Khách đáp ứng tiêu chí hiện tại"
            persistent-hint
            multiple
            chips
            closable-chips
            variant="outlined"
            class="mb-3"
          />
          <v-select
            v-model="policySelections.unqualified"
            :items="catalog"
            item-title="page_label_name"
            item-value="id"
            label="Chưa phù hợp"
            hint="Khách chưa đáp ứng tiêu chí"
            persistent-hint
            multiple
            chips
            closable-chips
            variant="outlined"
            class="mb-3"
          />
          <v-select
            v-model="policySelections.potential"
            :items="catalog"
            item-title="page_label_name"
            item-value="id"
            label="Tiềm năng"
            hint="Khách cần tiếp tục chăm sóc"
            persistent-hint
            multiple
            chips
            closable-chips
            variant="outlined"
          />

          <v-alert v-if="mappingValidation" type="error" variant="tonal" density="compact" class="mt-3">
            {{ mappingValidation }}
          </v-alert>
        </v-card-text>
        <v-card-actions class="px-6 pb-5">
          <v-spacer />
          <v-btn variant="text" :disabled="savingPolicy" @click="settingsDialog = false">Hủy</v-btn>
          <v-btn color="primary" :loading="savingPolicy" :disabled="Boolean(mappingValidation)" @click="savePolicy">Lưu cấu hình</v-btn>
        </v-card-actions>
      </v-card>
    </v-dialog>

    <v-snackbar v-model="snackbar" :color="snackColor" timeout="3500">{{ snackText }}</v-snackbar>
  </section>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, reactive, ref, watch } from 'vue'
import { useRoute } from 'vue-router'
import api from '../api'
import { useAuthStore } from '../stores/auth'
import {
  buildMetaLabelRules,
  classifiedCount,
  validateMetaLabelRules,
  type MetaLabelCategory,
  type MetaLabelCounts,
  type MetaLabelRule,
} from '../utils/meta-labels'

interface Page {
  id: string
  name: string
  is_active: boolean
}

interface MetaLabel {
  id: string
  page_label_name: string
}

type Classification = 'unclassified' | MetaLabelCategory | 'conflict' | 'unknown'
type SyncStatus = 'never' | 'syncing' | 'success' | 'partial' | 'error'

interface ClassificationRow {
  conversation_id: string
  customer_name: string
  channel_id: string
  classification: Classification
  labels: MetaLabel[]
  checked_at: string | null
  error: string
}

interface LabelsReport {
  generated_at: string
  channel_id: string
  pages: Page[]
  enabled: boolean
  rules: MetaLabelRule[]
  catalog: MetaLabel[]
  catalog_synced_at: string | null
  sync: {
    status: SyncStatus
    started_at: string | null
    finished_at: string | null
    error: string
  }
  freshness_minutes: number
  counts: MetaLabelCounts | null
  rows: ClassificationRow[]
}

const route = useRoute()
const authStore = useAuthStore()
const tenantId = computed(() => route.params.tenantId as string)
const canEditSettings = computed(() => authStore.canEdit('settings'))
const canSync = computed(() => authStore.canEdit('messages'))

const pages = ref<Page[]>([])
const channelId = ref('')
const data = ref<LabelsReport | null>(null)
const loading = ref(false)
const syncing = ref(false)
const errorMessage = ref('')
const statusFilter = ref('all')
const search = ref('')
const settingsDialog = ref(false)
const policyEnabled = ref(false)
const policySelections = reactive<Record<MetaLabelCategory, string[]>>({ qualified: [], unqualified: [], potential: [] })
const catalog = ref<MetaLabel[]>([])
const catalogSyncedAt = ref<string | null>(null)
const refreshingCatalog = ref(false)
const savingPolicy = ref(false)
const snackbar = ref(false)
const snackText = ref('')
const snackColor = ref('success')
let refreshTimer: number | undefined
let activeController: AbortController | null = null
let requestSequence = 0
let disposed = false
const actionSequence = { catalog: 0, policy: 0, sync: 0 }

function invalidateActions() {
  actionSequence.catalog++
  actionSequence.policy++
  actionSequence.sync++
  refreshingCatalog.value = false
  savingPolicy.value = false
  syncing.value = false
}

const pageOptions = computed(() => pages.value.map(page => ({
  title: `${page.name}${page.is_active ? '' : ' (ngừng hoạt động)'}`,
  value: page.id,
})))

const refreshSeconds = computed(() => data.value?.sync.status === 'syncing' ? 5 : 60)
const mappingRules = computed(() => buildMetaLabelRules(policySelections))
const mappingValidation = computed(() => validateMetaLabelRules(
  policyEnabled.value,
  mappingRules.value,
  new Set(catalog.value.map(label => label.id)),
))

const kpis = computed(() => {
  const counts = data.value?.counts || null
  return [
    { label: 'Chưa phân loại', value: counts?.unclassified || 0, hint: 'Không có nhãn đã ánh xạ', icon: 'mdi-label-off-outline', color: 'warning' },
    { label: 'Đã phân loại', value: classifiedCount(counts), hint: `${counts?.qualified || 0} phù hợp · ${counts?.unqualified || 0} chưa phù hợp · ${counts?.potential || 0} tiềm năng`, icon: 'mdi-label-check-outline', color: 'success' },
    { label: 'Chưa xác định', value: counts?.unknown || 0, hint: 'Chưa bật, lỗi hoặc dữ liệu cũ', icon: 'mdi-help-circle-outline', color: 'grey' },
    { label: 'Xung đột nhãn', value: counts?.conflict || 0, hint: 'Có nhãn thuộc nhiều nhóm', icon: 'mdi-label-multiple-outline', color: 'error' },
  ]
})

const statusOptions = [
  { title: 'Tất cả trạng thái', value: 'all' },
  { title: 'Chưa phân loại', value: 'unclassified' },
  { title: 'Phù hợp', value: 'qualified' },
  { title: 'Chưa phù hợp', value: 'unqualified' },
  { title: 'Tiềm năng', value: 'potential' },
  { title: 'Xung đột nhãn', value: 'conflict' },
  { title: 'Chưa xác định', value: 'unknown' },
]

const headers = [
  { title: 'Khách hàng', key: 'customer_name', sortable: true },
  { title: 'Phân loại', key: 'classification', sortable: true },
  { title: 'Nhãn trên Meta', key: 'labels', sortable: false },
  { title: 'Kiểm tra lúc', key: 'checked_at', sortable: true },
  { title: '', key: 'actions', sortable: false, align: 'end' as const },
]

const filteredRows = computed(() => {
  const term = search.value.trim().toLocaleLowerCase('vi')
  return (data.value?.rows || []).filter(row => {
    if (statusFilter.value !== 'all' && row.classification !== statusFilter.value) return false
    if (!term) return true
    return `${row.customer_name} ${row.labels.map(label => label.page_label_name).join(' ')}`.toLocaleLowerCase('vi').includes(term)
  })
})

function scheduleRefresh() {
  if (disposed) return
  if (refreshTimer) window.clearTimeout(refreshTimer)
  refreshTimer = window.setTimeout(() => loadData(false), refreshSeconds.value * 1000)
}

async function loadData(force = false) {
  if (loading.value && !force) return
  if (force) activeController?.abort()
  const controller = new AbortController()
  activeController = controller
  const sequence = ++requestSequence
  loading.value = true
  errorMessage.value = ''
  try {
    const { data: response } = await api.get<LabelsReport>(`/tenants/${tenantId.value}/messenger-labels`, {
      params: { channel_id: channelId.value || undefined },
      signal: controller.signal,
    })
    if (sequence !== requestSequence) return
    pages.value = response.pages || []
    if (!channelId.value && pages.value.length) {
      channelId.value = pages.value[0].id
      return
    }
    if (channelId.value && !pages.value.some(page => page.id === channelId.value)) {
      channelId.value = pages.value[0]?.id || ''
      data.value = null
      return
    }
    data.value = response
  } catch (error: any) {
    if (error?.code === 'ERR_CANCELED' || sequence !== requestSequence) return
    data.value = null
    if (Array.isArray(error?.response?.data?.pages)) pages.value = error.response.data.pages
    if (error?.response?.status === 422 && error.response.data.report?.channel_id === channelId.value) {
      data.value = error.response.data.report
    }
    errorMessage.value = error?.response?.data?.error || 'Không tải được dữ liệu nhãn Messenger.'
  } finally {
    if (sequence === requestSequence) {
      loading.value = false
      scheduleRefresh()
    }
  }
}

function openSettings() {
  if (!data.value) return
  catalog.value = [...data.value.catalog]
  catalogSyncedAt.value = data.value.catalog_synced_at
  policyEnabled.value = data.value.enabled
  policySelections.qualified = data.value.rules.filter(rule => rule.category === 'qualified').map(rule => rule.label_id)
  policySelections.unqualified = data.value.rules.filter(rule => rule.category === 'unqualified').map(rule => rule.label_id)
  policySelections.potential = data.value.rules.filter(rule => rule.category === 'potential').map(rule => rule.label_id)
  settingsDialog.value = true
}

async function refreshCatalog() {
  if (!channelId.value) return
  const sequence = ++actionSequence.catalog
  refreshingCatalog.value = true
  const targetTenant = tenantId.value
  const targetChannel = channelId.value
  try {
    const { data: response } = await api.post(`/tenants/${targetTenant}/messenger-labels/${targetChannel}/catalog`)
    if (disposed || sequence !== actionSequence.catalog || tenantId.value !== targetTenant || channelId.value !== targetChannel) return
    catalog.value = response.catalog || []
    catalogSyncedAt.value = response.catalog_synced_at || null
    showSnack('Đã lấy danh mục nhãn từ Meta', 'success')
  } catch (error: any) {
    if (disposed || sequence !== actionSequence.catalog || tenantId.value !== targetTenant || channelId.value !== targetChannel) return
    showSnack(error?.response?.data?.error || 'Không lấy được danh mục nhãn từ Meta', 'error')
  } finally {
    if (sequence === actionSequence.catalog) refreshingCatalog.value = false
  }
}

async function savePolicy() {
  if (!channelId.value || mappingValidation.value) return
  const sequence = ++actionSequence.policy
  savingPolicy.value = true
  const targetTenant = tenantId.value
  const targetChannel = channelId.value
  try {
    await api.put(`/tenants/${targetTenant}/messenger-labels/${targetChannel}/policy`, {
      enabled: policyEnabled.value,
      rules: mappingRules.value,
    })
    if (disposed || sequence !== actionSequence.policy || tenantId.value !== targetTenant || channelId.value !== targetChannel) return
    settingsDialog.value = false
    showSnack('Đã lưu cấu hình nhãn', 'success')
    await loadData(true)
  } catch (error: any) {
    if (disposed || sequence !== actionSequence.policy || tenantId.value !== targetTenant || channelId.value !== targetChannel) return
    showSnack(error?.response?.data?.error || 'Không lưu được cấu hình nhãn', 'error')
  } finally {
    if (sequence === actionSequence.policy) savingPolicy.value = false
  }
}

async function startSync() {
  if (!channelId.value) return
  const sequence = ++actionSequence.sync
  syncing.value = true
  const targetTenant = tenantId.value
  const targetChannel = channelId.value
  try {
    await api.post(`/tenants/${targetTenant}/messenger-labels/${targetChannel}/sync`)
    if (disposed || sequence !== actionSequence.sync || tenantId.value !== targetTenant || channelId.value !== targetChannel) return
    if (data.value) {
      data.value.sync.status = 'syncing'
      data.value.counts = null
      data.value.rows = []
    }
    showSnack('Đã bắt đầu đồng bộ nhãn', 'success')
    scheduleRefresh()
  } catch (error: any) {
    if (disposed || sequence !== actionSequence.sync || tenantId.value !== targetTenant || channelId.value !== targetChannel) return
    showSnack(error?.response?.data?.error || (error?.response?.status === 409 ? 'Fanpage đang đồng bộ nhãn' : 'Không thể bắt đầu đồng bộ nhãn'), 'error')
  } finally {
    if (sequence === actionSequence.sync) syncing.value = false
  }
}

function messageLink(row: ClassificationRow) {
  return { name: 'messages', params: { tenantId: tenantId.value }, query: { conv: row.conversation_id, channel_id: row.channel_id } }
}

function classificationLabel(status: Classification) {
  return ({
    unclassified: 'Chưa phân loại',
    qualified: 'Phù hợp',
    unqualified: 'Chưa phù hợp',
    potential: 'Tiềm năng',
    conflict: 'Xung đột nhãn',
    unknown: 'Chưa xác định',
  } as Record<Classification, string>)[status]
}

function classificationColor(status: Classification) {
  return ({ unclassified: 'warning', qualified: 'success', unqualified: 'grey', potential: 'primary', conflict: 'error', unknown: 'grey' } as Record<Classification, string>)[status]
}

function syncStatusLabel(status: SyncStatus) {
  return ({ never: 'Chưa từng đồng bộ', syncing: 'Đang đồng bộ', success: 'Đồng bộ thành công', partial: 'Đồng bộ một phần', error: 'Đồng bộ lỗi' } as Record<SyncStatus, string>)[status]
}

function formatDateTime(value: string) {
  const date = new Date(value)
  return Number.isNaN(date.getTime()) ? '—' : date.toLocaleString('vi-VN', { dateStyle: 'short', timeStyle: 'short', timeZone: 'Asia/Ho_Chi_Minh' })
}

function showSnack(message: string, color: string) {
  snackText.value = message
  snackColor.value = color
  snackbar.value = true
}

watch(channelId, () => {
  invalidateActions()
  requestSequence++
  activeController?.abort()
  data.value = null
  statusFilter.value = 'all'
  search.value = ''
  settingsDialog.value = false
  if (channelId.value) loadData(true)
})

watch(tenantId, () => {
  invalidateActions()
  requestSequence++
  activeController?.abort()
  if (refreshTimer) window.clearTimeout(refreshTimer)
  pages.value = []
  channelId.value = ''
  data.value = null
  settingsDialog.value = false
  loadData(true)
})

onMounted(() => loadData(true))

onBeforeUnmount(() => {
  disposed = true
  invalidateActions()
  requestSequence++
  activeController?.abort()
  if (refreshTimer) window.clearTimeout(refreshTimer)
})
</script>

<style scoped>
.meta-labels-panel {
  min-width: 0;
}

.title-block,
.min-width-0 {
  min-width: 0;
}

.panel-description {
  font-size: 13px;
  font-weight: 400;
  line-height: 1.5;
  letter-spacing: normal;
  white-space: normal;
  overflow-wrap: anywhere;
}

.kpi-card {
  min-height: 112px;
}

.meta-labels-panel :deep(.v-card-title) {
  white-space: normal;
  overflow-wrap: anywhere;
}

.meta-labels-panel :deep(.v-chip) {
  max-width: 100%;
}

.meta-labels-panel :deep(.v-chip__content) {
  overflow: hidden;
  text-overflow: ellipsis;
}

@media (max-width: 600px) {
  .meta-labels-panel :deep(.v-data-table__td),
  .meta-labels-panel :deep(.v-data-table__th) {
    white-space: nowrap;
  }
}
</style>
