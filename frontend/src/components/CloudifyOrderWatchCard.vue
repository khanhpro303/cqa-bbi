<template>
  <v-card class="pa-6 mb-6" data-testid="cloudify-order-watch">
    <div class="d-flex align-center mb-2">
      <v-icon :color="statusColor" class="mr-2">mdi-package-variant-closed</v-icon>
      <div class="text-subtitle-1 font-weight-bold">Báo đơn hàng Cloudify cho khách qua Zalo</div>
      <v-spacer />
      <v-btn size="small" variant="tonal" color="primary" :loading="polling" :disabled="!form.watch_enabled" prepend-icon="mdi-refresh" @click="pollNow">
        Quét ngay
      </v-btn>
    </div>
    <div class="text-body-2 text-grey-darken-1 mb-4">
      Cloudify không có webhook, nên CQA quét đơn trong vài ngày gần đây theo chu kỳ, so với lần trước để phát hiện đơn mới
      hoặc đơn đổi trạng thái, rồi nhắn vào nhóm Zalo đã gắn mã KH của đơn. Lần quét đầu tiên chỉ ghi nhận, không nhắn khách.
    </div>

    <v-alert v-if="error" type="error" variant="tonal" density="compact" class="mb-3">{{ error }}</v-alert>
    <v-alert v-if="data?.status.last_error" type="warning" variant="tonal" density="compact" class="mb-3" data-testid="watch-error">
      {{ data.status.last_error }}
    </v-alert>

    <div v-if="data" class="text-caption text-grey mb-4" data-testid="watch-status">
      <template v-if="data.status.last_ok_at">
        Quét thành công lúc {{ fmt(data.status.last_ok_at) }} · {{ data.status.last_count }} đơn trong cửa sổ
      </template>
      <template v-else-if="form.watch_enabled">Đang chờ lần quét đầu tiên…</template>
      <template v-else>Chưa bật.</template>
    </div>

    <v-row dense>
      <v-col cols="12" md="6">
        <v-switch v-model="form.watch_enabled" color="primary" density="compact" hide-details label="Theo dõi đơn hàng Cloudify" />
      </v-col>
      <v-col cols="12" md="6">
        <v-switch v-model="form.notify_enabled" color="primary" density="compact" hide-details label="Nhắn Zalo cho khách khi có thay đổi" data-testid="notify-switch" />
      </v-col>
      <v-col cols="12" md="4">
        <v-text-field v-model.number="form.poll_seconds" type="number" min="15" max="3600" label="Chu kỳ quét (giây)" density="compact" variant="outlined" />
      </v-col>
      <v-col cols="12" md="4">
        <v-text-field v-model.number="form.window_days" type="number" min="1" max="60" label="Quét đơn trong (ngày gần đây)" density="compact" variant="outlined" />
      </v-col>
      <v-col cols="12" md="4">
        <v-select v-model="form.notify_kinds" :items="kindItems" multiple chips closable-chips label="Báo khi" density="compact" variant="outlined" />
      </v-col>
      <v-col cols="12">
        <v-textarea v-model="form.template_created" rows="2" auto-grow label="Mẫu tin: đơn mới" density="compact" variant="outlined" />
        <v-textarea v-model="form.template_status_changed" rows="2" auto-grow label="Mẫu tin: đổi trạng thái" density="compact" variant="outlined" />
        <v-textarea v-model="form.template_updated" rows="2" auto-grow label="Mẫu tin: đơn được sửa" density="compact" variant="outlined" />
        <div class="text-caption text-grey">
          Biến dùng được: {ma_don} {khach_hang} {trang_thai} {trang_thai_cu} {tong_tien} {ngay}. Để trống để dùng mẫu mặc định.
        </div>
      </v-col>
    </v-row>

    <div class="d-flex justify-end mt-3">
      <v-btn color="primary" :loading="saving" @click="save" data-testid="save-watch">Lưu</v-btn>
    </div>

    <v-divider class="my-5" />
    <div class="text-subtitle-2 font-weight-bold mb-1">Webhook (tuỳ chọn, để báo trong ~1 giây)</div>
    <div class="text-body-2 text-grey-darken-1 mb-3">
      Nếu Cloudify bật được "Automated Action → Send Webhook Notification" cho đơn bán hàng, dán URL này vào đó.
      CQA sẽ đọc lại đơn ngay khi nhận tín hiệu thay vì đợi lượt quét.
    </div>
    <div class="d-flex align-center ga-2">
      <v-text-field
        :model-value="webhookUrl || (data?.webhook.has_secret ? 'Đã tạo. Tạo lại để xem URL mới.' : 'Chưa tạo')"
        readonly density="compact" variant="outlined" hide-details
        :append-inner-icon="webhookUrl ? 'mdi-content-copy' : undefined"
        @click:append-inner="copy"
      />
      <v-btn variant="tonal" :loading="rotating" @click="rotate">{{ data?.webhook.has_secret ? 'Tạo lại' : 'Tạo URL' }}</v-btn>
    </div>

    <v-divider class="my-5" />
    <div class="text-subtitle-2 font-weight-bold mb-2">Thay đổi gần đây</div>
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
  </v-card>
</template>

<script setup lang="ts">
import { ref, reactive, computed, onMounted } from 'vue'
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

const statusColor = computed(() => {
  if (!data.value?.settings.watch_enabled) return 'grey'
  return data.value.status.last_error ? 'warning' : 'success'
})

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

onMounted(load)
defineExpose({ load })
</script>
