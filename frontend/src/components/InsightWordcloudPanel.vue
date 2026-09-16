<template>
  <v-row>
    <v-col v-for="group in groups" :key="group.kind" cols="12" md="6" lg="3">
      <section class="segment h-100">
        <button class="segment-heading" type="button" @click="openSegment(group.kind)">
          <v-icon start size="small" :icon="group.icon" />{{ group.title }}
          <v-icon end size="16">mdi-arrow-top-right</v-icon>
        </button>
        <ShapeWordcloud
          v-if="group.items.length"
          :items="group.items.slice(0, cloudLimit)"
          :title="group.title"
          @select="openSegment(group.kind, $event)"
        />
        <div v-else class="empty-cloud text-body-2 text-medium-emphasis">{{ t('qualityInsight.emptyPeriod') }}</div>
        <button v-if="group.items.length" class="segment-footer" type="button" @click="openSegment(group.kind)">
          {{ t('qualityInsight.keywordDetails', { count: group.items.length }) }}
          <span v-if="group.items.length > cloudLimit" class="d-block">{{ t('qualityInsight.cloudLimit', { count: cloudLimit }) }}</span>
        </button>
      </section>
    </v-col>
  </v-row>

  <v-dialog v-model="dialog" max-width="1100">
    <v-card v-if="selectedGroup" class="insight-detail-card">
      <v-card-title class="d-flex align-center ga-2">
        <v-icon :icon="selectedGroup.icon" color="primary" />
        <span>{{ selectedGroup.title }}</span>
        <v-spacer />
        <v-btn icon="mdi-close" variant="text" :title="t('qualityInsight.closeDetails')" @click="dialog = false" />
      </v-card-title>
      <v-divider />
      <v-card-text class="insight-detail-body">
        <p class="text-body-2 text-medium-emphasis mb-4">
          {{ t('qualityInsight.countExplanation') }}
        </p>
        <div class="detail-columns">
          <div class="detail-column">
            <h2 class="text-subtitle-2 mb-2">{{ t('qualityInsight.keywordCounts', { count: selectedGroup.items.length }) }}</h2>
            <v-text-field v-model="keywordSearch" :label="t('qualityInsight.searchKeyword')" prepend-inner-icon="mdi-magnify" density="compact" variant="outlined" hide-details class="keyword-search mb-2" />
            <div class="keyword-list scroll-region">
              <button
                v-for="item in filteredKeywords"
                :key="item.key"
                class="keyword-row"
                :class="{ selected: selectedKeyword?.key === item.key }"
                :aria-pressed="selectedKeyword?.key === item.key"
                type="button"
                @click="selectedKey = item.key"
              >
                <span class="keyword-label">{{ item.label }}</span><strong class="keyword-count">{{ item.count }}</strong>
              </button>
              <p v-if="!filteredKeywords.length" class="text-body-2 text-medium-emphasis pa-3">{{ t('qualityInsight.noKeywords') }}</p>
            </div>
          </div>
          <div class="detail-column">
            <h2 class="text-subtitle-2 mb-2">
              {{ selectedKeyword ? t('qualityInsight.keywordConversations', { label: selectedKeyword.label, count: selectedKeyword.count }) : t('qualityInsight.selectKeyword') }}
            </h2>
            <div class="conversation-list scroll-region">
              <v-card v-for="row in sourceConversations" :key="row.conversation_id" variant="outlined" class="mb-3">
                <v-card-text>
                  <v-btn
                    class="conversation-link pa-0"
                    variant="text"
                    color="primary"
                    append-icon="mdi-arrow-right"
                    :to="{ name: 'messages', params: { tenantId }, query: { conv: row.conversation_id, channel_id: row.channel_id, tab: 'messages' } }"
                  >{{ row.customer_name || t('qualityInsight.messengerCustomer') }}</v-btn>
                  <div class="text-caption text-medium-emphasis mb-2">{{ row.channel_name }} · {{ row.conversation_id }}</div>
                  <div v-for="(evidence, index) in keywordEvidence(row)" :key="index" class="text-body-2 mb-2 evidence">{{ evidence }}</div>
                  <p class="text-body-2 mb-0 evidence">{{ row.insight?.summary || t('qualityInsight.noSummary') }}</p>
                </v-card-text>
              </v-card>
              <p v-if="selectedKeyword && !sourceConversations.length" class="text-body-2 text-medium-emphasis">{{ t('qualityInsight.noConversations') }}</p>
            </div>
          </div>
        </div>
      </v-card-text>
    </v-card>
  </v-dialog>
</template>

<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import ShapeWordcloud from './ShapeWordcloud.vue'
import { normalizeInsightLabel, type AggregateItem, type InsightRow } from '../utils/service-quality'

interface InsightGroup {
  kind: string
  title: string
  icon: string
  items: AggregateItem[]
}

interface SourceConversation extends InsightRow {
  customer_name: string
  channel_id: string
  channel_name: string
}

const props = defineProps<{ groups: InsightGroup[]; conversations: SourceConversation[]; tenantId: string }>()
const { t } = useI18n()
const cloudLimit = 40
const dialog = ref(false)
const selectedKind = ref('')
const selectedKey = ref('')
const keywordSearch = ref('')
const selectedGroup = computed(() => props.groups.find(group => group.kind === selectedKind.value))
const selectedKeyword = computed(() => selectedGroup.value?.items.find(item => item.key === selectedKey.value))
const filteredKeywords = computed(() => {
  const term = normalizeInsightLabel(keywordSearch.value)
  return selectedGroup.value?.items.filter(item => normalizeInsightLabel(item.label).includes(term)) || []
})
const sourceConversations = computed(() => {
  const ids = new Set(selectedKeyword.value?.conversationIds || [])
  return props.conversations.filter(row => ids.has(row.conversation_id))
})

