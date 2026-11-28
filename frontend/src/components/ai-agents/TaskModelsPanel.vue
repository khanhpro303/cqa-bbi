<template>
  <div data-testid="panel-models">
    <div class="d-flex align-start flex-wrap ga-3 mb-4">
      <div class="text-body-2 text-medium-emphasis flex-grow-1" style="min-width: 260px">
        {{ $t('tm_intro') }}
        <router-link :to="`/${tenantId}/settings`" data-testid="tm-keys-link">{{ $t('tm_keys_link') }}</router-link>.
      </div>
      <v-btn variant="text" size="small" prepend-icon="mdi-refresh" :loading="loadingAny" data-testid="tm-reload" @click="loadAll">{{ $t('tm_reload') }}</v-btn>
    </div>

    <!-- Default model -->
    <v-card class="pa-4 pa-sm-5 mb-5" data-testid="tm-default">
      <div class="text-subtitle-1 font-weight-bold mb-1">{{ $t('tm_default_title') }}</div>
      <div class="text-body-2 text-medium-emphasis mb-4">{{ $t('tm_default_desc') }}</div>
      <v-row dense>
        <v-col cols="12" md="5">
          <v-select
            :model-value="defaultDraft.provider"
            :label="$t('tm_provider')"
            :items="defaultProviderItems"
            density="compact"
            hide-details="auto"
            :disabled="!canEdit || savingDefault"
            data-testid="tm-default-provider"
            @update:model-value="selectDefaultProvider($event)"
          />
        </v-col>
        <v-col cols="12" md="7">
          <v-combobox
            :model-value="defaultDraft.model"
            :label="$t('tm_model')"
            :items="modelItems(defaultDraft.provider, false)"
            item-title="title"
            item-value="value"
            :return-object="false"
            :loading="isCatalogLoading(defaultDraft.provider)"
            :disabled="!canEdit || savingDefault"
            :hint="providerHint(defaultDraft.provider, false)"
            persistent-hint
            density="compact"
            clearable
            data-testid="tm-default-model"
            @update:model-value="defaultDraft.model = $event ?? ''"
          />
        </v-col>
      </v-row>
      <div v-if="canEdit" class="d-flex align-center flex-wrap ga-3 mt-3">
        <v-btn
          color="primary"
          variant="tonal"
          :loading="savingDefault"
          :disabled="!defaultChanged || !canSaveDefault"
          data-testid="tm-default-save"
          @click="saveDefault"
        >{{ $t('tm_default_save') }}</v-btn>
        <span v-if="defaultDraft.provider && !creds.apiKeys[defaultDraft.provider]" class="text-caption text-error">
          {{ $t('tm_no_key', { provider: providerName(defaultDraft.provider) }) }}
        </span>
      </div>
    </v-card>

    <v-alert
      v-if="taskSettingsError"
      type="error"
      variant="tonal"
      class="mb-4"
      data-testid="ai-task-settings-error"
    >
      <div>{{ taskSettingsError }}</div>
      <v-btn variant="text" size="small" class="mt-1 px-0" @click="loadTaskAISettings">{{ $t('tm_reload') }}</v-btn>
    </v-alert>

    <v-progress-linear v-if="loadingTaskSettings" indeterminate color="primary" class="mb-4" />

    <div data-testid="ai-task-model-settings">
      <!-- Around the chatbot -->
      <div class="text-h6 font-weight-bold mb-1" data-testid="tm-section-chatbot">{{ $t('tm_section_chatbot') }}</div>
      <div class="text-body-2 text-medium-emphasis mb-3">{{ $t('tm_section_chatbot_desc') }}</div>

      <v-card class="pa-4 pa-sm-5 mb-4" data-testid="ai-flow-model-settings">
        <div class="d-flex justify-space-between align-center flex-wrap ga-2 mb-1">
          <div class="text-subtitle-1 font-weight-bold">{{ $t('tm_flows_title') }}</div>
        </div>
        <div class="text-body-2 text-medium-emphasis mb-4">{{ $t('tm_flows_desc') }}</div>

        <v-alert v-if="flowSettingsError" type="error" variant="tonal" class="mb-4" data-testid="ai-flow-settings-error">
          {{ flowSettingsError }}
        </v-alert>
        <v-progress-linear v-if="loadingFlowSettings" indeterminate color="primary" class="mb-4" />

        <v-row v-else-if="hasFlowSettings">
          <v-col v-for="role in flowRoles" :key="role" cols="12" lg="6">
            <v-card variant="outlined" class="pa-4 h-100" :data-testid="`ai-flow-${role}`">
              <div class="d-flex align-start justify-space-between ga-2 mb-1">
                <div class="text-subtitle-2 font-weight-bold">{{ flowRoleLabel(role) }}</div>
                <v-chip size="small" :color="flowSettings[role]?.managed ? 'success' : 'warning'" variant="tonal">
                  {{ flowSettings[role]?.managed ? $t('tm_flow_managed') : $t('tm_flow_readonly') }}
                </v-chip>
              </div>
              <div class="text-caption text-medium-emphasis mb-3 text-truncate">
                {{ flowSettings[role]?.name || flowSettings[role]?.flow_id || $t('tm_flow_missing') }}
              </div>
              <v-alert v-if="flowSettings[role]?.management_problem" type="warning" variant="tonal" density="compact" class="mb-3">
                {{ flowSettings[role]?.management_problem }}
              </v-alert>
              <v-alert v-if="(flowSettings[role]?.shared_roles?.length || 0) > 1" type="info" variant="tonal" density="compact" class="mb-3">
                {{ $t('tm_flow_shared', { roles: sharedFlowRoles(flowSettings[role]!) }) }}
              </v-alert>
              <v-autocomplete
                v-model="flowSelections[role]"
                :label="$t('tm_flow_model')"
                :items="flowModelOptions(flowSettings[role])"
                item-title="title"
                return-object
                :disabled="!canEdit || !flowSettings[role]?.managed || !!savingFlowRole"
                :loading="savingFlowRole === role"
                density="compact"
                persistent-hint
                :hint="flowSelectionHint(role)"
                class="mb-3"
              />
              <v-btn
                v-if="canEdit"
                color="primary"
                variant="tonal"
                :loading="savingFlowRole === role"
                :disabled="!canSaveFlow(role) || !!savingFlowRole"
                :data-testid="`save-ai-flow-${role}`"
                @click="saveFlowAISetting(role)"
              >{{ $t('tm_flow_sync') }}</v-btn>
            </v-card>
          </v-col>
        </v-row>
        <div v-else-if="!flowSettingsError" class="text-body-2 text-medium-emphasis">{{ $t('tm_flow_missing') }}</div>
      </v-card>

      <v-card v-if="agents.length" class="pa-4 pa-sm-5 mb-4" data-testid="tm-agents">
        <div class="text-subtitle-1 font-weight-bold mb-1">{{ $t('tm_agents_title') }}</div>
        <div class="text-body-2 text-medium-emphasis mb-3">{{ $t('tm_agents_desc') }}</div>
        <div style="overflow-x: auto; -webkit-overflow-scrolling: touch;">
          <v-table density="compact" hover style="min-width: 560px;">
            <thead>
              <tr>
                <th>{{ $t('ag_col_agent') }}</th>
                <th>{{ $t('ag_col_runtime') }}</th>
                <th>{{ $t('tm_agents_col_model') }}</th>
              </tr>
            </thead>
            <tbody>
              <tr v-for="a in agents" :key="a.id" :data-testid="`tm-agent-${a.key}`">
                <td class="py-2">
                  <v-btn
                    variant="text"
                    size="small"
                    class="px-0 text-none"
                    :data-testid="`tm-agent-open-${a.key}`"
                    @click="emit('open-agent', a.id)"
                  >{{ a.name }}</v-btn>
                  <div v-if="!a.is_active" class="text-caption text-medium-emphasis">{{ $t('ag_inactive') }}</div>
                </td>
                <td class="text-no-wrap">{{ a.runtime === 'native' ? $t('ag_runtime_native') : 'Langflow' }}</td>
                <td>
                  <template v-if="a.runtime === 'native'">
                    <span class="text-body-2">{{ [a.provider, a.model].filter(Boolean).join(' / ') || '—' }}</span>
                  </template>
                  <template v-else>
                    <div class="text-body-2">{{ agentFlowLabel(a) }}</div>
                    <div class="text-caption text-medium-emphasis">{{ $t('tm_agents_langflow_note') }}</div>
                  </template>
                </td>
              </tr>
            </tbody>
          </v-table>
        </div>
      </v-card>

      <template v-for="section in taskSections" :key="section.id">
        <template v-if="section.id === 'other'">
          <div class="text-h6 font-weight-bold mb-1 mt-6" data-testid="tm-section-other">{{ $t('tm_section_other') }}</div>
          <div class="text-body-2 text-medium-emphasis mb-3">{{ $t('tm_section_other_desc') }}</div>
        </template>
        <v-card
          v-for="group in section.groups"
          :key="group.name"
          class="pa-4 pa-sm-5 mb-4"
          :data-testid="`tm-group-${group.name}`"
        >
          <div class="text-subtitle-1 font-weight-bold mb-3">{{ section.id === 'chatbot' ? $t('tm_chatbot_tasks') : group.name }}</div>
          <div
            v-for="(task, index) in group.tasks"
            :key="task.key"
            :data-testid="`ai-task-${task.key}`"
          >
            <v-divider v-if="index > 0" class="my-3" />
            <v-row dense align="center">
              <v-col cols="12" md="4">
                <div class="text-body-2 font-weight-medium">{{ task.label }}</div>
                <div class="d-flex align-center flex-wrap ga-1 mt-1">
                  <span class="text-caption text-medium-emphasis">{{ task.capability === 'vision' ? $t('tm_cap_vision') : $t('tm_cap_text') }}</span>
                  <v-chip v-if="!task.provider" size="x-small" color="primary" variant="tonal">{{ $t('tm_inherits') }}</v-chip>
                  <v-chip v-else-if="isTaskChanged(task)" size="x-small" color="warning" variant="tonal">{{ $t('tm_unsaved') }}</v-chip>
                </div>
              </v-col>
              <v-col cols="12" sm="5" md="3">
                <v-select
                  :model-value="task.provider"
                  :label="$t('tm_provider')"
                  :items="taskProviderOptions"
                  density="compact"
                  hide-details="auto"
                  :disabled="!canEdit || savingTaskSettings"
                  @update:model-value="selectTaskProvider(task, $event)"
                />
              </v-col>
              <v-col cols="12" sm="7" md="5">
                <v-combobox
                  :model-value="task.model"
                  :label="$t('tm_model')"
                  :items="modelItems(task.provider, task.capability === 'vision')"
                  item-title="title"
                  item-value="value"
                  :return-object="false"
                  :loading="isCatalogLoading(task.provider)"
                  :disabled="!canEdit || !task.provider || savingTaskSettings"
                  :placeholder="task.provider ? $t('tm_model_placeholder') : defaultLabel"
                  :hint="taskModelHint(task)"
                  persistent-hint
                  density="compact"
                  clearable
                  @update:model-value="task.model = $event ?? ''"
                />
              </v-col>
            </v-row>
          </div>
        </v-card>
      </template>

      <div v-if="canEdit && (taskChanges.length || hasInvalidTaskSettings)" class="tm-save-bar d-flex align-center flex-wrap ga-3 py-3">
        <v-btn
          color="primary"
          :loading="savingTaskSettings"
          :disabled="hasInvalidTaskSettings || !taskChanges.length"
          data-testid="save-ai-task-settings"
          @click="saveTaskAISettings"
        >{{ taskChanges.length ? $t('tm_save_n', { n: taskChanges.length }) : $t('tm_save') }}</v-btn>
        <span v-if="hasInvalidTaskSettings" class="text-caption text-error">{{ $t('tm_model_required') }}</span>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, reactive, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import api from '../../api'
