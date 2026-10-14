<template>
  <div v-if="channel">
    <!-- Header -->
    <div class="d-flex align-center mb-4 flex-wrap ga-2">
      <v-btn icon="mdi-arrow-left" variant="text" size="small" @click="router.back()" class="mr-2" />
      <h1 class="text-h5 font-weight-bold">{{ channel.name }}</h1>
      <v-spacer />
      <template v-if="authStore.canEdit('channels')">
        <v-btn variant="outlined" prepend-icon="mdi-pencil" size="small" @click="editDialog = true">{{ $t('edit') }}</v-btn>
        <v-btn v-if="channel.channel_type !== 'personal_zalo_import'" color="primary" prepend-icon="mdi-sync" size="small" :loading="syncing" :disabled="!channel.is_active" @click="doSync">
          {{ $t('sync_now') }}
        </v-btn>
        <v-btn v-if="channel.channel_type !== 'personal_zalo_import'" variant="outlined" prepend-icon="mdi-connection" size="small" :loading="testing" :disabled="!channel.is_active" @click="doTest">
          {{ $t('test_connection') }}
        </v-btn>
        <v-btn color="warning" variant="outlined" prepend-icon="mdi-delete-sweep" size="small" @click="confirmPurge = true">
          {{ $t('purge_conversations') }}
        </v-btn>
        <v-btn color="error" variant="outlined" prepend-icon="mdi-delete" size="small" @click="confirmDelete = true">{{ $t('delete') }}</v-btn>
      </template>
    </div>

    <!-- Alert if inactive -->
    <v-alert v-if="!channel.is_active" type="warning" variant="tonal" class="mb-4">
      {{ $t('channel_inactive_error') }}
    </v-alert>

    <!-- Channel Info -->
    <v-card class="pa-4 mb-4">
      <div class="text-subtitle-1 font-weight-bold mb-3">
        <v-icon start size="small">mdi-information</v-icon>
        {{ $t('channel_info') }}
      </div>
      <v-row>
        <v-col cols="6" sm="3">
          <div class="text-caption text-grey">{{ $t('channel_type') }}</div>
          <v-chip size="small" :color="channelTypeColor(channel.channel_type)" variant="tonal">
            {{ channelTypeLabel(channel.channel_type) }}
          </v-chip>
        </v-col>
        <v-col cols="6" sm="3">
          <div class="text-caption text-grey">{{ $t('status') }}</div>
          <v-chip size="small" :color="channel.is_active ? 'success' : 'grey'" variant="tonal">
            {{ channel.is_active ? $t('active') : $t('inactive') }}
          </v-chip>
        </v-col>
        <v-col cols="6" sm="3">
          <div class="text-caption text-grey">{{ $t('sync_status') }}</div>
          <v-chip size="small" :color="channel.last_sync_status === 'success' ? 'success' : channel.last_sync_status === 'error' ? 'error' : 'grey'" variant="tonal">
            {{ channel.last_sync_status === 'success' ? $t('sync_success') : channel.last_sync_status === 'error' ? $t('sync_error') : $t('sync_never') }}
          </v-chip>
        </v-col>
        <v-col cols="6" sm="3">
          <div class="text-caption text-grey">{{ $t('last_sync') }}</div>
          <div>{{ channel.last_sync_at ? formatDateTime(channel.last_sync_at) : $t('sync_never') }}</div>
        </v-col>
        <v-col cols="6" sm="3">
          <div class="text-caption text-grey">{{ $t('total_conversations') }}</div>
          <a href="#" class="text-primary font-weight-bold" @click.prevent="goToMessages">
            {{ channel.conversation_count || 0 }}
          </a>
        </v-col>
        <template v-if="channel.channel_type !== 'personal_zalo_import'">
          <v-col cols="6" sm="3">
            <div class="text-caption text-grey">{{ $t('sync_interval') }}</div>
            <div>{{ formatSyncInterval(metadata.sync_interval) }}</div>
          </v-col>
          <v-col cols="6" sm="3">
            <div class="text-caption text-grey">{{ $t('sync_files') }}</div>
            <v-chip size="small" :color="metadata.sync_files ? 'success' : 'grey'" variant="tonal">
              {{ metadata.sync_files ? $t('enabled') : $t('disabled') }}
            </v-chip>
          </v-col>
        </template>
        <template v-else>
          <v-col cols="6" sm="3">
            <div class="text-caption text-grey">{{ $t('sync_scope') }}</div>
            <div>{{ metadata.sync_scope === 'direct' ? $t('sync_scope_direct') : metadata.sync_scope === 'group' ? $t('sync_scope_group') : $t('sync_scope_all') }}</div>
          </v-col>
        </template>
        <v-col v-if="channel.channel_type === 'zalo_oa'" cols="6" sm="3">
          <div class="text-caption text-grey">{{ $t('campaign_alerts') }}</div>
          <v-chip size="small" :color="metadata.campaign_alerts_enabled ? 'success' : 'grey'" variant="tonal">
            {{ metadata.campaign_alerts_enabled ? $t('enabled') : $t('disabled') }}
          </v-chip>
        </v-col>
        <v-col cols="6" sm="3">
          <div class="text-caption text-grey">{{ $t('created_at') }}</div>
          <div>{{ formatDateTime(channel.created_at) }}</div>
        </v-col>
      </v-row>
    </v-card>

    <v-card v-if="isPersonalZalo" class="pa-4 mb-4 personal-zalo-connection-card">
      <div class="d-flex align-center mb-3 flex-wrap ga-2">
        <div>
          <div class="text-subtitle-1 font-weight-bold">
            <v-icon start size="small">mdi-cellphone-link</v-icon>
            {{ $t('personal_zalo_connect_title') }}
          </div>
          <div class="text-body-2 text-medium-emphasis">
            {{ $t('personal_zalo_connect_subtitle') }}
          </div>
        </div>
        <v-spacer />
        <v-chip size="small" :color="gatewayStatusColor" variant="tonal">
          {{ gatewayStatusLabel }}
        </v-chip>
        <v-btn size="small" variant="text" prepend-icon="mdi-refresh" :loading="gatewayLoading" @click="loadGatewayState()">
          {{ $t('refresh') }}
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
            <div class="text-overline mb-2">{{ $t('personal_zalo_steps_title') }}</div>
            <div class="d-flex flex-column ga-3">
              <div class="d-flex ga-3 align-start">
                <v-avatar size="28" :color="gatewayState.account_exists ? 'success' : 'grey-lighten-1'" variant="tonal">1</v-avatar>
                <div>
                  <div class="font-weight-medium">{{ $t('personal_zalo_step1_title') }}</div>
                  <div class="text-body-2 text-medium-emphasis">{{ $t('personal_zalo_step1_desc') }}</div>
                </div>
              </div>
              <div class="d-flex ga-3 align-start">
                <v-avatar size="28" :color="gatewayAccount?.status === 'connected' || gatewayAccount?.status === 'qr_pending' || gatewayAccount?.status === 'connecting' ? 'info' : 'grey-lighten-1'" variant="tonal">2</v-avatar>
                <div>
                  <div class="font-weight-medium">{{ $t('personal_zalo_step2_title') }}</div>
                  <div class="text-body-2 text-medium-emphasis">{{ $t('personal_zalo_step2_desc') }}</div>
                </div>
              </div>
              <div class="d-flex ga-3 align-start">
                <v-avatar size="28" :color="gatewayAccount?.status === 'connected' && (channel?.conversation_count || 0) > 0 ? 'success' : gatewayAccount?.status === 'connected' ? 'warning' : 'grey-lighten-1'" variant="tonal">3</v-avatar>
                <div>
                  <div class="font-weight-medium">{{ $t('personal_zalo_step3_title') }}</div>
                  <div class="text-body-2 text-medium-emphasis">{{ $t('personal_zalo_step3_desc') }}</div>
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
                  {{ $t('personal_zalo_account_id') }}: {{ gatewayAccount.account_external_id || gatewayAccount.zalo_uid || $t('none') }}
                </div>
                <div class="text-caption text-medium-emphasis">
                  {{ gatewayAccount.last_imported_at ? `${$t('personal_zalo_last_sync')}: ${formatDateTime(gatewayAccount.last_imported_at)}` : $t('personal_zalo_never_synced') }}
                </div>
              </div>
            </div>

            <div class="d-flex flex-wrap ga-2">
              <v-btn
                v-if="!gatewayState.account_exists || gatewayState.next_action === 'create_account' || gatewayState.next_action === 'scan_qr'"
                color="teal"
                prepend-icon="mdi-qrcode-scan"
                :loading="gatewayActionLoading === 'connect'"
                :disabled="!channel.is_active"
                @click="startGatewayConnect"
              >
                {{ gatewayState.account_exists ? $t('personal_zalo_get_new_qr') : $t('personal_zalo_start_connect') }}
              </v-btn>
              <v-btn
                v-if="gatewayState.account_exists && gatewayState.next_action === 'reconnect'"
                color="primary"
                variant="tonal"
                prepend-icon="mdi-connection"
                :loading="gatewayActionLoading === 'reconnect'"
                :disabled="!channel.is_active"
                @click="reconnectGateway"
              >
                {{ $t('personal_zalo_reconnect') }}
              </v-btn>
              <v-btn
                v-if="gatewayAccount?.status === 'connected'"
                color="primary"
                prepend-icon="mdi-sync"
                :loading="gatewayActionLoading === 'sync'"
                :disabled="!channel.is_active"
                @click="syncGateway"
              >
                {{ $t('personal_zalo_fetch_messages') }}
              </v-btn>
              <v-btn
                v-if="channel?.conversation_count"
                variant="text"
                prepend-icon="mdi-forum"
                @click="goToMessages"
              >
                {{ $t('personal_zalo_open_chat_list') }}
              </v-btn>
            </div>

            <div v-if="gatewayAccount?.last_error" class="text-caption text-error mt-3">
              {{ $t('personal_zalo_last_error') }}: {{ gatewayAccount.last_error }}
            </div>
          </v-sheet>
        </v-col>

        <v-col cols="12" md="5">
          <v-sheet rounded="lg" border class="pa-4 h-100 d-flex flex-column justify-center">
            <template v-if="gatewayAccount?.status === 'qr_pending' && gatewayQrImageSrc">
              <div class="text-overline mb-2 text-center">{{ $t('personal_zalo_scan_qr_title') }}</div>
              <div class="d-flex justify-center mb-3">
                <v-img :src="gatewayQrImageSrc" max-width="240" max-height="240" class="rounded-lg border" cover />
              </div>
              <div class="text-body-2 text-medium-emphasis text-center">
                {{ $t('personal_zalo_scan_qr_desc') }}
              </div>
              <div class="text-caption text-medium-emphasis text-center mt-2">
                {{ gatewayAccount.qr_generated_at ? $t('personal_zalo_qr_created_at', { time: formatDateTime(gatewayAccount.qr_generated_at) }) : $t('personal_zalo_waiting_for_qr_scan') }}
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
        {{ $t('channel_tech_info_title') }}
      </div>
      <v-alert type="info" variant="tonal" class="mb-4">
        {{ $t('channel_tech_info_desc') }}
      </v-alert>
      <v-row>
        <v-col cols="12" md="8">
          <v-text-field :model-value="channel.import_endpoint || ''" :label="$t('channel_import_endpoint')" readonly density="compact" />
        </v-col>
        <v-col cols="12" md="4">
          <v-text-field :model-value="channel.import_secret || channel.import_secret_masked || ''" :label="$t('channel_import_secret')" readonly density="compact" />
        </v-col>
        <v-col cols="12">
          <v-text-field :model-value="channel.import_endpoint_internal || channel.import_endpoint || ''" :label="$t('channel_import_internal_endpoint')" readonly density="compact" :hint="$t('channel_import_internal_hint')" persistent-hint />
        </v-col>
      </v-row>
    </v-card>

    <v-card v-if="channel.channel_type === 'personal_zalo_import'" class="pa-4 mb-4">
      <div class="d-flex align-center mb-3">
        <div class="text-subtitle-1 font-weight-bold">
          <v-icon start size="small">mdi-account-switch</v-icon>
          {{ $t('channel_assign_owners_title') }}
        </div>
        <v-spacer />
        <v-btn size="small" variant="text" prepend-icon="mdi-plus" @click="addAccountOwner">
          {{ $t('channel_add_owner_row') }}
        </v-btn>
      </div>
      <v-alert type="info" variant="tonal" class="mb-4">
        {{ $t('channel_assign_owners_guide') }}
      </v-alert>
      <div v-for="(owner, index) in accountOwnerDrafts" :key="`owner-${index}`" class="mb-3">
        <v-row>
          <v-col cols="12" md="5">
            <v-text-field v-model="owner.account_external_id" :label="$t('channel_zalo_account_id')" density="compact" :hint="$t('channel_zalo_account_id_hint')" persistent-hint />
          </v-col>
          <v-col cols="12" md="6">
            <v-select v-model="owner.user_id" :items="tenantUserOptions" :label="$t('channel_assigned_user')" density="compact" />
          </v-col>
          <v-col cols="12" md="1" class="d-flex align-center justify-end">
            <v-btn icon="mdi-delete-outline" variant="text" color="error" @click="removeAccountOwner(index)" />
          </v-col>
        </v-row>
      </div>
      <div v-if="!accountOwnerDrafts.length" class="text-body-2 text-grey mb-3">
        {{ $t('channel_no_assignments') }}
      </div>
      <div class="d-flex justify-end">
        <v-btn color="primary" :loading="savingAccountOwners" @click="saveAccountOwners">
          {{ $t('channel_save_assignments') }}
        </v-btn>
      </div>
    </v-card>

    <!-- Sync result alert -->
    <v-alert v-if="syncResult" :type="syncResult.type" closable class="mb-4" @click:close="syncResult = null">
      {{ syncResult.message }}
    </v-alert>

    <!-- Sync History -->
    <v-card class="pa-4">
      <div class="d-flex align-center mb-3 flex-wrap ga-2">
        <div class="text-subtitle-1 font-weight-bold">
          <v-icon start size="small">mdi-history</v-icon>
          {{ $t('sync_history') }}
        </div>
        <v-spacer />
        <v-select
          v-model="syncStatusFilter"
          :items="syncFilterOptions"
          density="compact"
          variant="outlined"
          hide-details
          style="max-width: 180px"
          @update:model-value="onSyncFilterChange"
        />
      </div>
      <v-table density="compact" v-if="channelStore.syncHistory.length > 0">
        <thead>
          <tr>
            <th>{{ $t('time') }}</th>
            <th>{{ $t('status') }}</th>
            <th>{{ $t('details') }}</th>
            <th>{{ $t('error') }}</th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="log in channelStore.syncHistory" :key="log.id">
            <td>{{ formatDateTime(log.created_at) }}</td>
            <td>
              <v-chip size="x-small" :color="(log.action === 'sync.completed' || log.action === 'import.personal_zalo') ? 'success' : 'error'" variant="tonal">
                {{ (log.action === 'sync.completed' || log.action === 'import.personal_zalo') ? $t('success') : $t('error') }}
              </v-chip>
            </td>
            <td class="text-caption">{{ log.detail?.substring(0, 120) }}</td>
            <td class="text-caption text-error">{{ log.error_message }}</td>
          </tr>
        </tbody>
      </v-table>
      <div v-else class="text-center text-grey pa-4">{{ $t('no_sync_history') }}</div>
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
        <v-card-title>{{ $t('edit_channel') }}</v-card-title>
        <v-card-text class="pt-3">
          <v-text-field v-model="editForm.name" :label="$t('channel_name')" density="compact" class="mb-2" />
          <v-switch v-model="editForm.is_active" :label="$t('active')" density="compact" color="primary" class="mb-2" />
          <template v-if="channel.channel_type !== 'personal_zalo_import'">
            <v-select v-model="editForm.sync_interval" :items="syncIntervalOptions" :label="$t('sync_interval')" density="compact" class="mb-2" />
            <v-switch v-model="editForm.sync_files" :label="$t('sync_files')" density="compact" color="primary" />
          </template>
          <template v-if="channel.channel_type === 'zalo_oa'">
            <v-divider class="my-3" />
            <div class="text-body-2 font-weight-medium mb-1">{{ $t('campaign_alert_settings_title') }}</div>
            <v-switch
              v-model="editForm.campaign_alerts_enabled"
              :label="$t('campaign_alert_enable_switch')"
              density="compact"
              color="primary"
              class="mb-1"
            />
            <v-select
              v-if="editForm.campaign_alerts_enabled"
              v-model="editForm.campaign_alert_group_id"
              :items="alertGroupOptions"
              :loading="loadingAlertGroups"
              :no-data-text="loadingAlertGroups ? $t('loading') : $t('campaign_alert_no_groups')"
              :label="$t('campaign_alert_group_label')"
              density="compact"
              :hint="$t('campaign_alert_group_hint')"
              persistent-hint
            />
            <CampaignAlertOutputs v-if="editForm.campaign_alerts_enabled" v-model="editForm.campaign_alert_outputs" />
          </template>
          <template v-else>
            <v-select
              v-model="editForm.sync_scope"
              :items="syncScopeOptions"
              :label="$t('sync_scope')"
              density="compact"
              class="mb-2"
              :hint="$t('sync_scope_hint')"
              persistent-hint
            />
          </template>
        </v-card-text>
        <v-card-actions>
          <v-spacer />
          <v-btn @click="editDialog = false">{{ $t('cancel') }}</v-btn>
          <v-btn color="primary" :loading="saving" @click="saveEdit">{{ $t('save') }}</v-btn>
        </v-card-actions>
      </v-card>
    </v-dialog>

    <!-- Purge Conversations Confirm -->
    <v-dialog v-model="confirmPurge" max-width="440">
      <v-card>
        <v-card-title>{{ $t('purge_conversations') }}</v-card-title>
        <v-card-text>
          {{ $t('purge_conversations_confirm_msg', { name: channel.name }) }}
        </v-card-text>
        <v-card-actions>
          <v-spacer />
          <v-btn @click="confirmPurge = false">{{ $t('cancel') }}</v-btn>
          <v-btn color="warning" :loading="purging" @click="doPurge">{{ $t('purge_conversations') }}</v-btn>
        </v-card-actions>
      </v-card>
    </v-dialog>

    <!-- Delete Confirm -->
    <v-dialog v-model="confirmDelete" max-width="400">
      <v-card>
        <v-card-title>{{ $t('delete_channel') }}</v-card-title>
        <v-card-text>{{ $t('delete_channel_confirm_msg', { name: channel.name }) }}</v-card-text>
        <v-card-actions>
          <v-spacer />
          <v-btn @click="confirmDelete = false">{{ $t('cancel') }}</v-btn>
          <v-btn color="error" :loading="deleting" @click="doDelete">{{ $t('delete') }}</v-btn>
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
import { useI18n } from 'vue-i18n'
import { useChannelStore } from '../../stores/channels'
import type { PersonalZaloGatewayState } from '../../stores/channels'
import { useAuthStore } from '../../stores/auth'
import { useUserStore } from '../../stores/users'
import api from '../../api'
import CampaignAlertOutputs from '../../components/channels/CampaignAlertOutputs.vue'

