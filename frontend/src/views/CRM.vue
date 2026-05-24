<template>
  <div>
    <div class="d-flex align-center mb-6">
      <h1 class="text-h5 font-weight-bold">{{ $t('nav_crm') }}</h1>
      <v-spacer />
      
      <!-- Action buttons depending on active tab -->
      <v-btn v-if="currentTab === 'groups'" color="primary" prepend-icon="mdi-plus-box" @click="openCreateGroupDialog">
        Thêm nhóm mới
      </v-btn>
      <v-btn v-else color="teal" prepend-icon="mdi-account-plus" @click="inviteDialog = true; inviteForm = { name: '', phone_number: '' }">
        Tạo mã kích hoạt khách
      </v-btn>
    </div>

    <!-- Navigation Tabs -->
    <v-tabs v-model="currentTab" color="primary" class="mb-4">
      <v-tab value="groups">
        <v-icon start>mdi-account-group</v-icon>
        {{ $t('crm_groups') }}
      </v-tab>
      <v-tab value="customers">
        <v-icon start>mdi-account-check</v-icon>
        {{ $t('crm_customers') }}
      </v-tab>
      <v-tab value="approvals">
        <v-icon start>mdi-account-clock</v-icon>
        {{ $t('crm_approvals') }}
        <v-chip v-if="pendingApprovalCount > 0" color="error" size="x-small" class="ml-2 font-weight-bold">
          {{ pendingApprovalCount }}
        </v-chip>
      </v-tab>
    </v-tabs>

    <!-- Tab Contents -->
    <v-window v-model="currentTab">
      
      <!-- TAB 1: CRM GROUPS (GMF) -->
      <v-window-item value="groups" class="pt-2">
        <v-card :loading="loadingGroups">
          <v-table density="comfortable">
            <thead>
              <tr>
                <th>Tên nhóm</th>
                <th>Mô tả</th>
                <th>Thành viên</th>
                <th>Ngày tạo</th>
                <th>Hành động</th>
              </tr>
            </thead>
            <tbody>
              <tr v-for="g in groups" :key="g.id">
                <td>
                  <div class="font-weight-bold text-primary">{{ g.name }}</div>
                  <div v-if="g.zalo_group_id" class="d-flex align-center mt-1">
                    <v-chip size="x-small" color="success" class="font-weight-bold" variant="flat">
                      <v-icon start size="10">mdi-message-text-outline</v-icon>
                      Đã liên kết Zalo GMF
                    </v-chip>
                  </div>
                </td>
                <td class="text-body-2 text-grey-darken-1">{{ g.description || '—' }}</td>
                <td>
                  <v-chip size="small" color="blue" variant="tonal" class="mr-2">
                    <v-icon start size="14">mdi-badge-account-outline</v-icon>
                    {{ g.employees ? g.employees.length : 0 }} Nhân viên
                  </v-chip>
                  <v-chip size="small" color="teal" variant="tonal">
                    <v-icon start size="14">mdi-account-star-outline</v-icon>
                    {{ g.customers ? g.customers.length : 0 }} Khách hàng
                  </v-chip>
                </td>
                <td class="text-caption">{{ new Date(g.created_at).toLocaleDateString() }}</td>
                <td>
                  <v-btn v-if="g.zalo_group_link" icon="mdi-qrcode" size="small" variant="text" color="success" @click="showGroupQR(g)" title="QR / Link nhóm Zalo" />
                  <v-btn icon="mdi-account-cog" size="small" variant="text" color="teal" @click="openManageMembersDialog(g)" title="Quản lý thành viên" />
                  <v-btn icon="mdi-pencil" size="small" variant="text" color="blue" @click="openEditGroupDialog(g)" title="Sửa nhóm" />
                  <v-btn icon="mdi-delete" size="small" variant="text" color="error" @click="deleteGroup(g.id)" title="Xóa nhóm" />
                </td>
              </tr>
              <tr v-if="groups.length === 0 && !loadingGroups">
                <td colspan="5" class="text-center py-8 text-grey text-body-2">
                  <v-icon size="40" color="grey-lighten-1" class="mb-2">mdi-account-group-outline</v-icon>
                  <div>Chưa có nhóm CRM nào được tạo.</div>
                </td>
              </tr>
            </tbody>
          </v-table>
        </v-card>
      </v-window-item>

      <!-- TAB 2: ACTIVE ZALO CUSTOMERS -->
      <v-window-item value="customers" class="pt-2">
        <v-card :loading="loadingCustomers">
          <v-table density="comfortable">
            <thead>
              <tr>
                <th>Ảnh</th>
                <th>Tên Zalo</th>
                <th>SĐT liên kết</th>
                <th>Mã Khách Hàng (Postgres)</th>
                <th>Zalo User ID</th>
                <th>Ngày liên kết</th>
                <th>Hành động</th>
              </tr>
            </thead>
            <tbody>
              <tr v-for="c in approvedCustomers" :key="c.id">
                <td>
                  <v-avatar size="32" color="teal-lighten-4">
                    <v-img v-if="c.avatar" :src="c.avatar" />
                    <v-icon v-else color="teal" size="20">mdi-account</v-icon>
                  </v-avatar>
                </td>
                <td>
                  <div class="font-weight-bold">{{ c.name }}</div>
                </td>
                <td>{{ c.phone_number || '—' }}</td>
                <td>
                  <v-chip color="success" size="small" variant="flat" class="font-weight-black">
                    {{ c.customer_code }}
                  </v-chip>
                </td>
                <td>
                  <code class="text-caption bg-grey-lighten-3 px-1 rounded">{{ c.zalo_user_id }}</code>
                </td>
                <td class="text-caption">{{ new Date(c.updated_at).toLocaleString() }}</td>
                <td>
                  <v-btn icon="mdi-delete" size="small" color="error" variant="text" @click="deleteCustomer(c.id)" title="Xóa khách hàng" />
                </td>
              </tr>
              <tr v-if="approvedCustomers.length === 0 && !loadingCustomers">
                <td colspan="7" class="text-center py-8 text-grey text-body-2">
                  <v-icon size="40" color="grey-lighten-1" class="mb-2">mdi-account-off-outline</v-icon>
                  <div>Chưa có khách hàng Zalo nào được kích hoạt.</div>
                </td>
              </tr>
            </tbody>
          </v-table>
        </v-card>
      </v-window-item>

      <!-- TAB 3: CUSTOMER APPROVALS -->
      <v-window-item value="approvals" class="pt-2">
        <v-card :loading="loadingCustomers" class="mb-6">
          <v-card-title class="text-subtitle-1 font-weight-bold pb-0">Chờ phê duyệt mã khách hàng</v-card-title>
          <v-table density="comfortable">
            <thead>
              <tr>
                <th>Ảnh</th>
                <th>Tên khách hàng</th>
                <th>SĐT</th>
                <th>Zalo User ID</th>
                <th>Thời gian gửi</th>
                <th>Thao tác</th>
              </tr>
            </thead>
            <tbody>
              <tr v-for="c in pendingApprovalCustomers" :key="c.id">
                <td>
                  <v-avatar size="32" color="warning-lighten-4">
                    <v-img v-if="c.avatar" :src="c.avatar" />
                    <v-icon v-else color="warning" size="20">mdi-account</v-icon>
                  </v-avatar>
                </td>
                <td>
                  <div class="font-weight-bold">{{ c.name }}</div>
                </td>
                <td>{{ c.phone_number || '—' }}</td>
                <td>
                  <code class="text-caption bg-grey-lighten-3 px-1 rounded">{{ c.zalo_user_id }}</code>
                </td>
                <td class="text-caption">{{ new Date(c.updated_at).toLocaleString() }}</td>
                <td>
                  <v-btn color="success" size="small" prepend-icon="mdi-check" class="text-none" @click="openApproveDialog(c)">
                    Duyệt & Gán mã
                  </v-btn>
                  <v-btn icon="mdi-delete" size="small" color="error" variant="text" class="ml-2" @click="deleteCustomer(c.id)" />
                </td>
              </tr>
              <tr v-if="pendingApprovalCustomers.length === 0 && !loadingCustomers">
                <td colspan="6" class="text-center py-6 text-grey text-body-2">
                  Không có yêu cầu phê duyệt nào.
                </td>
              </tr>
            </tbody>
          </v-table>
        </v-card>

        <v-card :loading="loadingCustomers">
          <v-card-title class="text-subtitle-1 font-weight-bold pb-0">Yêu cầu liên kết chờ quét QR</v-card-title>
          <v-table density="comfortable">
            <thead>
              <tr>
                <th>Tên khách hàng</th>
                <th>SĐT liên kết</th>
                <th>Mã Verify</th>
                <th>Thời gian tạo</th>
                <th>Thao tác</th>
              </tr>
            </thead>
            <tbody>
              <tr v-for="c in pendingVerifyCustomers" :key="c.id">
                <td>
                  <div class="font-weight-bold">{{ c.name }}</div>
                </td>
                <td>{{ c.phone_number || '—' }}</td>
                <td>
                  <v-chip size="small" color="teal" variant="flat" class="font-weight-bold">
                    verify {{ c.verify_token }}
                  </v-chip>
                </td>
                <td class="text-caption">{{ new Date(c.created_at).toLocaleString() }}</td>
                <td>
                  <v-btn icon="mdi-qrcode" size="small" color="teal" variant="text" @click="showPendingQR(c)" title="Xem mã QR" />
                  <v-btn icon="mdi-delete" size="small" color="error" variant="text" class="ml-2" @click="deleteCustomer(c.id)" />
                </td>
              </tr>
              <tr v-if="pendingVerifyCustomers.length === 0 && !loadingCustomers">
                <td colspan="5" class="text-center py-6 text-grey text-body-2">
                  Không có yêu cầu liên kết chờ quét QR nào.
                </td>
              </tr>
            </tbody>
          </v-table>
        </v-card>
      </v-window-item>
    </v-window>

    <!-- Dialog 1: Tạo/Sửa nhóm -->
    <v-dialog v-model="groupDialog" max-width="500">
      <v-card class="pa-4">
        <v-card-title class="font-weight-bold">
          {{ isEditGroup ? 'Cập nhật nhóm CRM' : 'Tạo nhóm CRM mới' }}
        </v-card-title>
        <v-card-text>
          <v-form ref="groupFormRef">
            <v-text-field v-model="groupForm.name" label="Tên nhóm *" :rules="[v => !!v || 'Tên nhóm là bắt buộc']" class="mb-3" />
            <v-textarea v-model="groupForm.description" label="Mô tả nhóm" class="mb-3" rows="3" />
            
            <!-- GMF Package Selector (only for creating new group) -->
            <v-select
              v-if="!isEditGroup"
              v-model="groupForm.asset_id"
              :items="gmfPackages"
              item-title="displayName"
              item-value="asset_id"
              label="Gói dịch vụ Zalo GMF (Tự động chọn nếu trống)"
              :loading="loadingPackages"
              class="mb-3"
              clearable
              hint="Chọn gói để tạo nhóm chat thực tế trên Zalo OA"
              persistent-hint
            />
          </v-form>
        </v-card-text>
        <v-card-actions>
          <v-spacer />
          <v-btn variant="text" @click="groupDialog = false">Hủy</v-btn>
          <v-btn color="primary" :loading="savingGroup" @click="saveGroup">Lưu lại</v-btn>
        </v-card-actions>
      </v-card>
    </v-dialog>

    <!-- Dialog 2: Quản lý thành viên nhóm (GMF) -->
    <v-dialog v-model="membersDialog" max-width="650">
      <v-card class="pa-4">
        <v-card-title class="font-weight-bold d-flex align-center pb-2">
          <v-icon start color="primary">mdi-account-multiple-plus</v-icon>
          Thành viên nhóm: <span class="text-primary ml-1">{{ activeGroup?.name }}</span>
        </v-card-title>
        
        <v-card-text class="pt-0">
          <v-tabs v-model="memberTab" color="teal" class="mb-4">
            <v-tab value="employees">Nhân viên phụ trách ({{ activeGroup?.employees?.length || 0 }})</v-tab>
            <v-tab value="customers">Khách hàng thuộc nhóm ({{ activeGroup?.customers?.length || 0 }})</v-tab>
          </v-tabs>

          <v-window v-model="memberTab">
            <!-- Sub-tab: Nhân viên -->
            <v-window-item value="employees">
              <div class="d-flex align-center ga-2 mb-4">
                <v-autocomplete
                  v-model="selectedEmployeeToAdd"
                  :items="availableEmployees"
                  item-title="name"
                  item-value="user_id"
                  label="Chọn nhân viên thêm vào nhóm"
                  density="compact"
                  variant="outlined"
                  hide-details
                  return-object
                />
                <v-btn color="teal" :disabled="!selectedEmployeeToAdd" @click="addGroupEmployee">Thêm</v-btn>
              </div>

              <v-list density="compact" max-height="300" class="overflow-y-auto border rounded">
                <v-list-item v-for="emp in activeGroup?.employees" :key="emp.id">
                  <template #prepend>
                    <v-icon color="primary">mdi-account-tie</v-icon>
                  </template>
                  <v-list-item-title class="font-weight-bold">{{ emp.name }}</v-list-item-title>
                  <v-list-item-subtitle>{{ emp.email }}</v-list-item-subtitle>
                  <template #append>
                    <v-btn icon="mdi-close" size="small" variant="text" color="error" @click="removeGroupEmployee(emp.user_id)" />
                  </template>
                </v-list-item>
                <div v-if="!activeGroup?.employees?.length" class="text-center py-6 text-grey text-caption">
                  Chưa có nhân viên nào trong nhóm.
                </div>
              </v-list>
            </v-window-item>

            <!-- Sub-tab: Khách hàng -->
            <v-window-item value="customers">
              <div class="d-flex align-center ga-2 mb-4">
                <v-autocomplete
                  v-model="selectedCustomerToAdd"
                  :items="availableCustomers"
                  item-title="name"
                  item-value="id"
                  label="Chọn khách hàng thêm vào nhóm"
                  density="compact"
                  variant="outlined"
                  hide-details
                  return-object
                />
                <v-btn color="teal" :disabled="!selectedCustomerToAdd" @click="addGroupCustomer">Thêm</v-btn>
              </div>

              <v-list density="compact" max-height="300" class="overflow-y-auto border rounded">
                <v-list-item v-for="cust in activeGroup?.customers" :key="cust.id">
                  <template #prepend>
                    <v-avatar size="24" class="mr-2">
                      <v-img v-if="cust.avatar" :src="cust.avatar" />
                      <v-icon v-else color="teal">mdi-account</v-icon>
                    </v-avatar>
                  </template>
                  <v-list-item-title class="font-weight-bold">
                    {{ cust.name }} 
                    <v-chip size="x-small" color="success" class="ml-2">{{ cust.customer_code }}</v-chip>
                  </v-list-item-title>
                  <v-list-item-subtitle>Zalo ID: {{ cust.zalo_user_id }}</v-list-item-subtitle>
                  <template #append>
                    <v-btn
                      v-if="activeGroup?.zalo_group_link"
                      icon="mdi-send"
                      size="small"
                      variant="text"
                      color="teal"
                      class="mr-2"
                      @click="inviteCustomerToZaloGroup(cust.id)"
                      title="Gửi tin nhắn mời vào nhóm Zalo"
                    />
                    <v-btn icon="mdi-close" size="small" variant="text" color="error" @click="removeGroupCustomer(cust.id)" />
                  </template>
                </v-list-item>
                <div v-if="!activeGroup?.customers?.length" class="text-center py-6 text-grey text-caption">
                  Chưa có khách hàng nào trong nhóm.
                </div>
              </v-list>
            </v-window-item>
          </v-window>
        </v-card-text>
        <v-card-actions>
          <v-spacer />
          <v-btn color="teal" @click="membersDialog = false">Đóng</v-btn>
        </v-card-actions>
      </v-card>
    </v-dialog>

    <!-- Dialog 3: Tạo mã kích hoạt khách mới -->
    <v-dialog v-model="inviteDialog" max-width="450">
      <v-card class="pa-4">
        <v-card-title class="font-weight-bold">Tạo mã kích hoạt khách hàng</v-card-title>
        <v-card-text>
          <v-form ref="inviteFormRef">
            <v-text-field v-model="inviteForm.name" label="Tên khách hàng gợi nhớ *" :rules="[v => !!v || 'Bắt buộc nhập']" class="mb-3" />
            <v-text-field v-model="inviteForm.phone_number" label="Số điện thoại liên kết" hint="Ví dụ: 0987654321" persistent-hint />
          </v-form>
        </v-card-text>
        <v-card-actions>
          <v-spacer />
          <v-btn variant="text" @click="inviteDialog = false">Hủy</v-btn>
          <v-btn color="teal" :loading="creatingInvite" @click="createInvite">Tạo mã liên kết</v-btn>
        </v-card-actions>
      </v-card>
    </v-dialog>

    <!-- Dialog 4: Duyệt & Gán mã khách hàng -->
    <v-dialog v-model="approveDialog" max-width="480">
      <v-card class="pa-4">
        <v-card-title class="font-weight-bold pb-2">Phê duyệt & Phân mã khách hàng</v-card-title>
        <v-card-text>
          <div class="d-flex align-center bg-grey-lighten-4 pa-3 rounded mb-4">
            <v-avatar size="40" class="mr-3">
              <v-img v-if="customerToApprove?.avatar" :src="customerToApprove.avatar" />
              <v-icon v-else color="teal">mdi-account</v-icon>
            </v-avatar>
            <div>
              <div class="font-weight-bold">{{ customerToApprove?.name }}</div>
              <div class="text-caption text-grey">Zalo ID: {{ customerToApprove?.zalo_user_id }}</div>
            </div>
          </div>

          <v-form ref="approveFormRef">
            <!-- Searchable Dropdown for Postgres ma_khach_hang -->
            <v-autocomplete
              v-model="approveForm.customer_code"
              :items="customerCodes"
              label="Chọn mã khách hàng (Từ Cloudify) *"
              :rules="[v => !!v || 'Vui lòng chọn mã khách hàng']"
              :loading="loadingCodes"
              class="mb-4"
              variant="outlined"
              no-data-text="Không tìm thấy mã khách hàng nào"
            />

            <!-- Select groups to add customer to -->
            <v-select
              v-model="approveForm.group_ids"
              :items="groups"
              item-title="name"
              item-value="id"
              label="Gán vào các nhóm CRM (Tùy chọn)"
              multiple
              chips
              variant="outlined"
              density="comfortable"
            />
          </v-form>
        </v-card-text>
        <v-card-actions>
          <v-spacer />
          <v-btn variant="text" @click="approveDialog = false">Hủy</v-btn>
          <v-btn color="success" :loading="approvingCustomer" @click="approveCustomer">Xác nhận duyệt</v-btn>
        </v-card-actions>
      </v-card>
    </v-dialog>

    <!-- Dialog 5: QR Code hiển thị mã verify cho khách hàng -->
    <v-dialog v-model="qrDialog" max-width="460">
      <v-card class="pa-6 text-center">
        <v-card-title class="font-weight-bold text-h6 justify-center">QR kích hoạt khách hàng</v-card-title>
        <v-card-text>
          <div class="text-body-1 font-weight-bold mb-1 text-teal">{{ activeInvite?.name }}</div>
          <div class="text-caption text-grey-darken-1 mb-4">
            Khách hàng quét mã QR dưới đây bằng app Zalo để nhắn tin cú pháp kích hoạt.
          </div>

          <!-- QR code container -->
          <div class="d-flex justify-center mb-4">
            <v-card variant="outlined" class="pa-2" style="border-color: #008fe5 !important;">
              <v-img
                v-if="activeZaloOA"
                :src="`https://api.qrserver.com/v1/create-qr-code/?size=200x200&data=${encodeURIComponent('https://zalo.me/' + activeZaloOA.external_id)}`"
                width="200"
                height="200"
              />
              <div v-else class="pa-6 text-caption text-grey">Chưa cấu hình Zalo OA hoạt động</div>
            </v-card>
          </div>

          <!-- Instruction code card -->
          <v-card color="teal-lighten-5" class="pa-4 mb-4" variant="flat">
            <div class="text-caption text-grey-darken-2 mb-1">Cú pháp tin nhắn gửi OA:</div>
            <div class="text-h5 font-weight-black text-teal" style="letter-spacing: 1px;">
              verify {{ activeInvite?.verify_token }}
            </div>
          </v-card>

          <v-divider class="mb-4" />

          <div class="text-left text-body-2">
            <div class="font-weight-bold mb-1">Hướng dẫn cho khách hàng:</div>
            <ol class="pl-4">
              <li class="mb-1">Quét mã QR bằng ứng dụng Zalo để vào mục chat với OA.</li>
              <li class="mb-1">Nhắn đúng cú pháp: <strong>verify {{ activeInvite?.verify_token }}</strong> và gửi đi.</li>
              <li>Tài khoản sẽ được gửi yêu cầu xác thực tự động về hệ thống CQA.</li>
            </ol>
          </div>
        </v-card-text>
        <v-card-actions class="justify-center">
          <v-btn color="teal" variant="elevated" class="px-6" @click="qrDialog = false; fetchCustomers()">Hoàn tất</v-btn>
        </v-card-actions>
      </v-card>
    </v-dialog>

    <!-- Dialog 6: QR Code hiển thị link nhóm GMF -->
    <v-dialog v-model="groupQrDialog" max-width="450">
      <v-card class="pa-6 text-center">
        <v-card-title class="font-weight-bold text-h6 justify-center">QR Nhóm Chat Zalo</v-card-title>
        <v-card-text>
          <div class="text-body-1 font-weight-bold mb-1 text-success">{{ activeGroupQR?.name }}</div>
          <div class="text-caption text-grey-darken-1 mb-4">
            Quét mã QR dưới đây bằng ứng dụng Zalo để tham gia nhóm chat hỗ trợ.
          </div>

          <!-- QR code container -->
          <div class="d-flex justify-center mb-4">
            <v-card variant="outlined" class="pa-2" style="border-color: #4caf50 !important;">
              <v-img
                v-if="activeGroupQR?.zalo_group_link"
                :src="`https://api.qrserver.com/v1/create-qr-code/?size=200x200&data=${encodeURIComponent(activeGroupQR.zalo_group_link)}`"
                width="200"
                height="200"
              />
            </v-card>
          </div>

          <v-card color="green-lighten-5" class="pa-3 mb-4" variant="flat">
            <div class="text-caption text-grey-darken-2 mb-1">Đường dẫn tham gia:</div>
            <a :href="activeGroupQR?.zalo_group_link" target="_blank" class="text-body-2 font-weight-bold text-success text-decoration-none">
              {{ activeGroupQR?.zalo_group_link }}
            </a>
          </v-card>
        </v-card-text>
        <v-card-actions class="justify-center">
          <v-btn color="success" variant="elevated" class="px-6" @click="groupQrDialog = false">Đóng</v-btn>
        </v-card-actions>
      </v-card>
    </v-dialog>

    <v-snackbar v-model="snack" :color="snackColor" timeout="3000">{{ snackText }}</v-snackbar>
  </div>
