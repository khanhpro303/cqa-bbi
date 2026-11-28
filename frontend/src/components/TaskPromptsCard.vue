<template>
  <v-card class="pa-6 mb-6" data-testid="task-prompts">
    <div class="text-subtitle-1 font-weight-bold mb-1">
      <v-icon start size="small">mdi-script-text-outline</v-icon>
      {{ $t('task_prompts_title') }}
    </div>
    <div class="text-body-2 text-medium-emphasis mb-4">{{ $t('task_prompts_desc') }}</div>

    <v-alert v-if="loadError" type="error" variant="tonal" density="compact" class="mb-3">{{ loadError }}</v-alert>

    <v-text-field
      v-model="search"
      :label="$t('task_prompt_search')"
      prepend-inner-icon="mdi-magnify"
      density="compact"
      variant="outlined"
      hide-details
      clearable
      class="mb-3"
      data-testid="task-prompt-search"
    />

    <v-expansion-panels v-model="openGroups" multiple variant="accordion">
      <v-expansion-panel v-for="group in groups" :key="group.key" :value="group.key" :data-testid="`task-prompt-group-${group.key}`">
        <v-expansion-panel-title>
          <span class="text-body-2 font-weight-medium">{{ $t(group.label) }}</span>
          <v-chip size="x-small" variant="tonal" class="ml-2">{{ group.items.length }}</v-chip>
          <v-chip v-if="group.customized" size="x-small" color="primary" variant="tonal" class="ml-1">
            {{ group.customized }} {{ $t('task_prompt_customized') }}
          </v-chip>
        </v-expansion-panel-title>
        <v-expansion-panel-text>
          <v-list density="compact" class="py-0 task-prompt-list">
            <v-list-item
              v-for="p in group.items"
              :key="p.key"
              :data-testid="`task-prompt-${p.key}`"
              @click="openEditor(p)"
            >
              <v-list-item-title class="d-flex align-center ga-2">
                {{ p.title }}
                <v-chip size="x-small" :color="p.customized ? 'primary' : 'grey'" variant="tonal">
                  {{ p.customized ? $t('task_prompt_customized') : $t('task_prompt_default') }}
                </v-chip>
              </v-list-item-title>
              <v-list-item-subtitle class="task-prompt-desc">{{ p.description }}</v-list-item-subtitle>
              <template #append>
                <v-icon size="small">mdi-pencil-outline</v-icon>
              </template>
            </v-list-item>
          </v-list>
        </v-expansion-panel-text>
      </v-expansion-panel>
    </v-expansion-panels>
    <div v-if="search && !groups.length" class="text-body-2 text-medium-emphasis mt-2">{{ $t('task_prompt_search_empty') }}</div>

    <v-dialog v-model="editorOpen" max-width="900px" persistent>
      <v-card v-if="editing">
        <v-card-title class="d-flex align-center">
          <v-icon start>mdi-script-text-outline</v-icon>
          {{ editing.title }}
          <v-spacer />
          <v-btn icon="mdi-close" variant="text" size="small" @click="editorOpen = false" />
        </v-card-title>
        <v-card-text>
          <div class="text-body-2 text-medium-emphasis mb-3">{{ editing.description }}</div>
          <div v-if="editing.required?.length" class="mb-3" data-testid="task-prompt-required">
            <span class="text-caption font-weight-medium mr-1">{{ $t('task_prompt_required') }}</span>
            <v-chip
              v-for="r in editing.required"
              :key="r"
              size="small"
              :color="draft.includes(r) ? 'success' : 'error'"
              variant="tonal"
              class="mr-1 required-marker"
            >
              <code>{{ r }}</code>
            </v-chip>
            <div class="text-caption text-medium-emphasis mt-1">{{ isMessage ? $t('task_prompt_message_required_hint') : $t('task_prompt_required_hint') }}</div>
          </div>
          <div v-if="isMessage && editing.placeholders?.length" class="mb-3" data-testid="task-prompt-placeholders">
            <div class="text-caption font-weight-medium mb-1">{{ $t('task_prompt_placeholders') }}</div>
            <div v-for="ph in editing.placeholders" :key="ph.name" class="d-flex align-center ga-2 mb-1">
              <v-chip size="small" variant="tonal" color="primary" class="placeholder-chip" @click="insertPlaceholder(ph.name)">
                <code>{{ '{' + ph.name + '}' }}</code>
              </v-chip>
              <span class="text-caption">{{ ph.description }}</span>
            </div>
            <div class="text-caption text-medium-emphasis">{{ $t('task_prompt_placeholders_hint') }}</div>
          </div>
          <v-textarea
            ref="textareaRef"
            v-model="draft"
            :label="isMessage ? $t('task_prompt_message_text') : $t('task_prompt_instructions')"
            :rows="isMessage ? 8 : 16"
            auto-grow
            variant="outlined"
            :class="isMessage ? 'message-textarea' : 'prompt-textarea'"
            data-testid="task-prompt-textarea"
          />
          <template v-if="isMessage">
            <div v-if="unknownPlaceholders.length" class="text-caption text-warning mb-2" data-testid="task-prompt-unknown">
              {{ $t('task_prompt_unknown_placeholders', { names: unknownPlaceholders.join(', ') }) }}
            </div>
            <div class="d-flex align-center mb-1">
              <span class="text-caption font-weight-medium">{{ $t('task_prompt_preview') }}</span>
              <v-spacer />
              <v-switch
                v-if="lineConditions.length"
                v-model="previewConditions"
                :label="$t('task_prompt_preview_condition')"
                density="compact"
                hide-details
                color="primary"
                data-testid="task-prompt-preview-switch"
              />
            </div>
            <pre class="message-preview pa-3 rounded" data-testid="task-prompt-preview">{{ preview }}</pre>
          </template>
          <template v-if="editing.format">
            <div class="text-caption font-weight-medium mb-1">{{ $t('task_prompt_fixed_format') }}</div>
            <pre class="fixed-format pa-3 rounded">{{ editing.format }}</pre>
            <div class="text-caption text-medium-emphasis mt-1">{{ $t('task_prompt_fixed_format_hint') }}</div>
          </template>
        </v-card-text>
        <v-card-actions>
          <v-btn variant="text" :disabled="draft === editing.default" @click="draft = editing.default">
            {{ $t('task_prompt_reset') }}
          </v-btn>
          <v-spacer />
          <v-btn variant="outlined" @click="editorOpen = false">{{ $t('cancel') }}</v-btn>
          <v-btn color="primary" variant="flat" :loading="saving" :disabled="!draft.trim() || missingRequired.length > 0" @click="saveDraft">
            {{ $t('save') }}
          </v-btn>
        </v-card-actions>
      </v-card>
    </v-dialog>

    <v-snackbar v-model="snack" :color="snackColor" timeout="2500">{{ snackText }}</v-snackbar>
  </v-card>
