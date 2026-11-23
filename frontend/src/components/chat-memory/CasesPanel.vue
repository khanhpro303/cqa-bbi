<template>
  <div>
    <div class="d-flex flex-wrap align-center ga-2 mb-3">
      <v-chip-group v-model="status" mandatory selected-class="text-primary" data-testid="case-status-filter" @update:model-value="load(1)">
        <v-chip v-for="s in statusTabs" :key="s.value" :value="s.value" variant="outlined" size="small" filter>
          {{ s.title }}<span v-if="s.count !== undefined" class="ml-1 text-medium-emphasis">({{ s.count }})</span>
        </v-chip>
      </v-chip-group>
    </div>
    <v-row v-if="!compact" dense class="mb-2">
      <v-col cols="12" md="6">
        <v-text-field
          v-model="query"
          :label="$t('cc_search')"
          prepend-inner-icon="mdi-magnify"
          density="compact"
          clearable
          hide-details
          @update:model-value="debouncedLoad"
        />
      </v-col>
      <v-col cols="6" md="3">
        <v-select v-model="type" :items="typeOptions" :label="$t('cc_type')" density="compact" hide-details @update:model-value="load(1)" />
      </v-col>
      <v-col cols="6" md="3">
        <v-select v-model="severity" :items="severityOptions" :label="$t('cc_severity')" density="compact" hide-details @update:model-value="load(1)" />
      </v-col>
    </v-row>

    <v-table density="comfortable" hover>
      <thead>
        <tr>
          <th>{{ $t('cc_severity') }}</th>
          <th>{{ $t('cc_issue') }}</th>
          <th v-if="!compact">{{ $t('cm_customer') }}</th>
          <th>{{ $t('cc_opened') }}</th>
          <th class="text-center">{{ $t('cc_followups') }}</th>
          <th>{{ $t('cm_status') }}</th>
          <th v-if="!compact">{{ $t('cc_assignee') }}</th>
        </tr>
      </thead>
      <tbody>
        <tr v-for="c in cases" :key="c.id" class="cursor-pointer" :data-testid="`case-${c.id}`" @click="open(c)">
          <td><v-chip size="x-small" :color="severityColor(c.severity)">{{ severityLabel(c.severity) }}</v-chip></td>
          <td class="title-cell">
            <div class="font-weight-medium text-truncate">{{ c.title }}</div>
            <div class="text-caption text-medium-emphasis">
              {{ $t(issueTypeLabelKey(c.type)) }}
              <v-icon v-if="parseCaseImages(c.images).length" size="x-small" class="ml-1">mdi-image-outline</v-icon>
              <span v-if="parseCaseImages(c.images).length">{{ parseCaseImages(c.images).length }}</span>
            </div>
          </td>
          <td v-if="!compact" class="customer-cell">
            <div>{{ c.customer_name || c.reporter_name || c.subject_id }}</div>
            <div class="text-caption text-medium-emphasis">
              <span v-if="c.customer_code">{{ c.customer_code }} · </span>{{ c.subject_type === 'group' ? $t('cm_group_chat') : $t('cm_direct_chat') }}
            </div>
          </td>
          <td class="text-no-wrap">
            <div>{{ formatDateTime(c.created_at) }}</div>
            <div v-if="isActive(c.status)" class="text-caption" :class="waitingHours(c.created_at) >= 24 ? 'text-error' : 'text-medium-emphasis'">
              {{ $t('cc_waiting', { d: waitingLabel(c.created_at) }) }}
            </div>
          </td>
          <td class="text-center">
            <v-chip v-if="c.followup_count" size="x-small" color="warning" variant="tonal">{{ c.followup_count }}</v-chip>
            <span v-else class="text-medium-emphasis">—</span>
          </td>
          <td><v-chip size="small" variant="tonal" :color="caseStatusColor(c.status)">{{ $t(`cc_status_${c.status}`) }}</v-chip></td>
          <td v-if="!compact">{{ c.assignee_name || '—' }}</td>
        </tr>
        <tr v-if="!loading && cases.length === 0">
          <td :colspan="compact ? 5 : 7" class="text-center text-medium-emphasis py-6">{{ $t('cc_no_cases') }}</td>
        </tr>
      </tbody>
    </v-table>
    <div class="d-flex justify-end mt-2">
      <v-pagination v-if="total > perPage" v-model="page" :length="Math.ceil(total / perPage)" density="compact" @update:model-value="load" />
    </div>

    <CaseDialog v-model="dialog" :tenant-id="tenantId" :case-id="selectedId" @saved="load()" @open-session="id => $emit('open-session', id)" />
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import api from '../../api'
import CaseDialog from './CaseDialog.vue'
import {
  type CustomerCase, caseTypes, caseStatusColor, issueTypeLabelKey, parseCaseImages, severityColor, waitingHours,
} from './labels'