</template>

<script setup lang="ts">
import { ref, computed, onMounted, onUnmounted } from 'vue'
import { useRoute } from 'vue-router'
import { useUserStore } from '../stores/users'
import { useChannelStore } from '../stores/channels'
import api from '../api'

const route = useRoute()
const userStore = useUserStore()
const channelStore = useChannelStore()
const tenantId = computed(() => route.params.tenantId as string)

const currentTab = ref('groups')
const memberTab = ref('employees')

// CRM Groups State
const groups = ref<any[]>([])
const loadingGroups = ref(false)
const groupDialog = ref(false)
const savingGroup = ref(false)
const isEditGroup = ref(false)
const groupForm = ref({ id: '', name: '', description: '', asset_id: '' })
const groupFormRef = ref<any>(null)

// GMF Packages State
const gmfPackages = ref<any[]>([])
const loadingPackages = ref(false)
const groupQrDialog = ref(false)
const activeGroupQR = ref<any>(null)

// Group Members management (GMF)
const activeGroup = ref<any>(null)
const membersDialog = ref(false)
const selectedEmployeeToAdd = ref<any>(null)
const selectedCustomerToAdd = ref<any>(null)

// Zalo Customers State
const customers = ref<any[]>([])
const loadingCustomers = ref(false)
const inviteDialog = ref(false)
const creatingInvite = ref(false)
const inviteForm = ref({ name: '', phone_number: '' })
const inviteFormRef = ref<any>(null)

