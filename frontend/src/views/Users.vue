<template>
  <div>
    <div class="d-flex align-center mb-6">
      <h1 class="text-h5 font-weight-bold">{{ $t('nav_users') }}</h1>
      <v-spacer />
      <v-btn v-if="currentTab === 'users'" color="primary" prepend-icon="mdi-account-plus" @click="inviteDialog = true">
        {{ $t('create_user') }}
      </v-btn>
      <v-btn v-else color="teal" prepend-icon="mdi-plus" @click="whitelistDialog = true; whitelistForm = { name: '', zalo_user_id: '', mode: 'qr', selectedOA: activeZaloOAs.length > 0 ? activeZaloOAs[0].external_id : '' }">
        Thêm nhân viên Whitelist
      </v-btn>
    </div>

    <v-tabs v-model="currentTab" color="primary" class="mb-4">
      <v-tab value="users">Người dùng hệ thống</v-tab>
      <v-tab value="whitelist">Whitelist nhân viên Zalo OA</v-tab>
    </v-tabs>

    <v-window v-model="currentTab">
      <v-window-item value="users" class="pt-2">
        <v-card>
          <v-table density="compact">
            <thead>
              <tr>
                <th>{{ $t('display_name') }}</th>
                <th>{{ $t('email') }}</th>
                <th>{{ $t('role') }}</th>
                <th>{{ $t('actions') }}</th>
              </tr>
            </thead>
            <tbody>
              <tr v-for="u in userStore.users" :key="u.user_id">
                <td>{{ u.name }}</td>
                <td>{{ u.email }}</td>
                <td>
                  <v-select
                    :model-value="u.role"
                    :items="roleOptions"
                    density="compact"
                    variant="plain"
                    hide-details
                    style="max-width: 140px"
                    :disabled="u.user_id === authStore.user?.id"
                    @update:model-value="changeRole(u.user_id, $event)"
                  />
                </td>
                <td>
                  <v-btn
                    v-if="u.role === 'member' && u.user_id !== authStore.user?.id"
                    icon="mdi-shield-edit"
                    size="small"
                    variant="text"
                    @click="openPermissions(u)"
                    title="Phân quyền"
                  />
                  <v-btn
                    v-if="u.user_id !== authStore.user?.id"
                    icon="mdi-lock-reset"
                    size="small"
                    variant="text"
                    @click="openResetPassword(u)"
                    title="Đặt lại mật khẩu"
                  />
                  <v-btn
                    v-if="u.user_id !== authStore.user?.id"
                    icon="mdi-delete"
                    size="small"
                    color="error"
                    variant="text"
                    @click="confirmRemove(u)"
                  />
                </td>
              </tr>
            </tbody>
          </v-table>
        </v-card>
      </v-window-item>

      <v-window-item value="whitelist" class="pt-2">
        <v-alert v-if="activeZaloOAs.length === 0" type="warning" variant="tonal" class="mb-4">
          Không tìm thấy Zalo OA hoạt động nào. Hãy kết nối và kích hoạt Zalo OA trong mục <strong>Kênh chat</strong> trước để nhân viên có thể xác thực qua mã QR.
        </v-alert>

        <v-card>
          <v-table density="compact">
            <thead>
              <tr>
                <th>Ảnh</th>
                <th>Tên nhân viên</th>
                <th>Zalo User ID</th>
                <th>Trạng thái</th>
                <th>Ngày tạo</th>
                <th>Hành động</th>
              </tr>
            </thead>
            <tbody>
              <tr v-for="item in whitelist" :key="item.id">
                <td>
                  <v-avatar size="32" color="teal-lighten-4">
                    <v-img v-if="item.avatar" :src="item.avatar" />
                    <v-icon v-else color="teal" size="20">mdi-account</v-icon>
                  </v-avatar>
                </td>
                <td>
                  <div class="font-weight-bold">{{ item.name }}</div>
                </td>
                <td>
                  <code class="text-caption bg-grey-lighten-3 px-1 rounded">{{ item.zalo_user_id || '—' }}</code>
                </td>
                <td>
                  <v-chip size="x-small" :color="item.status === 'active' ? 'success' : 'warning'" variant="tonal">
                    {{ item.status === 'active' ? 'Đã liên kết' : 'Chờ quét QR' }}
                  </v-chip>
                </td>
                <td>{{ new Date(item.created_at).toLocaleString() }}</td>
                <td>
                  <v-btn
                    v-if="item.status === 'pending'"
                    icon="mdi-qrcode"
                    size="small"
                    color="teal"
                    variant="text"
                    @click="showPendingQR(item)"
                    title="Xem mã QR"
                  />
                  <v-btn
                    icon="mdi-delete"
                    size="small"
                    color="error"
                    variant="text"
                    @click="deleteWhitelist(item.id)"
                    title="Xóa khỏi whitelist"
                  />
                </td>
              </tr>
              <tr v-if="whitelist.length === 0">
                <td colspan="6" class="text-center py-8 text-grey text-body-2">
                  <v-icon size="40" color="grey-lighten-1" class="mb-2">mdi-shield-account-outline</v-icon>
                  <div>Chưa có nhân viên nào trong whitelist Zalo.</div>
                  <div class="text-caption text-grey-darken-1 mt-1" style="max-width: 600px; margin: 0 auto;">
                    Khi Whitelist trống, bất kỳ ai cũng có thể chat với chatbot Zalo OA. Khi có ít nhất 1 nhân viên trong whitelist hoạt động, chatbot sẽ tự động chặn tất cả người lạ.
                  </div>
                </td>
              </tr>
            </tbody>
          </v-table>
        </v-card>
      </v-window-item>
    </v-window>

    <!-- Create user dialog -->
    <v-dialog v-model="inviteDialog" max-width="450">
      <v-card>
        <v-card-title>{{ $t('create_user') }}</v-card-title>
        <v-card-text>
          <v-form ref="createFormRef">
            <v-text-field v-model="inviteForm.name" :label="$t('display_name')" class="mb-2" :rules="[v => !!v || $t('validation_required')]" />
            <v-text-field v-model="inviteForm.email" label="Email" type="email" class="mb-2" :rules="[v => !!v || $t('validation_required'), v => /.+@.+\..+/.test(v) || 'Email invalid']" />
            <v-text-field v-model="inviteForm.password" :label="$t('password')" type="password" class="mb-2" :rules="[v => !!v || $t('validation_required'), v => v.length >= 6 || $t('password_too_short')]" />
            <v-select v-model="inviteForm.role" :items="roleOptions" :label="$t('role')" class="mb-2" />

            <!-- Member permissions -->
            <div v-if="inviteForm.role === 'member'" class="mt-2">
              <div class="text-subtitle-2 mb-2">{{ $t('permissions') }}</div>
              <v-table density="compact">
                <thead><tr><th>{{ $t('feature') }}</th><th>{{ $t('view') }}</th><th>{{ $t('edit') }}</th></tr></thead>
                <tbody>
                  <tr v-for="feat in permissionFeatures" :key="feat.key">
                    <td class="text-body-2">{{ feat.label }}</td>
                    <td><v-checkbox-btn v-model="inviteForm.permissions[feat.key]" true-value="r" false-value="" density="compact" /></td>
                    <td><v-checkbox-btn v-model="inviteForm.permissions[feat.key]" true-value="rw" :false-value="inviteForm.permissions[feat.key] === 'rw' ? 'r' : ''" density="compact" /></td>
                  </tr>
                </tbody>
              </v-table>
            </div>
          </v-form>
        </v-card-text>
        <v-card-actions>
          <v-spacer />
          <v-btn @click="inviteDialog = false">{{ $t('cancel') }}</v-btn>
          <v-btn color="primary" :loading="inviting" @click="doInvite">{{ $t('create_user') }}</v-btn>
        </v-card-actions>
      </v-card>
    </v-dialog>

    <!-- Add Whitelist Staff Dialog -->
    <v-dialog v-model="whitelistDialog" max-width="500">
      <v-card class="pa-4">
        <v-card-title class="font-weight-bold">Thêm nhân viên vào Whitelist</v-card-title>
        <v-card-text>
          <v-form ref="whitelistFormRef">
            <v-text-field
              v-model="whitelistForm.name"
              label="Tên nhân viên nội bộ"
              hint="Ví dụ: Nguyễn Văn A (Nhân viên kinh doanh)"
              persistent-hint
              class="mb-4"
              :rules="[v => !!v || 'Trường này là bắt buộc']"
            />

            <v-radio-group v-model="whitelistForm.mode" inline class="mb-2">
              <v-radio label="Xác minh quét QR (Khuyên dùng)" value="qr" color="teal" />
              <v-radio label="Thêm trực tiếp User ID" value="direct" color="teal" />
            </v-radio-group>

            <v-expand-transition>
              <div v-if="whitelistForm.mode === 'direct'">
                <v-text-field
                  v-model="whitelistForm.zalo_user_id"
                  label="Zalo User ID"
                  hint="Nhập Zalo User ID chính xác của nhân viên"
                  persistent-hint
                  class="mb-2"
                  :rules="[v => !!v || 'Trường này là bắt buộc']"
                />
              </div>
              <div v-else>
                <v-select
                  v-if="activeZaloOAs.length > 1"
                  v-model="whitelistForm.selectedOA"
                  :items="activeZaloOAs.map(oa => ({ title: oa.name, value: oa.external_id }))"
                  label="Chọn Zalo OA nhận mã xác thực"
                  class="mb-2"
                />
                <div class="text-caption text-grey-darken-1 mb-2">
                  Hệ thống sẽ tạo ra một mã xác nhận và mã QR. Nhân viên chỉ cần dùng app Zalo quét mã QR và gửi tin nhắn chứa mã để hoàn tất liên kết tài khoản tự động.
                </div>
              </div>
            </v-expand-transition>
          </v-form>
        </v-card-text>
        <v-card-actions>
          <v-spacer />
          <v-btn variant="text" @click="whitelistDialog = false">Hủy</v-btn>
          <v-btn color="teal" :loading="creatingWhitelist" @click="addWhitelist">
            {{ whitelistForm.mode === 'qr' ? 'Tạo mã liên kết' : 'Thêm vào Whitelist' }}
          </v-btn>
        </v-card-actions>
      </v-card>
    </v-dialog>

    <!-- QR Code / Instruction Verification Dialog -->
    <v-dialog v-model="qrDialog" max-width="480">
      <v-card class="pa-6 text-center">
        <v-card-title class="font-weight-bold text-h6 justify-center">Quét QR liên kết Zalo</v-card-title>
        <v-card-text>
          <div class="text-body-1 font-weight-bold mb-1 text-teal">Nhân viên: {{ activeInvite?.name }}</div>
          <div class="text-body-2 text-grey-darken-1 mb-4">
            Vui lòng gửi hướng dẫn dưới đây cho nhân viên để thực hiện liên kết tài khoản.
          </div>

          <!-- QR code container -->
          <div class="d-flex justify-center mb-4">
            <v-card variant="outlined" class="pa-2" style="border-color: #008fe5 !important;">
              <v-img
                v-if="whitelistForm.selectedOA || (activeZaloOAs.length > 0)"
                :src="`https://api.qrserver.com/v1/create-qr-code/?size=200x200&data=${encodeURIComponent('https://zalo.me/' + (whitelistForm.selectedOA || activeZaloOAs[0].external_id))}`"
                width="200"
                height="200"
              />
              <div v-else class="pa-6 text-caption text-grey">Chưa chọn Zalo OA</div>
            </v-card>
          </div>

          <!-- Instruction code card -->
          <v-card color="teal-lighten-5" class="pa-4 mb-4" variant="flat">
            <div class="text-caption text-grey-darken-2 mb-1">Cú pháp tin nhắn cần gửi:</div>
            <div class="text-h5 font-weight-black text-teal" style="letter-spacing: 1px;">
              verify {{ activeInvite?.verify_token }}
            </div>
          </v-card>

          <v-divider class="mb-4" />

          <div class="text-left text-body-2">
            <div class="font-weight-bold mb-1">Các bước thực hiện:</div>
            <ol class="pl-4">
              <li class="mb-1">Dùng ứng dụng Zalo quét mã QR ở trên để truy cập cuộc trò chuyện Zalo OA.</li>
              <li>Gửi chính xác tin nhắn chứa mã xác nhận bên trên (chữ thường hoặc hoa đều được).</li>
              <li>Chờ chatbot Zalo phản hồi <strong>"Xác thực thành công"</strong>. Trạng thái trên web sẽ tự động cập nhật!</li>
            </ol>
          </div>
        </v-card-text>
        <v-card-actions class="justify-center">
          <v-btn color="teal" variant="elevated" class="px-6" @click="qrDialog = false; fetchWhitelist()">Hoàn tất</v-btn>
        </v-card-actions>
      </v-card>
    </v-dialog>

    <!-- Remove confirm -->
    <v-dialog v-model="removeDialog" max-width="400">
      <v-card>
        <v-card-title>{{ $t('confirm') }}</v-card-title>
        <v-card-text>{{ $t('confirm_remove_user') }} <strong>{{ removeTarget?.email }}</strong>?</v-card-text>
        <v-card-actions>
          <v-spacer />
          <v-btn @click="removeDialog = false">{{ $t('cancel') }}</v-btn>
          <v-btn color="error" @click="doRemove">{{ $t('delete') }}</v-btn>
        </v-card-actions>
      </v-card>
    </v-dialog>

    <!-- Permissions edit dialog -->
    <v-dialog v-model="permDialog" max-width="450">
      <v-card>
        <v-card-title>{{ $t('permissions') }} — {{ permTarget?.name }}</v-card-title>
        <v-card-text>
          <v-table density="compact">
            <thead><tr><th>{{ $t('feature') }}</th><th>{{ $t('view') }}</th><th>{{ $t('edit') }}</th></tr></thead>
            <tbody>
              <tr v-for="feat in permissionFeatures" :key="feat.key">
                <td class="text-body-2">{{ feat.label }}</td>
                <td><v-checkbox-btn v-model="editPerms[feat.key]" true-value="r" false-value="" density="compact" /></td>
                <td><v-checkbox-btn v-model="editPerms[feat.key]" true-value="rw" :false-value="editPerms[feat.key] === 'rw' ? 'r' : ''" density="compact" /></td>
              </tr>
            </tbody>
          </v-table>
        </v-card-text>
        <v-card-actions>
          <v-spacer />
          <v-btn @click="permDialog = false">{{ $t('cancel') }}</v-btn>
          <v-btn color="primary" :loading="savingPerms" @click="savePermissions">{{ $t('save_settings') }}</v-btn>
        </v-card-actions>
      </v-card>
    </v-dialog>

    <!-- Reset password dialog -->
    <v-dialog v-model="resetDialog" max-width="400">
      <v-card>
        <v-card-title>Đặt lại mật khẩu — {{ resetTarget?.name }}</v-card-title>
        <v-card-text>
          <v-text-field
            v-model="resetPassword"
            label="Mật khẩu mới"
            type="password"
            :rules="[v => !!v || 'Bắt buộc', v => v.length >= 8 || 'Tối thiểu 8 ký tự']"
          />
        </v-card-text>
        <v-card-actions>
          <v-spacer />
          <v-btn @click="resetDialog = false">Hủy</v-btn>
          <v-btn color="primary" :loading="resettingPassword" @click="doResetPassword">Đặt lại</v-btn>
        </v-card-actions>
      </v-card>
    </v-dialog>

    <v-snackbar v-model="snack" :color="snackColor" timeout="3000">{{ snackText }}</v-snackbar>
  </div>
