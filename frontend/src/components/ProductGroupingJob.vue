<template>
  <div class="product-grouping-job">
    <div class="job-header mb-4">
      <div class="d-flex align-center min-width-0">
        <v-btn icon="mdi-arrow-left" variant="text" size="small" :to="`/${tenantId}/jobs`" :aria-label="t('back')" />
        <h1 class="text-h5 font-weight-bold ml-2">{{ job.name }}</h1>
      </div>
      <div class="d-flex ga-2 flex-wrap">
        <v-btn variant="outlined" prepend-icon="mdi-pencil" size="small" @click="openEditor">{{ t('edit') }}</v-btn>
        <v-btn variant="outlined" prepend-icon="mdi-refresh" size="small" :loading="loading" :disabled="running" @click="load">{{ t('pg_refresh') }}</v-btn>
        <v-btn v-if="canRun" color="primary" prepend-icon="mdi-play" size="small" :loading="running" :disabled="loading || isJobRunning || !dateRangeValid || scopeChanged || !report?.product_names.length || !job.is_active" @click="run">{{ t('run_now') }}</v-btn>
      </div>
    </div>

    <v-card class="pa-4 mb-4">
      <div class="text-subtitle-1 font-weight-bold mb-4"><v-icon start size="small">mdi-information</v-icon>{{ t('job_info') }}</div>
      <div class="info-grid">
        <div><div class="info-label">{{ t('job_type') }}</div><v-chip color="primary" variant="tonal" size="small">{{ t('pg_task_type') }}</v-chip></div>
        <div><div class="info-label">{{ t('ai_model') }}</div><div class="text-body-2">{{ aiModel || t('pg_tenant_model') }}</div></div>
        <div><div class="info-label">{{ t('pg_schedule') }}</div><div class="text-body-2">{{ t('pg_schedule_value') }}</div></div>
        <div><div class="info-label">{{ t('status') }}</div><v-chip :color="job.is_active ? 'success' : 'grey'" variant="tonal" size="small">{{ t(job.is_active ? 'active' : 'inactive') }}</v-chip></div>
        <div><div class="info-label">{{ t('job_input_channels') }}</div><div class="text-body-2">{{ t('pg_input_value') }}</div></div>
        <div><div class="info-label">{{ t('job_output') }}</div><router-link class="text-body-2" :to="`/${tenantId}/service-quality`">{{ t('pg_output_value') }}</router-link></div>
        <div><div class="info-label">{{ t('job_last_run') }}</div><div class="text-body-2">{{ formatDate(latestRun?.started_at || job.last_run_at) }} <v-chip v-if="latestRun?.status || job.last_run_status" :color="statusColor(latestRun?.status || job.last_run_status)" variant="tonal" size="x-small">{{ statusLabel(latestRun?.status || job.last_run_status) }}</v-chip></div></div>
        <div><div class="info-label">{{ t('job_created_at') }}</div><div class="text-body-2">{{ formatDate(job.created_at) }}</div></div>
      </div>
    </v-card>

    <div class="scorecards mb-4">
      <v-card class="pa-4"><div class="stat-content"><div><div class="info-label">{{ t('pg_products') }}</div><div class="stat-value">{{ report?.grouping_ready ? namedGroups.length : '—' }}</div></div><v-icon color="primary" size="30">mdi-package-variant-closed</v-icon></div><div class="text-caption text-medium-emphasis mt-1">{{ t('pg_selected_scope') }}</div></v-card>
      <v-card class="pa-4"><div class="stat-content"><div><div class="info-label">{{ t('pg_source_conversations') }}</div><div class="stat-value">{{ report?.grouping_ready ? conversationCount : '—' }}</div></div><v-icon color="info" size="30">mdi-message-text</v-icon></div><div class="text-caption text-medium-emphasis mt-1">{{ t('pg_unique_conversations') }}</div></v-card>
      <v-card class="pa-4"><div class="stat-content"><div><div class="info-label">{{ t('pg_successful_runs') }}</div><div class="stat-value">{{ historyLoaded ? successfulRuns : '—' }}</div></div><v-icon color="success" size="30">mdi-check-circle</v-icon></div><div class="text-caption text-medium-emphasis mt-1">{{ t('pg_recent_runs') }}</div></v-card>
      <v-card class="pa-4"><div class="stat-content"><div><div class="info-label">{{ t('pg_run_errors') }}</div><div class="stat-value" :class="{ 'text-error': failedRuns.length }">{{ historyLoaded ? failedRuns.length : '—' }}</div></div><v-icon :color="failedRuns.length ? 'error' : 'success'" size="30">{{ failedRuns.length ? 'mdi-alert-circle' : 'mdi-check-circle-outline' }}</v-icon></div><v-btn v-if="failedRuns.length" variant="text" color="error" size="x-small" class="mt-1 px-0" @click="tab = 'history'; errorsOnly = true">{{ t('pg_view_errors') }}</v-btn><div v-else class="text-caption text-medium-emphasis mt-1">{{ t('pg_recent_runs') }}</div></v-card>
    </div>

    <v-alert v-if="latestRun && isFailure(latestRun.status)" type="error" variant="tonal" class="mb-4" data-testid="latest-run-error">
      <div class="font-weight-bold">{{ t('pg_latest_failed') }} · {{ formatDate(latestRun.started_at) }}</div>
      <div class="error-text mt-1">{{ latestRun.error_message || t('pg_unknown_run_error') }}</div>
    </v-alert>
    <v-alert v-if="running || isJobRunning" type="info" variant="tonal" class="mb-4">{{ t('pg_running') }}</v-alert>
    <v-alert v-if="historyError" type="error" variant="tonal" class="mb-4" role="alert">{{ historyError }}</v-alert>
    <v-alert v-if="runError" type="error" variant="tonal" class="mb-4" role="alert">{{ runError }}</v-alert>
    <v-alert v-if="runSucceeded" type="success" variant="tonal" class="mb-4">{{ t('pg_run_success') }}</v-alert>

    <v-card>
      <v-tabs v-model="tab" color="primary"><v-tab value="results" prepend-icon="mdi-table">{{ t('pg_results') }}</v-tab><v-tab value="history" prepend-icon="mdi-history">{{ t('pg_history') }}</v-tab></v-tabs>
      <v-divider />
      <div v-if="tab === 'results'" class="pa-4">
        <div class="result-filters mb-3">
          <v-text-field v-model="dateFrom" type="date" :label="t('pg_from')" density="compact" variant="outlined" hide-details />
          <v-text-field v-model="dateTo" type="date" :label="t('pg_to')" density="compact" variant="outlined" hide-details />
          <v-select v-model="channelId" :items="channelOptions" :label="t('pg_page')" density="compact" variant="outlined" hide-details />
          <v-btn variant="outlined" :disabled="!dateRangeValid || loading || running" @click="load">{{ t('pg_apply') }}</v-btn>
        </div>
        <v-alert v-if="!dateRangeValid" type="warning" variant="tonal" class="mb-3">{{ t('pg_invalid_dates') }}</v-alert>
        <p v-if="scopeChanged && report" class="text-caption text-warning mb-3">{{ t('pg_apply_hint') }}</p>
        <p class="text-caption text-medium-emphasis mb-3">{{ t('pg_count_hint') }} <span v-if="report">{{ t('pg_loaded_scope', { from: formatScopeDate(appliedScope.from), to: formatScopeDate(appliedScope.to) }) }}</span></p>
        <v-alert v-if="resultsError" type="error" variant="tonal" class="mb-3" role="alert">{{ resultsError }}</v-alert>
        <div v-if="loading" class="empty-state" role="status"><v-progress-circular indeterminate size="30" /><span>{{ t('pg_loading') }}</span></div>
        <v-alert v-else-if="report && !report.grouping_ready" type="info" variant="tonal" class="mb-3">{{ t('pg_pending') }}</v-alert>
        <template v-else-if="report?.grouping_ready">
          <v-alert v-if="report.excluded_product_names.length" type="info" variant="tonal" class="mb-3">{{ t('pg_excluded_keywords') }} <span class="font-weight-medium">{{ report.excluded_product_names.join(', ') }}</span></v-alert>
          <div class="d-flex align-center ga-3 flex-wrap mb-3">
            <v-chip size="small" variant="tonal">{{ t('pg_result_count', { count: report.groups.length }) }}</v-chip>
            <v-spacer />
            <v-text-field v-model="search" :label="t('pg_search')" prepend-inner-icon="mdi-magnify" density="compact" variant="outlined" hide-details clearable class="product-search" />
          </div>
          <v-table v-if="report.groups.length" class="product-table" density="comfortable">
            <thead><tr><th>{{ t('pg_product_name') }}</th><th class="text-right">{{ t('pg_quantity') }}</th><th>{{ t('pg_keywords') }}</th><th>{{ t('pg_potential') }}</th><th><span class="sr-only">{{ t('pg_details') }}</span></th></tr></thead>
            <tbody>
              <template v-for="group in pagedGroups" :key="group.name">
                <tr class="product-row">
                  <td><span class="font-weight-bold">{{ group.name || t('pg_omitted') }}</span><v-chip v-if="!group.name" color="grey" variant="tonal" size="x-small" class="ml-2">{{ t('pg_skip') }}</v-chip></td>
                  <td class="text-right font-weight-bold">{{ group.count }}</td>
                  <td><div class="d-flex flex-wrap ga-1 py-2"><v-chip v-for="keyword in group.members.slice(0, 3)" :key="keyword" size="x-small" variant="tonal">{{ keyword }}</v-chip><span v-if="group.members.length > 3" class="text-caption text-medium-emphasis">+{{ group.members.length - 3 }}</span></div></td>
                  <td><div class="d-flex flex-wrap ga-1"><v-chip v-for="level in levels" :key="level" :color="leadColor(level)" size="x-small" variant="tonal">{{ t(`sq_lead_${level}`) }}: {{ group.sources.filter(source => source.lead_quality?.level === level).length }}</v-chip></div></td>
                  <td class="text-right"><v-btn variant="text" size="small" :append-icon="expanded === group.name ? 'mdi-chevron-up' : 'mdi-chevron-down'" :aria-expanded="expanded === group.name" @click="expanded = expanded === group.name ? null : group.name">{{ t('pg_details') }}</v-btn></td>
                </tr>
                <tr v-if="expanded === group.name" class="source-row"><td colspan="5">
                  <div class="source-panel py-4">
                    <div class="text-subtitle-2 font-weight-bold mb-2">{{ t('pg_provenance_title', { name: group.name || t('pg_omitted'), count: group.count }) }}</div>
                    <p class="text-caption text-medium-emphasis mb-3">{{ t(group.name ? 'pg_mapping_hint' : 'pg_omitted_hint') }}</p>
                    <div class="d-flex flex-wrap ga-1 mb-3"><v-chip v-for="keyword in group.members" :key="keyword" size="small" variant="outlined">{{ keyword }}</v-chip></div>
                    <v-table density="compact" class="source-table">
                      <thead><tr><th>{{ t('pg_conversation') }}</th><th>{{ t('pg_keyword_evidence') }}</th><th>{{ t('pg_lead_assessment') }}</th><th>{{ t('pg_quality_assessment') }}</th></tr></thead>
                      <tbody><tr v-for="source in group.sources.slice((sourcePage - 1) * sourcePageSize, sourcePage * sourcePageSize)" :key="source.conversation_id">
                        <td><router-link :to="{ path: `/${tenantId}/messages`, query: { conv: source.conversation_id, channel_id: source.channel_id } }" class="font-weight-medium">{{ source.customer_name || source.conversation_id }}</router-link><div class="text-caption text-medium-emphasis">{{ source.channel_name }}</div><div class="text-caption text-medium-emphasis">{{ formatDate(source.insight_at) }}</div><p v-if="source.summary" class="text-body-2 mt-2 detail-copy">{{ source.summary }}</p></td>
                        <td><div v-for="(product, index) in source.matched_products" :key="index" class="mb-2"><div class="font-weight-medium">{{ product.name }}<span v-if="product.sku" class="text-caption text-medium-emphasis"> · {{ product.sku }}</span></div><blockquote v-if="product.evidence" class="evidence mt-1">{{ product.evidence }}</blockquote><span v-else class="text-caption text-medium-emphasis">{{ t('pg_no_evidence') }}</span></div></td>
                        <td><v-chip :color="leadColor(source.lead_quality?.level)" size="x-small" variant="tonal">{{ leadLabel(source.lead_quality?.level) }}</v-chip><p class="text-body-2 mt-2 detail-copy">{{ source.lead_quality?.reason || t('pg_no_reason') }}</p><blockquote v-if="source.lead_quality?.evidence" class="evidence mt-2">{{ source.lead_quality.evidence }}</blockquote></td>
                        <td><template v-if="source.quality_analysis"><v-chip :color="source.quality_analysis.verdict === 'PASS' ? 'success' : source.quality_analysis.verdict === 'FAIL' ? 'error' : 'grey'" variant="tonal" size="x-small">{{ verdictLabel(source.quality_analysis.verdict) }}</v-chip><span v-if="source.quality_analysis.score != null" class="text-caption ml-2">{{ source.quality_analysis.score }}/100</span><v-chip v-if="source.quality_analysis_stale" color="warning" variant="tonal" size="x-small" class="ml-1">{{ t('pg_stale_quality') }}</v-chip><div v-if="source.quality_analysis.job_name || source.quality_analysis.evaluated_at" class="text-caption text-medium-emphasis mt-2">{{ source.quality_analysis.job_name }} · {{ formatDate(source.quality_analysis.evaluated_at) }}</div><p class="text-body-2 mt-2 detail-copy">{{ source.quality_analysis.review || t('pg_no_review') }}</p><div v-for="(violation, index) in source.quality_analysis.violations || []" :key="index" class="mt-2 detail-copy text-body-2"><strong>{{ violation.rule_name }}</strong><span v-if="violation.severity" class="text-caption ml-1">({{ violation.severity }})</span><p v-if="violation.explanation">{{ violation.explanation }}</p><p v-if="violation.suggestion" class="mt-1">{{ t('pg_suggestion') }} {{ violation.suggestion }}</p><blockquote v-if="violation.evidence" class="evidence mt-1">{{ violation.evidence }}</blockquote></div></template><span v-else class="text-caption text-medium-emphasis">{{ t('pg_no_quality') }}</span></td>
                      </tr></tbody>
                    </v-table>
                    <v-pagination v-if="group.sources.length > sourcePageSize" v-model="sourcePage" :length="Math.ceil(group.sources.length / sourcePageSize)" :total-visible="5" density="compact" class="mt-3" />
                  </div>
                </td></tr>
              </template>
              <tr v-if="!filteredGroups.length"><td colspan="5" class="text-center py-6 text-medium-emphasis">{{ t('pg_no_matches') }}</td></tr>
            </tbody>
          </v-table>
          <div v-if="!report.groups.length" class="empty-state text-medium-emphasis">{{ t('pg_empty') }}</div>
          <v-pagination v-if="filteredGroups.length > pageSize" v-model="page" :length="Math.ceil(filteredGroups.length / pageSize)" :total-visible="5" density="compact" class="mt-3" />
        </template>
        <div v-else-if="!loading && !resultsError" class="empty-state text-medium-emphasis"><v-icon size="36">mdi-package-variant-closed</v-icon><span>{{ t('pg_empty') }}</span></div>
      </div>
      <div v-else class="pa-4">
        <div class="d-flex align-center justify-space-between ga-3 mb-3"><p class="text-caption text-medium-emphasis">{{ t('pg_history_hint') }}</p><v-switch v-model="errorsOnly" :label="t('pg_errors_only')" color="error" density="compact" hide-details class="flex-grow-0" /></div>
        <v-table v-if="displayedRuns.length" density="comfortable" class="history-table"><thead><tr><th>{{ t('pg_started') }}</th><th>{{ t('status') }}</th><th>{{ t('pg_duration') }}</th><th>{{ t('pg_run_detail') }}</th></tr></thead><tbody><tr v-for="item in displayedRuns" :key="item.id"><td>{{ formatDate(item.started_at) }}<div class="text-caption text-medium-emphasis">{{ item.id }}</div></td><td><v-chip :color="statusColor(item.status)" size="small" variant="tonal">{{ statusLabel(item.status) }}</v-chip></td><td>{{ runDuration(item) }}</td><td><div v-if="item.error_message" class="text-error error-text" role="alert">{{ item.error_message }}</div><span v-else class="text-body-2 text-medium-emphasis">{{ t(item.status === 'success' ? 'pg_history_success' : item.status === 'running' ? 'pg_running' : 'pg_no_run_detail') }}</span></td></tr></tbody></v-table>
        <div v-else-if="!historyError" class="empty-state text-medium-emphasis"><v-icon size="36">mdi-history</v-icon><span>{{ t(errorsOnly ? 'pg_no_errors' : 'pg_no_runs') }}</span></div>
      </div>
    </v-card>

    <v-dialog v-model="editing" max-width="900" scrollable :persistent="saving">
      <v-card>
        <v-card-title>{{ t('edit') }} · {{ job.name }}</v-card-title>
        <v-card-text>
          <v-alert v-if="saveError" type="error" variant="tonal" class="mb-4">{{ saveError }}</v-alert>
          <p class="mb-4 text-body-2 text-medium-emphasis">{{ t('sq_prompt_hint') }}</p>
          <v-textarea v-model="prompt" :label="t('sq_system_prompt')" :readonly="!isAdmin" :disabled="saving" variant="outlined" rows="16" auto-grow class="system-prompt" hide-details />
          <p v-if="!isAdmin" class="text-caption text-medium-emphasis mt-3">{{ t('sq_prompt_admin_only') }}</p>
        </v-card-text>
        <v-card-actions><v-spacer /><v-btn :disabled="saving" @click="editing = false">{{ t('pg_close') }}</v-btn><v-btn v-if="isAdmin" color="primary" variant="flat" :loading="saving" :disabled="!prompt.trim() || prompt === job.system_prompt" @click="save">{{ t('sq_save_prompt') }}</v-btn></v-card-actions>
      </v-card>
    </v-dialog>
    <v-snackbar v-model="saved" color="success">{{ t('sq_prompt_saved') }}</v-snackbar>
  </div>
