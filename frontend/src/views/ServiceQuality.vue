<template>
  <div class="service-quality-page">
    <div class="d-flex align-start justify-space-between flex-wrap ga-3 mb-5">
      <div>
        <div class="d-flex align-center ga-2">
          <v-icon color="primary" size="30">mdi-headset</v-icon>
          <h1 class="text-h5 font-weight-bold">Chất lượng CSKH Messenger</h1>
        </div>
        <p class="text-body-2 text-medium-emphasis mt-1 mb-0">
          Theo dõi thời gian Fanpage phản hồi, khách đang chờ và nội dung trao đổi từ dữ liệu đã đồng bộ.
        </p>
      </div>
      <div class="d-flex flex-wrap ga-2">
        <v-btn
          v-if="authStore.canEdit('jobs')"
          variant="tonal"
          color="primary"
          prepend-icon="mdi-creation"
          :to="`/${tenantId}/jobs/create?template=messenger-insights`"
        >
          Tạo phân tích nội dung
        </v-btn>
        <v-btn
          v-if="canEditPolicy"
          variant="outlined"
          prepend-icon="mdi-tune"
          @click="openPolicyDialog"
        >
          Ngưỡng phản hồi
        </v-btn>
        <v-btn color="primary" prepend-icon="mdi-refresh" :loading="loading" @click="loadReport(true)">
          Làm mới
        </v-btn>
      </div>
    </div>

    <v-alert type="info" variant="tonal" class="mb-4" border="start">
      <div class="font-weight-medium">Phạm vi số liệu</div>
      <div class="text-body-2 mt-1">
        Hệ thống nhận diện phản hồi từ Fanpage, chưa xác định nhân viên, bot hay người trực cụ thể.
        KPI dựa trên lịch sử Messenger đã đồng bộ; kiểm tra thời điểm đồng bộ của từng trang bên dưới trước khi đánh giá.
      </div>
    </v-alert>

    <v-alert
      v-if="report && (report.summary.waiting || report.summary.overdue)"
      :type="report.summary.overdue ? 'error' : 'warning'"
      variant="tonal"
      class="mb-4"
      border="start"
      prominent
    >
      <div class="d-flex align-center justify-space-between flex-wrap ga-2">
        <div>
          <strong>{{ report.summary.overdue }} hội thoại quá hạn</strong>
          <span class="mx-1">·</span>
          {{ report.summary.waiting }} hội thoại đang chờ Fanpage phản hồi.
          <div class="text-caption mt-1">Hàng chờ là trạng thái hiện tại và không bị giới hạn bởi bộ lọc ngày.</div>
        </div>
        <v-btn size="small" variant="outlined" @click="statusFilter = report.summary.overdue ? 'overdue' : 'waiting'">
          Xem hàng chờ
        </v-btn>
      </div>
    </v-alert>

    <v-card class="mb-4" variant="outlined">
      <v-card-text>
        <div class="d-flex flex-wrap align-end ga-3">
          <v-select
            v-model="channelId"
            :items="channelOptions"
            item-title="title"
            item-value="value"
            label="Fanpage"
            density="compact"
            variant="outlined"
            hide-details
            style="min-width: 220px; max-width: 320px"
          />
          <v-text-field v-model="dateFrom" label="Từ ngày" type="date" density="compact" variant="outlined" hide-details style="max-width: 170px" />
          <v-text-field v-model="dateTo" label="Đến ngày" type="date" density="compact" variant="outlined" hide-details style="max-width: 170px" />
          <v-btn variant="tonal" color="primary" prepend-icon="mdi-filter" :disabled="!dateRangeValid" @click="loadReport(true)">
            Áp dụng
          </v-btn>
          <span v-if="!dateRangeValid" class="text-caption text-error">Ngày kết thúc phải từ ngày bắt đầu trở đi.</span>
          <v-spacer />
          <span v-if="report" class="text-caption text-medium-emphasis">
            Cập nhật báo cáo {{ formatDateTime(report.generated_at) }} · {{ report.policy.timezone }} · tự làm mới mỗi 60 giây
          </span>
        </div>
      </v-card-text>
    </v-card>

    <MetaLabelsPanel class="mb-6" />

    <v-progress-linear v-if="loading && report" indeterminate color="primary" class="mb-3" />

    <v-alert v-if="errorMessage" type="error" variant="tonal" class="mb-4" closable @click:close="errorMessage = ''">
      <div>{{ errorMessage }}</div>
      <v-btn class="mt-2" size="small" variant="outlined" @click="loadReport(true)">Thử lại</v-btn>
    </v-alert>

    <template v-if="loading && !report">
      <v-row class="mb-2">
        <v-col v-for="n in 8" :key="n" cols="12" sm="6" lg="3"><v-skeleton-loader type="article" /></v-col>
      </v-row>
      <v-skeleton-loader type="table" />
    </template>

    <template v-else-if="report">
      <v-alert v-if="report.invalid_timestamps" type="warning" variant="tonal" density="compact" class="mb-4">
        Bỏ qua {{ report.invalid_timestamps }} tin nhắn có thời gian không hợp lệ hoặc nằm trong tương lai.
      </v-alert>

      <v-row class="mb-2">
        <v-col v-for="kpi in kpis" :key="kpi.label" cols="12" sm="6" lg="3">
          <v-card variant="outlined" class="h-100 kpi-card">
            <v-card-text class="d-flex align-center ga-3">
              <v-avatar :color="kpi.color" variant="tonal" size="44"><v-icon :icon="kpi.icon" /></v-avatar>
              <div>
                <div class="text-caption text-medium-emphasis">{{ kpi.label }}</div>
                <div class="text-h6 font-weight-bold">{{ kpi.value }}</div>
                <div v-if="kpi.hint" class="text-caption text-medium-emphasis">{{ kpi.hint }}</div>
              </div>
            </v-card-text>
          </v-card>
        </v-col>
      </v-row>

      <v-card variant="outlined" class="mb-4">
        <v-card-title class="text-subtitle-1 font-weight-bold d-flex align-center flex-wrap ga-2">
          <v-icon start color="primary">mdi-cloud-sync</v-icon>
          Nguồn dữ liệu Messenger
          <v-spacer />
          <span class="text-caption font-weight-regular text-medium-emphasis">{{ report.conversations_scanned }} hội thoại đã quét</span>
        </v-card-title>
        <v-divider />
        <v-card-text v-if="report.pages.length" class="d-flex flex-wrap ga-2">
          <v-chip
            v-for="page in report.pages"
            :key="page.id"
            :color="syncColor(page)"
            variant="tonal"
            size="small"
          >
            <v-icon start :icon="page.is_active ? 'mdi-facebook' : 'mdi-link-off'" />
            {{ page.name }} · {{ page.last_sync_at ? formatDateTime(page.last_sync_at) : 'chưa đồng bộ' }} · {{ syncLabel(page.last_sync_status) }}
          </v-chip>
        </v-card-text>
        <v-card-text v-else class="text-center py-8">
          <v-icon size="42" color="grey">mdi-facebook</v-icon>
          <div class="text-subtitle-1 mt-2">Chưa có Fanpage Facebook</div>
          <v-btn v-if="authStore.canView('channels')" class="mt-3" color="primary" variant="tonal" :to="`/${tenantId}/channels`">Kết nối Fanpage</v-btn>
        </v-card-text>
      </v-card>

      <v-card variant="outlined" class="mb-4">
        <v-card-title class="d-flex align-center flex-wrap ga-2">
          <div>
            <div class="text-subtitle-1 font-weight-bold"><v-icon start color="primary">mdi-chart-box-outline</v-icon>Nội dung trao đổi</div>
            <div class="panel-description text-medium-emphasis mt-1">
              Chỉ tổng hợp {{ insightAggregates.eligibleConversations }} hội thoại có phân tích AI mới, được tạo trong khoảng ngày đã chọn.
              Đã loại {{ insightAggregates.excludedStale }} kết quả cũ sau khi hội thoại thay đổi và {{ insightAggregates.excludedOutsideWindow }} kết quả ngoài kỳ.
            </div>
          </div>
          <v-spacer />
          <v-btn v-if="authStore.canEdit('jobs')" size="small" variant="text" color="primary" :to="`/${tenantId}/jobs/create?template=messenger-insights`">
            Cấu hình tác vụ AI
          </v-btn>
        </v-card-title>
        <v-divider />
        <v-card-text>
          <v-row>
            <v-col v-for="group in insightGroups" :key="group.title" cols="12" md="6" lg="3">
              <div class="text-subtitle-2 font-weight-bold mb-2"><v-icon start size="small" :icon="group.icon" />{{ group.title }}</div>
              <div v-if="group.items.length" class="d-flex flex-wrap ga-2">
                <v-chip v-for="item in group.items.slice(0, 8)" :key="item.key" size="small" variant="tonal">
                  {{ displayAggregateLabel(group.kind, item.label) }} <strong class="ml-1">{{ item.count }}</strong>
                </v-chip>
              </div>
              <div v-else class="text-body-2 text-medium-emphasis py-2">Chưa có dữ liệu trong kỳ</div>
            </v-col>
          </v-row>
        </v-card-text>
      </v-card>

      <v-card variant="outlined">
        <v-card-title class="d-flex align-center flex-wrap ga-3">
          <div class="text-subtitle-1 font-weight-bold"><v-icon start color="primary">mdi-message-alert-outline</v-icon>Hội thoại</div>
          <v-spacer />
          <v-select
            v-model="statusFilter"
            :items="statusOptions"
            item-title="title"
            item-value="value"
            label="Trạng thái"
            density="compact"
            variant="outlined"
            hide-details
            style="max-width: 190px"
          />
          <v-text-field
            v-model="search"
            label="Tìm khách hàng"
            prepend-inner-icon="mdi-magnify"
            density="compact"
            variant="outlined"
            hide-details
            clearable
            style="max-width: 260px"
          />
        </v-card-title>
        <v-divider />
        <v-data-table
          :headers="headers"
          :items="filteredRows"
          item-value="conversation_id"
          :items-per-page="20"
          no-data-text="Không có hội thoại phù hợp"
          hover
        >
          <template #item.customer_name="{ item }">
            <div class="py-2">
              <div class="font-weight-medium">{{ item.customer_name || 'Khách Messenger' }}</div>
              <div class="text-caption text-medium-emphasis">{{ item.channel_name }}</div>
            </div>
          </template>
          <template #item.status="{ item }">
            <v-chip :color="statusColor(item.status)" variant="tonal" size="small">{{ statusLabel(item.status) }}</v-chip>
          </template>
          <template #item.waiting="{ item }">
            <span v-if="item.waiting" :class="item.status === 'overdue' ? 'text-error font-weight-bold' : ''">{{ formatDuration(item.waiting.seconds) }}</span>
            <span v-else>—</span>
          </template>
          <template #item.last_message_at="{ item }">{{ item.last_message_at ? formatDateTime(item.last_message_at) : '—' }}</template>
          <template #item.insight="{ item }">
            <v-chip v-if="item.insight" :color="item.insight_stale ? 'warning' : 'success'" size="x-small" variant="tonal">
              {{ item.insight_stale ? 'Cần phân tích lại' : 'Đã phân tích' }}
            </v-chip>
            <span v-else class="text-medium-emphasis">Chưa có</span>
          </template>
          <template #item.actions="{ item }">
            <div class="d-flex ga-1 justify-end">
              <v-btn icon="mdi-eye-outline" variant="text" size="small" title="Xem chi tiết" @click="openDetail(item)" />
              <v-btn
                icon="mdi-open-in-new"
                variant="text"
                size="small"
                color="primary"
                title="Mở tin nhắn"
                :to="messageLink(item)"
              />
              <v-btn
                v-if="item.waiting && canResolve"
                icon="mdi-check-circle-outline"
                variant="text"
                size="small"
                color="success"
                title="Đánh dấu đã xử lý"
                @click="openResolve(item)"
              />
            </div>
          </template>
        </v-data-table>
      </v-card>
    </template>

    <v-dialog v-model="detailDialog" max-width="760" scrollable>
      <v-card v-if="selectedRow">
        <v-card-title class="d-flex align-center">
          Chi tiết · {{ selectedRow.customer_name || 'Khách Messenger' }}
          <v-spacer />
          <v-btn icon="mdi-close" variant="text" @click="detailDialog = false" />
        </v-card-title>
        <v-divider />
        <v-card-text>
          <p class="text-caption text-medium-emphasis">Lịch sử đã đồng bộ từ {{ selectedRow.history_from ? formatDateTime(selectedRow.history_from) : 'chưa rõ' }}. Lượt đầu là lượt đầu quan sát được, có thể không phải lần liên hệ đầu tiên của khách.</p>
          <v-alert v-if="selectedRow.insight_stale" type="warning" variant="tonal" density="compact" class="mb-4">
            Hội thoại có tin nhắn mới sau lần AI phân tích. Nội dung bên dưới có thể đã cũ.
          </v-alert>
          <template v-if="selectedRow.insight">
            <div class="text-subtitle-2 font-weight-bold mb-1">Tóm tắt</div>
            <p class="text-body-2">{{ selectedRow.insight.summary || 'Chưa có tóm tắt.' }}</p>
            <div class="text-subtitle-2 font-weight-bold mb-2">Nhu cầu</div>
            <div class="d-flex flex-wrap ga-2 mb-4">
              <v-chip v-for="intent in selectedRow.insight.intents || []" :key="intent" size="small" variant="tonal">{{ intent }}</v-chip>
              <span v-if="!selectedRow.insight.intents?.length" class="text-body-2 text-medium-emphasis">Chưa nhận diện</span>
            </div>
            <div class="text-subtitle-2 font-weight-bold mb-2">Sản phẩm được hỏi</div>
            <v-list v-if="selectedRow.insight.products?.length" density="compact" class="mb-3">
              <v-list-item v-for="(product, index) in selectedRow.insight.products" :key="`${product.name}-${index}`" prepend-icon="mdi-package-variant">
                <v-list-item-title>{{ product.name }} <v-chip v-if="product.sku" size="x-small" class="ml-1">SKU {{ product.sku }}</v-chip></v-list-item-title>
                <v-list-item-subtitle>{{ product.evidence || 'Không có trích dẫn' }}</v-list-item-subtitle>
              </v-list-item>
            </v-list>
            <div v-else class="text-body-2 text-medium-emphasis mb-4">Chưa nhận diện sản phẩm</div>
            <div class="text-subtitle-2 font-weight-bold mb-2">Feedback</div>
            <v-list v-if="selectedRow.insight.feedback?.length" density="compact" class="mb-3">
              <v-list-item v-for="(feedback, index) in selectedRow.insight.feedback" :key="`${feedback.category}-${index}`" prepend-icon="mdi-comment-quote-outline">
                <v-list-item-title>{{ feedback.category }} · {{ sentimentLabel(feedback.sentiment) }}</v-list-item-title>
                <v-list-item-subtitle>{{ feedback.evidence || 'Không có trích dẫn' }}</v-list-item-subtitle>
              </v-list-item>
            </v-list>
            <div v-else class="text-body-2 text-medium-emphasis mb-4">Chưa có feedback được nhận diện</div>
            <div class="text-subtitle-2 font-weight-bold mb-2">Chất lượng khách hàng</div>
            <v-alert v-if="selectedRow.insight.lead_quality" :type="leadAlertType(selectedRow.insight.lead_quality.level)" variant="tonal" density="compact">
              <strong>{{ leadLabel(selectedRow.insight.lead_quality.level) }}</strong>
              <div>{{ selectedRow.insight.lead_quality.reason || selectedRow.insight.lead_quality.evidence || 'Không có giải thích.' }}</div>
              <div v-if="selectedRow.insight.lead_quality.reason && selectedRow.insight.lead_quality.evidence" class="text-caption mt-1">{{ selectedRow.insight.lead_quality.evidence }}</div>
            </v-alert>
          </template>
          <v-alert v-else type="info" variant="tonal" density="compact">
            Chưa có kết quả phân tích nội dung cho hội thoại này. Tạo hoặc chạy tác vụ “Messenger Insights” để bổ sung.
          </v-alert>

          <v-divider class="my-4" />
          <div class="text-subtitle-2 font-weight-bold mb-2">Các lượt chờ trong kỳ</div>
          <v-table density="compact">
            <thead><tr><th>Bắt đầu</th><th>Trạng thái</th><th>Thời gian trực</th><th>Số tin khách</th></tr></thead>
            <tbody>
              <tr v-for="(turn, index) in selectedRow.turns" :key="index">
                <td>{{ formatDateTime(turn.started_at) }}</td>
                <td>{{ statusLabel(turn.status) }}</td>
                <td>{{ formatDuration(turn.seconds) }}</td>
                <td>{{ turn.customer_messages }}</td>
              </tr>
              <tr v-if="!selectedRow.turns.length"><td colspan="4" class="text-medium-emphasis text-center py-4">Không có lượt chờ trong kỳ</td></tr>
            </tbody>
          </v-table>
        </v-card-text>
        <v-card-actions>
          <span v-if="selectedRow.resolution_note" class="text-caption text-medium-emphasis px-2">Lý do xử lý: {{ selectedRow.resolution_note }}</span>
          <v-spacer />
          <v-btn color="primary" :to="messageLink(selectedRow)">Mở hội thoại</v-btn>
        </v-card-actions>
      </v-card>
    </v-dialog>

    <v-dialog v-model="resolveDialog" max-width="520">
      <v-card>
        <v-card-title>Đánh dấu đã xử lý</v-card-title>
        <v-card-text>
          <p class="text-body-2 mb-3">
            Ghi nhận hội thoại đã được xử lý ngoài Messenger hoặc không cần Fanpage phản hồi. Thao tác này không được tính là một phản hồi đúng hạn.
          </p>
          <v-textarea
            v-model="resolveNote"
            label="Lý do xử lý *"
            placeholder="Ví dụ: Đã gọi điện xác nhận với khách"
            rows="3"
            maxlength="500"
            counter
            autofocus
          />
        </v-card-text>
        <v-card-actions>
          <v-spacer />
          <v-btn variant="text" @click="resolveDialog = false">Hủy</v-btn>
          <v-btn color="success" :loading="resolving" :disabled="!resolveNote.trim()" @click="resolveConversation">Xác nhận đã xử lý</v-btn>
        </v-card-actions>
      </v-card>
    </v-dialog>

    <v-dialog v-model="policyDialog" max-width="560">
      <v-card>
        <v-card-title>Ngưỡng phản hồi Messenger</v-card-title>
        <v-card-text>
          <v-alert type="info" variant="tonal" density="compact" class="mb-4">Thời gian chờ chỉ cộng trong giờ trực đã cấu hình, áp dụng cho tất cả các ngày.</v-alert>
          <v-switch v-model="policyForm.all_day" label="Trực 24/7" color="primary" />
          <v-row v-if="!policyForm.all_day">
            <v-col cols="6"><v-text-field v-model="policyForm.work_start" label="Bắt đầu trực" type="time" /></v-col>
            <v-col cols="6"><v-text-field v-model="policyForm.work_end" label="Kết thúc trực" type="time" /></v-col>
          </v-row>
          <v-text-field v-model.number="policyForm.target_minutes" label="Mục tiêu phản hồi (phút)" type="number" min="1" max="1440" />
          <v-text-field v-model.number="policyForm.overdue_minutes" label="Đánh dấu quá hạn sau (phút)" type="number" min="1" max="1440" />
          <v-text-field v-model="policyForm.timezone" label="Múi giờ" hint="Ví dụ: Asia/Ho_Chi_Minh" persistent-hint />
        </v-card-text>
        <v-card-actions>
          <v-spacer />
          <v-btn variant="text" @click="policyDialog = false">Hủy</v-btn>
          <v-btn color="primary" :loading="savingPolicy" :disabled="!policyValid" @click="savePolicy">Lưu cấu hình</v-btn>
        </v-card-actions>
      </v-card>
    </v-dialog>

    <v-snackbar v-model="snackbar" :color="snackColor" timeout="3500">{{ snackText }}</v-snackbar>
  </div>
