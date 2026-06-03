<template>
  <div>
    <!-- Demo Reset Banner -->
    <v-alert v-if="demoStatus && demoStatus.is_demo" type="warning" variant="tonal" class="mb-4" density="compact">
      <div class="d-flex align-center">
        <v-icon start size="small">mdi-information</v-icon>
        <span class="text-body-2 flex-grow-1">Bạn đang sử dụng dữ liệu demo. Khi sẵn sàng, hãy xóa để bắt đầu với dữ liệu thật.</span>
        <v-btn color="error" variant="text" size="small" @click="resetDialog = true">
          <v-icon start size="small">mdi-delete</v-icon>
          Xóa dữ liệu demo
        </v-btn>
      </div>
    </v-alert>

    <!-- Reset confirm dialog -->
    <v-dialog v-model="resetDialog" max-width="480">
      <v-card>
        <v-card-title class="text-error">Xóa toàn bộ dữ liệu demo</v-card-title>
        <v-card-text>
          <v-alert type="error" variant="tonal" class="mb-3">
            Tất cả dữ liệu sẽ bị xóa bao gồm: kênh chat, tin nhắn, công việc, kết quả đánh giá, nhật ký chi phí. Chỉ giữ lại danh sách thành viên.
          </v-alert>
          <div class="text-body-2">Hành động này không thể hoàn tác. Bạn có chắc chắn?</div>
        </v-card-text>
        <v-card-actions>
          <v-spacer />
          <v-btn variant="text" @click="resetDialog = false">Hủy</v-btn>
          <v-btn color="error" variant="flat" :loading="resettingDemo" @click="resetDemo">Xóa tất cả</v-btn>
        </v-card-actions>
      </v-card>
    </v-dialog>

    <!-- Chatbot Toggle Panel (Admin/Owner only) -->
    <v-card v-if="isAdmin" class="pa-4 mb-4" variant="tonal" color="primary">
      <!-- Master switch -->
      <div class="d-flex align-center flex-wrap ga-3">
        <v-icon color="primary" size="large">mdi-robot</v-icon>
        <div>
          <div class="text-subtitle-2 font-weight-bold text-primary">{{ $t('chatbot_master_label') }}</div>
          <div class="text-caption text-grey-darken-1">{{ $t('chatbot_master_desc') }}</div>
        </div>
        <v-spacer />
        <div class="d-flex align-center">
          <span class="text-body-2 font-weight-medium mr-2" :class="chatbotActive ? 'text-success' : 'text-grey'">
            {{ chatbotActive ? $t('chatbot_status_on') : $t('chatbot_status_off') }}
          </span>
          <v-switch
            v-model="chatbotActive"
            color="success"
            hide-details
            density="compact"
            :loading="togglingChatbot"
            :disabled="togglingChatbot"
            @update:model-value="toggleChatbot"
          />
        </div>
      </div>

      <!-- Per-channel auto-reply switches (active OA webhook channels only) -->
      <template v-if="webhookChannels.length">
        <v-divider class="my-3" />
        <div class="d-flex align-center mb-1 px-1">
          <span class="text-caption font-weight-medium text-grey-darken-1">{{ $t('chatbot_channel_autoreply_label') }}</span>
          <v-spacer />
          <span class="text-caption text-grey-darken-1">
            {{ $t('chatbot_channels_summary', { active: activeAutoReplyCount, total: webhookChannels.length }) }}
          </span>
        </div>
        <v-list density="compact" bg-color="transparent" class="py-0">
          <v-list-item v-for="ch in webhookChannels" :key="ch.id" class="px-1">
            <template #prepend>
              <v-icon size="small" class="mr-2" :color="ch.channel_type === 'facebook' ? 'blue' : 'light-blue-darken-2'">
                {{ ch.channel_type === 'facebook' ? 'mdi-facebook' : 'mdi-chat' }}
              </v-icon>
            </template>
            <v-list-item-title class="text-body-2">{{ ch.name }}</v-list-item-title>
            <template #append>
              <span v-if="!chatbotActive" class="text-caption text-grey mr-2">{{ $t('chatbot_master_off_hint') }}</span>
              <v-switch
                :model-value="ch.auto_reply_enabled"
                color="success"
                hide-details
                density="compact"
                :loading="togglingChannelId === ch.id"
                :disabled="!chatbotActive || togglingChannelId === ch.id"
                @update:model-value="(v: any) => toggleChannelAutoReply(ch, !!v)"
              />
            </template>
          </v-list-item>
        </v-list>
      </template>
    </v-card>

    <div class="d-flex flex-wrap align-center mb-4 ga-2">
      <h1 class="text-h5 font-weight-bold d-none d-md-block">{{ $t('dashboard') }}</h1>
      <v-spacer class="d-none d-md-block" />
      <v-chip-group v-model="datePreset">
        <v-chip v-for="p in datePresets" :key="p.value" :value="p.value" size="small" variant="outlined" @click="applyPreset(p.value)">
          {{ p.label }}
        </v-chip>
      </v-chip-group>
      <!-- Desktop: inline with chips -->
      <v-text-field v-model="dateFrom" type="date" density="compact" hide-details style="max-width: 160px" class="d-none d-md-block" @change="loadDashboard" />
      <v-text-field v-model="dateTo" type="date" density="compact" hide-details style="max-width: 160px" class="d-none d-md-block" @change="loadDashboard" />
    </div>
    <!-- Mobile: separate row -->
    <v-row dense class="mb-4 d-md-none">
      <v-col cols="6">
        <v-text-field v-model="dateFrom" type="date" density="compact" hide-details @change="loadDashboard" />
      </v-col>
      <v-col cols="6">
        <v-text-field v-model="dateTo" type="date" density="compact" hide-details @change="loadDashboard" />
      </v-col>
    </v-row>

    <!-- Stat cards -->
    <v-row class="mb-6">
      <v-col v-for="stat in stats" :key="stat.label" cols="6" sm="4" md="3">
        <v-card class="pa-4">
          <div class="d-flex justify-space-between align-center">
            <div>
              <div class="text-body-2 text-grey">{{ $t(stat.label) }}</div>
              <div class="text-h5 font-weight-bold mt-1">{{ stat.value }}</div>
            </div>
            <v-icon :color="stat.color" size="32" class="opacity-50">{{ stat.icon }}</v-icon>
          </div>
        </v-card>
      </v-col>
    </v-row>

    <!-- Channel counts + extra stats -->
    <v-row class="mb-4">
      <v-col v-for="ch in channelCounts" :key="ch.channel_type" cols="6" sm="3">
        <v-card class="pa-4">
          <div class="d-flex justify-space-between align-center">
            <div>
              <div class="text-body-2 text-grey">{{ ch.channel_type === 'facebook' ? 'Facebook' : 'Zalo OA' }}</div>
              <div class="text-h5 font-weight-bold mt-1">{{ ch.count }}</div>
            </div>
            <v-icon :color="ch.channel_type === 'facebook' ? 'blue' : 'green'" size="32" class="opacity-50">
              {{ ch.channel_type === 'facebook' ? 'mdi-facebook-messenger' : 'mdi-chat' }}
            </v-icon>
          </div>
        </v-card>
      </v-col>
      <v-col cols="6" sm="3">
        <v-card class="pa-4">
          <div class="d-flex justify-space-between align-center">
            <div>
              <div class="text-body-2 text-grey">{{ $t('total_messages') }}</div>
              <div class="text-h5 font-weight-bold mt-1">{{ totalMessages.toLocaleString() }}</div>
            </div>
            <v-icon color="primary" size="32" class="opacity-50">mdi-email-multiple</v-icon>
          </div>
        </v-card>
      </v-col>
      <v-col cols="6" sm="3">
        <v-card class="pa-4">
          <div class="d-flex justify-space-between align-center">
            <div>
              <div class="text-body-2 text-grey">{{ $t('ai_cost') }}</div>
              <div class="text-h5 font-weight-bold mt-1">{{ Math.round(costToday * exchangeRate).toLocaleString('vi-VN') }}đ</div>
            </div>
            <v-icon color="warning" size="32" class="opacity-50">mdi-currency-usd</v-icon>
          </div>
        </v-card>
      </v-col>
    </v-row>

    <v-row>
      <!-- Recent Activity (QC + Classification mixed) -->
      <v-col cols="12" md="7">
        <v-card class="pa-4 h-100 d-flex flex-column">
          <div class="text-subtitle-1 font-weight-bold mb-3 flex-shrink-0">
            <v-icon start size="small" color="primary">mdi-bell-ring</v-icon>
            {{ $t('recent_activity') }}
          </div>
          <div v-if="recentActivity.length" class="flex-grow-1">
            <div
              v-for="item in recentActivity"
              :key="item.id"
              class="d-flex align-center pa-2 mb-1 rounded"
              style="cursor: pointer"
              :style="{ background: item._type === 'qc' ? '#fff5f5' : '#f8f8fc' }"
              @click="goToConversation(item.conversation_id, item._type === 'qc' ? 'evaluation' : 'classification')"
            >
              <!-- QC Alert row -->
              <template v-if="item._type === 'qc'">
                <v-chip size="x-small" :color="item.severity === 'NGHIEM_TRONG' ? 'error' : 'warning'" variant="tonal" class="mr-2 flex-shrink-0">
                  {{ item.severity === 'NGHIEM_TRONG' ? 'Nghiêm trọng' : 'Cần cải thiện' }}
                </v-chip>
                <span class="text-body-2 flex-grow-1" style="overflow: hidden; white-space: nowrap; text-overflow: ellipsis;">{{ item.evidence || item.rule_name }}</span>
              </template>
              <!-- Classification row -->
              <template v-else>
                <span class="text-body-2 font-weight-medium mr-2 flex-shrink-0">{{ item.customer_name || '—' }}</span>
                <span class="text-body-2 text-grey-darken-1 mr-2 flex-shrink-0">Phân loại:</span>
                <v-chip size="x-small" color="deep-purple" variant="tonal" class="mr-1 flex-shrink-0">{{ item.rule_name }}</v-chip>
              </template>
              <v-spacer />
              <span class="text-caption text-grey text-no-wrap ml-2">{{ timeAgo(item.created_at) }}</span>
            </div>
          </div>
          <div v-else class="text-center pa-6 flex-grow-1 d-flex flex-column justify-center align-center">
            <v-icon size="40" color="success" class="mb-2">mdi-check-circle</v-icon>
            <div class="text-grey">Chưa có hoạt động nào trong khoảng thời gian này.</div>
          </div>
        </v-card>
      </v-col>

      <!-- AI Cost + Service Status -->
      <v-col cols="12" md="5">
        <!-- AI Cost Summary -->
        <v-card class="pa-4 mb-4">
          <div class="text-subtitle-1 font-weight-bold mb-3">
            <v-icon start size="small" color="warning">mdi-currency-usd</v-icon>
            {{ $t('ai_cost') }}
          </div>
          <div class="d-flex ga-4 mb-3">
            <div>
              <div class="text-caption text-grey">{{ $t('cost_today') }}</div>
              <div class="text-h6 font-weight-bold">{{ Math.round(costToday * exchangeRate).toLocaleString('vi-VN') }}đ</div>
            </div>
            <div>
              <div class="text-caption text-grey">{{ $t('cost_this_month') }}</div>
              <div class="text-h6 font-weight-bold">{{ Math.round(costMonth * exchangeRate).toLocaleString('vi-VN') }}đ</div>
            </div>
          </div>

          <!-- Cost by day table -->
          <v-table v-if="costByDay.length" density="compact" class="text-body-2">
            <thead>
              <tr>
                <th>{{ $t('date') }}</th>
                <th class="text-right">Tokens</th>
                <th class="text-right">{{ $t('cost') }} (VNĐ)</th>
              </tr>
            </thead>
            <tbody>
              <tr v-for="day in costByDay.slice(0, 7)" :key="day.date">
                <td>{{ formatDisplayDate(day.date) }}</td>
                <td class="text-right text-caption">{{ (day.input_tokens + day.output_tokens).toLocaleString() }}</td>
                <td class="text-right">{{ Math.round(day.total_cost * exchangeRate).toLocaleString('vi-VN') }}đ</td>
              </tr>
            </tbody>
          </v-table>
          <div v-else class="text-center text-grey text-caption pa-2">{{ $t('no_data') }}</div>
        </v-card>

        <!-- Service Status -->
        <v-card class="pa-4">
          <div class="text-subtitle-1 font-weight-bold mb-3">
            <v-icon start size="small" color="success">mdi-check-circle</v-icon>
            {{ $t('service_status') }}
          </div>
          <v-list density="compact">
            <v-list-item v-for="svc in services" :key="svc.name" class="px-0">
              <v-list-item-title class="text-body-2">{{ svc.name }}</v-list-item-title>
              <template #append>
                <v-chip size="x-small" :color="svc.ok ? 'success' : 'error'" variant="tonal">
                  {{ svc.ok ? $t('normal') : $t('error') }}
                </v-chip>
              </template>
            </v-list-item>
          </v-list>
        </v-card>
      </v-col>
    </v-row>

    <!-- Charts Row -->
    <v-row class="mt-4">
      <v-col cols="12" md="6">
        <v-card class="pa-4 h-100 d-flex flex-column">
          <div class="text-subtitle-1 font-weight-bold mb-3 flex-shrink-0">
            <v-icon start size="small" color="primary">mdi-message-text-clock</v-icon>
            {{ $t('messages_by_day') }}
          </div>
          <div v-if="messagesChartData.labels.length" class="flex-grow-1" style="min-height: 250px">
            <Line :data="messagesChartData" :options="chartOptions" style="max-height: 250px; height: 100%" />
          </div>
          <div v-else class="text-center text-grey pa-4 flex-grow-1 d-flex flex-column justify-center align-center" style="min-height: 250px">
            <v-icon size="40" color="grey-lighten-2" class="mb-2">mdi-chart-box-outline</v-icon>
            <div>{{ $t('no_data') }}</div>
          </div>
        </v-card>
      </v-col>
      <v-col cols="12" md="6">
        <v-card class="pa-4 h-100 d-flex flex-column">
          <div class="text-subtitle-1 font-weight-bold mb-3 flex-shrink-0">
            <v-icon start size="small" color="warning">mdi-chart-line</v-icon>
            {{ $t('cost_by_day_chart') }}
          </div>
          <div v-if="costChartData.labels.length" class="flex-grow-1" style="min-height: 250px">
            <Line :data="costChartData" :options="chartOptionsNoLegend" style="max-height: 250px; height: 100%" />
          </div>
          <div v-else class="text-center text-grey pa-4 flex-grow-1 d-flex flex-column justify-center align-center" style="min-height: 250px">
            <v-icon size="40" color="grey-lighten-2" class="mb-2">mdi-chart-box-outline</v-icon>
            <div>{{ $t('no_data') }}</div>
          </div>
        </v-card>
      </v-col>
    </v-row>
  </div>