const route = useRoute()
const router = useRouter()
const { t } = useI18n()
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
const syncStatusFilter = ref('')
const syncFilterOptions = computed(() => [
  { title: t('all'), value: '' },
  { title: t('filter_only_error'), value: 'error' },
  { title: t('success'), value: 'success' },
])

function onSyncFilterChange() {
  syncPage.value = 1
  channelStore.fetchSyncHistory(tenantId.value, channelId.value, 1, syncStatusFilter.value)
}
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

const editForm = ref({ name: '', is_active: true, sync_interval: 5, sync_files: false, sync_scope: 'all', campaign_alerts_enabled: false, campaign_alert_group_id: '', campaign_alert_outputs: [] as any[] })

// Alert recipient options: this channel's GMF groups that have a linked Zalo group.
const alertGroupOptions = ref<Array<{ title: string; value: string }>>([])
const loadingAlertGroups = ref(false)

async function loadAlertGroupOptions() {
  loadingAlertGroups.value = true
  try {
    const { data } = await api.get(`/tenants/${tenantId.value}/crm/groups`)
    alertGroupOptions.value = (Array.isArray(data) ? data : [])
      .filter((g: any) => g.zalo_group_id && (!g.channel_id || g.channel_id === channelId.value))
      .map((g: any) => ({ title: g.name, value: g.id }))
  } catch {
    alertGroupOptions.value = []
  } finally {
    loadingAlertGroups.value = false
  }
}

