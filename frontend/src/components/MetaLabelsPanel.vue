<template>
  <section class="meta-labels-panel">
    <v-card variant="outlined" class="mb-4">
      <v-card-title class="d-flex align-center flex-wrap ga-3">
        <div class="title-block">
          <div class="text-subtitle-1 font-weight-bold">
            <v-icon start color="primary">mdi-label-multiple-outline</v-icon>
            {{ t('quality_meta_title') }}
          </div>
          <div class="panel-description text-medium-emphasis mt-1">
            {{ t('quality_meta_description') }}
          </div>
        </div>
        <v-spacer />
        <v-btn
          v-if="canEditSettings"
          class="label-action"
          size="small"
          variant="outlined"
          prepend-icon="mdi-tune-variant"
          :disabled="!channelId"
          @click="openSettings"
        >
          {{ t('quality_meta_settings') }}
        </v-btn>
        <v-btn
          v-if="canSync"
          class="label-action"
          size="small"
          color="primary"
          variant="tonal"
          prepend-icon="mdi-cloud-sync-outline"
          :loading="syncing"
          :disabled="!channelId || data?.sync.status === 'syncing'"
          @click="startSync"
        >
          {{ t('quality_meta_sync') }}
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
            :label="t('quality_meta_page')"
            density="compact"
            variant="outlined"
            hide-details
            :disabled="loading && !pages.length"
            style="min-width: 240px; max-width: 360px"
          />
          <v-btn prepend-icon="mdi-refresh" variant="text" :loading="loading" @click="loadData(true)">
            {{ t('quality_meta_refresh') }}
          </v-btn>
          <v-spacer />
          <span v-if="data?.generated_at" class="text-caption text-medium-emphasis">
            {{ t('quality_meta_updated', { date: formatDateTime(data.generated_at), seconds: refreshSeconds }) }}
          </span>
        </div>
      </v-card-text>
    </v-card>

    <v-alert v-if="errorMessage" type="error" variant="tonal" class="mb-4" closable @click:close="errorMessage = ''">
      {{ errorMessage }}
    </v-alert>

    <v-alert v-if="!pages.length && !loading" type="info" variant="tonal" class="mb-4">
      {{ t('quality_meta_no_pages') }}
    </v-alert>

    <template v-if="channelId">
      <WarningBatch :warnings="labelWarnings" class="mb-4">
        <template #action="{ warning }">
          <v-btn v-if="warning.id === 'unclassified'" size="small" variant="outlined" @click="statusFilter = 'unclassified'">{{ t('quality_meta_view_unclassified') }}</v-btn>
        </template>
      </WarningBatch>

      <v-alert v-if="data?.sync.status === 'syncing'" type="info" variant="tonal" class="mb-4">
        {{ t('quality_meta_syncing_description') }}
      </v-alert>

      <v-alert
        v-if="data?.intake && data.intake.total > 0 && data.intake.captured === data.intake.total"
        :type="data.intake.captured === data.intake.total ? 'success' : 'warning'"
        variant="tonal"
        density="compact"
        class="mb-4"
      >
        {{ intakeSummary(data.intake) }}
        <span v-if="data.intake.failed"> {{ t('quality_meta_intake_failed', { count: formatNumber(data.intake.failed), error: data.intake.error }) }}</span>
      </v-alert>

      <v-progress-linear v-if="loading && data" indeterminate color="primary" class="mb-3" />

      <template v-if="loading && !data">
        <v-row class="mb-2">
          <v-col v-for="n in 4" :key="n" cols="12" sm="6" lg="3"><v-skeleton-loader type="article" /></v-col>
        </v-row>
        <v-skeleton-loader type="table" />
      </template>

      <template v-else-if="data?.counts">
        <v-row class="mb-2">
          <v-col v-for="kpi in kpis" :key="kpi.label" cols="12" sm="6" lg="3">
            <v-card variant="outlined" class="h-100 kpi-card">
              <v-card-text class="d-flex align-center ga-3">
                <v-avatar :color="kpi.color" variant="tonal" size="44"><v-icon :icon="kpi.icon" /></v-avatar>
                <div class="min-width-0">
                  <div class="text-caption text-medium-emphasis">{{ kpi.label }}</div>
                  <div class="text-h6 font-weight-bold">{{ formatNumber(kpi.value) }}</div>
                  <div class="text-caption text-medium-emphasis">{{ kpi.hint }}</div>
                </div>
              </v-card-text>
            </v-card>
          </v-col>
        </v-row>

        <div class="d-flex flex-wrap ga-2 mb-4" :aria-label="t('quality_meta_group_counts')">
          <v-chip color="success" variant="tonal" @click="statusFilter = 'qualified'">
            {{ t('quality_meta_qualified') }} <strong class="ml-1">{{ formatNumber(data.counts.qualified) }}</strong>
          </v-chip>
          <v-chip color="grey" variant="tonal" @click="statusFilter = 'unqualified'">
            {{ t('quality_meta_unqualified') }} <strong class="ml-1">{{ formatNumber(data.counts.unqualified) }}</strong>
          </v-chip>
          <v-chip color="primary" variant="tonal" @click="statusFilter = 'potential'">
            {{ t('quality_meta_potential') }} <strong class="ml-1">{{ formatNumber(data.counts.potential) }}</strong>
          </v-chip>
        </div>

        <v-card variant="outlined">
          <v-card-title class="d-flex align-center flex-wrap ga-3">
            <div class="title-block">
              <div class="text-subtitle-1 font-weight-bold">{{ t('quality_meta_conversations') }}</div>
              <div class="panel-description text-medium-emphasis mt-1">
                {{ t('quality_meta_total_description', { total: formatNumber(data.counts.total) }) }}
              </div>
            </div>
            <v-spacer />
            <v-select
              v-model="statusFilter"
              :items="statusOptions"
              item-title="title"
              item-value="value"
              :label="t('quality_meta_status')"
              density="compact"
              variant="outlined"
              hide-details
              style="min-width: 190px; max-width: 230px"
            />
            <v-text-field
              v-model="search"
              :label="t('quality_meta_search')"
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
            :no-data-text="t('quality_meta_no_rows')"
            hover
          >
            <template #item.customer_name="{ item }">
              <div class="d-flex align-center ga-2 flex-nowrap">
                <span class="font-weight-medium text-no-wrap">{{ item.customer_name || t('quality_meta_unnamed_customer') }}</span>
              </div>
            </template>
            <template #item.customer_error="{ item }">
              <v-tooltip v-if="item.error" :text="item.error" location="top">
                <template #activator="{ props }">
                  <v-icon v-bind="props" icon="mdi-close-circle-outline" color="error" size="18" tabindex="0" :aria-label="item.error" />
                </template>
              </v-tooltip>
            </template>
            <template #item.classification="{ item }">
              <v-chip :color="classificationColor(item.classification)" size="small" variant="tonal">
                {{ classificationLabel(item.classification) }}
              </v-chip>
            </template>
            <template #item.labels="{ item }">
              <div v-if="item.intake_labels.length || item.tracking_labels.length" class="d-flex flex-nowrap align-center ga-3 py-1">
                <div v-if="item.intake_labels.length" class="d-flex flex-nowrap align-center ga-1">
                  <span class="text-caption text-medium-emphasis mr-1">{{ t('quality_meta_default_labels') }}</span>
                  <v-chip v-for="label in item.intake_labels" :key="`intake-${label.id}`" size="x-small" variant="outlined" prepend-icon="mdi-lock-outline" color="grey">
                    {{ label.page_label_name }}
                  </v-chip>
                </div>
                <div v-if="item.tracking_labels.length" class="d-flex flex-nowrap align-center ga-1">
                  <span class="text-caption text-medium-emphasis mr-1">{{ t('quality_meta_current_labels') }}</span>
                  <v-chip v-for="label in item.tracking_labels" :key="`tracking-${label.id}`" size="x-small" variant="outlined" color="primary">
                    {{ label.page_label_name }}
                  </v-chip>
                </div>
              </div>
              <v-tooltip v-else-if="!item.intake_captured && item.intake_error" :text="item.intake_error" location="top">
                <template #activator="{ props }">
                  <span v-bind="props" class="text-medium-emphasis" tabindex="0">
                    {{ item.intake_error.startsWith('Meta không cho đọc nhãn') ? t('quality_meta_labels_unavailable') : t('quality_meta_labels_error') }}
                  </span>
                </template>
              </v-tooltip>
              <span v-else class="text-medium-emphasis">
                {{ item.intake_captured ? t('quality_meta_captured_no_labels') : (item.intake_error || (item.classification === 'unknown' ? t('quality_meta_pending_backfill') : t('quality_meta_no_labels'))) }}
              </span>
            </template>
            <template #item.checked_at="{ item }">
              <span>{{ item.checked_at ? formatDateTime(item.checked_at) : t('quality_meta_not_checked') }}</span>
            </template>
            <template #item.actions="{ item }">
              <v-btn size="small" variant="text" color="primary" :to="messageLink(item)">{{ t('quality_meta_open_conversation') }}</v-btn>
            </template>
          </v-data-table>
        </v-card>
      </template>
    </template>

    <v-dialog v-model="settingsDialog" max-width="760" persistent>
      <v-card>
        <v-card-title class="d-flex align-center flex-wrap ga-2">
          {{ t('quality_meta_settings_title') }}
          <v-spacer />
          <v-btn icon="mdi-close" variant="text" :disabled="savingPolicy || refreshingCatalog" @click="settingsDialog = false" />
        </v-card-title>
        <v-divider />
        <v-card-text>
          <v-alert type="info" variant="tonal" density="compact" class="mb-4">
            {{ t('quality_meta_settings_description') }}
          </v-alert>

          <v-alert v-if="intakeLabelIds.length" type="warning" variant="tonal" density="compact" class="mb-4">
            {{ t('quality_meta_intake_locked', { count: formatNumber(intakeLabelIds.length) }) }}
          </v-alert>

          <div class="d-flex align-center justify-space-between flex-wrap ga-2 mb-4">
            <v-switch v-model="policyEnabled" :label="t('quality_meta_enable_tracking')" color="primary" hide-details />
            <div class="text-right">
              <v-btn
                variant="outlined"
                prepend-icon="mdi-download-outline"
                :loading="refreshingCatalog"
                @click="refreshCatalog"
              >
                {{ t('quality_meta_fetch_catalog') }}
              </v-btn>
              <div class="text-caption text-medium-emphasis mt-1">
                {{ catalogSyncedAt ? t('quality_meta_catalog_updated', { date: formatDateTime(catalogSyncedAt) }) : t('quality_meta_catalog_never') }}
              </div>
            </div>
          </div>

          <v-alert v-if="!catalog.length" type="warning" variant="tonal" density="compact" class="mb-4">
            {{ t('quality_meta_catalog_empty') }}
          </v-alert>

          <v-select
            v-model="policySelections.qualified"
            :items="trackingCatalog"
            item-title="page_label_name"
            item-value="id"
            :label="t('quality_meta_qualified')"
            :hint="t('quality_meta_qualified_hint')"
            persistent-hint
            multiple
            chips
            closable-chips
            variant="outlined"
            class="mb-3"
          />
          <v-select
            v-model="policySelections.unqualified"
            :items="trackingCatalog"
            item-title="page_label_name"
            item-value="id"
            :label="t('quality_meta_unqualified')"
            :hint="t('quality_meta_unqualified_hint')"
            persistent-hint
            multiple
            chips
            closable-chips
            variant="outlined"
            class="mb-3"
          />
          <v-select
            v-model="policySelections.potential"
            :items="trackingCatalog"
            item-title="page_label_name"
            item-value="id"
            :label="t('quality_meta_potential')"
            :hint="t('quality_meta_potential_hint')"
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
          <v-btn variant="text" :disabled="savingPolicy" @click="settingsDialog = false">{{ t('quality_meta_cancel') }}</v-btn>
          <v-btn color="primary" :loading="savingPolicy" :disabled="Boolean(mappingValidation)" @click="savePolicy">{{ t('quality_meta_save') }}</v-btn>
        </v-card-actions>
      </v-card>
    </v-dialog>

    <v-snackbar v-model="snackbar" :color="snackColor" timeout="3500">{{ snackText }}</v-snackbar>
  </section>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, reactive, ref, watch } from 'vue'
