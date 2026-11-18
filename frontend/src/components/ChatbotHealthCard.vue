<template>
  <v-card class="mb-6" data-testid="chatbot-health">
    <!-- Collapsed header: basic status + summary badges -->
    <div
      class="d-flex align-center pa-4 cursor-pointer health-header"
      style="user-select: none;"
      @click="expanded = !expanded"
    >
      <v-icon :color="statusColor(report?.status)" class="mr-2">{{ statusIcon(report?.status) }}</v-icon>
      <div class="text-subtitle-1 font-weight-bold flex-grow-1">Sức khoẻ AI chatbot Zalo</div>

      <!-- Summary badges when collapsed -->
      <template v-if="!expanded">
        <v-chip
          v-if="summary.ok > 0"
          size="x-small"
          color="success"
          variant="tonal"
          class="mr-1"
          prepend-icon="mdi-check-circle"
        >
          {{ summary.ok }} đạt
        </v-chip>
        <v-chip
          v-if="summary.warn > 0"
          size="x-small"
          color="warning"
          variant="tonal"
          class="mr-1"
          prepend-icon="mdi-alert"
        >
          {{ summary.warn }} cảnh báo
        </v-chip>
        <v-chip
          v-if="summary.error > 0"
          size="x-small"
          color="error"
          variant="tonal"
          class="mr-1"
          prepend-icon="mdi-close-circle"
        >
          {{ summary.error }} lỗi
        </v-chip>
        <v-chip
          v-if="!report && !loading"
          size="x-small"
          color="grey"
          variant="tonal"
          class="mr-1"
        >
          Chưa kiểm tra
        </v-chip>
      </template>

      <v-btn
        icon
        size="x-small"
        variant="text"
        class="ml-2"
        :loading="loading"
        title="Kiểm tra ngay"
        @click.stop="run"
      >
        <v-icon size="16">mdi-refresh</v-icon>
      </v-btn>
      <v-icon class="ml-1">{{ expanded ? 'mdi-chevron-up' : 'mdi-chevron-down' }}</v-icon>
    </div>

    <!-- Expanded checklist detail -->
    <v-expand-transition>
      <div v-show="expanded">
        <v-divider />
        <div class="pa-4 pt-3">
          <div class="text-body-2 text-grey-darken-1 mb-3">
            Kiểm tra đúng cấu hình bot đang dùng thật cho từng kênh (gồm cả ghi đè trên kênh), không tốn lượt AI.
          </div>

          <v-alert v-if="error" type="error" variant="tonal" density="compact" class="mb-3">{{ error }}</v-alert>

          <v-list v-if="report && report.checks && report.checks.length" density="compact" class="pa-0">
            <v-list-item
              v-for="(c, i) in report.checks"
              :key="i"
              class="px-2 py-1 mb-1 rounded"
              :class="checkRowClass(c.status)"
            >
              <template #prepend>
                <v-icon :color="statusColor(c.status)" size="small" class="mr-2">{{ statusIcon(c.status) }}</v-icon>
              </template>
              <v-list-item-title class="text-body-2 font-weight-medium">
                {{ c.label }}<span v-if="c.channel" class="text-grey"> · {{ c.channel }}</span>
              </v-list-item-title>
              <v-list-item-subtitle v-if="c.detail" class="text-wrap text-caption mt-0.5">{{ c.detail }}</v-list-item-subtitle>
              <div v-if="c.fix && c.status !== 'ok'" class="text-caption mt-1" :class="c.status === 'error' ? 'text-error' : 'text-warning'">
                → {{ c.fix }}
              </div>
              <template #append>
                <v-chip size="x-small" :color="statusColor(c.status)" variant="tonal">
                  {{ statusText(c.status) }}
                </v-chip>
              </template>
            </v-list-item>
          </v-list>

          <div class="d-flex align-center justify-space-between mt-3 pt-2">
            <div v-if="report" class="text-caption text-grey">
              Kiểm tra lúc {{ new Date(report.checked).toLocaleString() }}
            </div>
            <v-btn
              size="small"
              variant="tonal"
              color="primary"
              :loading="loading"
              prepend-icon="mdi-stethoscope"
              @click="run"
            >
              Kiểm tra lại
            </v-btn>
          </div>
        </div>
      </div>
    </v-expand-transition>
  </v-card>
</template>

<script setup lang="ts">
import { ref, computed, onMounted } from 'vue'
import api from '../api'

const props = defineProps<{ tenantId: string }>()

interface HealthCheck { key: string; label: string; status: 'ok' | 'warn' | 'error'; detail?: string; fix?: string; channel?: string }
interface HealthReport { status: 'ok' | 'warn' | 'error'; checks: HealthCheck[]; checked: string }

const report = ref<HealthReport | null>(null)
const loading = ref(false)
const error = ref('')
const expanded = ref(false)

const summary = computed(() => {
  if (!report.value || !report.value.checks) {
    return { ok: 0, warn: 0, error: 0 }
  }
  return {
    ok: report.value.checks.filter(c => c.status === 'ok').length,
    warn: report.value.checks.filter(c => c.status === 'warn').length,
    error: report.value.checks.filter(c => c.status === 'error').length,
  }
})

function statusColor(s?: string) {
  return s === 'ok' ? 'success' : s === 'warn' ? 'warning' : s === 'error' ? 'error' : 'grey'
}
function statusIcon(s?: string) {
  return s === 'ok' ? 'mdi-check-circle' : s === 'warn' ? 'mdi-alert' : s === 'error' ? 'mdi-close-circle' : 'mdi-help-circle-outline'
}
function statusText(s?: string) {
  return s === 'ok' ? 'Đạt' : s === 'warn' ? 'Cảnh báo' : s === 'error' ? 'Lỗi' : '—'
}
function checkRowClass(s?: string) {
  if (s === 'warn') return 'health-row-warn'
  if (s === 'error') return 'health-row-error'
  return 'health-row-ok'
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

<style scoped>
.health-header {
  transition: background-color 0.2s ease;
}
.health-header:hover {
  background-color: rgba(128, 128, 128, 0.06);
}
.health-row-ok {
  background-color: rgba(76, 175, 80, 0.05);
}
.health-row-warn {
  background-color: rgba(255, 152, 0, 0.07);
}
.health-row-error {
  background-color: rgba(244, 67, 54, 0.07);
}
</style>
