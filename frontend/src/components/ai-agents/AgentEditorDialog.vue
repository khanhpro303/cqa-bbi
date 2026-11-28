<template>
  <v-dialog :model-value="modelValue" max-width="980" scrollable persistent @update:model-value="v => !v && requestClose()">
    <v-card class="agent-editor-card" data-testid="agent-editor">
      <v-card-title class="editor-header d-flex align-center">
        <div class="header-icon mr-3">
          <v-icon color="primary" size="20">mdi-robot-outline</v-icon>
        </div>
        <div class="d-flex align-center flex-wrap ga-2 min-w-0 flex-grow-1">
          <span class="editor-title text-truncate">{{ isNew ? $t('ag_new_agent') : (draft.name || draft.key) }}</span>
          <v-chip v-if="draft.options.legacy_source" size="x-small" variant="tonal" color="info">
            {{ $t('ag_legacy_badge') }}
          </v-chip>
        </div>
        <v-btn icon="mdi-close" variant="text" size="small" density="comfortable" class="ml-2 flex-shrink-0" :aria-label="$t('cancel')" @click="requestClose" />
      </v-card-title>

      <v-tabs v-if="!isNew" v-model="tab" color="primary" density="compact" class="px-6 flex-shrink-0">
        <v-tab value="config" data-testid="agent-tab-config">{{ $t('ag_tab_config') }}</v-tab>
        <v-tab value="revisions" data-testid="agent-tab-revisions">{{ $t('ag_tab_revisions') }}</v-tab>
      </v-tabs>
      <v-divider />

      <v-card-text class="editor-body">
        <div v-if="loading" class="d-flex justify-center py-12"><v-progress-circular indeterminate color="primary" /></div>

        <!-- Configuration -->
        <div v-else-if="tab === 'config'">
          <v-alert v-if="loadError" type="error" variant="tonal" class="mb-4">{{ loadError }}</v-alert>
          <v-alert v-if="readonly" type="info" variant="tonal" density="compact" class="mb-4">{{ $t('ag_readonly_hint') }}</v-alert>

          <v-row class="field-row">
            <v-col cols="12" md="6">
              <v-text-field
                v-model="draft.name"
                :label="$t('ag_field_name')"
                :error-messages="errors.name"
                :readonly="readonly"
                counter="120"
                density="comfortable"
                variant="outlined"
                data-testid="agent-name"
                @update:model-value="onNameInput"
              />
            </v-col>
            <v-col cols="12" md="6">
              <v-text-field
                v-model="draft.key"
                :label="$t('ag_field_key')"
                :hint="isNew ? $t('ag_field_key_hint_new') : $t('ag_field_key_hint')"
                persistent-hint
                :error-messages="errors.key"
                :readonly="!isNew || readonly"
                density="comfortable"
                variant="outlined"
                class="font-mono-field"
                data-testid="agent-key"
                @update:model-value="keyTouched = true"
              />
            </v-col>
          </v-row>

          <v-textarea
            v-model="draft.description"
            :label="$t('ag_field_description')"
            :hint="$t('ag_field_description_hint')"
            persistent-hint
            :error-messages="errors.description"
            :readonly="readonly"
            rows="2"
            max-rows="4"
            auto-grow
            density="comfortable"
            variant="outlined"
            counter="2000"
            class="mt-2 mb-4"
          />

          <div class="audience-status-row">
            <div class="audience-col">
              <div class="field-label mb-2">{{ $t('ag_field_audience') }}</div>
              <v-btn-toggle
                v-model="draft.audience"
                mandatory
                variant="text"
                density="comfortable"
                class="segmented-toggle"
                :disabled="readonly"
                data-testid="agent-audience"
              >
                <v-btn
                  v-for="a in AUDIENCES"
                  :key="a"
                  :value="a"
                  rounded="md"
                  variant="text"
                  class="segmented-btn"
                  :class="{ 'segmented-btn--active': draft.audience === a }"
                >
                  <v-icon start size="16">{{ audienceIcon(a) }}</v-icon>
                  {{ $t(`ag_audience_${a}`) }}
                </v-btn>
              </v-btn-toggle>
              <div class="field-hint mt-2">{{ $t(`ag_audience_${draft.audience}_hint`) }}</div>
              <div v-if="errors.audience" class="text-caption text-error mt-1">{{ errors.audience }}</div>
            </div>

            <div class="status-col">
              <div class="field-label mb-2">{{ $t('status') }}</div>
              <div class="status-pill" :class="{ 'status-pill--active': draft.is_active }">
                <v-switch
                  v-model="draft.is_active"
                  :label="draft.is_active ? $t('ag_active') : $t('ag_inactive')"
                  color="primary"
                  density="compact"
                  hide-details
                  inset
                  :disabled="readonly"
                  data-testid="agent-active"
                />
              </div>
            </div>
          </div>

          <div class="form-section">
            <div class="d-flex align-center justify-space-between ga-2 mb-2">
              <div class="section-title mb-0">
                <v-icon size="18" class="mr-2 text-medium-emphasis">mdi-text-box-outline</v-icon>
                {{ $t('ag_field_prompt') }}
              </div>
              <span class="prompt-counter tabular">{{ promptLength.toLocaleString('vi-VN') }} / {{ MAX_PROMPT_CHARS.toLocaleString('vi-VN') }}</span>
            </div>
            <v-textarea
              v-model="draft.system_prompt"
              :placeholder="$t('ag_field_prompt_placeholder')"
              :hint="draft.runtime === 'native' ? `${$t('ag_field_prompt_native_required')} ${$t('ag_field_prompt_hint')}` : $t('ag_field_prompt_hint')"
              persistent-hint
              :error-messages="errors.system_prompt"
              :readonly="readonly"
              rows="7"
              max-rows="18"
              auto-grow
              variant="outlined"
              class="prompt-field"
              data-testid="agent-prompt"
            />
          </div>

          <!-- Tools -->
          <div class="form-section">
            <div class="d-flex align-center flex-wrap ga-2 mb-1">
              <div class="section-title mb-0">
                <v-icon size="18" class="mr-2 text-medium-emphasis">mdi-tools</v-icon>
                {{ $t('ag_field_tools') }}
                <v-chip size="x-small" class="ml-2 tabular" variant="tonal" color="primary">{{ draft.tools.length }}</v-chip>
              </div>
              <v-spacer />
              <div v-if="!readonly" class="d-flex align-center ga-2">
                <v-btn size="small" variant="tonal" density="comfortable" @click="selectAllTools">{{ $t('ag_tools_all') }}</v-btn>
                <v-btn size="small" variant="outlined" density="comfortable" :disabled="draft.tools.length === 0" @click="draft.tools = []">{{ $t('ag_tools_none') }}</v-btn>
              </div>
            </div>
            <div class="field-hint mb-3">{{ $t('ag_tools_hint') }}</div>
            <v-alert v-if="errors.tools" type="error" variant="tonal" density="compact" class="mb-3">{{ errors.tools }}</v-alert>
            <v-alert v-if="draft.tools.length === 0" type="info" variant="tonal" density="compact" class="mb-3">{{ $t('ag_tools_empty') }}</v-alert>
            <v-row dense>
              <v-col v-for="g in toolGroups" :key="g.group" cols="12" md="6">
                <v-card variant="outlined" class="tool-group-card pa-3 h-100" :data-testid="`tool-group-${g.group}`">
                  <div class="d-flex align-center ga-2 mb-1">
                    <div class="text-body-2 font-weight-bold">{{ groupLabel(g.group) }}</div>
                    <v-spacer />
                    <v-btn
                      v-if="!readonly"
                      size="x-small"
                      variant="tonal"
                      @click="toggleGroup(g.group)"
                    >{{ groupAllSelected(g.group) ? $t('ag_tools_none') : $t('ag_tools_all') }}</v-btn>
                  </div>
                  <div v-if="groupHint(g.group)" class="field-hint mb-2">{{ groupHint(g.group) }}</div>
                  <v-checkbox
                    v-for="t in g.tools"
                    :key="t.info.name"
                    v-model="draft.tools"
                    :value="t.info.name"
                    :disabled="readonly || (!t.usable && !draft.tools.includes(t.info.name))"
                    density="compact"
                    hide-details
                    color="primary"
                    class="tool-checkbox"
                    :data-testid="`tool-${t.info.name}`"
                  >
                    <template #label>
                      <span class="tool-label">
                        <span class="tool-name">{{ toolLabel(t.info.name) }}</span>
                        <code class="tool-code">{{ t.info.name }}</code>
                        <v-chip v-if="t.info.writes" size="x-small" color="warning" variant="tonal">{{ $t('ag_tool_writes') }}</v-chip>
                        <v-chip v-if="!t.usable" size="x-small" color="error" variant="tonal">{{ $t('ag_tool_not_for_audience') }}</v-chip>
                      </span>
                    </template>
                  </v-checkbox>
                </v-card>
              </v-col>
            </v-row>
          </div>

          <!-- Runtime and flow -->
          <div class="form-section">
            <div class="section-title mb-3">
              <v-icon size="18" class="mr-2 text-medium-emphasis">mdi-sitemap-outline</v-icon>
              {{ $t('ag_section_runtime') }}
            </div>
            <v-row dense>
              <v-col cols="12" md="4">
                <v-select
                  v-model="draft.runtime"
                  :items="runtimeItems"
                  :label="$t('ag_field_runtime')"
                  :error-messages="errors.runtime"
                  :readonly="readonly"
                  :hint="runtimes.includes('native') ? '' : $t('ag_runtime_native_missing')"
                  persistent-hint
                />
              </v-col>
              <v-col cols="12" md="8">
                <v-text-field
                  v-if="draft.runtime === 'langflow'"
                  v-model="draft.langflow_flow_id"
                  :label="$t('ag_field_flow')"
                  :placeholder="$t('ag_field_flow_placeholder')"
                  :hint="$t('ag_field_flow_hint')"
                  persistent-hint
                  :error-messages="errors.langflow_flow_id"
                  :readonly="readonly"
                  clearable
                  class="font-mono-field"
                  data-testid="agent-flow"
                />
              </v-col>
            </v-row>
            <v-row v-if="draft.runtime === 'native'" dense class="mt-2">
              <v-col cols="12" md="4">
                <v-select v-model="draft.provider" :items="providerItems" :label="$t('ag_field_provider')" :error-messages="errors.provider" :readonly="readonly" />
              </v-col>
              <v-col cols="12" md="4">
                <v-text-field v-model="draft.model" :label="$t('ag_field_model')" :error-messages="errors.model" :readonly="readonly" class="font-mono-field" />
              </v-col>
              <v-col cols="6" md="2">
                <v-text-field
                  v-model.number="draft.options.max_tool_calls"
                  type="number"
                  min="0"
                  max="20"
                  :label="$t('ag_field_max_tool_calls')"
                  :hint="$t('ag_field_max_tool_calls_hint')"
                  persistent-hint
                  :error-messages="errors['options.max_tool_calls']"
                  :readonly="readonly"
                />
              </v-col>
              <v-col cols="6" md="2">
                <v-text-field
                  v-model.number="draft.options.temperature"
                  type="number"
                  min="0"
                  max="2"
                  step="0.1"
                  :label="$t('ag_field_temperature')"
                  :error-messages="errors['options.temperature']"
                  :readonly="readonly"
                />
              </v-col>
            </v-row>
          </div>

          <!-- Knowledge tags -->
          <div class="form-section">
            <div class="section-title mb-3">
              <v-icon size="18" class="mr-2 text-medium-emphasis">mdi-tag-multiple-outline</v-icon>
              {{ $t('ag_field_tags') }}
            </div>
            <v-combobox
              v-model="draft.knowledge_tags"
              :items="tagItems"
              item-title="title"
              item-value="value"
              :return-object="false"
              multiple
              chips
              closable-chips
              density="comfortable"
              variant="outlined"
              :hint="knowledgeTags ? $t('ag_field_tags_hint_live') : $t('ag_field_tags_hint')"
              persistent-hint
              :error-messages="errors.knowledge_tags"
              :readonly="readonly"
              :placeholder="$t('ag_field_tags_placeholder')"
              data-testid="agent-tags"
            />
          </div>
        </div>

        <!-- Revisions -->
        <div v-else-if="tab === 'revisions'" data-testid="agent-revisions">
          <div v-if="revisionsLoading" class="d-flex justify-center py-8"><v-progress-circular indeterminate color="primary" /></div>
          <v-alert v-else-if="revisions.length === 0" type="info" variant="tonal">{{ $t('ag_revisions_empty') }}</v-alert>
          <v-row v-else>
            <v-col cols="12" md="4">
              <div class="field-hint mb-2">{{ $t('ag_revisions_hint') }}</div>
              <v-list density="compact" class="revision-list" nav>
                <v-list-item
                  v-for="(rev, idx) in revisions"
                  :key="rev.id"
                  :active="selectedRevision?.id === rev.id"
                  color="primary"
                  :data-testid="`revision-${idx}`"
                  @click="selectedRevision = rev"
                >
                  <v-list-item-title class="text-body-2">
                    {{ formatTime(rev.created_at) }}
                    <v-chip v-if="idx === 0" size="x-small" color="success" variant="tonal" class="ml-1">{{ $t('ag_revision_current') }}</v-chip>
                  </v-list-item-title>
                  <v-list-item-subtitle class="text-caption">
                    {{ editorName(rev.edited_by) }} · {{ $t('ag_chars', { n: (rev.system_prompt || '').length.toLocaleString('vi-VN') }) }}
                  </v-list-item-subtitle>
                </v-list-item>
              </v-list>
            </v-col>
            <v-col v-if="selectedRevision" cols="12" md="8">
              <div class="d-flex align-center flex-wrap ga-2 mb-2">
                <v-btn-toggle v-model="revisionView" mandatory density="compact" variant="text" class="segmented-toggle segmented-toggle--compact">
                  <v-btn value="diff" size="small" rounded="md" variant="text" class="segmented-btn" :class="{ 'segmented-btn--active': revisionView === 'diff' }">{{ $t('ag_revision_view_diff') }}</v-btn>
                  <v-btn value="full" size="small" rounded="md" variant="text" class="segmented-btn" :class="{ 'segmented-btn--active': revisionView === 'full' }">{{ $t('ag_revision_view_full') }}</v-btn>
                </v-btn-toggle>
                <span v-if="revisionView === 'diff'" class="text-caption">
                  <span class="text-success">+{{ revisionStats.added }}</span>
                  <span class="text-error ml-1">−{{ revisionStats.removed }}</span>
                  <span class="text-medium-emphasis ml-1">{{ $t('ag_revision_diff_hint') }}</span>
                </span>
                <v-spacer />
                <v-btn
                  v-if="!readonly"
                  size="small"
                  color="primary"
                  variant="tonal"
                  prepend-icon="mdi-backup-restore"
                  data-testid="revision-restore"
                  @click="restoreRevision(selectedRevision)"
                >{{ $t('ag_revision_restore') }}</v-btn>
              </div>
              <div v-if="selectedRevision.name !== draft.name || selectedRevision.description !== draft.description" class="text-caption text-medium-emphasis mb-2">
                {{ $t('ag_field_name') }}: <strong>{{ selectedRevision.name }}</strong>
                <span v-if="selectedRevision.description"> · {{ selectedRevision.description }}</span>
              </div>
              <div v-if="revisionView === 'full'" class="prompt-view">{{ selectedRevision.system_prompt || '—' }}</div>
              <div v-else class="prompt-view diff-view">
                <div v-if="revisionStats.added + revisionStats.removed === 0" class="text-medium-emphasis">{{ $t('ag_revision_same') }}</div>
                <div v-for="(line, i) in revisionStats.added + revisionStats.removed ? revisionDiff : []" :key="i" :class="`diff-${line.kind}`">
                  <template v-if="line.kind === 'gap'">… {{ $t('ag_revision_gap', { n: line.text }) }} …</template>
                  <template v-else>{{ line.kind === 'add' ? '+ ' : line.kind === 'del' ? '− ' : '  ' }}{{ line.text }}</template>
                </div>
              </div>
            </v-col>
          </v-row>
        </div>
      </v-card-text>

      <v-divider />
      <v-card-actions class="editor-footer ga-3 flex-wrap">
        <v-tooltip v-if="!isNew && !readonly" :disabled="teamNames.length === 0" location="top">
          <template #activator="{ props: tip }">
            <span v-bind="tip">
              <v-btn
                color="error"
                variant="text"
                prepend-icon="mdi-delete-outline"
                :disabled="teamNames.length > 0 || saving"
                data-testid="agent-delete"
                @click="confirmDelete = true"
              >{{ $t('delete') }}</v-btn>
            </span>
          </template>
          {{ $t('ag_delete_in_team', { teams: teamNames.join(', ') }) }}
        </v-tooltip>
        <v-spacer />
        <v-btn variant="outlined" class="footer-btn" @click="requestClose">{{ readonly ? $t('ag_close') : $t('cancel') }}</v-btn>
        <v-btn v-if="!readonly" color="primary" variant="flat" class="footer-btn" :loading="saving" :disabled="loading" data-testid="agent-save" @click="save">{{ $t('save') }}</v-btn>
      </v-card-actions>
    </v-card>

    <v-dialog v-model="confirmDelete" max-width="460">
      <v-card>
        <v-card-title class="text-h6">{{ $t('ag_delete_title') }}</v-card-title>
        <v-card-text>{{ $t('ag_delete_body', { name: draft.name }) }}</v-card-text>
        <v-card-actions class="justify-end ga-2 pa-4">
          <v-btn variant="outlined" @click="confirmDelete = false">{{ $t('cancel') }}</v-btn>
          <v-btn color="error" variant="flat" :loading="deleting" data-testid="agent-delete-confirm" @click="remove">{{ $t('delete') }}</v-btn>
        </v-card-actions>
      </v-card>
    </v-dialog>

    <v-dialog v-model="confirmDiscard" max-width="420">
      <v-card>
        <v-card-title class="text-h6">{{ $t('ag_discard_title') }}</v-card-title>
        <v-card-text>{{ $t('ag_discard_body') }}</v-card-text>
        <v-card-actions class="justify-end ga-2 pa-4">
          <v-btn variant="outlined" @click="confirmDiscard = false">{{ $t('ag_keep_editing') }}</v-btn>
          <v-btn color="error" variant="flat" data-testid="agent-discard" @click="close">{{ $t('ag_discard') }}</v-btn>
        </v-card-actions>
      </v-card>
    </v-dialog>
  </v-dialog>
