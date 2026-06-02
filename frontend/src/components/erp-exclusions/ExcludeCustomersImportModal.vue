<template>
  <v-dialog v-model="dialog" max-width="560" persistent>
    <v-card>
      <v-card-title class="d-flex align-center">
        <v-icon class="mr-2" color="primary">mdi-file-upload-outline</v-icon>
        Import Excel danh sách mã KH loại trừ
      </v-card-title>
      <v-card-text>
        <p class="text-body-2 mb-3">
          File Excel cần đúng 2 cột:
          <strong>cột A = Mã KH</strong>,
          <strong>cột B = T/F</strong>
          (T = loại ra, F = không loại). Dòng đầu là tiêu đề.
          Mã đã link group GMF sẽ bị từ chối toàn bộ file.
        </p>
        <v-btn
          variant="text"
          size="small"
          color="primary"
          prepend-icon="mdi-download"
          class="mb-3 px-0"
          :loading="downloading"
          @click="downloadTemplate"
        >
          Tải file mẫu (đã pre-fill mã KH hiện có)
        </v-btn>

        <v-file-input
          v-model="selectedFile"
          accept=".xlsx,.xls"
          label="Chọn file Excel"
          variant="outlined"
          density="comfortable"
          show-size
          prepend-icon=""
          prepend-inner-icon="mdi-paperclip"
          :error-messages="fileError"
          clearable
        />

        <v-progress-linear
          v-if="uploading"
          indeterminate
          color="primary"
          class="mt-2"
        />

        <v-alert
          v-if="result"
          type="success"
          variant="tonal"
          density="comfortable"
          class="mt-3 text-body-2"
        >
          Đã import xong:
          <strong>{{ result.added }}</strong> thêm,
          <strong>{{ result.removed }}</strong> bỏ,
          <strong>{{ result.skipped }}</strong> bỏ qua.
          Cache hiện có <strong>{{ result.cache_rows }}</strong> khách hàng.
        </v-alert>

        <v-alert
          v-if="errorMsg"
          type="error"
          variant="tonal"
          density="comfortable"
          class="mt-3 text-body-2"
        >
          {{ errorMsg }}
        </v-alert>
      </v-card-text>
      <v-card-actions>
        <v-spacer />
        <v-btn variant="text" :disabled="uploading" @click="close">Đóng</v-btn>
        <v-btn
          color="primary"
          variant="flat"
          :loading="uploading"
          :disabled="!selectedFile"
          @click="upload"
        >
          Upload
        </v-btn>
      </v-card-actions>
    </v-card>
  </v-dialog>
</template>

<script setup lang="ts">
import { ref, watch } from 'vue'
import api from '../../api'

interface ImportResult {
  added: number
  removed: number
  skipped: number
  cache_rows: number
}

interface Props {
  modelValue: boolean
  tenantId: string
}
const props = defineProps<Props>()
const emit = defineEmits<{
  (e: 'update:modelValue', value: boolean): void
  (e: 'updated', cacheRows: number): void
}>()

const dialog = ref(props.modelValue)
const selectedFile = ref<File | null>(null)
const fileError = ref('')
const uploading = ref(false)
const downloading = ref(false)
const result = ref<ImportResult | null>(null)
const errorMsg = ref('')

watch(
  () => props.modelValue,
  (v) => {
    dialog.value = v
    if (v) {
      selectedFile.value = null
      fileError.value = ''
      result.value = null
      errorMsg.value = ''
    }
  },
)

watch(dialog, (v) => {
  if (v !== props.modelValue) emit('update:modelValue', v)
})

function close(): void {
  dialog.value = false
}

async function downloadTemplate(): Promise<void> {
  downloading.value = true
  errorMsg.value = ''
  try {
    const response = await api.get(
      `/tenants/${props.tenantId}/erp/customer-codes/template`,
      { responseType: 'blob' },
    )
    const url = URL.createObjectURL(new Blob([response.data]))
    const a = document.createElement('a')
    a.href = url
    a.download = `customer-code-template-${Date.now()}.xlsx`
    document.body.appendChild(a)
    a.click()
    a.remove()
    URL.revokeObjectURL(url)
  } catch (err: unknown) {
    errorMsg.value = readError(err, 'Không thể tải file mẫu.')
  } finally {
    downloading.value = false
  }
}

async function upload(): Promise<void> {
  if (!selectedFile.value) return
  uploading.value = true
  errorMsg.value = ''
  result.value = null
  try {
    const form = new FormData()
    form.append('file', selectedFile.value)
    const { data } = await api.post<ImportResult>(
      `/tenants/${props.tenantId}/erp/customer-codes/import`,
      form,
      { headers: { 'Content-Type': 'multipart/form-data' } },
    )
    result.value = data
    emit('updated', data.cache_rows)
  } catch (err: unknown) {
    errorMsg.value = readError(err, 'Upload thất bại.')
  } finally {
    uploading.value = false
  }
}

function readError(err: unknown, fallback: string): string {
  if (err && typeof err === 'object' && 'response' in err) {
    const r = (err as { response?: { data?: { message?: string; error?: string; codes?: string[] } } }).response
    if (r?.data?.error === 'gmf_linked_blocked' && r?.data?.codes?.length) {
      return `Không thể loại trừ mã đã link group GMF: ${r.data.codes.join(', ')}`
    }
    return r?.data?.message || r?.data?.error || fallback
  }
  return fallback
}
</script>