</template>

<script setup lang="ts">
import { ref, computed, onMounted } from 'vue'
import { useRoute } from 'vue-router'
import { useUserStore, type TenantUser } from '../stores/users'
import { useChannelStore } from '../stores/channels'
import api from '../api'
import { useAuthStore } from '../stores/auth'

const route = useRoute()
const userStore = useUserStore()
const authStore = useAuthStore()
const channelStore = useChannelStore()
const tenantId = computed(() => route.params.tenantId as string)

const currentTab = ref('users')

// Whitelist state
const whitelist = ref<any[]>([])
const loadingWhitelist = ref(false)
const whitelistDialog = ref(false)
const creatingWhitelist = ref(false)
const whitelistForm = ref({ name: '', zalo_user_id: '', mode: 'qr', selectedOA: '' })
const activeInvite = ref<any>(null)
const qrDialog = ref(false)
const whitelistFormRef = ref<any>(null)

// Computed active Zalo OAs
const activeZaloOAs = computed(() => {
  return channelStore.channels.filter((c: any) => c.channel_type === 'zalo_oa' && c.is_active)
})

const roleOptions = [
  { title: 'Owner', value: 'owner' },
  { title: 'Admin', value: 'admin' },
  { title: 'Member', value: 'member' },
]

const inviteDialog = ref(false)
const inviting = ref(false)
const inviteForm = ref({ name: '', email: '', password: '', role: 'member', permissions: { channels: 'r', messages: 'r', jobs: 'r', settings: '' } as Record<string, string> })
const createFormRef = ref<any>(null)
const permissionFeatures = [
  { key: 'channels', label: 'Kênh chat' },
  { key: 'messages', label: 'Tin nhắn' },
  { key: 'jobs', label: 'Công việc' },
  { key: 'settings', label: 'Cài đặt' },
]