</template>

<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import api from '../../api'
import type { Agent, AgentRevision, KnowledgeTag, ToolInfo } from './types'
import { emptyAgent, errorCodeOf, fieldErrorOf } from './types'
import {
  AGENT_KEY_PATTERN, AUDIENCES, FLOW_ID_PATTERN, MAX_PROMPT_CHARS,
  diffLines, diffStats, groupTools, normalizeTags, slugify, unusableTools,
} from './agentRules'

const props = defineProps<{
  modelValue: boolean
  tenantId: string
  // Empty for a new agent.
  agentId: string
  catalog: ToolInfo[]
  runtimes: string[]
  readonly?: boolean
  // Names of the teams the agent is in (it cannot be deleted while > 0).
  teamNames: string[]
  userNames?: Record<string, string>
  // Document tags in use; null when the server has no tag list yet.
  knowledgeTags?: KnowledgeTag[] | null
}>()

const emit = defineEmits<{
  'update:modelValue': [value: boolean]
  saved: [agent: Agent]
  deleted: [id: string]
  notify: [text: string, color: string]
}>()

const { t, te } = useI18n()

const tab = ref<'config' | 'revisions'>('config')
const draft = ref<Agent>(emptyAgent())
const snapshot = ref('')
const loading = ref(false)
const loadError = ref('')
const saving = ref(false)
const deleting = ref(false)
const confirmDelete = ref(false)
const confirmDiscard = ref(false)
const keyTouched = ref(false)
const errors = ref<Record<string, string>>({})

