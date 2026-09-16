<template>
  <div class="service-quality-page">
    <div class="d-flex align-start justify-space-between flex-wrap ga-3 mb-5">
      <div>
        <div class="d-flex align-center ga-2">
          <v-icon color="primary" size="30">mdi-headset</v-icon>
          <h1 class="text-h5 font-weight-bold">{{ t('sq_title') }}</h1>
        </div>
        <p class="text-body-2 text-medium-emphasis mt-1 mb-0">
          {{ t('sq_subtitle') }}
        </p>
      </div>
      <div class="d-flex flex-wrap ga-2">
        <v-btn
          v-if="authStore.canEdit('jobs')"
          variant="tonal"
          color="primary"
          prepend-icon="mdi-creation"
          :to="`/${tenantId}/jobs/create?template=messenger-insights`"
        >
          {{ t('sq_create_analysis') }}
        </v-btn>
        <v-btn
          v-if="canEditPolicy"
          variant="outlined"
          prepend-icon="mdi-tune"
          @click="openPolicyDialog"
        >
          {{ t('sq_response_threshold') }}
        </v-btn>
        <v-btn color="primary" prepend-icon="mdi-refresh" :loading="loading" @click="loadReport(true)">
          {{ t('sq_refresh') }}
        </v-btn>
      </div>
    </div>

    <v-alert type="info" variant="tonal" class="mb-4" border="start">
      <div class="font-weight-medium">{{ t('sq_data_scope') }}</div>
      <div class="text-body-2 mt-1">
        {{ t('sq_scope_description') }}
      </div>
    </v-alert>

    <v-alert
      v-if="report && (report.summary.waiting || report.summary.overdue)"
      :type="report.summary.overdue ? 'error' : 'warning'"
      variant="tonal"
      class="mb-4"
      border="start"
      prominent
    >
      <div class="d-flex align-center justify-space-between flex-wrap ga-2">
        <div>
          <strong>{{ t('sq_overdue_count', { count: report.summary.overdue }) }}</strong>
          <span class="mx-1">·</span>
          {{ t('sq_waiting_count', { count: report.summary.waiting }) }}
          <div class="text-caption mt-1">{{ t('sq_queue_current') }}</div>
        </div>
        <v-btn size="small" variant="outlined" aria-controls="conversation-queue" @click="viewQueue">
          {{ t('sq_view_queue') }}
        </v-btn>
      </div>
    </v-alert>

    <v-card class="mb-4" variant="outlined">
      <v-card-text>
        <div class="d-flex flex-wrap align-end ga-3">
          <v-select
            v-model="channelId"
            :items="channelOptions"
            item-title="title"
            item-value="value"
            :label="t('sq_page')"
            density="compact"
            variant="outlined"
            hide-details
            style="min-width: 220px; max-width: 320px"
          />
          <v-text-field v-model="dateFrom" :label="t('sq_date_from')" type="date" density="compact" variant="outlined" hide-details style="max-width: 170px" />
          <v-text-field v-model="dateTo" :label="t('sq_date_to')" type="date" density="compact" variant="outlined" hide-details style="max-width: 170px" />
          <v-btn variant="tonal" color="primary" prepend-icon="mdi-filter" :disabled="!dateRangeValid" @click="loadReport(true)">
            {{ t('sq_apply') }}
          </v-btn>
          <span v-if="!dateRangeValid" class="text-caption text-error">{{ t('sq_invalid_range') }}</span>
          <v-spacer />
          <span v-if="report" class="text-caption text-medium-emphasis">
            {{ t('sq_updated', { date: formatDateTime(report.generated_at), timezone: report.policy.timezone }) }}
          </span>
        </div>
      </v-card-text>
    </v-card>

    <MetaLabelsPanel class="mb-6" />

    <v-progress-linear v-if="loading && report" indeterminate color="primary" class="mb-3" />

    <v-alert v-if="errorMessage" type="error" variant="tonal" class="mb-4" closable @click:close="errorMessage = ''">
      <div>{{ errorMessage }}</div>
      <v-btn class="mt-2" size="small" variant="outlined" @click="loadReport(true)">{{ t('sq_retry') }}</v-btn>
    </v-alert>

    <template v-if="loading && !report">
      <v-row class="mb-2">
        <v-col v-for="n in 8" :key="n" cols="12" sm="6" lg="3"><v-skeleton-loader type="article" /></v-col>
      </v-row>
      <v-skeleton-loader type="table" />
    </template>

    <template v-else-if="report">
      <v-alert v-if="report.invalid_timestamps" type="warning" variant="tonal" density="compact" class="mb-4">
        {{ t('sq_invalid_timestamps', { count: report.invalid_timestamps }) }}
      </v-alert>

      <v-row class="mb-2">
        <v-col v-for="kpi in kpis" :key="kpi.label" cols="12" sm="6" lg="3">
          <v-card variant="outlined" class="h-100 kpi-card">
            <v-card-text class="d-flex align-center ga-3">
              <v-avatar :color="kpi.color" variant="tonal" size="44"><v-icon :icon="kpi.icon" /></v-avatar>
              <div>
                <div class="text-caption text-medium-emphasis">{{ kpi.label }}</div>
                <div class="text-h6 font-weight-bold">{{ kpi.value }}</div>
                <div v-if="kpi.hint" class="text-caption text-medium-emphasis">{{ kpi.hint }}</div>
              </div>
            </v-card-text>
          </v-card>
        </v-col>
      </v-row>

      <v-card variant="outlined" class="mb-4">
        <v-card-title class="text-subtitle-1 font-weight-bold d-flex align-center flex-wrap ga-2">
          <v-icon start color="primary">mdi-cloud-sync</v-icon>
          {{ t('sq_data_source') }}
          <v-spacer />
          <span class="panel-description text-medium-emphasis">{{ t('sq_scanned', { count: report.conversations_scanned }) }}</span>
        </v-card-title>
        <v-divider />
        <v-card-text v-if="report.pages.length" class="d-flex flex-wrap ga-2">
          <v-chip
            v-for="page in report.pages"
            :key="page.id"
            :color="syncColor(page)"
            variant="tonal"
            size="small"
          >
            <v-icon start :icon="page.is_active ? 'mdi-facebook' : 'mdi-link-off'" />
            {{ page.name }} · {{ page.last_sync_at ? formatDateTime(page.last_sync_at) : t('sq_not_synced') }} · {{ syncLabel(page.last_sync_status) }}
          </v-chip>
        </v-card-text>
        <v-card-text v-else class="text-center py-8">
          <v-icon size="42" color="grey">mdi-facebook</v-icon>
          <div class="text-subtitle-1 mt-2">{{ t('sq_no_pages') }}</div>
          <v-btn v-if="authStore.canView('channels')" class="mt-3" color="primary" variant="tonal" :to="`/${tenantId}/channels`">{{ t('sq_connect_page') }}</v-btn>
        </v-card-text>
      </v-card>

      <v-card variant="outlined" class="mb-4">
        <v-card-title class="d-flex align-center flex-wrap ga-2">
          <div>
            <div class="text-subtitle-1 font-weight-bold"><v-icon start color="primary">mdi-chart-box-outline</v-icon>{{ t('sq_content') }}</div>
            <div class="panel-description text-medium-emphasis mt-1">
              {{ t('sq_aggregate_scope', { eligible: insightAggregates.eligibleConversations, stale: insightAggregates.excludedStale, outside: insightAggregates.excludedOutsideWindow }) }}
            </div>
          </div>
          <v-spacer />
          <v-btn v-if="authStore.canEdit('jobs')" size="small" variant="text" color="primary" :to="`/${tenantId}/jobs/create?template=messenger-insights`">
            {{ t('sq_configure_ai') }}
          </v-btn>
        </v-card-title>
        <v-divider />
        <v-card-text>
          <InsightWordcloudPanel :groups="insightGroups" :conversations="report.rows" :tenant-id="tenantId" />
        </v-card-text>
      </v-card>

      <section
        id="conversation-queue"
        ref="conversationQueue"
        class="conversation-queue"
        tabindex="-1"
        aria-labelledby="conversation-queue-title"
      >
      <v-card variant="outlined">
        <v-card-title class="d-flex align-center flex-wrap ga-3">
          <div>
            <h2 id="conversation-queue-title" class="queue-title text-subtitle-1 font-weight-bold"><v-icon start color="primary">mdi-message-alert-outline</v-icon>{{ queueTitle }}</h2>
            <div class="panel-description text-medium-emphasis mt-1" role="status">
              {{ t('sq_matching', { count: filteredRows.length }) }}<span v-if="statusFilter === 'overdue' || statusFilter === 'waiting'">{{ t('sq_queue_date_note') }}</span>
            </div>
          </div>
          <v-spacer />
          <v-select
            v-model="statusFilter"
            :items="statusOptions"
            item-title="title"
            item-value="value"
            :label="t('sq_status')"
            density="compact"
            variant="outlined"
            hide-details
            style="max-width: 190px"
          />
          <v-btn
            v-if="authStore.canEdit('jobs') && staleInsightCount"
            size="small"
            color="warning"
            variant="tonal"
            prepend-icon="mdi-creation"
            :loading="reanalysingStale"
            :disabled="loading"
            @click="reanalyseStaleInsights"
          >
            {{ t('sq_reanalyse_now', { count: staleInsightCount }) }}
          </v-btn>
          <v-text-field
            v-model="search"
            :label="t('sq_search_customer')"
            prepend-inner-icon="mdi-magnify"
            density="compact"
            variant="outlined"
            hide-details
            clearable
            style="max-width: 260px"
          />
        </v-card-title>
        <v-divider />
        <v-data-table
          v-model:page="conversationPage"
          :headers="headers"
          :items="filteredRows"
          item-value="conversation_id"
          :items-per-page="20"
          :no-data-text="t('sq_no_conversations')"
          hover
        >
          <template #item.customer_name="{ item }">
            <div class="py-2">
              <div class="font-weight-medium">{{ item.customer_name || t('sq_customer_fallback') }}</div>
              <div class="text-caption text-medium-emphasis">{{ item.channel_name }}</div>
            </div>
          </template>
          <template #item.status="{ item }">
            <v-chip :color="statusColor(item.status)" variant="tonal" size="small">{{ statusLabel(item.status) }}</v-chip>
          </template>
          <template #item.waiting="{ item }">
            <span v-if="item.waiting" :class="item.status === 'overdue' ? 'text-error font-weight-bold' : ''">{{ formatDuration(item.waiting.seconds) }}</span>
            <span v-else>—</span>
          </template>
          <template #item.last_message_at="{ item }">{{ item.last_message_at ? formatDateTime(item.last_message_at) : '—' }}</template>
          <template #item.insight="{ item }">
            <v-chip v-if="item.insight" :color="item.insight_stale ? 'warning' : 'success'" size="x-small" variant="tonal">
              {{ item.insight_stale ? t('sq_reanalyse') : t('sq_analysed') }}
            </v-chip>
            <span v-else class="text-medium-emphasis">{{ t('sq_none') }}</span>
          </template>
          <template #item.actions="{ item }">
            <div class="d-flex ga-1 justify-end">
              <v-btn icon="mdi-eye-outline" variant="text" size="small" :title="t('sq_view_detail')" @click="openDetail(item)" />
              <v-btn
                icon="mdi-open-in-new"
                variant="text"
                size="small"
                color="primary"
                :title="t('sq_open_messages')"
                :to="messageLink(item)"
              />
              <v-btn
                v-if="item.waiting && canResolve"
                icon="mdi-check-circle-outline"
                variant="text"
                size="small"
                color="success"
                :title="t('sq_mark_resolved')"
                @click="openResolve(item)"
              />
            </div>
          </template>
        </v-data-table>
      </v-card>
      </section>
    </template>

    <v-dialog v-model="detailDialog" max-width="760" scrollable>
      <v-card v-if="selectedRow">
        <v-card-title class="d-flex align-center">
          {{ t('sq_detail_title', { customer: selectedRow.customer_name || t('sq_customer_fallback') }) }}
          <v-spacer />
          <v-btn icon="mdi-close" variant="text" @click="detailDialog = false" />
        </v-card-title>
        <v-divider />
        <v-card-text>
          <p class="text-caption text-medium-emphasis">{{ t('sq_history', { date: selectedRow.history_from ? formatDateTime(selectedRow.history_from) : t('sq_unknown') }) }}</p>
          <v-alert v-if="selectedRow.insight_stale" type="warning" variant="tonal" density="compact" class="mb-4">
            {{ t('sq_stale_warning') }}
          </v-alert>
          <template v-if="selectedRow.insight">
            <div class="text-subtitle-2 font-weight-bold mb-1">{{ t('sq_summary') }}</div>
            <p class="text-body-2">{{ selectedRow.insight.summary || t('sq_no_summary') }}</p>
            <div class="text-subtitle-2 font-weight-bold mb-2">{{ t('sq_intents') }}</div>
            <div class="d-flex flex-wrap ga-2 mb-4">
              <v-chip v-for="intent in selectedRow.insight.intents || []" :key="intent" size="small" variant="tonal">{{ intent }}</v-chip>
              <span v-if="!selectedRow.insight.intents?.length" class="text-body-2 text-medium-emphasis">{{ t('sq_unidentified') }}</span>
            </div>
            <div class="text-subtitle-2 font-weight-bold mb-2">{{ t('sq_products') }}</div>
            <v-list v-if="selectedRow.insight.products?.length" density="compact" class="mb-3">
              <v-list-item v-for="(product, index) in selectedRow.insight.products" :key="`${product.name}-${index}`" prepend-icon="mdi-package-variant">
                <v-list-item-title>{{ product.name }} <v-chip v-if="product.sku" size="x-small" class="ml-1">SKU {{ product.sku }}</v-chip></v-list-item-title>
                <v-list-item-subtitle>{{ product.evidence || t('sq_no_evidence') }}</v-list-item-subtitle>
              </v-list-item>
            </v-list>
            <div v-else class="text-body-2 text-medium-emphasis mb-4">{{ t('sq_no_products') }}</div>
            <div class="text-subtitle-2 font-weight-bold mb-2">{{ t('sq_feedback') }}</div>
            <v-list v-if="selectedRow.insight.feedback?.length" density="compact" class="mb-3">
              <v-list-item v-for="(feedback, index) in selectedRow.insight.feedback" :key="`${feedback.category}-${index}`" prepend-icon="mdi-comment-quote-outline">
                <v-list-item-title>{{ feedback.category }} · {{ sentimentLabel(feedback.sentiment) }}</v-list-item-title>
                <v-list-item-subtitle>{{ feedback.evidence || t('sq_no_evidence') }}</v-list-item-subtitle>
              </v-list-item>
            </v-list>
            <div v-else class="text-body-2 text-medium-emphasis mb-4">{{ t('sq_no_feedback') }}</div>
            <div class="text-subtitle-2 font-weight-bold mb-2">{{ t('sq_lead_quality') }}</div>
            <v-alert v-if="selectedRow.insight.lead_quality" :type="leadAlertType(selectedRow.insight.lead_quality.level)" variant="tonal" density="compact">
              <strong>{{ leadLabel(selectedRow.insight.lead_quality.level) }}</strong>
              <div>{{ selectedRow.insight.lead_quality.reason || selectedRow.insight.lead_quality.evidence || t('sq_no_reason') }}</div>
              <div v-if="selectedRow.insight.lead_quality.reason && selectedRow.insight.lead_quality.evidence" class="text-caption mt-1">{{ selectedRow.insight.lead_quality.evidence }}</div>
            </v-alert>
          </template>
          <v-alert v-else type="info" variant="tonal" density="compact">
            {{ t('sq_no_analysis') }}
          </v-alert>

          <v-divider class="my-4" />
          <div class="text-subtitle-2 font-weight-bold mb-2">{{ t('sq_turns') }}</div>
          <v-table density="compact">
            <thead><tr><th>{{ t('sq_started') }}</th><th>{{ t('sq_status') }}</th><th>{{ t('sq_working_time') }}</th><th>{{ t('sq_customer_messages') }}</th></tr></thead>
            <tbody>
              <tr v-for="(turn, index) in selectedRow.turns" :key="index">
                <td>{{ formatDateTime(turn.started_at) }}</td>
                <td>{{ statusLabel(turn.status) }}</td>
                <td>{{ formatDuration(turn.seconds) }}</td>
                <td>{{ turn.customer_messages }}</td>
              </tr>
              <tr v-if="!selectedRow.turns.length"><td colspan="4" class="text-medium-emphasis text-center py-4">{{ t('sq_no_turns') }}</td></tr>
            </tbody>
          </v-table>
        </v-card-text>
        <v-card-actions>
          <span v-if="selectedRow.resolution_note" class="text-caption text-medium-emphasis px-2">{{ t('sq_resolution_note', { note: selectedRow.resolution_note }) }}</span>
          <v-spacer />
          <v-btn color="primary" :to="messageLink(selectedRow)">{{ t('sq_open_conversation') }}</v-btn>
        </v-card-actions>
      </v-card>
    </v-dialog>

    <v-dialog v-model="resolveDialog" max-width="520">
      <v-card>
        <v-card-title>{{ t('sq_mark_resolved') }}</v-card-title>
        <v-card-text>
          <p class="text-body-2 mb-3">
            {{ t('sq_resolve_description') }}
          </p>
          <v-textarea
            v-model="resolveNote"
            :label="t('sq_resolve_reason')"
            :placeholder="t('sq_resolve_placeholder')"
            rows="3"
            maxlength="500"
            counter
            autofocus
          />
        </v-card-text>
        <v-card-actions>
          <v-spacer />
          <v-btn variant="text" @click="resolveDialog = false">{{ t('sq_cancel') }}</v-btn>
          <v-btn color="success" :loading="resolving" :disabled="!resolveNote.trim()" @click="resolveConversation">{{ t('sq_confirm_resolved') }}</v-btn>
        </v-card-actions>
      </v-card>
    </v-dialog>

    <v-dialog v-model="policyDialog" max-width="560">
      <v-card>
        <v-card-title>{{ t('sq_policy_title') }}</v-card-title>
        <v-card-text>
          <v-alert type="info" variant="tonal" density="compact" class="mb-4">{{ t('sq_policy_description') }}</v-alert>
          <v-switch v-model="policyForm.all_day" :label="t('sq_all_day')" color="primary" />
          <v-row v-if="!policyForm.all_day">
            <v-col cols="6"><v-text-field v-model="policyForm.work_start" :label="t('sq_work_start')" type="time" /></v-col>
            <v-col cols="6"><v-text-field v-model="policyForm.work_end" :label="t('sq_work_end')" type="time" /></v-col>
          </v-row>
          <v-text-field v-model.number="policyForm.target_minutes" :label="t('sq_target_minutes')" type="number" min="1" max="1440" />
          <v-text-field v-model.number="policyForm.overdue_minutes" :label="t('sq_overdue_minutes')" type="number" min="1" max="1440" />
          <v-text-field v-model="policyForm.timezone" :label="t('sq_timezone')" :hint="t('sq_timezone_hint')" persistent-hint />
        </v-card-text>
        <v-card-actions>
          <v-spacer />
          <v-btn variant="text" @click="policyDialog = false">{{ t('sq_cancel') }}</v-btn>
          <v-btn color="primary" :loading="savingPolicy" :disabled="!policyValid" @click="savePolicy">{{ t('sq_save_policy') }}</v-btn>
        </v-card-actions>
      </v-card>
    </v-dialog>

    <v-snackbar v-model="snackbar" :color="snackColor" timeout="3500">{{ snackText }}</v-snackbar>
  </div>