// QR Dialog
const activeInvite = ref<any>(null)
const qrDialog = ref(false)

// Approvals state
const approveDialog = ref(false)
const customerToApprove = ref<any>(null)
const customerCodes = ref<string[]>([])
const loadingCodes = ref(false)
const approvingCustomer = ref(false)
const approveForm = ref({ customer_code: '', group_ids: [] as string[] })
const approveFormRef = ref<any>(null)

// Notification snackbar
const snack = ref(false)
const snackText = ref('')
const snackColor = ref('success')

// Computed Zalo OA active channel
const activeZaloOA = computed(() => {
  return channelStore.channels.find((c: any) => c.channel_type === 'zalo_oa' && c.is_active)
})

const approvedCustomers = computed(() => {
  return customers.value.filter(c => c.status === 'approved')
})

const pendingApprovalCustomers = computed(() => {
  return customers.value.filter(c => c.status === 'pending_approval')
})

const pendingVerifyCustomers = computed(() => {
  return customers.value.filter(c => c.status === 'pending_verify')
})

const pendingApprovalCount = computed(() => {
  return pendingApprovalCustomers.value.length
})

// Available members to add to active group
const availableEmployees = computed(() => {
  if (!activeGroup.value || !userStore.users) return []
  const existingIds = new Set(activeGroup.value.employees?.map((e: any) => e.user_id) || [])
  return userStore.users.filter((u: any) => !existingIds.has(u.user_id))
})