</template>

<script setup lang="ts">
import MetaLabelsPanel from '../components/MetaLabelsPanel.vue'
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useRoute } from 'vue-router'
import api from '../api'
import { useAuthStore } from '../stores/auth'
import {
  aggregateInsights,
  formatDuration,
  type ConversationInsight,
  type ServiceQualityStatus,
} from '../utils/service-quality'

interface Policy {
  timezone: string
  work_start: string
  work_end: string
  all_day: boolean
  target_minutes: number
  overdue_minutes: number
}

interface Page {
  id: string
  name: string
  last_sync_at: string | null
  last_sync_status: string
  is_active: boolean
}

interface Turn {
  started_at: string
  replied_at?: string
  last_customer_id: string
  last_customer_at: string
  seconds: number
  customer_messages: number
  first: boolean
  status: ServiceQualityStatus
}

interface Row {
  conversation_id: string
  customer_name: string
  channel_id: string
  channel_name: string
  last_message_at: string | null
  status: ServiceQualityStatus
  waiting?: Turn
  turns: Turn[]
  insight?: ConversationInsight
  insight_at?: string
  insight_stale: boolean
  resolution_note?: string
  history_from?: string
}

interface Report {
  policy: Policy
  generated_at: string
  from: string
  to: string
  pages: Page[]
  summary: {
    answered: number
    on_time: number
    on_time_percent: number | null
    median_seconds: number | null
    p90_seconds: number | null
    first_median_seconds: number | null
    waiting: number
    overdue: number
    resolved: number
  }
  rows: Row[]
  invalid_timestamps: number
  conversations_scanned: number
}