</template>

<script setup lang="ts">
import MetaLabelsPanel from '../components/MetaLabelsPanel.vue'
import InsightWordcloudPanel from '../components/InsightWordcloudPanel.vue'
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useRoute } from 'vue-router'
import { useI18n } from 'vue-i18n'
import api from '../api'
import { useAuthStore } from '../stores/auth'
import {
  aggregateInsights,
  type ConversationInsight,
  type ServiceQualityStatus,
} from '../utils/service-quality'

interface Policy {
  timezone: string
  work_start: string
  work_end: string
  all_day: boolean
  target_minutes: number
  overdue_minutes: number
}

interface Page {
  id: string
  name: string
  last_sync_at: string | null
  last_sync_status: string
  is_active: boolean
}

interface Turn {
  started_at: string
  replied_at?: string
  last_customer_id: string
  last_customer_at: string
  seconds: number
  customer_messages: number
  first: boolean
  status: ServiceQualityStatus
}

interface Row {
  conversation_id: string
  customer_name: string
  channel_id: string
  channel_name: string
  last_message_at: string | null
  status: ServiceQualityStatus
  waiting?: Turn
  turns: Turn[]
  insight?: ConversationInsight
  insight_at?: string
  insight_job_id?: string
  insight_stale: boolean
  resolution_note?: string
  history_from?: string
}