const availableCustomers = computed(() => {
  if (!activeGroup.value || !customers.value) return []
  const existingIds = new Set(activeGroup.value.customers?.map((c: any) => c.id) || [])
  return approvedCustomers.value.filter(c => !existingIds.has(c.id))
})

onMounted(async () => {
  window.addEventListener('crm-data-updated', fetchCustomers)
  await userStore.fetchUsers(tenantId.value)
  await channelStore.fetchChannels(tenantId.value)
  await fetchGroups()
  await fetchCustomers()
  await fetchCustomerCodes()
  await fetchGmfPackages()
})

onUnmounted(() => {
  window.removeEventListener('crm-data-updated', fetchCustomers)
})

// Load Groups from Backend
async function fetchGroups() {
  loadingGroups.value = true
  try {
    const { data } = await api.get(`/tenants/${tenantId.value}/crm/groups`)
    groups.value = data || []
  } catch (err) {
    showSnack('Không thể tải danh sách nhóm CRM', 'error')
  } finally {
    loadingGroups.value = false
  }
}

// Load Zalo Customers from Backend
async function fetchCustomers() {
  loadingCustomers.value = true
  try {
    const { data } = await api.get(`/tenants/${tenantId.value}/crm/customers`)
    customers.value = data || []
  } catch (err) {
    showSnack('Không thể tải danh sách khách hàng Zalo', 'error')
  } finally {
    loadingCustomers.value = false
  }
}