const route = useRoute()
const authStore = useAuthStore()
const tenantId = computed(() => route.params.tenantId as string)
const loading = ref(false)
const report = ref<Report | null>(null)
const pageCatalog = ref<Page[]>([])
const errorMessage = ref('')
const channelId = ref('')
const statusFilter = ref('all')
const search = ref('')
const detailDialog = ref(false)
const selectedRow = ref<Row | null>(null)
const resolveDialog = ref(false)
const resolveTarget = ref<Row | null>(null)
const resolveNote = ref('')
const resolving = ref(false)
const policyDialog = ref(false)
const savingPolicy = ref(false)
const policyForm = ref<Policy>({ timezone: 'Asia/Ho_Chi_Minh', work_start: '08:00', work_end: '22:00', all_day: false, target_minutes: 5, overdue_minutes: 15 })
const snackbar = ref(false)
const snackText = ref('')
const snackColor = ref('success')
let refreshTimer: number | undefined
let activeController: AbortController | null = null
let requestSequence = 0

function localDate(offsetDays = 0) {
  const parts = new Intl.DateTimeFormat('en-CA', { timeZone: 'Asia/Ho_Chi_Minh', year: 'numeric', month: '2-digit', day: '2-digit' }).formatToParts(new Date())
  const part = (name: string) => parts.find(p => p.type === name)!.value
  const date = new Date(`${part('year')}-${part('month')}-${part('day')}T12:00:00Z`)
  date.setUTCDate(date.getUTCDate() + offsetDays)
  return date.toISOString().slice(0, 10)
}

