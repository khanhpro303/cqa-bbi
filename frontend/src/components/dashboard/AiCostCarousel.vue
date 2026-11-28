<template>
  <v-card
    class="dashboard-chart dashboard-chart--cost"
    @mouseenter="hovered = true"
    @mouseleave="hovered = false"
    @focusin="onFocusIn"
    @focusout="focused = false"
  >
    <div class="dashboard-chart__heading">
      <span class="dashboard-chart__icon" aria-hidden="true">
        <v-icon size="20">{{ view === 'day' ? 'mdi-chart-line' : 'mdi-chart-bar' }}</v-icon>
      </span>
      <h2>{{ title }}</h2>
      <span class="cost-currency">{{ locale === 'vi' ? 'VNĐ' : 'VND' }}</span>
      <v-btn
        class="cost-playback"
        :icon="playing ? 'mdi-pause' : 'mdi-play'"
        :aria-label="playbackLabel"
        :title="playbackLabel"
        size="32"
        variant="text"
        @click="playing = !playing"
      />
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

    <div v-if="loading" class="cost-slide">
      <DashboardChartSkeleton :label="$t('loading')" />
    </div>
    <div v-else class="cost-slide" @touchstart.passive="playing = false">
      <Transition name="cost-slide" mode="out-in">
        <div :key="view" class="cost-slide__content">
          <div v-if="activeRows === null" class="dashboard-chart__state" role="status">
            <span>{{ $t('dashboard_chart_load_error') }}</span>
            <v-btn size="small" variant="tonal" @click="reload++">{{ $t('retry') }}</v-btn>
          </div>
          <div v-else-if="!activeRows.length" class="dashboard-chart__state" role="status">
            <v-icon size="28" aria-hidden="true">mdi-chart-box-outline</v-icon>
            <span>{{ $t(view === 'employee' && source === 'job' ? 'cost_employee_chatbot_only' : 'no_data') }}</span>
          </div>
          <DailyTrendChart v-else-if="view === 'day'" :label="title" :dates="dates" :series="series" currency />
          <div v-else class="cost-bars">
            <div class="cost-bars__axis" aria-hidden="true">
              <span v-for="tick in costTicks" :key="tick">{{ compactNumber.format(tick) }}</span>
            </div>
            <div class="cost-bars__scroll" role="region" :aria-label="title" tabindex="0">
              <div class="cost-bars__plot" :style="{ '--cost-bar-count': barRows.length }">
                <div class="cost-bars__grid" aria-hidden="true">
                  <span v-for="tick in costTicks" :key="tick" />
                </div>
                <ol class="cost-bars__columns" role="list">
                  <li v-for="(row, index) in barRows" :key="index">
                    <div class="cost-bars__column">
                      <div class="cost-bars__bar" :style="{ height: `${Math.max(0, row.cost) / costTicks[0] * 100}%` }">
                        <strong class="cost-bars__value">{{ currency.format(row.cost) }}</strong>
                      </div>
                    </div>
                    <span class="cost-bars__name" :title="row.label">{{ row.label }}</span>
                  </li>
                </ol>
              </div>
            </div>
          </div>
        </div>
      </Transition>
    </div>
  </v-card>
</template>

<script setup lang="ts">
import { ref, computed, watch, reactive } from 'vue'
import { useI18n } from 'vue-i18n'
import api from '../../api'
import DailyTrendChart from './DailyTrendChart.vue'
import DashboardChartSkeleton from './DashboardChartSkeleton.vue'
import './dashboard-skeleton.css'

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
const playing = ref(!window.matchMedia('(prefers-reduced-motion: reduce)').matches)
const hovered = ref(false)
const focused = ref(false)
const source = ref<keyof typeof sources>('all')
const segment = ref<keyof typeof segments>('all')
const loading = ref(true)
const reload = ref(0)
const rows = reactive<Record<GroupBy, CostRow[] | null>>({ day: [], channel: [], employee: [] })

const title = computed(() => t(views[view.value].title))
const playbackLabel = computed(() => t(playing.value ? 'cost_slideshow_pause' : 'cost_slideshow_play'))
const currency = computed(() => new Intl.NumberFormat(locale.value, { style: 'currency', currency: 'VND' }))
const compactNumber = computed(() => new Intl.NumberFormat(locale.value, {
  notation: 'compact', compactDisplay: 'long', maximumFractionDigits: 1,
}))
const activeRows = computed(() => rows[view.value])
const dates = computed(() => rows.day?.map(row => row.label) ?? [])
const series = computed(() => [{ label: t('cost'), values: rows.day?.map(row => toVnd(row.total_cost)) ?? [] }])
const barRows = computed(() => (activeRows.value ?? []).map(row => ({
  label: row.label,
  cost: toVnd(row.total_cost),
})))
const costTicks = computed(() => {
  const max = barRows.value.reduce((highest, row) => Math.max(highest, row.cost), 0)
  const intervals = 4
  const targetStep = Math.max(1, max / intervals)
  const magnitude = 10 ** Math.floor(Math.log10(targetStep))
  const multiplier = [1, 2, 5, 10].find(value => value * magnitude >= targetStep) ?? 10
  const step = multiplier * magnitude

  return Array.from({ length: intervals + 1 }, (_, index) => (intervals - index) * step)
})

