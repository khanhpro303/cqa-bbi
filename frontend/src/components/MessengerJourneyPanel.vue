<template>
  <div v-if="available" class="journey-panel">
    <button
      type="button"
      class="journey-toggle"
      :aria-expanded="expanded"
      :aria-controls="panelId"
      :aria-labelledby="`${panelId}-title`"
      :aria-describedby="summary.last_observed_at ? `${panelId}-updated` : undefined"
      @click="expanded = !expanded"
    >
      <span class="journey-heading">
        <span class="journey-heading-icon">
          <v-icon size="20">mdi-map-marker-path</v-icon>
        </span>
        <span class="journey-heading-content">
          <span :id="`${panelId}-title`" class="journey-title">{{ t('journey_title') }}</span>
          <time v-if="summary.last_observed_at" :id="`${panelId}-updated`" class="journey-updated" :datetime="summary.last_observed_at">
            {{ formatTime(summary.last_observed_at) }}
          </time>
        </span>
      </span>
      <span class="journey-chevron">
        <v-icon size="18">{{ expanded ? 'mdi-chevron-up' : 'mdi-chevron-down' }}</v-icon>
      </span>
    </button>

    <div
      v-if="summary.classification || summary.labels?.length || (!expanded && summary.latest_referral?.ad_id)"
      class="journey-tags"
      role="group"
      :aria-label="t('journey_summary')"
      tabindex="0"
    >
      <v-chip v-if="summary.classification" size="small" :color="classificationColor(summary.classification)" variant="tonal">
        {{ classificationLabel(summary.classification) }}
      </v-chip>
      <v-chip v-for="label in summary.labels" :key="label" size="small" variant="outlined">{{ label }}</v-chip>
      <span v-if="!expanded && summary.latest_referral?.ad_id" class="journey-ad">
        {{ t('journey_latest_ad') }} {{ shortId(summary.latest_referral.ad_id) }}
      </span>
    </div>

    <v-expand-transition>
      <div v-show="expanded" :id="panelId" class="journey-detail" role="region" :aria-labelledby="`${panelId}-title`" tabindex="0">
        <div v-if="loading" class="journey-state" role="status">
          <v-progress-circular indeterminate size="18" width="2" color="primary" aria-hidden="true" />
          <span>{{ t('journey_loading') }}</span>
        </div>
        <div v-else-if="error" class="journey-state">
          <span role="alert">{{ t('journey_load_error') }}</span>
          <v-btn size="small" variant="tonal" prepend-icon="mdi-refresh" @click="load(true)">{{ t('retry') }}</v-btn>
        </div>
        <template v-else>
          <dl class="journey-sources">
            <div class="journey-source">
              <dt><v-icon size="16" start>mdi-map-marker-outline</v-icon>{{ t('journey_first_source') }}</dt>
              <dd class="journey-source-value">{{ referralLabel(summary.first_referral) }}</dd>
              <dd v-if="referralTime(summary.first_referral)" class="journey-time">
                <time :datetime="referralTime(summary.first_referral)">{{ formatTime(referralTime(summary.first_referral)) }}</time>
              </dd>
            </div>
            <div class="journey-source">
              <dt><v-icon size="16" start>mdi-clock-outline</v-icon>{{ t('journey_latest_source') }}</dt>
              <dd class="journey-source-value">{{ referralLabel(summary.latest_referral) }}</dd>
              <dd v-if="referralTime(summary.latest_referral)" class="journey-time">
                <time :datetime="referralTime(summary.latest_referral)">{{ formatTime(referralTime(summary.latest_referral)) }}</time>
              </dd>
            </div>
          </dl>

          <ol v-if="events.length" role="list" class="journey-timeline">
            <li v-for="event in events" :key="event.event_id" class="journey-event">
              <span class="journey-event-icon" :class="`journey-event-icon--${event.kind}`">
                <v-icon size="16">{{ eventIcons[event.kind] || 'mdi-circle-small' }}</v-icon>
              </span>
              <div class="journey-event-content">
                <div class="journey-event-title">{{ eventTitle(event) }}</div>
                <div v-if="eventDetail(event)" class="journey-event-detail">{{ eventDetail(event) }}</div>
                <time class="journey-time" :datetime="event.observed_at">{{ formatTime(event.observed_at) }}</time>
              </div>
            </li>
          </ol>
          <div v-else class="journey-state journey-empty">
            <v-icon size="18">mdi-timeline-outline</v-icon>
            <span>{{ t(canLoadMore ? 'journey_page_empty' : 'journey_empty') }}</span>
          </div>

          <div v-if="canLoadMore" class="journey-footer">
            <span v-if="paginationError" role="alert">{{ t('journey_load_more_error') }}</span>
            <v-btn size="small" variant="tonal" :prepend-icon="paginationError ? 'mdi-refresh' : 'mdi-history'" :loading="loadingMore" @click="loadMore">
              {{ t(paginationError ? 'retry' : 'journey_load_more') }}
            </v-btn>
          </div>
        </template>
      </div>
    </v-expand-transition>
  </div>
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
const paginationError = ref(false)
const summary = ref<MessengerJourneySummary>({ labels: [] })
const events = ref<MessengerJourneyEvent[]>([])
const nextCursor = ref('')
const hasMore = ref(false)
let requestVersion = 0