</template>

<script setup lang="ts">
import { ref, computed, onMounted } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { Line } from 'vue-chartjs'
import { Chart as ChartJS, CategoryScale, LinearScale, PointElement, LineElement, Title, Tooltip, Filler, Legend } from 'chart.js'
import api from '../api'
import { useAuthStore } from '../stores/auth'
import { useChannelStore, type Channel } from '../stores/channels'

ChartJS.register(CategoryScale, LinearScale, PointElement, LineElement, Title, Tooltip, Filler, Legend)

const route = useRoute()
const router = useRouter()
const { t } = useI18n()
const tenantId = computed(() => route.params.tenantId as string)

const authStore = useAuthStore()
const isAdmin = computed(() => {
  const role = authStore.tenantPerms.role
  return role === 'owner' || role === 'admin'
})

const channelStore = useChannelStore()
const chatbotActive = ref(true)
const togglingChatbot = ref(false)
const togglingChannelId = ref<string | null>(null)

// Channels that can run a webhook-based chatbot: OA-type AND currently active.
// Personal Zalo (no webhook) and deactivated channels are never shown/controllable.
const WEBHOOK_CHANNEL_TYPES = ['zalo_oa', 'facebook']
const webhookChannels = computed(() =>
  channelStore.channels.filter(
    (c) => WEBHOOK_CHANNEL_TYPES.includes(c.channel_type) && c.is_active
  )
)
const activeAutoReplyCount = computed(
  () => webhookChannels.value.filter((c) => c.auto_reply_enabled).length
)