</template>

<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import api from '../api'

export interface TaskPlaceholder {
  name: string
  description: string
  example?: string
  // The placeholder prints nothing; its line shows only when the condition holds.
  line?: boolean
}

export interface TaskPrompt {
  key: string
  group: string
  title: string
  description: string
  default: string
  format: string
  // Markers the code reads in the bot's answer; an edit must keep them.
  required?: string[]
  // 'message': text sent to people as is, with {name} placeholders.
  kind?: string
  placeholders?: TaskPlaceholder[]
  value: string
  customized: boolean
}

const props = defineProps<{ tenantId: string }>()
const { t } = useI18n()

const prompts = ref<TaskPrompt[]>([])
const loadError = ref('')
const editorOpen = ref(false)
const editing = ref<TaskPrompt | null>(null)
const draft = ref('')
const saving = ref(false)
const snack = ref(false)
const snackText = ref('')
const snackColor = ref('success')

const groupLabels: Record<string, string> = {
  chat_memory: 'task_prompt_group_chat_memory',
  chatbot: 'task_prompt_group_chatbot',
  erp: 'task_prompt_group_erp',
  complaints: 'task_prompt_group_complaints',
  personal_zalo: 'task_prompt_group_personal_zalo',
}

const search = ref<string | null>('')
const openGroups = ref<string[]>([])

function matches(p: TaskPrompt, q: string) {
  return [p.title, p.description, p.key, p.value].some(text => text?.toLowerCase().includes(q))
}

const groups = computed(() => {
  const q = (search.value || '').trim().toLowerCase()
  const order: string[] = []
  const byGroup: Record<string, TaskPrompt[]> = {}
  for (const p of prompts.value) {
    if (q && !matches(p, q)) continue
    if (!byGroup[p.group]) {
      byGroup[p.group] = []
      order.push(p.group)
    }
    byGroup[p.group].push(p)
  }
  return order.map(key => ({
    key, label: groupLabels[key] || key, items: byGroup[key],
    customized: byGroup[key].filter(p => p.customized).length,
  }))
})

