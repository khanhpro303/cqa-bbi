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
        <v-card v-if="activeTab === 'ai'" class="pa-4 pa-sm-6">
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
            v-if="aiSettings.provider !== 'openai' && aiSettings.provider !== 'openrouter'"
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
            :hint="aiSettings.provider === 'openrouter' ? $t('openrouter_free_models_hint') : 'Chọn model gợi ý hoặc nhập model OpenAI tùy chỉnh'"
            :loading="loadingOpenRouterModels"
            persistent-hint
            clearable
            class="mb-3"
            data-testid="ai-model-combobox"
          />
          <v-btn
            v-if="aiSettings.provider === 'openrouter'"
            variant="text"
            size="small"
            prepend-icon="mdi-refresh"
            class="mb-3 px-0"
            :loading="loadingOpenRouterModels"
            data-testid="openrouter-load-models"
            @click="loadOpenRouterModels(true)"
          >{{ $t('openrouter_load_models') }}</v-btn>

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

          <div class="d-flex flex-wrap ga-2">
            <v-btn color="primary" :loading="savingAI" :disabled="savingAICredentials" @click="saveAI(false)">Lưu cấu hình AI chung</v-btn>
            <v-btn variant="tonal" :loading="savingAICredentials" :disabled="savingAI" @click="saveAI(true)">Lưu API key và Base URL</v-btn>
            <v-btn variant="outlined" :loading="testingKey" @click="testKey">{{ $t('test_api_key') }}</v-btn>
          </div>

          <v-divider class="my-6" />

          <div data-testid="ai-task-model-settings">
            <div class="text-subtitle-1 font-weight-bold mb-1">Model theo tác vụ</div>
            <div class="text-body-2 text-medium-emphasis mb-4">
              Tác vụ để trống sẽ dùng cấu hình chung. Tác vụ có model riêng dùng API key và Base URL đã lưu cho nhà cung cấp được chọn.
            </div>

            <v-alert
              v-if="taskSettingsError"
              type="error"
              variant="tonal"
              class="mb-4"
              data-testid="ai-task-settings-error"
            >
              <div>{{ taskSettingsError }}</div>
              <v-btn variant="text" size="small" class="mt-1 px-0" @click="loadTaskAISettings">Tải lại</v-btn>
            </v-alert>

            <v-progress-linear v-if="loadingTaskSettings" indeterminate color="primary" class="mb-4" />

            <template v-else-if="taskAISettings.length">
              <div
                v-for="group in taskGroups"
                :key="group"
                class="mb-5"
              >
                <div class="text-subtitle-2 font-weight-bold mb-2">{{ group }}</div>
                <v-card
                  v-for="task in tasksByGroup[group]"
                  :key="task.key"
                  variant="outlined"
                  class="pa-3 mb-2"
                  :data-testid="`ai-task-${task.key}`"
                >
                  <div class="d-flex flex-wrap align-center justify-space-between ga-2 mb-3">
                    <div>
                      <div class="text-body-2 font-weight-medium">{{ task.label }}</div>
                      <div class="text-caption text-medium-emphasis">
                        {{ task.capability === 'vision' ? 'Đọc hình ảnh' : 'Văn bản' }}
                      </div>
                    </div>
                    <v-chip v-if="!task.provider" size="small" color="primary" variant="tonal">
                      Kế thừa {{ taskDefaultLabel }}
                    </v-chip>
                  </div>

                  <v-row dense>
                    <v-col cols="12" md="5">
                      <v-select
                        :model-value="task.provider"
                        label="Nhà cung cấp"
                        :items="taskProviderOptions"
                        density="compact"
                        hide-details="auto"
                        :disabled="savingTaskSettings"
                        @update:model-value="selectTaskProvider(task, $event)"
                      />
                    </v-col>
                    <v-col cols="12" md="7">
                      <v-combobox
                        :model-value="task.model"
                        label="Model"
                        :items="taskModelOptions(task)"
                        item-title="title"
                        item-value="value"
                        :return-object="false"
                        :disabled="!task.provider || savingTaskSettings"
                        :placeholder="task.provider ? 'Chọn hoặc nhập model' : taskDefaults.model"
                        :hint="taskModelHint(task)"
                        persistent-hint
                        density="compact"
                        clearable
                        @update:model-value="task.model = $event ?? ''"
                      />
                    </v-col>
                  </v-row>
                </v-card>
              </div>

              <div class="d-flex align-center flex-wrap ga-3">
                <v-btn
                  color="primary"
                  :loading="savingTaskSettings"
                  :disabled="hasInvalidTaskSettings || !taskChanges.length"
                  data-testid="save-ai-task-settings"
                  @click="saveTaskAISettings"
                >
                  Lưu model theo tác vụ
                </v-btn>
                <span v-if="hasInvalidTaskSettings" class="text-caption text-error">
                  Tác vụ chọn nhà cung cấp riêng phải có model.
                </span>
              </div>
            </template>
          </div>

          <v-divider class="my-6" />

          <div data-testid="ai-flow-model-settings">
            <div class="d-flex justify-space-between align-center mb-1">
              <div class="text-subtitle-1 font-weight-bold">Model chatbot Langflow</div>
              <v-btn
                variant="text"
                size="small"
                prepend-icon="mdi-refresh"
                :disabled="loadingFlowSettings || !!savingFlowRole"
                @click="loadFlowAISettings"
              >Tải lại</v-btn>
            </div>
            <div class="text-body-2 text-medium-emphasis mb-4">
              Đồng bộ cập nhật model trên flow đang chạy. Đổi nhà cung cấp và khóa API của agent trong Langflow.
            </div>

            <v-alert
              v-if="flowSettingsError"
              type="error"
              variant="tonal"
              class="mb-4"
              data-testid="ai-flow-settings-error"
            >
              <div>{{ flowSettingsError }}</div>
            </v-alert>

            <v-progress-linear v-if="loadingFlowSettings" indeterminate color="primary" class="mb-4" />

            <v-row v-else-if="hasFlowSettings">
              <v-col v-for="role in flowRoles" :key="role" cols="12" lg="6">
                <v-card variant="outlined" class="pa-4 h-100" :data-testid="`ai-flow-${role}`">
                  <div class="d-flex align-start justify-space-between ga-2 mb-1">
                    <div class="text-subtitle-2 font-weight-bold">{{ flowRoleLabel(role) }}</div>
                    <v-chip
                      size="small"
                      :color="flowSettings[role]?.managed ? 'success' : 'warning'"
                      variant="tonal"
                    >
                      {{ flowSettings[role]?.managed ? 'Quản lý tập trung' : 'Chỉ đọc' }}
                    </v-chip>
                  </div>
                  <div class="text-caption text-medium-emphasis mb-3 text-truncate">
                    {{ flowSettings[role]?.name || flowSettings[role]?.flow_id || 'Chưa cấu hình flow' }}
                  </div>

                  <v-alert
                    v-if="flowSettings[role]?.management_problem"
                    type="warning"
                    variant="tonal"
                    density="compact"
                    class="mb-3"
                  >
                    {{ flowSettings[role]?.management_problem }}
                  </v-alert>

                  <v-alert
                    v-if="(flowSettings[role]?.shared_roles?.length || 0) > 1"
                    type="info"
                    variant="tonal"
                    density="compact"
                    class="mb-3"
                  >
                    Flow này dùng chung cho {{ sharedFlowRoles(flowSettings[role]!) }}. Một lần đồng bộ sẽ cập nhật tất cả vai trò dùng chung.
                  </v-alert>

                  <v-autocomplete
                    v-model="flowSelections[role]"
                    label="Model trong flow"
                    :items="flowModelOptions(flowSettings[role])"
                    item-title="title"
                    return-object
                    :disabled="!flowSettings[role]?.managed || !!savingFlowRole"
                    :loading="savingFlowRole === role"
                    density="compact"
                    persistent-hint
                    :hint="flowSelectionHint(role)"
                    class="mb-3"
                  />

                  <v-btn
                    color="primary"
                    variant="tonal"
                    :loading="savingFlowRole === role"
                    :disabled="!canSaveFlow(role) || !!savingFlowRole"
                    :data-testid="`save-ai-flow-${role}`"
                    @click="saveFlowAISetting(role)"
                  >
                    Đồng bộ vào Langflow
                  </v-btn>
                </v-card>
              </v-col>
            </v-row>
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

