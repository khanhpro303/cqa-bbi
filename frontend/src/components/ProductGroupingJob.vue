<template>
  <div>
    <div class="d-flex align-center mb-4">
      <v-btn icon="mdi-arrow-left" variant="text" :to="`/${tenantId}/jobs`" />
      <h1 class="text-h5 font-weight-bold ml-2">{{ job.name }}</h1>
    </div>
    <p class="text-medium-emphasis mb-4">{{ t('sq_grouping_task_description') }}</p>
    <v-card>
      <v-tabs model-value="prompt" color="primary"><v-tab value="prompt">{{ t('sq_system_prompt') }}</v-tab></v-tabs>
      <v-divider />
      <v-card-text>
        <v-alert v-if="error" type="error" variant="tonal" class="mb-4">{{ error }}</v-alert>
        <v-alert v-if="saved" type="success" variant="tonal" class="mb-4">{{ t('sq_prompt_saved') }}</v-alert>
        <p class="mb-4 text-body-2">{{ t('sq_prompt_hint') }}</p>
        <v-textarea v-model="prompt" :label="t('sq_system_prompt')" :readonly="!isAdmin" :disabled="saving" variant="outlined" rows="20" auto-grow class="system-prompt" hide-details />
        <p v-if="!isAdmin" class="text-caption text-medium-emphasis mt-3">{{ t('sq_prompt_admin_only') }}</p>
        <v-btn v-if="isAdmin" color="primary" class="mt-4" :loading="saving" :disabled="!prompt.trim() || prompt === job.system_prompt" @click="save">{{ t('sq_save_prompt') }}</v-btn>
      </v-card-text>
    </v-card>
  </div>
</template>

<script setup lang="ts">
import { computed, nextTick, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useAuthStore } from '../stores/auth'
import api from '../api'

const props = defineProps<{ job: Record<string, any>; tenantId: string }>()
const emit = defineEmits<{ updated: [job: Record<string, any>] }>()
const { t } = useI18n()
const authStore = useAuthStore()
const isAdmin = computed(() => ['owner', 'admin'].includes(authStore.tenantPerms.role))
const prompt = ref('')
const saving = ref(false)
const saved = ref(false)
const error = ref('')
watch(() => [props.tenantId, props.job.id, props.job.system_prompt], () => {
  prompt.value = props.job.system_prompt || ''
  saved.value = false
  error.value = ''
}, { immediate: true })
watch(prompt, () => { saved.value = false })
async function save() {
  if (!isAdmin.value || saving.value || !prompt.value.trim()) return
  const tenant = props.tenantId
  const jobId = props.job.id
  saving.value = true
  error.value = ''
  saved.value = false
  try {
    const { data } = await api.put(`/tenants/${tenant}/jobs/${jobId}/product-grouping-prompt`, { system_prompt: prompt.value })
    if (tenant !== props.tenantId || jobId !== props.job.id) return
    emit('updated', data)
    await nextTick()
    saved.value = true
  } catch (err: any) {
    if (tenant === props.tenantId && jobId === props.job.id) error.value = err?.response?.data?.error || t('sq_prompt_save_error')
  } finally { saving.value = false }
}
</script>

<style scoped>
.system-prompt :deep(textarea) { font-family: ui-monospace, monospace; font-size: 13px; line-height: 1.6; }
</style>
