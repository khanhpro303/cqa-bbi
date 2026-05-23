<template>
  <div v-if="channel">
    <!-- Header -->
    <div class="d-flex align-center mb-4 flex-wrap ga-2">
      <v-btn icon="mdi-arrow-left" variant="text" size="small" @click="router.back()" class="mr-2" />
      <h1 class="text-h5 font-weight-bold">{{ channel.name }}</h1>
      <v-spacer />
      <template v-if="authStore.canEdit('channels')">
        <v-btn variant="outlined" prepend-icon="mdi-pencil" size="small" @click="editDialog = true">{{ $t('edit') }}</v-btn>
        <v-btn v-if="channel.channel_type !== 'personal_zalo_import'" color="primary" prepend-icon="mdi-sync" size="small" :loading="syncing" @click="doSync">
          {{ $t('sync_now') || 'Dong bo ngay' }}
        </v-btn>
        <v-btn v-if="channel.channel_type !== 'personal_zalo_import'" variant="outlined" prepend-icon="mdi-connection" size="small" :loading="testing" @click="doTest">
          Kiểm tra kết nối
        </v-btn>
        <v-btn color="warning" variant="outlined" prepend-icon="mdi-delete-sweep" size="small" @click="confirmPurge = true">
          Xóa cuộc chat
        </v-btn>
        <v-btn color="error" variant="outlined" prepend-icon="mdi-delete" size="small" @click="confirmDelete = true">{{ $t('delete') }}</v-btn>
      </template>
    </div>

    <!-- Channel Info -->
    <v-card class="pa-4 mb-4">
      <div class="text-subtitle-1 font-weight-bold mb-3">
        <v-icon start size="small">mdi-information</v-icon>
        Thông tin kênh
      </div>
      <v-row>
        <v-col cols="6" sm="3">
          <div class="text-caption text-grey">Loại kênh</div>
          <v-chip size="small" :color="channelTypeColor(channel.channel_type)" variant="tonal">
            {{ channelTypeLabel(channel.channel_type) }}
          </v-chip>
        </v-col>
        <v-col cols="6" sm="3">
          <div class="text-caption text-grey">Trạng thái</div>
          <v-chip size="small" :color="channel.is_active ? 'success' : 'grey'" variant="tonal">
            {{ channel.is_active ? 'Hoạt động' : 'Tạm dừng' }}
          </v-chip>
        </v-col>
        <v-col cols="6" sm="3">
          <div class="text-caption text-grey">Trạng thái đồng bộ</div>
          <v-chip size="small" :color="channel.last_sync_status === 'success' ? 'success' : channel.last_sync_status === 'error' ? 'error' : 'grey'" variant="tonal">
            {{ channel.last_sync_status === 'success' ? 'Thành công' : channel.last_sync_status === 'error' ? 'Lỗi' : 'Chưa đồng bộ' }}
          </v-chip>
        </v-col>
        <v-col cols="6" sm="3">
          <div class="text-caption text-grey">Đồng bộ lần cuối</div>
          <div>{{ channel.last_sync_at ? formatDateTime(channel.last_sync_at) : 'Chưa đồng bộ' }}</div>
        </v-col>
        <v-col cols="6" sm="3">
          <div class="text-caption text-grey">Tổng cuộc chat</div>
          <a href="#" class="text-primary font-weight-bold" @click.prevent="goToMessages">
            {{ channel.conversation_count || 0 }}
          </a>
        </v-col>
        <template v-if="channel.channel_type !== 'personal_zalo_import'">
          <v-col cols="6" sm="3">
            <div class="text-caption text-grey">Chu kỳ đồng bộ</div>
            <div>{{ formatSyncInterval(metadata.sync_interval) }}</div>
          </v-col>
          <v-col cols="6" sm="3">
            <div class="text-caption text-grey">Lưu file/ảnh</div>
            <v-chip size="small" :color="metadata.sync_files ? 'success' : 'grey'" variant="tonal">
              {{ metadata.sync_files ? 'Bật' : 'Tắt' }}
            </v-chip>
          </v-col>
        </template>
        <template v-else>
          <v-col cols="6" sm="3">
            <div class="text-caption text-grey">Phạm vi đồng bộ</div>
            <div>{{ metadata.sync_scope === 'direct' ? 'Chỉ tin 1:1' : metadata.sync_scope === 'group' ? 'Chỉ tin nhóm' : 'Tất cả' }}</div>
          </v-col>
        </template>
        <v-col cols="6" sm="3">
          <div class="text-caption text-grey">Ngày tạo</div>
          <div>{{ formatDateTime(channel.created_at) }}</div>
        </v-col>
      </v-row>
    </v-card>

    <v-card v-if="isPersonalZalo" class="pa-4 mb-4 personal-zalo-connection-card">
      <div class="d-flex align-center mb-3 flex-wrap ga-2">
        <div>
          <div class="text-subtitle-1 font-weight-bold">
            <v-icon start size="small">mdi-cellphone-link</v-icon>
            Kết nối Zalo cá nhân
          </div>
          <div class="text-body-2 text-medium-emphasis">
            Làm theo từng bước ngay tại đây, không cần thao tác kỹ thuật phức tạp.
          </div>
        </div>
        <v-spacer />
        <v-chip size="small" :color="gatewayStatusColor" variant="tonal">
          {{ gatewayStatusLabel }}
        </v-chip>
        <v-btn size="small" variant="text" prepend-icon="mdi-refresh" :loading="gatewayLoading" @click="loadGatewayState()">
          Làm mới
        </v-btn>
      </div>

      <v-alert
        :type="gatewayAlert.type"
        variant="tonal"
        class="mb-4"
      >
        {{ gatewayAlert.message }}
      </v-alert>

      <v-row class="mb-2">
        <v-col cols="12" md="7">
          <v-sheet rounded="lg" border class="pa-4 h-100">
            <div class="text-overline mb-2">Các bước kết nối</div>
            <div class="d-flex flex-column ga-3">
              <div class="d-flex ga-3 align-start">
                <v-avatar size="28" :color="gatewayState.account_exists ? 'success' : 'grey-lighten-1'" variant="tonal">1</v-avatar>
                <div>
                  <div class="font-weight-medium">Bắt đầu kết nối</div>
                  <div class="text-body-2 text-medium-emphasis">Bấm nút để hệ thống tự chuẩn bị kết nối phía kỹ thuật.</div>
                </div>
              </div>
              <div class="d-flex ga-3 align-start">
                <v-avatar size="28" :color="gatewayAccount?.status === 'connected' || gatewayAccount?.status === 'qr_pending' || gatewayAccount?.status === 'connecting' ? 'info' : 'grey-lighten-1'" variant="tonal">2</v-avatar>
                <div>
                  <div class="font-weight-medium">Quét QR bằng điện thoại</div>
                  <div class="text-body-2 text-medium-emphasis">Lần đầu thì quét QR. Nếu đã kết nối trước đó, hệ thống sẽ tự thử vào lại.</div>
                </div>
              </div>
              <div class="d-flex ga-3 align-start">
                <v-avatar size="28" :color="gatewayAccount?.status === 'connected' && (channel?.conversation_count || 0) > 0 ? 'success' : gatewayAccount?.status === 'connected' ? 'warning' : 'grey-lighten-1'" variant="tonal">3</v-avatar>
                <div>
                  <div class="font-weight-medium">Lấy tin nhắn mới và kiểm tra</div>
                  <div class="text-body-2 text-medium-emphasis">Bấm lấy dữ liệu, rồi mở danh sách chat để kiểm tra kết quả.</div>
                </div>
              </div>
            </div>

            <v-divider class="my-4" />

            <div v-if="gatewayAccount" class="d-flex align-center ga-3 mb-4 flex-wrap">
              <v-avatar size="52" color="teal-lighten-5">
                <v-img v-if="gatewayAccount.avatar_url" :src="gatewayAccount.avatar_url" />
                <span v-else class="text-subtitle-2">{{ gatewayAvatarInitial }}</span>
              </v-avatar>
              <div class="flex-grow-1">
                <div class="font-weight-bold">{{ gatewayAccount.display_name || channel.name }}</div>
                <div class="text-body-2 text-medium-emphasis">
                  Mã tài khoản: {{ gatewayAccount.account_external_id || gatewayAccount.zalo_uid || 'Chưa có' }}
                </div>
                <div class="text-caption text-medium-emphasis">
                  {{ gatewayAccount.last_imported_at ? `Lần lấy dữ liệu gần nhất: ${formatDateTime(gatewayAccount.last_imported_at)}` : 'Chưa có lần lấy dữ liệu nào' }}
                </div>
              </div>
            </div>

            <div class="d-flex flex-wrap ga-2">
              <v-btn
                v-if="!gatewayState.account_exists || gatewayState.next_action === 'create_account' || gatewayState.next_action === 'scan_qr'"
                color="teal"
                prepend-icon="mdi-qrcode-scan"
                :loading="gatewayActionLoading === 'connect'"
                @click="startGatewayConnect"
              >
                {{ gatewayState.account_exists ? 'Lấy mã QR mới' : 'Bắt đầu kết nối' }}
              </v-btn>
              <v-btn
                v-if="gatewayState.account_exists && gatewayState.next_action === 'reconnect'"
                color="primary"
                variant="tonal"
                prepend-icon="mdi-connection"
                :loading="gatewayActionLoading === 'reconnect'"
                @click="reconnectGateway"
              >
                Kết nối lại
              </v-btn>
              <v-btn
                v-if="gatewayAccount?.status === 'connected'"
                color="primary"
                prepend-icon="mdi-sync"
                :loading="gatewayActionLoading === 'sync'"
                @click="syncGateway"
              >
                Lấy tin nhắn mới
              </v-btn>
              <v-btn
                v-if="channel?.conversation_count"
                variant="text"
                prepend-icon="mdi-forum"
                @click="goToMessages"
              >
                Mở danh sách chat
              </v-btn>
            </div>

            <div v-if="gatewayAccount?.last_error" class="text-caption text-error mt-3">
              Lỗi gần nhất: {{ gatewayAccount.last_error }}
            </div>
          </v-sheet>
        </v-col>

        <v-col cols="12" md="5">
          <v-sheet rounded="lg" border class="pa-4 h-100 d-flex flex-column justify-center">
            <template v-if="gatewayAccount?.status === 'qr_pending' && gatewayQrImageSrc">
              <div class="text-overline mb-2 text-center">Quét bằng Zalo trên điện thoại</div>
              <div class="d-flex justify-center mb-3">
                <v-img :src="gatewayQrImageSrc" max-width="240" max-height="240" class="rounded-lg border" cover />
              </div>
              <div class="text-body-2 text-medium-emphasis text-center">
                Mở Zalo, vào biểu tượng quét QR, quét mã này rồi xác nhận đăng nhập.
              </div>
              <div class="text-caption text-medium-emphasis text-center mt-2">
                {{ gatewayAccount.qr_generated_at ? `QR tạo lúc ${formatDateTime(gatewayAccount.qr_generated_at)}` : 'Hệ thống đang chờ bạn quét mã.' }}
              </div>
            </template>
            <template v-else>
              <div class="d-flex justify-center mb-3">
                <v-avatar size="72" :color="gatewayStatusColor" variant="tonal">
                  <v-icon size="36">{{ gatewayStatusIcon }}</v-icon>
                </v-avatar>
              </div>
              <div class="text-subtitle-2 font-weight-medium text-center mb-2">
                {{ gatewayStatusHeadline }}
              </div>
              <div class="text-body-2 text-medium-emphasis text-center">
                {{ gatewayStatusBody }}
              </div>
            </template>
          </v-sheet>
        </v-col>
      </v-row>
    </v-card>

    <v-card v-if="channel.channel_type === 'personal_zalo_import'" class="pa-4 mb-4">
      <div class="text-subtitle-1 font-weight-bold mb-3">
        <v-icon start size="small">mdi-api</v-icon>
        Thông tin kết nối (cho kỹ thuật)
      </div>
      <v-alert type="info" variant="tonal" class="mb-4">
        Đội kỹ thuật dùng các thông tin này để kết nối dịch vụ lấy dữ liệu Zalo cá nhân.
      </v-alert>
      <v-row>
        <v-col cols="12" md="8">
          <v-text-field :model-value="channel.import_endpoint || ''" label="Địa chỉ nhận dữ liệu (URL)" readonly density="compact" />
        </v-col>
        <v-col cols="12" md="4">
          <v-text-field :model-value="channel.import_secret || channel.import_secret_masked || ''" label="Mã bảo mật kết nối" readonly density="compact" />
        </v-col>
        <v-col cols="12">
          <v-text-field :model-value="channel.import_endpoint_internal || channel.import_endpoint || ''" label="Địa chỉ nội bộ (Docker)" readonly density="compact" hint="Nếu chạy bằng docker-compose, kỹ thuật sẽ dùng địa chỉ này." persistent-hint />
        </v-col>
      </v-row>
    </v-card>

    <v-card v-if="channel.channel_type === 'personal_zalo_import'" class="pa-4 mb-4">
      <div class="d-flex align-center mb-3">
        <div class="text-subtitle-1 font-weight-bold">
          <v-icon start size="small">mdi-account-switch</v-icon>
          Giao tài khoản Zalo cho nhân sự phụ trách
        </div>
        <v-spacer />
        <v-btn size="small" variant="text" prepend-icon="mdi-plus" @click="addAccountOwner">
          Thêm dòng phân công
        </v-btn>
      </div>
      <v-alert type="info" variant="tonal" class="mb-4">
        Nếu chưa biết mã tài khoản Zalo ở lần đầu, bạn có thể để trống một dòng và chọn nhân sự phụ trách trước.
      </v-alert>
      <div v-for="(owner, index) in accountOwnerDrafts" :key="`owner-${index}`" class="mb-3">
        <v-row>
          <v-col cols="12" md="5">
            <v-text-field v-model="owner.account_external_id" label="Mã tài khoản Zalo" density="compact" hint="Có thể để trống ở lần đồng bộ đầu tiên" persistent-hint />
          </v-col>
          <v-col cols="12" md="6">
            <v-select v-model="owner.user_id" :items="tenantUserOptions" label="Nhân sự phụ trách" density="compact" />
          </v-col>
          <v-col cols="12" md="1" class="d-flex align-center justify-end">
            <v-btn icon="mdi-delete-outline" variant="text" color="error" @click="removeAccountOwner(index)" />
          </v-col>
        </v-row>
      </div>
      <div v-if="!accountOwnerDrafts.length" class="text-body-2 text-grey mb-3">
        Chưa có phân công nào.
      </div>
      <div class="d-flex justify-end">
        <v-btn color="primary" :loading="savingAccountOwners" @click="saveAccountOwners">
          Lưu phân công
        </v-btn>
      </div>
    </v-card>

    <!-- Sync result alert -->
    <v-alert v-if="syncResult" :type="syncResult.type" closable class="mb-4" @click:close="syncResult = null">
      {{ syncResult.message }}
    </v-alert>

    <!-- Sync History -->
    <v-card class="pa-4">
      <div class="text-subtitle-1 font-weight-bold mb-3">
        <v-icon start size="small">mdi-history</v-icon>
        Lịch sử đồng bộ
      </div>
      <v-table density="compact" v-if="channelStore.syncHistory.length > 0">
        <thead>
          <tr>
            <th>Thời gian</th>
            <th>Trạng thái</th>
            <th>Chi tiết</th>
            <th>Lỗi</th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="log in channelStore.syncHistory" :key="log.id">
            <td>{{ formatDateTime(log.created_at) }}</td>
            <td>
              <v-chip size="x-small" :color="(log.action === 'sync.completed' || log.action === 'import.personal_zalo') ? 'success' : 'error'" variant="tonal">
                {{ (log.action === 'sync.completed' || log.action === 'import.personal_zalo') ? 'Thành công' : 'Lỗi' }}
              </v-chip>
            </td>
            <td class="text-caption">{{ log.detail?.substring(0, 120) }}</td>
            <td class="text-caption text-error">{{ log.error_message }}</td>
          </tr>
        </tbody>
      </v-table>
      <div v-else class="text-center text-grey pa-4">Chưa có lịch sử đồng bộ</div>
      <v-pagination
        v-if="syncTotalPages > 1"
        v-model="syncPage"
        :length="syncTotalPages"
        :total-visible="5"
        density="compact"
        class="mt-3"
      />
    </v-card>

    <!-- Edit Dialog -->
    <v-dialog v-model="editDialog" max-width="500">
      <v-card>
        <v-card-title>Sửa kênh chat</v-card-title>
        <v-card-text class="pt-3">
          <v-text-field v-model="editForm.name" label="Tên kênh" density="compact" class="mb-2" />
          <v-switch v-model="editForm.is_active" label="Hoạt động" density="compact" color="primary" class="mb-2" />
          <template v-if="channel.channel_type !== 'personal_zalo_import'">
            <v-select v-model="editForm.sync_interval" :items="syncIntervalOptions" label="Chu kỳ đồng bộ" density="compact" class="mb-2" />
            <v-switch v-model="editForm.sync_files" label="Lưu file/ảnh" density="compact" color="primary" />
          </template>
          <template v-else>
            <v-select
              v-model="editForm.sync_scope"
              :items="[
                { title: 'Tất cả (Cá nhân & Nhóm)', value: 'all' },
                { title: 'Chỉ tin nhắn 1:1', value: 'direct' },
                { title: 'Chỉ tin nhắn nhóm', value: 'group' },
              ]"
              label="Phạm vi đồng bộ"
              density="compact"
              class="mb-2"
              hint="Thay đổi loại tin nhắn lấy về từ Zalo cá nhân"
              persistent-hint
            />
          </template>
        </v-card-text>
        <v-card-actions>
          <v-spacer />
          <v-btn @click="editDialog = false">Hủy</v-btn>
          <v-btn color="primary" :loading="saving" @click="saveEdit">Lưu</v-btn>
        </v-card-actions>
      </v-card>
    </v-dialog>

    <!-- Purge Conversations Confirm -->
    <v-dialog v-model="confirmPurge" max-width="440">
      <v-card>
        <v-card-title>Xóa cuộc chat</v-card-title>
        <v-card-text>
          Xóa tất cả cuộc chat và tin nhắn của kênh <b>{{ channel.name }}</b>.
          Dữ liệu đánh giá QC liên quan cũng sẽ bị xóa. Bạn có thể đồng bộ lại sau khi xóa.
        </v-card-text>
        <v-card-actions>
          <v-spacer />
          <v-btn @click="confirmPurge = false">Hủy</v-btn>
          <v-btn color="warning" :loading="purging" @click="doPurge">Xóa cuộc chat</v-btn>
        </v-card-actions>
      </v-card>
    </v-dialog>

    <!-- Delete Confirm -->
    <v-dialog v-model="confirmDelete" max-width="400">
      <v-card>
        <v-card-title>Xóa kênh chat</v-card-title>
        <v-card-text>Xóa kênh <b>{{ channel.name }}</b> sẽ xóa tất cả cuộc chat và tin nhắn liên quan. Không thể hoàn tác.</v-card-text>
        <v-card-actions>
          <v-spacer />
          <v-btn @click="confirmDelete = false">Hủy</v-btn>
          <v-btn color="error" :loading="deleting" @click="doDelete">Xóa</v-btn>
        </v-card-actions>
      </v-card>
    </v-dialog>
  </div>
  <div v-else class="text-center pa-8">
    <v-progress-circular indeterminate />
  </div>