const revisions = ref<AgentRevision[]>([])
const revisionsLoading = ref(false)
const revisionsLoaded = ref(false)
const selectedRevision = ref<AgentRevision | null>(null)
const revisionView = ref<'diff' | 'full'>('diff')

const isNew = computed(() => !props.agentId)
const promptLength = computed(() => [...(draft.value.system_prompt || '')].length)
const toolGroups = computed(() => groupTools(props.catalog, draft.value.tools, draft.value.audience))
const runtimeItems = computed(() => {
  const items = [{ title: t('ag_runtime_langflow'), value: 'langflow', props: {} as Record<string, unknown> }]
  items.push({ title: t('ag_runtime_native'), value: 'native', props: { disabled: !props.runtimes.includes('native') } })
  return items
})
// Same vendors as the AI task model picker in Settings; the backend
// validates the pair, so a vendor it does not know is refused there.
const tagItems = computed(() => (props.knowledgeTags || []).map(x => ({
  title: t('ag_tag_item', { tag: x.tag, n: x.documents }),
  value: x.tag,
})))
const providerItems = [
  { title: 'OpenAI', value: 'openai' },
  { title: 'Claude', value: 'claude' },
  { title: 'Gemini', value: 'gemini' },
  { title: 'OpenRouter', value: 'openrouter' },
  { title: '9router', value: '9router' },
]