const panelId = computed(() => `messenger-journey-${props.conversationId}`)
const canLoadMore = computed(() => hasMore.value && Boolean(nextCursor.value))
const eventIcons: Record<string, string> = {
  referral: 'mdi-map-marker-outline',
  labels: 'mdi-tag-outline',
  classification: 'mdi-filter-outline',
  order: 'mdi-package-variant-closed',
  deletion: 'mdi-delete-outline',
}

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
  if (!canLoadMore.value || loadingMore.value) return
  const version = requestVersion
  loadingMore.value = true
  paginationError.value = false
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
    if (version === requestVersion) paginationError.value = true
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
  paginationError.value = false
  summary.value = { labels: [] }
  events.value = []
  nextCursor.value = ''
  hasMore.value = false
  void load()
}, { immediate: true })
</script>

<style scoped>
.journey-panel {
  --journey-border: rgba(var(--v-theme-on-surface), .15);
  --journey-muted: rgba(var(--v-theme-on-surface), .72);
  flex: 0 0 auto;
  min-width: 0;
  margin: 12px 16px 8px;
  border: 1px solid var(--journey-border);
  border-radius: 12px;
  background: rgb(var(--v-theme-surface));
  color: rgb(var(--v-theme-on-surface));
  container-type: inline-size;
}

.journey-toggle {
  appearance: none;
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
  width: 100%;
  min-height: 52px;
  padding: 8px 12px;
  border: 0;
  border-radius: 11px;
  background: transparent;
  color: inherit;
  text-align: start;
  cursor: pointer;
}

.journey-toggle:hover { background: rgba(var(--v-theme-on-surface), .045); }
.journey-toggle[aria-expanded='true'] { background: rgba(var(--v-theme-primary), .07); }
.journey-toggle:focus-visible,
.journey-tags:focus-visible,
.journey-detail:focus-visible {
  outline: 2px solid rgb(var(--v-theme-primary));
  outline-offset: -2px;
}

.journey-heading,
.journey-heading-content { display: flex; align-items: center; gap: 10px; min-width: 0; }
.journey-heading { flex: 1; }
.journey-heading-content { flex: 1; justify-content: space-between; flex-wrap: wrap; gap: 2px 12px; }
.journey-heading-icon,
.journey-chevron,
.journey-event-icon { display: grid; flex: none; place-items: center; }
.journey-heading-icon {
  width: 36px;
  height: 36px;
  border-radius: 9px;
  background: rgba(var(--v-theme-primary), .12);
  color: rgb(var(--v-theme-primary));
}
.journey-chevron { width: 26px; height: 26px; color: var(--journey-muted); }
.journey-title { font-size: .875rem; font-weight: 650; line-height: 1.35; }
.journey-updated,
.journey-time,
.journey-ad { color: var(--journey-muted); font-size: .75rem; line-height: 1.45; }
.journey-updated { font-variant-numeric: tabular-nums; }

