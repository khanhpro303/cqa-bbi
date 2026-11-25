<template>
  <v-card class="dashboard-chart dashboard-chart--cost">
    <div class="dashboard-chart__heading">
      <span class="dashboard-chart__icon" aria-hidden="true">
        <v-icon size="20">mdi-chart-line</v-icon>
      </span>
      <h2>{{ title }}</h2>
      <span class="cost-currency">{{ locale === 'vi' ? 'VNĐ' : 'VND' }}</span>
    </div>

    <div class="cost-views" role="group" :aria-label="$t('ai_cost')">
      <button
        v-for="(option, key) in views"
        :key="key"
        type="button"
        :aria-pressed="view === key"
        @click="view = key"
      >
        {{ $t(option.label) }}
      </button>
    </div>

    <div class="cost-filters">
      <div class="cost-filter" role="group" :aria-label="$t('cost_source')">
        <span class="cost-filter__label">{{ $t('cost_source') }}</span>
        <div class="cost-filter__options">
          <button
            v-for="(label, key) in sources"
            :key="key"
            type="button"
            :aria-pressed="source === key"
            @click="source = key"
          >
            {{ $t(label) }}
          </button>
        </div>
      </div>
      <div class="cost-filter" role="group" :aria-label="$t('cost_segment')">
        <span class="cost-filter__label">{{ $t('cost_segment') }}</span>
        <div class="cost-filter__options">
          <button
            v-for="(label, key) in segments"
            :key="key"
            type="button"
            :aria-pressed="segment === key"
            @click="segment = key"
          >
            {{ $t(label) }}
          </button>
        </div>
      </div>
    </div>

    <div v-if="loading" class="dashboard-chart__state" role="status">
      <v-progress-circular indeterminate size="24" width="2" />
      <span>{{ $t('loading') }}</span>
    </div>
    <div v-else-if="activeRows === null" class="dashboard-chart__state" role="status">
      <span>{{ $t('dashboard_chart_load_error') }}</span>
      <v-btn size="small" variant="tonal" @click="reload++">{{ $t('retry') }}</v-btn>
    </div>
    <div v-else-if="!activeRows.length" class="dashboard-chart__state" role="status">
      <v-icon size="28" aria-hidden="true">mdi-chart-box-outline</v-icon>
      <span>{{ $t(view === 'employee' && source === 'job' ? 'cost_employee_chatbot_only' : 'no_data') }}</span>
    </div>
    <DailyTrendChart v-else-if="view === 'day'" :label="title" :dates="dates" :series="series" currency />
    <ol v-else class="cost-ranking" role="list" :aria-label="title" tabindex="0">
      <li v-for="(row, index) in rankedRows" :key="index" class="cost-ranking__row">
        <div class="cost-ranking__caption">
          <span class="cost-ranking__name">{{ row.label }}</span>
          <strong>{{ currency.format(row.cost) }}</strong>
        </div>
        <div class="cost-ranking__track" aria-hidden="true">
          <span :style="{ width: row.share + '%' }" />
        </div>
      </li>
    </ol>
  </v-card>
</template>

<script setup lang="ts">
import { ref, computed, watch, reactive } from 'vue'
import { useI18n } from 'vue-i18n'
import api from '../../api'
import DailyTrendChart from './DailyTrendChart.vue'

interface CostRow {
  label: string
  total_cost: number
}

const props = withDefaults(
  defineProps<{ tenantId: string; exchangeRate?: number }>(),
  { exchangeRate: 26000 },
)

const views = {
  day: { label: 'cost_view_day', title: 'cost_by_day_chart' },
  channel: { label: 'cost_view_channel', title: 'cost_by_channel_chart' },
  employee: { label: 'cost_view_employee', title: 'cost_by_employee_chart' },
} as const

const sources = { all: 'source_all', job: 'source_job', chatbot: 'source_chatbot' } as const
const segments = { all: 'segment_all', public: 'segment_public', private: 'segment_private' } as const

type GroupBy = keyof typeof views
const groupings = Object.keys(views) as GroupBy[]
const { t, locale } = useI18n()

const view = ref<GroupBy>('day')
const source = ref<keyof typeof sources>('all')
const segment = ref<keyof typeof segments>('all')
const loading = ref(true)
const reload = ref(0)
const rows = reactive<Record<GroupBy, CostRow[] | null>>({ day: [], channel: [], employee: [] })

const title = computed(() => t(views[view.value].title))
const currency = computed(() => new Intl.NumberFormat(locale.value, { style: 'currency', currency: 'VND' }))
const activeRows = computed(() => rows[view.value])
const dates = computed(() => rows.day?.map(row => row.label) ?? [])
const series = computed(() => [{ label: t('cost'), values: rows.day?.map(row => toVnd(row.total_cost)) ?? [] }])
const rankedRows = computed(() => {
  const entries = (activeRows.value ?? []).map(row => ({ label: row.label, cost: toVnd(row.total_cost) }))
  const max = entries.reduce((highest, row) => Math.max(highest, row.cost), 0)

  return entries.map(row => ({ ...row, share: max > 0 ? Math.max(0, row.cost) / max * 100 : 0 }))
})