const props = defineProps<{
  tenantId: string
  compact?: boolean
  subjectType?: string
  subjectId?: string
  customerCode?: string
}>()
defineEmits<{ (e: 'open-session', id: string): void }>()

const { t } = useI18n()
const perPage = 20
const cases = ref<CustomerCase[]>([])
const total = ref(0)
const page = ref(1)
const counts = ref<Record<string, number>>({})
const loading = ref(false)
const status = ref(props.compact ? 'all' : 'active')
const query = ref('')
const type = ref('')
const severity = ref('')
const dialog = ref(false)
const selectedId = ref('')

const statusTabs = computed(() => {
  const c = counts.value
  const active = (c.open || 0) + (c.in_progress || 0)
  const all = Object.values(c).reduce((a, b) => a + b, 0)
  return [
    { value: 'active', title: t('cc_status_active'), count: active },
    { value: 'open', title: t('cc_status_open'), count: c.open || 0 },
    { value: 'in_progress', title: t('cc_status_in_progress'), count: c.in_progress || 0 },
    { value: 'resolved', title: t('cc_status_resolved'), count: c.resolved || 0 },
    { value: 'cancelled', title: t('cc_status_cancelled'), count: c.cancelled || 0 },
    { value: 'all', title: t('cc_status_all'), count: all },
  ]
})
const typeOptions = computed(() => [
  { title: t('all'), value: '' },
  ...caseTypes.map(v => ({ title: t(issueTypeLabelKey(v)), value: v })),
])
const severityOptions = computed(() => [
  { title: t('all'), value: '' },
  ...['high', 'medium', 'low'].map(v => ({ title: t(`cm_severity_${v}`), value: v })),
])

async function load(p = page.value) {
  page.value = p
  loading.value = true
  try {
    const { data } = await api.get(`/tenants/${props.tenantId}/customer-cases`, {
      params: {
        page: p, per_page: perPage, status: status.value,
        q: query.value || undefined, type: type.value || undefined, severity: severity.value || undefined,
        subject_type: props.subjectType || undefined, subject_id: props.subjectId || undefined,
        customer_code: props.customerCode || undefined,
      },
    })
    cases.value = data?.data || []
    total.value = data?.total || 0
    counts.value = data?.counts || {}
  } catch {
    cases.value = []
  } finally {
    loading.value = false
  }
}

let timer: ReturnType<typeof setTimeout> | undefined
function debouncedLoad() {
  clearTimeout(timer)
  timer = setTimeout(() => load(1), 300)
}

function open(c: CustomerCase) {
  selectedId.value = c.id
  dialog.value = true
}

function isActive(s: string) { return s === 'open' || s === 'in_progress' }
function severityLabel(s: string) { const k = `cm_severity_${s}`; const v = t(k); return v === k ? s : v }
function waitingLabel(createdAt: string) {
  const h = waitingHours(createdAt)
  if (h < 1) return t('cc_minutes', { n: Math.max(1, Math.round(h * 60)) })
  if (h < 48) return t('cc_hours', { n: Math.floor(h) })
  return t('cc_days', { n: Math.floor(h / 24) })
}
function formatDateTime(v?: string | null) {
  if (!v) return '—'
  return new Date(v).toLocaleString('vi-VN', { hour: '2-digit', minute: '2-digit', day: '2-digit', month: '2-digit' })
}

watch(() => [props.subjectId, props.customerCode], () => load(1))
onMounted(() => load(1))

defineExpose({ load, cases, status })
</script>

<style scoped>
.title-cell { max-width: 420px; }
.customer-cell { min-width: 180px; }
.cursor-pointer { cursor: pointer; }
</style>
