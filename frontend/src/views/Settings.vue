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
        <!-- AI Config: provider keys and model lists only. Picking models
             happens in Agents AI > Model tác vụ. -->
        <v-card v-if="activeTab === 'ai'" class="pa-4 pa-sm-6" data-testid="ai-provider-settings">
          <div class="text-subtitle-1 font-weight-bold mb-1">
            <v-icon start size="small">mdi-robot</v-icon>
            {{ $t('ai_config') }}
          </div>
          <div class="text-body-2 text-medium-emphasis mb-4">
            {{ $t('ai_keys_intro') }}
            <router-link :to="{ path: `/${tenantId}/ai-agents`, query: { tab: 'models' } }" data-testid="ai-task-models-link">{{ $t('ai_keys_models_link') }}</router-link>.
          </div>

          <v-expansion-panels v-model="openProviders" multiple variant="accordion" class="mb-6">
            <v-expansion-panel
              v-for="option in providerOptions"
              :key="option.value"
              :value="option.value"
              :data-testid="`ai-provider-${option.value}`"
            >
              <v-expansion-panel-title>
                <div class="d-flex align-center flex-wrap ga-2">
                  <span class="font-weight-medium">{{ option.title }}</span>
                  <v-chip size="x-small" :color="creds.apiKeys[option.value] ? 'success' : 'grey'" variant="tonal">
                    {{ creds.apiKeys[option.value] ? $t(option.value === '9router' ? 'ai_token_saved' : 'ai_key_saved') : $t('ai_key_missing') }}
                  </v-chip>
                  <v-chip v-if="creds.defaultProvider === option.value" size="x-small" color="primary" variant="tonal">{{ $t('ai_default_provider') }}</v-chip>
                </div>
              </v-expansion-panel-title>
              <v-expansion-panel-text>
                <v-text-field
                  v-model="drafts[option.value].apiKey"
                  :label="$t(option.value === '9router' ? 'nine_router_api_key' : 'api_key')"
                  :type="drafts[option.value].showKey ? 'text' : 'password'"
                  :append-inner-icon="drafts[option.value].showKey ? 'mdi-eye-off' : 'mdi-eye'"
                  autocomplete="off"
                  class="mb-2"
                  :data-testid="`ai-key-${option.value}`"
                  @click:append-inner="drafts[option.value].showKey = !drafts[option.value].showKey"
                />

                <template v-if="option.value !== '9router'">
                  <v-switch
                    v-model="drafts[option.value].customUrl"
                    :label="$t('custom_api_url')"
                    color="primary"
                    density="compact"
                    hide-details
                    class="mb-2"
                  />
                  <div v-if="!drafts[option.value].customUrl" class="text-caption text-grey mb-2">{{ $t('custom_api_url_hint') }}</div>
                </template>
                <v-text-field
                  v-if="option.value === '9router' || drafts[option.value].customUrl"
                  v-model="drafts[option.value].baseUrl"
                  label="Base URL"
                  :placeholder="baseURLPlaceholders[option.value]"
                  :hint="option.value === '9router' ? $t('nine_router_url_hint') : ''"
                  persistent-hint
                  clearable
                  :rules="baseURLRules(option.value)"
                  class="mb-3"
                  :data-testid="`ai-base-url-${option.value}`"
                />

                <div class="d-flex flex-wrap ga-2 mt-1">
                  <v-btn
                    color="primary"
                    variant="tonal"
                    :loading="drafts[option.value].saving"
                    :disabled="drafts[option.value].testing"
                    :data-testid="`ai-save-${option.value}`"
                    @click="saveProvider(option.value)"
                  >{{ $t(option.value === '9router' ? 'nine_router_save_connection' : 'save_ai_provider_credentials') }}</v-btn>
                  <v-btn
                    variant="outlined"
                    :loading="drafts[option.value].testing"
                    :disabled="drafts[option.value].saving"
                    :data-testid="`ai-test-${option.value}`"
                    @click="testProvider(option.value)"
                  >{{ $t(option.value === '9router' ? 'ai_test_token' : 'test_api_key') }}</v-btn>
                </div>
              </v-expansion-panel-text>
            </v-expansion-panel>
          </v-expansion-panels>

          <div data-testid="ai-model-catalogs">
            <div class="text-subtitle-1 font-weight-bold mb-1">{{ $t('ai_catalogs_title') }}</div>
            <div class="text-body-2 text-medium-emphasis mb-3">{{ $t('ai_catalogs_desc') }}</div>
            <v-table density="compact" class="mb-3 catalog-table">
              <tbody>
                <tr data-testid="ai-catalog-9router">
                  <td class="font-weight-medium">9router</td>
                  <td class="text-medium-emphasis">{{ nineRouterCatalogMessage }}</td>
                </tr>
                <tr data-testid="ai-catalog-openrouter">
                  <td class="font-weight-medium">OpenRouter (free)</td>
                  <td class="text-medium-emphasis">{{ openRouterCatalogMessage }}</td>
                </tr>
                <tr>
                  <td class="font-weight-medium">Claude, Gemini, OpenAI</td>
                  <td class="text-medium-emphasis">{{ $t('ai_catalog_builtin') }}</td>
                </tr>
              </tbody>
            </v-table>
            <v-btn
              variant="tonal"
              prepend-icon="mdi-refresh"
              :loading="refreshingCatalogs"
              data-testid="refresh-model-catalogs"
              @click="refreshCatalogs"
            >{{ $t('ai_catalogs_refresh') }}</v-btn>
          </div>
        </v-card>

        <!-- General -->
        <!-- Analysis Settings -->
        <v-card v-if="activeTab === 'analysis'" class="pa-4 pa-sm-6">
          <div class="text-subtitle-1 font-weight-bold mb-4">
            <v-icon start size="small">mdi-chart-bar</v-icon>
            Cài đặt phân tích
          </div>

          <div class="text-subtitle-2 mb-2">Chế độ Batch (tối ưu chi phí)</div>
          <v-switch
            v-model="analysisSettings.batchMode"
            label="Bật chế độ Batch"
            hint="Gom nhiều cuộc chat vào 1 lần gọi AI. Tiết kiệm token nhưng có thể giảm độ chính xác."
            persistent-hint
            density="compact"
            color="primary"
            class="mb-3"
          />
          <v-select
            v-if="analysisSettings.batchMode"
            v-model="analysisSettings.batchSize"
            label="Số cuộc chat / batch"
            :items="[3, 5, 10, 15, 20, 30]"
            density="compact"
            class="mb-4"
            style="max-width: 200px"
          />

          <v-btn color="primary" :loading="savingAnalysis" @click="saveAnalysis">Lưu cài đặt</v-btn>
        </v-card>

        <!-- General -->
        <v-card v-if="activeTab === 'general'" class="pa-4 pa-sm-6">
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

        <v-card v-if="activeTab === 'facebook'" class="pa-4 pa-sm-6">
          <FacebookAccountManager :active="activeTab === 'facebook'" show-unavailable />
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
import FacebookAccountManager from '../components/FacebookAccountManager.vue'
import {
  type AIProvider,
  clearNineRouterModels,
  defaultModelByProvider,
  loadNineRouterModels,
  loadOpenRouterModels,
  parseProviderCredentials,
  providerOptions,
  useAIModelCatalog,
} from '../composables/aiModelCatalog'

