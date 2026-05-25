<template>
  <div>
    <h1 class="text-h5 font-weight-bold mb-6">{{ $t('settings') }}</h1>

    <v-row>
      <v-col cols="12" md="3">
        <v-card class="pa-2">
          <v-list density="compact" nav>
            <v-list-item
              v-for="tab in tabs"
              :key="tab.value"
              :active="activeTab === tab.value"
              :prepend-icon="tab.icon"
              :title="$t(tab.label)"
              rounded="lg"
              color="primary"
              @click="activeTab = tab.value"
            />
          </v-list>
        </v-card>
      </v-col>

      <v-col cols="12" md="9">
        <!-- AI Config -->
        <v-card v-if="activeTab === 'ai'" class="pa-6">
          <div class="text-subtitle-1 font-weight-bold mb-4">
            <v-icon start size="small">mdi-robot</v-icon>
            {{ $t('ai_config') }}
          </div>

          <v-select
            v-model="aiSettings.provider"
            :label="$t('ai_provider')"
            :items="providerOptions"
            class="mb-3"
            @update:model-value="onProviderChange"
          />

          <v-select
            v-if="aiSettings.provider !== 'openai'"
            v-model="aiSettings.model"
            :label="$t('ai_model')"
            :items="modelOptions"
            item-title="title"
            item-value="value"
            class="mb-3"
          />
          <v-combobox
            v-else
            v-model="aiSettings.model"
            :label="$t('ai_model')"
            :items="modelOptions"
            item-title="title"
            item-value="value"
            :return-object="false"
            hint="Chọn model gợi ý hoặc nhập model OpenAI tùy chỉnh"
            persistent-hint
            clearable
            class="mb-3"
          />

          <v-text-field
            v-model="aiSettings.apiKey"
            :label="$t('api_key')"
            :type="showKey ? 'text' : 'password'"
            :append-inner-icon="showKey ? 'mdi-eye-off' : 'mdi-eye'"
            @click:append-inner="showKey = !showKey"
            class="mb-3"
          />

          <v-switch
            v-model="useCustomBaseUrl"
            :label="$t('custom_api_url')"
            color="primary"
            density="compact"
            hide-details
            class="mt-1 mb-2"
          />
          <div v-if="!useCustomBaseUrl" class="text-caption text-grey mb-2">{{ $t('custom_api_url_hint') }}</div>

          <v-text-field
            v-if="useCustomBaseUrl"
            v-model="aiSettings.baseUrl"
            label="Base URL"
            :placeholder="baseURLPlaceholder"
            hint="Để trống để dùng mặc định"
            persistent-hint
            clearable
            :rules="[
              v => !!v || 'Vui lòng nhập URL hoặc tắt tùy chỉnh',
              v => !v || v.startsWith('http://') || v.startsWith('https://') || 'URL phải bắt đầu bằng http:// hoặc https://',
            ]"
            class="mb-3"
          />

          <div class="d-flex ga-2">
            <v-btn color="primary" :loading="savingAI" @click="saveAI">{{ $t('save_settings') }}</v-btn>
            <v-btn variant="outlined" :loading="testingKey" @click="testKey">{{ $t('test_api_key') }}</v-btn>
          </div>
        </v-card>

        <!-- General -->
        <!-- Analysis Settings -->
        <v-card v-if="activeTab === 'analysis'" class="pa-6">
          <div class="text-subtitle-1 font-weight-bold mb-4">
            <v-icon start size="small">mdi-chart-bar</v-icon>
            Cài đặt phân tích
          </div>

          <div class="text-subtitle-2 mb-2">Chế độ Batch (tối ưu chi phí)</div>
          <v-switch
            v-model="aiSettings.batchMode"
            label="Bật chế độ Batch"
            hint="Gom nhiều cuộc chat vào 1 lần gọi AI. Tiết kiệm token nhưng có thể giảm độ chính xác."
            persistent-hint
            density="compact"
            color="primary"
            class="mb-3"
          />
          <v-select
            v-if="aiSettings.batchMode"
            v-model="aiSettings.batchSize"
            label="Số cuộc chat / batch"
            :items="[3, 5, 10, 15, 20, 30]"
            density="compact"
            class="mb-4"
            style="max-width: 200px"
          />

          <v-btn color="primary" :loading="savingAnalysis" @click="saveAnalysis">Lưu cài đặt</v-btn>
        </v-card>

        <!-- General -->
        <v-card v-if="activeTab === 'general'" class="pa-6">
          <div class="text-subtitle-1 font-weight-bold mb-4">
            <v-icon start size="small">mdi-cog</v-icon>
            {{ $t('general') }}
          </div>

          <v-text-field v-model="generalSettings.companyName" :label="$t('company_name')" class="mb-3" />
          <v-select
            v-model="generalSettings.timezone"
            :label="$t('timezone')"
            :items="['Asia/Ho_Chi_Minh', 'Asia/Bangkok', 'UTC', 'America/New_York']"
            class="mb-3"
          />
          <v-select
            v-model="generalSettings.language"
            :label="$t('language')"
            :items="[{ title: 'Tiếng Việt', value: 'vi' }, { title: 'English', value: 'en' }]"
            class="mb-3"
          />

          <v-text-field
            v-model.number="generalSettings.exchangeRate"
            :label="$t('exchange_rate_vnd')"
            type="number"
            suffix="VND = 1 USD"
            class="mb-3"
          />

          <v-text-field
            v-model="generalSettings.appUrl"
            label="URL ứng dụng"
            placeholder="https://cqa.yourdomain.com"
            hint="Cấu hình URL để hệ thống gửi link chính xác qua Telegram và Email"
            persistent-hint
            :rules="appUrlRules"
            class="mb-3"
          />

          <v-btn color="primary" :loading="savingGeneral" @click="saveGeneral">{{ $t('save_settings') }}</v-btn>
        </v-card>
      </v-col>
    </v-row>

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

