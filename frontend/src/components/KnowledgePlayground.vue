<template>
  <v-card variant="outlined" class="knowledge-playground mb-5">
    <v-tabs v-model="mode" color="primary" density="compact" class="playground-tabs" :aria-label="t('kb_playground_label')">
      <v-tab id="kb-chat-tab" value="chat" aria-controls="kb-chat-panel" prepend-icon="mdi-chat-outline">
        {{ t('kb_chat_tab') }}
      </v-tab>
      <v-tab id="kb-search-tab" value="search" aria-controls="kb-search-panel" prepend-icon="mdi-magnify">
        {{ t('kb_search_tab') }}
      </v-tab>
    </v-tabs>

    <section v-show="mode === 'chat'" id="kb-chat-panel" role="tabpanel" aria-labelledby="kb-chat-tab">
      <div class="playground-header">
        <div>
          <h2>{{ t('kb_chat_title') }}</h2>
          <p>{{ t('kb_chat_description') }}</p>
        </div>
        <div class="playground-controls">
          <v-select v-model="chatAgent" :items="chatAgents" :label="t('kb_as_label')" density="compact" hide-details class="agent-select" />
          <v-btn
            variant="text" prepend-icon="mdi-plus" :disabled="!messages.length && !question && !chatError"
            @click="startNewChat"
          >{{ t('kb_chat_reset') }}</v-btn>
        </div>
      </div>

      <div ref="transcript" class="transcript" :tabindex="messages.length ? 0 : undefined" :aria-label="t('kb_chat_transcript')">
        <div v-if="!messages.length" class="chat-empty">
          <v-icon icon="mdi-message-processing-outline" size="22" aria-hidden="true" />
          <p>{{ t('kb_chat_starter') }}</p>
          <div class="d-flex justify-center flex-wrap ga-2">
            <v-btn variant="outlined" size="small" rounded="pill" @click="useExample('kb_chat_example_warranty')">
              {{ t('kb_chat_example_warranty') }}
            </v-btn>
            <v-btn variant="outlined" size="small" rounded="pill" @click="useExample('kb_chat_example_returns')">
              {{ t('kb_chat_example_returns') }}
            </v-btn>
          </div>
        </div>
        <div class="chat-log" role="log" :aria-label="t('kb_chat_transcript')">
          <div v-for="(message, index) in messages" :key="index" class="message" :class="{ 'message-user': message.role === 'user' }">
            <span class="message-label">{{ t(message.role === 'user' ? 'kb_chat_you' : 'kb_chat_bot') }}</span>
            <div class="message-text">{{ message.text }}</div>
          </div>
          <div v-if="chatRequest" class="message">
            <span class="message-label">{{ t('kb_chat_bot') }}</span>
            <div class="message-text d-flex align-center ga-2">
              <v-progress-circular indeterminate size="15" width="2" color="primary" aria-hidden="true" />
              {{ t('kb_chat_waiting') }}
            </div>
          </div>
        </div>
      </div>
      <v-alert v-if="chatError" type="error" variant="tonal" density="compact" class="mx-4 mt-3" role="alert">
        {{ t(chatError) }}
      </v-alert>
      <form class="playground-form" @submit.prevent="sendQuestion">
        <v-text-field
          ref="chatInput" v-model="question" :label="t('kb_chat_input')" density="compact" hide-details
          maxlength="2000" :readonly="!!chatRequest"
        />
        <v-btn type="submit" color="primary" variant="flat" prepend-icon="mdi-send-outline" :disabled="!question.trim() || !!chatRequest">
          {{ t('kb_chat_send') }}
        </v-btn>
      </form>
    </section>

    <section v-show="mode === 'search'" id="kb-search-panel" role="tabpanel" aria-labelledby="kb-search-tab">
      <div class="playground-header">
        <div>
          <h2>{{ t('kb_search_title') }}</h2>
          <p>{{ t('kb_search_description') }}</p>
        </div>
        <v-select v-model="searchAgent" :items="agents" :label="t('kb_as_label')" density="compact" hide-details class="agent-select" />
      </div>
      <form class="playground-form search-form" @submit.prevent="runSearch">
        <v-text-field v-model="searchQuery" :label="t('kb_search_input')" density="compact" hide-details maxlength="2000" />
        <v-btn type="submit" color="primary" variant="flat" prepend-icon="mdi-magnify" :disabled="!searchQuery.trim() || !!searchRequest">
          {{ t('kb_search_button') }}
        </v-btn>
      </form>
      <div v-if="searchRequest || hits || searchError" class="search-results">
        <p v-if="searchRequest" role="status" class="search-state">{{ t('kb_search_waiting') }}</p>
        <v-alert v-else-if="searchError" type="error" variant="tonal" density="compact" role="alert">{{ t('kb_search_error') }}</v-alert>
        <template v-else-if="hits">
          <p role="status" class="search-state">
            {{ hits.length ? t('kb_search_count', { count: hits.length }) : t('kb_search_no_hits') }}
          </p>
          <details v-for="(hit, index) in hits" :key="index" class="search-hit">
            <summary>
              <div class="hit-heading">
                <strong>{{ hit.title }}</strong>
                <span>{{ t('kb_score') }} {{ hit.score.toFixed(3) }}</span>
                <v-icon icon="mdi-chevron-down" size="18" class="hit-chevron" aria-hidden="true" />
              </div>
              <div class="hit-excerpt hit-preview" aria-hidden="true">{{ hit.content }}</div>
            </summary>
            <p class="hit-excerpt hit-full">{{ hit.content }}</p>
          </details>
        </template>
      </div>
    </section>
  </v-card>