const syncIntervalOptions = computed(() => [
  { title: t('sync_1m'), value: 1 },
  { title: t('sync_5m'), value: 5 },
  { title: t('sync_10m'), value: 10 },
  { title: t('sync_15m'), value: 15 },
  { title: t('sync_30m'), value: 30 },
  { title: t('sync_1h'), value: 60 },
  { title: t('sync_6h'), value: 360 },
  { title: t('sync_1d'), value: 1440 },
])

const syncScopeOptions = computed(() => [
  { title: t('sync_scope_all'), value: 'all' },
  { title: t('sync_scope_direct'), value: 'direct' },
  { title: t('sync_scope_group'), value: 'group' },
])

function formatDateTime(d: string) {
  const dt = new Date(d)
  const dd = String(dt.getDate()).padStart(2, '0')
  const mm = String(dt.getMonth() + 1).padStart(2, '0')
  const hh = String(dt.getHours()).padStart(2, '0')
  const mi = String(dt.getMinutes()).padStart(2, '0')
  return `${dd}/${mm}/${dt.getFullYear()} ${hh}:${mi}`
}

function formatSyncInterval(mins: number) {
  if (!mins) return t('minutes_unit', { count: 5 })
  if (mins < 60) return t('minutes_unit', { count: mins })
  if (mins < 1440) return t('hours_unit', { count: mins / 60 })
  return t('days_unit', { count: mins / 1440 })
}

