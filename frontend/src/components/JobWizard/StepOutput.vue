<template>
  <div>
    <h3 class="text-h6 mb-2">{{ $t('job_wizard_step_output') }}</h3>
    <div class="text-body-2 text-grey-darken-1 mb-4">Cấu hình nơi nhận kết quả phân tích. Bỏ qua nếu chỉ muốn xem trên hệ thống.</div>

    <v-switch
      v-model="notifyOnError"
      label="Gửi thông báo khi tác vụ chạy lỗi"
      color="primary"
      density="compact"
      hide-details
      class="mb-2"
    />
    <div class="text-caption text-grey mb-4">Khi tác vụ chạy lỗi, gửi cảnh báo tới tất cả đầu ra đã cấu hình bên dưới (Telegram / Email / Zalo).</div>

    <v-card v-for="(output, idx) in outputs" :key="idx" variant="outlined" class="pa-4 mb-3">
      <div class="d-flex align-center mb-3">
        <v-chip :color="outputTypeColor(output.type)" variant="tonal" size="small">
          {{ outputTypeLabel(output.type) }}
        </v-chip>
        <v-chip v-if="testPassed[idx]" color="success" variant="tonal" size="x-small" class="ml-2">
          <v-icon start size="x-small">mdi-check</v-icon>
          Đã test
        </v-chip>
        <v-spacer />
        <v-btn icon="mdi-close" size="x-small" variant="text" @click="removeOutput(idx)" />
      </div>

      <v-select
        v-model="output.type"
        :items="[{ title: $t('output_telegram'), value: 'telegram' }, { title: $t('output_email'), value: 'email' }, { title: 'Zalo (nhóm GMF)', value: 'zalo' }]"
        label="Type"
        density="compact"
        class="mb-2"
        @update:model-value="resetTest(idx)"
      />

      <!-- Telegram -->
      <template v-if="output.type === 'telegram'">
        <v-text-field
          v-model="output.bot_token"
          :label="$t('bot_token')"
          :rules="[v => !!v || 'Bot Token là bắt buộc']"
          density="compact"
          class="mb-2"
          hint="Token của Telegram Bot (lấy từ @BotFather)"
          persistent-hint
          @update:model-value="resetTest(idx)"
        />
        <v-text-field
          v-model="output.chat_id"
          label="Group ID"
          :rules="[v => !!v || 'Group ID là bắt buộc']"
          density="compact"
          hint="Thêm bot @RawDataBot vào group Telegram, bot sẽ gửi lại Group ID (số âm, ví dụ: -1001234567890)"
          persistent-hint
          @update:model-value="resetTest(idx)"
        />
      </template>

      <!-- Zalo (nhóm GMF) -->
      <template v-else-if="output.type === 'zalo'">
        <v-select
          v-model="output.channel_id"
          :items="zaloChannelOptions"
          :loading="loadingZaloRefs"
          label="Kênh Zalo OA"
          density="compact"
          class="mb-2"
          :no-data-text="loadingZaloRefs ? 'Đang tải…' : 'Chưa có kênh Zalo OA nào'"
          hint="Kênh OA dùng để gửi kết quả vào nhóm GMF"
          persistent-hint
          @update:model-value="onZaloChannelChange(output, idx)"
        />
        <v-select
          v-model="output.group_id"
          :items="groupOptionsFor(output.channel_id)"
          :loading="loadingZaloRefs"
          :disabled="!output.channel_id"
          label="Nhóm GMF nhận kết quả"
          density="compact"
          :no-data-text="loadingZaloRefs ? 'Đang tải…' : 'Chưa có nhóm GMF nào đã liên kết Zalo'"
          hint="Kết quả phân tích sẽ gửi vào nhóm này"
          persistent-hint
          @update:model-value="resetTest(idx)"
        />
      </template>

      <!-- Email -->
      <template v-else>
        <v-row dense>
          <v-col cols="8"><v-text-field v-model="output.smtp_host" :label="$t('smtp_host')" :rules="[v => !!v || 'Bắt buộc']" density="compact" @update:model-value="resetTest(idx)" /></v-col>
          <v-col cols="4"><v-text-field v-model="output.smtp_port" :label="$t('smtp_port')" type="number" :rules="[v => !!v || 'Bắt buộc']" density="compact" @update:model-value="resetTest(idx)" /></v-col>
        </v-row>
        <v-row dense>
          <v-col cols="6"><v-text-field v-model="output.smtp_user" :label="$t('smtp_user')" density="compact" /></v-col>
          <v-col cols="6"><v-text-field v-model="output.smtp_pass" :label="$t('smtp_pass')" type="password" density="compact" /></v-col>
        </v-row>
        <v-text-field v-model="output.from" :label="$t('email_from')" :rules="[v => !!v || 'Bắt buộc']" density="compact" class="mb-2" @update:model-value="resetTest(idx)" />
        <v-text-field v-model="output.to" :label="$t('email_to')" :rules="[v => !!v || 'Bắt buộc']" density="compact" hint="Nhiều email cách nhau bằng dấu phẩy" persistent-hint @update:model-value="resetTest(idx)" />
      </template>

      <!-- Template -->
      <v-divider class="my-3" />
      <v-select
        v-model="output.template"
        :items="[{ title: 'Mặc định — Gửi tóm tắt kết quả + danh sách vấn đề', value: 'default' }, { title: 'Tùy chỉnh — Tự soạn nội dung gửi', value: 'custom' }]"
        label="Template gửi"
        density="compact"
        class="mb-2"
        @update:model-value="(val: string) => { if (val === 'custom' && !output.custom_template) output.custom_template = getDefaultTemplate(output.type) }"
      />
      <template v-if="output.template === 'custom'">
        <v-textarea
          v-model="output.custom_template"
          label="Nội dung template tùy chỉnh"
          rows="6"
          density="compact"
          class="font-mono mb-2"
          style="font-size: 13px;"
        />
        <v-alert type="info" variant="tonal" density="compact" class="mb-2">
          <div class="text-caption" style="white-space: pre-line;">{{ templateHelpText }}</div>
        </v-alert>
        <v-btn variant="text" size="x-small" color="primary" @click="output.custom_template = getDefaultTemplate(output.type)">
          <v-icon start size="small">mdi-restore</v-icon>
          Khôi phục mặc định
        </v-btn>
      </template>

      <!-- Test + Status -->
      <div class="d-flex align-center mt-3">
        <v-btn
          variant="outlined"
          size="small"
          color="primary"
          :loading="testingIdx === idx"
          :disabled="!isOutputValid(output)"
          @click="testSend(idx)"
        >
          <v-icon start size="small">mdi-send-check</v-icon>
          Test gửi
        </v-btn>
        <v-chip v-if="testResult && testResult.idx === idx" :color="testResult.success ? 'success' : 'error'" size="small" variant="tonal" class="ml-2">
          {{ testResult.message }}
        </v-chip>
        <div v-if="!testPassed[idx] && isOutputValid(output)" class="text-caption text-warning ml-2">
          Cần test gửi thành công trước khi tiếp tục
        </div>
      </div>
    </v-card>

    <v-btn variant="outlined" color="primary" @click="addOutput">
      <v-icon start>mdi-plus</v-icon>
      {{ $t('add_output') }}
    </v-btn>
  </div>