async function fetchChatbotStatus() {
  if (!isAdmin.value) return
  try {
    const { data } = await api.get(`/tenants/${tenantId.value}/settings`)
    chatbotActive.value = data?.settings?.chatbot_active !== 'false'
  } catch { /* ignore */ }
}

async function fetchChannelsForPanel() {
  if (!isAdmin.value) return
  try {
    await channelStore.fetchChannels(tenantId.value)
  } catch { /* ignore — panel simply renders empty */ }
}

async function toggleChatbot(val: any) {
  togglingChatbot.value = true
  try {
    await api.put(`/tenants/${tenantId.value}/settings`, {
      key: 'chatbot_active',
      value: val ? 'true' : 'false'
    })
  } catch (e: any) {
    chatbotActive.value = !val
    alert(t('chatbot_toggle_error') + ': ' + (e.response?.data?.error || e.message))
  } finally {
    togglingChatbot.value = false
  }
}

async function toggleChannelAutoReply(ch: Channel, val: boolean) {
  togglingChannelId.value = ch.id
  try {
    await channelStore.updateChannel(tenantId.value, ch.id, { auto_reply_enabled: val })
  } catch (e: any) {
    ch.auto_reply_enabled = !val
    alert(t('chatbot_toggle_error') + ': ' + (e.response?.data?.error || e.message))
  } finally {
    togglingChannelId.value = null
  }
}