const dateFrom = ref(localDate(-6))
const dateTo = ref(localDate())
const dateRangeValid = computed(() => Boolean(dateFrom.value && dateTo.value && dateFrom.value <= dateTo.value))
const canResolve = computed(() => authStore.canEdit('messages'))
const canEditPolicy = computed(() => authStore.canEdit('settings'))
const channelOptions = computed(() => [
  { title: 'Tất cả Fanpage', value: '' },
  ...pageCatalog.value.map(page => ({ title: page.name, value: page.id })),
])
const statusOptions = [
  { title: 'Tất cả trạng thái', value: 'all' },
  { title: 'Quá hạn', value: 'overdue' },
  { title: 'Đang chờ', value: 'waiting' },
  { title: 'Đã phản hồi', value: 'answered' },
  { title: 'Đã xử lý', value: 'resolved' },
  { title: 'Không có yêu cầu', value: 'no_request' },
]
const headers = [
  { title: 'Khách hàng / Fanpage', key: 'customer_name', sortable: true },
  { title: 'Trạng thái', key: 'status', sortable: true },
  { title: 'Đang chờ', key: 'waiting', sortable: false },
  { title: 'Tin cuối', key: 'last_message_at', sortable: true },
  { title: 'AI nội dung', key: 'insight', sortable: false },
  { title: '', key: 'actions', sortable: false, align: 'end' as const },
]