</template>

<script setup lang="ts">
import { ref, watch, computed, onMounted } from 'vue'
import { useRoute } from 'vue-router'
import { useI18n } from 'vue-i18n'
import api from '../../api'

const route = useRoute()
const { t } = useI18n()
const tenantId = computed(() => route.params.tenantId as string)
const form = defineModel<Record<string, any>>('form', { required: true })

// Notify-on-error toggle (defaults to true when the job has no explicit value).
const notifyOnError = computed({
  get: () => form.value.notify_on_error !== false,
  set: (v: boolean) => { form.value.notify_on_error = v },
})

interface OutputItem {
  type: string
  bot_token?: string
  chat_id?: string
  smtp_host?: string
  smtp_port?: number
  smtp_user?: string
  smtp_pass?: string
  from?: string
  to?: string
  channel_id?: string
  group_id?: string
  template?: string
  custom_template?: string
}

function outputTypeColor(type: string): string {
  if (type === 'telegram') return 'blue'
  if (type === 'zalo') return 'green'
  return 'orange'
}
function outputTypeLabel(type: string): string {
  if (type === 'telegram') return t('output_telegram')
  if (type === 'zalo') return 'Zalo (nhóm GMF)'
  return t('output_email')
}

const templateHelpText = `Biến có thể dùng:
{{job_name}} — Tên công việc | {{total}} — Tổng cuộc chat | {{passed}} — Số đạt | {{failed}} — Số không đạt | {{issues}} — Số vấn đề
{{content}} — Nội dung đánh giá chi tiết | {{link}} — Link xem trên hệ thống`

function getDefaultTemplate(outputType: string) {
  // Zalo group messages are plain text — no HTML tags.
  if (outputType === 'zalo') {
    return `Kết quả phân tích: {{job_name}}

Tổng: {{total}} cuộc | Đạt: {{passed}} | Không đạt: {{failed}} | Vấn đề: {{issues}}

{{content}}

Xem chi tiết: {{link}}`
  }
  const linkLine = outputType === 'email'
    ? `<a href="{{link}}">Xem chi tiết trên hệ thống</a>`
    : `Xem chi tiết: {{link}}`
  return `<b>Kết quả phân tích: {{job_name}}</b>

Tổng: {{total}} cuộc | Đạt: {{passed}} | Không đạt: {{failed}} | Vấn đề: {{issues}}

{{content}}

${linkLine}`
}

