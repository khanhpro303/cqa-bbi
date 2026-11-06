<template>
  <v-card v-if="available" variant="flat" class="journey-panel rounded-0">
    <button
      type="button"
      class="journey-toggle"
      :aria-expanded="expanded"
      :aria-controls="panelId"
      @click="expanded = !expanded"
    >
      <span class="d-flex align-center ga-2 flex-wrap">
        <v-icon size="small" color="primary">mdi-map-marker-path</v-icon>
        <span class="text-body-2 font-weight-medium">{{ t('journey_title') }}</span>
        <v-chip v-if="summary.classification" size="x-small" :color="classificationColor(summary.classification)" variant="tonal">
          {{ classificationLabel(summary.classification) }}
        </v-chip>
        <v-chip v-for="label in summary.labels || []" :key="label" size="x-small" variant="outlined">{{ label }}</v-chip>
        <span v-if="summary.latest_referral?.ad_id" class="text-caption text-medium-emphasis">
          {{ t('journey_latest_ad') }} {{ shortId(summary.latest_referral.ad_id) }}
        </span>
      </span>
      <span class="d-flex align-center ga-1 text-caption text-medium-emphasis">
        <v-progress-circular v-if="loading && !events.length" indeterminate size="14" width="2" />
        <span v-else-if="summary.last_observed_at">{{ formatTime(summary.last_observed_at) }}</span>
        <v-icon size="small">{{ expanded ? 'mdi-chevron-up' : 'mdi-chevron-down' }}</v-icon>
      </span>
    </button>

    <v-expand-transition>
      <div v-if="expanded" :id="panelId" class="journey-detail px-4 pb-3">
        <div v-if="loading && !events.length" class="d-flex align-center justify-center ga-2 py-4 text-medium-emphasis">
          <v-progress-circular indeterminate size="20" width="2" />
          <span class="text-body-2">{{ t('journey_loading') }}</span>
        </div>
        <div v-else-if="error" class="d-flex align-center justify-space-between ga-3 py-2 text-error">
          <span class="text-body-2">{{ t('journey_load_error') }}</span>
          <v-btn size="small" variant="text" color="primary" prepend-icon="mdi-refresh" @click="load(true)">{{ t('retry') }}</v-btn>
        </div>
        <template v-else>
          <div class="journey-summary-grid mb-3">
            <div>
              <div class="journey-label">{{ t('journey_first_source') }}</div>
              <div class="text-body-2">{{ referralLabel(summary.first_referral) }}</div>
              <div v-if="referralTime(summary.first_referral)" class="text-caption text-medium-emphasis">{{ formatTime(referralTime(summary.first_referral)) }}</div>
            </div>
            <div>
              <div class="journey-label">{{ t('journey_latest_source') }}</div>
              <div class="text-body-2">{{ referralLabel(summary.latest_referral) }}</div>
              <div v-if="referralTime(summary.latest_referral)" class="text-caption text-medium-emphasis">{{ formatTime(referralTime(summary.latest_referral)) }}</div>
            </div>
          </div>

          <div v-if="events.length" class="journey-timeline">
            <div v-for="event in events" :key="event.event_id" class="journey-event">
              <span class="journey-dot" :class="`journey-dot--${event.kind}`" />
              <div class="d-flex align-start justify-space-between ga-3">
                <div class="min-width-0">
                  <div class="text-body-2 font-weight-medium">{{ eventTitle(event) }}</div>
                  <div v-if="eventDetail(event)" class="text-caption text-medium-emphasis journey-event-detail">{{ eventDetail(event) }}</div>
                </div>
                <time class="text-caption text-medium-emphasis text-no-wrap" :datetime="event.observed_at">{{ formatTime(event.observed_at) }}</time>
              </div>
            </div>
          </div>
          <div v-else class="text-body-2 text-medium-emphasis py-2">{{ t('journey_empty') }}</div>

          <div v-if="hasMore" class="text-center mt-2">
            <v-btn size="small" variant="text" color="primary" prepend-icon="mdi-history" :loading="loadingMore" @click="loadMore">
              {{ t('journey_load_more') }}
            </v-btn>
          </div>
        </template>
      </div>
    </v-expand-transition>
  </v-card>
</template>

<script setup lang="ts">
import axios from 'axios'
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import api from '../api'
import {
  journeyPayloadLabels,
  journeyPayloadNumber,
  journeyPayloadText,
  normalizeMessengerJourneyPage,
  type MessengerJourneyEvent,
  type MessengerJourneyReferral,
  type MessengerJourneySummary,
} from '../utils/messenger-journey'

const props = defineProps<{ tenantId: string; conversationId: string }>()
const { t, locale } = useI18n()
const available = ref(true)
const expanded = ref(false)
const loading = ref(false)
const loadingMore = ref(false)
const error = ref(false)
const summary = ref<MessengerJourneySummary>({ labels: [] })
const events = ref<MessengerJourneyEvent[]>([])
const nextCursor = ref('')
const hasMore = ref(false)
let requestVersion = 0

const panelId = computed(() => `messenger-journey-${props.conversationId}`)

async function load(force = false) {
  if (!props.tenantId || !props.conversationId || (loading.value && !force)) return
  const version = ++requestVersion
  loading.value = true
  error.value = false
  try {
    const response = await api.get(`/tenants/${props.tenantId}/conversations/${props.conversationId}/journey`, { params: { limit: 20 } })
    if (version !== requestVersion) return
    const page = normalizeMessengerJourneyPage(response.data)
    available.value = true
    summary.value = page.summary
    events.value = page.events
    nextCursor.value = page.next_cursor
    hasMore.value = page.has_more
  } catch (cause) {
    if (version !== requestVersion) return
    if (axios.isAxiosError(cause) && cause.response?.status === 404) {
      available.value = false
      return
    }
    available.value = true
    error.value = true
  } finally {
    if (version === requestVersion) loading.value = false
  }
}