const route = useRoute()
const { t } = useI18n()
const tenantId = computed(() => route.params.tenantId as string)

const activeTab = ref(typeof route.query.facebook_link === 'string' ? 'facebook' : 'ai')
const snackbar = ref(false)
const snackText = ref('')
const snackColor = ref('success')

const savingGeneral = ref(false)

const tabs = [
  { label: 'ai_config', value: 'ai', icon: 'mdi-robot' },
  { label: 'analysis_settings', value: 'analysis', icon: 'mdi-chart-bar' },
  { label: 'general', value: 'general', icon: 'mdi-cog' },
  { label: 'facebook_account', value: 'facebook', icon: 'mdi-facebook' },
]

// ---- provider keys ----
type ProviderDraft = { apiKey: string; baseUrl: string; customUrl: boolean; showKey: boolean; saving: boolean; testing: boolean }

const catalog = useAIModelCatalog()
const creds = reactive(parseProviderCredentials({}))
const drafts = reactive(Object.fromEntries(providerOptions.map(({ value }) => [value, {
  apiKey: '', baseUrl: '', customUrl: false, showKey: false, saving: false, testing: false,
}])) as Record<AIProvider, ProviderDraft>)
const openProviders = ref<AIProvider[]>([])

const baseURLPlaceholders: Record<AIProvider, string> = {
  claude: 'https://api.anthropic.com',
  gemini: 'https://generativelanguage.googleapis.com',
  openai: 'https://api.openai.com/v1',
  openrouter: 'https://openrouter.ai/api/v1',
  '9router': 'https://router.example.com/v1',
}

