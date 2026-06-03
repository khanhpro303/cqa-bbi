<template>
  <span>
    <v-btn
      color="error"
      variant="tonal"
      size="small"
      prepend-icon="mdi-delete-sweep"
      @click="open = true"
    >
      {{ $t('delete_logs') }}
    </v-btn>

    <v-dialog v-model="open" max-width="460">
      <v-card>
        <v-card-title class="text-subtitle-1 font-weight-bold px-6 pt-5">
          {{ $t('delete_logs') }}
        </v-card-title>

        <v-card-text class="px-6 pb-0">
          <v-alert type="warning" variant="tonal" density="compact" class="mb-4">
            {{ $t('delete_logs_warning') }}
          </v-alert>

          <v-radio-group v-model="range" density="compact" hide-details>
            <v-radio :label="$t('delete_last_7_days')" value="7d" />
            <v-radio :label="$t('delete_last_month')" value="30d" />
            <v-radio :label="$t('custom_range')" value="custom" />
            <v-radio :label="$t('delete_all')" value="all" />
          </v-radio-group>

          <div v-if="range === 'custom'" class="d-flex ga-3 mt-3">
            <v-text-field
              v-model="customFrom"
              type="date"
              :label="$t('date_from')"
              density="compact"
              hide-details
            />
            <v-text-field
              v-model="customTo"
              type="date"
              :label="$t('date_to')"
              density="compact"
              hide-details
            />
          </div>

          <div v-if="errorMsg" class="text-error text-body-2 mt-3">{{ errorMsg }}</div>
        </v-card-text>

        <v-card-actions class="px-6 pb-5 pt-3 ga-2">
          <v-spacer />
          <v-btn variant="text" :disabled="loading" @click="open = false">{{ $t('cancel') }}</v-btn>
          <v-btn color="error" variant="flat" :loading="loading" :disabled="!canConfirm" @click="confirmDelete">
            {{ $t('confirm_delete') }}
          </v-btn>
        </v-card-actions>
      </v-card>
    </v-dialog>
  </span>
</template>

<script setup lang="ts">
import { ref, computed } from 'vue'
import api from '../api'

const props = defineProps<{
  tenantId: string
  endpoint: string // e.g. `/tenants/${id}/activity-logs`
}>()

const emit = defineEmits<{
  (e: 'deleted', count: number): void
}>()

const open = ref(false)
const loading = ref(false)
const range = ref<'7d' | '30d' | 'custom' | 'all'>('7d')
const customFrom = ref('')
const customTo = ref('')
const errorMsg = ref('')

const canConfirm = computed(() => {
  if (range.value !== 'custom') return true
  return !!customFrom.value || !!customTo.value
})

function toDateStr(d: Date): string {
  const yyyy = d.getFullYear()
  const mm = String(d.getMonth() + 1).padStart(2, '0')
  const dd = String(d.getDate()).padStart(2, '0')
  return `${yyyy}-${mm}-${dd}`
}

function buildParams(): Record<string, string> {
  const params: Record<string, string> = {}
  if (range.value === 'all') return params
  if (range.value === 'custom') {
    if (customFrom.value) params.from = customFrom.value
    if (customTo.value) params.to = customTo.value
    return params
  }
  // 7d / 30d: keep the last N days (including today)
  const days = range.value === '7d' ? 6 : 29
  const from = new Date()
  from.setDate(from.getDate() - days)
  params.from = toDateStr(from)
  return params
}

async function confirmDelete() {
  loading.value = true
  errorMsg.value = ''
  try {
    const { data } = await api.delete(props.endpoint, { params: buildParams() })
    emit('deleted', data?.deleted ?? 0)
    open.value = false
    range.value = '7d'
    customFrom.value = ''
    customTo.value = ''
  } catch (err: unknown) {
    const e = err as { response?: { data?: { error?: string } } }
    errorMsg.value = e?.response?.data?.error || 'Lỗi khi xoá logs'
  } finally {
    loading.value = false
  }
}
</script>