interface Report {
  policy: Policy
  generated_at: string
  from: string
  to: string
  pages: Page[]
  summary: {
    answered: number
    on_time: number
    on_time_percent: number | null
    median_seconds: number | null
    p90_seconds: number | null
    first_median_seconds: number | null
    waiting: number
    overdue: number
    resolved: number
  }
  rows: Row[]
  invalid_timestamps: number
  conversations_scanned: number
}

const { t, locale } = useI18n()
const numberLocale = computed(() => locale.value === 'en' ? 'en-US' : 'vi-VN')
const route = useRoute()
const authStore = useAuthStore()
const tenantId = computed(() => route.params.tenantId as string)
const loading = ref(false)
const report = ref<Report | null>(null)
const pageCatalog = ref<Page[]>([])
const errorMessage = ref('')
const channelId = ref('')
const statusFilter = ref('all')
const search = ref('')
const conversationQueue = ref<HTMLElement | null>(null)
const conversationPage = ref(1)
const queueTitle = computed(() => statusFilter.value === 'overdue'
  ? t('sq_queue_overdue')
  : statusFilter.value === 'waiting' ? t('sq_queue_waiting') : t('sq_conversations'))
const detailDialog = ref(false)
const selectedRow = ref<Row | null>(null)
const resolveDialog = ref(false)
const resolveTarget = ref<Row | null>(null)
const resolveNote = ref('')
const resolving = ref(false)
const policyDialog = ref(false)
const savingPolicy = ref(false)
const reanalysingStale = ref(false)
const policyForm = ref<Policy>({ timezone: 'Asia/Ho_Chi_Minh', work_start: '08:00', work_end: '22:00', all_day: false, target_minutes: 5, overdue_minutes: 15 })
const snackbar = ref(false)
const snackText = ref('')
const snackColor = ref('success')
let refreshTimer: number | undefined
let activeController: AbortController | null = null
let requestSequence = 0