const revisionDiff = computed(() => selectedRevision.value
  ? diffLines(selectedRevision.value.system_prompt || '', draft.value.system_prompt || '')
  : [])
const revisionStats = computed(() => diffStats(revisionDiff.value))

function serialize(a: Agent): string {
  return JSON.stringify({ ...a, tools: [...a.tools].sort(), knowledge_tags: [...a.knowledge_tags] })
}
const dirty = computed(() => snapshot.value !== '' && serialize(draft.value) !== snapshot.value)

function toolLabel(name: string): string {
  const key = `ag_tool_${name}`
  return te(key) ? t(key) : name
}
function audienceIcon(audience: string): string {
  if (audience === 'staff') return 'mdi-badge-account-outline'
  if (audience === 'all') return 'mdi-account-multiple-outline'
  return 'mdi-account-outline'
}
function groupLabel(group: string): string {
  const key = `ag_group_${group}`
  return te(key) ? t(key) : group
}
function groupHint(group: string): string {
  const key = `ag_group_${group}_hint`
  return te(key) ? t(key) : ''
}

function groupNames(group: string): string[] {
  return toolGroups.value.find(g => g.group === group)?.tools.filter(x => x.usable).map(x => x.info.name) || []
}
function groupAllSelected(group: string): boolean {
  const names = groupNames(group)
  return names.length > 0 && names.every(n => draft.value.tools.includes(n))
}
function toggleGroup(group: string) {
  const names = groupNames(group)
  if (groupAllSelected(group)) {
    draft.value.tools = draft.value.tools.filter(n => !names.includes(n))
  } else {
    draft.value.tools = [...new Set([...draft.value.tools, ...names])]
  }
}
function selectAllTools() {
  draft.value.tools = props.catalog.filter(x => !x.hidden || draft.value.tools.includes(x.name))
    .filter(x => toolGroups.value.some(g => g.tools.some(tt => tt.info.name === x.name && tt.usable)))
    .map(x => x.name)
}