// Zalo reference data: OA channels + GMF groups (loaded once for the zalo output type).
const zaloChannelOptions = ref<Array<{ title: string; value: string }>>([])
const zaloGroups = ref<Array<{ id: string; name: string; zalo_group_id?: string; channel_id?: string }>>([])
const loadingZaloRefs = ref(false)

async function loadZaloRefs() {
  loadingZaloRefs.value = true
  try {
    const [chRes, grpRes] = await Promise.all([
      api.get(`/tenants/${tenantId.value}/channels`),
      api.get(`/tenants/${tenantId.value}/crm/groups`),
    ])
    const channels = Array.isArray(chRes.data) ? chRes.data : (chRes.data?.data ?? [])
    zaloChannelOptions.value = channels
      .filter((c: any) => c.channel_type === 'zalo_oa')
      .map((c: any) => ({ title: c.name, value: c.id }))
    zaloGroups.value = Array.isArray(grpRes.data) ? grpRes.data : (grpRes.data?.data ?? [])
  } catch {
    zaloChannelOptions.value = []
    zaloGroups.value = []
  } finally {
    loadingZaloRefs.value = false
  }
}

function groupOptionsFor(channelId?: string): Array<{ title: string; value: string }> {
  return zaloGroups.value
    .filter((g) => g.zalo_group_id && (!g.channel_id || g.channel_id === channelId))
    .map((g) => ({ title: g.name, value: g.id }))
}

function onZaloChannelChange(output: OutputItem, idx: number) {
  // Drop a group selection that no longer belongs to the chosen channel.
  const valid = groupOptionsFor(output.channel_id).some((o) => o.value === output.group_id)
  if (!valid) output.group_id = ''
  resetTest(idx)
}

const outputs = ref<OutputItem[]>([])
const testingIdx = ref<number | null>(null)
const testResult = ref<{ idx: number; success: boolean; message: string } | null>(null)
const testPassed = ref<boolean[]>([])
const internalUpdate = ref(false)

function isOutputValid(output: OutputItem): boolean {
  if (output.type === 'telegram') {
    return !!(output.bot_token?.trim() && output.chat_id?.trim())
  }
  if (output.type === 'email') {
    return !!(output.smtp_host?.trim() && output.smtp_port && output.from?.trim() && output.to?.trim())
  }
  if (output.type === 'zalo') {
    return !!(output.channel_id?.trim() && output.group_id?.trim())
  }
  return false
}

function resetTest(idx: number) {
  testPassed.value[idx] = false
  syncValidation()
}

// Sync validation state to parent form
function syncValidation() {
  const allPassed = outputs.value.length === 0 || outputs.value.every((_, i) => testPassed.value[i])
  form.value.outputs_validated = allPassed
}

function tryParseOutputs() {
  if (outputs.value.length > 0) return
  if (form.value.outputs && form.value.outputs !== '[]') {
    try {
      const parsed = JSON.parse(form.value.outputs)
      if (Array.isArray(parsed) && parsed.length > 0) {
        internalUpdate.value = true
        outputs.value = parsed.map((o: any) => ({ ...o, template: o.template || 'default' }))
        // Mark existing outputs as tested (edit mode — they were saved before)
        testPassed.value = parsed.map(() => true)
        syncValidation()
      }
    } catch { /* ignore */ }
  }
}

onMounted(() => {
  tryParseOutputs()
  syncValidation()
  loadZaloRefs()
})

watch(() => form.value.outputs, () => {
  if (!internalUpdate.value) tryParseOutputs()
  internalUpdate.value = false
})

watch(outputs, (val) => {
  internalUpdate.value = true
  form.value.outputs = JSON.stringify(val)
  // Ensure testPassed array matches length
  while (testPassed.value.length < val.length) testPassed.value.push(false)
  syncValidation()
}, { deep: true })

function addOutput() {
  outputs.value.push({ type: 'telegram', bot_token: '', chat_id: '', template: 'default' })
  testPassed.value.push(false)
  syncValidation()
}

function removeOutput(idx: number) {
  outputs.value.splice(idx, 1)
  testPassed.value.splice(idx, 1)
  syncValidation()
}

async function testSend(idx: number) {
  const output = outputs.value[idx]
  testingIdx.value = idx
  testResult.value = null
  try {
    await api.post(`/tenants/${tenantId.value}/test-output`, {
      type: output.type,
      bot_token: output.bot_token,
      chat_id: output.chat_id,
      channel_id: output.channel_id,
      group_id: output.group_id,
    })
    testResult.value = { idx, success: true, message: 'Gửi thành công!' }
    testPassed.value[idx] = true
    syncValidation()
  } catch (err: any) {
    const msg = err.response?.data?.error || 'Gửi thất bại'
    testResult.value = { idx, success: false, message: msg }
    testPassed.value[idx] = false
    syncValidation()
  } finally {
    testingIdx.value = null
  }
}
</script>