function baseURLRules(provider: AIProvider) {
  const nineRouter = provider === '9router'
  return [
    (v: string) => !!v || (nineRouter ? 'Vui lòng nhập URL 9router' : 'Vui lòng nhập URL hoặc tắt tùy chỉnh'),
    (v: string) => !v || v.startsWith('http://') || v.startsWith('https://') || 'URL phải bắt đầu bằng http:// hoặc https://',
    (v: string) => !nineRouter || !v || /\/v1\/*$/.test(v.trim()) || 'URL 9router phải kết thúc bằng /v1',
  ]
}

function draftBaseURL(provider: AIProvider) {
  const draft = drafts[provider]
  return provider === '9router' || draft.customUrl ? (draft.baseUrl || '').trim() : ''
}

function resetDraft(provider: AIProvider) {
  const draft = drafts[provider]
  draft.apiKey = creds.apiKeys[provider] || ''
  draft.baseUrl = creds.baseURLs[provider] || ''
  draft.customUrl = !!draft.baseUrl
}

const sameURL = (a: string, b: string) => a.trim().replace(/\/+$/, '') === b.trim().replace(/\/+$/, '')
const hasSavedNineRouter = computed(() => !!creds.apiKeys['9router'] && !!creds.baseURLs['9router'])
const nineRouterDraftMatchesSaved = computed(() => hasSavedNineRouter.value &&
  drafts['9router'].apiKey === creds.apiKeys['9router'] &&
  sameURL(drafts['9router'].baseUrl || '', creds.baseURLs['9router'] || ''))

function failureText(err: any) {
  const failure = err.response?.data
  return ['invalid_base_url', '9router_endpoint_changed', 'invalid_ai_configuration', 'api_test_failed'].includes(failure?.error) && failure?.details
    ? failure.details
    : failure?.error || t('error')
}

async function saveProvider(provider: AIProvider) {
  const draft = drafts[provider]
  if (!draft.apiKey) {
    showSnack('Vui lòng nhập API Key', 'error')
    return
  }
  const baseURL = draftBaseURL(provider)
  if ((provider === '9router' || draft.customUrl) && !baseURL) {
    showSnack('Vui lòng nhập Base URL', 'error')
    return
  }
  draft.saving = true
  try {
    // The first key saved when the default provider has none becomes the
    // default, so a new tenant works without a trip to Model tác vụ.
    const becomeDefault = provider !== creds.defaultProvider && !creds.apiKeys[creds.defaultProvider] && provider !== '9router'
    await api.put(`/tenants/${tenantId.value}/settings/ai`, {
      provider,
      api_key: draft.apiKey,
      base_url: baseURL,
      ...(becomeDefault ? { model: defaultModelByProvider[provider] } : { credentials_only: true }),
    })
    await loadSettings()
    if (becomeDefault) {
      showSnack(t('ai_became_default'), 'success')
      return
    }
    if (provider === '9router') {
      clearNineRouterModels()
      loadNineRouterModels(tenantId.value, true)
    }
    showSnack(t('success'), 'success')
  } catch (err: any) {
    showSnack(failureText(err), 'error')
  } finally {
    draft.saving = false
  }
}

// "Kiểm tra key": a one-line call to the provider. For 9router the check is
// listing the routes of the saved connection, which needs no model ID.
async function testProvider(provider: AIProvider) {
  const draft = drafts[provider]
  if (!draft.apiKey) {
    showSnack('Vui lòng nhập API Key', 'error')
    return
  }
  draft.testing = true
  try {
    if (provider === '9router') {
      if (!nineRouterDraftMatchesSaved.value) {
        showSnack(t('ai_test_token_save_first'), 'error')
        return
      }
      await loadNineRouterModels(tenantId.value, true)
      if (catalog.nineRouterStatus === 'loaded') {
        showSnack(t('ai_test_token_ok', { count: catalog.nineRouter.length }), 'success')
      } else {
        showSnack(t('nine_router_models_failed'), 'error')
      }
      return
    }
    const baseURL = draftBaseURL(provider)
    if (draft.customUrl && !baseURL) {
      showSnack('Vui lòng nhập Base URL', 'error')
      return
    }
    // The default provider is tested with the saved default model.
    const { data } = await api.post(`/tenants/${tenantId.value}/settings/ai/test`, {
      provider,
      api_key: draft.apiKey,
      base_url: baseURL,
      model: provider === creds.defaultProvider ? '' : defaultModelByProvider[provider],
    })
    showSnack(`${data.provider}: ${data.message}`, 'success')
  } catch (err: any) {
    showSnack(failureText(err), 'error')
  } finally {
    draft.testing = false
  }
}

// ---- model lists that feed the Model tác vụ dropdowns ----
const refreshingCatalogs = ref(false)

const nineRouterCatalogMessage = computed(() => {
  if (!hasSavedNineRouter.value) return t('ai_catalog_nine_router_not_saved')
  switch (catalog.nineRouterStatus) {
    case 'loading':
      return t('ai_catalog_loading')
    case 'error':
      return t('nine_router_models_failed')
    case 'loaded':
      return catalog.nineRouter.length ? t('nine_router_models_count', { count: catalog.nineRouter.length }) : t('nine_router_models_empty')
    default:
      return t('ai_catalog_not_loaded')
  }
})

const openRouterCatalogMessage = computed(() => {
  switch (catalog.openRouterStatus) {
    case 'loading':
      return t('ai_catalog_loading')
    case 'error':
      return t('openrouter_load_failed')
    case 'loaded':
      return t('ai_catalog_openrouter_count', { count: catalog.openRouter.length })
    default:
      return t('ai_catalog_not_loaded')
  }
})

async function refreshCatalogs() {
  refreshingCatalogs.value = true
  try {
    await Promise.all([
      loadOpenRouterModels(tenantId.value, true),
      hasSavedNineRouter.value ? loadNineRouterModels(tenantId.value, true) : Promise.resolve(),
    ])
    const failed = catalog.openRouterStatus === 'error' || (hasSavedNineRouter.value && catalog.nineRouterStatus === 'error')
    showSnack(failed ? t('ai_catalogs_refresh_partial') : t('ai_catalogs_refreshed'), failed ? 'warning' : 'success')
  } finally {
    refreshingCatalogs.value = false
  }
}

// ---- analysis + general ----
const analysisSettings = reactive({ batchMode: true, batchSize: 5 })
const generalSettings = reactive({ companyName: '', timezone: 'Asia/Ho_Chi_Minh', language: 'vi', exchangeRate: 26000, appUrl: '' })

const appUrlRules = [
  (v: string) => !v || /^https?:\/\/.+/.test(v) || 'URL phải bắt đầu bằng http:// hoặc https://',
  (v: string) => !v || !v.endsWith('/') || 'URL không nên có dấu / ở cuối',
]

async function loadSettings() {
  try {
    const { data } = await api.get(`/tenants/${tenantId.value}/settings`)
    const settings = data.settings || {}
    Object.assign(creds, parseProviderCredentials(settings))
    for (const { value } of providerOptions) resetDraft(value)

    if (settings.ai_batch_mode) analysisSettings.batchMode = settings.ai_batch_mode === 'true'
    if (settings.ai_batch_size) analysisSettings.batchSize = parseInt(settings.ai_batch_size) || 5
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
      batch_mode: analysisSettings.batchMode ? 'true' : 'false',
      batch_size: String(analysisSettings.batchSize),
    })
    showSnack(t('success'), 'success')
  } catch (err: any) {
    showSnack(err.response?.data?.error || t('error'), 'error')
  } finally {
    savingAnalysis.value = false
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

onMounted(async () => {
  await loadSettings()
  // Open the default provider so its key is one click away.
  openProviders.value = [creds.defaultProvider]
  loadOpenRouterModels(tenantId.value)
  if (hasSavedNineRouter.value) loadNineRouterModels(tenantId.value)
})

defineExpose({ creds, drafts, saveProvider, testProvider, refreshCatalogs, analysisSettings })
</script>

<style scoped>
.catalog-table :deep(td:first-child) {
  width: 200px;
}
</style>