function channelTypeLabel(channelType: string) {
  if (channelType === 'facebook') return t('channel_facebook')
  if (channelType === 'personal_zalo_import') return t('channel_personal_zalo')
  return t('channel_zalo')
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
  if (!gatewayState.value.gateway_configured) return t('gateway_status_unconfigured')
  if (!gatewayState.value.gateway_reachable) return t('gateway_status_unreachable')
  if (!gatewayState.value.account_exists) return t('gateway_status_disconnected')
  if (gatewayAccount.value?.status === 'connected') return t('gateway_status_connected')
  if (gatewayAccount.value?.status === 'qr_pending') return t('gateway_status_qr_pending')
  if (gatewayAccount.value?.status === 'connecting') return t('gateway_status_connecting')
  return t('gateway_status_interrupted')
})
const gatewayStatusHeadline = computed(() => {
  if (!gatewayState.value.gateway_configured) return t('gateway_headline_unconfigured')
  if (!gatewayState.value.gateway_reachable) return t('gateway_headline_unreachable')
  if (!gatewayState.value.account_exists) return t('gateway_headline_ready')
  if (gatewayAccount.value?.status === 'connected') return t('gateway_headline_connected')
  if (gatewayAccount.value?.status === 'qr_pending') return t('gateway_headline_qr_pending')
  if (gatewayAccount.value?.status === 'connecting') return t('gateway_headline_reconnecting')
  return t('gateway_headline_interrupted')
})
const gatewayStatusBody = computed(() => {
  if (gatewayState.value.message) return gatewayState.value.message
  if (!gatewayState.value.account_exists) return t('gateway_body_ready')
  if (gatewayAccount.value?.status === 'connected') return t('gateway_body_connected')
  if (gatewayAccount.value?.status === 'connecting') return t('gateway_body_reconnecting')
  if (gatewayAccount.value?.status === 'qr_pending') return t('gateway_body_qr_pending')
  return gatewayAccount.value?.last_error || t('gateway_body_fallback')
})
const gatewayAlert = computed(() => {
  if (!gatewayState.value.gateway_configured) {
    return { type: 'warning' as const, message: t('gateway_alert_unconfigured') }
  }
  if (!gatewayState.value.gateway_reachable) {
    return { type: 'warning' as const, message: t('gateway_alert_unreachable') }
  }
  if (!gatewayState.value.account_exists) {
    return { type: 'info' as const, message: t('gateway_alert_ready') }
  }
  if (gatewayAccount.value?.status === 'connected') {
    return { type: 'success' as const, message: t('gateway_alert_connected') }
  }
  if (gatewayAccount.value?.status === 'qr_pending') {
    return { type: 'info' as const, message: t('gateway_alert_qr_pending') }
  }
  return { type: 'warning' as const, message: gatewayAccount.value?.last_error || t('gateway_alert_fallback') }
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
      message: err.response?.data?.details || err.response?.data?.error || t('gateway_state_fetch_failed'),
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
    syncResult.value = { type: 'success', message: gatewayState.value.account?.status === 'connected' ? t('personal_zalo_connected') : t('personal_zalo_qr_created') }
    await channelStore.fetchChannel(tenantId.value, channelId.value)
  } catch (err: any) {
    syncResult.value = { type: 'error', message: err.response?.data?.details || err.response?.data?.message || err.response?.data?.error || t('personal_zalo_connect_failed') }
  } finally {
    gatewayActionLoading.value = ''
    updateGatewayPolling()
  }
}

