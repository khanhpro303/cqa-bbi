<template>
  <v-card class="pa-6 mb-6" data-testid="firecrawl-card">
    <div class="text-subtitle-1 font-weight-bold mb-1">
      <v-icon start size="small">mdi-web</v-icon>
      {{ t('firecrawl_title') }}
    </div>
    <div class="text-body-2 text-medium-emphasis mb-4">{{ t('firecrawl_desc') }}</div>

    <v-alert v-if="state" :type="state.enabled ? 'success' : 'info'" variant="tonal" density="compact" class="mb-4">
      {{ t(state.enabled ? 'firecrawl_on' : 'firecrawl_off') }}
    </v-alert>
    <v-progress-linear v-if="operation === 'load'" indeterminate :aria-label="t('firecrawl_title')" />

    <form v-if="state" @submit.prevent="save(false)">
      <v-text-field
        v-model="apiKey"
        :label="t('dp_key')"
        :placeholder="state.has_key ? '••••••••' : ''"
        :hint="state.has_key ? t('dp_key_saved') : ''"
        :disabled="!!operation"
        persistent-hint
        type="password"
        autocomplete="new-password"
        density="compact"
        data-testid="firecrawl-key"
      />

      <div class="d-flex flex-wrap ga-2 mt-4">
        <v-btn v-if="state.has_key" type="button" variant="text" color="error" :disabled="!!operation" :loading="operation === 'clear'" data-testid="firecrawl-clear" @click="save(true)">{{ t('dp_clear_key') }}</v-btn>
        <v-spacer />
        <v-btn type="button" variant="outlined" :loading="operation === 'test'" :disabled="!state.enabled || !!operation || !!apiKey.trim()" data-testid="firecrawl-test" @click="test">{{ t('dp_test') }}</v-btn>
        <v-btn type="submit" color="primary" :loading="operation === 'save'" :disabled="!apiKey.trim() || !!operation">{{ t('save') }}</v-btn>
      </div>
    </form>

    <v-alert v-if="feedback" :type="feedback.type" variant="tonal" density="compact" class="mt-3" data-testid="firecrawl-feedback">
      {{ feedback.message }}
      <v-btn v-if="!state" type="button" variant="text" size="small" @click="load">{{ t('kb_retry') }}</v-btn>
    </v-alert>
  </v-card>
</template>

<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import api from '../api'

interface FirecrawlState {
  has_key: boolean
  enabled: boolean
}

const props = defineProps<{ tenantId: string }>()
const { t } = useI18n()
const endpoint = computed(() => `/tenants/${props.tenantId}/knowledge/firecrawl`)
const state = ref<FirecrawlState | null>(null)
const apiKey = ref('')
const operation = ref<'load' | 'save' | 'clear' | 'test' | null>(null)
const feedback = ref<{ type: 'success' | 'error'; message: string } | null>(null)
let version = 0

async function load() {
  const request = ++version
  state.value = null
  apiKey.value = ''
  feedback.value = null
  operation.value = 'load'
  try {
    const { data } = await api.get<FirecrawlState>(endpoint.value)
    if (request === version) state.value = data
  } catch {
    if (request === version) feedback.value = { type: 'error', message: t('firecrawl_load_failed') }
  } finally {
    if (request === version) operation.value = null
  }
}

async function save(clearKey: boolean) {
  const key = apiKey.value.trim()
  if (operation.value || !state.value || (!clearKey && !key)) return
  const request = version
  operation.value = clearKey ? 'clear' : 'save'
  feedback.value = null
  try {
    const { data } = await api.put<FirecrawlState>(endpoint.value, clearKey ? { clear_key: true } : { api_key: key })
    if (request !== version) return
    state.value = data
    apiKey.value = ''
    feedback.value = { type: 'success', message: t(clearKey ? 'firecrawl_cleared' : 'firecrawl_saved') }
  } catch {
    if (request === version) feedback.value = { type: 'error', message: t('firecrawl_save_failed') }
  } finally {
    if (request === version) operation.value = null
  }
}

async function test() {
  if (operation.value || !state.value?.enabled || apiKey.value.trim()) return
  const request = version
  operation.value = 'test'
  feedback.value = null
  try {
    const { data } = await api.post<{ ok: boolean; message?: string }>(`${endpoint.value}/test`)
    if (request !== version) return
    feedback.value = data.ok
      ? { type: 'success', message: t('firecrawl_test_ok') }
      : { type: 'error', message: data.message || t('firecrawl_test_failed') }
  } catch {
    if (request === version) feedback.value = { type: 'error', message: t('firecrawl_test_failed') }
  } finally {
    if (request === version) operation.value = null
  }
}

watch(endpoint, load, { immediate: true })
</script>