function localDate(offsetDays = 0) {
  const parts = new Intl.DateTimeFormat('en-CA', { timeZone: 'Asia/Ho_Chi_Minh', year: 'numeric', month: '2-digit', day: '2-digit' }).formatToParts(new Date())
  const part = (name: string) => parts.find(p => p.type === name)!.value
  const date = new Date(`${part('year')}-${part('month')}-${part('day')}T12:00:00Z`)
  date.setUTCDate(date.getUTCDate() + offsetDays)
  return date.toISOString().slice(0, 10)
}

const dateFrom = ref(localDate(-6))
const dateTo = ref(localDate())
const dateRangeValid = computed(() => Boolean(dateFrom.value && dateTo.value && dateFrom.value <= dateTo.value))
const canResolve = computed(() => authStore.canEdit('messages'))
const canEditPolicy = computed(() => authStore.canEdit('settings'))
const channelOptions = computed(() => [
  { title: t('sq_all_pages'), value: '' },
  ...pageCatalog.value.map(page => ({ title: page.name, value: page.id })),
])
const statusOptions = computed(() => [
  { title: t('sq_all_statuses'), value: 'all' },
  { title: t('sq_overdue'), value: 'overdue' },
  { title: t('sq_waiting'), value: 'waiting' },
  { title: t('sq_answered'), value: 'answered' },
  { title: t('sq_resolved'), value: 'resolved' },
  { title: t('sq_no_request'), value: 'no_request' },
])
const headers = computed(() => [
  { title: t('sq_customer_page'), key: 'customer_name', sortable: true },
  { title: t('sq_status'), key: 'status', sortable: true },
  { title: t('sq_waiting'), key: 'waiting', sortable: false },
  { title: t('sq_last_message'), key: 'last_message_at', sortable: true },
  { title: t('sq_ai_content'), key: 'insight', sortable: false },
  { title: '', key: 'actions', sortable: false, align: 'end' as const },
])