function toVnd(usd: number): number {
  return Math.round(usd * (props.exchangeRate || 26000))
}

function onFocusIn(event: FocusEvent) {
  focused.value = event.target instanceof Element && !event.target.closest('.cost-playback')
}

watch([view, playing, hovered, focused, loading, activeRows], (_, __, onCleanup) => {
  if (!playing.value || hovered.value || focused.value || loading.value || activeRows.value === null) return

  const timer = window.setTimeout(() => {
    const next = (groupings.indexOf(view.value) + 1) % groupings.length
    view.value = groupings[next]
  }, 6000)
  onCleanup(() => window.clearTimeout(timer))
})

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
  --cost-bar-fill: #c46c31;
  --cost-bar-edge: #df955a;
}

:global(.v-theme--dark) .dashboard-chart--cost.v-card {
  --chart-accent: #ffc38c;
  --chart-tint: rgba(255, 180, 99, 0.14);
  --cost-bar-fill: #be7845;
  --cost-bar-edge: #ffc38c;
}

.cost-currency {
  margin-inline-start: auto;
  color: rgba(var(--v-theme-on-surface), 0.68);
  font-size: 0.75rem;
  font-weight: 600;
}

.cost-playback {
  flex-shrink: 0;
}

.cost-slide,
.cost-slide__content {
  display: flex;
  flex: 1 0 240px;
  flex-direction: column;
  min-width: 0;
}

.cost-slide-enter-active,
.cost-slide-leave-active {
  transition: transform 160ms ease, opacity 160ms ease;
}

.cost-slide-leave-active {
  pointer-events: none;
}

.cost-slide-enter-from {
  transform: translateX(16px);
  opacity: 0;
}

.cost-slide-leave-to {
  transform: translateX(-16px);
  opacity: 0;
}

@media (prefers-reduced-motion: reduce) {
  .cost-slide-enter-active,
  .cost-slide-leave-active {
    transition: none;
  }
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
.cost-bars__scroll:focus-visible {
  outline: 2px solid var(--chart-accent);
  outline-offset: 2px;
}

.cost-bars {
  --cost-plot-height: 174px;
  --cost-plot-top: 26px;
  --cost-column-width: 112px;
  display: grid;
  grid-template-columns: auto minmax(0, 1fr);
  gap: 10px;
  min-width: 0;
}

.cost-bars__axis,
.cost-bars__grid {
  display: flex;
  flex-direction: column;
  justify-content: space-between;
  height: var(--cost-plot-height);
  margin-top: var(--cost-plot-top);
}

.cost-bars__axis {
  text-align: end;
  white-space: nowrap;
  color: rgba(var(--v-theme-on-surface), 0.72);
  font-size: 0.6875rem;
  font-variant-numeric: tabular-nums;
}

.cost-bars__axis span {
  line-height: 0;
}

.cost-bars__scroll {
  min-width: 0;
  overflow-x: auto;
  border-radius: 4px;
}

.cost-bars__plot {
  position: relative;
  min-width: calc(var(--cost-bar-count) * var(--cost-column-width));
}

.cost-bars__grid {
  position: absolute;
  inset: 0 0 auto;
  pointer-events: none;
}

.cost-bars__grid span {
  width: 100%;
  height: 0;
  box-shadow: 0 1px 0 0.5px rgba(var(--v-theme-on-surface), 0.10);
}

.cost-bars__grid span:last-child {
  box-shadow: 0 1px 0 0.5px rgba(var(--v-theme-on-surface), 0.24);
}

.cost-bars__columns {
  position: relative;
  display: grid;
  grid-template-columns: repeat(var(--cost-bar-count), minmax(0, 1fr));
  margin: 0;
  padding: var(--cost-plot-top) 0 8px;
  list-style: none;
  text-align: center;
}

.cost-bars__column {
  display: flex;
  justify-content: center;
  align-items: flex-end;
  height: var(--cost-plot-height);
}

.cost-bars__bar {
  position: relative;
  width: min(56%, 72px);
  border-radius: 7px 7px 0 0;
  background: linear-gradient(to bottom, var(--cost-bar-edge), var(--cost-bar-fill));
}

.cost-bars__value {
  position: absolute;
  bottom: calc(100% + 5px);
  left: 50%;
  transform: translateX(-50%);
  white-space: nowrap;
  padding: 1px 4px;
  background: rgb(var(--v-theme-surface));
  font-size: 0.75rem;
  font-variant-numeric: tabular-nums;
}

.cost-bars__name {
  display: -webkit-box;
  -webkit-box-orient: vertical;
  -webkit-line-clamp: 2;
  overflow: hidden;
  overflow-wrap: anywhere;
  margin: 8px 5px 0;
  color: rgba(var(--v-theme-on-surface), 0.82);
  font-size: 0.75rem;
  line-height: 1.25;
}
</style>
