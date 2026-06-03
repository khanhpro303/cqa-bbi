<template>
  <div>
    <!-- Header row: optional title + time filter (presets + from/to range) -->
    <div class="d-flex align-center flex-wrap mb-3 ga-2">
      <slot name="title" />
      <v-spacer class="d-none d-md-block" />
      <v-chip-group :model-value="datePreset" selected-class="text-primary">
        <v-chip
          v-for="p in presets"
          :key="p.value"
          :value="p.value"
          size="small"
          variant="outlined"
          @click="emit('preset', p.value)"
        >
          {{ p.label }}
        </v-chip>
      </v-chip-group>
      <v-text-field
        :model-value="dateFrom"
        type="date"
        density="compact"
        variant="outlined"
        hide-details
        class="date-time-field"
        style="max-width: 165px"
        @update:model-value="(v: string) => emit('update:dateFrom', v)"
      />
      <v-text-field
        :model-value="dateTo"
        type="date"
        density="compact"
        variant="outlined"
        hide-details
        class="date-time-field"
        style="max-width: 165px"
        @update:model-value="(v: string) => emit('update:dateTo', v)"
      />
    </div>

    <!-- Stat cards -->
    <v-row dense class="mb-2">
      <v-col cols="6" md="3">
        <v-card variant="tonal" color="primary" class="stat-card">
          <div class="stat-num">{{ stats?.campaignsThisMonth ?? '—' }}</div>
          <div class="stat-label">{{ firstCardLabel || $t('dash_campaigns_month') }}</div>
        </v-card>
      </v-col>
      <v-col cols="6" md="3">
        <v-card variant="tonal" color="teal" class="stat-card">
          <div class="stat-num">{{ (stats?.messagesSentThisMonth ?? 0).toLocaleString() }}</div>
          <div class="stat-label">{{ $t('dash_messages_sent') }}</div>
        </v-card>
      </v-col>
      <v-col cols="6" md="3">
        <v-card variant="tonal" color="success" class="stat-card">
          <div class="stat-num">{{ stats ? stats.successRate.toFixed(1) + '%' : '—' }}</div>
          <div class="stat-label">{{ $t('dash_success_rate') }}</div>
        </v-card>
      </v-col>
      <v-col cols="6" md="3">
        <v-card variant="tonal" color="indigo" class="stat-card">
          <div class="stat-num">{{ stats?.upcomingRuns ?? '—' }}</div>
          <div class="stat-label">{{ $t('dash_upcoming') }}</div>
        </v-card>
      </v-col>
    </v-row>

    <!-- By-day chart -->
    <v-card variant="outlined" class="pa-3 mb-3">
      <div class="text-body-2 font-weight-bold mb-2">{{ $t('dash_by_day') }}</div>
      <div style="height: 240px">
        <Bar v-if="hasChartData" :data="chartData" :options="chartOptions" />
        <div v-else class="chart-empty">
          <v-icon size="48" color="grey-lighten-1">mdi-chart-bar</v-icon>
          <div class="chart-empty-text">{{ $t('dash_no_chart_data') }}</div>
        </div>
      </div>
    </v-card>

    <!-- Recent campaigns -->
    <div class="text-body-2 font-weight-bold mb-2">{{ $t('dash_recent') }}</div>
    <v-table density="compact">
      <thead>
        <tr>
          <th>{{ $t('campaign_name') }}</th>
          <th>{{ $t('status') }}</th>
          <th class="text-right">{{ $t('campaign_sent_month') }}</th>
          <th class="text-right">{{ $t('dash_fail') }}</th>
          <th>{{ $t('dash_last_run') }}</th>
        </tr>
      </thead>
      <tbody>
        <tr v-for="r in stats?.recent || []" :key="r.id">
          <td class="font-weight-medium">{{ r.name }}</td>
          <td>
            <v-chip :color="statusColor(r.status)" size="x-small" variant="flat">
              {{ $t('campaign_status_' + r.status) }}
            </v-chip>
          </td>
          <td class="text-right">{{ r.sent.toLocaleString() }}</td>
          <td class="text-right text-error">{{ r.fail.toLocaleString() }}</td>
          <td class="text-caption">{{ formatDateTime(r.lastRunAt) }}</td>
        </tr>
        <tr v-if="!loading && (stats?.recent?.length ?? 0) === 0">
          <td colspan="5" class="text-center py-6 text-grey text-body-2">{{ $t('no_data') }}</td>
        </tr>
      </tbody>
    </v-table>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { Bar } from 'vue-chartjs'