const filteredRows = computed(() => {
  const term = search.value.trim().toLocaleLowerCase(locale.value)
  return (report.value?.rows || []).filter(row => {
    if (statusFilter.value !== 'all' && row.status !== statusFilter.value) return false
    if (!term) return true
    return `${row.customer_name} ${row.channel_name} ${row.insight?.summary || ''}`.toLocaleLowerCase(locale.value).includes(term)
  })
})

const staleInsightGroups = computed(() => {
  const groups = new Map<string, string[]>()
  for (const row of report.value?.rows || []) {
    if (!row.insight_stale || !row.insight_job_id) continue
    const ids = groups.get(row.insight_job_id) || []
    ids.push(row.conversation_id)
    groups.set(row.insight_job_id, ids)
  }
  return groups
})
const staleInsightCount = computed(() => [...staleInsightGroups.value.values()].reduce((total, ids) => total + ids.length, 0))

watch([statusFilter, search, tenantId], () => { conversationPage.value = 1 })

async function viewQueue() {
  if (!report.value) return
  statusFilter.value = report.value.summary.overdue ? 'overdue' : 'waiting'
  search.value = ''
  conversationPage.value = 1
  await nextTick()
  conversationQueue.value?.scrollIntoView({
    behavior: window.matchMedia('(prefers-reduced-motion: reduce)').matches ? 'auto' : 'smooth',
    block: 'start',
  })
  conversationQueue.value?.focus({ preventScroll: true })
}