const filteredRows = computed(() => {
  const term = search.value.trim().toLocaleLowerCase('vi')
  return (report.value?.rows || []).filter(row => {
    if (statusFilter.value !== 'all' && row.status !== statusFilter.value) return false
    if (!term) return true
    return `${row.customer_name} ${row.channel_name} ${row.insight?.summary || ''}`.toLocaleLowerCase('vi').includes(term)
  })
})

const insightAggregates = computed(() => report.value
  ? aggregateInsights(report.value.rows, report.value.from, report.value.to)
  : aggregateInsights([], new Date(0), new Date(0)))

const insightGroups = computed(() => [
  { title: 'Nhu cầu chính', icon: 'mdi-cart-outline', kind: 'intent', items: insightAggregates.value.intents },
  { title: 'Sản phẩm được hỏi', icon: 'mdi-package-variant-closed', kind: 'product', items: insightAggregates.value.products },
  { title: 'Feedback khách hàng', icon: 'mdi-comment-quote-outline', kind: 'feedback', items: insightAggregates.value.feedback },
  { title: 'Chất lượng khách hàng', icon: 'mdi-account-star-outline', kind: 'lead', items: insightAggregates.value.leadQuality },
])

const kpis = computed(() => {
  const summary = report.value!.summary
  const policy = report.value!.policy
  return [
    { label: 'Lượt đã phản hồi trong kỳ', value: summary.answered.toLocaleString('vi-VN'), hint: 'Mẫu số SLA', icon: 'mdi-message-check-outline', color: 'primary' },
    { label: `Đúng hạn ≤ ${policy.target_minutes} phút`, value: summary.on_time_percent == null ? '—' : `${summary.on_time_percent.toFixed(1)}%`, hint: `${summary.on_time}/${summary.answered} lượt`, icon: 'mdi-timer-check-outline', color: 'success' },
    { label: 'Trung vị phản hồi', value: formatDuration(summary.median_seconds), hint: 'Các lượt trong kỳ', icon: 'mdi-timer-outline', color: 'info' },
    { label: 'P90 phản hồi', value: formatDuration(summary.p90_seconds), hint: '90% lượt nhanh hơn mức này', icon: 'mdi-chart-timeline-variant', color: 'deep-purple' },
    { label: 'Lượt đầu quan sát được', value: formatDuration(summary.first_median_seconds), hint: 'Trong lịch sử đã đồng bộ', icon: 'mdi-ray-start-arrow', color: 'indigo' },
    { label: 'Đang chờ hiện tại', value: summary.waiting.toLocaleString('vi-VN'), hint: 'Chưa tới ngưỡng quá hạn', icon: 'mdi-account-clock-outline', color: 'warning' },
    { label: `Quá hạn ≥ ${policy.overdue_minutes} phút`, value: summary.overdue.toLocaleString('vi-VN'), hint: 'Hàng chờ hiện tại', icon: 'mdi-alert-circle-outline', color: 'error' },
    { label: 'Đã xử lý trong kỳ', value: summary.resolved.toLocaleString('vi-VN'), hint: 'Không tính là phản hồi', icon: 'mdi-check-decagram-outline', color: 'teal' },
  ]
})

