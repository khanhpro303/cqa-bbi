<template>
  <div>
    <h1 class="text-h5 font-weight-bold mb-6">{{ $t('ai_engines') }}</h1>
    <div class="text-grey-darken-1 mb-6">{{ $t('ai_engines_desc') }}</div>

    <v-card class="pa-6 mb-6">
      <div class="text-subtitle-1 font-weight-bold mb-4">
        <v-icon start size="small">mdi-brain</v-icon>
        {{ $t('langflow_config') }}
      </div>

      <v-text-field
        v-model="langflow.baseUrl"
        :label="$t('base_url')"
        placeholder="https://langflow.example.com"
        hint="URL của Langflow server (Bỏ dấu / ở cuối)"
        persistent-hint
        clearable
        class="mb-3"
      />

      <v-text-field
        v-model="langflow.flowId"
        :label="$t('flow_id')"
        placeholder="e.g. 12345678-1234-1234-1234-123456789012"
        class="mb-3"
      />

      <v-text-field
        v-model="langflow.token"
        :label="$t('application_token')"
        :type="showKey ? 'text' : 'password'"
        :append-inner-icon="showKey ? 'mdi-eye-off' : 'mdi-eye'"
        @click:append-inner="showKey = !showKey"
        placeholder="••••••••"
        class="mb-3"
      />

      <div class="d-flex ga-2 mt-4">
        <v-btn color="primary" :loading="saving" @click="save">{{ $t('save') }}</v-btn>
        <v-btn variant="outlined" :loading="testing" @click="testConnection">
          {{ $t('test_connection') }}
        </v-btn>
      </div>
    </v-card>

    <v-snackbar v-model="snackbar" :color="snackColor" timeout="3000">{{ snackText }}</v-snackbar>
  </div>
</template>

<script setup lang="ts">
import { ref, reactive, computed, onMounted } from 'vue'
import { useRoute } from 'vue-router'
import { useI18n } from 'vue-i18n'
import api from '../api'

const route = useRoute()
const { t } = useI18n()
const tenantId = computed(() => route.params.tenantId as string)

const showKey = ref(false)
const snackbar = ref(false)
const snackText = ref('')
const snackColor = ref('success')

const saving = ref(false)
const testing = ref(false)

const langflow = reactive({
  baseUrl: '',
  flowId: '',
  token: '',
})

async function loadSettings() {
  try {
    const { data } = await api.get(`/tenants/${tenantId.value}/settings`)
    const settings = data.settings || {}

    langflow.baseUrl = settings.ai_engine_langflow_url || ''
    langflow.flowId = settings.ai_engine_langflow_flow_id || ''
    
    if (settings.ai_engine_langflow_token) {
      langflow.token = settings.ai_engine_langflow_token
    }
  } catch {
    // Ignore
  }
}

async function save() {
  saving.value = true
  try {
    await api.put(`/tenants/${tenantId.value}/settings/ai-engines`, {
      langflow_base_url: langflow.baseUrl,
      langflow_flow_id: langflow.flowId,
      langflow_token: langflow.token,
    })
    showSnack(t('success'), 'success')
  } catch (err: any) {
    showSnack(err.response?.data?.error || t('error'), 'error')
  } finally {
    saving.value = false
  }
}

async function testConnection() {
  if (!langflow.baseUrl || !langflow.flowId) {
    showSnack('Vui lòng nhập Base URL và Flow ID', 'warning')
    return
  }
  
  testing.value = true
  try {
    const { data } = await api.post(`/tenants/${tenantId.value}/settings/ai-engines/test`, {
      langflow_base_url: langflow.baseUrl,
      langflow_flow_id: langflow.flowId,
      langflow_token: langflow.token,
    })
    showSnack(data.message || 'Kết nối thành công', 'success')
  } catch (err: any) {
    showSnack(err.response?.data?.message || err.response?.data?.error || t('error'), 'error')
  } finally {
    testing.value = false
  }
}

function showSnack(text: string, color: string) {
  snackText.value = text
  snackColor.value = color
  snackbar.value = true
}

onMounted(loadSettings)
</script>
