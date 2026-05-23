<template>
  <div>
    <div class="text-body-2 text-grey-darken-1 mb-4">
      Đặt tên và chọn loại công việc phân tích.
    </div>
    <v-text-field
      v-model="form.name"
      :label="$t('job_name')"
      :rules="[v => !!v || $t('validation_required'), v => (v && v.length >= 2) || $t('validation_min_chars', { min: 2 })]"
      class="mb-3"
    />
    <v-textarea v-model="form.description" :label="$t('job_description')" rows="2" class="mb-4" />

    <div class="text-subtitle-2 mb-2">Loại công việc</div>
    <v-radio-group v-model="form.job_type" class="mt-0">
      <v-radio value="qc_analysis">
        <template #label>
          <div>
            <div class="font-weight-medium">Phân tích chất lượng CSKH</div>
            <div class="text-caption text-grey">Đánh giá chất lượng trả lời của nhân viên, phát hiện vi phạm quy tắc.</div>
          </div>
        </template>
      </v-radio>
      <v-radio value="classification">
        <template #label>
          <div>
            <div class="font-weight-medium">Phân loại chat</div>
            <div class="text-caption text-grey">Phân loại cuộc chat theo chủ đề (feedback, hỏi giá, khiếu nại...). Phù hợp gom phản hồi khách hàng.</div>
          </div>
        </template>
      </v-radio>
      <v-radio value="chatbot_toggle">
        <template #label>
          <div>
            <div class="font-weight-medium">Bật/Tắt Chatbot tự động</div>
            <div class="text-caption text-grey">Tự động bật hoặc tắt chatbot OA theo khung giờ cấu hình (cron job).</div>
          </div>
        </template>
      </v-radio>
    </v-radio-group>
  </div>
</template>

<script setup lang="ts">
const form = defineModel<Record<string, any>>('form', { required: true })
</script>