const removeDialog = ref(false)
const removeTarget = ref<TenantUser | null>(null)

const permDialog = ref(false)
const permTarget = ref<TenantUser | null>(null)
const editPerms = ref<Record<string, string>>({})
const savingPerms = ref(false)

const resetDialog = ref(false)
const resetTarget = ref<TenantUser | null>(null)
const resetPassword = ref('')
const resettingPassword = ref(false)

const snack = ref(false)
const snackText = ref('')
const snackColor = ref('success')

onMounted(async () => {
  await userStore.fetchUsers(tenantId.value)
  await channelStore.fetchChannels(tenantId.value)
  await fetchWhitelist()
})

async function fetchWhitelist() {
  loadingWhitelist.value = true
  try {
    const { data } = await api.get(`/tenants/${tenantId.value}/zalo-whitelist`)
    whitelist.value = data
  } catch (err) {
    showSnack('Không thể tải whitelist Zalo', 'error')
  } finally {
    loadingWhitelist.value = false
  }
}

async function addWhitelist() {
  const { valid } = await whitelistFormRef.value?.validate() || {}
  if (!valid) return

  creatingWhitelist.value = true
  try {
    if (whitelistForm.value.mode === 'direct') {
      await api.post(`/tenants/${tenantId.value}/zalo-whitelist`, {
        zalo_user_id: whitelistForm.value.zalo_user_id,
        name: whitelistForm.value.name,
      })
      whitelistDialog.value = false
      showSnack('Đã thêm nhân viên trực tiếp vào Whitelist', 'success')
      await fetchWhitelist()
    } else {
      // QR verification mode
      const { data } = await api.post(`/tenants/${tenantId.value}/zalo-whitelist/invite`, {
        name: whitelistForm.value.name,
      })
      activeInvite.value = data
      
      // Auto-select first Zalo OA if select field not filled
      if (!whitelistForm.value.selectedOA && activeZaloOAs.value.length > 0) {
        whitelistForm.value.selectedOA = activeZaloOAs.value[0].external_id
      }
      
      whitelistDialog.value = false
      qrDialog.value = true
      await fetchWhitelist()
    }
  } catch (err: any) {
    showSnack(err.response?.data?.error || 'Có lỗi xảy ra', 'error')
  } finally {
    creatingWhitelist.value = false
  }
}