import type { Agent } from './types'
import {
  type AIProvider,
  type ProviderCredentials,
  defaultModelByProvider,
  isAIProvider,
  loadNineRouterModels,
  loadOpenRouterModels,
  modelOptionsFor,
  parseProviderCredentials,
  providerOptions,
  useAIModelCatalog,
} from '../../composables/aiModelCatalog'

const props = defineProps<{
  tenantId: string
  canEdit: boolean
  agents: Agent[]
}>()
const emit = defineEmits<{
  (e: 'notify', text: string, color?: string): void
  (e: 'open-agent', id: string): void
}>()

const { t } = useI18n()
const catalog = useAIModelCatalog()

type TaskAISetting = {
  key: string
  label: string
  group: string
  capability: 'text' | 'vision'
  provider: AIProvider | ''
  model: string
}
type FlowRole = 'internal' | 'public'
type FlowModelOption = { title?: string; name: string; provider: string }
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

// The task registry's "Chatbot" group is the part around the chatbot; every
// other group (analysis, ERP, knowledge base, ...) is "other business work".
const CHATBOT_GROUP = 'Chatbot'

const creds = reactive<ProviderCredentials>(parseProviderCredentials({}))
const loadingCreds = ref(false)

// ---- default model ----
const defaultSaved = reactive<{ provider: AIProvider; model: string }>({ provider: 'claude', model: '' })
const defaultDraft = reactive<{ provider: AIProvider; model: string }>({ provider: 'claude', model: '' })
const savingDefault = ref(false)
const defaultLabel = computed(() => [defaultSaved.provider, defaultSaved.model].filter(Boolean).join(' / '))
const defaultChanged = computed(() => defaultDraft.provider !== defaultSaved.provider || defaultDraft.model.trim() !== defaultSaved.model)
const canSaveDefault = computed(() => !!creds.apiKeys[defaultDraft.provider] && !!defaultDraft.model.trim() &&
  (defaultDraft.provider !== '9router' || !!creds.baseURLs['9router']))