const activeTab = ref('ai')
const showKey = ref(false)
const snackbar = ref(false)
const snackText = ref('')
const snackColor = ref('success')

const savingAI = ref(false)
const testingKey = ref(false)
const savingGeneral = ref(false)

const tabs = [
  { label: 'ai_config', value: 'ai', icon: 'mdi-robot' },
  { label: 'analysis_settings', value: 'analysis', icon: 'mdi-chart-bar' },
  { label: 'general', value: 'general', icon: 'mdi-cog' },
]

type AIProvider = 'claude' | 'gemini' | 'openai'

const providerOptions = [
  { title: 'Claude (Anthropic)', value: 'claude' },
  { title: 'Gemini (Google)', value: 'gemini' },
  { title: 'OpenAI (Codex)', value: 'openai' },
]

const claudeModels = [
  { title: 'Claude Sonnet 4.6 (Recommended)', value: 'claude-sonnet-4-6' },
  { title: 'Claude Haiku 4.5 (Fast & Cheap)', value: 'claude-haiku-4-5-20251001' },
  { title: 'Claude Opus 4 (Most Capable)', value: 'claude-opus-4' },
  { title: 'Claude Sonnet 4.5 (CLIProxy)', value: 'claude-sonnet-4-5-20250929' },
  { title: 'Claude Opus 4.6 (CLIProxy)', value: 'claude-opus-4-6' },
]
const geminiModels = [
  { title: 'Gemini 2.5 Flash (Fast & Cheap)', value: 'gemini-2.5-flash' },
  { title: 'Gemini 2.5 Flash Lite (Fastest)', value: 'gemini-2.5-flash-lite' },
  { title: 'Gemini 2.5 Pro (Most Capable)', value: 'gemini-2.5-pro' },
]
const openAIModels = [
  { title: 'GPT-5.4 Mini (Recommended)', value: 'gpt-5.4-mini' },
  { title: 'GPT-5.3-Codex (Coding)', value: 'gpt-5.3-codex' },
  { title: 'GPT-5.3 Chat (General)', value: 'gpt-5.3-chat-latest' },
  { title: 'GPT-5 Mini (Low Cost)', value: 'gpt-5-mini' },
  { title: 'GPT-5 Nano (Cheapest)', value: 'gpt-5-nano' },
]

const defaultModelByProvider: Record<AIProvider, string> = {
  claude: 'claude-sonnet-4-6',
  gemini: 'gemini-2.5-flash',
  openai: 'gpt-5.4-mini',
}

const providerApiKeys = reactive<Record<AIProvider, string>>({
  claude: '',
  gemini: '',
  openai: '',
})

const useCustomBaseUrl = ref(false)
const aiSettings = reactive<{ provider: AIProvider; model: string; apiKey: string; baseUrl: string; batchMode: boolean; batchSize: number }>({
  provider: 'claude',
  model: 'claude-sonnet-4-6',
  apiKey: '',
  baseUrl: '',
  batchMode: true,
  batchSize: 5,
})
const generalSettings = reactive({ companyName: '', timezone: 'Asia/Ho_Chi_Minh', language: 'vi', exchangeRate: 26000, appUrl: '' })

const appUrlRules = [
  (v: string) => !v || /^https?:\/\/.+/.test(v) || 'URL phải bắt đầu bằng http:// hoặc https://',
  (v: string) => !v || !v.endsWith('/') || 'URL không nên có dấu / ở cuối',
]

const modelOptions = computed(() => {
  switch (aiSettings.provider) {
    case 'claude':
      return claudeModels
    case 'gemini':
      return geminiModels
    case 'openai':
      return openAIModels
    default:
      return claudeModels
  }
})

function onProviderChange() {
  aiSettings.model = defaultModelByProvider[aiSettings.provider]
  aiSettings.apiKey = providerApiKeys[aiSettings.provider] || ''
}

const baseURLPlaceholder = computed(() => {
  switch (aiSettings.provider) {
    case 'claude':
      return 'https://api.anthropic.com'
    case 'gemini':
      return 'https://generativelanguage.googleapis.com'
    case 'openai':
      return 'https://api.openai.com/v1'
    default:
      return ''
  }
})

