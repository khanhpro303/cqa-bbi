<template>
  <div>
    <div class="d-flex align-center mb-6">
      <v-btn icon="mdi-arrow-left" variant="text" :to="`/${tenantId}/jobs/${jobId}`" />
      <h1 class="text-h5 font-weight-bold ml-2">{{ $t('edit_job') }}</h1>
    </div>

    <div v-if="loading" class="text-center py-8">
      <v-progress-circular indeterminate />
    </div>

    <template v-else-if="form">
      <v-expansion-panels v-model="openPanels" multiple>
        <!-- Basic Info -->
        <v-expansion-panel value="info">
          <v-expansion-panel-title>
            <v-icon start size="small">mdi-information</v-icon>
            {{ $t('job_info') }}
          </v-expansion-panel-title>
          <v-expansion-panel-text>
            <StepType v-model:form="form" />
          </v-expansion-panel-text>
        </v-expansion-panel>

        <!-- Input Channels -->
        <v-expansion-panel value="input" v-if="form.job_type !== 'chatbot_toggle' && form.job_type !== 'erp_product_cache' && form.job_type !== 'erp_customer_cache'">
          <v-expansion-panel-title>
            <v-icon start size="small">mdi-chat</v-icon>
            {{ $t('job_wizard_step_input') }}
            <v-chip size="x-small" variant="tonal" class="ml-2">{{ form.input_channel_ids?.length || 0 }}</v-chip>
          </v-expansion-panel-title>
          <v-expansion-panel-text>
            <StepInput v-model:form="form" />
          </v-expansion-panel-text>
        </v-expansion-panel>

        <!-- Chatbot target channels -->
        <v-expansion-panel value="channels" v-if="form.job_type === 'chatbot_toggle'">
          <v-expansion-panel-title>
            <v-icon start size="small">mdi-chat</v-icon>
            {{ $t('jobs_chatbot_toggle_select_channels') }}
          </v-expansion-panel-title>
          <v-expansion-panel-text>
            <StepChatbotChannels v-model:form="form" />
          </v-expansion-panel-text>
        </v-expansion-panel>

        <!-- Rules -->
        <v-expansion-panel value="rules" v-if="form.job_type !== 'erp_product_cache' && form.job_type !== 'erp_customer_cache'">
          <v-expansion-panel-title>
            <v-icon start size="small">mdi-robot</v-icon>
            {{ $t('job_wizard_step_rules') }}
          </v-expansion-panel-title>
          <v-expansion-panel-text>
            <StepRules v-model:form="form" />
          </v-expansion-panel-text>
        </v-expansion-panel>

        <!-- Output -->
        <v-expansion-panel value="output" v-if="form.job_type !== 'chatbot_toggle' && form.job_type !== 'erp_product_cache' && form.job_type !== 'erp_customer_cache'">
          <v-expansion-panel-title>
            <v-icon start size="small">mdi-send</v-icon>
            {{ $t('job_wizard_step_output') }}
            <v-chip size="x-small" variant="tonal" class="ml-2">{{ outputCount }}</v-chip>
          </v-expansion-panel-title>
          <v-expansion-panel-text>
            <StepOutput v-model:form="form" />
          </v-expansion-panel-text>
        </v-expansion-panel>

        <!-- Schedule -->
        <v-expansion-panel value="schedule">
          <v-expansion-panel-title>
            <v-icon start size="small">mdi-clock</v-icon>
            {{ $t('job_wizard_step_schedule') }}
          </v-expansion-panel-title>
          <v-expansion-panel-text>
            <StepOutputSchedule v-model:form="form" />
          </v-expansion-panel-text>
        </v-expansion-panel>
      </v-expansion-panels>

      <div class="d-flex justify-end mt-6">
        <v-btn variant="text" :to="`/${tenantId}/jobs/${jobId}`" class="mr-2">
          {{ $t('cancel') }}
        </v-btn>
        <v-btn color="primary" :loading="saving" @click="saveJob">
          <v-icon start>mdi-content-save</v-icon>
          {{ $t('save_job') }}
        </v-btn>
      </div>

      <v-snackbar v-model="showSuccess" color="success" :timeout="2000">
        {{ $t('job_updated') }}
      </v-snackbar>
    </template>
  </div>
