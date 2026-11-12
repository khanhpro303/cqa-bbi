<template>
  <section class="meta-labels-panel">
    <v-card variant="outlined" class="mb-4">
      <v-card-title class="label-panel-header">
        <div class="title-block">
          <div class="text-subtitle-1 font-weight-bold">
            <v-icon start color="primary">mdi-label-multiple-outline</v-icon>
            {{ st('quality_meta_title') }}
          </div>
          <div class="panel-description text-medium-emphasis mt-1">
            {{ st('quality_meta_description') }}
          </div>
        </div>
        <v-spacer />
        <div class="label-toolbar">
          <v-btn-toggle
            v-model="source"
            class="source-toggle"
            density="compact"
            mandatory
            :aria-label="t('quality_label_source')"
          >
            <v-btn
              value="meta"
              class="source-option source-option--meta"
              :class="{ 'source-option--active': source === 'meta' }"
              size="small"
              :variant="source === 'meta' ? 'tonal' : 'text'"
            >
              <img class="source-logo" src="/assets/brand/meta-mark.svg" alt="" aria-hidden="true">
              <span>Meta</span>
            </v-btn>
            <v-btn
              value="pancake"
              class="source-option source-option--pancake"
              :class="{ 'source-option--active': source === 'pancake' }"
              size="small"
              :variant="source === 'pancake' ? 'tonal' : 'text'"
            >
              <img class="source-logo" src="/assets/brand/pancake-mark.svg" alt="" aria-hidden="true">
              <span>Pancake</span>
            </v-btn>
          </v-btn-toggle>
          <v-btn
            v-if="canEditSettings"
            class="label-action"
            size="small"
            variant="outlined"
            prepend-icon="mdi-tune-variant"
            :disabled="!channelId"
            @click="openSettings"
          >
            {{ st('quality_meta_settings') }}
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
            {{ st('quality_meta_sync') }}
          </v-btn>
        </div>
      </v-card-title>
      <v-divider />
      <v-card-text>
        <div class="d-flex flex-wrap align-center ga-3">
          <v-select
            v-model="channelId"
            :items="pageOptions"
            item-title="title"
            item-value="value"
            :label="st('quality_meta_page')"
            density="compact"
            variant="outlined"
            hide-details
            :disabled="loading && !pages.length"
            style="min-width: 240px; max-width: 360px"
          />
          <v-menu v-if="pancakeNotice" location="bottom start" :offset="8" :close-on-content-click="false">
            <template #activator="{ props }">
              <v-btn
                v-bind="props"
                class="source-notice-trigger"
                :color="pancakeNotice.type"
                icon="mdi-information-outline"
                variant="text"
                size="small"
                :aria-label="pancakeNotice.text"
              />
            </template>
            <v-card class="source-notice-content" elevation="8">
              <v-alert :type="pancakeNotice.type" variant="tonal" density="compact">
                {{ pancakeNotice.text }}
              </v-alert>
            </v-card>
          </v-menu>
          <v-btn prepend-icon="mdi-refresh" variant="text" :loading="loading" @click="loadData(true)">
            {{ st('quality_meta_refresh') }}
          </v-btn>
          <v-spacer />
          <span v-if="data?.generated_at" class="text-caption text-medium-emphasis">
            {{ st('quality_meta_updated', { date: formatDateTime(data.generated_at), seconds: refreshSeconds }) }}
          </span>
        </div>
      </v-card-text>
    </v-card>

    <v-alert v-if="errorMessage" type="error" variant="tonal" class="mb-4" closable @click:close="errorMessage = ''">
      {{ errorMessage }}
    </v-alert>

    <v-alert v-if="!pages.length && !loading" type="info" variant="tonal" class="mb-4">
      {{ st('quality_meta_no_pages') }}
    </v-alert>

    <template v-if="data">
      <WarningBatch :warnings="labelWarnings" class="mb-4">
        <template #action="{ warning }">
          <v-btn v-if="warning.id === 'unclassified'" size="small" variant="outlined" @click="statusFilter = 'unclassified'">{{ st('quality_meta_view_unclassified') }}</v-btn>
        </template>
      </WarningBatch>

      <v-alert v-if="data?.sync.status === 'syncing'" type="info" variant="tonal" class="mb-4">
        {{ st('quality_meta_syncing_description') }}
      </v-alert>

      <v-alert
        v-if="data?.intake && data.intake.total > 0 && data.intake.captured === data.intake.total"
        :type="data.intake.captured === data.intake.total ? 'success' : 'warning'"
        variant="tonal"
        density="compact"
        class="mb-4"
      >
        {{ intakeSummary(data.intake) }}
        <span v-if="data.intake.failed"> {{ st('quality_meta_intake_failed', { count: formatNumber(data.intake.failed), error: data.intake.error }) }}</span>
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

        <div class="d-flex flex-wrap ga-2 mb-4" :aria-label="st('quality_meta_group_counts')">
          <v-chip color="success" variant="tonal" @click="statusFilter = 'qualified'">
            {{ st('quality_meta_qualified') }} <strong class="ml-1">{{ formatNumber(data.counts.qualified) }}</strong>
          </v-chip>
          <v-chip color="grey" variant="tonal" @click="statusFilter = 'unqualified'">
            {{ st('quality_meta_unqualified') }} <strong class="ml-1">{{ formatNumber(data.counts.unqualified) }}</strong>
          </v-chip>
          <v-chip color="primary" variant="tonal" @click="statusFilter = 'potential'">
            {{ st('quality_meta_potential') }} <strong class="ml-1">{{ formatNumber(data.counts.potential) }}</strong>
          </v-chip>
        </div>

        <v-card variant="outlined">
          <v-card-title class="d-flex align-center flex-wrap ga-3">
            <div class="title-block">
              <div class="meta-conversations-title text-subtitle-1 font-weight-bold">
                <v-icon start color="primary" icon="mdi-message-text-outline" />
                {{ st('quality_meta_conversations') }}
              </div>
              <div class="panel-description text-medium-emphasis mt-1">
                {{ totalDescription(data.counts.total) }}
              </div>
            </div>
            <v-spacer />
            <v-select
              v-model="statusFilter"
              :items="statusOptions"
              item-title="title"
              item-value="value"
              :label="st('quality_meta_status')"
              density="compact"
              variant="outlined"
              hide-details
              style="min-width: 190px; max-width: 230px"
            />
            <v-text-field
              v-model="search"
              :label="st('quality_meta_search')"
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
            :no-data-text="st('quality_meta_no_rows')"
            hover
          >
            <template #item.customer_name="{ item }">
              <div class="d-flex align-center ga-2 flex-nowrap">
                <span class="font-weight-medium text-no-wrap">{{ item.customer_name || st('quality_meta_unnamed_customer') }}</span>
              </div>
            </template>
            <template #item.channel_name="{ item }">
              <v-chip size="small" color="secondary" variant="tonal">
                {{ item.channel_name }}
              </v-chip>
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
                  <span class="text-caption text-medium-emphasis mr-1">{{ st('quality_meta_default_labels') }}</span>
                  <v-chip v-for="label in item.intake_labels" :key="`intake-${label.id}`" size="x-small" variant="outlined" prepend-icon="mdi-lock-outline" color="grey">
                    {{ label.page_label_name }}
                  </v-chip>
                </div>
                <div v-if="item.tracking_labels.length" class="d-flex flex-nowrap align-center ga-1">
                  <span class="text-caption text-medium-emphasis mr-1">{{ st('quality_meta_current_labels') }}</span>
                  <v-chip v-for="label in item.tracking_labels" :key="`tracking-${label.id}`" size="x-small" variant="outlined" color="primary">
                    {{ label.page_label_name }}
                  </v-chip>
                </div>
              </div>
              <v-tooltip v-else-if="!item.intake_captured && item.intake_error" :text="item.intake_error" location="top">
                <template #activator="{ props }">
                  <span v-bind="props" class="text-medium-emphasis" tabindex="0">
                    {{ item.intake_error.startsWith('Meta không cho đọc nhãn') ? st('quality_meta_labels_unavailable') : st('quality_meta_labels_error') }}
                  </span>
                </template>
              </v-tooltip>
              <span v-else class="text-medium-emphasis">
                {{ item.intake_captured ? st('quality_meta_captured_no_labels') : (item.intake_error || (item.classification === 'unknown' ? st('quality_meta_pending_backfill') : st('quality_meta_no_labels'))) }}
              </span>
            </template>
            <template #item.checked_at="{ item }">
              <span>{{ item.checked_at ? formatDateTime(item.checked_at) : st('quality_meta_not_checked') }}</span>
            </template>
            <template #item.actions="{ item }">
              <v-btn size="small" variant="text" color="primary" :to="messageLink(item)">{{ st('quality_meta_open_conversation') }}</v-btn>
            </template>
          </v-data-table>
        </v-card>
      </template>
    </template>

    <v-dialog v-model="settingsDialog" max-width="760" persistent>
      <v-card>
        <v-card-title class="d-flex align-center flex-wrap ga-2">
          {{ st('quality_meta_settings_title') }}
          <v-spacer />
          <v-btn icon="mdi-close" variant="text" :disabled="savingPolicy || refreshingCatalog" @click="settingsDialog = false" />
        </v-card-title>
        <v-divider />
        <v-card-text>
          <v-alert type="info" variant="tonal" density="compact" class="mb-4">
            {{ st('quality_meta_settings_description') }}
          </v-alert>

          <div v-if="isPancake" class="pancake-tokens mb-4">
            <div class="text-subtitle-2 font-weight-bold mb-1">{{ t('quality_pancake_tokens_title') }}</div>
            <div class="text-caption text-medium-emphasis mb-3">{{ t('quality_pancake_tokens_description') }}</div>
            <div class="d-flex align-start ga-2 mb-3">
              <v-text-field
                v-model="pageTokenInput"
                type="password"
                autocomplete="off"
                :label="t('quality_pancake_page_token')"
                :hint="data?.has_page_token ? t('quality_pancake_token_saved') : t('quality_pancake_page_token_hint')"
                persistent-hint
                variant="outlined"
                density="compact"
              />
              <v-btn variant="tonal" color="primary" class="token-save" :loading="savingToken === 'page'" :disabled="!pageTokenInput.trim()" @click="saveToken('page')">
                {{ t('quality_pancake_save_token') }}
              </v-btn>
            </div>
            <div class="d-flex align-start ga-2">
              <v-text-field
                v-model="userTokenInput"
                type="password"
                autocomplete="off"
                :label="t('quality_pancake_user_token')"
                :hint="data?.connection?.has_user_token ? t('quality_pancake_token_saved') : t('quality_pancake_user_token_hint')"
                persistent-hint
                variant="outlined"
                density="compact"
              />
              <v-btn variant="tonal" color="primary" class="token-save" :loading="savingToken === 'user'" :disabled="!userTokenInput.trim()" @click="saveToken('user')">
                {{ t('quality_pancake_save_token') }}
              </v-btn>
            </div>
            <div class="text-caption text-medium-emphasis mt-2">{{ socketStatusText }}</div>
          </div>

          <v-alert v-if="intakeLabelIds.length" type="warning" variant="tonal" density="compact" class="mb-4">
            {{ st('quality_meta_intake_locked', { count: formatNumber(intakeLabelIds.length) }) }}
          </v-alert>

          <div class="d-flex align-center justify-space-between flex-wrap ga-2 mb-4">
            <v-switch v-model="policyEnabled" :label="st('quality_meta_enable_tracking')" color="primary" hide-details />
            <div class="text-right">
              <v-btn
                variant="outlined"
                prepend-icon="mdi-download-outline"
                :loading="refreshingCatalog"
                @click="refreshCatalog"
              >
                {{ st('quality_meta_fetch_catalog') }}
              </v-btn>
              <div class="text-caption text-medium-emphasis mt-1">
                {{ catalogSyncedAt ? st('quality_meta_catalog_updated', { date: formatDateTime(catalogSyncedAt) }) : st('quality_meta_catalog_never') }}
              </div>
            </div>
          </div>

          <v-alert v-if="!catalog.length" type="warning" variant="tonal" density="compact" class="mb-4">
            {{ st('quality_meta_catalog_empty') }}
          </v-alert>

          <v-select
            v-model="policySelections.qualified"
            :items="trackingCatalog"
            item-title="page_label_name"
            item-value="id"
            :label="st('quality_meta_qualified')"
            :hint="st('quality_meta_qualified_hint')"
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
            :label="st('quality_meta_unqualified')"
            :hint="st('quality_meta_unqualified_hint')"
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
            :label="st('quality_meta_potential')"
            :hint="st('quality_meta_potential_hint')"
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
          <v-btn variant="text" :disabled="savingPolicy" @click="settingsDialog = false">{{ st('quality_meta_cancel') }}</v-btn>
          <v-btn color="primary" :loading="savingPolicy" :disabled="Boolean(mappingValidation)" @click="savePolicy">{{ st('quality_meta_save') }}</v-btn>
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
  channel_name: string
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
  source?: LabelSource
  meta_enabled?: boolean
  has_page_token?: boolean
  reconciled_at?: string | null
  unmatched?: number
  connection?: {
    has_user_token: boolean
    socket_enabled: boolean
    socket_status: 'never' | 'connected' | 'disconnected' | 'error'
    socket_error: string
    socket_connected_at: string | null
    socket_event_at: string | null
  }
}