async function loadMore() {
  if (!nextCursor.value || loadingMore.value) return
  const version = requestVersion
  loadingMore.value = true
  error.value = false
  try {
    const response = await api.get(`/tenants/${props.tenantId}/conversations/${props.conversationId}/journey`, {
      params: { limit: 20, after: nextCursor.value },
    })
    if (version !== requestVersion) return
    const page = normalizeMessengerJourneyPage(response.data)
    const seen = new Set(events.value.map(event => event.event_id))
    events.value.push(...page.events.filter(event => !seen.has(event.event_id)))
    nextCursor.value = page.next_cursor
    hasMore.value = page.has_more
  } catch {
    if (version === requestVersion) error.value = true
  } finally {
    if (version === requestVersion) loadingMore.value = false
  }
}

function shortId(value: string): string {
  return value.length > 16 ? `${value.slice(0, 8)}…${value.slice(-4)}` : value
}

function formatTime(value?: string): string {
  if (!value) return ''
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return ''
  return new Intl.DateTimeFormat(locale.value, { dateStyle: 'short', timeStyle: 'short' }).format(date)
}

function referralTime(value?: MessengerJourneyReferral | null): string {
  return value?.occurred_at || value?.observed_at || ''
}

function referralLabel(value?: MessengerJourneyReferral | null): string {
  if (!value) return t('journey_unknown_source')
  if (value.ad_id) return `${t('journey_ad')} ${shortId(value.ad_id)}`
  return value.ref || value.source || t('journey_unknown_source')
}

function classificationLabel(value: string): string {
  const key = `journey_classification_${value}`
  const translated = t(key)
  return translated === key ? value : translated
}

function classificationColor(value: string): string {
  if (value === 'qualified') return 'success'
  if (value === 'unqualified') return 'error'
  if (value === 'potential') return 'warning'
  if (value === 'conflict') return 'deep-orange'
  return 'grey'
}

function eventTitle(event: MessengerJourneyEvent): string {
  const key = `journey_event_${event.kind}`
  const translated = t(key)
  return translated === key ? event.kind : translated
}

function eventDetail(event: MessengerJourneyEvent): string {
  const payload = event.payload
  switch (event.kind) {
    case 'referral': {
      const adId = journeyPayloadText(payload, 'ad_id')
      return adId ? `${t('journey_ad')} ${shortId(adId)}` : journeyPayloadText(payload, 'ref') || journeyPayloadText(payload, 'source')
    }
    case 'labels':
      return journeyPayloadLabels(payload).join(', ')
    case 'classification':
      return classificationLabel(journeyPayloadText(payload, 'classification'))
    case 'order': {
      const status = journeyPayloadText(payload, 'status')
      const reference = journeyPayloadText(payload, 'order_ref')
      const value = journeyPayloadNumber(payload, 'value_vnd')
      const amount = value == null ? '' : new Intl.NumberFormat(locale.value, { style: 'currency', currency: 'VND', maximumFractionDigits: 0 }).format(value)
      return [reference, status, amount].filter(Boolean).join(' · ')
    }
    case 'deletion':
      return t('journey_deletion_detail')
    default:
      return ''
  }
}

watch(() => [props.tenantId, props.conversationId] as const, () => {
  requestVersion += 1
  loading.value = false
  loadingMore.value = false
  available.value = true
  expanded.value = false
  error.value = false
  summary.value = { labels: [] }
  events.value = []
  nextCursor.value = ''
  hasMore.value = false
  void load()
}, { immediate: true })
</script>

<style scoped>
.journey-panel { border-bottom: 1px solid rgba(var(--v-border-color), var(--v-border-opacity)); }
.journey-toggle { display: flex; width: 100%; min-height: 44px; align-items: center; justify-content: space-between; gap: 12px; padding: 8px 16px; color: inherit; text-align: left; }
.journey-toggle:hover { background: rgba(var(--v-theme-primary), 0.035); }
.journey-detail { border-top: 1px solid rgba(var(--v-border-color), calc(var(--v-border-opacity) * .65)); }
.journey-summary-grid { display: grid; grid-template-columns: repeat(2, minmax(0, 1fr)); gap: 12px; padding-top: 12px; }
.journey-label { color: rgba(var(--v-theme-on-surface), .62); font-size: 11px; font-weight: 600; text-transform: uppercase; }
.journey-timeline { position: relative; margin-left: 6px; padding-left: 18px; }
.journey-timeline::before { position: absolute; top: 8px; bottom: 8px; left: 4px; width: 2px; background: rgba(var(--v-theme-primary), .18); content: ''; }
.journey-event { position: relative; padding: 5px 0 8px; }
.journey-dot { position: absolute; top: 10px; left: -18px; width: 10px; height: 10px; border: 2px solid rgb(var(--v-theme-surface)); border-radius: 50%; background: rgb(var(--v-theme-primary)); box-shadow: 0 0 0 1px rgba(var(--v-theme-primary), .3); }
.journey-dot--labels { background: rgb(var(--v-theme-info)); }
.journey-dot--classification { background: rgb(var(--v-theme-warning)); }
.journey-dot--order { background: rgb(var(--v-theme-success)); }
.journey-dot--deletion { background: rgb(var(--v-theme-error)); }
.journey-event-detail { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.min-width-0 { min-width: 0; }
@media (max-width: 600px) { .journey-summary-grid { grid-template-columns: 1fr; } .journey-toggle { align-items: flex-start; } }
</style>