import { useRoute } from 'vue-router'
import { useI18n } from 'vue-i18n'
import api from '../api'
import WarningBatch from './WarningBatch.vue'
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
  intake_labels: MetaLabel[]
  intake_captured: boolean
  intake_error: string
  tracking_labels: MetaLabel[]
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
  intake_label_ids: string[]
  catalog_synced_at: string | null
  sync: {
    status: SyncStatus
    started_at: string | null
    finished_at: string | null
    error: string
  }
  intake: {
    total: number
    captured: number
    with_labels: number
    failed: number
    error: string
  }
  freshness_minutes: number
  counts: MetaLabelCounts | null
  rows: ClassificationRow[]
}

const { t, locale } = useI18n()
const formatLocale = computed(() => locale.value === 'en' ? 'en-US' : 'vi-VN')
const route = useRoute()
const authStore = useAuthStore()
const tenantId = computed(() => route.params.tenantId as string)
const canEditSettings = computed(() => authStore.canEdit('settings'))
const canSync = computed(() => authStore.canEdit('messages'))

const pages = ref<Page[]>([])
const channelId = ref('')
const data = ref<LabelsReport | null>(null)
const labelWarnings = computed(() => {
  const report = data.value
  if (!report) return []
  const warnings: { id: string; title: string; detail: string }[] = []
  if (!report.enabled) {
    warnings.push({ id: 'disabled', title: t('quality_meta_disabled_title'), detail: t('quality_meta_disabled_detail') })
  } else if (report.sync.status === 'error' || report.sync.status === 'partial') {
    warnings.push({ id: 'sync', title: syncStatusLabel(report.sync.status), detail: `${report.sync.error || t('quality_meta_sync_partial_detail')} ${t('quality_meta_stale_detail', { minutes: formatNumber(report.freshness_minutes) })}` })
  }
  if (report.intake?.total > 0 && report.intake.captured !== report.intake.total) {
    warnings.push({ id: 'intake', title: t('quality_meta_intake_title'), detail: `${intakeSummary(report.intake)}${report.intake.failed ? ` ${t('quality_meta_intake_failed', { count: formatNumber(report.intake.failed), error: report.intake.error })}` : ''}` })
  }
  if (report.counts?.unclassified) {
    warnings.push({ id: 'unclassified', title: t('quality_meta_unclassified_title', { count: formatNumber(report.counts.unclassified) }), detail: t('quality_meta_unclassified_detail') })
  }
  return warnings
})
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
  title: `${page.name}${page.is_active ? '' : t('quality_meta_inactive_suffix')}`,
  value: page.id,
})))