</template>

<script setup lang="ts">
import { ref, computed, onMounted, onBeforeUnmount, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { useChannelStore } from '../../stores/channels'
import type { PersonalZaloGatewayState } from '../../stores/channels'
import { useAuthStore } from '../../stores/auth'
import { useUserStore } from '../../stores/users'

const route = useRoute()
const router = useRouter()
const channelStore = useChannelStore()
const authStore = useAuthStore()
const userStore = useUserStore()

const tenantId = computed(() => route.params.tenantId as string)
const channelId = computed(() => route.params.channelId as string)
const channel = computed(() => channelStore.currentChannel)
const tenantUserOptions = computed(() => userStore.users.map((user) => ({
  title: user.name ? `${user.name} (${user.email})` : user.email,
  value: user.user_id,
})))
const isPersonalZalo = computed(() => channel.value?.channel_type === 'personal_zalo_import')
const metadata = computed(() => {
  try { return JSON.parse(channel.value?.metadata || '{}') } catch { return {} }
})

const syncing = ref(false)
const testing = ref(false)
const saving = ref(false)
const deleting = ref(false)
const purging = ref(false)
const editDialog = ref(false)
const confirmDelete = ref(false)
const confirmPurge = ref(false)
const syncResult = ref<{ type: 'success' | 'warning' | 'error' | 'info'; message: string } | null>(null)
const syncPage = ref(1)
const syncTotalPages = computed(() => Math.ceil(channelStore.syncHistoryTotal / 10))
const accountOwnerDrafts = ref<Array<{ account_external_id: string; user_id: string }>>([])
const savingAccountOwners = ref(false)
const gatewayState = ref<PersonalZaloGatewayState>({
  gateway_configured: true,
  gateway_reachable: true,
  account_exists: false,
  next_action: 'create_account',
  message: '',
})
const gatewayLoading = ref(false)
const gatewayActionLoading = ref<'connect' | 'reconnect' | 'sync' | ''>('')
let gatewayPollTimer: number | null = null

const editForm = ref({ name: '', is_active: true, sync_interval: 5, sync_files: false, sync_scope: 'all' })

const syncIntervalOptions = [
  { title: '1 phút', value: 1 },
  { title: '5 phút', value: 5 },
  { title: '10 phút', value: 10 },
  { title: '15 phút', value: 15 },
  { title: '30 phút', value: 30 },
  { title: '1 giờ', value: 60 },
  { title: '6 giờ', value: 360 },
  { title: '1 ngày', value: 1440 },
]

function formatDateTime(d: string) {
  const dt = new Date(d)
  const dd = String(dt.getDate()).padStart(2, '0')
  const mm = String(dt.getMonth() + 1).padStart(2, '0')
  const hh = String(dt.getHours()).padStart(2, '0')
  const mi = String(dt.getMinutes()).padStart(2, '0')
  return `${dd}/${mm}/${dt.getFullYear()} ${hh}:${mi}`
}

function formatSyncInterval(mins: number) {
  if (!mins) return '5 phút'
  if (mins < 60) return `${mins} phút`
  if (mins < 1440) return `${mins / 60} giờ`
  return `${mins / 1440} ngày`
}

function channelTypeLabel(channelType: string) {
  if (channelType === 'facebook') return 'Facebook'
  if (channelType === 'personal_zalo_import') return 'Zalo cá nhân'
  return 'Zalo OA'
}

function channelTypeColor(channelType: string) {
  if (channelType === 'facebook') return 'blue'
  if (channelType === 'personal_zalo_import') return 'teal'
  return 'green'
}

const gatewayAccount = computed(() => gatewayState.value.account)
const gatewayStatusColor = computed(() => {
  if (!gatewayState.value.gateway_configured || !gatewayState.value.gateway_reachable) return 'warning'
  if (gatewayAccount.value?.status === 'connected') return 'success'
  if (gatewayAccount.value?.status === 'qr_pending' || gatewayAccount.value?.status === 'connecting') return 'info'
  return 'grey'
})
const gatewayStatusIcon = computed(() => {
  if (!gatewayState.value.gateway_configured || !gatewayState.value.gateway_reachable) return 'mdi-lan-disconnect'
  if (gatewayAccount.value?.status === 'connected') return 'mdi-check-decagram'
  if (gatewayAccount.value?.status === 'qr_pending') return 'mdi-qrcode-scan'
  if (gatewayAccount.value?.status === 'connecting') return 'mdi-connection'
  return 'mdi-cellphone-link-off'
})
const gatewayStatusLabel = computed(() => {
  if (!gatewayState.value.gateway_configured) return 'Chưa thiết lập'
  if (!gatewayState.value.gateway_reachable) return 'Mất kết nối'
  if (!gatewayState.value.account_exists) return 'Chưa kết nối'
  if (gatewayAccount.value?.status === 'connected') return 'Đã kết nối'
  if (gatewayAccount.value?.status === 'qr_pending') return 'Chờ quét QR'
  if (gatewayAccount.value?.status === 'connecting') return 'Đang kết nối'
  return 'Đã ngắt kết nối'
})
const gatewayStatusHeadline = computed(() => {
  if (!gatewayState.value.gateway_configured) return 'Chưa bật kết nối Zalo cá nhân'
  if (!gatewayState.value.gateway_reachable) return 'Không kết nối được dịch vụ Zalo cá nhân'
  if (!gatewayState.value.account_exists) return 'Sẵn sàng bắt đầu kết nối'
  if (gatewayAccount.value?.status === 'connected') return 'Zalo cá nhân đã sẵn sàng'
  if (gatewayAccount.value?.status === 'qr_pending') return 'Đang chờ bạn quét mã QR'
  if (gatewayAccount.value?.status === 'connecting') return 'Đang thử kết nối lại'
  return 'Kết nối tạm thời gián đoạn'
})
const gatewayStatusBody = computed(() => {
  if (gatewayState.value.message) return gatewayState.value.message
  if (!gatewayState.value.account_exists) return 'Bấm "Bắt đầu kết nối", sau đó quét QR trên điện thoại để hoàn tất.'
  if (gatewayAccount.value?.status === 'connected') return 'Bấm "Lấy tin nhắn mới" để đưa hội thoại vào hệ thống.'
  if (gatewayAccount.value?.status === 'connecting') return 'Hệ thống đang thử khôi phục phiên cũ và sẽ tự làm mới màn hình.'
  if (gatewayAccount.value?.status === 'qr_pending') return 'Quét QR rồi xác nhận đăng nhập trong Zalo, trạng thái sẽ tự cập nhật.'
  return gatewayAccount.value?.last_error || 'Nếu chưa vào được, bấm "Kết nối lại" hoặc tạo mã QR mới.'
})
const gatewayAlert = computed(() => {
  if (!gatewayState.value.gateway_configured) {
    return { type: 'warning' as const, message: 'Kết nối Zalo cá nhân chưa được bật trên máy chủ. Vui lòng nhờ đội kỹ thuật kiểm tra cấu hình.' }
  }
  if (!gatewayState.value.gateway_reachable) {
    return { type: 'warning' as const, message: 'Hệ thống chưa gọi được dịch vụ Zalo cá nhân. Vui lòng kiểm tra dịch vụ đang chạy.' }
  }
  if (!gatewayState.value.account_exists) {
    return { type: 'info' as const, message: 'Kênh đã tạo xong. Bấm "Bắt đầu kết nối" để tiếp tục.' }
  }
  if (gatewayAccount.value?.status === 'connected') {
    return { type: 'success' as const, message: 'Đã kết nối thành công. Bây giờ bạn chỉ cần bấm "Lấy tin nhắn mới".' }
  }
  if (gatewayAccount.value?.status === 'qr_pending') {
    return { type: 'info' as const, message: 'Đây là bước cần điện thoại: quét QR và xác nhận trên Zalo, hệ thống sẽ tự chuyển trạng thái.' }
  }
  return { type: 'warning' as const, message: gatewayAccount.value?.last_error || 'Phiên đăng nhập hiện không dùng được. Hãy kết nối lại hoặc tạo mã QR mới.' }
})
const gatewayAvatarInitial = computed(() => (gatewayAccount.value?.display_name || channel.value?.name || 'Z').slice(0, 1).toUpperCase())
const gatewayQrImageSrc = computed(() => normalizeGatewayQrImage(gatewayAccount.value?.qr_image))

function normalizeGatewayQrImage(raw?: string): string {
  const source = raw?.trim()
  if (!source) return ''
  if (source.startsWith('data:image/')) return source
  if (source.startsWith('http://') || source.startsWith('https://') || source.startsWith('blob:')) return source
  // personal-zalo-gateway trả base64 PNG thô, cần thêm data URL để <v-img> render được.
  return `data:image/png;base64,${source}`
}

function goToMessages() {
  router.push(`/${tenantId.value}/messages?channel_id=${channelId.value}`)
}

function clearGatewayPolling() {
  if (gatewayPollTimer !== null) {
    window.clearInterval(gatewayPollTimer)
    gatewayPollTimer = null
  }
}

function updateGatewayPolling() {
  clearGatewayPolling()
  if (!isPersonalZalo.value) return
  if (!gatewayState.value.gateway_reachable || !gatewayState.value.gateway_configured) return
  if (!gatewayAccount.value || !['qr_pending', 'connecting'].includes(gatewayAccount.value.status)) return
  gatewayPollTimer = window.setInterval(() => {
    void loadGatewayState(false)
  }, 3000)
}

async function loadGatewayState(showSpinner = true) {
  if (!isPersonalZalo.value) return
  if (showSpinner) gatewayLoading.value = true
  try {
    gatewayState.value = await channelStore.fetchPersonalZaloGatewayState(tenantId.value, channelId.value)
  } catch (err: any) {
    gatewayState.value = {
      gateway_configured: true,
      gateway_reachable: false,
      account_exists: false,
      next_action: 'fix_gateway',
      message: err.response?.data?.details || err.response?.data?.error || 'Không lấy được trạng thái kết nối',
    }
  } finally {
    if (showSpinner) gatewayLoading.value = false
    updateGatewayPolling()
  }
}

async function startGatewayConnect() {
  gatewayActionLoading.value = 'connect'
  try {
    gatewayState.value = await channelStore.connectPersonalZaloGateway(tenantId.value, channelId.value)
    syncResult.value = { type: 'success', message: gatewayState.value.account?.status === 'connected' ? 'Zalo cá nhân đã kết nối.' : 'Đã tạo phiên kết nối. Quét QR để hoàn tất.' }
    await channelStore.fetchChannel(tenantId.value, channelId.value)
  } catch (err: any) {
    syncResult.value = { type: 'error', message: err.response?.data?.details || err.response?.data?.message || err.response?.data?.error || 'Không thể bắt đầu kết nối' }
  } finally {
    gatewayActionLoading.value = ''
    updateGatewayPolling()
  }
}

async function reconnectGateway() {
  gatewayActionLoading.value = 'reconnect'
  try {
    gatewayState.value = await channelStore.reconnectPersonalZaloGateway(tenantId.value, channelId.value)
    syncResult.value = { type: 'info', message: 'Đang thử kết nối lại tài khoản Zalo cá nhân.' }
  } catch (err: any) {
    syncResult.value = { type: 'error', message: err.response?.data?.details || err.response?.data?.message || err.response?.data?.error || 'Reconnect thất bại' }
  } finally {
    gatewayActionLoading.value = ''
    updateGatewayPolling()
  }
}

async function syncGateway() {
  gatewayActionLoading.value = 'sync'
  try {
    gatewayState.value = await channelStore.syncPersonalZaloGateway(tenantId.value, channelId.value)
    await channelStore.fetchChannel(tenantId.value, channelId.value)
    await channelStore.fetchSyncHistory(tenantId.value, channelId.value, syncPage.value)
    syncResult.value = { type: 'success', message: 'Đã bắt đầu lấy dữ liệu. Tin nhắn mới sẽ xuất hiện sau ít phút.' }
  } catch (err: any) {
    syncResult.value = { type: 'error', message: err.response?.data?.details || err.response?.data?.message || err.response?.data?.error || 'Lấy dữ liệu thất bại' }
  } finally {
    gatewayActionLoading.value = ''
  }
}

async function doSync() {
  syncing.value = true
  syncResult.value = null
  try {
    await channelStore.syncChannel(tenantId.value, channelId.value)
    // Poll channel status until sync completes (max 3 minutes)
    let pollAttempts = 0
    const maxPollAttempts = 60
    while (pollAttempts < maxPollAttempts) {
      await new Promise(r => setTimeout(r, 3000))
      const ch = await channelStore.fetchChannel(tenantId.value, channelId.value)
      if (ch.last_sync_status !== 'syncing') break
      pollAttempts++
    }
    if (pollAttempts >= maxPollAttempts) {
      syncResult.value = { type: 'error', message: 'Đồng bộ quá lâu, vui lòng kiểm tra lại sau' }
      syncing.value = false
      return
    }
    await channelStore.fetchSyncHistory(tenantId.value, channelId.value, syncPage.value)
    const ch = channelStore.currentChannel
    if (ch?.last_sync_status === 'success') {
      syncResult.value = { type: 'success', message: 'Đồng bộ thành công' }
    } else {
      syncResult.value = { type: 'error', message: ch?.last_sync_error || 'Đồng bộ thất bại' }
    }
  } catch (err: any) {
    syncResult.value = { type: 'error', message: err.response?.data?.error || 'Đồng bộ thất bại' }
  } finally {
    syncing.value = false
  }
}

async function doTest() {
  testing.value = true
  try {
    const result = await channelStore.testConnection(tenantId.value, channelId.value)
    syncResult.value = { type: 'success', message: result.message || 'Kết nối thành công' }
  } catch (err: any) {
    syncResult.value = { type: 'error', message: err.response?.data?.error || 'Kết nối thất bại' }
  } finally {
    testing.value = false
  }
}

async function saveEdit() {
  saving.value = true
  try {
    const payload: Record<string, unknown> = {
      name: editForm.value.name,
      is_active: editForm.value.is_active,
    }
    if (channel.value?.channel_type !== 'personal_zalo_import') {
      payload.metadata = JSON.stringify({ sync_interval: editForm.value.sync_interval, sync_files: editForm.value.sync_files })
    } else {
      payload.metadata = JSON.stringify({ sync_scope: editForm.value.sync_scope })
    }
    await channelStore.updateChannel(tenantId.value, channelId.value, payload)
    editDialog.value = false
    await channelStore.fetchChannel(tenantId.value, channelId.value)
  } finally {
    saving.value = false
  }
}

function addAccountOwner() {
  accountOwnerDrafts.value.push({ account_external_id: '', user_id: '' })
}

function removeAccountOwner(index: number) {
  accountOwnerDrafts.value.splice(index, 1)
}

async function loadAccountOwners() {
  const owners = await channelStore.fetchAccountOwners(tenantId.value, channelId.value)
  accountOwnerDrafts.value = owners.length
    ? owners.map(owner => ({ account_external_id: owner.account_external_id || '', user_id: owner.user_id }))
    : [{ account_external_id: '', user_id: '' }]
}

async function saveAccountOwners() {
  savingAccountOwners.value = true
  try {
    const payload = accountOwnerDrafts.value
      .map(owner => ({
        account_external_id: owner.account_external_id.trim(),
        user_id: owner.user_id,
      }))
      .filter(owner => owner.user_id)
    await channelStore.updateAccountOwners(tenantId.value, channelId.value, payload)
    await loadAccountOwners()
    syncResult.value = { type: 'success', message: 'Đã lưu phân công tài khoản cho nhân sự' }
  } catch (err: any) {
    syncResult.value = { type: 'error', message: err.response?.data?.details || err.response?.data?.error || 'Lưu phân công thất bại' }
  } finally {
    savingAccountOwners.value = false
  }
}

async function doPurge() {
  purging.value = true
  try {
    const result = await channelStore.purgeConversations(tenantId.value, channelId.value)
    confirmPurge.value = false
    syncResult.value = {
      type: 'success',
      message: `Đã xóa ${result.conversations_deleted} cuộc chat, ${result.messages_deleted} tin nhắn. Bạn có thể đồng bộ lại.`
    }
    // Refresh channel data to reflect reset sync state
    await channelStore.fetchChannel(tenantId.value, channelId.value)
  } catch {
    syncResult.value = { type: 'error', message: 'Xóa cuộc chat thất bại' }
  } finally {
    purging.value = false
  }
}

async function doDelete() {
  deleting.value = true
  try {
    await channelStore.deleteChannel(tenantId.value, channelId.value)
    router.push(`/${tenantId.value}/channels`)
  } finally {
    deleting.value = false
  }
}

watch(editDialog, (v) => {
  if (v && channel.value) {
    editForm.value = {
      name: channel.value.name,
      is_active: channel.value.is_active,
      sync_interval: metadata.value.sync_interval || 5,
      sync_files: metadata.value.sync_files || false,
      sync_scope: metadata.value.sync_scope || 'all',
    }
  }
})

watch(syncPage, (p) => {
  channelStore.fetchSyncHistory(tenantId.value, channelId.value, p)
})

watch(() => gatewayAccount.value?.status, () => {
  updateGatewayPolling()
})

onMounted(async () => {
  // Handle OAuth callback redirect
  const params = new URLSearchParams(window.location.search)
  if (params.get('zalo_auth') === 'success' || params.get('fb_auth') === 'success') {
    syncResult.value = { type: 'success', message: 'Xác thực thành công! Bấm "Đồng bộ ngay" để lấy tin nhắn.' }
    window.history.replaceState({}, '', window.location.pathname)
  } else if (params.get('zalo_auth') === 'error' || params.get('fb_auth') === 'error') {
    syncResult.value = { type: 'error', message: params.get('message') || 'Xác thực thất bại. Vui lòng thử lại.' }
    window.history.replaceState({}, '', window.location.pathname)
  }

  await channelStore.fetchChannel(tenantId.value, channelId.value)
  await channelStore.fetchSyncHistory(tenantId.value, channelId.value, 1)
  if (channelStore.currentChannel?.channel_type === 'personal_zalo_import') {
    await userStore.fetchUsers(tenantId.value)
    await loadAccountOwners()
    await loadGatewayState()
  }
})

onBeforeUnmount(() => {
  clearGatewayPolling()
})
</script>

<style scoped>
.personal-zalo-connection-card {
  background:
    radial-gradient(circle at top right, rgba(0, 150, 136, 0.08), transparent 32%),
    linear-gradient(180deg, rgba(0, 150, 136, 0.04), transparent 45%);
}
</style>
