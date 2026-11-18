<template>
  <v-card class="pa-6 mb-6" data-testid="chatbot-health">
    <div class="d-flex align-center mb-2">
      <v-icon :color="statusColor(report?.status)" class="mr-2">{{ statusIcon(report?.status) }}</v-icon>
      <div class="text-subtitle-1 font-weight-bold">Sức khoẻ AI chatbot Zalo</div>
      <v-spacer />
      <v-btn size="small" variant="tonal" color="primary" :loading="loading" prepend-icon="mdi-stethoscope" @click="run">
        Kiểm tra ngay
      </v-btn>
    </div>
    <div class="text-body-2 text-grey-darken-1 mb-4">
      Kiểm tra đúng cấu hình bot đang dùng thật cho từng kênh (gồm cả ghi đè trên kênh), không tốn lượt AI.
    </div>

    <v-alert v-if="error" type="error" variant="tonal" density="compact" class="mb-3">{{ error }}</v-alert>

    <v-list v-if="report" density="compact" class="pa-0">
      <v-list-item v-for="(c, i) in report.checks" :key="i" class="px-0">
        <template #prepend>
          <v-icon :color="statusColor(c.status)" size="small">{{ statusIcon(c.status) }}</v-icon>
        </template>
        <v-list-item-title class="text-body-2 font-weight-medium">
          {{ c.label }}<span v-if="c.channel" class="text-grey"> · {{ c.channel }}</span>
        </v-list-item-title>
        <v-list-item-subtitle v-if="c.detail" class="text-wrap">{{ c.detail }}</v-list-item-subtitle>
        <div v-if="c.fix && c.status !== 'ok'" class="text-caption mt-1" :class="c.status === 'error' ? 'text-error' : 'text-warning'">
          → {{ c.fix }}
        </div>
      </v-list-item>
    </v-list>

    <div v-if="report" class="text-caption text-grey mt-2">Kiểm tra lúc {{ new Date(report.checked).toLocaleString() }}</div>
  </v-card>
</template>

<script setup lang="ts">
import { ref, onMounted } from 'vue'
import api from '../api'

const props = defineProps<{ tenantId: string }>()

interface HealthCheck { key: string; label: string; status: 'ok' | 'warn' | 'error'; detail?: string; fix?: string; channel?: string }
interface HealthReport { status: 'ok' | 'warn' | 'error'; checks: HealthCheck[]; checked: string }

const report = ref<HealthReport | null>(null)
const loading = ref(false)
const error = ref('')

function statusColor(s?: string) {
  return s === 'ok' ? 'success' : s === 'warn' ? 'warning' : s === 'error' ? 'error' : 'grey'
}
function statusIcon(s?: string) {
  return s === 'ok' ? 'mdi-check-circle' : s === 'warn' ? 'mdi-alert' : s === 'error' ? 'mdi-close-circle' : 'mdi-help-circle-outline'
}

async function run() {
  if (!props.tenantId) return
  loading.value = true
  error.value = ''
  try {
    const { data } = await api.get(`/tenants/${props.tenantId}/chatbot/health`)
    report.value = data
  } catch (err: any) {
    error.value = err.response?.data?.message || err.response?.data?.error || 'Không chạy được kiểm tra'
  } finally {
    loading.value = false
  }
}

onMounted(run)
defineExpose({ run })
</script>
