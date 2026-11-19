<template>
  <v-card class="pa-6 mb-6" data-testid="task-prompts">
    <div class="text-subtitle-1 font-weight-bold mb-1">
      <v-icon start size="small">mdi-script-text-outline</v-icon>
      {{ $t('task_prompts_title') }}
    </div>
    <div class="text-body-2 text-medium-emphasis mb-4">{{ $t('task_prompts_desc') }}</div>

    <v-alert v-if="loadError" type="error" variant="tonal" density="compact" class="mb-3">{{ loadError }}</v-alert>

    <div v-for="group in groups" :key="group.key" class="mb-4">
      <div class="text-body-2 font-weight-medium mb-2">{{ $t(group.label) }}</div>
      <v-list density="compact" class="py-0 task-prompt-list" border rounded>
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
    </div>

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
          <v-textarea
            v-model="draft"
            :label="$t('task_prompt_instructions')"
            rows="16"
            auto-grow
            variant="outlined"
            class="prompt-textarea"
            data-testid="task-prompt-textarea"
          />
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
          <v-btn color="primary" variant="flat" :loading="saving" :disabled="!draft.trim()" @click="saveDraft">
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

export interface TaskPrompt {
  key: string
  group: string
  title: string
  description: string
  default: string
  format: string
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
}

const groups = computed(() => {
  const order: string[] = []
  const byGroup: Record<string, TaskPrompt[]> = {}
  for (const p of prompts.value) {
    if (!byGroup[p.group]) {
      byGroup[p.group] = []
      order.push(p.group)
    }
    byGroup[p.group].push(p)
  }
  return order.map(key => ({ key, label: groupLabels[key] || key, items: byGroup[key] }))
})

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
  } catch {
    snackText.value = t('task_prompt_save_failed')
    snackColor.value = 'error'
  } finally {
    saving.value = false
    snack.value = true
  }
}

onMounted(load)
watch(() => props.tenantId, load)

defineExpose({ prompts, openEditor, saveDraft, draft, editing, editorOpen })
</script>

<style scoped>
.task-prompt-desc {
  white-space: normal;
  -webkit-line-clamp: unset;
}
.fixed-format {
  white-space: pre-wrap;
  font-size: 12px;
  background: rgba(128, 128, 128, 0.08);
  max-height: 260px;
  overflow: auto;
}
.prompt-textarea :deep(textarea) {
  font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
  font-size: 13px;
}
</style>
