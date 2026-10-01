<template>
  <v-card variant="outlined" class="quality-charts" role="region" aria-roledescription="carousel" :aria-label="t('sq_charts')">
    <div class="chart-heading">
      <h2 class="text-subtitle-1 font-weight-bold">
        <v-icon start color="primary" size="small">{{ slide === 0 ? 'mdi-chart-pie' : 'mdi-chart-tree' }}</v-icon>
        {{ titles[slide] }}
      </h2>
      <div class="chart-navigation">
        <v-btn icon="mdi-chevron-left" variant="text" size="small" :aria-label="t('sq_chart_previous')" @click="slide = slide === 0 ? 1 : 0" />
        <span class="text-caption" aria-live="polite">{{ slide + 1 }} / 2</span>
        <v-btn icon="mdi-chevron-right" variant="text" size="small" :aria-label="t('sq_chart_next')" @click="slide = slide === 0 ? 1 : 0" />
      </div>
    </div>
    <div class="chart-tabs">
      <button v-for="(title, index) in titles" :key="index" type="button" :aria-label="title" :aria-pressed="slide === index" :class="{ active: slide === index }" @click="slide = index" />
    </div>
    <v-divider />
    <div class="chart-body">
      <template v-if="slide === 0">
        <div v-if="totalLeads" class="pie-layout">
          <svg viewBox="0 0 200 200" class="potential-pie" role="img" :aria-label="pieDescription">
            <template v-for="segment in pieSegments" :key="segment.level">
              <circle v-if="segment.count === totalLeads" cx="100" cy="100" r="92" :fill="segment.color"><title>{{ segment.description }}</title></circle>
              <path v-else-if="segment.count" :d="segment.path" :fill="segment.color" stroke="rgb(var(--v-theme-surface))" stroke-width="2"><title>{{ segment.description }}</title></path>
            </template>
          </svg>
          <div class="pie-legend">
            <div class="text-caption text-medium-emphasis">{{ t('sq_chart_conversations', { count: totalLeads }) }}</div>
            <button v-for="segment in pieSegments" :key="segment.level" type="button" class="legend-row" :title="t('sq_chart_filter_products')" @click="filter = segment.level; slide = 1">
              <span class="legend-dot" :style="{ background: segment.color }" />
              <span>{{ segment.label }}</span>
              <strong>{{ segment.count }}</strong>
              <span class="text-caption text-medium-emphasis">{{ segment.percent }}%</span>
            </button>
          </div>
        </div>
        <div v-else class="chart-empty text-medium-emphasis"><v-icon size="36">mdi-chart-pie</v-icon><span>{{ t('sq_chart_empty') }}</span></div>
      </template>
      <template v-else>
        <div v-if="!productGroupingAvailable" class="chart-empty text-medium-emphasis"><span>{{ t('sq_product_grouping_disabled') }} <router-link :to="`/${tenantId}/jobs`">{{ t('sq_ai_jobs_link') }}</router-link></span></div>
        <template v-else>
        <v-select v-model="filter" class="potential-filter" :items="filterOptions" :label="t('sq_chart_potential_filter')" density="compact" variant="outlined" hide-details />
        <div v-if="grouping || refreshingReport" class="chart-empty product-empty text-medium-emphasis" role="status"><v-progress-circular indeterminate size="30" /><span>{{ t(refreshingReport ? 'sq_chart_refreshing_report' : 'sq_chart_ai_grouping') }}</span></div>
        <div v-else-if="groupingError" class="chart-empty product-empty text-error" role="alert"><span>{{ t(groupingTooLarge ? 'sq_chart_ai_too_large' : 'sq_chart_ai_error') }}</span><v-btn v-if="!groupingTooLarge" variant="text" size="small" @click="loadGroups">{{ t('sq_retry') }}</v-btn></div>
        <div v-else-if="tiles.length" class="product-treemap" role="list" :aria-label="t('sq_chart_products')">
          <div v-for="(tile, index) in tiles" :key="tile.key" class="treemap-tile" role="listitem" tabindex="0" :title="t('sq_chart_product_count', { product: tile.label, count: tile.count })" :aria-label="t('sq_chart_product_count', { product: tile.label, count: tile.count })" :style="{ left: `${tile.x}%`, top: `${tile.y}%`, width: `${tile.width}%`, height: `${tile.height}%`, background: tileColors[index % tileColors.length], fontSize: `${tileFontSize(tile)}px` }">
            <strong v-if="tileShowsLabel(tile)" :style="{ fontSize: `${tileFontSize(tile) * 0.85}px` }">{{ tile.label }}</strong><span>{{ tile.count }}</span>
          </div>
        </div>
        <div v-else class="chart-empty product-empty text-medium-emphasis"><v-icon size="36">mdi-package-variant-closed</v-icon><span>{{ t('sq_chart_products_empty') }}</span></div>
        </template>
      </template>
    </div>
    <p v-if="slide === 0 || productGroupingAvailable" class="chart-note text-caption text-medium-emphasis">
      {{ slide === 0 ? t('sq_chart_lead_scope', { count: excludedLeads }) : t('sq_chart_product_scope') }}
    </p>
  </v-card>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { aggregateInsights, type InsightRow } from '../utils/service-quality'