function onNameInput(name: string) {
  if (isNew.value && !keyTouched.value) draft.value.key = slugify(name || '')
}

function formatTime(iso: string): string {
  const d = new Date(iso)
  return Number.isNaN(d.getTime()) ? iso : d.toLocaleString('vi-VN', { dateStyle: 'short', timeStyle: 'short' })
}
function editorName(id: string): string {
  if (!id) return t('ag_revision_system')
  return props.userNames?.[id] || id.slice(0, 8)
}

async function load() {
  errors.value = {}
  loadError.value = ''
  tab.value = 'config'
  keyTouched.value = false
  revisions.value = []
  revisionsLoaded.value = false
  selectedRevision.value = null
  if (isNew.value) {
    draft.value = emptyAgent()
    snapshot.value = serialize(draft.value)
    return
  }
  loading.value = true
  try {
    const { data } = await api.get(`/tenants/${props.tenantId}/ai-agents/${props.agentId}`)
    draft.value = { ...emptyAgent(), ...data, tools: data.tools || [], knowledge_tags: data.knowledge_tags || [], options: data.options || {}, system_prompt: data.system_prompt || '' }
    snapshot.value = serialize(draft.value)
  } catch {
    loadError.value = t('ag_load_failed')
    snapshot.value = ''
  } finally {
    loading.value = false
  }
}