</template>

<script setup lang="ts">
import { computed, onUnmounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useAuthStore } from '../stores/auth'
import type { JobRun } from '../stores/jobs'
import type { ProductGroupingReport } from '../utils/product-grouping-report'
import api from '../api'

const props = defineProps<{ job: Record<string, any>; tenantId: string }>()
const emit = defineEmits<{ updated: [job: Record<string, any>] }>()
const { t, locale } = useI18n()
const authStore = useAuthStore()
const isAdmin = computed(() => ['owner', 'admin'].includes(authStore.tenantPerms.role))
const canRun = computed(() => authStore.canEdit('jobs') && authStore.canView('messages'))
const report = ref<ProductGroupingReport | null>(null)
const runs = ref<JobRun[]>([])
const historyLoaded = ref(false)
const aiModel = ref('')
const tab = ref('results')
const loading = ref(false)
const running = ref(false)
const resultsError = ref('')
const historyError = ref('')
const runError = ref('')
const runSucceeded = ref(false)
const editing = ref(false)
const prompt = ref('')
const saving = ref(false)
const saved = ref(false)
const saveError = ref('')
const search = ref('')
const channelId = ref('')
const dateFrom = ref(localDate(-6))
const dateTo = ref(localDate())
const appliedScope = ref({ from: '', to: '', channel_id: '' })
const scopeChanged = computed(() => dateFrom.value !== appliedScope.value.from || dateTo.value !== appliedScope.value.to || channelId.value !== appliedScope.value.channel_id)
const dateRangeValid = computed(() => !!dateFrom.value && !!dateTo.value && dateFrom.value <= dateTo.value)
const page = ref(1)
const pageSize = 20
const sourcePage = ref(1)
const sourcePageSize = 10
const expanded = ref<string | null>(null)
const errorsOnly = ref(false)
const levels = ['high', 'medium', 'low'] as const
const channelOptions = computed(() => [{ title: t('sq_all_pages'), value: '' }, ...(report.value?.report.pages || []).map(item => ({ title: item.name, value: item.id }))])
const namedGroups = computed(() => (report.value?.groups || []).filter(group => group.name))
const conversationCount = computed(() => new Set(namedGroups.value.flatMap(group => group.sources.map(source => source.conversation_id))).size)
const filteredGroups = computed(() => {
  const term = (search.value || '').trim().toLocaleLowerCase()
  return (report.value?.groups || []).filter(group => !term || [group.name, ...group.members].some(value => value.toLocaleLowerCase().includes(term)))
})
const pagedGroups = computed(() => filteredGroups.value.slice((page.value - 1) * pageSize, page.value * pageSize))
const successfulRuns = computed(() => runs.value.filter(item => item.status === 'success').length)
const failedRuns = computed(() => runs.value.filter(item => isFailure(item.status)))
const latestRun = computed(() => runs.value[0])
const isJobRunning = computed(() => runs.value.some(item => item.status === 'running'))
const displayedRuns = computed(() => errorsOnly.value ? failedRuns.value : runs.value)
let requestSequence = 0
let controller: AbortController | undefined
let pollTimer: ReturnType<typeof setTimeout> | undefined
let disposed = false

