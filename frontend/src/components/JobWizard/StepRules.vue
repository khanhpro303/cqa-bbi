<template>
  <div>
    <h3 class="text-h6 mb-2">{{ form.job_type === 'chatbot_toggle' ? 'Cấu hình Trạng thái' : $t('job_wizard_step_rules') }}</h3>
    <div class="text-body-2 text-grey-darken-1 mb-4">
      {{ form.job_type === 'qc_analysis'
        ? 'Nhập quy tắc đánh giá chất lượng. AI Agent sẽ phân tích từng cuộc hội thoại dựa trên các quy tắc này để đánh giá nhân viên. Ví dụ: Nhân viên phải chào hỏi lịch sự, trả lời đầy đủ câu hỏi khách hàng...'
        : form.job_type === 'classification'
        ? 'Cấu hình các nhãn phân loại. AI Agent sẽ tự động phân loại cuộc chat theo các nhãn bạn định nghĩa (feedback, hỏi giá, khiếu nại...).'
        : 'Chọn trạng thái của chatbot OA mà bạn muốn thiết lập khi công việc chạy.' }}
    </div>

    <!-- QC Analysis: markdown rules -->
    <div v-if="form.job_type === 'qc_analysis'">
      <v-textarea
        v-model="form.rules_content"
        :placeholder="$t('rules_placeholder')"
        rows="12"
        auto-grow
        class="font-mono"
      />
      <v-btn variant="text" size="small" color="primary" class="mt-2" @click="loadTemplate">
        <v-icon start size="small">mdi-file-document</v-icon>
        Dùng mẫu quy định CSKH
      </v-btn>

      <v-divider class="my-4" />
      <div class="text-subtitle-2 font-weight-bold mb-1">
        <v-icon start size="small" color="grey">mdi-skip-next-circle</v-icon>
        Điều kiện bỏ qua (không đánh giá)
      </div>
      <div class="text-caption text-grey-darken-1 mb-2">
        Mô tả các trường hợp cuộc chat không cần đánh giá. AI sẽ đánh dấu "Bỏ qua" thay vì Đạt/Không đạt.
      </div>
      <v-textarea
        v-model="form.skip_conditions"
        placeholder="Ví dụ: Cuộc chat không có tin nhắn từ OA, cuộc chat dưới 2 tin nhắn, khách chỉ gửi sticker hoặc file..."
        rows="3"
        auto-grow
        variant="outlined"
        density="compact"
      />
      <v-btn variant="text" size="small" color="grey-darken-1" class="mt-1" @click="loadSkipTemplate">
        <v-icon start size="small">mdi-file-document</v-icon>
        Tải mẫu điều kiện bỏ qua
      </v-btn>
    </div>

    <!-- Classification: dynamic rules -->
    <div v-else-if="form.job_type === 'classification'">
      <v-card v-for="(rule, idx) in rules" :key="idx" variant="outlined" class="pa-3 mb-3">
        <div class="d-flex align-center mb-2">
          <span class="text-subtitle-2 font-weight-bold">Rule {{ idx + 1 }}</span>
          <v-spacer />
          <v-btn icon="mdi-close" size="x-small" variant="text" @click="removeRule(idx)" />
        </div>
        <v-text-field v-model="rule.name" :label="$t('rule_name')" density="compact" class="mb-2" />
        <v-textarea v-model="rule.description" :label="$t('rule_description')" rows="2" density="compact" class="mb-2" />
        <v-select
          v-model="rule.severity"
          :label="$t('rule_severity')"
          :items="[{ title: $t('severity_critical'), value: 'NGHIEM_TRONG' }, { title: $t('severity_warning'), value: 'CAN_CAI_THIEN' }]"
          density="compact"
        />
      </v-card>
      <v-btn variant="outlined" color="primary" @click="addRule">
        <v-icon start>mdi-plus</v-icon>
        {{ $t('add_rule') }}
      </v-btn>
    </div>

    <!-- Chatbot Toggle state config -->
    <div v-else-if="form.job_type === 'chatbot_toggle'">
      <div class="text-subtitle-2 mb-2">Trạng thái chatbot mong muốn</div>
      <v-radio-group v-model="form.rules_content" class="mt-0">
        <v-radio value="true">
          <template #label>
            <div>
              <div class="font-weight-medium text-success">Bật Chatbot (ON)</div>
              <div class="text-caption text-grey">Kích hoạt chatbot tự động trả lời tin nhắn khách hàng.</div>
            </div>
          </template>
        </v-radio>
        <v-radio value="false">
          <template #label>
            <div>
              <div class="font-weight-medium text-grey">Tắt Chatbot (OFF)</div>
              <div class="text-caption text-grey">Vô hiệu hóa chatbot tự động trả lời tin nhắn khách hàng.</div>
            </div>
          </template>
        </v-radio>
      </v-radio-group>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, watch, onMounted } from 'vue'

const form = defineModel<Record<string, any>>('form', { required: true })

onMounted(() => {
  if (form.value.job_type === 'chatbot_toggle' && !form.value.rules_content) {
    form.value.rules_content = 'true'
  }
})

function parseRules() {
  try {
    const parsed = JSON.parse(form.value.rules_config || '[]')
    return Array.isArray(parsed) ? parsed : []
  } catch { return [] }
}

const rules = ref<Array<{ name: string; description: string; severity: string }>>(parseRules())

// Sync rules to form.rules_config
watch(rules, (val) => {
  form.value.rules_config = JSON.stringify(val)
}, { deep: true })

function addRule() {
  rules.value.push({ name: '', description: '', severity: 'CAN_CAI_THIEN' })
}

function removeRule(idx: number) {
  rules.value.splice(idx, 1)
}

const defaultTemplate = `# Quy định chất lượng CSKH

## 1. Thời gian phản hồi
- Phải trả lời khách trong vòng 5 phút
- Nghiêm trọng nếu không phản hồi sau 15 phút

## 2. Thái độ giao tiếp
- Luôn chào hỏi lịch sự
- Không dùng từ ngữ thô tục
- Phải xin lỗi khi khách phản ánh vấn đề

## 3. Chất lượng nội dung
- Trả lời đúng trọng tâm câu hỏi
- Cung cấp thông tin chính xác
- Hướng dẫn cụ thể, rõ ràng

## 4. Kết thúc hội thoại
- Hỏi khách còn cần hỗ trợ gì không
- Cảm ơn khách đã liên hệ`

function loadTemplate() {
  form.value.rules_content = defaultTemplate
}

const defaultSkipTemplate = `- Cuộc chat dưới 2 tin nhắn
- Khách chỉ gửi sticker hoặc hình ảnh mà không có nội dung text
- Cuộc chat chỉ có tin nhắn tự động từ hệ thống`

function loadSkipTemplate() {
  form.value.skip_conditions = defaultSkipTemplate
}
</script>