async function loadRevisions() {
  if (isNew.value || revisionsLoaded.value) return
  revisionsLoading.value = true
  try {
    const { data } = await api.get(`/tenants/${props.tenantId}/ai-agents/${props.agentId}/revisions`)
    revisions.value = data.revisions || []
    revisionsLoaded.value = true
    selectedRevision.value = revisions.value[1] || revisions.value[0] || null
  } catch {
    emit('notify', t('ag_load_failed'), 'error')
  } finally {
    revisionsLoading.value = false
  }
}

function restoreRevision(rev: AgentRevision) {
  draft.value.name = rev.name
  draft.value.description = rev.description || ''
  draft.value.system_prompt = rev.system_prompt || ''
  tab.value = 'config'
  emit('notify', t('ag_revision_restored', { time: formatTime(rev.created_at) }), 'info')
}

// validate checks what the backend would refuse, so the message shows at
// the field without a round trip.
function validate(): boolean {
  const e: Record<string, string> = {}
  const a = draft.value
  if (!a.name.trim()) e.name = t('ag_err_name')
  if (isNew.value && !AGENT_KEY_PATTERN.test(a.key.trim())) e.key = t('ag_err_key')
  if (a.runtime === 'langflow' && !FLOW_ID_PATTERN.test((a.langflow_flow_id || '').trim())) e.langflow_flow_id = t('ag_err_flow')
  if (a.runtime === 'native' && !(a.system_prompt || '').trim()) e.system_prompt = t('ag_field_prompt_native_required')
  if (promptLength.value > MAX_PROMPT_CHARS) e.system_prompt = t('ag_err_prompt_long', { n: MAX_PROMPT_CHARS.toLocaleString('vi-VN') })
  const bad = unusableTools(props.catalog, a.tools, a.audience)
  if (bad.length) e.tools = t('ag_err_tools', { tools: bad.join(', ') })
  errors.value = e
  return Object.keys(e).length === 0
}

function payload(): Agent {
  const a = draft.value
  const options = { ...a.options }
  if (options.temperature === null || (options.temperature as unknown) === '') delete options.temperature
  if ((options.max_tool_calls as unknown) === '' || options.max_tool_calls == null) delete options.max_tool_calls
  return {
    ...a,
    key: a.key.trim(),
    name: a.name.trim(),
    langflow_flow_id: (a.langflow_flow_id || '').trim(),
    provider: a.runtime === 'native' ? a.provider : '',
    model: a.runtime === 'native' ? (a.model || '').trim() : '',
    knowledge_tags: normalizeTags(a.knowledge_tags),
    options,
  }
}