const refreshSeconds = computed(() => data.value?.sync.status === 'syncing' ? 5 : 60)
const mappingRules = computed(() => buildMetaLabelRules(policySelections))
const intakeLabelIds = computed(() => data.value?.intake_label_ids || [])
const intakeLabelIdSet = computed(() => new Set(intakeLabelIds.value))
const trackingCatalog = computed(() => catalog.value.filter(label => !intakeLabelIdSet.value.has(label.id)))
const validationMessageKeys: Record<string, string> = {
  "Chọn ít nhất một nhãn để bật theo dõi.": 'quality_meta_validation_select',
  "Có nhãn không còn tồn tại trong danh mục Meta.": 'quality_meta_validation_missing',
  "Nhãn mặc định đã có lúc tiếp nhận hội thoại không được dùng làm nhãn theo dõi.": 'quality_meta_validation_intake',
  "Một nhãn chỉ được gán cho một nhóm trạng thái.": 'quality_meta_validation_duplicate',
}
const mappingValidation = computed(() => {
  const message = validateMetaLabelRules(
    policyEnabled.value,
    mappingRules.value,
    new Set(catalog.value.map(label => label.id)),
    intakeLabelIdSet.value,
  )
  return message ? t(validationMessageKeys[message] || message) : ''
})

const kpis = computed(() => {
  const counts = data.value?.counts || null
  return [
    { label: t('quality_meta_unclassified'), value: counts?.unclassified || 0, hint: t('quality_meta_unclassified_hint'), icon: 'mdi-label-off-outline', color: 'warning' },
    { label: t('quality_meta_classified'), value: classifiedCount(counts), hint: t('quality_meta_classified_hint', { qualified: formatNumber(counts?.qualified || 0), unqualified: formatNumber(counts?.unqualified || 0), potential: formatNumber(counts?.potential || 0) }), icon: 'mdi-check-circle-outline', color: 'success' },
    { label: t('quality_meta_unknown'), value: counts?.unknown || 0, hint: t('quality_meta_unknown_hint'), icon: 'mdi-help-circle-outline', color: 'grey' },
    { label: t('quality_meta_conflict'), value: counts?.conflict || 0, hint: t('quality_meta_conflict_hint'), icon: 'mdi-label-multiple-outline', color: 'error' },
  ]
})

