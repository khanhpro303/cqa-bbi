<template>
  <v-card class="pa-6 mb-6" data-testid="cloudify-order-watch">
    <!-- Header with status icon, title, Quét ngay button, and config button -->
    <div class="d-flex align-center mb-2">
      <v-icon :color="statusColor" class="mr-2">mdi-package-variant-closed</v-icon>
      <div class="text-subtitle-1 font-weight-bold flex-grow-1">Báo đơn hàng Cloudify cho khách qua Zalo</div>
      <v-btn
        size="small"
        variant="tonal"
        color="primary"
        class="mr-2"
        :loading="polling"
        :disabled="!form.watch_enabled"
        prepend-icon="mdi-refresh"
        @click="pollNow"
      >
        Quét ngay
      </v-btn>
      <v-btn
        icon="mdi-cog-outline"
        size="small"
        variant="text"
        color="primary"
        title="Cấu hình chi tiết"
        @click="panelOpen = !panelOpen"
      />
    </div>

    <div class="text-body-2 text-grey-darken-1 mb-3">
      Tự động quét đơn hàng Cloudify và gửi thông báo qua Zalo cho khách hàng.
    </div>

    <v-alert v-if="error" type="error" variant="tonal" density="compact" class="mb-3">{{ error }}</v-alert>
    <v-alert v-if="data?.status.last_error" type="warning" variant="tonal" density="compact" class="mb-3" data-testid="watch-error">
      {{ data.status.last_error }}
    </v-alert>

    <div v-if="data" class="text-caption text-grey mb-3" data-testid="watch-status">
      <template v-if="data.status.last_ok_at">
        Quét thành công lúc {{ fmt(data.status.last_ok_at) }} · {{ data.status.last_count }} đơn trong cửa sổ
      </template>
      <template v-else-if="form.watch_enabled">Đang chờ lần quét đầu tiên…</template>
      <template v-else>Chưa bật.</template>
    </div>

    <!-- Compact Toggles List -->
    <div class="d-flex flex-column ga-2 mt-2">
      <!-- Master watch toggle -->
      <div class="d-flex align-center justify-space-between pa-3 rounded toggle-row">
        <div class="d-flex align-center ga-2">
          <v-icon size="20" :color="form.watch_enabled ? 'primary' : 'grey'">mdi-radar</v-icon>
          <div>
            <div class="text-body-2 font-weight-medium">Theo dõi đơn hàng Cloudify</div>
            <div class="text-caption text-grey">Quét đơn định kỳ để phát hiện đơn mới hoặc đổi trạng thái</div>
          </div>
        </div>
        <div class="d-flex align-center ga-2" @click.stop>
          <v-chip size="x-small" :color="form.watch_enabled ? 'success' : 'grey'" variant="tonal">
            {{ form.watch_enabled ? 'BẬT' : 'TẮT' }}
          </v-chip>
          <v-switch
            v-model="form.watch_enabled"
            color="primary"
            density="compact"
            hide-details
            @update:model-value="save"
          />
        </div>
      </div>

      <!-- Notify toggle -->
      <div class="d-flex align-center justify-space-between pa-3 rounded toggle-row">
        <div class="d-flex align-center ga-2">
          <v-icon size="20" :color="form.notify_enabled ? 'primary' : 'grey'">mdi-bell-ring-outline</v-icon>
          <div>
            <div class="text-body-2 font-weight-medium">Nhắn Zalo cho khách khi có thay đổi</div>
            <div class="text-caption text-grey">Gửi tin nhắn vào nhóm Zalo gắn mã khách hàng của đơn</div>
          </div>
        </div>
        <div class="d-flex align-center ga-2" @click.stop>
          <v-chip size="x-small" :color="form.notify_enabled ? 'success' : 'grey'" variant="tonal">
            {{ form.notify_enabled ? 'BẬT' : 'TẮT' }}
          </v-chip>
          <v-switch
            v-model="form.notify_enabled"
            color="primary"
            density="compact"
            hide-details
            data-testid="notify-switch"
            @update:model-value="save"
          />
        </div>
      </div>

      <!-- Notify kinds toggles -->
      <div class="d-flex align-center justify-space-between pa-3 rounded toggle-row">
        <div class="d-flex align-center ga-2">
          <v-icon size="20" color="primary">mdi-format-list-checks</v-icon>
          <div>
            <div class="text-body-2 font-weight-medium">Loại sự kiện gửi thông báo</div>
            <div class="text-caption text-grey">{{ selectedKindsText }}</div>
          </div>
        </div>
        <div class="d-flex align-center ga-1 flex-wrap justify-end">
          <v-chip
            v-for="k in kindItems"
            :key="k.value"
            size="small"
            :color="form.notify_kinds.includes(k.value) ? 'primary' : undefined"
            :variant="form.notify_kinds.includes(k.value) ? 'flat' : 'outlined'"
            style="cursor: pointer;"
            @click="toggleKind(k.value)"
          >
            {{ k.title }}
          </v-chip>
        </div>
      </div>
    </div>

    <!-- Bottom link to detail config panel -->
    <div class="d-flex align-center justify-space-between mt-3 pt-1">
      <span class="text-caption text-grey">
        Nhấn biểu tượng <v-icon size="14">mdi-cog-outline</v-icon> để chỉnh mẫu tin, chu kỳ & webhook
      </span>
      <v-btn
        size="small"
        variant="text"
        color="primary"
        append-icon="mdi-arrow-right"
        @click="panelOpen = !panelOpen"
      >
        Cấu hình chi tiết
      </v-btn>
    </div>

    <!-- ===================== Side Panel: Detailed Config ===================== -->
    <v-navigation-drawer
      v-model="panelOpen"
      location="right"
      :width="540"
      temporary
      :scrim="false"
      elevation="6"
      class="border-s"
      style="z-index: 1200;"
    >
      <div class="d-flex align-center pa-4 pb-2 border-b">
        <v-icon start color="primary" class="mr-2">mdi-package-variant-closed</v-icon>
        <div class="text-subtitle-1 font-weight-bold flex-grow-1">Cấu hình báo đơn hàng Cloudify</div>
        <v-btn icon="mdi-close" variant="text" size="small" @click="panelOpen = false" />
      </div>

      <div class="pa-4 overflow-y-auto" style="max-height: calc(100vh - 120px);">
        <!-- Basic info alert -->
        <v-alert color="info" variant="tonal" density="compact" class="mb-4 text-caption">
          Cloudify không có webhook, nên CQA quét đơn trong vài ngày gần đây theo chu kỳ để phát hiện đơn mới
          hoặc đơn đổi trạng thái. Lần quét đầu tiên chỉ ghi nhận, không nhắn khách.
        </v-alert>

        <!-- Polling & Window settings -->
        <div class="text-subtitle-2 font-weight-bold mb-3">1. Chu kỳ & Phạm vi quét</div>
        <v-row dense class="mb-3">
          <v-col cols="12" sm="6">
            <v-text-field
              v-model.number="form.poll_seconds"
              type="number"
              min="15"
              max="3600"
              label="Chu kỳ quét (giây)"
              density="compact"
              variant="outlined"
            />
          </v-col>
          <v-col cols="12" sm="6">
            <v-text-field
              v-model.number="form.window_days"
              type="number"
              min="1"
              max="60"
              label="Quét đơn trong (ngày)"
              density="compact"
              variant="outlined"
            />
          </v-col>
          <v-col cols="12">
            <v-select
              v-model="form.notify_kinds"
              :items="kindItems"
              multiple
              chips
              closable-chips
              label="Sự kiện thông báo"
              density="compact"
              variant="outlined"
            />
          </v-col>
        </v-row>

        <v-divider class="my-4" />

        <!-- Templates settings -->
        <div class="text-subtitle-2 font-weight-bold mb-2">2. Mẫu tin nhắn Zalo</div>
        <div class="text-caption text-grey mb-3">
          Biến dùng được: <code v-pre>{ma_don}</code> <code v-pre>{khach_hang}</code> <code v-pre>{trang_thai}</code> <code v-pre>{trang_thai_cu}</code> <code v-pre>{tong_tien}</code> <code v-pre>{ngay}</code>. Để trống để dùng mẫu mặc định.
        </div>

        <v-textarea
          v-model="form.template_created"
          rows="2"
          auto-grow
          label="Mẫu tin: đơn mới"
          density="compact"
          variant="outlined"
          class="mb-3"
        />
        <v-textarea
          v-model="form.template_status_changed"
          rows="2"
          auto-grow
          label="Mẫu tin: đổi trạng thái"
          density="compact"
          variant="outlined"
          class="mb-3"
        />
        <v-textarea
          v-model="form.template_updated"
          rows="2"
          auto-grow
          label="Mẫu tin: đơn được sửa"
          density="compact"
          variant="outlined"
          class="mb-3"
        />

        <div class="d-flex justify-end mb-4">
          <v-btn color="primary" :loading="saving" @click="save" data-testid="save-watch">
            Lưu cấu hình
          </v-btn>
        </div>

        <v-divider class="my-4" />

        <!-- Webhook optional setup -->
        <div class="text-subtitle-2 font-weight-bold mb-1">3. Webhook (tuỳ chọn, để báo trong ~1 giây)</div>
        <div class="text-caption text-grey mb-3">
          Nếu Cloudify bật được "Automated Action → Send Webhook Notification" cho đơn bán hàng, dán URL này vào đó.
          CQA sẽ đọc lại đơn ngay khi nhận tín hiệu thay vì đợi lượt quét.
        </div>
        <div class="d-flex align-center ga-2 mb-4">
          <v-text-field
            :model-value="webhookUrl || (data?.webhook.has_secret ? 'Đã tạo. Tạo lại để xem URL mới.' : 'Chưa tạo')"
            readonly
            density="compact"
            variant="outlined"
            hide-details
            :append-inner-icon="webhookUrl ? 'mdi-content-copy' : undefined"
            @click:append-inner="copy"
          />
          <v-btn variant="tonal" :loading="rotating" @click="rotate">
            {{ data?.webhook.has_secret ? 'Tạo lại' : 'Tạo URL' }}
          </v-btn>
        </div>

        <v-divider class="my-4" />

        <!-- Recent events table -->
        <div class="text-subtitle-2 font-weight-bold mb-2">4. Thay đổi gần đây</div>
        <v-table v-if="data?.events?.length" density="compact" data-testid="watch-events">
          <thead>
            <tr><th>Lúc</th><th>Đơn</th><th>Khách</th><th>Thay đổi</th><th>Gửi Zalo</th></tr>
          </thead>
          <tbody>
            <tr v-for="e in data.events" :key="e.ID">
              <td class="text-caption">{{ fmt(e.CreatedAt) }}</td>
              <td>{{ e.OrderCode }}</td>
              <td>{{ e.CustomerCode }}</td>
              <td>{{ describe(e) }}</td>
              <td class="text-caption">{{ deliveryLabel(e) }}</td>
            </tr>
          </tbody>
        </v-table>
        <div v-else class="text-caption text-grey">Chưa có thay đổi nào.</div>

        <!-- Footer actions inside panel -->
        <div class="d-flex justify-end ga-2 mt-6 pt-3 border-t">
          <v-btn variant="tonal" color="grey" @click="panelOpen = false">
            Đóng
          </v-btn>
          <v-btn color="primary" :loading="saving" @click="save">
            Lưu cấu hình
          </v-btn>
        </div>
      </div>
    </v-navigation-drawer>
  </v-card>