const route = useRoute()
const { t } = useI18n()
const tenantId = computed(() => route.params.tenantId as string)

const activeTab = ref(typeof route.query.facebook_link === 'string' ? 'facebook' : 'ai')
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
  { label: 'facebook_account', value: 'facebook', icon: 'mdi-facebook' },
]

type AIProvider = 'claude' | 'gemini' | 'openai' | 'openrouter'

type TaskAISetting = {
  key: string
  label: string
  group: string
  capability: 'text' | 'vision'
  provider: AIProvider | ''
  model: string
}

type FlowRole = 'internal' | 'public'

type FlowModelOption = {
  title?: string
  name: string
  provider: string
}

type FlowAISetting = {
  role: FlowRole
  flow_id: string
  name: string
  model: string
  provider: string
  options: FlowModelOption[]
  source: string
  managed: boolean
  management_problem?: string
  shared_roles?: FlowRole[]
}

const providerOptions = [
  { title: 'Claude (Anthropic)', value: 'claude' },
  { title: 'Gemini (Google)', value: 'gemini' },
  { title: 'OpenAI (Codex)', value: 'openai' },
  { title: 'OpenRouter (nhiều hãng, có model free)', value: 'openrouter' },
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
  openrouter: 'meta-llama/llama-3.3-70b-instruct:free',
}