function localDate(offsetDays = 0) {
  const parts = new Intl.DateTimeFormat('en-CA', { timeZone: 'Asia/Ho_Chi_Minh', year: 'numeric', month: '2-digit', day: '2-digit' }).formatToParts(new Date())
  const part = (name: string) => parts.find(item => item.type === name)!.value
  const date = new Date(`${part('year')}-${part('month')}-${part('day')}T12:00:00Z`)
  date.setUTCDate(date.getUTCDate() + offsetDays)
  return date.toISOString().slice(0, 10)
}
function formatDate(value?: string | null) {
  if (!value || !Number.isFinite(new Date(value).getTime())) return '—'
  return new Intl.DateTimeFormat(locale.value === 'en' ? 'en-GB' : 'vi-VN', { timeZone: 'Asia/Ho_Chi_Minh', year: 'numeric', month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit' }).format(new Date(value))
}
function formatScopeDate(value: string) { const [year, month, day] = value.split('-'); return year && month && day ? `${day}/${month}/${year}` : '—' }
function isFailure(status: string) { return ['error', 'failed', 'partial_error'].includes(status) }
function statusColor(status: string) { return status === 'success' ? 'success' : isFailure(status) ? 'error' : status === 'running' ? 'info' : 'grey' }
function statusLabel(status: string) { return t(status === 'success' ? 'pg_status_success' : isFailure(status) ? 'pg_status_error' : status === 'running' ? 'pg_status_running' : status === 'cancelled' ? 'pg_status_cancelled' : 'pg_status_unknown') }
function leadColor(level?: string) { return level === 'high' ? 'success' : level === 'medium' ? 'info' : level === 'low' ? 'warning' : 'grey' }
function leadLabel(level?: string) { return levels.includes(level as typeof levels[number]) ? t(`sq_lead_${level}`) : t('pg_status_unknown') }
function verdictLabel(verdict: string) { return t(verdict === 'PASS' ? 'pg_pass' : verdict === 'FAIL' ? 'pg_fail' : 'pg_skip') }
function runDuration(item: JobRun) {
  if (!item.finished_at) return '—'
  const seconds = Math.max(0, Math.round((new Date(item.finished_at).getTime() - new Date(item.started_at).getTime()) / 1000))
  return Number.isFinite(seconds) ? t('pg_seconds', { count: seconds }) : '—'
}
function errorMessage(error: any, fallback: string) { return error?.response?.data?.error || t(fallback) }
function openEditor() { prompt.value = props.job.system_prompt || ''; saveError.value = ''; editing.value = true }

async function load() {
  if (!dateRangeValid.value || disposed) return
  const sequence = ++requestSequence
  controller?.abort()
  clearTimeout(pollTimer)
  controller = new AbortController()
  const signal = controller.signal
  const tenant = props.tenantId
  const jobId = props.job.id
  loading.value = true
  resultsError.value = ''
  historyError.value = ''
  const scope = { from: dateFrom.value, to: dateTo.value, channel_id: channelId.value }
  const responses = await Promise.allSettled([
    authStore.canView('messages') ? api.get<ProductGroupingReport>(`/tenants/${tenant}/jobs/${jobId}/product-grouping-results`, { params: scope, signal }) : Promise.reject(new Error('messages_permission')),
    api.get<JobRun[]>(`/tenants/${tenant}/jobs/${jobId}/runs`, { signal }),
  ])
  if (sequence !== requestSequence || disposed) return
  const [results, history] = responses
  if (results.status === 'fulfilled') { report.value = results.value.data; appliedScope.value = scope; expanded.value = null; page.value = 1 }
  else { report.value = null; resultsError.value = errorMessage(results.reason, authStore.canView('messages') ? 'pg_load_error' : 'pg_messages_required') }
  if (history.status === 'fulfilled') { runs.value = history.value.data || []; historyLoaded.value = true }
  else { historyLoaded.value = false; runs.value = []; historyError.value = errorMessage(history.reason, 'pg_history_error') }
  loading.value = false
  if (isJobRunning.value) pollTimer = setTimeout(() => void load(), 5000)
}
async function run() {
  if (!canRun.value || running.value || isJobRunning.value || !report.value || !dateRangeValid.value || scopeChanged.value || !props.job.is_active) return
  const tenant = props.tenantId
  const jobId = props.job.id
  running.value = true
  runError.value = ''
  runSucceeded.value = false
  try {
    const { data } = await api.post(`/tenants/${tenant}/service-quality/product-groups`, { from: dateFrom.value, to: dateTo.value, channel_id: channelId.value, product_names: report.value.product_names })
    if (disposed || tenant !== props.tenantId || jobId !== props.job.id) return
    if (!data.enabled) throw new Error('task_inactive')
    runSucceeded.value = true
  } catch (error: any) {
    if (!disposed && tenant === props.tenantId && jobId === props.job.id) runError.value = errorMessage(error, 'pg_run_error')
  } finally {
    if (!disposed && tenant === props.tenantId && jobId === props.job.id) { running.value = false; await load(); if (runSucceeded.value && !report.value?.grouping_ready) { runSucceeded.value = false; runError.value = t('pg_result_unavailable') } }
  }
}
async function save() {
  if (!isAdmin.value || saving.value || !prompt.value.trim()) return
  const tenant = props.tenantId
  const jobId = props.job.id
  saving.value = true
  saveError.value = ''
  saved.value = false
  try {
    const { data } = await api.put(`/tenants/${tenant}/jobs/${jobId}/product-grouping-prompt`, { system_prompt: prompt.value })
    if (disposed || tenant !== props.tenantId || jobId !== props.job.id) return
    emit('updated', data)
    editing.value = false
    saved.value = true
    runSucceeded.value = false
    await load()
  } catch (error: any) {
    if (!disposed && tenant === props.tenantId && jobId === props.job.id) saveError.value = errorMessage(error, 'sq_prompt_save_error')
  } finally { saving.value = false }
}
watch(expanded, () => { sourcePage.value = 1 })
watch(search, () => { page.value = 1; expanded.value = null })
watch(() => [props.tenantId, props.job.id], () => {
  report.value = null; runs.value = []; historyLoaded.value = false; errorsOnly.value = false; search.value = ''; tab.value = 'results'; editing.value = false; saved.value = false; runError.value = ''; runSucceeded.value = false; running.value = false; channelId.value = ''; aiModel.value = ''
  void load()
  if (authStore.canView('settings')) {
    const tenant = props.tenantId
    void api.get(`/tenants/${tenant}/settings`).then(({ data }) => {
      if (!disposed && tenant === props.tenantId) aiModel.value = [data?.settings?.ai_provider, data?.settings?.ai_model].filter(Boolean).join(' / ')
    }).catch(() => {})
  }
}, { immediate: true })
onUnmounted(() => { disposed = true; requestSequence++; controller?.abort(); clearTimeout(pollTimer) })
</script>

<style scoped>
.job-header { display: flex; justify-content: space-between; align-items: center; gap: 16px; flex-wrap: wrap; }
.job-header h1 { overflow-wrap: anywhere; }
.min-width-0 { min-width: 0; }
.info-grid, .scorecards { display: grid; grid-template-columns: repeat(4, minmax(0, 1fr)); gap: 24px; }
.info-label { color: rgb(var(--v-theme-on-surface), .6); font-size: 14px; margin-bottom: 5px; }
.stat-content { display: flex; align-items: center; justify-content: space-between; gap: 12px; }
.stat-value { font-size: 24px; font-weight: 700; line-height: 1.3; }
.stat-content .v-icon { opacity: .65; }
.result-filters { display: grid; grid-template-columns: 180px 180px minmax(180px, 1fr) auto; align-items: center; gap: 12px; }
.product-search { max-width: 340px; min-width: 200px; }
.product-table :deep(th), .history-table :deep(th) { white-space: nowrap; }
.product-table :deep(td) { vertical-align: middle; }
.product-table :deep(th:first-child) { min-width: 180px; }
.source-row > td { background: rgb(var(--v-theme-on-surface), .025); }
.source-panel { min-width: 820px; }
.source-table :deep(th) { white-space: nowrap; }
.source-table :deep(td) { vertical-align: top; padding-top: 12px; padding-bottom: 12px; width: 25%; }
.detail-copy { white-space: pre-wrap; overflow-wrap: anywhere; }
.evidence { border-left: 3px solid rgb(var(--v-theme-primary), .3); padding-left: 10px; font-size: 12px; white-space: pre-wrap; overflow-wrap: anywhere; color: rgb(var(--v-theme-on-surface), .7); }
.error-text { white-space: pre-wrap; overflow-wrap: anywhere; }
.empty-state { padding: 44px 16px; display: flex; align-items: center; justify-content: center; flex-direction: column; gap: 12px; text-align: center; }
.sr-only { position: absolute; width: 1px; height: 1px; overflow: hidden; clip: rect(0, 0, 0, 0); }
.system-prompt :deep(textarea) { font-family: ui-monospace, monospace; font-size: 13px; line-height: 1.6; }
@media (max-width: 960px) { .info-grid, .scorecards { grid-template-columns: repeat(2, minmax(0, 1fr)); gap: 16px; } .result-filters { grid-template-columns: repeat(2, minmax(0, 1fr)); } }
@media (max-width: 480px) { .job-header h1 { font-size: 20px !important; } .info-grid, .scorecards { gap: 12px; } .info-label { font-size: 12px; } }
</style>
