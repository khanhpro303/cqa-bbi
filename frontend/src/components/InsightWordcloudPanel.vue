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
              <v-card v-for="row in sourceConversations" :key="row.conversation_id" variant="outlined" class="conversation-card mb-3">
                <div class="conversation-card-header">
                  <button
                    class="conversation-toggle"
                    type="button"
                    :aria-expanded="Boolean(expandedConversations[row.conversation_id])"
                    :aria-controls="`conversation-detail-${row.conversation_id}`"
                    @click="toggleConversation(row)"
                  >
                    <span class="conversation-heading">
                      <strong class="text-primary">{{ row.customer_name || t('qualityInsight.messengerCustomer') }}</strong>
                      <span class="text-caption text-medium-emphasis">{{ row.channel_name }} · {{ row.conversation_id }}</span>
                      <span v-for="(evidence, index) in keywordEvidence(row)" :key="index" class="text-body-2 evidence">{{ evidence }}</span>
                      <span class="text-body-2 evidence conversation-summary">{{ row.insight?.summary || t('qualityInsight.noSummary') }}</span>
                    </span>
                    <v-icon size="small" :icon="expandedConversations[row.conversation_id] ? 'mdi-chevron-up' : 'mdi-chevron-down'" />
                  </button>
                  <v-btn
                    class="conversation-link"
                    icon="mdi-open-in-new"
                    size="small"
                    variant="text"
                    color="primary"
                    :title="t('qualityInsight.openMessages')"
                    :to="{ name: 'messages', params: { tenantId }, query: { conv: row.conversation_id, channel_id: row.channel_id, tab: 'messages' } }"
                  />
                </div>

                <div
                  v-if="expandedConversations[row.conversation_id]"
                  :id="`conversation-detail-${row.conversation_id}`"
                  class="conversation-detail"
                >
                  <v-divider />
                  <div class="conversation-detail-grid">
                    <section>
                      <h3 class="detail-title"><v-icon size="small" icon="mdi-chat-outline" />{{ t('qualityInsight.transcript') }}</h3>
                      <div v-if="loadingConversations[row.conversation_id]" class="detail-state">
                        <v-progress-circular indeterminate size="24" width="2" />
                        <span>{{ t('qualityInsight.loadingConversation') }}</span>
                      </div>
                      <div v-else-if="conversationErrors[row.conversation_id]" class="detail-state text-error">
                        <span>{{ t('qualityInsight.loadConversationError') }}</span>
                        <v-btn size="small" variant="text" color="primary" @click="loadConversation(row, true)">{{ t('qualityInsight.retry') }}</v-btn>
                      </div>
                      <div v-else-if="conversationMessages[row.conversation_id]?.length" class="chat-transcript">
                        <article
                          v-for="message in conversationMessages[row.conversation_id]"
                          :key="message.id"
                          class="chat-message"
                          :class="message.sender_type === 'agent' ? 'chat-message-agent' : 'chat-message-customer'"
                        >
                          <header>
                            <strong>{{ message.sender_name || (message.sender_type === 'agent' ? row.channel_name : row.customer_name) }}</strong>
                            <time>{{ formatMessageTime(message.sent_at) }}</time>
                          </header>
                          <p v-if="message.content" class="evidence">{{ message.content }}</p>
                          <span v-else class="text-caption text-medium-emphasis">[{{ message.content_type || t('qualityInsight.attachment') }}]</span>
                        </article>
                      </div>
                      <div v-else class="detail-state text-medium-emphasis">{{ t('qualityInsight.noMessages') }}</div>
                    </section>

                    <section>
                      <h3 class="detail-title"><v-icon size="small" icon="mdi-alert-circle-outline" />{{ t('qualityInsight.evaluationDetail') }}</h3>
                      <v-alert type="success" variant="tonal" density="compact" class="mb-3">
                        {{ row.insight?.summary || t('qualityInsight.noSummary') }}
                      </v-alert>
                      <div class="d-flex align-center flex-wrap ga-2 mb-2">
                        <v-chip size="x-small" color="warning" variant="tonal">{{ t('qualityInsight.classified') }}</v-chip>
                        <strong class="text-body-2">{{ selectedKeyword?.label }}</strong>
                      </div>
                      <div
                        v-for="(evidence, index) in keywordEvidence(row)"
                        :key="index"
                        class="classification-evidence evidence"
                      >{{ evidence }}</div>
                      <p v-if="!keywordEvidence(row).length" class="text-body-2 text-medium-emphasis mb-0">
                        {{ t('qualityInsight.noEvidence') }}
                      </p>
                    </section>
                  </div>
                </div>
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
import api from '../api'
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

interface ConversationMessage {
  id: string
  sender_type: string
  sender_name: string
  content: string
  content_type: string
  sent_at: string
}