</template>

<script setup lang="ts">
import { isAxiosError } from 'axios'
import { computed, onBeforeUnmount, ref, shallowRef, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import api from '../api'

type Agent = 'public' | 'private'
type Hit = { title: string; content: string; score: number }
type Message = { role: 'user' | 'bot'; text: string }

const props = defineProps<{ base: string; isAdmin: boolean }>()
const { t } = useI18n()
const mode = ref<'chat' | 'search'>('chat')
const agents = computed(() => [
  { title: t('kb_as_customer'), value: 'public' },
  { title: t('kb_as_staff'), value: 'private' },
])
const chatAgents = computed(() => props.isAdmin ? agents.value : agents.value.slice(0, 1))

const chatAgent = ref<Agent>('public')
const question = ref('')
const messages = ref<Message[]>([])
const sessionID = ref('')
const chatError = ref('')
const chatRequest = shallowRef<AbortController>()
const transcript = ref<HTMLElement>()
const chatInput = ref<{ focus: () => void }>()

function useExample(key: string) {
  question.value = t(key)
  chatInput.value?.focus()
}

function resetChat() {
  chatRequest.value?.abort()
  chatRequest.value = undefined
  question.value = ''
  messages.value = []
  sessionID.value = ''
  chatError.value = ''
}

function startNewChat() {
  resetChat()
  chatInput.value?.focus()
}

watch(chatAgent, resetChat, { flush: 'sync' })
watch([() => messages.value.length, chatRequest, mode], () => {
  if (mode.value === 'chat' && transcript.value) transcript.value.scrollTop = transcript.value.scrollHeight
}, { flush: 'post' })

async function sendQuestion() {
  const text = question.value.trim()
  if (!text || chatRequest.value) return
  chatInput.value?.focus()
  const request = new AbortController()
  chatRequest.value = request
  chatError.value = ''
  question.value = ''
  messages.value.push({ role: 'user', text })
  try {
    const { data } = await api.post<{ answer: string; session_id: string }>(`${props.base}/chat-preview`, {
      question: text,
      agent_type: chatAgent.value,
      ...(sessionID.value && { session_id: sessionID.value }),
    }, { signal: request.signal })
    if (chatRequest.value !== request) return
    sessionID.value = data.session_id
    messages.value.push({ role: 'bot', text: data.answer })
  } catch (error) {
    if (chatRequest.value !== request) return
    messages.value.pop()
    question.value = text
    const response = isAxiosError(error) ? error.response : undefined
    if (response?.status === 429) chatError.value = 'kb_chat_rate_limited'
    else if (response?.data?.error === 'preview_flow_not_configured') chatError.value = 'kb_chat_unconfigured'
    else chatError.value = 'kb_chat_error'
  } finally {
    if (chatRequest.value === request) chatRequest.value = undefined
  }
}

const searchAgent = ref<Agent>('public')
const searchQuery = ref('')
const hits = ref<Hit[] | null>(null)
const searchRequest = shallowRef<AbortController>()
const searchError = ref(false)

function clearSearch() {
  searchRequest.value?.abort()
  searchRequest.value = undefined
  hits.value = null
  searchError.value = false
}

watch(searchAgent, clearSearch, { flush: 'sync' })
watch(() => props.base, () => {
  resetChat()
  clearSearch()
  chatAgent.value = 'public'
  searchAgent.value = 'public'
  searchQuery.value = ''
  mode.value = 'chat'
}, { flush: 'sync' })
watch(() => props.isAdmin, (isAdmin) => {
  if (!isAdmin) chatAgent.value = 'public'
}, { flush: 'sync' })
onBeforeUnmount(() => {
  chatRequest.value?.abort()
  searchRequest.value?.abort()
})

async function runSearch() {
  const query = searchQuery.value.trim()
  if (!query || searchRequest.value) return
  const request = new AbortController()
  searchRequest.value = request
  searchError.value = false
  hits.value = null
  try {
    const { data } = await api.post<{ hits: Hit[] }>(`${props.base}/search`, {
      query,
      agent_type: searchAgent.value,
    }, { signal: request.signal })
    if (searchRequest.value === request) hits.value = data.hits || []
  } catch {
    if (searchRequest.value === request) searchError.value = true
  } finally {
    if (searchRequest.value === request) searchRequest.value = undefined
  }
}
</script>

<style scoped>
.knowledge-playground {
  border-color: rgba(var(--v-border-color), var(--v-border-opacity));
}
.playground-tabs {
  padding-inline: 0.5rem;
  border-bottom: 1px solid rgba(var(--v-border-color), var(--v-border-opacity));
}
.playground-header {
  display: flex;
  justify-content: space-between;
  align-items: center;
  flex-wrap: wrap;
  gap: 0.85rem 1.5rem;
  padding: 1rem;
}
.playground-header h2 {
  font-size: 1rem;
  font-weight: 650;
}
.playground-header p, .chat-empty p {
  opacity: 0.8;
  font-size: 0.875rem;
}
.playground-controls {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 0.5rem;
}
.agent-select {
  flex: 0 0 12.5rem;
  min-width: 12.5rem;
}
.transcript {
  display: flex;
  flex-direction: column;
  gap: 1rem;
  min-height: 10rem;
  max-height: min(24rem, 50vh);
  overflow-y: auto;
  margin-inline: 1rem;
  padding: 1rem;
  border: 1px solid rgba(var(--v-border-color), var(--v-border-opacity));
  border-radius: 0.65rem;
  background: rgba(var(--v-theme-on-surface), 0.035);
}
.chat-empty {
  margin: auto;
  text-align: center;
}
.transcript:focus-visible { outline: 2px solid rgb(var(--v-theme-primary)); outline-offset: 2px; }
.chat-log { display: flex; flex-direction: column; gap: 1rem; }
.chat-log:empty { display: none; }
.chat-empty p { margin: 0.35rem 0 0.75rem; }
.chat-empty .v-btn { text-transform: none; letter-spacing: normal; }
.message {
  align-self: flex-start;
  max-width: min(48rem, 88%);
}
.message-user { align-self: flex-end; }
.message-label {
  display: block;
  margin: 0 0.35rem 0.25rem;
  font-size: 0.75rem;
  font-weight: 600;
}
.message-user .message-label { text-align: end; }
.message-text {
  padding: 0.65rem 0.85rem;
  border-radius: 0.75rem;
  border: 1px solid rgba(var(--v-border-color), var(--v-border-opacity));
  background: rgb(var(--v-theme-surface));
  white-space: pre-wrap;
  overflow-wrap: anywhere;
  line-height: 1.6;
}
.message-user .message-text { background: rgba(var(--v-theme-primary), 0.09); }
.playground-form {
  display: flex;
  align-items: center;
  gap: 0.6rem;
  padding: 1rem;
}
.search-form { padding-top: 0; }
.search-results {
  display: grid;
  gap: 0.5rem;
  padding: 0 1rem 1rem;
}
.search-state { font-size: 0.875rem; }
.search-hit {
  border: 1px solid rgba(var(--v-border-color), var(--v-border-opacity));
  border-radius: 0.5rem;
}
.search-hit summary {
  padding: 0.65rem 0.85rem;
  cursor: pointer;
  list-style: none;
}
.search-hit summary::-webkit-details-marker { display: none; }
.search-hit summary:focus-visible { outline: 2px solid rgb(var(--v-theme-primary)); outline-offset: 2px; }
.hit-heading {
  display: flex;
  align-items: center;
  gap: 0.45rem;
  font-size: 0.875rem;
}
.hit-heading strong { flex: 1; overflow-wrap: anywhere; }
.hit-heading span { color: rgba(var(--v-theme-on-surface), 0.75); white-space: nowrap; font-size: 0.75rem; }
.search-hit[open] .hit-chevron { transform: rotate(180deg); }
.hit-excerpt { margin-top: 0.25rem; white-space: pre-wrap; overflow-wrap: anywhere; font-size: 0.875rem; line-height: 1.55; }
.hit-preview { display: -webkit-box; -webkit-box-orient: vertical; -webkit-line-clamp: 2; overflow: hidden; }
.search-hit[open] .hit-preview { display: none; }
.hit-full { margin: 0; padding: 0 0.85rem 0.75rem; }
@media (max-width: 600px) {
  .playground-controls { width: 100%; }
  .playground-controls .agent-select { flex: 1; min-width: 0; }
  .playground-controls .v-btn { padding-inline: 0.55rem; }
}
</style>