</template>

<script setup lang="ts">
import { ref, reactive, computed, onMounted, onUnmounted } from 'vue'
import api from '../api'

const props = defineProps<{ tenantId: string }>()

interface WatchEvent {
  ID: string; Kind: string; OrderCode: string; CustomerCode: string; PrevStatus: string; Status: string
  DeliveryStatus: string; DeliveryNote: string; CreatedAt: string
}
interface WatchData {
  settings: Record<string, any>
  status: { seeded_at: string | null; last_poll_at: string | null; last_ok_at: string | null; last_error: string; last_count: number }
  webhook: { has_secret: boolean; path: string }
  events: WatchEvent[]
}

const statusNames: Record<string, string> = { '0': 'Hủy', '1': 'Đang thực hiện', '2': 'Hoàn thành', '3': 'Đang giao' }
const kindItems = [
  { title: 'Đơn mới', value: 'created' },
  { title: 'Đổi trạng thái', value: 'status_changed' },
  { title: 'Đơn được sửa', value: 'updated' },
]
const notes: Record<string, string> = {
  notify_off: 'Chưa bật nhắn khách', kind_off: 'Loại này không báo', stale: 'Quá cũ, bỏ qua',
  no_zalo_group: 'Mã KH chưa gắn nhóm Zalo', empty_template: 'Mẫu tin trống',
}

