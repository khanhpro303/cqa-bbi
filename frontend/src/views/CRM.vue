<template>
  <div>
    <div class="d-flex align-center mb-6">
      <h1 class="text-h5 font-weight-bold">{{ $t('nav_crm') }}</h1>
      <v-spacer />
      
      <!-- Action buttons depending on active tab -->
      <v-btn v-if="currentTab === 'groups'" color="primary" prepend-icon="mdi-plus-box" @click="openCreateGroupDialog">
        Thêm nhóm mới
      </v-btn>
      <v-btn v-else color="teal" prepend-icon="mdi-account-plus" @click="openInviteDialog">
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
      <v-tab value="cloudify_customers">
        <v-icon start>mdi-database-outline</v-icon>
        {{ $t('crm_cloudify_customers') }}
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
                <td class="py-3">
                  <div class="font-weight-bold text-primary">{{ g.name }}</div>
                  <div class="d-flex align-center mt-1 flex-wrap ga-1">
                    <v-chip v-if="g.zalo_group_id" size="x-small" color="success" class="font-weight-bold" variant="flat">
                      <v-icon start size="10">mdi-message-text-outline</v-icon>
                      Đã liên kết Zalo GMF
                    </v-chip>
                    <v-chip v-if="g.channel" size="x-small" color="blue-grey" class="font-weight-bold" variant="outlined">
                      OA: {{ g.channel.name }}
                    </v-chip>
                    <v-chip v-if="g.customer_code" size="x-small" color="indigo" class="font-weight-bold" variant="flat">
                      Mã KH: {{ g.customer_code }}
                    </v-chip>
                  </div>
                </td>
                <td class="text-body-2 text-grey-darken-1 py-3">{{ g.description || '—' }}</td>
                <td class="py-3">
                  <v-chip size="small" color="blue" variant="tonal" class="mr-2">
                    <v-icon start size="14">mdi-badge-account-outline</v-icon>
                    {{ g.employees ? g.employees.length : 0 }} Nhân viên
                  </v-chip>
                  <v-chip size="small" color="teal" variant="tonal">
                    <v-icon start size="14">mdi-account-star-outline</v-icon>
                    {{ g.customers ? g.customers.length : 0 }} Khách hàng
                  </v-chip>
                </td>
                <td class="text-caption py-3">{{ new Date(g.created_at).toLocaleDateString() }}</td>
                <td class="py-3">
                  <v-btn v-if="g.zalo_group_link" icon="mdi-qrcode" size="small" variant="text" color="success" @click="showGroupQR(g)" title="QR / Link nhóm Zalo" />
                  <v-btn icon="mdi-account-cog" size="small" variant="text" color="teal" @click="openManageMembersDialog(g)" title="Quản lý thành viên" />
                  <v-btn icon="mdi-shield-lock-outline" size="small" variant="text" color="indigo" @click="openGroupPermissionsDialog(g)" title="Phân quyền Endpoint & Sơ đồ live" />
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
                <th>Mã KH</th>
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

      <!-- TAB 2.5: CLOUDIFY CUSTOMERS -->
      <v-window-item value="cloudify_customers" class="pt-2">
        <v-card :loading="loadingCloudifyCustomers">
          <v-card-title class="d-flex align-center py-3 px-4">
            <v-text-field
              v-model="cloudifySearch"
              prepend-inner-icon="mdi-magnify"
              label="Tìm kiếm khách hàng..."
              single-line
              flat
              hide-details
              variant="outlined"
              density="compact"
              style="max-width: 320px;"
            />
            <v-spacer />
            <v-btn
              color="teal"
              prepend-icon="mdi-refresh"
              variant="tonal"
              size="small"
              class="text-none font-weight-medium"
              @click="fetchCloudifyCustomers"
            >
              Lấy lại dữ liệu
            </v-btn>
          </v-card-title>
          <v-table density="compact">
            <thead>
              <tr>
                <th style="width: 90px;">Mã KH</th>
                <th style="min-width: 180px;">Tên KH</th>
                <th>Địa chỉ</th>
                <th style="width: 80px;">Miền</th>
                <th style="width: 120px;">SĐT Cloudify</th>
                <th style="width: 100px;">SĐT CQA</th>
                <th style="width: 110px;">Trạng thái</th>
                <th style="width: 130px;">Verify</th>
                <th style="width: 90px;" class="text-center">Hành động</th>
              </tr>
            </thead>
            <tbody>
              <tr v-for="c in paginatedCloudifyCustomers" :key="c.customer_code">
                <td>
                  <div class="text-truncate" style="max-width: 85px;" :title="c.customer_code">
                    <v-chip color="success" size="small" variant="flat" class="font-weight-black text-truncate" style="max-width: 100%;">
                      {{ c.customer_code }}
                    </v-chip>
                  </div>
                </td>
                <td class="font-weight-bold">{{ c.name }}</td>
                <td>
                  <div class="text-body-2 text-grey-darken-1 text-truncate" style="max-width: 320px;" :title="c.address">
                    {{ c.address || '—' }}
                  </div>
                </td>
                <td>
                  <v-chip v-if="c.region" size="x-small" color="blue-grey" variant="outlined">{{ c.region }}</v-chip>
                  <span v-else>—</span>
                </td>
                <td>{{ c.cloudify_phone || '—' }}</td>
                <td class="font-weight-bold text-teal">{{ c.cqa_phone || '—' }}</td>
                <td>
                  <v-chip v-if="c.zalo_user_id" color="success" size="x-small" class="font-weight-bold" variant="flat">
                    Đã liên kết
                  </v-chip>
                  <v-chip v-else-if="c.cqa_phone" color="warning" size="x-small" class="font-weight-bold" variant="outlined">
                    Chờ quét QR
                  </v-chip>
                  <v-chip v-else color="grey-lighten-1" size="x-small" variant="flat">
                    Chưa thiết lập
                  </v-chip>
                </td>
                <td>
                  <div v-if="c.verify_token" class="d-flex align-center ga-1">
                    <code class="text-caption bg-teal-lighten-5 text-teal px-1 rounded font-weight-bold">
                      verify {{ c.verify_token }}
                    </code>
                    <v-btn icon="mdi-qrcode" size="x-small" color="teal" variant="text" @click="showCloudifyQR(c)" title="Xem mã QR kích hoạt" />
                  </div>
                  <span v-else>—</span>
                </td>
                <td class="text-center">
                  <v-btn icon="mdi-cellphone-link" size="small" color="teal" variant="text" @click="openAssignPhoneDialog(c)" title="Gán số điện thoại thủ công" />
                </td>
              </tr>
              <tr v-if="filteredCloudifyCustomers.length === 0 && !loadingCloudifyCustomers">
                <td colspan="9" class="text-center py-8 text-grey text-body-2">
                  <v-icon size="40" color="grey-lighten-1" class="mb-2">mdi-account-search-outline</v-icon>
                  <div>Không tìm thấy khách hàng nào.</div>
                </td>
              </tr>
            </tbody>
          </v-table>
          <v-divider />
          <div class="d-flex align-center justify-space-between py-2 px-4 flex-wrap ga-2">
            <div class="text-caption text-grey">
              Hiển thị {{ paginatedCloudifyCustomers.length }} / {{ filteredCloudifyCustomers.length }} khách hàng
            </div>
            <v-pagination
              v-model="cloudifyPage"
              :length="cloudifyPageCount"
              :total-visible="5"
              density="compact"
              size="small"
              active-color="teal"
            />
          </div>
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
      <v-card>
        <v-card-title class="px-6 pt-6 pb-2 font-weight-bold">
          {{ isEditGroup ? 'Cập nhật nhóm CRM' : 'Tạo nhóm CRM mới' }}
        </v-card-title>
        <v-card-text class="px-6 py-2">
          <v-form ref="groupFormRef">
            <v-text-field v-model="groupForm.name" label="Tên nhóm *" :rules="[v => !!v || 'Tên nhóm là bắt buộc']" class="mb-3" />
            <v-textarea v-model="groupForm.description" label="Mô tả nhóm" class="mb-3" rows="3" />

            <!-- Customer Code Selector (Required for both create & edit) -->
            <v-autocomplete
              v-model="groupForm.customer_code"
              :items="customerCodes"
              label="Mã khách hàng phục vụ *"
              placeholder="Tìm và chọn mã khách hàng..."
              :rules="[v => !!v || 'Vui lòng chọn mã khách hàng']"
              :loading="loadingCodes"
              class="mb-3"
              variant="outlined"
              density="comfortable"
              no-data-text="Không tìm thấy mã khách hàng nào"
            />
            
            <!-- Zalo OA Account Selector (only for creating new group) -->
            <v-select
              v-if="!isEditGroup"
              v-model="groupForm.channel_id"
              :items="zaloOAChannels"
              item-title="name"
              item-value="id"
              label="Tài khoản Zalo OA *"
              :rules="[v => !!v || 'Tài khoản Zalo OA là bắt buộc']"
              class="mb-3"
              @update:model-value="onChannelSelected"
            />

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
        <v-card-actions class="px-6 pb-6 pt-2">
          <v-spacer />
          <v-btn variant="text" @click="groupDialog = false">Hủy</v-btn>
          <v-btn color="primary" :loading="savingGroup" @click="saveGroup">Lưu lại</v-btn>
        </v-card-actions>
      </v-card>
    </v-dialog>

    <!-- Dialog 2: Quản lý thành viên nhóm (GMF) -->
    <v-dialog v-model="membersDialog" max-width="650">
      <v-card>
        <v-card-title class="px-6 pt-6 pb-2 font-weight-bold d-flex align-center">
          <v-icon start color="primary">mdi-account-multiple-plus</v-icon>
          Thành viên nhóm: <span class="text-primary ml-1">{{ activeGroup?.name }}</span>
        </v-card-title>
        
        <v-card-text class="px-6 py-2">
          <v-tabs v-model="memberTab" color="teal" class="mb-4">
            <v-tab value="employees">Nhân viên phụ trách ({{ activeGroup?.employees?.length || 0 }})</v-tab>
            <v-tab value="customers">Khách hàng thuộc nhóm ({{ loadingLiveMembers ? '...' : liveCustomers.length }})</v-tab>
            <v-tab value="pending">Chờ duyệt tham gia ({{ loadingPendingInvites ? '...' : pendingInvites.length }})</v-tab>
          </v-tabs>

          <v-window v-model="memberTab">
            <!-- Sub-tab: Nhân viên -->
            <v-window-item value="employees">
              <div class="text-caption font-weight-bold mb-2 text-grey-darken-2">Chọn nhân viên thêm vào nhóm:</div>
              <div class="d-flex align-center ga-2 mb-4">
                <v-autocomplete
                  v-model="selectedEmployeeToAdd"
                  :items="availableEmployees"
                  item-title="name"
                  item-value="id"
                  placeholder="Tìm và chọn nhân viên..."
                  density="comfortable"
                  variant="outlined"
                  hide-details
                  return-object
                  class="flex-grow-1"
                />
                <v-btn color="teal" height="48" :disabled="!selectedEmployeeToAdd" @click="addGroupEmployee">Thêm</v-btn>
              </div>

              <v-list density="compact" max-height="300" class="overflow-y-auto border rounded">
                <v-list-item v-for="emp in activeGroup?.employees" :key="emp.id">
                  <template #prepend>
                    <v-icon color="primary">mdi-account-tie</v-icon>
                  </template>
                  <v-list-item-title class="font-weight-bold">{{ emp.name }}</v-list-item-title>
                  <v-list-item-subtitle>Zalo ID: {{ emp.zalo_user_id }}</v-list-item-subtitle>
                  <template #append>
                    <v-btn icon="mdi-close" size="small" variant="text" color="error" @click="removeGroupEmployee(emp.id)" />
                  </template>
                </v-list-item>
                <div v-if="!activeGroup?.employees?.length" class="text-center py-6 text-grey text-caption">
                  Chưa có nhân viên nào trong nhóm.
                </div>
              </v-list>
            </v-window-item>

            <!-- Sub-tab: Khách hàng -->
            <v-window-item value="customers">
              <div class="text-caption font-weight-bold mb-2 text-grey-darken-2">Chọn khách hàng thêm vào nhóm:</div>
              <div class="d-flex align-center ga-2 mb-4">
                <v-autocomplete
                  v-model="selectedCustomerToAdd"
                  :items="availableCustomers"
                  item-title="name"
                  item-value="id"
                  placeholder="Tìm và chọn khách hàng..."
                  density="comfortable"
                  variant="outlined"
                  hide-details
                  return-object
                  class="flex-grow-1"
                />
                <v-btn color="teal" height="48" :disabled="!selectedCustomerToAdd" @click="addGroupCustomer">Thêm</v-btn>
              </div>

              <!-- Loading spinner -->
              <div v-if="loadingLiveMembers" class="text-center py-6">
                <v-progress-circular indeterminate color="teal" />
                <div class="text-caption text-grey mt-2">Đang tải thành viên từ Zalo...</div>
              </div>

              <v-list v-else density="compact" max-height="300" class="overflow-y-auto border rounded">
                <v-list-item v-for="cust in liveCustomers" :key="cust.zalo_user_id">
                  <template #prepend>
                    <v-avatar size="24" class="mr-2">
                      <v-img v-if="cust.avatar" :src="cust.avatar" />
                      <v-icon v-else color="teal">mdi-account</v-icon>
                    </v-avatar>
                  </template>
                  <v-list-item-title class="font-weight-bold">
                    {{ cust.name }} 
                    <v-chip v-if="cust.customer_code" size="x-small" color="success" class="ml-2">{{ cust.customer_code }}</v-chip>
                    <v-chip v-else size="x-small" color="warning" class="ml-2">Chưa đồng bộ CRM</v-chip>
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
                       :disabled="!cust.id"
                       title="Gửi tin nhắn mời vào nhóm Zalo"
                    />
                    <v-btn icon="mdi-close" size="small" variant="text" color="error" @click="removeGroupCustomer(cust.id, cust.zalo_user_id)" />
                  </template>
                </v-list-item>
                <div v-if="!liveCustomers.length" class="text-center py-6 text-grey text-caption">
                  Chưa có khách hàng nào trong nhóm.
                </div>
              </v-list>
            </v-window-item>

            <!-- Sub-tab: Chờ duyệt tham gia -->
            <v-window-item value="pending">
              <!-- Loading spinner -->
              <div v-if="loadingPendingInvites" class="text-center py-6">
                <v-progress-circular indeterminate color="warning" />
                <div class="text-caption text-grey mt-2">Đang tải danh sách chờ duyệt từ Zalo...</div>
              </div>

              <div v-else>
                <v-list density="compact" max-height="300" class="overflow-y-auto border rounded">
                  <v-list-item v-for="invitee in pendingInvites" :key="invitee.user_id">
                    <template #prepend>
                      <v-avatar size="24" class="mr-2" color="amber-lighten-4">
                        <v-icon color="amber-darken-2" size="16">mdi-account-clock</v-icon>
                      </v-avatar>
                    </template>
                    <v-list-item-title class="font-weight-bold text-warning-darken-2">
                      {{ invitee.name }}
                    </v-list-item-title>
                    <v-list-item-subtitle>Zalo ID: {{ invitee.user_id }}</v-list-item-subtitle>
                    <template #append>
                      <v-btn
                        color="success"
                        size="small"
                        variant="flat"
                        class="text-none font-weight-bold mr-2 px-2"
                        prepend-icon="mdi-check"
                        :loading="processingInvite === invitee.user_id + '_accept'"
                        :disabled="!!processingInvite"
                        @click="acceptGroupInvite(invitee.user_id)"
                      >
                        Đồng ý
                      </v-btn>
                      <v-btn
                        color="error"
                        size="small"
                        variant="flat"
                        class="text-none font-weight-bold px-2"
                        prepend-icon="mdi-close"
                        :loading="processingInvite === invitee.user_id + '_reject'"
                        :disabled="!!processingInvite"
                        @click="rejectGroupInvite(invitee.user_id)"
                      >
                        Từ chối
                      </v-btn>
                    </template>
                  </v-list-item>
                  <div v-if="!pendingInvites.length" class="text-center py-6 text-grey text-caption">
                    Không có yêu cầu tham gia nào đang chờ duyệt.
                  </div>
                </v-list>
              </div>
            </v-window-item>
          </v-window>
        </v-card-text>
        <v-card-actions class="px-6 pb-6 pt-2">
          <v-spacer />
          <v-btn color="teal" @click="membersDialog = false">Đóng</v-btn>
        </v-card-actions>
      </v-card>
    </v-dialog>

    <!-- Dialog 3: Tạo mã kích hoạt khách mới -->
    <v-dialog v-model="inviteDialog" max-width="450">
      <v-card>
        <v-card-title class="px-6 pt-6 pb-2 font-weight-bold">Tạo mã kích hoạt khách hàng</v-card-title>
        <v-card-text class="px-6 py-2">
          <v-form ref="inviteFormRef">
            <v-text-field v-model="inviteForm.name" label="Tên khách hàng gợi nhớ *" :rules="[v => !!v || 'Bắt buộc nhập']" class="mb-3" />
            <v-text-field v-model="inviteForm.phone_number" label="Số điện thoại liên kết" hint="Ví dụ: 0987654321" persistent-hint class="mb-3" />
            
            <!-- Zalo OA Selector Dropdown for Invite -->
            <v-select
              v-model="inviteForm.channel"
              :items="zaloOAChannels"
              item-title="name"
              item-value="id"
              return-object
              label="Chọn Zalo OA nhận xác thực *"
              :rules="[v => !!v || 'Vui lòng chọn Zalo OA']"
              variant="outlined"
              density="comfortable"
              hide-details
            />
          </v-form>
        </v-card-text>
        <v-card-actions class="px-6 pb-6 pt-2">
          <v-spacer />
          <v-btn variant="text" @click="inviteDialog = false">Hủy</v-btn>
          <v-btn color="teal" :loading="creatingInvite" @click="createInvite">Tạo mã liên kết</v-btn>
        </v-card-actions>
      </v-card>
    </v-dialog>

    <!-- Dialog 4: Duyệt & Gán mã khách hàng -->
    <v-dialog v-model="approveDialog" max-width="480">
      <v-card>
        <v-card-title class="px-6 pt-6 pb-2 font-weight-bold">Phê duyệt & Phân mã khách hàng</v-card-title>
        <v-card-text class="px-6 py-2">
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
        <v-card-actions class="px-6 pb-6 pt-2">
          <v-spacer />
          <v-btn variant="text" @click="approveDialog = false">Hủy</v-btn>
          <v-btn color="success" :loading="approvingCustomer" @click="approveCustomer">Xác nhận duyệt</v-btn>
        </v-card-actions>
      </v-card>
    </v-dialog>

    <!-- Dialog 4.5: Gán SĐT thủ công -->
    <v-dialog v-model="assignPhoneDialog" max-width="480">
      <v-card>
        <v-card-title class="px-6 pt-6 pb-2 font-weight-bold">Gán số điện thoại khách hàng</v-card-title>
        <v-card-text class="px-6 py-2">
          <div class="bg-grey-lighten-4 pa-3 rounded mb-4">
            <div class="text-caption text-grey">Khách hàng:</div>
            <div class="font-weight-bold text-primary">{{ assignForm.name }} ({{ assignForm.customer_code }})</div>
          </div>
          <v-form ref="assignFormRef">
            <v-text-field
              v-model="assignForm.phone_number"
              label="Số điện thoại liên kết (CQA) *"
              hint="Ví dụ: 0987654321"
              persistent-hint
              :rules="[v => !!v || 'Vui lòng nhập số điện thoại', v => /^[0-9]{9,11}$/.test(v) || 'Số điện thoại không hợp lệ']"
              variant="outlined"
              density="comfortable"
            />
          </v-form>
        </v-card-text>
        <v-card-actions class="px-6 pb-6 pt-2">
          <v-spacer />
          <v-btn variant="text" @click="assignPhoneDialog = false">Hủy</v-btn>
          <v-btn color="teal" :loading="assigningPhone" @click="assignCustomerPhone">Xác nhận</v-btn>
        </v-card-actions>
      </v-card>
    </v-dialog>


    <!-- Dialog 5: QR Code hiển thị mã verify cho khách hàng -->
    <v-dialog v-model="qrDialog" max-width="460">
      <v-card class="text-center">
        <v-card-title class="px-6 pt-6 pb-2 font-weight-bold text-h6 justify-center">QR kích hoạt khách hàng</v-card-title>
        <v-card-text class="px-6 py-2">
          <div class="text-body-1 font-weight-bold mb-1 text-teal">{{ activeInvite?.name }}</div>
          <div class="text-caption text-grey-darken-1 mb-4">
            Khách hàng quét mã QR dưới đây bằng app Zalo để nhắn tin cú pháp kích hoạt.
          </div>

          <!-- Zalo OA Selector Dropdown -->
          <v-select
            v-model="selectedQrOA"
            :items="zaloOAChannels"
            item-title="name"
            item-value="id"
            return-object
            label="Chọn Zalo OA nhận xác thực *"
            density="compact"
            variant="outlined"
            class="mb-4 text-left"
            hide-details
          />

          <!-- QR code container -->
          <div class="d-flex justify-center mb-4">
            <v-card variant="outlined" class="pa-2" style="border-color: #008fe5 !important;">
              <v-img
                v-if="selectedQrOA"
                :src="`https://api.qrserver.com/v1/create-qr-code/?size=200x200&data=${encodeURIComponent('https://zalo.me/' + selectedQrOA.external_id)}`"
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
        <v-card-actions class="px-6 pb-6 pt-2 justify-center">
          <v-btn color="teal" variant="elevated" class="px-6" @click="qrDialog = false; fetchCustomers()">Hoàn tất</v-btn>
        </v-card-actions>
      </v-card>
    </v-dialog>

    <!-- Dialog 6: QR Code hiển thị link nhóm GMF -->
    <v-dialog v-model="groupQrDialog" max-width="450">
      <v-card class="text-center">
        <v-card-title class="px-6 pt-6 pb-2 font-weight-bold text-h6 justify-center">QR Nhóm Chat Zalo</v-card-title>
        <v-card-text class="px-6 py-2">
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
        <v-card-actions class="px-6 pb-6 pt-2 justify-center">
          <v-btn color="success" variant="elevated" class="px-6" @click="groupQrDialog = false">Đóng</v-btn>
        </v-card-actions>
      </v-card>
    </v-dialog>

    <!-- Dialog 7: Phân quyền Endpoint & Sơ đồ tương tác dữ liệu live -->
    <v-dialog v-model="permsDialog" max-width="960">
      <v-card>
        <v-card-title class="px-6 pt-6 pb-2 font-weight-bold d-flex align-center">
          <v-icon start color="indigo">mdi-shield-lock-outline</v-icon>
          {{ $t('crm_group_perms_title') }} <span class="text-indigo ml-1">{{ activeGroupForPerms?.name }}</span>
        </v-card-title>

        <v-card-text class="px-6 py-2">
          <v-row class="align-stretch">
            <!-- Left: Table of endpoints -->
            <v-col cols="12" md="6" class="d-flex flex-column">
              <div class="text-subtitle-2 mb-2 font-weight-bold">{{ $t('erp_endpoints_title') }}</div>
              <v-card variant="outlined" class="rounded-lg scopes-card pa-1 flex-grow-1 d-flex flex-column justify-space-between">
                <div>
                  <v-table density="compact">
                    <thead>
                      <tr>
                        <th style="width:36%">{{ $t('erp_endpoint_resource') }}</th>
                        <th style="width:14%" class="text-center">{{ $t('erp_endpoint_enabled') }}</th>
                        <th style="width:26%">{{ $t('erp_endpoint_scope') }}</th>
                        <th style="width:24%">{{ $t('erp_endpoint_groups') }}</th>
                      </tr>
                    </thead>
                    <tbody>
                      <tr v-for="ep in groupEndpoints" :key="ep.resource">
                        <td class="text-caption font-weight-bold">{{ resourceLabel(ep.resource) }}</td>
                        <td class="text-center">
                          <v-switch
                            v-model="ep.is_enabled"
                            color="primary"
                            density="compact"
                            hide-details
                            :disabled="!isOwnerOrAdmin"
                          />
                        </td>
                        <td>
                          <v-select
                            v-model="ep.scope_type"
                            :items="scopeOptions"
                            density="compact"
                            variant="plain"
                            hide-details
                            disabled
                            style="font-size:0.75rem"
                          />
                        </td>
                        <td>
                          <v-select
                            v-if="ep.resource === 'products' || ep.resource === 'inventory'"
                            v-model="ep.product_groups_arr"
                            :items="listTenNhomVthh"
                            multiple
                            density="compact"
                            variant="plain"
                            hide-details
                            :disabled="!ep.is_enabled || !isOwnerOrAdmin"
                            placeholder="Chọn nhóm..."
                            style="font-size:0.75rem; min-width: 90px;"
                          >
                            <template #selection="{ index }">
                              <span
                                v-if="index === 0"
                                class="text-truncate"
                                :title="ep.product_groups_arr.join(', ')"
                                style="max-width: 100%; display: inline-block;"
                              >
                                <v-chip
                                  size="x-small"
                                  color="indigo-lighten-1"
                                  variant="flat"
                                  class="ma-0 font-weight-bold"
                                >
                                  {{ ep.product_groups_arr.length > 1 ? `(${ep.product_groups_arr.length})` : ep.product_groups_arr[0] }}
                                </v-chip>
                              </span>
                            </template>
                          </v-select>
                          <span v-else class="text-caption text-grey">—</span>
                        </td>
                      </tr>
                    </tbody>
                  </v-table>
                </div>
                <div class="pa-3 d-flex align-center ga-2">
                  <v-btn v-if="isOwnerOrAdmin" size="small" color="primary" variant="tonal" :loading="savingGroupEndpoints" @click="saveGroupEndpoints">
                    <v-icon start size="small">mdi-content-save</v-icon>
                    {{ $t('erp_save_endpoints') }}
                  </v-btn>
                  <v-progress-circular v-if="loadingGroupEndpoints" indeterminate size="16" width="2" color="primary" />
                </div>
              </v-card>
            </v-col>

            <!-- Right: Sơ đồ tương tác dữ liệu Live -->
            <v-col cols="12" md="6" class="d-flex flex-column">
              <div class="text-subtitle-2 mb-2 font-weight-bold">{{ $t('crm_live_flow_title') }}</div>
              <div class="erp-visual-graph pa-6 rounded-lg d-flex flex-column justify-center align-center border flex-grow-1" style="min-height: 250px;">
                <div class="d-flex flex-row justify-space-between align-center w-100 my-auto">
                  <!-- Node Left: CRM Group -->
                  <div class="graph-node graph-node-card pa-3 text-center rounded-xl elevation-2 border-2" :style="{ width: '130px', borderColor: '#3f51b5' }">
                    <v-icon color="primary" size="small" class="mb-1">mdi-account-group</v-icon>
                    <div class="text-caption font-weight-bold text-truncate" style="max-width: 110px;">{{ activeGroupForPerms?.name }}</div>
                    <div class="text-grey text-caption" style="font-size: 0.6rem !important">{{ $t('crm_groups') }}</div>
                  </div>

                  <!-- Path Left-to-Center -->
                  <div class="graph-arrow flex-grow-1 mx-2 position-relative text-center d-flex align-center justify-center">
                    <div class="arrow-line animated-flow"></div>
                    <v-icon class="arrow-tip" color="indigo" size="20">mdi-chevron-right</v-icon>
                  </div>

                  <!-- Node Center: CQA Gateway Checks -->
                  <div class="graph-node graph-node-card pa-3 text-center rounded-xl elevation-2 border-2" :style="{ width: '160px', borderColor: '#4caf50' }">
                    <v-icon color="success" size="small" class="mb-1">mdi-shield-check-outline</v-icon>
                    <div class="text-caption font-weight-bold" style="font-size: 0.7rem !important">Gateway (Group Scopes)</div>
                    <div class="mt-1 d-flex justify-center ga-1 flex-wrap">
                      <v-chip size="x-small" :color="isGroupEndpointEnabled('products') ? 'success' : 'grey-lighten-2'">Prod</v-chip>
                      <v-chip size="x-small" :color="isGroupEndpointEnabled('inventory') ? 'success' : 'grey-lighten-2'">Stock</v-chip>
                      <v-chip size="x-small" :color="isGroupEndpointEnabled('orders') ? 'success' : 'grey-lighten-2'">Ord</v-chip>
                      <v-chip size="x-small" :color="isGroupEndpointEnabled('customers') ? 'success' : 'grey-lighten-2'">Cust</v-chip>
                      <v-chip size="x-small" :color="isGroupEndpointEnabled('debt') ? 'success' : 'grey-lighten-2'">Debt</v-chip>
                    </div>
                  </div>

                  <!-- Path Center-to-Right -->
                  <div class="graph-arrow flex-grow-1 mx-2 position-relative text-center d-flex align-center justify-center">
                    <div class="arrow-line" :class="{ 'animated-flow-success': hasAnyGroupEndpointEnabled() }"></div>
                    <v-icon class="arrow-tip" :color="hasAnyGroupEndpointEnabled() ? 'success' : 'grey'" size="20">mdi-chevron-right</v-icon>
                  </div>

                  <!-- Node Right: Cloudify ERP -->
                  <div class="graph-node graph-node-card pa-3 text-center rounded-xl elevation-2 border-2" :style="{ width: '120px', borderColor: hasAnyGroupEndpointEnabled() ? '#ff9800' : '#9e9e9e' }">
                    <v-icon :color="hasAnyGroupEndpointEnabled() ? 'warning' : 'grey'" size="small" class="mb-1">mdi-server-network</v-icon>
                    <div class="text-caption font-weight-bold" :class="{ 'text-grey': !hasAnyGroupEndpointEnabled() }">Cloudify ERP</div>
                    <div class="text-grey text-caption text-truncate" style="font-size: 0.6rem !important; max-width: 100px;" v-if="isGroupEndpointEnabled('products') && getGroupProductGroups('products')" :title="`Sản phẩm: ${getGroupProductGroups('products')}`">
                      Lọc SP: {{ getGroupProductGroups('products') }}
                    </div>
                    <div class="text-grey text-caption text-truncate" style="font-size: 0.6rem !important; max-width: 100px;" v-if="isGroupEndpointEnabled('inventory') && getGroupProductGroups('inventory')" :title="`Tồn kho: ${getGroupProductGroups('inventory')}`">
                      Lọc Kho: {{ getGroupProductGroups('inventory') }}
                    </div>
                  </div>
                </div>
              </div>
            </v-col>
          </v-row>
        </v-card-text>

        <v-card-actions class="px-6 pb-6 pt-2">
          <v-spacer />
          <v-btn color="indigo" variant="elevated" @click="permsDialog = false">{{ $t('mcp_close') }}</v-btn>
        </v-card-actions>
      </v-card>
    </v-dialog>

    <v-snackbar v-model="snack" :color="snackColor" timeout="3000">{{ snackText }}</v-snackbar>

    <ProductGroupRequiredModal
      v-model="groupFilterModal"
      :resources="missingGroupResources"
    />
  </div>