const providerApiKeys = reactive<Record<AIProvider, string>>({
  claude: '',
  gemini: '',
  openai: '',
  openrouter: '',
})
const providerBaseURLs = reactive<Partial<Record<AIProvider, string>>>({})

type OpenRouterModel = { id: string; name: string; context_length?: number; vision?: boolean; json_mode?: boolean }
const openRouterModels = ref<OpenRouterModel[]>([])
const loadingOpenRouterModels = ref(false)
const openRouterModelOptions = computed(() => openRouterModels.value.map(m => {
  const tags = [m.vision ? 'đọc ảnh' : '', m.json_mode ? 'JSON' : '', m.context_length ? `${Math.round(m.context_length / 1000)}k` : '']
    .filter(Boolean).join(', ')
  return { title: tags ? `${m.name} (${tags})` : m.name, value: m.id }
}))

const taskAISettings = ref<TaskAISetting[]>([])
const savedTaskModels = ref(new Map<string, { provider: AIProvider | ''; model: string }>())
const taskDefaults = reactive({ provider: '', model: '' })
const loadingTaskSettings = ref(false)
const savingTaskSettings = ref(false)
const taskSettingsError = ref('')

const taskProviderOptions = [
  { title: 'Dùng cấu hình chung', value: '' },
  ...providerOptions,
]

const taskGroups = computed(() => [...new Set(taskAISettings.value.map(task => task.group))])
const tasksByGroup = computed(() => Object.fromEntries(
  taskGroups.value.map(group => [group, taskAISettings.value.filter(task => task.group === group)]),
) as Record<string, TaskAISetting[]>)
const taskDefaultLabel = computed(() => [taskDefaults.provider, taskDefaults.model].filter(Boolean).join(' / '))
const hasInvalidTaskSettings = computed(() => taskAISettings.value.some(task => task.provider && !task.model.trim()))
const taskChanges = computed(() => taskAISettings.value.flatMap(task => {
  const model = task.provider ? task.model.trim() : ''
  const saved = savedTaskModels.value.get(task.key)
  return task.provider === saved?.provider && model === saved?.model
    ? []
    : [{ key: task.key, provider: task.provider, model }]
}))