const insightAggregates = computed(() => report.value
  ? aggregateInsights(report.value.rows, report.value.from, report.value.to)
  : aggregateInsights([], new Date(0), new Date(0)))

const insightGroups = computed(() => [
  { title: t('sq_main_intents'), icon: 'mdi-cart-outline', kind: 'intent', items: insightAggregates.value.intents },
  { title: t('sq_products'), icon: 'mdi-package-variant-closed', kind: 'product', items: insightAggregates.value.products },
  { title: t('sq_customer_feedback'), icon: 'mdi-comment-quote-outline', kind: 'feedback', items: insightAggregates.value.feedback },
  { title: t('sq_lead_quality'), icon: 'mdi-account-star-outline', kind: 'lead', items: insightAggregates.value.leadQuality },
].map(group => ({ ...group, items: group.items.map(item => ({ ...item, label: displayAggregateLabel(group.kind, item.label) })) })))

const kpis = computed(() => {
  const summary = report.value!.summary
  const policy = report.value!.policy
  return [
    { label: t('sq_answered_period'), value: summary.answered.toLocaleString(numberLocale.value), hint: t('sq_sla_denominator'), icon: 'mdi-message-check-outline', color: 'primary' },
    { label: t('sq_on_time_target', { minutes: policy.target_minutes }), value: summary.on_time_percent == null ? '—' : `${summary.on_time_percent.toLocaleString(numberLocale.value, { minimumFractionDigits: 1, maximumFractionDigits: 1 })}%`, hint: t('sq_turn_count', { onTime: summary.on_time, answered: summary.answered }), icon: 'mdi-timer-check-outline', color: 'success' },
    { label: t('sq_median'), value: formatDuration(summary.median_seconds), hint: t('sq_period_turns'), icon: 'mdi-timer-outline', color: 'info' },
    { label: t('sq_p90'), value: formatDuration(summary.p90_seconds), hint: t('sq_p90_hint'), icon: 'mdi-chart-timeline-variant', color: 'deep-purple' },
    { label: t('sq_first_observed'), value: formatDuration(summary.first_median_seconds), hint: t('sq_synced_history'), icon: 'mdi-ray-start-arrow', color: 'indigo' },
    { label: t('sq_waiting_now'), value: summary.waiting.toLocaleString(numberLocale.value), hint: t('sq_waiting_hint'), icon: 'mdi-account-clock-outline', color: 'warning' },
    { label: t('sq_overdue_target', { minutes: policy.overdue_minutes }), value: summary.overdue.toLocaleString(numberLocale.value), hint: t('sq_current_queue'), icon: 'mdi-alert-circle-outline', color: 'error' },
    { label: t('sq_resolved_period'), value: summary.resolved.toLocaleString(numberLocale.value), hint: t('sq_not_response'), icon: 'mdi-check-decagram-outline', color: 'teal' },
  ]
})