</template>

<script setup lang="ts">
import { ref, computed, watch, onMounted, onUnmounted } from 'vue'
import { useRoute } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { useUserStore } from '../stores/users'
import { useChannelStore } from '../stores/channels'
import { useAuthStore } from '../stores/auth'
import api from '../api'
import ProductGroupRequiredModal from '../components/erp-permissions/ProductGroupRequiredModal.vue'
import { findEndpointsMissingGroups } from '../utils/erp-permission-validation'

const route = useRoute()
const { t } = useI18n()
const userStore = useUserStore()
const authStore = useAuthStore()
const channelStore = useChannelStore()
const tenantId = computed(() => route.params.tenantId as string)

const isOwnerOrAdmin = computed(() => {
  return authStore.tenantPerms?.role === 'owner' || authStore.tenantPerms?.role === 'admin'
})

const currentTab = ref('groups')
const memberTab = ref('employees')

// CRM Groups State
const groups = ref<any[]>([])
const loadingGroups = ref(false)
const groupDialog = ref(false)
const savingGroup = ref(false)
const isEditGroup = ref(false)
const groupForm = ref({ id: '', name: '', description: '', asset_id: '', channel_id: '', customer_code: '' })
const groupFormRef = ref<any>(null)

// GMF Packages State
const gmfPackages = ref<any[]>([])
const loadingPackages = ref(false)
const groupQrDialog = ref(false)
const activeGroupQR = ref<any>(null)