async function deleteWhitelist(id: string) {
  if (!confirm('Xóa nhân viên này khỏi whitelist Zalo?')) return
  try {
    await api.delete(`/tenants/${tenantId.value}/zalo-whitelist/${id}`)
    showSnack('Đã xóa khỏi whitelist', 'success')
    await fetchWhitelist()
  } catch (err) {
    showSnack('Lỗi xóa whitelist', 'error')
  }
}

function showPendingQR(item: any) {
  activeInvite.value = item
  // Select first OA if not selected
  if (!whitelistForm.value.selectedOA && activeZaloOAs.value.length > 0) {
    whitelistForm.value.selectedOA = activeZaloOAs.value[0].external_id
  }
  qrDialog.value = true
}

async function doInvite() {
  const { valid } = await createFormRef.value?.validate() || {}
  if (!valid) return

  inviting.value = true
  try {
    const payload = {
      ...inviteForm.value,
      permissions: inviteForm.value.role === 'member' ? JSON.stringify(inviteForm.value.permissions) : '',
    }
    await userStore.inviteUser(tenantId.value, payload)
    inviteDialog.value = false
    inviteForm.value = { name: '', email: '', password: '', role: 'member', permissions: { channels: 'r', messages: 'r', jobs: 'r', settings: '' } }
    showSnack('User created', 'success')
  } catch (err: any) {
    showSnack(friendlyError(err), 'error')
  } finally {
    inviting.value = false
  }
}