const flowRoles: FlowRole[] = ['internal', 'public']
const flowSettings = reactive<Record<FlowRole, FlowAISetting | null>>({ internal: null, public: null })
const flowSelections = reactive<Record<FlowRole, FlowModelOption | null>>({ internal: null, public: null })
const loadingFlowSettings = ref(false)
const savingFlowRole = ref<FlowRole | ''>('')
const flowSettingsError = ref('')
const hasFlowSettings = computed(() => flowRoles.some(role => flowSettings[role]))

async function loadOpenRouterModels(force = false) {
  if (loadingOpenRouterModels.value || (openRouterModels.value.length && !force)) return
  loadingOpenRouterModels.value = true
  try {
    const { data } = await api.get(`/tenants/${tenantId.value}/settings/ai/openrouter-models`)
    openRouterModels.value = Array.isArray(data?.models) ? data.models : []
  } catch {
    showSnack(t('openrouter_load_failed'), 'error')
  } finally {
    loadingOpenRouterModels.value = false
  }
}

function taskModelOptions(task: TaskAISetting) {
  switch (task.provider) {
    case 'claude':
      return claudeModels
    case 'gemini':
      return geminiModels
    case 'openai':
      return openAIModels
    case 'openrouter':
      if (task.capability === 'vision') {
        const visionModelIDs = new Set(openRouterModels.value.filter(model => model.vision).map(model => model.id))
        return openRouterModelOptions.value.filter(model => visionModelIDs.has(model.value))
      }
      return openRouterModelOptions.value
    default:
      return []
  }
}

function selectTaskProvider(task: TaskAISetting, provider: AIProvider | '') {
  if (!provider) {
    task.provider = ''
    task.model = ''
    return
  }

  if (task.provider !== provider) {
    task.provider = provider
    task.model = defaultModelByProvider[provider]
  }
  if (provider === 'openrouter') loadOpenRouterModels()
}

function taskModelHint(task: TaskAISetting) {
  if (!task.provider) return `Đang dùng ${taskDefaultLabel.value || 'cấu hình chung'}`
  if (!providerApiKeys[task.provider]) return `Chưa lưu API key cho ${task.provider}. Hãy lưu ở cấu hình chung trước.`
  if (task.capability === 'vision') return 'Chọn model có khả năng đọc hình ảnh.'
  return 'Chọn model gợi ý hoặc nhập model tùy chỉnh.'
}

function applyTaskAISettings(data: any) {
  taskDefaults.provider = typeof data?.default_provider === 'string' ? data.default_provider : ''
  taskDefaults.model = typeof data?.default_model === 'string' ? data.default_model : ''
  taskAISettings.value = Array.isArray(data?.tasks)
    ? data.tasks.map((task: any) => ({
        key: String(task.key || ''),
        label: String(task.label || task.key || ''),
        group: String(task.group || 'Tác vụ khác'),
        capability: task.capability === 'vision' ? 'vision' : 'text',
        provider: providerOptions.some(option => option.value === task.provider) ? task.provider : '',
        model: typeof task.model === 'string' ? task.model : '',
      })).filter((task: TaskAISetting) => task.key)
    : []
  savedTaskModels.value = new Map(taskAISettings.value.map(({ key, provider, model }) => [key, { provider, model }]))
}

async function loadTaskAISettings() {
  loadingTaskSettings.value = true
  taskSettingsError.value = ''
  try {
    const { data } = await api.get(`/tenants/${tenantId.value}/settings/ai/tasks`)
    applyTaskAISettings(data)
    if (taskAISettings.value.some(task => task.provider === 'openrouter')) loadOpenRouterModels()
  } catch (err: any) {
    taskSettingsError.value = err.response?.data?.error || 'Không thể tải cấu hình model theo tác vụ. Kiểm tra kết nối rồi thử lại.'
  } finally {
    loadingTaskSettings.value = false
  }
}

async function saveTaskAISettings() {
  if (hasInvalidTaskSettings.value || !taskChanges.value.length || savingTaskSettings.value) return
  savingTaskSettings.value = true
  taskSettingsError.value = ''
  try {
    const { data } = await api.put(`/tenants/${tenantId.value}/settings/ai/tasks`, {
      tasks: taskChanges.value,
    })
    applyTaskAISettings(data)
    showSnack('Đã lưu model theo tác vụ', 'success')
  } catch (err: any) {
    taskSettingsError.value = err.response?.data?.details || err.response?.data?.error || 'Không thể lưu cấu hình model theo tác vụ. Kiểm tra API key và tên model rồi thử lại.'
  } finally {
    savingTaskSettings.value = false
  }
}