const stats = ref([
  { label: 'total_conversations', value: 0, icon: 'mdi-message-text', color: 'primary' },
  { label: 'issues_today', value: 0, icon: 'mdi-alert-circle', color: 'error' },
  { label: 'active_jobs', value: 0, icon: 'mdi-briefcase-check', color: 'success' },
  { label: 'active_channels', value: 0, icon: 'mdi-connection', color: 'info' },
])

const qcAlerts = ref<any[]>([])
const classRecent = ref<any[]>([])

const recentActivity = computed(() => {
  const qc = qcAlerts.value.map(a => ({ ...a, _type: 'qc' }))
  const cls = classRecent.value.map(a => ({ ...a, _type: 'class' }))
  return [...qc, ...cls]
    .sort((a, b) => new Date(b.created_at).getTime() - new Date(a.created_at).getTime())
    .slice(0, 10)
})

const costToday = ref(0)
const costMonth = ref(0)
const costByDay = ref<any[]>([])
const exchangeRate = ref(26000)
const services = ref([
  { name: 'API Server', ok: true },
  { name: 'Database', ok: true },
  { name: 'Scheduler', ok: true },
])
const messagesByDay = ref<any[]>([])
const channelCounts = ref<any[]>([])
const totalMessages = computed(() => messagesByDay.value.reduce((sum, d) => sum + (d.count || 0), 0))