const policyValid = computed(() => {
  const p = policyForm.value
  return Boolean(p.timezone && /^\d{2}:\d{2}$/.test(p.work_start) && /^\d{2}:\d{2}$/.test(p.work_end)
    && (p.all_day || p.work_start < p.work_end) && p.target_minutes >= 1 && p.overdue_minutes >= p.target_minutes && p.overdue_minutes <= 1440)
})

async function loadReport(force = false) {
  if (!dateRangeValid.value) return
  if (loading.value && !force) return
  if (force) activeController?.abort()
  const controller = new AbortController()
  activeController = controller
  const sequence = ++requestSequence
  loading.value = true
  errorMessage.value = ''
  try {
    const { data } = await api.get<Report>(`/tenants/${tenantId.value}/service-quality`, {
      params: { channel_id: channelId.value || undefined, from: dateFrom.value, to: dateTo.value },
      signal: controller.signal,
    })
    if (sequence !== requestSequence) return
    report.value = data
    pageCatalog.value = data.pages
    if (channelId.value && !data.pages.some(page => page.id === channelId.value)) channelId.value = ''
  } catch (error: any) {
    if (error?.code === 'ERR_CANCELED' || sequence !== requestSequence) return
    report.value = null
    if (Array.isArray(error?.response?.data?.pages)) pageCatalog.value = error.response.data.pages
    errorMessage.value = error?.response?.data?.error || t('sq_load_error')
  } finally {
    if (sequence === requestSequence) loading.value = false
  }
}

function openDetail(row: Row) {
  selectedRow.value = row
  detailDialog.value = true
}

function openResolve(row: Row) {
  resolveTarget.value = row
  resolveNote.value = ''
  resolveDialog.value = true
}

async function resolveConversation() {
  const target = resolveTarget.value
  if (!target?.waiting || !resolveNote.value.trim()) return
  resolving.value = true
  const targetTenant = tenantId.value
  try {
    await api.post(`/tenants/${targetTenant}/service-quality/${target.conversation_id}/resolve`, {
      through_message_id: target.waiting.last_customer_id,
      note: resolveNote.value.trim(),
    })
    if (tenantId.value !== targetTenant) return
    resolveDialog.value = false
    showSnack(t('sq_resolve_success'), 'success')
    await loadReport(true)
  } catch (error: any) {
    if (tenantId.value !== targetTenant) return
    const conflict = error?.response?.status === 409
    showSnack(error?.response?.data?.error || t('sq_resolve_error'), 'error')
    if (conflict) {
      resolveDialog.value = false
      await loadReport(true)
    }
  } finally {
    resolving.value = false
  }
}

async function reanalyseStaleInsights() {
  if (!staleInsightCount.value || reanalysingStale.value) return
  reanalysingStale.value = true
  const targetTenant = tenantId.value
  const groups = [...staleInsightGroups.value.entries()]
  const requestedCount = groups.reduce((total, [, ids]) => total + ids.length, 0)
  try {
    const requests = groups.map(([jobId, conversationIds]) =>
      api.post(`/tenants/${targetTenant}/jobs/${jobId}/reanalyse-stale`, { conversation_ids: conversationIds }),
    )
    await Promise.all(requests)
    if (tenantId.value !== targetTenant) return
    showSnack(t('sq_reanalyse_started', { count: requestedCount }), 'success')
  } catch (error: any) {
    if (tenantId.value !== targetTenant) return
    showSnack(error?.response?.data?.error || t('sq_reanalyse_error'), 'error')
  } finally {
    reanalysingStale.value = false
  }
}

function openPolicyDialog() {
  if (report.value) policyForm.value = { ...report.value.policy }
  policyDialog.value = true
}

async function savePolicy() {
  if (!policyValid.value) return
  savingPolicy.value = true
  const targetTenant = tenantId.value
  try {
    await api.put(`/tenants/${targetTenant}/service-quality/policy`, policyForm.value)
    if (tenantId.value !== targetTenant) return
    policyDialog.value = false
    showSnack(t('sq_policy_success'), 'success')
    await loadReport(true)
  } catch (error: any) {
    if (tenantId.value !== targetTenant) return
    showSnack(error?.response?.data?.error || t('sq_policy_error'), 'error')
  } finally {
    savingPolicy.value = false
  }
}

function messageLink(row: Row) {
  return { name: 'messages', params: { tenantId: tenantId.value }, query: { conv: row.conversation_id, channel_id: row.channel_id } }
}

function formatDuration(seconds: number | null | undefined): string {
  if (seconds == null || !Number.isFinite(seconds)) return '—'
  const total = Math.max(0, Math.round(seconds))
  const unit = (name: string, count: number) => t(`sq_${name}`, { count: count.toLocaleString(numberLocale.value) })
  if (total < 60) return unit('seconds', total)
  const minutes = Math.floor(total / 60)
  const remainingSeconds = total % 60
  if (minutes < 60) return remainingSeconds ? `${unit('minutes', minutes)} ${unit('seconds', remainingSeconds)}` : unit('minutes', minutes)
  const hours = Math.floor(minutes / 60)
  const remainingMinutes = minutes % 60
  return remainingMinutes ? `${unit('hours', hours)} ${unit('minutes', remainingMinutes)}` : unit('hours', hours)
}

