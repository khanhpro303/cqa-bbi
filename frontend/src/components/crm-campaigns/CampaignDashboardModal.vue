<template>
  <v-dialog v-model="open" max-width="960" scrollable>
    <v-card :loading="loading">
      <v-card-title class="d-flex align-center py-3">
        <v-icon color="primary" class="mr-2">mdi-chart-box-outline</v-icon>
        {{ $t('campaign_dashboard') }}
        <v-spacer />
        <v-select
          v-model="month"
          :items="monthOptions"
          density="compact"
          variant="outlined"
          hide-details
          style="max-width: 170px"
          @update:model-value="load"
        />
        <v-btn icon="mdi-close" variant="text" size="small" class="ml-2" @click="open = false" />
      </v-card-title>
      <v-divider />

      <v-card-text style="max-height: 74vh">
        <!-- Stat cards -->
        <v-row dense class="mb-2">
          <v-col cols="6" md="3">
            <v-card variant="tonal" color="primary" class="stat-card">
              <div class="stat-num">{{ stats?.campaignsThisMonth ?? '—' }}</div>
              <div class="stat-label">{{ $t('dash_campaigns_month') }}</div>
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
            <Bar v-if="chartData.labels.length" :data="chartData" :options="chartOptions" />
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
      </v-card-text>
    </v-card>
  </v-dialog>
</template>

<script setup lang="ts">
import { ref, computed, watch } from 'vue'
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
import { getStats } from './mockCampaigns'
import type { CampaignStats, CampaignStatus } from './types'

ChartJS.register(CategoryScale, LinearScale, BarElement, Title, Tooltip, Legend)

const { t } = useI18n()

const props = defineProps<{ modelValue: boolean }>()
const emit = defineEmits<{ 'update:modelValue': [boolean] }>()

const open = ref(props.modelValue)
watch(() => props.modelValue, (v) => { open.value = v; if (v) load() })
watch(open, (v) => emit('update:modelValue', v))

const loading = ref(false)
const stats = ref<CampaignStats | null>(null)

// Month dropdown: current + previous 5 months as YYYY-MM.
const monthOptions = computed(() => {
  const out: { title: string; value: string }[] = []
  const now = new Date()
  for (let i = 0; i < 6; i++) {
    const d = new Date(now.getFullYear(), now.getMonth() - i, 1)
    const value = `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, '0')}`
    out.push({ title: `${t('cron_at') === 'lúc' ? 'Tháng' : 'Month'} ${value}`, value })
  }
  return out
})
const month = ref(monthOptions.value[0]?.value ?? '')

async function load() {
  loading.value = true
  try {
    stats.value = await getStats(month.value)
  } finally {
    loading.value = false
  }
}

const chartData = computed(() => ({
  labels: (stats.value?.byDay ?? []).map((d) => formatChartDate(d.date)),
  datasets: [
    {
      label: t('dash_messages_sent'),
      data: (stats.value?.byDay ?? []).map((d) => d.sent),
      backgroundColor: 'rgba(211,47,47,0.65)',
      borderRadius: 4,
    },
  ],
}))

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
</style>