const policyValid = computed(() => {
  const p = policyForm.value
  return Boolean(p.timezone && /^\d{2}:\d{2}$/.test(p.work_start) && /^\d{2}:\d{2}$/.test(p.work_end)
    && (p.all_day || p.work_start < p.work_end) && p.target_minutes >= 1 && p.overdue_minutes >= p.target_minutes && p.overdue_minutes <= 1440)
})

async function loadReport(force = false) {
  if (!dateRangeValid.value) return
  if (loading.value && !force) return
  if (force) activeController?.abort()
  const controller = new AbortController()
  activeController = controller
  const sequence = ++requestSequence
  loading.value = true
  errorMessage.value = ''
  try {
    const { data } = await api.get<Report>(`/tenants/${tenantId.value}/service-quality`, {
      params: { channel_id: channelId.value || undefined, from: dateFrom.value, to: dateTo.value },
      signal: controller.signal,
    })
    if (sequence !== requestSequence) return
    report.value = data
    pageCatalog.value = data.pages
    if (channelId.value && !data.pages.some(page => page.id === channelId.value)) channelId.value = ''
  } catch (error: any) {
    if (error?.code === 'ERR_CANCELED' || sequence !== requestSequence) return
    report.value = null
    if (Array.isArray(error?.response?.data?.pages)) pageCatalog.value = error.response.data.pages
    errorMessage.value = error?.response?.data?.error || 'Không tải được báo cáo chất lượng CSKH. Vui lòng thử lại.'
  } finally {
    if (sequence === requestSequence) loading.value = false
  }
}