const defaultProviderItems = computed(() => providerOptions.map(option => ({
  title: creds.apiKeys[option.value] ? option.title : `${option.title} (${t('tm_no_key_short')})`,
  value: option.value,
})))

function providerName(provider: AIProvider | '') {
  return providerOptions.find(option => option.value === provider)?.title || provider
}

function selectDefaultProvider(provider: AIProvider) {
  if (provider === defaultDraft.provider) return
  defaultDraft.provider = provider
  defaultDraft.model = provider === defaultSaved.provider ? defaultSaved.model : defaultModelByProvider[provider]
  ensureCatalog(provider)
}

async function saveDefault() {
  if (!defaultChanged.value || !canSaveDefault.value || savingDefault.value) return
  const provider = defaultDraft.provider
  const model = defaultDraft.model.trim()
  savingDefault.value = true
  try {
    await api.put(`/tenants/${props.tenantId}/settings/ai`, {
      provider,
      model,
      model_only: true,
    })
    defaultSaved.provider = provider
    defaultSaved.model = model
    defaultDraft.model = model
    creds.defaultProvider = provider
    creds.defaultModel = model
    emit('notify', t('tm_default_saved'), 'success')
  } catch (err: any) {
    const failure = err.response?.data
    emit('notify', failure?.details || failure?.error || t('tm_default_save_failed'), 'error')
  } finally {
    savingDefault.value = false
  }
}