async function reconnectGateway() {
  gatewayActionLoading.value = 'reconnect'
  try {
    gatewayState.value = await channelStore.reconnectPersonalZaloGateway(tenantId.value, channelId.value)
    syncResult.value = { type: 'info', message: t('personal_zalo_reconnecting') }
  } catch (err: any) {
    syncResult.value = { type: 'error', message: err.response?.data?.details || err.response?.data?.message || err.response?.data?.error || t('personal_zalo_reconnect_failed') }
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
    await channelStore.fetchSyncHistory(tenantId.value, channelId.value, syncPage.value, syncStatusFilter.value)
    syncResult.value = { type: 'success', message: t('personal_zalo_sync_started') }
  } catch (err: any) {
    syncResult.value = { type: 'error', message: err.response?.data?.details || err.response?.data?.message || err.response?.data?.error || t('personal_zalo_sync_failed') }
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
      syncResult.value = { type: 'error', message: t('sync_timeout_check_later') }
      syncing.value = false
      return
    }
    await channelStore.fetchSyncHistory(tenantId.value, channelId.value, syncPage.value, syncStatusFilter.value)
    const ch = channelStore.currentChannel
    if (ch?.last_sync_status === 'success') {
      syncResult.value = { type: 'success', message: t('sync_success') }
    } else {
      syncResult.value = { type: 'error', message: ch?.last_sync_error || t('sync_failed') }
    }
  } catch (err: any) {
    syncResult.value = { type: 'error', message: err.response?.data?.error || t('sync_failed') }
  } finally {
    syncing.value = false
  }
}