// Load PostgreSQL customer codes
async function fetchCustomerCodes() {
  loadingCodes.value = true
  try {
    const { data } = await api.get(`/tenants/${tenantId.value}/crm/customer-profiles`)
    customerCodes.value = data || []
  } catch (err) {
    showSnack('Không thể kết nối tải mã khách hàng Postgres', 'warning')
  } finally {
    loadingCodes.value = false
  }
}

async function fetchGmfPackages() {
  loadingPackages.value = true
  try {
    const { data } = await api.get(`/tenants/${tenantId.value}/crm/gmf-packages`)
    gmfPackages.value = (data || []).map((pkg: any) => ({
      ...pkg,
      displayName: `${pkg.asset_type} (${pkg.used_group}/${pkg.total_group} nhóm)`
    }))
  } catch (err) {
    console.error('Failed to fetch GMF packages', err)
  } finally {
    loadingPackages.value = false
  }
}

function showGroupQR(g: any) {
  activeGroupQR.value = g
  groupQrDialog.value = true
}

async function inviteCustomerToZaloGroup(customerId: string) {
  try {
    await api.post(`/tenants/${tenantId.value}/crm/groups/${activeGroup.value.id}/invite-customer`, {
      customer_id: customerId
    })
    showSnack('Đã gửi tin nhắn mời khách hàng tham gia nhóm Zalo', 'success')
  } catch (err: any) {
    showSnack(err.response?.data?.error || 'Lỗi gửi tin nhắn mời', 'error')
  }
}