</template>

<script setup lang="ts">
import { ref, computed, onMounted } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { useJobStore } from '../../stores/jobs'
import StepType from '../../components/JobWizard/StepType.vue'
import StepInput from '../../components/JobWizard/StepInput.vue'
import StepChatbotChannels from '../../components/JobWizard/StepChatbotChannels.vue'
import StepRules from '../../components/JobWizard/StepRules.vue'
import StepOutput from '../../components/JobWizard/StepOutput.vue'
import StepOutputSchedule from '../../components/JobWizard/StepOutputSchedule.vue'

const route = useRoute()
const router = useRouter()
const jobStore = useJobStore()

const tenantId = computed(() => route.params.tenantId as string)
const jobId = computed(() => route.params.jobId as string)

const loading = ref(true)
const saving = ref(false)
const showSuccess = ref(false)
const openPanels = ref(['info'])

const form = ref<Record<string, any>>({
  name: '',
  description: '',
  job_type: 'qc_analysis',
  input_channel_ids: [] as string[],
  rules_content: '',
  rules_config: '[]',
  skip_conditions: '',
  ai_provider: 'claude',
  ai_model: '',
  outputs: '[]',
  output_schedule: 'instant',
  output_cron: '',
  output_at: '',
  schedule_type: 'cron',
  schedule_cron: '0 7 * * *',
})

const outputCount = computed(() => {
  try {
    return JSON.parse(form.value.outputs || '[]').length
  } catch { return 0 }
})

onMounted(async () => {
  try {
    const job = await jobStore.fetchJob(tenantId.value, jobId.value)
    // Map job data to form
    form.value = {
      name: job.name || '',
      description: job.description || '',
      job_type: job.job_type || 'qc_analysis',
      input_channel_ids: JSON.parse(job.input_channel_ids || '[]'),
      rules_content: job.rules_content || '',
      rules_config: job.rules_config || '[]',
      skip_conditions: job.skip_conditions || '',
      ai_provider: job.ai_provider || 'claude',
      ai_model: job.ai_model || '',
      outputs: job.outputs || '[]',
      output_schedule: job.output_schedule || 'instant',
      output_cron: job.output_cron || '',
      output_at: job.output_at || '',
      schedule_type: job.schedule_type || 'cron',
      schedule_cron: job.schedule_cron || '0 7 * * *',
    }
  } finally {
    loading.value = false
  }
})

async function saveJob() {
  saving.value = true
  try {
    const isErpCache = form.value.job_type === 'erp_product_cache' || form.value.job_type === 'erp_customer_cache'
    const isSpecialJob = form.value.job_type === 'chatbot_toggle' || isErpCache
    const selectedChannelIds = Array.isArray(form.value.input_channel_ids)
      ? form.value.input_channel_ids
      : JSON.parse(form.value.input_channel_ids || '[]')
    await jobStore.updateJob(tenantId.value, jobId.value, {
      name: form.value.name,
      description: form.value.description,
      rules_content: form.value.rules_content,
      rules_config: isSpecialJob ? '[]' : form.value.rules_config,
      skip_conditions: isSpecialJob ? '' : form.value.skip_conditions,
      // ERP cache is tenant-wide (['global']); chatbot_toggle uses its per-channel
      // selection (which may itself be ['global'] = all OA channels).
      input_channel_ids: isErpCache ? ['global'] : selectedChannelIds,
      outputs: isSpecialJob
        ? []
        : (typeof form.value.outputs === 'string'
          ? JSON.parse(form.value.outputs || '[]')
          : form.value.outputs || []),
      output_schedule: isSpecialJob ? 'none' : form.value.output_schedule,
      output_cron: isSpecialJob ? '' : form.value.output_cron,
      output_at: isSpecialJob ? null : (form.value.output_at || null),
      schedule_type: form.value.schedule_type,
      schedule_cron: form.value.schedule_cron,
    })
    showSuccess.value = true
    setTimeout(() => router.push(`/${tenantId.value}/jobs/${jobId.value}`), 1500)
  } catch (err) {
    console.error('Update job failed:', err)
  } finally {
    saving.value = false
  }
}
</script>