// Product Groups list
const listTenNhomVthh = ref<string[]>([])

// Group Members management (GMF)
const activeGroup = ref<any>(null)
const membersDialog = ref(false)
const selectedEmployeeToAdd = ref<any>(null)
const selectedCustomerToAdd = ref<any>(null)
const liveEmployees = ref<any[]>([])
const liveCustomers = ref<any[]>([])
const loadingLiveMembers = ref(false)
const pendingInvites = ref<any[]>([])
const loadingPendingInvites = ref(false)
const processingInvite = ref<string | null>(null)

// Zalo Customers State
const customers = ref<any[]>([])
const loadingCustomers = ref(false)
const inviteDialog = ref(false)
const creatingInvite = ref(false)
const inviteForm = ref({ name: '', phone_number: '', channel: null as any })
const inviteFormRef = ref<any>(null)

// Cloudify Customers State
const cloudifyCustomers = ref<any[]>([])
const loadingCloudifyCustomers = ref(false)
const cloudifySearch = ref('')
const cloudifyPage = ref(1)
const cloudifyPerPage = ref(10)
const assignPhoneDialog = ref(false)
const assigningPhone = ref(false)
const assignForm = ref({ customer_code: '', name: '', phone_number: '' })
const assignFormRef = ref<any>(null)