import {
  Chart as ChartJS,
  CategoryScale,
  LinearScale,
  BarElement,
  Title,
  Tooltip,
  Legend,
} from 'chart.js'
import type { CampaignStats, CampaignStatus } from './types'

ChartJS.register(CategoryScale, LinearScale, BarElement, Title, Tooltip, Legend)

const { t } = useI18n()

interface DatePreset {
  label: string
  value: string
}

const props = defineProps<{
  stats: CampaignStats | null
  loading: boolean
  dateFrom: string
  dateTo: string
  datePreset: string
  presets: DatePreset[]
  firstCardLabel?: string
}>()

const emit = defineEmits<{
  preset: [string]
  'update:dateFrom': [string]
  'update:dateTo': [string]
}>()

const chartData = computed(() => ({
  labels: (props.stats?.byDay ?? []).map((d) => formatChartDate(d.date)),
  datasets: [
    {
      label: t('dash_messages_sent'),
      data: (props.stats?.byDay ?? []).map((d) => d.sent),
      backgroundColor: 'rgba(211,47,47,0.65)',
      borderRadius: 4,
    },
  ],
}))

// Treat "no rows" and "all-zero rows" alike — both should show the empty mask
// instead of a flat baseline chart.
const hasChartData = computed(() => {
  const days = props.stats?.byDay ?? []
  return days.length > 0 && days.some((d) => d.sent > 0)
})

const chartOptions = {
  responsive: true,
  maintainAspectRatio: false,
  plugins: { legend: { display: false } },
  scales: { y: { beginAtZero: true } },
}

function formatChartDate(dateStr: string): string {
  const parts = dateStr.split('T')[0].split('-')
  if (parts.length === 3) return `${parseInt(parts[2])}/${parseInt(parts[1])}`
  return dateStr
}
function formatDateTime(iso?: string): string {
  if (!iso) return '—'
  const d = new Date(iso)
  const pad = (n: number) => String(n).padStart(2, '0')
  return `${pad(d.getDate())}/${pad(d.getMonth() + 1)} ${pad(d.getHours())}:${pad(d.getMinutes())}`
}
function statusColor(s: CampaignStatus): string {
  return { draft: 'grey', active: 'success', paused: 'warning', done: 'blue-grey' }[s]
}
</script>

<style scoped>
.stat-card {
  padding: 14px 16px;
  text-align: center;
}
.stat-num {
  font-size: 1.6rem;
  font-weight: 800;
  line-height: 1.1;
}
.stat-label {
  font-size: 0.75rem;
  opacity: 0.85;
  margin-top: 4px;
}

/* Empty-state mask for the by-day chart. */
.chart-empty {
  height: 100%;
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: 8px;
  border: 1px dashed rgba(var(--v-border-color), 0.4);
  border-radius: 8px;
  background: rgba(var(--v-theme-on-surface), 0.02);
}
.chart-empty-text {
  font-size: 0.85rem;
  color: rgba(var(--v-theme-on-surface), 0.55);
}

/* Give the native calendar picker icon room from the field border. */
.date-time-field :deep(input[type='date']) {
  padding-right: 4px;
}
.date-time-field :deep(input[type='date']::-webkit-calendar-picker-indicator) {
  margin-inline-start: 8px;
  margin-inline-end: 2px;
  opacity: 0.65;
  cursor: pointer;
}
.date-time-field :deep(input[type='date']::-webkit-calendar-picker-indicator:hover) {
  opacity: 1;
}
</style>