// ---- model suggestions ----
function modelItems(provider: AIProvider | '', vision: boolean) {
  return modelOptionsFor(provider, vision, t('nine_router_other_models'))
}

function isCatalogLoading(provider: AIProvider | '') {
  return (provider === '9router' && catalog.nineRouterStatus === 'loading') ||
    (provider === 'openrouter' && catalog.openRouterStatus === 'loading')
}

const hasSavedNineRouter = computed(() => !!creds.apiKeys['9router'] && !!creds.baseURLs['9router'])

function ensureCatalog(provider: AIProvider | '') {
  if (provider === 'openrouter') loadOpenRouterModels(props.tenantId)
  if (provider === '9router' && hasSavedNineRouter.value) loadNineRouterModels(props.tenantId)
}

function providerHint(provider: AIProvider | '', vision: boolean) {
  if (!provider) return ''
  if (provider === '9router') {
    if (!hasSavedNineRouter.value) return t('tm_nine_router_not_saved')
    if (catalog.nineRouterStatus === 'error') return t('nine_router_models_failed')
    return t(vision ? 'nine_router_vision_hint' : 'nine_router_model_hint')
  }
  if (!creds.apiKeys[provider]) return t('tm_no_key', { provider: providerName(provider) })
  if (provider === 'openrouter' && catalog.openRouterStatus === 'error') return t('openrouter_load_failed')
  return vision ? t('tm_hint_vision') : t('tm_hint_text')
}

// ---- per-task models ----
const taskAISettings = ref<TaskAISetting[]>([])
const savedTaskModels = ref(new Map<string, { provider: AIProvider | ''; model: string }>())
const loadingTaskSettings = ref(false)
const savingTaskSettings = ref(false)
const taskSettingsError = ref('')

const taskProviderOptions = computed(() => [
  { title: t('tm_use_default'), value: '' },
  ...providerOptions,
])