// QR Dialog
const activeInvite = ref<any>(null)
const qrDialog = ref(false)
const selectedQrOA = ref<any>(null)

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

const zaloOAChannels = computed(() => {
  return channelStore.channels.filter((c: any) => c.channel_type === 'zalo_oa' && c.is_active)
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
const whitelistStaff = ref<any[]>([])
async function fetchWhitelistStaff(channelId?: string) {
  try {
    const url = channelId 
      ? `/tenants/${tenantId.value}/zalo-whitelist?channel_id=${channelId}`
      : `/tenants/${tenantId.value}/zalo-whitelist`
    const { data } = await api.get(url)
    whitelistStaff.value = data || []
  } catch (err) {
    console.error('Failed to fetch Zalo whitelist', err)
  }
}

const availableEmployees = computed(() => {
  if (!activeGroup.value || !whitelistStaff.value) return []
  const existingIds = new Set(activeGroup.value.employees?.map((e: any) => e.id) || [])
  return whitelistStaff.value.filter((s: any) => s.status === 'active' && !existingIds.has(s.id))
})

const availableCustomers = computed(() => {
  if (!activeGroup.value || !customers.value) return []
  const existingZaloUserIds = new Set(liveCustomers.value.map(c => c.zalo_user_id).filter(id => !!id))
  return approvedCustomers.value.filter(c => 
    !existingZaloUserIds.has(c.zalo_user_id) && 
    c.customer_code === activeGroup.value.customer_code
  )
})

const filteredCloudifyCustomers = computed(() => {
  if (!cloudifySearch.value) return cloudifyCustomers.value
  const query = cloudifySearch.value.toLowerCase()
  return cloudifyCustomers.value.filter(c => 
    c.customer_code.toLowerCase().includes(query) ||
    c.name.toLowerCase().includes(query) ||
    (c.address && c.address.toLowerCase().includes(query)) ||
    (c.region && c.region.toLowerCase().includes(query)) ||
    (c.cloudify_phone && c.cloudify_phone.includes(query)) ||
    (c.cqa_phone && c.cqa_phone.includes(query))
  )
})

const cloudifyPageCount = computed(() => {
  return Math.ceil(filteredCloudifyCustomers.value.length / cloudifyPerPage.value)
})

const paginatedCloudifyCustomers = computed(() => {
  const start = (cloudifyPage.value - 1) * cloudifyPerPage.value
  const end = start + cloudifyPerPage.value
  return filteredCloudifyCustomers.value.slice(start, end)
})

watch(cloudifySearch, () => {
  cloudifyPage.value = 1
})

onMounted(async () => {
  window.addEventListener('crm-data-updated', fetchCustomers)
  await userStore.fetchUsers(tenantId.value)
  await channelStore.fetchChannels(tenantId.value)
  await fetchWhitelistStaff()
  await fetchGroups()
  await fetchCustomers()
  await fetchCustomerCodes()
  await fetchCloudifyCustomers()
  await fetchListTenNhomVthh()
  if (zaloOAChannels.value.length > 0) {
    await fetchGmfPackages(zaloOAChannels.value[0].id)
  } else {
    await fetchGmfPackages()
  }
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

// Load Cloudify Customers from Backend
async function fetchCloudifyCustomers() {
  loadingCloudifyCustomers.value = true
  cloudifyPage.value = 1
  try {
    const { data } = await api.get(`/tenants/${tenantId.value}/crm/cloudify-customers`)
    cloudifyCustomers.value = data || []
  } catch (err) {
    showSnack('Không thể tải danh sách khách hàng Cloudify', 'error')
  } finally {
    loadingCloudifyCustomers.value = false
  }
}

function openAssignPhoneDialog(c: any) {
  assignForm.value = {
    customer_code: c.customer_code,
    name: c.name,
    phone_number: c.cqa_phone || c.cloudify_phone || ''
  }
  assignPhoneDialog.value = true
}

async function assignCustomerPhone() {
  if (!assignFormRef.value) return
  const { valid } = await assignFormRef.value.validate()
  if (!valid) return

  assigningPhone.value = true
  try {
    await api.post(`/tenants/${tenantId.value}/crm/cloudify-customers/assign-phone`, assignForm.value)
    showSnack('Gán số điện thoại khách hàng thành công', 'success')
    assignPhoneDialog.value = false
    await fetchCloudifyCustomers()
    await fetchCustomers()
  } catch (err: any) {
    showSnack(err.response?.data?.error || 'Lỗi khi gán số điện thoại', 'error')
  } finally {
    assigningPhone.value = false
  }
}

function openInviteDialog() {
  inviteForm.value = {
    name: '',
    phone_number: '',
    channel: activeZaloOA.value || null
  }
  inviteDialog.value = true
}

function openQrDialog(inviteData: any, selectedChannel?: any) {
  activeInvite.value = inviteData
  if (selectedChannel) {
    selectedQrOA.value = selectedChannel
  } else {
    selectedQrOA.value = activeZaloOA.value || null
  }
  qrDialog.value = true
}


function showCloudifyQR(c: any) {
  openQrDialog({
    name: c.name,
    verify_token: c.verify_token
  })
}


async function fetchGmfPackages(channelId?: string) {
  loadingPackages.value = true
  try {
    const url = channelId 
      ? `/tenants/${tenantId.value}/crm/gmf-packages?channel_id=${channelId}`
      : `/tenants/${tenantId.value}/crm/gmf-packages`
    const { data } = await api.get(url)
    gmfPackages.value = (data || []).map((pkg: any) => ({
      ...pkg,
      displayName: `${pkg.asset_type} (${pkg.used_group}/${pkg.total_group} nhóm)`
    }))
    // Auto select first package with available quota if not set yet
    if (!isEditGroup.value && !groupForm.value.asset_id && data && data.length > 0) {
      const available = data.find((pkg: any) => pkg.used_group < pkg.total_group)
      if (available) {
        groupForm.value.asset_id = available.asset_id
      }
    }
  } catch (err) {
    console.error('Failed to fetch GMF packages', err)
    gmfPackages.value = []
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
  const firstOA = zaloOAChannels.value[0]
  const defaultChannelId = firstOA ? firstOA.id : ''
  groupForm.value = { id: '', name: '', description: '', asset_id: '', channel_id: defaultChannelId, customer_code: '' }
  if (defaultChannelId) {
    fetchGmfPackages(defaultChannelId)
  } else {
    gmfPackages.value = []
  }
  groupDialog.value = true
}

function onChannelSelected(channelId: string) {
  groupForm.value.asset_id = '' // Reset asset_id
  if (channelId) {
    fetchGmfPackages(channelId)
  } else {
    gmfPackages.value = []
  }
}

function openEditGroupDialog(g: any) {
  isEditGroup.value = true
  groupForm.value = { id: g.id, name: g.name, description: g.description, asset_id: g.zalo_asset_id || '', channel_id: g.channel_id || '', customer_code: g.customer_code || '' }
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
        description: groupForm.value.description,
        customer_code: groupForm.value.customer_code
      })
      showSnack('Đã cập nhật nhóm thành công', 'success')
    } else {
      await api.post(`/tenants/${tenantId.value}/crm/groups`, {
        name: groupForm.value.name,
        description: groupForm.value.description,
        asset_id: groupForm.value.asset_id,
        channel_id: groupForm.value.channel_id,
        customer_code: groupForm.value.customer_code
      })
      showSnack('Đã tạo nhóm mới thành công', 'success')
    }
    groupDialog.value = false
    await fetchGroups()
  } catch (err: any) {
    showSnack(err.response?.data?.details || err.response?.data?.error || 'Có lỗi xảy ra', 'error')
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
async function fetchLiveMembers(groupId: string) {
  loadingLiveMembers.value = true
  try {
    const { data } = await api.get(`/tenants/${tenantId.value}/crm/groups/${groupId}/members`)
    liveEmployees.value = data.employees || []
    liveCustomers.value = data.customers || []
  } catch (err: any) {
    const errMsg = err.response?.data?.details || err.response?.data?.error || 'Không thể tải danh sách thành viên live từ Zalo'
    showSnack(errMsg, 'error')
    liveEmployees.value = []
    liveCustomers.value = []
  } finally {
    loadingLiveMembers.value = false
  }
}

function openManageMembersDialog(g: any) {
  activeGroup.value = g
  selectedEmployeeToAdd.value = null
  selectedCustomerToAdd.value = null
  memberTab.value = 'employees'
  liveEmployees.value = []
  liveCustomers.value = []
  pendingInvites.value = []
  membersDialog.value = true
  fetchWhitelistStaff(g.channel_id)
  fetchLiveMembers(g.id)
  fetchPendingInvites(g.id)
}

async function fetchPendingInvites(groupId: string) {
  loadingPendingInvites.value = true
  try {
    const { data } = await api.get(`/tenants/${tenantId.value}/crm/groups/${groupId}/pending-invites`)
    pendingInvites.value = data || []
  } catch (err: any) {
    const errMsg = err.response?.data?.details || err.response?.data?.error || 'Không thể tải danh sách chờ duyệt từ Zalo'
    showSnack(errMsg, 'error')
    pendingInvites.value = []
  } finally {
    loadingPendingInvites.value = false
  }
}

async function acceptGroupInvite(zaloUserID: string) {
  if (!activeGroup.value) return
  processingInvite.value = zaloUserID + '_accept'
  try {
    await api.post(`/tenants/${tenantId.value}/crm/groups/${activeGroup.value.id}/accept-invite`, {
      member_user_ids: [zaloUserID]
    })
    showSnack('Đã đồng ý yêu cầu tham gia', 'success')
    await fetchPendingInvites(activeGroup.value.id)
    await fetchLiveMembers(activeGroup.value.id)
    await fetchGroups()
  } catch (err: any) {
    showSnack(err.response?.data?.details || 'Lỗi khi phê duyệt yêu cầu', 'error')
  } finally {
    processingInvite.value = null
  }
}

async function rejectGroupInvite(zaloUserID: string) {
  if (!activeGroup.value) return
  processingInvite.value = zaloUserID + '_reject'
  try {
    await api.post(`/tenants/${tenantId.value}/crm/groups/${activeGroup.value.id}/reject-invite`, {
      member_user_ids: [zaloUserID]
    })
    showSnack('Đã từ chối yêu cầu tham gia', 'success')
    await fetchPendingInvites(activeGroup.value.id)
  } catch (err: any) {
    showSnack(err.response?.data?.details || 'Lỗi khi từ chối yêu cầu', 'error')
  } finally {
    processingInvite.value = null
  }
}

async function addGroupEmployee() {
  if (!selectedEmployeeToAdd.value || !activeGroup.value) return
  try {
    await api.post(`/tenants/${tenantId.value}/crm/groups/${activeGroup.value.id}/members`, {
      employee_ids: [selectedEmployeeToAdd.value.id]
    })
    
    // Update local state
    if (!activeGroup.value.employees) activeGroup.value.employees = []
    activeGroup.value.employees.push(selectedEmployeeToAdd.value)
    selectedEmployeeToAdd.value = null
    showSnack('Đã thêm nhân viên vào nhóm', 'success')
    await fetchWhitelistStaff(activeGroup.value.channel_id)
    await fetchGroups() // reload values
    await fetchLiveMembers(activeGroup.value.id)
  } catch (err) {
    showSnack('Lỗi thêm nhân viên', 'error')
  }
}

async function removeGroupEmployee(empID: string) {
  try {
    await api.delete(`/tenants/${tenantId.value}/crm/groups/${activeGroup.value.id}/members`, {
      data: { employee_ids: [empID] }
    })
    activeGroup.value.employees = activeGroup.value.employees.filter((e: any) => e.id !== empID)
    showSnack('Đã gỡ nhân viên khỏi nhóm', 'success')
    await fetchWhitelistStaff(activeGroup.value.channel_id)
    await fetchGroups()
    await fetchLiveMembers(activeGroup.value.id)
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
    await fetchLiveMembers(activeGroup.value.id)
  } catch (err) {
    showSnack('Lỗi thêm khách hàng', 'error')
  }
}

async function removeGroupCustomer(custID: string, zaloUserID?: string) {
  try {
    const payload: any = {}
    if (custID) {
      payload.customer_ids = [custID]
    } else if (zaloUserID) {
      payload.zalo_user_ids = [zaloUserID]
    } else {
      return
    }
    await api.delete(`/tenants/${tenantId.value}/crm/groups/${activeGroup.value.id}/members`, {
      data: payload
    })
    if (custID) {
      activeGroup.value.customers = activeGroup.value.customers.filter((c: any) => c.id !== custID)
    }
    showSnack('Đã gỡ khách hàng khỏi nhóm', 'success')
    await fetchGroups()
    await fetchLiveMembers(activeGroup.value.id)
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
    inviteDialog.value = false
    openQrDialog(data, inviteForm.value.channel)
    await fetchCustomers()
  } catch (err) {
    showSnack('Lỗi tạo mã xác thực', 'error')
  } finally {
    creatingInvite.value = false
  }
}

function showPendingQR(c: any) {
  openQrDialog(c)
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

// CRM Group ERP endpoint permissions management
const permsDialog = ref(false)
const activeGroupForPerms = ref<any>(null)
const groupEndpoints = ref<any[]>([])
const loadingGroupEndpoints = ref(false)
const savingGroupEndpoints = ref(false)
const groupFilterModal = ref(false)
const missingGroupResources = ref<string[]>([])

const scopeOptions = [
  { title: 'Tất cả', value: 'all' },
  { title: 'Của họ (OWN)', value: 'own' },
  { title: 'Phân công (ASSIGNED)', value: 'assigned' },
]

const resourceLabels: Record<string, string> = {
  products: '🛍 Sản phẩm',
  inventory: '📦 Tồn kho',
  orders: '📋 Đơn hàng',
  customers: '👥 Khách hàng',
  debt: '💰 Công nợ',
}

function resourceLabel(r: string): string {
  return resourceLabels[r] || r
}

async function openGroupPermissionsDialog(g: any) {
  activeGroupForPerms.value = g
  permsDialog.value = true
  await loadGroupEndpoints(g.id)
}

async function fetchListTenNhomVthh() {
  try {
    const { data } = await api.get(`/tenants/${tenantId.value}/crm/list-ten-nhom-vthh`)
    listTenNhomVthh.value = data || []
  } catch (err: any) {
    const errMsg = err.response?.data?.message || 'Không thể tải danh sách nhóm sản phẩm'
    showSnack(errMsg, 'error')
  }
}

async function loadGroupEndpoints(groupId: string) {
  loadingGroupEndpoints.value = true
  try {
    const { data } = await api.get(`/tenants/${tenantId.value}/crm/groups/${groupId}/erp/endpoints`)
    groupEndpoints.value = (data.endpoints || []).map((ep: any) => {
      const groupsArr = ep.product_groups 
        ? ep.product_groups.split(',').map((g: string) => g.trim()).filter((g: string) => g)
        : []
      return {
        ...ep,
        scope_type: 'own', // Enforce 'own' scope for GMF groups
        product_groups_arr: groupsArr
      }
    })
  } catch (err) {
    showSnack(t('crm_load_perms_error'), 'error')
  } finally {
    loadingGroupEndpoints.value = false
  }
}

async function saveGroupEndpoints() {
  // Block save when an enabled product/inventory endpoint has no VTHH filter.
  const missing = findEndpointsMissingGroups(groupEndpoints.value)
  if (missing.length > 0) {
    missingGroupResources.value = missing
    groupFilterModal.value = true
    return
  }

  savingGroupEndpoints.value = true
  try {
    const payload = groupEndpoints.value.map((ep: any) => {
      const epCopy = { ...ep }
      if (epCopy.product_groups_arr) {
        epCopy.product_groups = epCopy.product_groups_arr.join(',')
        delete epCopy.product_groups_arr
      }
      return epCopy
    })
    await api.put(`/tenants/${tenantId.value}/crm/groups/${activeGroupForPerms.value.id}/erp/endpoints`, {
      endpoints: payload
    })
    showSnack(t('crm_save_perms_success'), 'success')
    // Reload endpoints from database to sync frontend state and Live Diagram visual state
    await loadGroupEndpoints(activeGroupForPerms.value.id)
  } catch (err: any) {
    showSnack(err.response?.data?.error || t('error'), 'error')
  } finally {
    savingGroupEndpoints.value = false
  }
}

function isGroupEndpointEnabled(resource: string): boolean {
  const ep = groupEndpoints.value.find(e => e.resource === resource)
  return ep ? ep.is_enabled : false
}

function hasAnyGroupEndpointEnabled(): boolean {
  return groupEndpoints.value.some(e => e.is_enabled)
}

function getGroupProductGroups(resource: string): string {
  const ep = groupEndpoints.value.find(e => e.resource === resource)
  if (!ep) return ''
  if (ep.product_groups_arr) {
    return ep.product_groups_arr.join(', ')
  }
  return ep.product_groups || ''
}
</script>

<style scoped>
.v-table {
  border-radius: 4px;
}
.v-list-item {
  border-bottom: 1px solid rgba(0, 0, 0, 0.05);
}

.scopes-card {
  background-color: #fafafa;
  border-color: #e0e0e0 !important;
}
.v-theme--dark .scopes-card {
  background-color: #252525;
  border-color: #333333 !important;
}
.erp-visual-graph {
  background-color: #f8fafc;
  border-color: #e2e8f0 !important;
  transition: all 0.3s ease;
}
.v-theme--dark .erp-visual-graph {
  background-color: #1a1a1a;
  border-color: #333333 !important;
}
.graph-node {
  transition: all 0.3s ease;
  z-index: 2;
}
.graph-node:hover {
  transform: translateY(-2px);
}
.graph-node-card {
  background-color: #ffffff;
}
.v-theme--dark .graph-node-card {
  background-color: #242424;
}
.graph-arrow {
  position: relative;
  height: 3px;
}
.arrow-line {
  height: 3px;
  background: #cbd5e1;
  width: 100%;
  position: absolute;
  top: 50%;
  left: 0;
  transform: translateY(-50%);
}
.v-theme--dark .arrow-line {
  background: #334155;
}
.animated-flow {
  background: linear-gradient(90deg, #3f51b5, #cbd5e1);
  background-size: 200% 100%;
  animation: dataflow 1.5s linear infinite;
}
.v-theme--dark .animated-flow {
  background: linear-gradient(90deg, #334155 0%, #818cf8 40%, #ffffff 50%, #818cf8 60%, #334155 100%);
}
.animated-flow-success {
  background: linear-gradient(90deg, #4caf50, #cbd5e1);
  background-size: 200% 100%;
  animation: dataflow 1.5s linear infinite;
}
.v-theme--dark .animated-flow-success {
  background: linear-gradient(90deg, #334155 0%, #4caf50 40%, #a7f3d0 50%, #4caf50 60%, #334155 100%);
}
.arrow-tip {
  position: absolute;
  right: -6px;
  top: 50%;
  transform: translateY(-50%);
  margin-top: -1px;
  z-index: 1;
  display: flex !important;
  align-items: center;
  justify-content: center;
}
.graph-arrow-label {
  background-color: #ffffff;
}
.v-theme--dark .graph-arrow-label {
  background-color: #1a1a1a;
}
@keyframes dataflow {
  0% { background-position: 100% 0; }
  100% { background-position: -100% 0; }
}
</style>