// Date filter + presets
const now = new Date()
const dateFrom = ref(formatDate(new Date(now.getFullYear(), now.getMonth(), now.getDate() - 28)))
const dateTo = ref(formatDate(now))
const datePreset = ref('28days')

const datePresets = [
  { label: 'Hôm nay', value: 'today' },
  { label: '7 ngày', value: '7days' },
  { label: '28 ngày', value: '28days' },
  { label: 'Tháng này', value: 'month' },
  { label: 'Quý này', value: 'quarter' },
  { label: 'Năm này', value: 'year' },
]

function formatDate(d: Date) {
  return d.toISOString().split('T')[0]
}

function applyPreset(preset: string) {
  const d = new Date()
  const y = d.getFullYear()
  const m = d.getMonth()
  dateTo.value = formatDate(d)

  switch (preset) {
    case 'today':
      dateFrom.value = formatDate(new Date(y, m, d.getDate()))
      break
    case '7days':
      dateFrom.value = formatDate(new Date(y, m, d.getDate() - 7))
      break
    case '28days':
      dateFrom.value = formatDate(new Date(y, m, d.getDate() - 28))
      break
    case 'week': {
      const day = d.getDay() || 7
      dateFrom.value = formatDate(new Date(y, m, d.getDate() - day + 1))
      break
    }
    case 'month':
      dateFrom.value = formatDate(new Date(y, m, 1))
      break
    case 'quarter': {
      const qm = Math.floor(m / 3) * 3
      dateFrom.value = formatDate(new Date(y, qm, 1))
      break
    }
    case 'year':
      dateFrom.value = formatDate(new Date(y, 0, 1))
      break
  }
  loadDashboard()
}

function formatDisplayDate(dateStr: string) {
  if (!dateStr) return ''
  // Handle "2026-03-21" or "2026-03-21T00:00:00Z"
  const parts = dateStr.split('T')[0].split('-')
  if (parts.length === 3) return `${parts[2]}/${parts[1]}/${parts[0]}`
  return dateStr
}