async function doTest() {
  testing.value = true
  try {
    const result = await channelStore.testConnection(tenantId.value, channelId.value)
    syncResult.value = { type: 'success', message: result.message || t('connection_success') }
  } catch (err: any) {
    syncResult.value = { type: 'error', message: err.response?.data?.error || t('connection_failed') }
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
    if (channel.value?.channel_type === 'personal_zalo_import') {
      payload.metadata = JSON.stringify({ sync_scope: editForm.value.sync_scope })
    } else if (channel.value?.channel_type === 'zalo_oa') {
      payload.metadata = JSON.stringify({
        sync_interval: editForm.value.sync_interval,
        sync_files: editForm.value.sync_files,
        campaign_alerts_enabled: editForm.value.campaign_alerts_enabled,
        campaign_alert_group_id: editForm.value.campaign_alerts_enabled ? editForm.value.campaign_alert_group_id : '',
        campaign_alert_outputs: editForm.value.campaign_alerts_enabled ? editForm.value.campaign_alert_outputs : [],
      })
    } else {
      payload.metadata = JSON.stringify({ sync_interval: editForm.value.sync_interval, sync_files: editForm.value.sync_files })
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
    syncResult.value = { type: 'success', message: t('save_assignments_success') }
  } catch (err: any) {
    syncResult.value = { type: 'error', message: err.response?.data?.details || err.response?.data?.error || t('save_assignments_failed') }
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
      message: t('purge_success', { conversations: result.conversations_deleted, messages: result.messages_deleted })
    }
    // Refresh channel data to reflect reset sync state
    await channelStore.fetchChannel(tenantId.value, channelId.value)
  } catch {
    syncResult.value = { type: 'error', message: t('purge_failed') }
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
      campaign_alerts_enabled: metadata.value.campaign_alerts_enabled || false,
      campaign_alert_group_id: metadata.value.campaign_alert_group_id || '',
      campaign_alert_outputs: Array.isArray(metadata.value.campaign_alert_outputs) ? metadata.value.campaign_alert_outputs : [],
    }
    if (channel.value.channel_type === 'zalo_oa') {
      loadAlertGroupOptions()
    }
  }
})

watch(syncPage, (p) => {
  channelStore.fetchSyncHistory(tenantId.value, channelId.value, p, syncStatusFilter.value)
})

watch(() => gatewayAccount.value?.status, () => {
  updateGatewayPolling()
})

onMounted(async () => {
  // Handle OAuth callback redirect
  const params = new URLSearchParams(window.location.search)
  if (params.get('zalo_auth') === 'success' || params.get('fb_auth') === 'success') {
    syncResult.value = { type: 'success', message: t('auth_success_sync_now') }
    window.history.replaceState({}, '', window.location.pathname)
  } else if (params.get('zalo_auth') === 'error' || params.get('fb_auth') === 'error') {
    syncResult.value = { type: 'error', message: params.get('message') || t('auth_failed_try_again') }
    window.history.replaceState({}, '', window.location.pathname)
  }

  await channelStore.fetchChannel(tenantId.value, channelId.value)
  await channelStore.fetchSyncHistory(tenantId.value, channelId.value, 1, syncStatusFilter.value)
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