async function changeRole(userId: string, role: string) {
  try {
    await userStore.updateRole(tenantId.value, userId, role)
    showSnack('Role updated', 'success')
  } catch (err: any) {
    showSnack(err.response?.data?.error || 'Error', 'error')
    userStore.fetchUsers(tenantId.value) // reload to revert
  }
}

function confirmRemove(u: TenantUser) {
  removeTarget.value = u
  removeDialog.value = true
}

async function doRemove() {
  if (!removeTarget.value) return
  try {
    await userStore.removeUser(tenantId.value, removeTarget.value.user_id)
    removeDialog.value = false
    showSnack('User removed', 'success')
  } catch (err: any) {
    showSnack(err.response?.data?.error || 'Error', 'error')
  }
}

function openPermissions(u: TenantUser) {
  permTarget.value = u
  try {
    editPerms.value = u.permissions ? JSON.parse(u.permissions) : { channels: 'r', messages: 'r', jobs: 'r', settings: '' }
  } catch {
    editPerms.value = { channels: 'r', messages: 'r', jobs: 'r', settings: '' }
  }
  permDialog.value = true
}

async function savePermissions() {
  if (!permTarget.value) return
  savingPerms.value = true
  try {
    await api.put(`/tenants/${tenantId.value}/users/${permTarget.value.user_id}/role`, {
      role: 'member',
      permissions: JSON.stringify(editPerms.value),
    })
    // Update local
    permTarget.value.permissions = JSON.stringify(editPerms.value)
    permDialog.value = false
    showSnack('Permissions updated', 'success')
  } catch (err: any) {
    showSnack(err.response?.data?.error || 'Error', 'error')
  } finally {
    savingPerms.value = false
  }
}