function formatDateTime(value: string) {
  const date = new Date(value)
  return Number.isNaN(date.getTime()) ? '—' : date.toLocaleString(numberLocale.value, { dateStyle: 'short', timeStyle: 'short', timeZone: report.value?.policy.timezone || 'Asia/Ho_Chi_Minh' })
}

function statusLabel(status: ServiceQualityStatus) {
  return ({ answered: t('sq_answered'), waiting: t('sq_waiting'), overdue: t('sq_overdue'), resolved: t('sq_resolved'), no_request: t('sq_no_request') } as Record<string, string>)[status] || status
}

function statusColor(status: ServiceQualityStatus) {
  return ({ answered: 'success', waiting: 'warning', overdue: 'error', resolved: 'teal', no_request: 'grey' } as Record<string, string>)[status] || 'grey'
}

function sentimentLabel(sentiment: string) {
  return ({ positive: t('sq_positive'), neutral: t('sq_neutral'), negative: t('sq_negative'), mixed: t('sq_mixed'), unknown: t('sq_unknown_label') } as Record<string, string>)[sentiment] || sentiment
}

function leadLabel(level: string) {
  return ({ high: t('sq_lead_high'), medium: t('sq_lead_medium'), low: t('sq_lead_low'), spam: t('sq_spam'), unknown: t('sq_unknown_label') } as Record<string, string>)[level] || level
}

function leadAlertType(level: string): 'success' | 'info' | 'warning' | 'error' {
  return ({ high: 'success', medium: 'info', low: 'warning', spam: 'error', unknown: 'info' } as Record<string, any>)[level] || 'info'
}

function displayAggregateLabel(kind: string, label: string) {
  if (kind === 'lead') return leadLabel(label)
  if (kind === 'feedback') {
    const [category, sentiment] = label.split(' · ')
    return `${category} · ${sentimentLabel(sentiment)}`
  }
  return label
}

function syncColor(page: Page) {
  if (!page.is_active || page.last_sync_status === 'error') return 'error'
  if (!page.last_sync_at || page.last_sync_status === 'syncing') return 'warning'
  if (Date.now() - new Date(page.last_sync_at).getTime() > 30 * 60_000) return 'warning'
  return 'success'
}

function syncLabel(status: string) {
  return ({ success: t('sq_sync_success'), syncing: t('sq_syncing'), error: t('sq_sync_error'), pending: t('sq_sync_pending') } as Record<string, string>)[status] || status || t('sq_sync_unknown')
}

function showSnack(message: string, color: string) {
  snackText.value = message
  snackColor.value = color
  snackbar.value = true
}

watch(tenantId, () => {
  requestSequence++
  activeController?.abort()
  report.value = null
  pageCatalog.value = []
  detailDialog.value = false
  resolveDialog.value = false
  policyDialog.value = false
  selectedRow.value = null
  resolveTarget.value = null
  channelId.value = ''
  statusFilter.value = 'all'
  loadReport(true)
})

onMounted(() => {
  loadReport(true)
  refreshTimer = window.setInterval(() => loadReport(false), 60_000)
})

onBeforeUnmount(() => {
  requestSequence++
  activeController?.abort()
  if (refreshTimer) window.clearInterval(refreshTimer)
})
</script>

<style scoped>
.service-quality-page {
  max-width: 1680px;
  margin: 0 auto;
}

.kpi-card {
  min-height: 104px;
}

.conversation-queue {
  scroll-margin-top: 64px;
  border-radius: 4px;
}

.queue-title {
  font-size: 1rem !important;
  line-height: 1.75rem !important;
  letter-spacing: 0.009375em !important;
}

.conversation-queue:focus-visible {
  outline: 2px solid rgb(var(--v-theme-primary));
  outline-offset: 4px;
}

.panel-description {
  font-size: 13px;
  font-weight: 400;
  line-height: 1.5;
  letter-spacing: normal;
  white-space: normal;
  overflow-wrap: anywhere;
}

.service-quality-page :deep(.v-card-title) {
  white-space: normal;
  overflow-wrap: anywhere;
}

.service-quality-page :deep(.v-card-title > div) {
  min-width: 0;
  max-width: 100%;
}

.service-quality-page :deep(.v-chip) {
  max-width: 100%;
}

.service-quality-page :deep(.v-chip__content) {
  overflow: hidden;
  text-overflow: ellipsis;
}

@media (max-width: 600px) {
  :deep(.v-data-table__td),
  :deep(.v-data-table__th) {
    white-space: nowrap;
  }
}
</style>