function openDetail(row: Row) {
  selectedRow.value = row
  detailDialog.value = true
}

function openResolve(row: Row) {
  resolveTarget.value = row
  resolveNote.value = ''
  resolveDialog.value = true
}

async function resolveConversation() {
  const target = resolveTarget.value
  if (!target?.waiting || !resolveNote.value.trim()) return
  resolving.value = true
  const targetTenant = tenantId.value
  try {
    await api.post(`/tenants/${targetTenant}/service-quality/${target.conversation_id}/resolve`, {
      through_message_id: target.waiting.last_customer_id,
      note: resolveNote.value.trim(),
    })
    if (tenantId.value !== targetTenant) return
    resolveDialog.value = false
    showSnack('Đã ghi nhận hội thoại được xử lý', 'success')
    await loadReport(true)
  } catch (error: any) {
    if (tenantId.value !== targetTenant) return
    const conflict = error?.response?.status === 409
    showSnack(error?.response?.data?.error || 'Không thể ghi nhận xử lý', 'error')
    if (conflict) {
      resolveDialog.value = false
      await loadReport(true)
    }
  } finally {
    resolving.value = false
  }
}

function openPolicyDialog() {
  if (report.value) policyForm.value = { ...report.value.policy }
  policyDialog.value = true
}

async function savePolicy() {
  if (!policyValid.value) return
  savingPolicy.value = true
  const targetTenant = tenantId.value
  try {
    await api.put(`/tenants/${targetTenant}/service-quality/policy`, policyForm.value)
    if (tenantId.value !== targetTenant) return
    policyDialog.value = false
    showSnack('Đã lưu ngưỡng phản hồi', 'success')
    await loadReport(true)
  } catch (error: any) {
    if (tenantId.value !== targetTenant) return
    showSnack(error?.response?.data?.error || 'Không lưu được cấu hình', 'error')
  } finally {
    savingPolicy.value = false
  }
}

