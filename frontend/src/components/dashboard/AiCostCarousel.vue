<template>
  <v-card class="pa-4 h-100 d-flex flex-column">
    <!-- Header + filters -->
    <div class="d-flex flex-wrap align-center justify-space-between mb-2 flex-shrink-0 ga-2">
      <div class="text-subtitle-1 font-weight-bold">
        <v-icon start size="small" color="warning">mdi-chart-multiple</v-icon>
        {{ slideTitle }}
      </div>
      <div class="d-flex flex-wrap ga-3 align-center">
        <div class="d-flex align-center">
          <span class="text-caption text-grey mr-1">{{ $t('cost_source') }}:</span>
          <v-chip-group v-model="source" mandatory selected-class="text-warning" density="comfortable">
            <v-chip size="x-small" value="all" variant="tonal">{{ $t('source_all') }}</v-chip>
            <v-chip size="x-small" value="job" variant="tonal">{{ $t('source_job') }}</v-chip>
            <v-chip size="x-small" value="chatbot" variant="tonal">{{ $t('source_chatbot') }}</v-chip>
          </v-chip-group>
        </div>
        <div class="d-flex align-center">
          <span class="text-caption text-grey mr-1">{{ $t('cost_segment') }}:</span>
          <v-chip-group v-model="segment" mandatory selected-class="text-warning" density="comfortable">
            <v-chip size="x-small" value="all" variant="tonal">{{ $t('segment_all') }}</v-chip>
            <v-chip size="x-small" value="public" variant="tonal">{{ $t('segment_public') }}</v-chip>
            <v-chip size="x-small" value="private" variant="tonal">{{ $t('segment_private') }}</v-chip>
          </v-chip-group>
        </div>
      </div>
    </div>

    <!-- Auto-rotating carousel of chart topics; pauses on hover -->
    <div
      class="flex-grow-1"
      style="min-height: 270px"
      @mouseenter="paused = true"
      @mouseleave="paused = false"
    >
      <v-carousel
        v-model="slide"
        :cycle="!paused"
        :interval="6000"
        height="270"
        hide-delimiter-background
        show-arrows="hover"
        progress="warning"
      >
        <v-carousel-item v-for="(s, i) in slides" :key="s.key" :value="i">
          <div class="px-2 pt-2" style="height: 240px">
            <component
              :is="s.component"
              v-if="s.hasData"
              :data="s.data"
              :options="s.options"
            />
            <div
              v-else
              class="text-center text-grey pa-4 h-100 d-flex flex-column justify-center align-center"
            >
              <v-icon size="40" color="grey-lighten-2" class="mb-2">mdi-chart-box-outline</v-icon>
              <div>{{ $t('no_data') }}</div>
            </div>
          </div>
        </v-carousel-item>
      </v-carousel>
    </div>
  </v-card>
</template>

<script setup lang="ts">
import { ref, computed, watch, onMounted } from 'vue'
import { useI18n } from 'vue-i18n'
import { Line, Bar } from 'vue-chartjs'
import {
  Chart as ChartJS,
  CategoryScale,
  LinearScale,
  PointElement,
  LineElement,
  BarElement,
  Title,
  Tooltip,
  Filler,
  Legend,
} from 'chart.js'
import api from '../../api'

ChartJS.register(
  CategoryScale,
  LinearScale,
  PointElement,
  LineElement,
  BarElement,
  Title,
  Tooltip,
  Filler,
  Legend
)

interface CostRow {
  label: string
  total_cost: number
  input_tokens: number
  output_tokens: number
  call_count: number
}

const props = withDefaults(
  defineProps<{ tenantId: string; exchangeRate?: number }>(),
  { exchangeRate: 26000 }
)

const { t } = useI18n()

const source = ref<'all' | 'job' | 'chatbot'>('all')
const segment = ref<'all' | 'public' | 'private'>('all')
const slide = ref(0)
const paused = ref(false)