function openResetPassword(u: TenantUser) {
  resetTarget.value = u
  resetPassword.value = ''
  resetDialog.value = true
}

async function doResetPassword() {
  if (!resetTarget.value || resetPassword.value.length < 8) return
  resettingPassword.value = true
  try {
    await api.put(`/tenants/${tenantId.value}/users/${resetTarget.value.user_id}/reset-password`, {
      password: resetPassword.value,
    })
    resetDialog.value = false
    showSnack('Đã đặt lại mật khẩu', 'success')
  } catch (err: any) {
    showSnack(friendlyError(err), 'error')
  } finally {
    resettingPassword.value = false
  }
}

function friendlyError(err: any): string {
  const key = err?.response?.data?.error
  const msg = err?.response?.data?.message
  if (msg) return msg
  const map: Record<string, string> = {
    weak_password: 'Mật khẩu phải có ít nhất 8 ký tự, 1 chữ hoa và 1 chữ số',
    email_already_exists: 'Email đã tồn tại',
    invalid_request: 'Vui lòng kiểm tra lại thông tin',
    password_reset_failed: 'Không thể đặt lại mật khẩu',
  }
  return map[key] || key || 'Có lỗi xảy ra'
}

function showSnack(text: string, color: string) {
  snackText.value = text
  snackColor.value = color
  snack.value = true
}
</script>