import { aggregateProductDemand, collectProductNames, layoutTreemap, potentialLevels, type AIProductGroup, type PotentialFilter } from '../utils/quality-charts'
import api from '../api'

const props = defineProps<{ rows: InsightRow[]; from: string; to: string; tenantId: string; reportVersion?: string; productGroupingEnabled?: boolean; productGroupingVersion?: string; scope: { from: string; to: string; channel_id: string } }>()
const emit = defineEmits<{ 'refresh-report': [] }>()
const { t, locale } = useI18n()
const slide = ref(0)
const filter = ref<PotentialFilter>('all')
const titles = computed(() => [t('sq_chart_potential'), t('sq_chart_products')])
const colors = { high: '#047857', medium: '#0369a1', low: '#b45309' }
const tileColors = ['#0369a1', '#047857', '#6d28d9', '#b45309', '#0e7490', '#be123c']
const aggregates = computed(() => aggregateInsights(props.rows, props.from, props.to))
const leads = computed(() => potentialLevels.map(level => ({ level, count: aggregates.value.leadQuality.find(item => item.key === level)?.count || 0 })))
const totalLeads = computed(() => leads.value.reduce((sum, lead) => sum + lead.count, 0))
const excludedLeads = computed(() => aggregates.value.leadQuality.filter(item => !potentialLevels.includes(item.key as typeof potentialLevels[number])).reduce((sum, item) => sum + item.count, 0))
const filterOptions = computed(() => [
  { title: t('sq_chart_all_potential'), value: 'all' },
  ...potentialLevels.map(level => ({ title: t(`sq_lead_${level}`), value: level })),
])
const groups = ref<AIProductGroup[]>([])
const grouping = ref(false)
const groupingError = ref(false)
const groupingTooLarge = ref(false)
const refreshingReport = ref(false)
const disabledByServer = ref(false)
const productGroupingAvailable = computed(() => !!props.productGroupingEnabled && !disabledByServer.value)
let controller: AbortController | undefined
let requestSequence = 0
const sourceNames = computed(() => collectProductNames(props.rows, props.from, props.to))
const groupingSignature = computed(() => JSON.stringify([props.tenantId, props.scope, sourceNames.value, !!props.productGroupingEnabled, props.productGroupingVersion]))
const tiles = computed(() => layoutTreemap(aggregateProductDemand(props.rows, props.from, props.to, filter.value, groups.value).slice(0, 20)))

/** Dynamic font size per tile so product names fit inside small cells. */
function tileFontSize(tile: { width: number; height: number }): number {
  const size = Math.min(tile.width * 1.1, tile.height * 1.6)
  return Math.max(7, Math.min(13, size))
}
/** Whether the tile is large enough to display the product name legibly. */
function tileShowsLabel(tile: { width: number; height: number }): boolean {
  return tile.width >= 10 && tile.height >= 12
}