const data = ref<WatchData | null>(null)
const form = reactive<Record<string, any>>({
  watch_enabled: false, notify_enabled: false, notify_kinds: ['created', 'status_changed'], window_days: 7, poll_seconds: 30,
  template_created: '', template_status_changed: '', template_updated: '',
})
const error = ref('')
const saving = ref(false)
const polling = ref(false)
const rotating = ref(false)
const webhookUrl = ref('')
const panelOpen = ref(false)

const statusColor = computed(() => {
  if (!data.value?.settings.watch_enabled) return 'grey'
  return data.value.status.last_error ? 'warning' : 'success'
})

const selectedKindsText = computed(() => {
  if (!form.notify_kinds || form.notify_kinds.length === 0) return 'Chưa chọn loại nào'
  if (form.notify_kinds.length === kindItems.length) return 'Tất cả loại sự kiện'
  return form.notify_kinds.map((k: string) => kindItems.find(item => item.value === k)?.title || k).join(', ')
})

function toggleKind(kind: string) {
  const idx = form.notify_kinds.indexOf(kind)
  if (idx >= 0) {
    form.notify_kinds.splice(idx, 1)
  } else {
    form.notify_kinds.push(kind)
  }
  save()
}

function apply(d: WatchData) {
  data.value = d
  Object.assign(form, d.settings)
}