const dayRows = ref<CostRow[]>([])
const channelRows = ref<CostRow[]>([])
const employeeRows = ref<CostRow[]>([])

const ORANGE = '#FFA726'

function toVnd(usd: number): number {
  return Math.round(usd * (props.exchangeRate || 26000))
}

function formatDay(d: string): string {
  // label arrives as YYYY-MM-DD; show DD/MM
  const parts = d.split('-')
  return parts.length === 3 ? `${parts[2]}/${parts[1]}` : d
}

const tooltipVnd = {
  callbacks: {
    label: (ctx: { parsed: { x?: number | null; y?: number | null } }) => {
      const v = ctx.parsed.y ?? ctx.parsed.x ?? 0
      return ` ${v.toLocaleString('vi-VN')}đ`
    },
  },
}

const dayData = computed(() => ({
  labels: dayRows.value.map((r) => formatDay(r.label)),
  datasets: [
    {
      label: t('cost'),
      data: dayRows.value.map((r) => toVnd(r.total_cost)),
      borderColor: ORANGE,
      backgroundColor: 'rgba(255,167,38,0.12)',
      fill: true,
      tension: 0.3,
    },
  ],
}))

const channelData = computed(() => ({
  labels: channelRows.value.map((r) => r.label),
  datasets: [
    {
      label: t('cost'),
      data: channelRows.value.map((r) => toVnd(r.total_cost)),
      backgroundColor: '#42A5F5',
      borderRadius: 6,
    },
  ],
}))

const employeeData = computed(() => ({
  labels: employeeRows.value.map((r) => r.label),
  datasets: [
    {
      label: t('cost'),
      data: employeeRows.value.map((r) => toVnd(r.total_cost)),
      backgroundColor: '#66BB6A',
      borderRadius: 6,
    },
  ],
}))

const lineOptions = {
  responsive: true,
  maintainAspectRatio: false,
  plugins: { legend: { display: false }, tooltip: tooltipVnd },
  scales: { y: { beginAtZero: true } },
}

const barOptions = {
  responsive: true,
  maintainAspectRatio: false,
  plugins: { legend: { display: false }, tooltip: tooltipVnd },
  scales: { y: { beginAtZero: true } },
}

// Horizontal bars read better for an employee leaderboard.
const employeeOptions = {
  responsive: true,
  maintainAspectRatio: false,
  indexAxis: 'y' as const,
  plugins: { legend: { display: false }, tooltip: tooltipVnd },
  scales: { x: { beginAtZero: true } },
}

const slides = computed(() => [
  {
    key: 'day',
    title: t('cost_by_day_chart'),
    component: Line,
    data: dayData.value,
    options: lineOptions,
    hasData: dayRows.value.length > 0,
  },
  {
    key: 'channel',
    title: t('cost_by_channel_chart'),
    component: Bar,
    data: channelData.value,
    options: barOptions,
    hasData: channelRows.value.length > 0,
  },
  {
    key: 'employee',
    title: t('cost_by_employee_chart'),
    component: Bar,
    data: employeeData.value,
    options: employeeOptions,
    hasData: employeeRows.value.length > 0,
  },
])

const slideTitle = computed(() => slides.value[slide.value]?.title ?? t('ai_cost'))

async function fetchGroup(groupBy: 'day' | 'channel' | 'employee'): Promise<CostRow[]> {
  try {
    const { data } = await api.get(`/tenants/${props.tenantId}/analytics/ai-cost`, {
      params: { group_by: groupBy, source: source.value, segment: segment.value },
    })
    return (data?.rows as CostRow[]) || []
  } catch {
    return []
  }
}

async function loadAll() {
  const [day, channel, employee] = await Promise.all([
    fetchGroup('day'),
    fetchGroup('channel'),
    fetchGroup('employee'),
  ])
  dayRows.value = day
  channelRows.value = channel
  employeeRows.value = employee
}

watch([source, segment], loadAll)
onMounted(loadAll)
</script>
