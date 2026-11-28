<template>
  <v-dialog :model-value="modelValue" max-width="760" scrollable @update:model-value="v => emit('update:modelValue', v)">
    <v-card data-testid="team-test">
      <v-card-title class="d-flex align-center">
        <v-icon color="primary" size="20" class="mr-2">mdi-play-circle-outline</v-icon>
        <span class="text-truncate">{{ $t('ag_test_title', { name: team?.name || '' }) }}</span>
        <v-spacer />
        <v-btn icon="mdi-close" variant="text" size="small" density="comfortable" :aria-label="$t('cancel')" @click="emit('update:modelValue', false)" />
      </v-card-title>
      <v-divider />

      <v-card-text>
        <div class="text-body-2 text-medium-emphasis mb-3">{{ $t('ag_test_intro') }}</div>

        <div class="d-flex flex-wrap align-center ga-3 mb-3">
          <v-btn-toggle v-model="audience" mandatory density="compact" variant="outlined" color="primary" divided data-testid="test-audience">
            <v-btn value="customer">{{ $t('ag_sender_customer') }}</v-btn>
            <v-btn value="staff" :disabled="!canStaff">{{ $t('ag_sender_staff') }}</v-btn>
          </v-btn-toggle>
          <v-select
            v-model="currentAgent"
            :items="currentItems"
            :label="$t('ag_test_current')"
            density="compact"
            variant="outlined"
            hide-details
            class="current-select"
            data-testid="test-current"
          />
        </div>

        <v-textarea
          v-model="message"
          :label="$t('ag_test_message')"
          rows="2"
          auto-grow
          counter="2000"
          variant="outlined"
          data-testid="test-message"
          @keydown.enter.exact.prevent="run"
        />
        <div class="d-flex flex-wrap align-center ga-2">
          <v-switch v-model="reply" :label="$t('ag_test_reply')" color="primary" density="compact" hide-details inset data-testid="test-reply" />
          <v-spacer />
          <v-btn variant="text" :disabled="running || (!result && !currentAgent)" @click="restart">{{ $t('ag_test_restart') }}</v-btn>
          <v-btn color="primary" variant="flat" prepend-icon="mdi-send" :loading="running" :disabled="!message.trim()" data-testid="test-run" @click="run">{{ $t('ag_test_run') }}</v-btn>
        </div>
        <div v-if="reply" class="text-caption text-medium-emphasis mt-1">{{ $t('ag_test_reply_hint') }}</div>

        <v-alert v-if="error" type="warning" variant="tonal" density="compact" class="mt-4" data-testid="test-error">{{ error }}</v-alert>

        <v-alert v-if="result?.silent" type="warning" variant="tonal" density="compact" class="mt-4" data-testid="test-silent">
          {{ $t(`ag_silent_warning_${audience}`) }}
        </v-alert>
        <v-card v-else-if="result" variant="tonal" class="pa-3 mt-4" data-testid="test-result">
          <div class="d-flex flex-wrap align-center ga-2">
            <span class="text-medium-emphasis">{{ $t('ag_test_answered_by') }}</span>
            <v-chip color="primary" variant="flat" size="small" data-testid="test-agent">{{ result.agent?.name }}</v-chip>
            <span class="text-caption text-medium-emphasis">{{ reasonText(result.reason) }}</span>
          </div>
          <div v-if="result.router_reason" class="text-body-2 mt-2" data-testid="test-router-reason">“{{ result.router_reason }}”</div>
          <div class="text-caption text-medium-emphasis mt-2">
            {{ $t('ag_test_candidates') }}: {{ (result.candidates || []).map(c => c.name).join(', ') }}
          </div>
          <template v-if="result.reply !== undefined">
            <v-divider class="my-3" />
            <div class="reply-text text-body-2" data-testid="test-reply-text">{{ result.reply || $t('ag_test_empty_reply') }}</div>
            <div v-if="result.usage" class="text-caption text-medium-emphasis mt-2">
              {{ result.usage.model || result.runtime }} · {{ result.usage.input_tokens + result.usage.output_tokens }} tokens{{ result.usage.estimated ? ` (${$t('ag_test_estimated')})` : '' }} · ${{ result.usage.cost_usd.toFixed(4) }}
            </div>
          </template>
        </v-card>
      </v-card-text>
    </v-card>
  </v-dialog>
</template>

<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import api from '../../api'
import type { Team } from './types'

interface Brief { id: string, key: string, name: string }
interface ConsoleResult {
  silent?: boolean
  agent?: Brief
  reason?: string
  router_reason?: string
  routed?: boolean
  candidates?: Brief[]
  reply?: string
  runtime?: string
  session_id?: string
  usage?: { provider: string, model: string, input_tokens: number, output_tokens: number, estimated: boolean, cost_usd: number }
}

const props = defineProps<{ modelValue: boolean, tenantId: string, team: Team | null, canStaff: boolean }>()
const emit = defineEmits<{ (e: 'update:modelValue', v: boolean): void }>()
const { t, te } = useI18n()

const audience = ref<'customer' | 'staff'>('customer')
const currentAgent = ref('')
const message = ref('')
const reply = ref(false)
const running = ref(false)
const error = ref('')
const result = ref<ConsoleResult | null>(null)
const sessionId = ref('')

function serves(a: { audience: string, is_active: boolean }, aud: string) {
  return a.is_active && (a.audience === aud || a.audience === 'all')
}

// The agents a chat with this sender could already be with.
const currentItems = computed(() => {
  const team = props.team
  const enabled = new Set((team?.memberships || []).filter(m => m.enabled).map(m => m.agent_id))
  const members = (team?.members || []).filter(a => (!team?.memberships?.length || enabled.has(a.id)) && serves(a, audience.value))
  return [{ title: t('ag_test_current_none'), value: '' }, ...members.map(a => ({ title: a.name, value: a.key }))]
})

function restart() {
  currentAgent.value = ''
  sessionId.value = ''
  result.value = null
  error.value = ''
}

watch(() => [props.modelValue, props.team?.id], () => {
  if (props.modelValue) {
    restart()
    message.value = ''
    audience.value = 'customer'
  }
})
watch(audience, restart)

function reasonText(reason?: string) {
  const key = `ag_test_reason_${reason}`
  return reason && te(key) ? t(key) : reason || ''
}

async function run() {
  const text = message.value.trim()
  if (!text || !props.team || running.value) return
  running.value = true
  error.value = ''
  try {
    const { data } = await api.post<ConsoleResult>(`/tenants/${props.tenantId}/ai-agents/test`, {
      team_id: props.team.id,
      audience: audience.value,
      message: text,
      current_agent: currentAgent.value,
      reply: reply.value,
      session_id: reply.value && result.value?.agent?.key === currentAgent.value ? sessionId.value : '',
    })
    result.value = data
    if (data.agent) {
      // The next message continues the same chat with this agent.
      currentAgent.value = data.agent.key
      sessionId.value = data.session_id || ''
    }
  } catch (err) {
    const resp = (err as { response?: { data?: { error?: string, routing?: ConsoleResult } } }).response?.data
    const code = resp?.error || ''
    error.value = te(`ag_test_err_${code}`) ? t(`ag_test_err_${code}`) : t('ag_test_err_generic')
    if (resp?.routing) result.value = resp.routing
  } finally {
    running.value = false
  }
}

defineExpose({ run, result, currentAgent, audience, message, reply })
</script>

<style scoped>
.current-select {
  min-width: 220px;
  max-width: 320px;
}
.reply-text {
  white-space: pre-wrap;
}
</style>