const taskSections = computed(() => {
  const groups = [...new Set(taskAISettings.value.map(task => task.group))]
  const byGroup = (name: string) => ({ name, tasks: taskAISettings.value.filter(task => task.group === name) })
  return [
    { id: 'chatbot', groups: groups.filter(name => name === CHATBOT_GROUP).map(byGroup) },
    { id: 'other', groups: groups.filter(name => name !== CHATBOT_GROUP).map(byGroup) },
  ].filter(section => section.groups.length)
})

const hasInvalidTaskSettings = computed(() => taskAISettings.value.some(task => task.provider && !task.model.trim()))
const taskChanges = computed(() => taskAISettings.value.flatMap(task => {
  const model = task.provider ? task.model.trim() : ''
  const saved = savedTaskModels.value.get(task.key)
  return task.provider === saved?.provider && model === saved?.model
    ? []
    : [{ key: task.key, provider: task.provider, model }]
}))

function isTaskChanged(task: TaskAISetting) {
  return taskChanges.value.some(change => change.key === task.key)
}

function selectTaskProvider(task: TaskAISetting, provider: AIProvider | '') {
  if (!provider) {
    task.provider = ''
    task.model = ''
    return
  }
  if (task.provider !== provider) {
    const saved = savedTaskModels.value.get(task.key)
    task.provider = provider
    task.model = saved?.provider === provider ? saved.model : defaultModelByProvider[provider]
  }
  ensureCatalog(provider)
}

function taskModelHint(task: TaskAISetting) {
  if (!task.provider) return t('tm_using_default', { model: defaultLabel.value || t('tm_default_title') })
  return providerHint(task.provider, task.capability === 'vision')
}

function applyTaskAISettings(data: any, includeDefault: boolean) {
  if (includeDefault && !savingDefault.value && isAIProvider(data?.default_provider)) {
    const preserveDraft = defaultChanged.value
    defaultSaved.provider = data.default_provider
    defaultSaved.model = typeof data?.default_model === 'string' ? data.default_model : ''
    if (!preserveDraft) {
      defaultDraft.provider = defaultSaved.provider
      defaultDraft.model = defaultSaved.model
    }
  }
  taskAISettings.value = Array.isArray(data?.tasks)
    ? data.tasks.map((task: any) => ({
        key: String(task.key || ''),
        label: String(task.label || task.key || ''),
        group: String(task.group || t('tm_group_misc')),
        capability: task.capability === 'vision' ? 'vision' : 'text',
        provider: isAIProvider(task.provider) ? task.provider : '',
        model: typeof task.model === 'string' ? task.model : '',
      })).filter((task: TaskAISetting) => task.key)
    : []
  savedTaskModels.value = new Map(taskAISettings.value.map(({ key, provider, model }) => [key, { provider, model }]))
}

async function loadTaskAISettings() {
  loadingTaskSettings.value = true
  taskSettingsError.value = ''
  try {
    const { data } = await api.get(`/tenants/${props.tenantId}/settings/ai/tasks`)
    applyTaskAISettings(data, true)
  } catch (err: any) {
    taskSettingsError.value = err.response?.data?.error || t('tm_tasks_load_failed')
  } finally {
    loadingTaskSettings.value = false
  }
}

async function saveTaskAISettings() {
  if (hasInvalidTaskSettings.value || !taskChanges.value.length || savingTaskSettings.value) return
  savingTaskSettings.value = true
  taskSettingsError.value = ''
  try {
    const { data } = await api.put(`/tenants/${props.tenantId}/settings/ai/tasks`, { tasks: taskChanges.value })
    applyTaskAISettings(data, false)
    emit('notify', t('tm_saved'), 'success')
  } catch (err: any) {
    taskSettingsError.value = err.response?.data?.details || err.response?.data?.error || t('tm_tasks_save_failed')
  } finally {
    savingTaskSettings.value = false
  }
}

// ---- Langflow chatbot flows ----
const flowRoles: FlowRole[] = ['internal', 'public']
const flowSettings = reactive<Record<FlowRole, FlowAISetting | null>>({ internal: null, public: null })
const flowSelections = reactive<Record<FlowRole, FlowModelOption | null>>({ internal: null, public: null })
const loadingFlowSettings = ref(false)
const savingFlowRole = ref<FlowRole | ''>('')
const flowSettingsError = ref('')
const hasFlowSettings = computed(() => flowRoles.some(role => flowSettings[role]))

function flowRoleLabel(role: FlowRole) {
  return role === 'internal' ? t('tm_flow_internal') : t('tm_flow_public')
}