const { t, te, locale } = useI18n()
const formatLocale = computed(() => locale.value === 'en' ? 'en-US' : 'vi-VN')
const route = useRoute()
const authStore = useAuthStore()
const tenantId = computed(() => route.params.tenantId as string)
const canEditSettings = computed(() => authStore.canEdit('settings'))
const canSync = computed(() => authStore.canEdit('messages'))

type LabelSource = 'meta' | 'pancake'
const sourceStorageKey = 'cqa.labelSource'
const source = ref<LabelSource>(localStorage.getItem(sourceStorageKey) === 'pancake' ? 'pancake' : 'meta')
const isPancake = computed(() => source.value === 'pancake')
const apiBase = computed(() => isPancake.value ? 'pancake-labels' : 'messenger-labels')
// Pancake reuses every Meta text unless a quality_pancake_* override exists.
function st(key: string, values?: Record<string, unknown>) {
  const override = key.replace('quality_meta_', 'quality_pancake_')
  const chosen = isPancake.value && te(override) ? override : key
  return values ? t(chosen, values) : t(chosen)
}
const pageTokenInput = ref('')
const userTokenInput = ref('')
const savingToken = ref<'' | 'page' | 'user'>('')

const pages = ref<Page[]>([])
const channelId = ref('')
const data = ref<LabelsReport | null>(null)
const labelWarnings = computed(() => {
  const report = data.value
  if (!report || !channelId.value) return []
  const warnings: { id: string; title: string; detail: string }[] = []
  if (!report.enabled) {
    warnings.push({ id: 'disabled', title: t('quality_meta_disabled_title'), detail: st('quality_meta_disabled_detail') })
  } else if (report.sync.status === 'error' || report.sync.status === 'partial') {
    warnings.push({ id: 'sync', title: syncStatusLabel(report.sync.status), detail: `${report.sync.error || st('quality_meta_sync_partial_detail')} ${t('quality_meta_stale_detail', { minutes: formatNumber(report.freshness_minutes) })}` })
  }
  if (report.intake?.total > 0 && report.intake.captured !== report.intake.total) {
    warnings.push({ id: 'intake', title: t('quality_meta_intake_title'), detail: `${intakeSummary(report.intake)}${report.intake.failed ? ` ${t('quality_meta_intake_failed', { count: formatNumber(report.intake.failed), error: report.intake.error })}` : ''}` })
  }
  if (report.counts?.unclassified) {
    warnings.push({ id: 'unclassified', title: t('quality_meta_unclassified_title', { count: formatNumber(report.counts.unclassified) }), detail: st('quality_meta_unclassified_detail') })
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

const isAllPages = computed(() => channelId.value === '')
const pageOptions = computed(() => [
  { title: t('quality_meta_all_pages'), value: '' },
  ...pages.value.map(page => ({
    title: `${page.name}${page.is_active ? '' : t('quality_meta_inactive_suffix')}`,
    value: page.id,
  })),
])

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
  return message ? st(validationMessageKeys[message] || message) : ''
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
  ...(isAllPages.value ? [{ title: t('quality_meta_page'), key: 'channel_name', sortable: true }] : []),
  { title: '', key: 'customer_error', width: '42px', sortable: false },
  { title: t('quality_meta_classification'), key: 'classification', sortable: true },
  { title: st('quality_meta_meta_labels'), key: 'labels', sortable: false },
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

const socketStatusText = computed(() => {
  const connection = data.value?.connection
  if (!connection) return ''
  if (!connection.socket_enabled) return t('quality_pancake_socket_off')
  if (!connection.has_user_token) return t('quality_pancake_socket_no_token')
  const status = connection.socket_status
  if (status === 'connected') {
    return t('quality_pancake_socket_connected', { date: connection.socket_event_at ? formatDateTime(connection.socket_event_at) : '—' })
  }
  if (status === 'error' || status === 'disconnected') return connection.socket_error || t('quality_pancake_socket_disconnected')
  return t('quality_pancake_socket_waiting')
})

const pancakeNotice = computed<{ type: 'info' | 'warning'; text: string } | null>(() => {
  const report = data.value
  if (!isPancake.value) return null
  if (!channelId.value) return { type: 'info', text: t('quality_pancake_pick_page') }
  if (!report) return null
  if (report.meta_enabled) return { type: 'warning', text: t('quality_pancake_meta_enabled') }
  const parts = [socketStatusText.value]
  if (report.reconciled_at) parts.push(t('quality_pancake_reconciled', { date: formatDateTime(report.reconciled_at) }))
  if (report.unmatched) parts.push(t('quality_pancake_unmatched', { count: formatNumber(report.unmatched) }))
  const warn = report.connection?.socket_status === 'error' || !report.has_page_token
  return { type: warn ? 'warning' : 'info', text: parts.filter(Boolean).join(' · ') }
})

async function saveToken(kind: 'page' | 'user') {
  const value = (kind === 'page' ? pageTokenInput.value : userTokenInput.value).trim()
  if (!value || (kind === 'page' && !channelId.value)) return
  savingToken.value = kind
  const targetTenant = tenantId.value
  try {
    const path = kind === 'page'
      ? `/tenants/${targetTenant}/pancake-labels/${channelId.value}/token`
      : `/tenants/${targetTenant}/pancake-labels/connection`
    await api.put(path, { token: value })
    if (disposed || tenantId.value !== targetTenant) return
    if (kind === 'page') pageTokenInput.value = ''
    else userTokenInput.value = ''
    showSnack(t('quality_pancake_token_saved_snack'), 'success')
    await loadData(true)
  } catch (error: any) {
    if (disposed) return
    showSnack(error?.response?.data?.error || t('quality_pancake_token_error'), 'error')
  } finally {
    savingToken.value = ''
  }
}

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
    const { data: response } = await api.get<LabelsReport>(`/tenants/${tenantId.value}/${apiBase.value}`, {
      params: { channel_id: channelId.value || undefined },
      signal: controller.signal,
    })
    if (sequence !== requestSequence) return
    pages.value = response.pages || []
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
    const { data: response } = await api.post(`/tenants/${targetTenant}/${apiBase.value}/${targetChannel}/catalog`)
    if (disposed || sequence !== actionSequence.catalog || tenantId.value !== targetTenant || channelId.value !== targetChannel) return
    catalog.value = response.catalog || []
    catalogSyncedAt.value = response.catalog_synced_at || null
    showSnack(st('quality_meta_catalog_success'), 'success')
  } catch (error: any) {
    if (disposed || sequence !== actionSequence.catalog || tenantId.value !== targetTenant || channelId.value !== targetChannel) return
    showSnack(error?.response?.data?.error || st('quality_meta_catalog_error'), 'error')
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
    await api.put(`/tenants/${targetTenant}/${apiBase.value}/${targetChannel}/policy`, {
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
    const { data: response } = await api.post(`/tenants/${targetTenant}/${apiBase.value}/${targetChannel}/sync`)
    if (disposed || sequence !== actionSequence.sync || tenantId.value !== targetTenant || channelId.value !== targetChannel) return
    if (data.value && response.status === 'syncing') {
      data.value.sync.status = 'syncing'
      data.value.counts = null
      data.value.rows = []
    }
    showSnack(response.status === 'capturing_intake' ? t('quality_meta_backfill_started') : st('quality_meta_sync_started'), 'success')
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

function totalDescription(total: number) {
  return t(isAllPages.value ? 'quality_meta_total_description_all' : 'quality_meta_total_description', { total: formatNumber(total) })
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

watch(source, value => {
  localStorage.setItem(sourceStorageKey, value)
  invalidateActions()
  requestSequence++
  activeController?.abort()
  data.value = null
  statusFilter.value = 'all'
  search.value = ''
  settingsDialog.value = false
  pageTokenInput.value = ''
  userTokenInput.value = ''
  loadData(true)
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

.label-panel-header {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 14px;
}

.label-toolbar {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 10px;
}

.panel-description {
  font-size: 13px;
  font-weight: 400;
  line-height: 1.5;
  letter-spacing: normal;
  white-space: normal;
  overflow-wrap: anywhere;
}

.source-toggle {
  height: 38px;
  padding: 3px;
  gap: 4px;
  border: 1px solid rgba(var(--v-border-color), var(--v-border-opacity));
  border-radius: 10px;
  background: rgb(var(--v-theme-surface));
  box-shadow: inset 0 0 0 1px rgba(var(--v-theme-on-surface), 0.02);
}

.source-option {
  min-width: 96px;
  height: 30px;
  padding-inline: 12px;
  border-radius: 7px !important;
  font-size: 0.875rem;
  font-weight: 600;
  line-height: 1.25;
  letter-spacing: 0.01em;
  text-transform: none;
  color: rgba(var(--v-theme-on-surface), 0.76);
}

.source-option--active {
  color: rgb(var(--v-theme-on-surface));
}

.source-option--meta.source-option--active {
  background: rgba(var(--v-theme-info), 0.14);
}

.source-option--pancake.source-option--active {
  background: rgba(var(--v-theme-primary), 0.14);
}

.source-logo {
  width: 19px;
  height: 19px;
  margin-inline-end: 7px;
  object-fit: contain;
  flex: 0 0 auto;
}

.source-notice-content {
  width: min(400px, calc(100vw - 32px));
  background: rgb(var(--v-theme-surface));
  white-space: normal;
}

.token-save {
  margin-top: 2px;
}

.label-action {
  min-height: 36px;
  padding-inline: 13px;
  font-size: 0.875rem;
  font-weight: 600;
  line-height: 1.25;
  letter-spacing: 0.01em;
  text-transform: none;
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
  .label-toolbar {
    width: 100%;
  }

  .source-toggle {
    width: 100%;
  }

  .source-option {
    flex: 1 1 50%;
  }

  .meta-labels-panel :deep(.v-data-table__td),
  .meta-labels-panel :deep(.v-data-table__th) {
    white-space: nowrap;
  }
}
</style>