function flowRoleLabel(role: FlowRole) {
  return role === 'internal' ? 'Chatbot nội bộ' : 'Chatbot khách hàng'
}

function sharedFlowRoles(flow: FlowAISetting) {
  return (flow.shared_roles || []).map(flowRoleLabel).join(' và ')
}

function flowModelOptions(flow: FlowAISetting | null) {
  if (!flow) return []
  const options = [...(Array.isArray(flow.options) ? flow.options : [])]
  if (flow.model && !options.some(option => option.name === flow.model && option.provider === flow.provider)) {
    options.unshift({ name: flow.model, provider: flow.provider })
  }
  return options.map(option => ({
    ...option,
    title: option.provider ? `${option.name} · ${option.provider}` : option.name,
  }))
}

function flowSelectionHint(role: FlowRole) {
  const flow = flowSettings[role]
  if (!flow) return 'Chưa cấu hình flow.'
  if (!flow.managed) return flow.management_problem || 'Flow này đang dùng cấu hình dùng chung và chưa thể đồng bộ từ tenant.'
  return flow.provider ? `Hiện tại: ${flow.model} · ${flow.provider}` : `Hiện tại: ${flow.model}`
}

function canSaveFlow(role: FlowRole) {
  const flow = flowSettings[role]
  const selection = flowSelections[role]
  return !!flow?.managed && !!selection?.name && (
    selection.name !== flow.model || selection.provider !== flow.provider
  )
}

function applyFlowAISettings(data: any) {
  for (const role of flowRoles) {
    const value = data?.flows?.[role]
    if (!value || typeof value !== 'object') {
      flowSettings[role] = null
      flowSelections[role] = null
      continue
    }
    const flow: FlowAISetting = {
      role,
      flow_id: String(value.flow_id || ''),
      name: String(value.name || ''),
      model: String(value.model || ''),
      provider: String(value.provider || ''),
      options: Array.isArray(value.options) ? value.options : [],
      source: String(value.source || ''),
      managed: value.managed === true,
      management_problem: typeof value.management_problem === 'string' ? value.management_problem : undefined,
      shared_roles: Array.isArray(value.shared_roles)
        ? value.shared_roles.filter((item: unknown): item is FlowRole => item === 'internal' || item === 'public')
        : [],
    }
    flowSettings[role] = flow
    flowSelections[role] = flowModelOptions(flow).find(option => (
      option.name === flow.model && option.provider === flow.provider
    )) || null
  }
}

async function loadFlowAISettings() {
  if (loadingFlowSettings.value) return
  loadingFlowSettings.value = true
  flowSettingsError.value = ''
  try {
    const { data } = await api.get(`/tenants/${tenantId.value}/settings/ai/flows`)
    applyFlowAISettings(data)
  } catch (err: any) {
    flowSettingsError.value = err.response?.data?.message || err.response?.data?.error || 'Không thể đọc model từ Langflow. Kiểm tra URL, token và Flow ID rồi thử lại.'
  } finally {
    loadingFlowSettings.value = false
  }
}

async function saveFlowAISetting(role: FlowRole) {
  const selection = flowSelections[role]
  if (!selection || !canSaveFlow(role) || savingFlowRole.value) return
  savingFlowRole.value = role
  flowSettingsError.value = ''
  try {
    const { data } = await api.put(`/tenants/${tenantId.value}/settings/ai/flows/${role}`, {
      model: selection.name,
      provider: selection.provider,
    })
    const updatedRoles = Array.isArray(data?.updated_roles) ? data.updated_roles : [role]
    await loadFlowAISettings()
    showSnack(`Đã đồng bộ ${updatedRoles.map((item: FlowRole) => flowRoleLabel(item)).join(' và ')}`, 'success')
  } catch (err: any) {
    flowSettingsError.value = err.response?.data?.message || err.response?.data?.error || 'Không thể đồng bộ model vào Langflow. Kiểm tra model có trong danh sách của node rồi thử lại.'
  } finally {
    savingFlowRole.value = ''
  }
}

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
    case 'openrouter':
      return openRouterModelOptions.value
    default:
      return claudeModels
  }
})

