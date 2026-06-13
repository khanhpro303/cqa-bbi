<template>
  <div>
    <h1 class="text-h5 font-weight-bold mb-6">{{ $t('nav_notification_logs') }}</h1>

    <v-card style="overflow-x: auto;">
      <v-card-text class="d-flex align-center flex-wrap pb-0" style="gap: 12px;">
        <v-btn-toggle
          v-model="sourceFilter"
          density="comfortable"
          mandatory
          color="primary"
          class="bg-transparent"
        >
          <v-btn value="all" size="small" variant="outlined">Tất cả</v-btn>
          <v-btn value="job" size="small" variant="outlined">
            <v-icon start size="small">mdi-robot-outline</v-icon>Tác vụ AI
          </v-btn>
          <v-btn value="campaign" size="small" variant="outlined">
            <v-icon start size="small">mdi-bullhorn-outline</v-icon>Chiến dịch
          </v-btn>
        </v-btn-toggle>

        <DeleteLogsDialog
          v-if="canManage"
          class="ml-auto"
          :tenant-id="tenantId"
          :endpoint="`/tenants/${tenantId}/notification-logs`"
          @deleted="onDeleted"
        />
      </v-card-text>

      <v-table v-if="logs.length" density="compact">
        <thead>
          <tr>
            <th>{{ $t('sent_at') }}</th>
            <th>Nguồn</th>
            <th>{{ $t('notification_channel') }}</th>
            <th>{{ $t('recipient') }}</th>
            <th>{{ $t('status') }}</th>
            <th>{{ $t('actions') }}</th>
          </tr>
        </thead>
        <tbody>
          <template v-for="log in logs" :key="log.id">
            <tr :class="log.status !== 'sent' ? 'bg-red-lighten-5' : ''">
              <td class="text-body-2">{{ new Date(log.sent_at).toLocaleString() }}</td>
              <td>
                <v-chip size="x-small" :color="sourceMeta(log).color" variant="flat">
                  <v-icon start size="x-small">{{ sourceMeta(log).icon }}</v-icon>
                  {{ sourceMeta(log).label }}
                </v-chip>
              </td>
              <td>
                <v-chip size="x-small" :color="channelMeta(log.channel_type).color" variant="tonal">
                  {{ channelMeta(log.channel_type).label }}
                </v-chip>
              </td>
              <td class="text-body-2">{{ log.recipient }}</td>
              <td>
                <v-chip size="x-small" :color="log.status === 'sent' ? 'success' : 'error'" variant="tonal">
                  <v-icon v-if="log.status !== 'sent'" start size="x-small">mdi-alert-circle</v-icon>
                  {{ log.status === 'sent' ? 'Đã gửi' : 'Lỗi' }}
                </v-chip>
              </td>
              <td>
                <v-btn size="small" variant="text" color="primary" @click="expandedId = expandedId === log.id ? '' : log.id">
                  <v-icon start size="small">{{ expandedId === log.id ? 'mdi-chevron-up' : 'mdi-eye' }}</v-icon>
                  {{ expandedId === log.id ? 'Ẩn' : 'Xem' }}
                </v-btn>
              </td>
            </tr>
            <tr v-if="expandedId === log.id">
              <td colspan="6" class="bg-grey-lighten-5 pa-4">
                <div v-if="log.error_message" class="mb-3">
                  <v-alert type="error" variant="tonal" density="compact" :text="log.error_message" />
                </div>
                <div class="text-caption text-grey mb-2">Nội dung đã gửi:</div>
                <div class="text-body-2 pa-3 rounded" style="background: white; border: 1px solid #e0e0e0; white-space: pre-wrap;">{{ log.body }}</div>
                <div v-if="log.subject" class="text-caption text-grey mt-2">Tiêu đề: {{ log.subject }}</div>
              </td>
            </tr>
          </template>
        </tbody>
      </v-table>
      <v-card-actions v-if="totalPages > 1" class="justify-center">
        <v-pagination v-model="page" :length="totalPages" :total-visible="7" density="compact" />
      </v-card-actions>
      <div v-else-if="!logs.length" class="text-center pa-8">
        <v-icon size="48" color="grey-lighten-1" class="mb-3">mdi-bell-outline</v-icon>
        <div class="text-grey">{{ $t('no_notifications_desc') }}</div>
      </div>
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

interface NotificationLog {
  id: string
  sent_at: string
  source?: string
  channel_type: string
  recipient: string
  subject?: string
  body: string
  status: string
  error_message?: string
}

const route = useRoute()
const { t } = useI18n()
const auth = useAuthStore()
const tenantId = computed(() => route.params.tenantId as string)
const canManage = computed(() => ['owner', 'admin'].includes(auth.tenantPerms.role))
const logs = ref<NotificationLog[]>([])
const expandedId = ref('')
const page = ref(1)
const total = ref(0)
const perPage = 20
const totalPages = computed(() => Math.ceil(total.value / perPage))
const snackbar = ref(false)
const snackbarText = ref('')
const sourceFilter = ref<'all' | 'job' | 'campaign'>('all')

// channelMeta maps a stored channel_type to a human label + chip color. The two
// Zalo variants are deliberately distinct: a job pushes into a GMF group ("zalo"),
// while a campaign failure alert targets the campaign's Zalo OA group ("zalo_oa").
function channelMeta(type: string): { label: string; color: string } {
  switch (type) {
    case 'telegram':
      return { label: 'Telegram', color: 'blue' }
    case 'email':
      return { label: 'Email', color: 'teal' }
    case 'zalo':
      return { label: 'Zalo (nhóm GMF)', color: 'orange' }
    case 'zalo_oa':
      return { label: 'Zalo (chiến dịch)', color: 'deep-orange' }
    default:
      return { label: type, color: 'grey' }
  }
}

// sourceMeta derives the origin chip. Prefer the explicit backend `source`; fall
// back to channel_type for any legacy row written before the column existed.
function sourceMeta(log: NotificationLog): { label: string; color: string; icon: string } {
  const source = log.source || (log.channel_type === 'zalo_oa' ? 'campaign' : 'job')
  if (source === 'campaign') {
    return { label: 'Chiến dịch', color: 'purple', icon: 'mdi-bullhorn-outline' }
  }
  return { label: 'Tác vụ AI', color: 'indigo', icon: 'mdi-robot-outline' }
}

function onDeleted(count: number) {
  snackbarText.value = t('logs_deleted', { count })
  snackbar.value = true
  page.value = 1
  loadLogs()
}

async function loadLogs() {
  try {
    const params: Record<string, unknown> = { page: page.value, per_page: perPage }
    if (sourceFilter.value !== 'all') {
      params.source = sourceFilter.value
    }
    const { data } = await api.get(`/tenants/${tenantId.value}/notification-logs`, { params })
    logs.value = data.data || data || []
    total.value = data.total || 0
  } catch {
    // Not available yet
  }
}

onMounted(loadLogs)
watch(page, loadLogs)
watch(sourceFilter, () => {
  expandedId.value = ''
  if (page.value !== 1) {
    page.value = 1 // triggers loadLogs via the page watcher
  } else {
    loadLogs()
  }
})
</script>

<style scoped>
.v-btn-toggle {
  gap: 8px;
}
.v-btn-toggle :deep(.v-btn) {
  border-radius: 8px !important;
}
</style>