const props = defineProps<{ groups: InsightGroup[]; conversations: SourceConversation[]; tenantId: string }>()
const { t, locale } = useI18n()
const cloudLimit = 40
const dialog = ref(false)
const selectedKind = ref('')
const selectedKey = ref('')
const keywordSearch = ref('')
const expandedConversations = ref<Record<string, boolean>>({})
const conversationMessages = ref<Record<string, ConversationMessage[]>>({})
const loadingConversations = ref<Record<string, boolean>>({})
const conversationErrors = ref<Record<string, boolean>>({})
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

async function toggleConversation(row: SourceConversation) {
  const id = row.conversation_id
  expandedConversations.value[id] = !expandedConversations.value[id]
  if (expandedConversations.value[id] && !conversationMessages.value[id] && !loadingConversations.value[id]) {
    await loadConversation(row)
  }
}

async function loadConversation(row: SourceConversation, force = false) {
  const id = row.conversation_id
  const tenantId = props.tenantId
  if (!force && (conversationMessages.value[id] || loadingConversations.value[id])) return
  loadingConversations.value[id] = true
  conversationErrors.value[id] = false
  try {
    const { data } = await api.get(`/tenants/${tenantId}/conversations/${id}/messages`)
    if (props.tenantId !== tenantId) return
    conversationMessages.value[id] = Array.isArray(data.messages) ? data.messages : []
  } catch {
    if (props.tenantId !== tenantId) return
    conversationErrors.value[id] = true
  } finally {
    if (props.tenantId === tenantId) loadingConversations.value[id] = false
  }
}

function formatMessageTime(value: string) {
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return '—'
  return date.toLocaleString(locale.value === 'en' ? 'en-US' : 'vi-VN', { dateStyle: 'short', timeStyle: 'short' })
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

watch(() => props.tenantId, () => {
  dialog.value = false
  expandedConversations.value = {}
  conversationMessages.value = {}
  loadingConversations.value = {}
  conversationErrors.value = {}
})
watch(selectedKey, () => { expandedConversations.value = {} })
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
.conversation-card { overflow: hidden; }
.conversation-card-header { display: flex; align-items: stretch; }
.conversation-toggle { display: flex; flex: 1 1 auto; align-items: center; justify-content: space-between; gap: 16px; min-width: 0; padding: 16px; text-align: left; cursor: pointer; }
.conversation-heading { display: flex; flex-direction: column; gap: 4px; min-width: 0; }
.conversation-summary { display: -webkit-box; overflow: hidden; -webkit-box-orient: vertical; -webkit-line-clamp: 2; }
.conversation-link { align-self: center; flex: 0 0 auto; margin-inline-end: 8px; }
.conversation-detail { padding: 0 16px 16px; }
.conversation-detail-grid { display: grid; grid-template-columns: minmax(0, 1.45fr) minmax(260px, 1fr); gap: 20px; padding-top: 16px; }
.detail-title { display: flex; align-items: center; gap: 6px; margin-bottom: 10px; color: rgb(var(--v-theme-on-surface)); font-size: 13px; font-weight: 700; }
.detail-state { display: flex; align-items: center; justify-content: center; gap: 8px; min-height: 120px; font-size: 13px; }
.chat-transcript { max-height: 420px; overflow-y: auto; padding: 8px; border-radius: 8px; background: rgba(var(--v-theme-on-surface), .04); }
.chat-message { margin-bottom: 8px; padding: 10px; border: 1px solid rgba(var(--v-theme-on-surface), .12); border-radius: 8px; }
.chat-message:last-child { margin-bottom: 0; }
.chat-message-agent { margin-left: 28px; background: rgba(var(--v-theme-primary), .08); }
.chat-message-customer { margin-right: 28px; background: rgb(var(--v-theme-surface)); }
.chat-message header { display: flex; align-items: center; justify-content: space-between; gap: 12px; margin-bottom: 4px; font-size: 12px; }
.chat-message time { flex: 0 0 auto; color: rgba(var(--v-theme-on-surface), .58); }
.chat-message p { margin: 0; font-size: 13px; }
.classification-evidence { margin-bottom: 8px; padding: 10px; border-left: 3px solid rgb(var(--v-theme-warning)); border-radius: 4px; background: rgba(var(--v-theme-warning), .11); font-size: 13px; }
.evidence { white-space: pre-wrap; overflow-wrap: anywhere; }
@media (max-width: 600px) {
  .insight-detail-card { height: auto; max-height: 90vh; }
  .insight-detail-body { display: block; overflow-y: auto; }
  .detail-columns { display: grid; grid-template-columns: minmax(0, 1fr); gap: 20px; overflow: visible; }
  .keyword-list { max-height: 220px; }
  .conversation-list { max-height: none; }
  .conversation-detail-grid { grid-template-columns: minmax(0, 1fr); }
  .conversation-toggle { padding: 12px; }
  .chat-message-agent { margin-left: 16px; }
  .chat-message-customer { margin-right: 16px; }
}
</style>