function openSegment(kind: string, key?: string) {
  selectedKind.value = kind
  selectedKey.value = key || props.groups.find(group => group.kind === kind)?.items[0]?.key || ''
  keywordSearch.value = ''
  dialog.value = true
}

function keywordEvidence(row: SourceConversation): string[] {
  const key = selectedKeyword.value?.key
  if (!key || !row.insight) return []
  if (selectedKind.value === 'product') {
    return [...new Set((row.insight.products || []).filter(product => {
      const productKey = product.sku?.trim()
        ? `sku:${normalizeInsightLabel(product.sku)}`
        : `name:${normalizeInsightLabel(product.name)}`
      return productKey === key
    }).map(product => product.evidence).filter((evidence): evidence is string => Boolean(evidence)))]
  }
  if (selectedKind.value === 'feedback') {
    return [...new Set((row.insight.feedback || []).filter(feedback =>
      `${normalizeInsightLabel(feedback.category)}:${feedback.sentiment.trim().toLowerCase()}` === key
    ).map(feedback => feedback.evidence).filter((evidence): evidence is string => Boolean(evidence)))]
  }
  if (selectedKind.value === 'lead') {
    const lead = row.insight.lead_quality
    return [lead?.reason, lead?.evidence].filter((value): value is string => Boolean(value))
  }
  return []
}

watch(() => props.tenantId, () => { dialog.value = false })
// Keep the open detail in sync with refreshed report data.
watch(selectedGroup, group => {
  if (!group?.items.length) dialog.value = false
  else if (!group.items.some(item => item.key === selectedKey.value)) selectedKey.value = group.items[0]!.key
})
</script>

<style scoped>
.segment { border: 1px solid rgba(var(--v-theme-on-surface), .12); border-radius: 12px; padding: 16px; --cloud-1: #3949ab; --cloud-2: #00796b; --cloud-3: #7b1fa2; --cloud-4: #1565c0; --cloud-5: #ad4510; }
:global(.v-theme--dark) .segment { --cloud-1: #9fa8da; --cloud-2: #80cbc4; --cloud-3: #ce93d8; --cloud-4: #90caf9; --cloud-5: #ffcc80; }
button { background: transparent; border: 0; font-family: inherit; }
.segment-heading, .segment-footer { width: 100%; text-align: left; cursor: pointer; color: rgb(var(--v-theme-primary)); }
.segment-heading { font-size: 14px; font-weight: 600; }
.segment-footer { font-size: 12px; line-height: 1.6; padding-top: 12px; }
button:focus-visible { outline: 2px solid rgb(var(--v-theme-primary)); outline-offset: 3px; border-radius: 3px; }
.empty-cloud { min-height: 220px; display: grid; place-content: center; }
.insight-detail-card { display: flex; flex-direction: column; height: min(86vh, 780px); }
.insight-detail-body { display: flex; flex: 1 1 auto; flex-direction: column; min-height: 0; overflow: hidden; }
.detail-columns { display: grid; flex: 1 1 auto; grid-template-columns: minmax(0, 1fr) minmax(0, 1.7fr); gap: 24px; min-height: 0; }
.detail-column { display: flex; flex-direction: column; min-height: 0; min-width: 0; }
.keyword-search { flex: 0 0 auto; }
.keyword-list, .conversation-list { flex: 1 1 auto; min-height: 0; overflow-y: auto; }
.scroll-region {
  overscroll-behavior: contain;
  padding-inline-end: 4px;
  scrollbar-color: transparent transparent;
  scrollbar-width: thin;
}
.scroll-region:hover,
.scroll-region:focus-within { scrollbar-color: rgba(var(--v-theme-on-surface), .28) transparent; }
.scroll-region::-webkit-scrollbar { width: 8px; }
.scroll-region::-webkit-scrollbar-track { background: transparent; }
.scroll-region::-webkit-scrollbar-thumb {
  background-color: transparent;
  background-clip: content-box;
  border: 2px solid transparent;
  border-radius: 999px;
}
.scroll-region:hover::-webkit-scrollbar-thumb,
.scroll-region:focus-within::-webkit-scrollbar-thumb { background-color: rgba(var(--v-theme-on-surface), .28); }
.scroll-region::-webkit-scrollbar-thumb:hover { background-color: rgba(var(--v-theme-on-surface), .42); }
.keyword-row { display: flex; align-items: center; justify-content: space-between; gap: 16px; padding: 12px; width: 100%; text-align: left; cursor: pointer; border-bottom: 1px solid rgba(var(--v-theme-on-surface), .1); }
.keyword-label { min-width: 0; overflow-wrap: anywhere; }
.keyword-count { flex: 0 0 auto; white-space: nowrap; font-variant-numeric: tabular-nums; }
.keyword-row.selected, .keyword-row:hover { background: rgba(var(--v-theme-primary), .09); color: rgb(var(--v-theme-primary)); }
.conversation-link { max-width: 100%; height: auto; min-height: 32px; }
.conversation-link :deep(.v-btn__content) { white-space: normal; text-align: left; overflow-wrap: anywhere; }
.evidence { white-space: pre-wrap; overflow-wrap: anywhere; }
@media (max-width: 600px) {
  .insight-detail-card { height: auto; max-height: 90vh; }
  .insight-detail-body { display: block; overflow-y: auto; }
  .detail-columns { display: grid; grid-template-columns: minmax(0, 1fr); gap: 20px; overflow: visible; }
  .keyword-list { max-height: 220px; }
  .conversation-list { max-height: none; }
}
</style>