// Group Actions
function openCreateGroupDialog() {
  isEditGroup.value = false
  groupForm.value = { id: '', name: '', description: '', asset_id: '' }
  fetchGmfPackages()
  groupDialog.value = true
}

function openEditGroupDialog(g: any) {
  isEditGroup.value = true
  groupForm.value = { id: g.id, name: g.name, description: g.description, asset_id: g.zalo_asset_id || '' }
  groupDialog.value = true
}

async function saveGroup() {
  const { valid } = await groupFormRef.value?.validate() || {}
  if (!valid) return

  savingGroup.value = true
  try {
    if (isEditGroup.value) {
      await api.put(`/tenants/${tenantId.value}/crm/groups/${groupForm.value.id}`, {
        name: groupForm.value.name,
        description: groupForm.value.description
      })
      showSnack('Đã cập nhật nhóm thành công', 'success')
    } else {
      await api.post(`/tenants/${tenantId.value}/crm/groups`, {
        name: groupForm.value.name,
        description: groupForm.value.description,
        asset_id: groupForm.value.asset_id
      })
      showSnack('Đã tạo nhóm mới thành công', 'success')
    }
    groupDialog.value = false
    await fetchGroups()
  } catch (err: any) {
    showSnack(err.response?.data?.error || 'Có lỗi xảy ra', 'error')
  } finally {
    savingGroup.value = false
  }
}

