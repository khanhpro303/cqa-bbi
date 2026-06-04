<template>
  <div>
    <h1 class="text-h5 font-weight-bold mb-6">{{ $t('activity_logs') }}</h1>

    <v-card style="overflow-x: auto;">
      <v-card-text class="d-flex ga-3 pb-0 align-center">
        <v-select
          v-model="filterAction"
          :items="actionOptions"
          :label="$t('filter')"
          density="compact"
          clearable
          hide-details
          style="max-width: 200px"
          @update:model-value="loadLogs"
        />
        <DeleteLogsDialog
          v-if="canManage"
          class="ml-auto"
          :tenant-id="tenantId"
          :endpoint="`/tenants/${tenantId}/activity-logs`"
          @deleted="onDeleted"
        />
      </v-card-text>

      <v-table density="compact">
        <thead>
          <tr>
            <th>{{ $t('sent_at') }}</th>
            <th>{{ $t('action') }}</th>
            <th>{{ $t('user') }}</th>
            <th>{{ $t('detail') }}</th>
            <th>{{ $t('error') }}</th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="log in logs" :key="log.id">
            <td class="text-caption">{{ formatTime(log.created_at) }}</td>
            <td>
              <v-chip size="x-small" :color="actionColor(log.action)" variant="tonal">{{ log.action }}</v-chip>
            </td>
            <td class="text-body-2">{{ log.user_email || 'system' }}</td>
            <td class="text-body-2" style="max-width: 400px">{{ log.detail?.substring(0, 120) }}</td>
            <td>
              <v-chip v-if="log.error_message" size="x-small" color="error" variant="tonal">{{ log.error_message.substring(0, 80) }}</v-chip>
            </td>
          </tr>
        </tbody>
      </v-table>

      <v-card-actions v-if="totalPages > 1" class="justify-center">
        <v-pagination v-model="page" :length="totalPages" density="compact" />
      </v-card-actions>
    </v-card>

    <v-snackbar v-model="snackbar" color="success" timeout="3000">{{ snackbarText }}</v-snackbar>
  </div>
</template>

<script setup lang="ts">
import { ref, computed, onMounted, watch } from 'vue'
import { useRoute } from 'vue-router'
import { useI18n } from 'vue-i18n'
import api from '../api'
import { useAuthStore } from '../stores/auth'
import DeleteLogsDialog from '../components/DeleteLogsDialog.vue'

const route = useRoute()
const { t } = useI18n()
const auth = useAuthStore()
const tenantId = computed(() => route.params.tenantId as string)
const canManage = computed(() => ['owner', 'admin'].includes(auth.tenantPerms.role))

const logs = ref<any[]>([])
const page = ref(1)
const total = ref(0)
const perPage = 20
const filterAction = ref('')
const totalPages = computed(() => Math.ceil(total.value / perPage))
const snackbar = ref(false)
const snackbarText = ref('')

function onDeleted(count: number) {
  snackbarText.value = t('logs_deleted', { count })
  snackbar.value = true
  page.value = 1
  loadLogs()
}

const actionOptions = [
  { title: 'Kênh & Chatbot', value: 'channel' },
  { title: 'Cấu hình AI', value: 'ai' },
  { title: 'Cấu hình ERP', value: 'erp' },
  { title: 'Chiến dịch', value: 'campaign' },
  { title: 'Nhóm GMF', value: 'crm_group' },
  { title: 'Người dùng & bảo mật', value: 'user' },
  { title: 'Job', value: 'job' },
  { title: 'Job Run', value: 'job.run' },
  { title: 'Job Create', value: 'job.create' },
  { title: 'Job Delete', value: 'job.delete' },
  { title: 'Notification', value: 'notification' },
  { title: 'Settings', value: 'settings' },
]

onMounted(() => loadLogs())
watch(page, () => loadLogs())

async function loadLogs() {
  const params: Record<string, any> = { page: page.value, per_page: perPage }
  if (filterAction.value) params.action = filterAction.value
  const { data } = await api.get(`/tenants/${tenantId.value}/activity-logs`, { params })
  logs.value = data.data || []
  total.value = data.total || 0
}

function formatTime(d: string) {
  const dt = new Date(d)
  const dd = String(dt.getDate()).padStart(2, '0')
  const mm = String(dt.getMonth() + 1).padStart(2, '0')
  const hh = String(dt.getHours()).padStart(2, '0')
  const mi = String(dt.getMinutes()).padStart(2, '0')
  return `${dd}/${mm}/${dt.getFullYear()} ${hh}:${mi}`
}

function actionColor(action: string) {
  if (action.includes('error') || action.includes('login_failed')) return 'error'
  if (action.includes('locked')) return 'error'
  if (action.includes('delete') || action.includes('removed')) return 'warning'
  if (action.includes('create') || action.includes('invited')) return 'success'
  if (action.includes('toggle') || action.includes('updated') || action.includes('update')) return 'info'
  if (action.includes('status_changed') || action.includes('role_changed')) return 'primary'
  return 'info'
}
</script>