async function save() {
  if (!validate()) {
    tab.value = 'config'
    return
  }
  saving.value = true
  try {
    const body = payload()
    const { data } = isNew.value
      ? await api.post(`/tenants/${props.tenantId}/ai-agents`, body)
      : await api.put(`/tenants/${props.tenantId}/ai-agents/${props.agentId}`, body)
    snapshot.value = serialize(draft.value)
    emit('saved', data)
    emit('notify', t('ag_saved', { name: data.name }), 'success')
    close()
  } catch (err) {
    const fe = fieldErrorOf(err)
    if (fe) {
      errors.value = { [fe.field]: fe.message }
      tab.value = 'config'
    } else {
      emit('notify', t('ag_save_failed'), 'error')
    }
  } finally {
    saving.value = false
  }
}

async function remove() {
  deleting.value = true
  try {
    await api.delete(`/tenants/${props.tenantId}/ai-agents/${props.agentId}`)
    confirmDelete.value = false
    emit('deleted', props.agentId)
    emit('notify', t('ag_deleted'), 'success')
    close()
  } catch (err) {
    confirmDelete.value = false
    emit('notify', errorCodeOf(err) === 'in_use' ? t('ag_delete_in_use') : t('ag_save_failed'), 'error')
  } finally {
    deleting.value = false
  }
}

function requestClose() {
  if (!props.readonly && dirty.value) {
    confirmDiscard.value = true
    return
  }
  close()
}

function close() {
  confirmDiscard.value = false
  emit('update:modelValue', false)
}

watch(() => [props.modelValue, props.agentId] as const, ([open]) => {
  if (open) load()
}, { immediate: true })

watch(tab, v => {
  if (v === 'revisions') loadRevisions()
})

defineExpose({ draft, errors, save, validate, restoreRevision, loadRevisions, revisions, selectedRevision, dirty, requestClose, confirmDiscard, tab })
</script>