async function deleteGroup(id: string) {
  if (!confirm('Bạn có chắc chắn muốn xóa nhóm CRM này? Các liên kết thành viên sẽ bị gỡ bỏ.')) return
  try {
    await api.delete(`/tenants/${tenantId.value}/crm/groups/${id}`)
    showSnack('Đã xóa nhóm CRM thành công', 'success')
    await fetchGroups()
  } catch (err) {
    showSnack('Lỗi khi xóa nhóm', 'error')
  }
}

// Manage Group Members (GMF)
function openManageMembersDialog(g: any) {
  activeGroup.value = g
  selectedEmployeeToAdd.value = null
  selectedCustomerToAdd.value = null
  memberTab.value = 'employees'
  membersDialog.value = true
}

async function addGroupEmployee() {
  if (!selectedEmployeeToAdd.value || !activeGroup.value) return
  try {
    await api.post(`/tenants/${tenantId.value}/crm/groups/${activeGroup.value.id}/members`, {
      employee_ids: [selectedEmployeeToAdd.value.user_id]
    })
    
    // Update local state
    if (!activeGroup.value.employees) activeGroup.value.employees = []
    activeGroup.value.employees.push(selectedEmployeeToAdd.value)
    selectedEmployeeToAdd.value = null
    showSnack('Đã thêm nhân viên vào nhóm', 'success')
    await fetchGroups() // reload values
  } catch (err) {
    showSnack('Lỗi thêm nhân viên', 'error')
  }
}