function toVnd(usd: number): number {
  return Math.round(usd * (props.exchangeRate || 26000))
}

watch(
  [() => props.tenantId, source, segment, reload],
  async ([tenantId, selectedSource, selectedSegment], _, onCleanup) => {
    const controller = new AbortController()
    onCleanup(() => controller.abort())
    loading.value = true

    const results = await Promise.allSettled(groupings.map(groupBy => api.get<{ rows?: CostRow[] | null }>(
      `/tenants/${tenantId}/analytics/ai-cost`,
      { params: { group_by: groupBy, source: selectedSource, segment: selectedSegment }, signal: controller.signal },
    )))
    if (controller.signal.aborted) return

    results.forEach((result, index) => {
      rows[groupings[index]] = result.status === 'fulfilled' ? result.value.data?.rows ?? [] : null
    })
    loading.value = false
  },
  { immediate: true },
)
</script>

<style scoped>
.dashboard-chart--cost.v-card {
  --chart-accent: #91420b;
  --chart-tint: rgba(191, 91, 20, 0.10);
  --ranking-bar: #c46c31;
}

:global(.v-theme--dark) .dashboard-chart--cost.v-card {
  --chart-accent: #ffc38c;
  --chart-tint: rgba(255, 180, 99, 0.14);
  --ranking-bar: #e0a46e;
}

.cost-currency {
  margin-inline-start: auto;
  color: rgba(var(--v-theme-on-surface), 0.68);
  font-size: 0.75rem;
  font-weight: 600;
}

.cost-views {
  display: grid;
  grid-template-columns: repeat(3, minmax(0, 1fr));
  gap: 4px;
  padding: 4px;
  border-radius: 10px;
  background: rgba(var(--v-theme-on-surface), 0.05);
}

.cost-views button,
.cost-filter__options button {
  appearance: none;
  min-height: 34px;
  border: 1px solid transparent;
  background: transparent;
  color: inherit;
  padding: 4px 8px;
  cursor: pointer;
  font-size: 0.8125rem;
  font-weight: 600;
  line-height: 1.25;
}

.cost-views button {
  border-radius: 7px;
}

.cost-views button[aria-pressed='true'] {
  border-color: rgba(var(--v-theme-on-surface), 0.09);
  background: rgb(var(--v-theme-surface));
  color: var(--chart-accent);
  box-shadow: 0 1px 2px rgba(0, 0, 0, 0.08);
}

.cost-filters {
  display: grid;
  row-gap: 4px;
  margin: 12px 0 14px;
}

.cost-filter {
  display: grid;
  grid-template-columns: 65px minmax(0, 1fr);
  gap: 4px 8px;
}

@media (max-width: 480px) {
  .cost-filter {
    grid-template-columns: minmax(0, 1fr);
  }

  .cost-views button {
    padding-inline: 4px;
  }
}

.cost-filter__label {
  align-self: center;
  font-size: 0.75rem;
  font-weight: 600;
  color: rgba(var(--v-theme-on-surface), 0.68);
}

.cost-filter__options {
  display: flex;
  flex-wrap: wrap;
  gap: 4px;
}

.cost-filter__options button {
  border-color: rgba(var(--v-theme-on-surface), 0.12);
  border-radius: 7px;
  font-weight: 500;
}

.cost-filter__options button[aria-pressed='true'] {
  border-color: currentColor;
  background: var(--chart-tint);
  color: var(--chart-accent);
  font-weight: 600;
}

.cost-views button:hover:not([aria-pressed='true']),
.cost-filter__options button:hover:not([aria-pressed='true']) {
  background: rgba(var(--v-theme-on-surface), 0.08);
}

.cost-views button:focus-visible,
.cost-filter__options button:focus-visible,
.cost-ranking:focus-visible {
  outline: 2px solid var(--chart-accent);
  outline-offset: 2px;
}

.cost-ranking {
  flex: 1;
  min-height: 240px;
  max-height: 286px;
  overflow-y: auto;
  margin: 0;
  padding: 3px 6px 3px 0;
  list-style: none;
}

.cost-ranking__row + .cost-ranking__row {
  margin-top: 14px;
}

.cost-ranking__caption {
  display: flex;
  justify-content: space-between;
  gap: 12px;
  margin-bottom: 6px;
  font-size: 0.8125rem;
  line-height: 1.35;
}

.cost-ranking__name {
  overflow-wrap: anywhere;
}

.cost-ranking__caption strong {
  flex-shrink: 0;
  font-variant-numeric: tabular-nums;
}

.cost-ranking__track {
  height: 7px;
  border-radius: 6px;
  overflow: hidden;
  background: rgba(var(--v-theme-on-surface), 0.07);
}

.cost-ranking__track span {
  display: block;
  height: 100%;
  border-radius: inherit;
  background: var(--ranking-bar);
}
</style>