<style scoped>
.editor-header {
  padding: 16px 24px;
}
.header-icon {
  display: flex;
  align-items: center;
  justify-content: center;
  width: 36px;
  height: 36px;
  border-radius: 10px;
  background: rgba(var(--v-theme-primary), 0.1);
  flex-shrink: 0;
}
.editor-title {
  font-size: 1.125rem;
  font-weight: 600;
  line-height: 1.35;
  letter-spacing: -0.01em;
}
.editor-body {
  min-height: 420px;
  padding: 20px 24px 24px;
}
.editor-body :deep(.v-field__input) {
  font-size: 0.875rem;
  line-height: 1.5;
}
.editor-body :deep(.v-label) {
  font-size: 0.875rem;
}
.editor-body :deep(.v-input__details) {
  padding-top: 5px;
}
.editor-body :deep(.v-messages__message) {
  font-size: 0.75rem;
  line-height: 1.45;
}
.field-row {
  row-gap: 4px;
}
.field-label {
  font-size: 0.875rem;
  font-weight: 600;
  line-height: 1.4;
  color: rgba(var(--v-theme-on-surface), 0.87);
}
.field-hint {
  font-size: 0.75rem;
  line-height: 1.45;
  color: rgba(var(--v-theme-on-surface), 0.62);
}
.form-section {
  margin-top: 22px;
  padding-top: 20px;
  border-top: 1px solid rgba(var(--v-border-color), var(--v-border-opacity));
}
.section-title {
  display: flex;
  align-items: center;
  font-weight: 600;
  font-size: 0.9375rem;
  line-height: 1.4;
  margin-bottom: 4px;
}
.audience-status-row {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 20px;
  flex-wrap: wrap;
}
.audience-col {
  flex: 1 1 360px;
  min-width: 0;
}
.status-col {
  flex: 0 0 auto;
  min-width: 170px;
}
.segmented-toggle {
  display: inline-flex;
  flex-wrap: wrap;
  height: auto !important;
  min-height: 42px;
  padding: 4px;
  gap: 4px;
  border: 1px solid rgba(var(--v-border-color), var(--v-border-opacity));
  border-radius: 10px !important;
  background: rgba(var(--v-theme-on-surface), 0.035);
  overflow: visible;
  max-width: 100%;
}
.segmented-toggle--compact {
  min-height: 36px;
  padding: 3px;
  gap: 3px;
  border-radius: 8px !important;
}
.segmented-toggle :deep(.segmented-btn) {
  height: 32px !important;
  min-width: 0;
  padding: 0 14px !important;
  border: 1px solid transparent !important;
  border-radius: 7px !important;
  font-size: 0.875rem;
  font-weight: 500;
  line-height: 1.2;
  letter-spacing: normal;
  text-transform: none;
  color: rgba(var(--v-theme-on-surface), 0.72);
  background: transparent;
  transition: background-color 0.15s ease, color 0.15s ease, border-color 0.15s ease, box-shadow 0.15s ease;
}
.segmented-toggle--compact :deep(.segmented-btn) {
  height: 28px !important;
  padding: 0 10px !important;
  border-radius: 6px !important;
  font-size: 0.8125rem;
}
.segmented-toggle :deep(.segmented-btn:hover:not(.segmented-btn--active)) {
  color: rgba(var(--v-theme-on-surface), 0.92);
  background: rgba(var(--v-theme-on-surface), 0.04);
}
.segmented-toggle :deep(.segmented-btn--active) {
  color: rgb(var(--v-theme-primary)) !important;
  font-weight: 600;
  background: rgb(var(--v-theme-surface)) !important;
  border-color: rgba(var(--v-theme-primary), 0.28) !important;
  box-shadow: 0 1px 3px rgba(0, 0, 0, 0.08);
}
.segmented-toggle :deep(.segmented-btn--active .v-btn__overlay) {
  opacity: 0 !important;
}
.status-pill {
  display: inline-flex;
  align-items: center;
  min-height: 42px;
  padding: 0 14px;
  border: 1px solid rgba(var(--v-border-color), var(--v-border-opacity));
  border-radius: 10px;
  background: rgba(var(--v-theme-on-surface), 0.025);
  transition: border-color 0.15s ease, background-color 0.15s ease;
}
.status-pill--active {
  border-color: rgba(var(--v-theme-primary), 0.28);
  background: rgba(var(--v-theme-primary), 0.04);
}
.status-pill :deep(.v-label) {
  font-size: 0.875rem;
  font-weight: 500;
  opacity: 1;
}
.prompt-counter {
  font-size: 0.75rem;
  font-weight: 500;
  line-height: 1.4;
  padding: 2px 8px;
  border-radius: 6px;
  background: rgba(var(--v-theme-on-surface), 0.05);
  color: rgba(var(--v-theme-on-surface), 0.68);
}
.font-mono-field :deep(textarea),
.font-mono-field :deep(input) {
  font-family: ui-monospace, SFMono-Regular, Menlo, Consolas, monospace;
  font-size: 0.84375rem;
  line-height: 1.5;
}
.prompt-field :deep(textarea) {
  font-size: 0.875rem;
  line-height: 1.6;
}
.tool-group-card {
  border-radius: 10px;
}
.tool-checkbox {
  margin-top: 2px;
}
.tool-label {
  display: inline-flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 4px 6px;
  font-size: 0.875rem;
  line-height: 1.4;
}
.tool-code {
  font-size: 0.75rem;
  padding: 1px 6px;
  border-radius: 4px;
  background: rgba(var(--v-theme-on-surface), 0.06);
  color: rgba(var(--v-theme-on-surface), 0.7);
}
.tabular {
  font-variant-numeric: tabular-nums;
}
.editor-footer {
  padding: 14px 24px;
}
.footer-btn {
  min-width: 92px;
}
.revision-list {
  max-height: 460px;
  overflow-y: auto;
  border: 1px solid rgba(var(--v-border-color), var(--v-border-opacity));
  border-radius: 8px;
}
.prompt-view {
  max-height: 460px;
  overflow: auto;
  white-space: pre-wrap;
  word-break: break-word;
  font-size: 0.85rem;
  line-height: 1.5;
  padding: 12px;
  border-radius: 8px;
  background: rgba(var(--v-theme-on-surface), 0.04);
}
.diff-add {
  background: rgba(var(--v-theme-success), 0.14);
}
.diff-del {
  background: rgba(var(--v-theme-error), 0.12);
  text-decoration: line-through;
  text-decoration-color: rgba(var(--v-theme-error), 0.5);
}
.diff-gap {
  color: rgba(var(--v-theme-on-surface), 0.55);
  font-style: italic;
  padding: 2px 0;
}
@media (max-width: 600px) {
  .editor-header {
    padding: 14px 16px;
  }
  .editor-body {
    padding: 16px;
  }
  .editor-footer {
    padding: 12px 16px;
  }
  .status-col {
    width: 100%;
  }
}
</style>