async function loadSettings() {
  try {
    const { data } = await api.get(`/tenants/${tenantId.value}/settings`)
    const settings = data.settings || {}

    providerApiKeys.claude = settings.ai_api_key_claude || ''
    providerApiKeys.gemini = settings.ai_api_key_gemini || ''
    providerApiKeys.openai = settings.ai_api_key_openai || ''

    const provider = (settings.ai_provider as AIProvider) || 'claude'
    aiSettings.provider = provider

    if (!providerApiKeys[provider] && settings.ai_api_key) {
      providerApiKeys[provider] = settings.ai_api_key
    }
    aiSettings.apiKey = providerApiKeys[provider] || ''

    if (settings.ai_model) {
      aiSettings.model = settings.ai_model
    } else {
      aiSettings.model = defaultModelByProvider[provider]
    }

    if (settings.ai_base_url) {
      aiSettings.baseUrl = settings.ai_base_url
      useCustomBaseUrl.value = true
    }
    if (settings.ai_batch_mode) aiSettings.batchMode = settings.ai_batch_mode === 'true'
    if (settings.ai_batch_size) aiSettings.batchSize = parseInt(settings.ai_batch_size) || 5
    if (settings.exchange_rate_vnd) generalSettings.exchangeRate = parseFloat(settings.exchange_rate_vnd) || 26000
    if (settings.app_url) generalSettings.appUrl = settings.app_url
    if (data.tenant) {
      generalSettings.companyName = data.tenant.name || ''
      generalSettings.timezone = data.tenant.timezone || 'Asia/Ho_Chi_Minh'
      generalSettings.language = data.tenant.language || 'vi'
    }
  } catch {
    // Settings not yet saved
  }
}

const savingAnalysis = ref(false)

async function saveAnalysis() {
  savingAnalysis.value = true
  try {
    await api.put(`/tenants/${tenantId.value}/settings/analysis`, {
      batch_mode: aiSettings.batchMode ? 'true' : 'false',
      batch_size: String(aiSettings.batchSize),
    })
    showSnack(t('success'), 'success')
  } catch (err: any) {
    showSnack(err.response?.data?.error || t('error'), 'error')
  } finally {
    savingAnalysis.value = false
  }
}

async function saveAI() {
  if (!aiSettings.apiKey) {
    showSnack('Vui lòng nhập API Key', 'error')
    return
  }
  if (!aiSettings.model) {
    showSnack('Vui lòng chọn hoặc nhập model', 'error')
    return
  }
  if (useCustomBaseUrl.value && !aiSettings.baseUrl) {
    showSnack('Vui lòng nhập Base URL hoặc tắt tùy chỉnh', 'error')
    return
  }
  savingAI.value = true
  try {
    const modelStr = typeof aiSettings.model === 'object' && aiSettings.model ? (aiSettings.model as any).value : aiSettings.model
    await api.put(`/tenants/${tenantId.value}/settings/ai`, {
      provider: aiSettings.provider,
      model: modelStr,
      api_key: aiSettings.apiKey,
      base_url: useCustomBaseUrl.value ? (aiSettings.baseUrl || '') : '',
      batch_mode: aiSettings.batchMode ? 'true' : 'false',
      batch_size: String(aiSettings.batchSize),
    })
    providerApiKeys[aiSettings.provider] = aiSettings.apiKey
    showSnack(t('success'), 'success')
  } catch (err: any) {
    showSnack(err.response?.data?.error || t('error'), 'error')
  } finally {
    savingAI.value = false
  }
}

async function testKey() {
  if (!aiSettings.apiKey) {
    showSnack('Vui lòng nhập API Key', 'error')
    return
  }
  testingKey.value = true
  try {
    const modelStr = typeof aiSettings.model === 'object' && aiSettings.model ? (aiSettings.model as any).value : aiSettings.model
    const { data } = await api.post(`/tenants/${tenantId.value}/settings/ai/test`, {
      provider: aiSettings.provider,
      api_key: aiSettings.apiKey,
      model: modelStr,
      base_url: useCustomBaseUrl.value ? (aiSettings.baseUrl || '') : '',
    })
    showSnack(`${data.provider}: ${data.message}`, 'success')
  } catch (err: any) {
    showSnack(err.response?.data?.error || t('error'), 'error')
  } finally {
    testingKey.value = false
  }
}

async function saveGeneral() {
  savingGeneral.value = true
  try {
    await api.put(`/tenants/${tenantId.value}/settings/general`, {
      company_name: generalSettings.companyName,
      timezone: generalSettings.timezone,
      language: generalSettings.language,
      exchange_rate_vnd: generalSettings.exchangeRate,
      app_url: generalSettings.appUrl,
    })
    showSnack(t('success'), 'success')
  } catch (err: any) {
    showSnack(err.response?.data?.error || t('error'), 'error')
  } finally {
    savingGeneral.value = false
  }
}

function showSnack(text: string, color: string) {
  snackText.value = text
  snackColor.value = color
  snackbar.value = true
}

onMounted(loadSettings)
</script>
