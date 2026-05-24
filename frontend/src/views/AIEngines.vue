<template>
  <div>
    <h1 class="text-h5 font-weight-bold mb-6">{{ $t('ai_engines') }}</h1>
    <div class="text-grey-darken-1 mb-6">{{ $t('ai_engines_desc') }}</div>

    <!-- Langflow Config -->
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

      <v-row>
        <v-col cols="12" md="6">
          <v-text-field
            v-model="langflow.flowId"
            :label="$t('flow_id')"
            placeholder="e.g. 12345678-1234-1234-1234-123456789012"
            class="mb-3"
          />
        </v-col>
        <v-col cols="12" md="6">
          <v-text-field
            v-model="langflow.publicFlowId"
            label="Flow ID (Public / General Users)"
            placeholder="e.g. 87654321-4321-4321-4321-210987654321"
            class="mb-3"
          />
        </v-col>
      </v-row>

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

    <!-- ERP Config -->
    <v-card class="pa-6 mb-6">
      <div class="text-subtitle-1 font-weight-bold mb-4 d-flex align-center">
        <v-icon start size="small" color="primary" class="mr-2">mdi-database-sync</v-icon>
        {{ $t('erp_config_title') }}
      </div>

      <!-- Part 1: ERP Cloudify Connection (Shared) -->
      <div class="text-subtitle-2 mb-3 font-weight-bold" :class="isDark ? 'text-grey-lighten-1' : 'text-grey-darken-3'">{{ $t('erp_part1_title') }}</div>
      
      <v-row class="mb-4">
        <v-col cols="12" md="6">
          <v-text-field
            v-model="erp.url"
            :label="$t('erp_url_label')"
            placeholder="https://bbiapi.cloudify.vn"
            class="mb-3"
            clearable
            density="compact"
          />
          <v-text-field
            v-model="erp.username"
            :label="$t('erp_username_label')"
            placeholder="bien.la@cloudify.vn"
            class="mb-3"
            clearable
            density="compact"
          />
        </v-col>
        <v-col cols="12" md="6">
          <v-text-field
            v-model="erp.dbName"
            :label="$t('erp_db_label')"
            :hint="$t('erp_db_hint')"
            persistent-hint
            placeholder="demobienla"
            class="mb-3"
            clearable
            density="compact"
          />
          <v-text-field
            v-model="erp.password"
            :label="$t('erp_password_label')"
            type="password"
            placeholder="••••••••"
            class="mb-3"
            clearable
            density="compact"
          />
        </v-col>
      </v-row>

      <div class="d-flex ga-2 mb-6">
        <v-btn variant="outlined" size="small" :loading="testingERP" @click="testERPConnection">
          {{ $t('erp_connection_test') }}
        </v-btn>
      </div>

      <v-divider class="mb-6"></v-divider>

      <!-- Action Save Settings -->
      <div class="d-flex ga-2">
        <v-btn color="primary" :loading="savingERP" @click="saveERP">{{ $t('save') }}</v-btn>
      </div>
    </v-card>

    <!-- Integration info card -->
    <v-card class="pa-6 mb-6">
      <div class="text-subtitle-1 font-weight-bold mb-4 d-flex align-center">
        <v-icon start size="small" color="primary" class="mr-2">mdi-information-outline</v-icon>
        {{ $t('erp_agent_credentials') }}
      </div>
      
      <v-alert color="info" variant="tonal" class="mb-4">
        <div class="text-caption" :class="isDark ? 'text-grey-lighten-1' : 'text-grey-darken-2'">
          {{ $t('erp_agent_token_hint') }}
        </div>
      </v-alert>

      <v-row>
        <v-col cols="12" md="4">
          <v-text-field
            :model-value="gatewayUrl"
            label="CQA Secure Gateway Endpoint"
            readonly
            append-inner-icon="mdi-content-copy"
            @click:append-inner="copyToClipboard(gatewayUrl, 'Đã copy URL Endpoint')"
            density="compact"
            variant="outlined"
            class="mb-3"
            :bg-color="isDark ? '#2a2a2a' : 'white'"
          />
        </v-col>
        <v-col cols="12" md="4">
          <v-text-field
            :model-value="erp.publicAgentToken"
            label="Agent Secure Token (Public Bot)"
            readonly
            append-inner-icon="mdi-content-copy"
            @click:append-inner="copyToClipboard(erp.publicAgentToken, 'Đã copy Public Agent Token')"
            density="compact"
            variant="outlined"
            class="mb-3"
            :bg-color="isDark ? '#2a2a2a' : 'white'"
          />
        </v-col>
        <v-col cols="12" md="4">
          <v-text-field
            :model-value="erp.privateAgentToken"
            label="Agent Secure Token (Private/Whitelist Bot)"
            readonly
            append-inner-icon="mdi-content-copy"
            @click:append-inner="copyToClipboard(erp.privateAgentToken, 'Đã copy Private Agent Token')"
            density="compact"
            variant="outlined"
            class="mb-3"
            :bg-color="isDark ? '#2a2a2a' : 'white'"
          />
        </v-col>
      </v-row>
    </v-card>

    <v-snackbar v-model="snackbar" :color="snackColor" timeout="3000">{{ snackText }}</v-snackbar>
  </div>