// Searching opens every group that has a match.
watch(search, (q) => {
  if ((q || '').trim()) openGroups.value = groups.value.map(g => g.key)
})

const isMessage = computed(() => editing.value?.kind === 'message')
const textareaRef = ref<any>(null)
const previewConditions = ref(false)
const lineConditions = computed(() => (editing.value?.placeholders || []).filter(p => p.line).map(p => p.name))

// Mirrors engine.FillMessageTemplate: lines are picked first, then values go
// in one pass so a value is never read as template.
function fillMessageTemplate(template: string, vars: Record<string, string>, show: Record<string, boolean>) {
  const kept: string[] = []
  for (let line of template.split('\n')) {
    let keep = true
    for (const [name, on] of Object.entries(show)) {
      const token = `{${name}}`
      if (line.includes(token)) {
        keep = keep && on
        line = line.split(token).join('')
      }
    }
    if (keep) kept.push(line)
  }
  return kept.join('\n').replace(/\{([a-z0-9_]+)\}/g, (m, name: string) => (name in vars ? vars[name] : m))
}

const preview = computed(() => {
  const vars: Record<string, string> = {}
  const show: Record<string, boolean> = {}
  for (const p of editing.value?.placeholders || []) {
    if (p.line) show[p.name] = previewConditions.value
    else vars[p.name] = p.example || ''
  }
  return fillMessageTemplate(draft.value, vars, show)
})

const unknownPlaceholders = computed(() => {
  const known = new Set((editing.value?.placeholders || []).map(p => p.name))
  const found = [...draft.value.matchAll(/\{([a-z0-9_]+)\}/g)].map(m => m[1]).filter(n => !known.has(n))
  return [...new Set(found)].map(n => `{${n}}`)
})

function insertPlaceholder(name: string) {
  const token = `{${name}}`
  const el: HTMLTextAreaElement | undefined = textareaRef.value?.$el?.querySelector?.('textarea')
  const at = el && typeof el.selectionStart === 'number' ? el.selectionStart : draft.value.length
  draft.value = draft.value.slice(0, at) + token + draft.value.slice(el?.selectionEnd ?? at)
}

const missingRequired = computed(() => (editing.value?.required || []).filter(r => !draft.value.includes(r)))

async function load() {
  if (!props.tenantId) return
  loadError.value = ''
  try {
    const { data } = await api.get(`/tenants/${props.tenantId}/settings/ai-prompts`)
    prompts.value = Array.isArray(data?.data) ? data.data : []
  } catch {
    loadError.value = t('task_prompts_load_failed')
  }
}

function openEditor(p: TaskPrompt) {
  editing.value = p
  draft.value = p.value
  previewConditions.value = false
  editorOpen.value = true
}

async function saveDraft() {
  if (!editing.value) return
  saving.value = true
  try {
    const { data } = await api.put(`/tenants/${props.tenantId}/settings/ai-prompts/${editing.value.key}`, { value: draft.value })
    const i = prompts.value.findIndex(p => p.key === data.key)
    if (i >= 0) prompts.value[i] = data
    editorOpen.value = false
    snackText.value = t('task_prompt_saved')
    snackColor.value = 'success'
  } catch (e: any) {
    snackText.value = e?.response?.data?.error === 'prompt_missing_required'
      ? t('task_prompt_missing_required', { markers: (e.response.data.missing || []).join(', ') })
      : t('task_prompt_save_failed')
    snackColor.value = 'error'
  } finally {
    saving.value = false
    snack.value = true
  }
}

onMounted(load)
watch(() => props.tenantId, load)

defineExpose({ prompts, openEditor, saveDraft, draft, editing, editorOpen, search, groups, missingRequired, openGroups,
  isMessage, preview, previewConditions, unknownPlaceholders, insertPlaceholder })
</script>

<style scoped>
.task-prompt-desc {
  white-space: normal;
  -webkit-line-clamp: unset;
}
.required-marker code {
  font-size: 12px;
}
.fixed-format {
  white-space: pre-wrap;
  font-size: 12px;
  background: rgba(128, 128, 128, 0.08);
  max-height: 260px;
  overflow: auto;
}
.placeholder-chip code {
  font-size: 12px;
}
.message-preview {
  white-space: pre-wrap;
  font-family: inherit;
  font-size: 13px;
  background: rgba(128, 128, 128, 0.08);
}
.message-textarea :deep(textarea) {
  font-size: 14px;
}
.prompt-textarea :deep(textarea) {
  font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
  font-size: 13px;
}
</style>
