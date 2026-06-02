<template>
  <div>
    <!-- Analysis Schedule -->
    <h3 class="text-h6 mb-2">{{ $t('job_wizard_step_analysis_schedule') }}</h3>
    <p class="text-body-2 text-grey mb-4">{{ $t('analysis_schedule_desc') }}</p>

    <v-radio-group v-model="form.schedule_type">
      <v-radio value="cron" :label="$t('schedule_cron')" />
      <v-radio value="after_sync" :label="$t('schedule_after_sync')" />
      <v-radio value="manual" :label="$t('schedule_manual')" />
    </v-radio-group>

    <CronPicker
      v-if="form.schedule_type === 'cron'"
      v-model="form.schedule_cron"
      class="mt-2 ml-8"
    />

    <template v-if="form.job_type !== 'erp_product_cache' && form.job_type !== 'erp_customer_cache' && form.job_type !== 'chatbot_toggle'">
      <v-divider class="my-6" />

      <!-- Output Schedule -->
      <h3 class="text-h6 mb-2">{{ $t('job_wizard_step_output_schedule') }}</h3>
      <p class="text-body-2 text-grey mb-4">{{ $t('output_schedule_desc') }}</p>

      <v-radio-group v-model="form.output_schedule">
        <v-radio value="none" :label="$t('output_none')" />
        <v-radio value="instant" :label="$t('output_instant')" />
        <v-radio value="cron" :label="$t('output_scheduled')" />
        <v-radio value="scheduled" :label="$t('output_once')" />
      </v-radio-group>

      <CronPicker
        v-if="form.output_schedule === 'cron'"
        v-model="form.output_cron"
        class="mt-2 ml-8"
      />

      <v-text-field
        v-if="form.output_schedule === 'scheduled'"
        v-model="form.output_at"
        :label="$t('send_at')"
        type="datetime-local"
        class="mt-3"
      />
    </template>

  </div>
</template>

<script setup lang="ts">
import CronPicker from '../CronPicker.vue'

const form = defineModel<Record<string, any>>('form', { required: true })
</script>