function onProviderChange() {
  aiSettings.model = defaultModelByProvider[aiSettings.provider]
  aiSettings.apiKey = providerApiKeys[aiSettings.provider] || ''
  aiSettings.baseUrl = providerBaseURLs[aiSettings.provider] || ''
  useCustomBaseUrl.value = !!aiSettings.baseUrl
  if (aiSettings.provider === 'openrouter') loadOpenRouterModels()
}

const baseURLPlaceholder = computed(() => {
  switch (aiSettings.provider) {
    case 'claude':
      return 'https://api.anthropic.com'
    case 'gemini':
      return 'https://generativelanguage.googleapis.com'
    case 'openai':
      return 'https://api.openai.com/v1'
    case 'openrouter':
      return 'https://openrouter.ai/api/v1'
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
    providerApiKeys.openrouter = settings.ai_api_key_openrouter || ''

    const provider = (settings.ai_provider as AIProvider) || 'claude'
    aiSettings.provider = provider
    for (const { value } of providerOptions) {
      const scopedProvider = value as AIProvider
      providerBaseURLs[scopedProvider] = settings[`ai_base_url_${scopedProvider}`] ?? (scopedProvider === provider ? settings.ai_base_url || '' : '')
    }

    if (provider === 'openrouter') loadOpenRouterModels()
    if (!providerApiKeys[provider] && settings.ai_api_key && provider !== 'openrouter') {
      providerApiKeys[provider] = settings.ai_api_key
    }
    aiSettings.apiKey = providerApiKeys[provider] || ''

    if (settings.ai_model) {
      aiSettings.model = settings.ai_model
    } else {
      aiSettings.model = defaultModelByProvider[provider]
    }

    aiSettings.baseUrl = providerBaseURLs[provider] || ''
    useCustomBaseUrl.value = !!aiSettings.baseUrl
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

const savingAICredentials = ref(false)

async function saveAI(credentialsOnly: boolean) {
  if (!aiSettings.apiKey) {
    showSnack('Vui lòng nhập API Key', 'error')
    return
  }
  if (!credentialsOnly && !aiSettings.model) {
    showSnack('Vui lòng chọn hoặc nhập model', 'error')
    return
  }
  if (useCustomBaseUrl.value && !aiSettings.baseUrl) {
    showSnack('Vui lòng nhập Base URL hoặc tắt tùy chỉnh', 'error')
    return
  }
  const loading = credentialsOnly ? savingAICredentials : savingAI
  loading.value = true
  try {
    const { provider, apiKey } = aiSettings
    const baseURL = useCustomBaseUrl.value ? (aiSettings.baseUrl || '').trim() : ''
    const modelStr = typeof aiSettings.model === 'object' && aiSettings.model ? (aiSettings.model as any).value : aiSettings.model
    await api.put(`/tenants/${tenantId.value}/settings/ai`, {
      provider,
      api_key: apiKey,
      base_url: baseURL,
      ...(credentialsOnly ? { credentials_only: true } : {
        model: modelStr,
        batch_mode: aiSettings.batchMode ? 'true' : 'false',
        batch_size: String(aiSettings.batchSize),
      }),
    })
    providerApiKeys[provider] = apiKey
    providerBaseURLs[provider] = baseURL
    if (!credentialsOnly) {
      taskDefaults.provider = provider
      taskDefaults.model = modelStr
    }
    showSnack(t('success'), 'success')
  } catch (err: any) {
    showSnack(err.response?.data?.error || t('error'), 'error')
  } finally {
    loading.value = false
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

onMounted(() => {
  loadSettings()
  loadTaskAISettings()
  loadFlowAISettings()
})
</script>