async function loadGroups() {
  controller?.abort()
  const sequence = ++requestSequence
  groups.value = []
  groupingError.value = false
  groupingTooLarge.value = false
  refreshingReport.value = false
  grouping.value = false
  disabledByServer.value = false
  if (!props.productGroupingEnabled || !sourceNames.value.length) return
  controller = new AbortController()
  grouping.value = true
  try {
    const { data } = await api.post<{ enabled: boolean; groups: AIProductGroup[] }>(`/tenants/${props.tenantId}/service-quality/product-groups`, { ...props.scope, product_names: sourceNames.value }, { signal: controller.signal })
    if (sequence !== requestSequence) return
    if (!data.enabled) {
      disabledByServer.value = true
      emit('refresh-report')
      return
    }
    // A report may change while the AI call is in flight. Never show partially grouped demand.
    const assigned = new Set(data.groups.flatMap(group => group.members))
    if (sourceNames.value.some(name => !assigned.has(name))) throw new Error('incomplete grouping')
    groups.value = data.groups
  } catch (error: any) {
    if (sequence !== requestSequence || error?.code === 'ERR_CANCELED') return
    if (error?.response?.status === 409) {
      refreshingReport.value = true
      emit('refresh-report')
      return
    }
    if (error?.response?.status === 502 && typeof error?.response?.data?.reason === 'string') {
      console.warn('Product grouping validation failed:', error.response.data.reason)
    }
    groupingError.value = true
    groupingTooLarge.value = error?.response?.status === 422
  } finally {
    if (sequence === requestSequence) grouping.value = false
  }
}
watch(groupingSignature, loadGroups, { immediate: true })
watch(() => props.reportVersion, () => { if (refreshingReport.value || (disabledByServer.value && props.productGroupingEnabled)) void loadGroups() })
onBeforeUnmount(() => { requestSequence++; controller?.abort() })
const pieSegments = computed(() => {
  let angle = -Math.PI / 2
  return leads.value.map(lead => {
    const fraction = totalLeads.value ? lead.count / totalLeads.value : 0
    const end = angle + fraction * Math.PI * 2
    const path = `M100,100 L${100 + 92 * Math.cos(angle)},${100 + 92 * Math.sin(angle)} A92,92 0 ${fraction > 0.5 ? 1 : 0},1 ${100 + 92 * Math.cos(end)},${100 + 92 * Math.sin(end)} Z`
    angle = end
    const label = t(`sq_lead_${lead.level}`)
    const percent = (fraction * 100).toLocaleString(locale.value, { maximumFractionDigits: 1 })
    return { ...lead, label, percent, path, color: colors[lead.level], description: `${label}: ${lead.count} (${percent}%)` }
  })
})
const pieDescription = computed(() => pieSegments.value.map(segment => segment.description).join(', '))
</script>

<style scoped>
.quality-charts { display: flex; flex-direction: column; height: 370px; }
.chart-heading { display: flex; justify-content: space-between; align-items: center; gap: 8px; padding: 10px 12px 4px 16px; }
.chart-heading h2 { min-width: 0; margin: 0; font-size: 16px; line-height: 1.5; }
.chart-navigation { display: flex; align-items: center; flex-shrink: 0; }
.chart-tabs { display: flex; gap: 5px; padding: 0 16px 12px; }
.chart-tabs button { flex: 1; height: 4px; border-radius: 4px; border: 0; background: rgba(var(--v-theme-primary), .18); cursor: pointer; }
.chart-tabs button.active { background: rgb(var(--v-theme-primary)); }
.chart-body { padding: 12px 16px 0; flex: 1; min-height: 0; }
.pie-layout { display: flex; align-items: center; justify-content: center; gap: 16px; height: 100%; }
.potential-pie { width: 44%; max-width: 210px; max-height: 230px; flex-shrink: 0; }
.pie-legend { min-width: 0; flex: 1; max-width: 250px; }
.legend-row { display: grid; grid-template-columns: 10px 1fr auto auto; align-items: center; gap: 8px; text-align: left; font-size: 13px; width: 100%; padding: 12px 0; color: inherit; border: 0; background: transparent; cursor: pointer; }
.legend-row:hover { color: rgb(var(--v-theme-primary)); }
.legend-dot { width: 10px; height: 10px; border-radius: 50%; }
.potential-filter { max-width: 270px; margin-bottom: 12px; }
.product-treemap { position: relative; height: calc(100% - 56px); min-height: 100px; }
.treemap-tile { position: absolute; border: 2px solid rgb(var(--v-theme-surface)); border-radius: 6px; color: white; display: flex; flex-direction: column; align-items: center; justify-content: center; overflow: hidden; padding: 2px; gap: 0; line-height: 1.2; text-align: center; }
.treemap-tile strong { max-width: 100%; display: -webkit-box; -webkit-line-clamp: 2; -webkit-box-orient: vertical; overflow: hidden; word-break: break-word; line-height: 1.15; }
.treemap-tile span { font-weight: 700; }
.treemap-tile:focus { outline: 2px solid rgb(var(--v-theme-primary)); outline-offset: -2px; }
.chart-note { margin: 0; padding: 12px 16px; line-height: 1.4; font-size: 12px; }
.chart-empty { display: flex; flex-direction: column; align-items: center; justify-content: center; gap: 12px; text-align: center; height: 100%; }
.product-empty { height: calc(100% - 56px); }
@media (max-width: 400px) {
  .chart-heading { gap: 0; padding-left: 12px; }
  .chart-navigation { gap: 0; }
  .pie-layout { gap: 8px; }
  .legend-row { gap: 5px; font-size: 12px; }
}
</style>
