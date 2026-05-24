<template>
  <div>
    <div class="text-body-2 text-grey-darken-1 mb-4">
      {{ $t('jobs_step_type_intro') }}
    </div>
    <v-text-field
      v-model="form.name"
      :label="$t('job_name')"
      :rules="[v => !!v || $t('validation_required'), v => (v && v.length >= 2) || $t('validation_min_chars', { min: 2 })]"
      class="mb-3"
    />
    <v-textarea v-model="form.description" :label="$t('job_description')" rows="2" class="mb-4" />

    <div class="text-subtitle-2 mb-2">{{ $t('jobs_job_type_label') }}</div>
    <v-radio-group v-model="form.job_type" class="mt-0">
      <v-radio value="qc_analysis">
        <template #label>
          <div>
            <div class="font-weight-medium">{{ $t('jobs_type_qc_title') }}</div>
            <div class="text-caption text-grey">{{ $t('jobs_type_qc_caption') }}</div>
          </div>
        </template>
      </v-radio>
      <v-radio value="classification">
        <template #label>
          <div>
            <div class="font-weight-medium">{{ $t('jobs_type_classification_title') }}</div>
            <div class="text-caption text-grey">{{ $t('jobs_type_classification_caption') }}</div>
          </div>
        </template>
      </v-radio>
      <v-radio value="chatbot_toggle">
        <template #label>
          <div>
            <div class="font-weight-medium">{{ $t('jobs_type_chatbot_toggle_title') }}</div>
            <div class="text-caption text-grey">{{ $t('jobs_type_chatbot_toggle_caption') }}</div>
          </div>
        </template>
      </v-radio>
      <v-radio value="erp_product_cache">
        <template #label>
          <div>
            <div class="font-weight-medium">{{ $t('jobs_type_erp_product_cache_title') }}</div>
            <div class="text-caption text-grey">{{ $t('jobs_type_erp_product_cache_caption') }}</div>
          </div>
        </template>
      </v-radio>
    </v-radio-group>
  </div>
</template>

<script setup lang="ts">
const form = defineModel<Record<string, any>>('form', { required: true })
</script>