const statusOptions = computed(() => [
  { title: t('quality_meta_all_statuses'), value: 'all' },
  { title: t('quality_meta_unclassified'), value: 'unclassified' },
  { title: t('quality_meta_qualified'), value: 'qualified' },
  { title: t('quality_meta_unqualified'), value: 'unqualified' },
  { title: t('quality_meta_potential'), value: 'potential' },
  { title: t('quality_meta_conflict'), value: 'conflict' },
  { title: t('quality_meta_unknown'), value: 'unknown' },
])

const headers = computed(() => [
  { title: t('quality_meta_customer'), key: 'customer_name', sortable: true },
  { title: '', key: 'customer_error', width: '42px', sortable: false },
  { title: t('quality_meta_classification'), key: 'classification', sortable: true },
  { title: t('quality_meta_meta_labels'), key: 'labels', sortable: false },
  { title: t('quality_meta_checked_at'), key: 'checked_at', sortable: true },
  { title: '', key: 'actions', sortable: false, align: 'end' as const },
])

const filteredRows = computed(() => {
  const term = search.value.trim().toLocaleLowerCase(formatLocale.value)
  return (data.value?.rows || []).filter(row => {
    if (statusFilter.value !== 'all' && row.classification !== statusFilter.value) return false
    if (!term) return true
    const labels = [...row.intake_labels, ...row.tracking_labels].map(label => label.page_label_name).join(' ')
    return `${row.customer_name} ${labels}`.toLocaleLowerCase(formatLocale.value).includes(term)
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
    errorMessage.value = error?.response?.data?.error || t('quality_meta_load_error')
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
    showSnack(t('quality_meta_catalog_success'), 'success')
  } catch (error: any) {
    if (disposed || sequence !== actionSequence.catalog || tenantId.value !== targetTenant || channelId.value !== targetChannel) return
    showSnack(error?.response?.data?.error || t('quality_meta_catalog_error'), 'error')
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
    showSnack(t('quality_meta_save_success'), 'success')
    await loadData(true)
  } catch (error: any) {
    if (disposed || sequence !== actionSequence.policy || tenantId.value !== targetTenant || channelId.value !== targetChannel) return
    showSnack(error?.response?.data?.error || t('quality_meta_save_error'), 'error')
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
    const { data: response } = await api.post(`/tenants/${targetTenant}/messenger-labels/${targetChannel}/sync`)
    if (disposed || sequence !== actionSequence.sync || tenantId.value !== targetTenant || channelId.value !== targetChannel) return
    if (data.value && response.status === 'syncing') {
      data.value.sync.status = 'syncing'
      data.value.counts = null
      data.value.rows = []
    }
    showSnack(response.status === 'capturing_intake' ? t('quality_meta_backfill_started') : t('quality_meta_sync_started'), 'success')
    if (response.status === 'capturing_intake') {
      window.setTimeout(() => loadData(true), 3000)
    }
    scheduleRefresh()
  } catch (error: any) {
    if (disposed || sequence !== actionSequence.sync || tenantId.value !== targetTenant || channelId.value !== targetChannel) return
    showSnack(error?.response?.data?.error || (error?.response?.status === 409 ? t('quality_meta_sync_busy') : t('quality_meta_sync_error')), 'error')
  } finally {
    if (sequence === actionSequence.sync) syncing.value = false
  }
}

function messageLink(row: ClassificationRow) {
  return { name: 'messages', params: { tenantId: tenantId.value }, query: { conv: row.conversation_id, channel_id: row.channel_id } }
}

function classificationLabel(status: Classification) {
  return ({
    unclassified: t('quality_meta_unclassified'),
    qualified: t('quality_meta_qualified'),
    unqualified: t('quality_meta_unqualified'),
    potential: t('quality_meta_potential'),
    conflict: t('quality_meta_conflict'),
    unknown: t('quality_meta_unknown'),
  } as Record<Classification, string>)[status]
}

function classificationColor(status: Classification) {
  return ({ unclassified: 'warning', qualified: 'success', unqualified: 'grey', potential: 'primary', conflict: 'error', unknown: 'grey' } as Record<Classification, string>)[status]
}

function syncStatusLabel(status: SyncStatus) {
  return ({ never: t('quality_meta_sync_never'), syncing: t('quality_meta_sync_running'), success: t('quality_meta_sync_success'), partial: t('quality_meta_sync_partial'), error: t('quality_meta_sync_failed') } as Record<SyncStatus, string>)[status]
}

function formatNumber(value: number) {
  return value.toLocaleString(formatLocale.value)
}

function intakeSummary(intake: LabelsReport['intake']) {
  return t('quality_meta_intake_summary', { captured: formatNumber(intake.captured), total: formatNumber(intake.total), withLabels: formatNumber(intake.with_labels) })
}

function formatDateTime(value: string) {
  const date = new Date(value)
  return Number.isNaN(date.getTime()) ? '—' : date.toLocaleString(formatLocale.value, { dateStyle: 'short', timeStyle: 'short', timeZone: 'Asia/Ho_Chi_Minh' })
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

.label-action {
  font-size: 13px;
  font-weight: 500;
  letter-spacing: normal;
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

.meta-labels-panel :deep(.v-data-table__td) {
  white-space: nowrap;
}

@media (max-width: 600px) {
  .meta-labels-panel :deep(.v-data-table__td),
  .meta-labels-panel :deep(.v-data-table__th) {
    white-space: nowrap;
  }
}
</style>