</template>

<script setup lang="ts">
import { ref, reactive, computed, onMounted } from 'vue'
import { useRoute } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { useTheme } from 'vuetify'
import api from '../api'

const route = useRoute()
const { t } = useI18n()
const tenantId = computed(() => route.params.tenantId as string)

const theme = useTheme()
const isDark = computed(() => theme.global.current.value.dark)

const showKey = ref(false)
const snackbar = ref(false)
const snackText = ref('')
const snackColor = ref('success')

const saving = ref(false)
const testing = ref(false)

const savingERP = ref(false)
const testingERP = ref(false)

const langflow = reactive({
  baseUrl: '',
  flowId: '',
  publicFlowId: '',
  token: '',
})

const erp = reactive({
  url: '',
  dbName: '',
  username: '',
  password: '',
  publicAgentToken: '',
  privateAgentToken: '',
})

const gatewayUrl = computed(() => {
  return `${window.location.origin}/api/v1/tenants/${tenantId.value}/erp/query`
})

function copyToClipboard(text: string, msg: string) {
  navigator.clipboard.writeText(text)
  showSnack(msg, 'success')
}

async function loadSettings() {
  try {
    const { data } = await api.get(`/tenants/${tenantId.value}/settings`)
    const settings = data.settings || {}

    langflow.baseUrl = settings.ai_engine_langflow_url || ''
    langflow.flowId = settings.ai_engine_langflow_flow_id || ''
    langflow.publicFlowId = settings.ai_engine_langflow_public_flow_id || ''
    
    if (settings.ai_engine_langflow_token) {
      langflow.token = settings.ai_engine_langflow_token
    }

    erp.url = settings.erp_api_url || ''
    erp.dbName = settings.erp_api_db || ''
    erp.username = settings.erp_api_username || ''

    if (settings.erp_api_password) {
      erp.password = settings.erp_api_password
    }

    erp.publicAgentToken = settings.ai_agent_erp_token_public || ''
    erp.privateAgentToken = settings.ai_agent_erp_token_private || ''
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
      langflow_public_flow_id: langflow.publicFlowId,
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

async function saveERP() {
  savingERP.value = true
  try {
    await api.put(`/tenants/${tenantId.value}/settings/erp`, {
      url: erp.url,
      db: erp.dbName,
      username: erp.username,
      password: erp.password,
      // Public bot settings defaulted to false as they are managed per crm group
      public_active: 'false',
      public_scopes: '',
      public_product_groups: '',
      // Private bot settings defaulted to true for staff integration
      private_active: 'true',
      private_scopes: '',
      private_product_groups: '',
    })
    showSnack(t('success'), 'success')
  } catch (err: any) {
    showSnack(err.response?.data?.error || t('error'), 'error')
  } finally {
    savingERP.value = false
  }
}

async function testERPConnection() {
  if (!erp.url) {
    showSnack('Vui lòng nhập URL của Cloudify API', 'warning')
    return
  }
  if (!erp.username) {
    showSnack('Vui lòng nhập tài khoản Cloudify', 'warning')
    return
  }

  testingERP.value = true
  try {
    const { data } = await api.post(`/tenants/${tenantId.value}/settings/erp/test`, {
      url: erp.url,
      db: erp.dbName,
      username: erp.username,
      password: erp.password,
    })
    showSnack(data.message || 'Kết nối thành công', 'success')
  } catch (err: any) {
    showSnack(err.response?.data?.message || err.response?.data?.error || t('error'), 'error')
  } finally {
    testingERP.value = false
  }
}

function showSnack(text: string, color: string) {
  snackText.value = text
  snackColor.value = color
  snackbar.value = true
}

onMounted(loadSettings)
</script>

<style scoped>
/* Scoped styles are kept minimal here */
</style>