async function removeGroupEmployee(empID: string) {
  try {
    await api.delete(`/tenants/${tenantId.value}/crm/groups/${activeGroup.value.id}/members`, {
      data: { employee_ids: [empID] }
    })
    activeGroup.value.employees = activeGroup.value.employees.filter((e: any) => e.user_id !== empID)
    showSnack('Đã gỡ nhân viên khỏi nhóm', 'success')
    await fetchGroups()
  } catch (err) {
    showSnack('Lỗi khi gỡ nhân viên', 'error')
  }
}

async function addGroupCustomer() {
  if (!selectedCustomerToAdd.value || !activeGroup.value) return
  try {
    await api.post(`/tenants/${tenantId.value}/crm/groups/${activeGroup.value.id}/members`, {
      customer_ids: [selectedCustomerToAdd.value.id]
    })
    if (!activeGroup.value.customers) activeGroup.value.customers = []
    activeGroup.value.customers.push(selectedCustomerToAdd.value)
    selectedCustomerToAdd.value = null
    showSnack('Đã thêm khách hàng vào nhóm', 'success')
    await fetchGroups()
  } catch (err) {
    showSnack('Lỗi thêm khách hàng', 'error')
  }
}

async function removeGroupCustomer(custID: string) {
  try {
    await api.delete(`/tenants/${tenantId.value}/crm/groups/${activeGroup.value.id}/members`, {
      data: { customer_ids: [custID] }
    })
    activeGroup.value.customers = activeGroup.value.customers.filter((c: any) => c.id !== custID)
    showSnack('Đã gỡ khách hàng khỏi nhóm', 'success')
    await fetchGroups()
  } catch (err) {
    showSnack('Lỗi khi gỡ khách hàng', 'error')
  }
}

// Invite Customer (Create verify token)
async function createInvite() {
  const { valid } = await inviteFormRef.value?.validate() || {}
  if (!valid) return

  creatingInvite.value = true
  try {
    const { data } = await api.post(`/tenants/${tenantId.value}/crm/customers/invite`, {
      name: inviteForm.value.name,
      phone_number: inviteForm.value.phone_number
    })
    activeInvite.value = data
    inviteDialog.value = false
    qrDialog.value = true
    await fetchCustomers()
  } catch (err) {
    showSnack('Lỗi tạo mã xác thực', 'error')
  } finally {
    creatingInvite.value = false
  }
}

function showPendingQR(c: any) {
  activeInvite.value = c
  qrDialog.value = true
}

// Approve Customer & Assign ma_khach_hang
function openApproveDialog(c: any) {
  customerToApprove.value = c
  approveForm.value = { customer_code: '', group_ids: [] }
  approveDialog.value = true
}

async function approveCustomer() {
  const { valid } = await approveFormRef.value?.validate() || {}
  if (!valid) return

  approvingCustomer.value = true
  try {
    await api.post(`/tenants/${tenantId.value}/crm/customers/${customerToApprove.value.id}/approve`, {
      customer_code: approveForm.value.customer_code,
      group_ids: approveForm.value.group_ids
    })
    showSnack(`Đã duyệt khách hàng với mã ${approveForm.value.customer_code}`, 'success')
    approveDialog.value = false
    await fetchCustomers()
    await fetchGroups() // reload groups members counts
  } catch (err) {
    showSnack('Lỗi phê duyệt khách hàng', 'error')
  } finally {
    approvingCustomer.value = false
  }
}

async function deleteCustomer(id: string) {
  if (!confirm('Bạn có chắc muốn hủy liên kết/xóa khách hàng này?')) return
  try {
    await api.delete(`/tenants/${tenantId.value}/crm/customers/${id}`)
    showSnack('Đã xóa khách hàng thành công', 'success')
    await fetchCustomers()
    await fetchGroups()
  } catch (err) {
    showSnack('Lỗi khi xóa khách hàng', 'error')
  }
}

function showSnack(text: string, color: string) {
  snackText.value = text
  snackColor.value = color
  snack.value = true
}
</script>

<style scoped>
.v-table {
  border-radius: 4px;
}
.v-list-item {
  border-bottom: 1px solid rgba(0, 0, 0, 0.05);
}
</style>