function fmt(s: string | null) {
  return s ? new Date(s).toLocaleString() : ''
}

function describe(e: WatchEvent) {
  if (e.Kind === 'created') return 'Đơn mới'
  if (e.Kind === 'status_changed') return `${statusNames[e.PrevStatus] || e.PrevStatus} → ${statusNames[e.Status] || e.Status}`
  return 'Đơn được sửa'
}

function deliveryLabel(e: WatchEvent) {
  if (e.DeliveryStatus === 'sent') return 'Đã gửi'
  if (e.DeliveryStatus === 'pending') return 'Đang chờ gửi'
  if (e.DeliveryStatus === 'failed') return `Lỗi: ${e.DeliveryNote}`
  return notes[e.DeliveryNote] || e.DeliveryNote
}

function message(err: any, fallback: string) {
  return err?.response?.data?.error || fallback
}

async function load() {
  if (!props.tenantId) return
  try {
    const { data: d } = await api.get(`/tenants/${props.tenantId}/cloudify-orders`)
    apply(d)
  } catch (err) {
    error.value = message(err, 'Không tải được cấu hình báo đơn hàng.')
  }
}

async function save() {
  saving.value = true
  error.value = ''
  try {
    const { data: d } = await api.put(`/tenants/${props.tenantId}/cloudify-orders`, {
      ...form,
      window_days: Number(form.window_days),
      poll_seconds: Number(form.poll_seconds),
    })
    apply(d)
  } catch (err) {
    error.value = message(err, 'Không lưu được cấu hình.')
  } finally {
    saving.value = false
  }
}

async function pollNow() {
  polling.value = true
  try {
    await api.post(`/tenants/${props.tenantId}/cloudify-orders/poll`)
    setTimeout(load, 5000)
  } catch (err) {
    error.value = message(err, 'Không gửi được yêu cầu quét.')
  } finally {
    polling.value = false
  }
}

async function rotate() {
  rotating.value = true
  try {
    const { data: d } = await api.post(`/tenants/${props.tenantId}/cloudify-orders/webhook-secret`)
    webhookUrl.value = window.location.origin + d.path
    if (data.value) data.value.webhook.has_secret = true
  } catch (err) {
    error.value = message(err, 'Không tạo được URL webhook.')
  } finally {
    rotating.value = false
  }
}

function copy() {
  if (webhookUrl.value) navigator.clipboard?.writeText(webhookUrl.value)
}

function handleKeydown(e: KeyboardEvent) {
  if (e.key === 'Escape' && panelOpen.value) {
    panelOpen.value = false
  }
}

onMounted(() => {
  window.addEventListener('keydown', handleKeydown)
  load()
})

onUnmounted(() => {
  window.removeEventListener('keydown', handleKeydown)
})

defineExpose({ load })
</script>

<style scoped>
.toggle-row {
  background-color: rgba(128, 128, 128, 0.05);
  transition: background-color 0.2s ease;
}
.toggle-row:hover {
  background-color: rgba(128, 128, 128, 0.09);
}
</style>