.journey-tags {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  align-content: start;
  gap: 5px 6px;
  max-block-size: 5rem;
  overflow-y: auto;
  padding: 6px 12px 9px;
}
.journey-tags :deep(.v-chip) { max-width: 100%; height: auto; min-height: 24px; padding-block: 2px; white-space: normal; }
.journey-tags :deep(.v-chip__content) { display: block; min-width: 0; overflow-wrap: anywhere; }

.journey-detail {
  display: grid;
  align-content: start;
  gap: 10px;
  max-block-size: min(25rem, 40dvh);
  overflow-y: auto;
  padding: 10px 12px 12px;
  border-top: 1px solid var(--journey-border);
  border-radius: 0 0 11px 11px;
}
.journey-sources { display: grid; grid-template-columns: repeat(2, minmax(0, 1fr)); gap: 8px; margin: 0; }
.journey-source {
  display: grid;
  align-content: start;
  gap: 2px;
  min-width: 0;
  padding: 8px 10px;
  border: 1px solid var(--journey-border);
  border-radius: 8px;
  background: rgba(var(--v-theme-on-surface), .025);
}
.journey-source dt { display: flex; align-items: center; color: var(--journey-muted); font-size: .75rem; font-weight: 600; }
.journey-source dd { margin: 0; }
.journey-source-value { font-size: .8125rem; font-weight: 600; overflow-wrap: anywhere; }

.journey-state,
.journey-footer { display: flex; flex-wrap: wrap; align-items: center; gap: 8px; color: var(--journey-muted); font-size: .8125rem; }
.journey-state { padding: 8px 10px; border-radius: 8px; background: rgba(var(--v-theme-on-surface), .035); }
.journey-empty { border: 1px dashed var(--journey-border); }
.journey-footer { justify-content: flex-end; }
.journey-footer :deep(.v-btn),
.journey-state :deep(.v-btn) { min-height: 32px; text-transform: none; letter-spacing: normal; }

.journey-timeline { margin: 0; padding: 0; list-style: none; }
.journey-event { position: relative; display: grid; grid-template-columns: 28px minmax(0, 1fr); gap: 8px; padding: 6px 0; }
.journey-event:not(:last-child)::before {
  position: absolute;
  top: 32px;
  bottom: -7px;
  left: 13px;
  width: 2px;
  background: var(--journey-border);
  content: '';
}
.journey-event-icon {
  z-index: 1;
  width: 28px;
  height: 28px;
  border-radius: 50%;
  --event-accent: var(--v-theme-primary);
  background: rgba(var(--event-accent), .13);
  color: rgb(var(--event-accent));
}
.journey-event-icon--labels { --event-accent: var(--v-theme-info); }
.journey-event-icon--classification { --event-accent: var(--v-theme-warning); }
.journey-event-icon--order { --event-accent: var(--v-theme-success); }
.journey-event-icon--deletion { --event-accent: var(--v-theme-error); }
.journey-event-content {
  display: grid;
  grid-template-columns: minmax(0, 1fr) auto;
  align-items: baseline;
  gap: 2px 12px;
  padding: 3px 0;
}
.journey-event-title { font-size: .8125rem; font-weight: 650; overflow-wrap: anywhere; }
.journey-event-detail { grid-column: 1 / -1; color: var(--journey-muted); font-size: .8125rem; overflow-wrap: anywhere; }
.journey-event-content .journey-time { grid-column: 2; grid-row: 1; }

@container (max-width: 400px) {
  .journey-heading-content { flex-direction: column; align-items: flex-start; }
  .journey-event-content { grid-template-columns: minmax(0, 1fr); }
  .journey-event-content .journey-time { grid-column: auto; grid-row: auto; }
}

@container (max-width: 275px) {
  .journey-sources { grid-template-columns: 1fr; }
}
</style>