function formatChartDate(dateStr: string) {
  if (!dateStr) return ''
  const parts = dateStr.split('T')[0].split('-')
  if (parts.length === 3) return `${parseInt(parts[2])}/${parseInt(parts[1])}`
  return dateStr
}

const messagesChartData = computed(() => ({
  labels: messagesByDay.value.map(d => formatChartDate(d.date)),
  datasets: [
    {
      label: t('total_messages'),
      data: messagesByDay.value.map(d => d.count),
      borderColor: '#5C6BC0',
      backgroundColor: 'rgba(92,107,192,0.1)',
      fill: false,
      tension: 0.3,
    },
    {
      label: 'Cuộc chat',
      data: messagesByDay.value.map(d => d.chat_count || 0),
      borderColor: '#66BB6A',
      fill: false,
      tension: 0.3,
    },
    {
      label: 'Trả lời NV',
      data: messagesByDay.value.map(d => d.reply_count || 0),
      borderColor: '#FFA726',
      fill: false,
      tension: 0.3,
    },
  ],
}))

const costChartData = computed(() => ({
  labels: [...costByDay.value].reverse().map(d => formatChartDate(d.date)),
  datasets: [{
    label: 'Chi phí (VNĐ)',
    data: [...costByDay.value].reverse().map(d => Math.round(d.total_cost * exchangeRate.value)),
    borderColor: '#FFA726',
    backgroundColor: 'rgba(255,167,38,0.1)',
    fill: true,
    tension: 0.3,
  }],
}))

const chartOptions = {
  responsive: true,
  maintainAspectRatio: false,
  plugins: { legend: { display: true, position: 'bottom' as const } },
  scales: { y: { beginAtZero: true } },
}

const chartOptionsNoLegend = {
  responsive: true,
  maintainAspectRatio: false,
  plugins: { legend: { display: false } },
  scales: { y: { beginAtZero: true } },
}

async function loadDashboard() {
  try {
    const params: Record<string, string> = {}
    if (dateFrom.value) params.from = dateFrom.value
    if (dateTo.value) params.to = dateTo.value

    const { data } = await api.get(`/tenants/${tenantId.value}/dashboard`, { params })
    stats.value[0].value = data.total_conversations
    stats.value[1].value = data.issues_today
    stats.value[2].value = data.active_jobs
    stats.value[3].value = data.active_channels

    costToday.value = data.cost_today || 0
    costMonth.value = data.cost_this_month || 0
    costByDay.value = data.cost_by_day || []
    exchangeRate.value = data.exchange_rate || 26000

    qcAlerts.value = data.qc_alerts || []
    classRecent.value = data.classification_recent || []
    messagesByDay.value = data.messages_by_day || []
    channelCounts.value = data.conversations_by_channel || []
  } catch {
    // Dashboard data not available yet
  }
}

// Demo data state
const demoStatus = ref<{ has_data: boolean; is_demo: boolean } | null>(null)
const resettingDemo = ref(false)
const resetDialog = ref(false)

async function loadDemoStatus() {
  try {
    const { data } = await api.get(`/tenants/${tenantId.value}/demo/status`)
    demoStatus.value = data
  } catch { /* ignore */ }
}

async function resetDemo() {
  resettingDemo.value = true
  try {
    await api.delete(`/tenants/${tenantId.value}/demo/reset`)
    resetDialog.value = false
    await loadDemoStatus()
    await loadDashboard()
  } catch (e: any) {
    alert(e.response?.data?.error || 'Reset failed')
  } finally {
    resettingDemo.value = false
  }
}

onMounted(() => {
  loadDemoStatus()
  loadDashboard()
  fetchChatbotStatus()
  fetchChannelsForPanel()
})

function timeAgo(dateStr: string) {
  const diff = Date.now() - new Date(dateStr).getTime()
  const mins = Math.floor(diff / 60000)
  if (mins < 60) return `${mins} phút trước`
  const hours = Math.floor(mins / 60)
  if (hours < 24) return `${hours} giờ trước`
  return `${Math.floor(hours / 24)} ngày trước`
}

function goToConversation(convId: string, tab?: string) {
  if (convId) {
    const query: Record<string, string> = { conv: convId }
    if (tab) query.tab = tab
    router.push({ path: `/${tenantId.value}/messages`, query })
  }
}
</script>