function messageLink(row: Row) {
  return { name: 'messages', params: { tenantId: tenantId.value }, query: { conv: row.conversation_id, channel_id: row.channel_id } }
}

function formatDateTime(value: string) {
  const date = new Date(value)
  return Number.isNaN(date.getTime()) ? '—' : date.toLocaleString('vi-VN', { dateStyle: 'short', timeStyle: 'short', timeZone: report.value?.policy.timezone || 'Asia/Ho_Chi_Minh' })
}

function statusLabel(status: ServiceQualityStatus) {
  return ({ answered: 'Đã phản hồi', waiting: 'Đang chờ', overdue: 'Quá hạn', resolved: 'Đã xử lý', no_request: 'Không có yêu cầu' } as Record<string, string>)[status] || status
}

function statusColor(status: ServiceQualityStatus) {
  return ({ answered: 'success', waiting: 'warning', overdue: 'error', resolved: 'teal', no_request: 'grey' } as Record<string, string>)[status] || 'grey'
}

function sentimentLabel(sentiment: string) {
  return ({ positive: 'Tích cực', neutral: 'Trung lập', negative: 'Tiêu cực', mixed: 'Trái chiều', unknown: 'Chưa rõ' } as Record<string, string>)[sentiment] || sentiment
}

function leadLabel(level: string) {
  return ({ high: 'Tiềm năng cao', medium: 'Tiềm năng vừa', low: 'Tiềm năng thấp', spam: 'Spam', unknown: 'Chưa rõ' } as Record<string, string>)[level] || level
}

function leadAlertType(level: string): 'success' | 'info' | 'warning' | 'error' {
  return ({ high: 'success', medium: 'info', low: 'warning', spam: 'error', unknown: 'info' } as Record<string, any>)[level] || 'info'
}

function displayAggregateLabel(kind: string, label: string) {
  if (kind === 'lead') return leadLabel(label)
  if (kind === 'feedback') {
    const [category, sentiment] = label.split(' · ')
    return `${category} · ${sentimentLabel(sentiment)}`
  }
  return label
}

function syncColor(page: Page) {
  if (!page.is_active || page.last_sync_status === 'error') return 'error'
  if (!page.last_sync_at || page.last_sync_status === 'syncing') return 'warning'
  if (Date.now() - new Date(page.last_sync_at).getTime() > 30 * 60_000) return 'warning'
  return 'success'
}

function syncLabel(status: string) {
  return ({ success: 'đồng bộ thành công', syncing: 'đang đồng bộ', error: 'lỗi đồng bộ', pending: 'chờ đồng bộ' } as Record<string, string>)[status] || status || 'chưa rõ trạng thái'
}

function showSnack(message: string, color: string) {
  snackText.value = message
  snackColor.value = color
  snackbar.value = true
}

watch(tenantId, () => {
  requestSequence++
  activeController?.abort()
  report.value = null
  pageCatalog.value = []
  detailDialog.value = false
  resolveDialog.value = false
  policyDialog.value = false
  selectedRow.value = null
  resolveTarget.value = null
  channelId.value = ''
  statusFilter.value = 'all'
  loadReport(true)
})

onMounted(() => {
  loadReport(true)
  refreshTimer = window.setInterval(() => loadReport(false), 60_000)
})

onBeforeUnmount(() => {
  requestSequence++
  activeController?.abort()
  if (refreshTimer) window.clearInterval(refreshTimer)
})
</script>

<style scoped>
.service-quality-page {
  max-width: 1680px;
  margin: 0 auto;
}

.kpi-card {
  min-height: 104px;
}

.panel-description {
  font-size: 13px;
  font-weight: 400;
  line-height: 1.5;
  letter-spacing: normal;
  white-space: normal;
  overflow-wrap: anywhere;
}

.service-quality-page :deep(.v-card-title) {
  white-space: normal;
  overflow-wrap: anywhere;
}

.service-quality-page :deep(.v-card-title > div) {
  min-width: 0;
  max-width: 100%;
}

.service-quality-page :deep(.v-chip) {
  max-width: 100%;
}

.service-quality-page :deep(.v-chip__content) {
  overflow: hidden;
  text-overflow: ellipsis;
}

@media (max-width: 600px) {
  :deep(.v-data-table__td),
  :deep(.v-data-table__th) {
    white-space: nowrap;
  }
}
</style>