function sharedFlowRoles(flow: FlowAISetting) {
  return (flow.shared_roles || []).map(flowRoleLabel).join(` ${t('tm_and')} `)
}

function flowModelOptions(flow: FlowAISetting | null) {
  if (!flow) return []
  const options = [...(Array.isArray(flow.options) ? flow.options : [])]
  if (flow.model && !options.some(option => option.name === flow.model && option.provider === flow.provider)) {
    options.unshift({ name: flow.model, provider: flow.provider })
  }
  return options.map(option => ({ ...option, title: option.provider ? `${option.name} · ${option.provider}` : option.name }))
}

function flowSelectionHint(role: FlowRole) {
  const flow = flowSettings[role]
  if (!flow) return t('tm_flow_missing')
  if (!flow.managed) return flow.management_problem || t('tm_flow_not_managed')
  return t('tm_flow_current', { model: flow.provider ? `${flow.model} · ${flow.provider}` : flow.model })
}

function canSaveFlow(role: FlowRole) {
  const flow = flowSettings[role]
  const selection = flowSelections[role]
  return !!flow?.managed && !!selection?.name && (selection.name !== flow.model || selection.provider !== flow.provider)
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
    flowSelections[role] = flowModelOptions(flow).find(option => option.name === flow.model && option.provider === flow.provider) || null
  }
}

async function loadFlowAISettings() {
  if (loadingFlowSettings.value) return
  loadingFlowSettings.value = true
  flowSettingsError.value = ''
  try {
    const { data } = await api.get(`/tenants/${props.tenantId}/settings/ai/flows`)
    applyFlowAISettings(data)
  } catch (err: any) {
    flowSettingsError.value = err.response?.data?.message || err.response?.data?.error || t('tm_flows_load_failed')
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
    const { data } = await api.put(`/tenants/${props.tenantId}/settings/ai/flows/${role}`, {
      model: selection.name,
      provider: selection.provider,
    })
    const updatedRoles: FlowRole[] = Array.isArray(data?.updated_roles) ? data.updated_roles : [role]
    await loadFlowAISettings()
    emit('notify', t('tm_flow_synced', { roles: updatedRoles.map(flowRoleLabel).join(` ${t('tm_and')} `) }), 'success')
  } catch (err: any) {
    flowSettingsError.value = err.response?.data?.message || err.response?.data?.error || t('tm_flow_sync_failed')
  } finally {
    savingFlowRole.value = ''
  }
}

// A Langflow agent runs on its own flow, or on the AI Engine flow of its tier.
function agentFlowLabel(a: Agent) {
  if (a.langflow_flow_id) return t('tm_agents_own_flow', { id: a.langflow_flow_id })
  const name = (role: FlowRole) => flowSettings[role]?.name || flowSettings[role]?.flow_id || flowRoleLabel(role)
  if (a.audience === 'customer') return name('public')
  if (a.audience === 'staff') return name('internal')
  return `${name('public')} · ${name('internal')}`
}

// ---- loading ----
async function loadCredentials() {
  loadingCreds.value = true
  try {
    const { data } = await api.get(`/tenants/${props.tenantId}/settings`)
    Object.assign(creds, parseProviderCredentials(data?.settings))
  } catch {
    // Without saved settings every provider shows "no key".
  } finally {
    loadingCreds.value = false
  }
}

const loadingAny = computed(() => loadingCreds.value || loadingTaskSettings.value || loadingFlowSettings.value)

async function loadAll() {
  await Promise.all([loadCredentials(), loadTaskAISettings(), loadFlowAISettings()])
  const used = new Set<AIProvider | ''>([defaultDraft.provider, ...taskAISettings.value.map(task => task.provider)])
  for (const provider of used) ensureCatalog(provider)
}

watch(() => props.tenantId, (next, prev) => {
  if (next && next !== prev) loadAll()
})

onMounted(loadAll)

defineExpose({
  taskAISettings, defaultDraft, defaultSaved, creds, flowSettings, flowSelections, taskSections,
  selectTaskProvider, saveTaskAISettings, selectDefaultProvider, saveDefault, saveFlowAISetting, modelItems, agentFlowLabel,
})
</script>

<style scoped>
.tm-save-bar {
  position: sticky;
  bottom: 0;
  z-index: 2;
  background: rgb(var(--v-theme-background));
  border-top: 1px solid rgba(var(--v-border-color), var(--v-border-opacity));
}
</style>
