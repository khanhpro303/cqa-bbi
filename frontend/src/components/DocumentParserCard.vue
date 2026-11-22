<template>
  <v-card class="pa-6 mb-6" data-testid="document-parser">
    <div class="text-subtitle-1 font-weight-bold mb-1">
      <v-icon start size="small">mdi-file-search-outline</v-icon>
      {{ $t('dp_title') }}
    </div>
    <div class="text-body-2 text-medium-emphasis mb-4">{{ $t('dp_desc') }}</div>

    <v-alert :type="state.enabled ? 'success' : 'info'" variant="tonal" density="compact" class="mb-4" data-testid="dp-state">
      {{ state.enabled ? $t('dp_on') : $t('dp_off') }}
      <span v-if="state.env_configured"> {{ $t('dp_env') }}</span>
    </v-alert>

    <v-row dense>
      <v-col cols="12" md="7">
        <v-text-field v-model="form.api_url" :label="$t('dp_url')" :placeholder="state.default_api_url" :hint="$t('dp_url_hint')" persistent-hint density="compact" />
      </v-col>
      <v-col cols="12" md="5">
        <v-text-field
          v-model="form.api_key"
          :label="$t('dp_key')"
          :placeholder="state.has_key ? '••••••••' : ''"
          :hint="state.has_key ? $t('dp_key_saved') : ''"
          persistent-hint
          type="password"
          autocomplete="off"
          density="compact"
        />
      </v-col>
      <v-col cols="12" md="6">
        <v-select v-model="form.strategy" :items="strategies" :label="$t('dp_strategy')" density="compact" :hint="$t('dp_strategy_hint')" persistent-hint />
      </v-col>
      <v-col cols="12" md="6">
        <v-text-field v-model="form.languages" :label="$t('dp_languages')" placeholder="vie,eng" density="compact" :hint="$t('dp_languages_hint')" persistent-hint />
      </v-col>
    </v-row>

    <v-alert v-if="testResult" :type="testResult.ok ? 'success' : 'error'" variant="tonal" density="compact" class="mt-3">
      {{ testResult.ok ? $t('dp_test_ok') : testResult.message }}
    </v-alert>

    <div class="d-flex flex-wrap ga-2 mt-4">
      <v-btn v-if="state.has_key" variant="text" color="error" @click="save(true)">{{ $t('dp_clear_key') }}</v-btn>
      <v-spacer />
      <v-btn variant="outlined" :loading="testing" :disabled="!state.enabled" @click="test">{{ $t('dp_test') }}</v-btn>
      <v-btn color="primary" :loading="saving" @click="save(false)">{{ $t('save') }}</v-btn>
    </div>
  </v-card>
</template>

<script setup lang="ts">
import { onMounted, ref } from 'vue'
import api from '../api'

const props = defineProps<{ tenantId: string }>()

interface ParserState {
  api_url: string
  default_api_url: string
  strategy: string
  languages: string
  has_key: boolean
  env_configured: boolean
  enabled: boolean
}

const state = ref<ParserState>({ api_url: '', default_api_url: '', strategy: '', languages: '', has_key: false, env_configured: false, enabled: false })
const form = ref({ api_url: '', api_key: '', strategy: 'auto', languages: 'vie,eng' })
const strategies = [
  { title: 'auto', value: 'auto' },
  { title: 'hi_res', value: 'hi_res' },
  { title: 'fast', value: 'fast' },
  { title: 'ocr_only', value: 'ocr_only' },
]
const saving = ref(false)
const testing = ref(false)
const testResult = ref<{ ok: boolean; message?: string } | null>(null)

function apply(data: ParserState) {
  state.value = data
  form.value = { api_url: data.api_url || '', api_key: '', strategy: data.strategy || 'auto', languages: data.languages || 'vie,eng' }
}

async function load() {
  const { data } = await api.get(`/tenants/${props.tenantId}/knowledge/parser`)
  apply(data)
}

async function save(clearKey: boolean) {
  saving.value = true
  testResult.value = null
  try {
    const { data } = await api.put(`/tenants/${props.tenantId}/knowledge/parser`, { ...form.value, clear_key: clearKey })
    apply(data)
  } catch (e: any) {
    testResult.value = { ok: false, message: e?.response?.data?.message || e?.response?.data?.error || String(e) }
  } finally {
    saving.value = false
  }
}

async function test() {
  testing.value = true
  testResult.value = null
  try {
    const { data } = await api.post(`/tenants/${props.tenantId}/knowledge/parser/test`)
    testResult.value = data
  } catch (e: any) {
    testResult.value = { ok: false, message: e?.response?.data?.message || e?.response?.data?.error || String(e) }
  } finally {
    testing.value = false
  }
}

onMounted(load)
defineExpose({ load, save, test, form, state, testResult })
</script>
