<template>
  <div>
    <div class="d-flex align-center justify-space-between mb-6">
      <h1 class="text-h5 font-weight-bold">{{ $t('channels') }}</h1>
      <v-btn v-if="authStore.canEdit('channels')" color="primary" prepend-icon="mdi-plus" @click="showDialog = true">
        {{ $t('connect_channel') }}
      </v-btn>
    </div>

    <v-row>
      <v-col v-for="ch in channelStore.channels" :key="ch.id" cols="12" sm="6" md="4">
        <v-card class="pa-4" style="cursor: pointer" @click="router.push(`/${tenantId}/channels/${ch.id}`)">
          <div class="d-flex align-center mb-3">
            <v-icon :color="channelColor(ch.channel_type)" size="32" class="mr-3">
              {{ channelIcon(ch.channel_type) }}
            </v-icon>
            <div class="flex-grow-1">
              <div class="text-subtitle-1 font-weight-bold">{{ ch.name }}</div>
              <v-chip size="x-small" :color="channelColor(ch.channel_type)" variant="tonal">
                {{ channelLabel(ch.channel_type) }}
              </v-chip>
              <div v-if="ch.channel_type === 'zalo_oa' && ch.external_id" class="text-caption text-grey mt-1" style="white-space: nowrap; overflow: hidden; text-overflow: ellipsis;">
                OA: {{ ch.external_id }}
              </div>
              <div v-else-if="ch.channel_type === 'personal_zalo_import' && ch.external_id" class="text-caption text-grey mt-1" style="white-space: nowrap; overflow: hidden; text-overflow: ellipsis;">
                Account: {{ ch.external_id }}
              </div>
            </div>
            <div class="text-right">
              <div class="text-h6 font-weight-bold">{{ ch.conversation_count || 0 }}</div>
              <div class="text-caption text-grey">cuộc chat</div>
            </div>
          </div>

          <div class="d-flex align-center justify-space-between mb-2">
            <span class="text-caption text-grey">{{ $t('status') }}</span>
            <div class="d-flex align-center ga-3" @click.stop>
              <v-switch
                :model-value="ch.is_active"
                hide-details
                density="compact"
                color="success"
                :disabled="!authStore.canEdit('channels')"
                @update:model-value="toggleActive(ch)"
              />
              <span class="text-caption font-weight-medium" :class="ch.is_active ? 'text-success' : 'text-grey'">
                {{ ch.is_active ? $t('active') : $t('inactive') }}
              </span>
            </div>
          </div>

          <!-- Per-channel auto-reply (webhook channels only) — separate from "hoạt động" -->
          <div v-if="WEBHOOK_CHANNEL_TYPES.includes(ch.channel_type)" class="mb-2">
            <div class="d-flex align-center justify-space-between">
              <span class="text-caption text-grey">{{ $t('chatbot_channel_autoreply_short') }}</span>
              <div class="d-flex align-center ga-3" @click.stop>
                <v-switch
                  :model-value="ch.auto_reply_enabled && chatbotActive"
                  hide-details
                  density="compact"
                  color="success"
                  :loading="togglingAutoReplyId === ch.id"
                  :disabled="!authStore.canEdit('channels') || !ch.is_active || !chatbotActive || togglingAutoReplyId === ch.id"
                  @update:model-value="(v: any) => toggleAutoReply(ch, !!v)"
                />
                <span class="text-caption font-weight-medium" :class="(ch.auto_reply_enabled && chatbotActive) ? 'text-success' : 'text-grey'">
                  {{ (ch.auto_reply_enabled && chatbotActive) ? $t('chatbot_status_on') : $t('chatbot_status_off') }}
                </span>
              </div>
            </div>
            <div v-if="!chatbotActive" class="text-right mt-n1">
              <span class="text-caption text-grey">{{ $t('chatbot_master_off_hint') }}</span>
            </div>
          </div>

          <div class="d-flex align-center justify-space-between mb-2">
            <span class="text-caption text-grey">{{ $t('sync_status') }}</span>
            <v-chip size="x-small" :color="syncColor(ch.last_sync_status)" variant="tonal">
              {{ ch.last_sync_status || '—' }}
            </v-chip>
          </div>
          <div class="d-flex align-center justify-space-between mb-3">
            <span class="text-caption text-grey">{{ $t('last_sync') }}</span>
            <span class="text-body-2">{{ ch.last_sync_at ? new Date(ch.last_sync_at).toLocaleString() : '—' }}</span>
          </div>

          <v-divider class="mb-3" />
          <div class="d-flex ga-2 flex-wrap" @click.stop>
            <v-btn v-if="ch.channel_type !== 'personal_zalo_import'" size="small" variant="tonal" color="primary" prepend-icon="mdi-sync" :loading="syncing === ch.id" :disabled="!ch.is_active" @click="syncNow(ch.id)">
              {{ $t('sync_now') }}
            </v-btn>
            <v-btn v-if="ch.channel_type !== 'personal_zalo_import' && ch.last_sync_status === 'error'" size="small" variant="tonal" color="warning" prepend-icon="mdi-link-variant" :loading="reauthing === ch.id" :disabled="!ch.is_active" @click="reauthChannel(ch.id)">
              {{ $t('reauth') }}
            </v-btn>
            <v-btn v-if="ch.channel_type !== 'personal_zalo_import'" size="small" variant="text" color="primary" :disabled="!ch.is_active" @click="testConn(ch.id)">
              {{ $t('test_connection') }}
            </v-btn>
            <v-spacer />
            <v-btn icon="mdi-pencil" size="small" variant="text" color="primary" @click="openEdit(ch)" />
            <v-btn icon="mdi-delete" size="small" variant="text" color="error" @click="remove(ch.id)" />
          </div>
        </v-card>
      </v-col>
    </v-row>

    <div v-if="!channelStore.channels.length" class="text-center mt-12 pa-8">
      <v-icon size="64" color="grey-lighten-1" class="mb-4">mdi-chat-plus</v-icon>
      <div class="text-h6 text-grey-darken-1 mb-2">{{ $t('no_chat_channels') }}</div>
      <div class="text-body-2 text-grey mb-4" style="max-width: 500px; margin: 0 auto;">
        {{ $t('connect_channel_guide') }}
      </div>
      <v-btn color="primary" prepend-icon="mdi-plus" @click="showDialog = true">{{ $t('connect_channel_short') }}</v-btn>
    </div>

    <!-- Connect Channel Dialog -->
    <v-dialog v-model="showDialog" max-width="560">
      <v-card>
        <v-card-title class="px-6 pt-6 pb-2">{{ $t('connect_channel') }}</v-card-title>
        <v-card-text class="px-6 py-2">
          <v-select
            v-model="newChannel.channel_type"
            :label="$t('channel_type')"
            :items="[
              { title: $t('channel_zalo'), value: 'zalo_oa' },
              { title: $t('channel_facebook'), value: 'facebook' },
              { title: $t('channel_personal_zalo'), value: 'personal_zalo_import' },
            ]"
            class="mb-3"
          />
          <v-text-field v-model="newChannel.name" :label="$t('channel_name')" class="mb-3" />

          <!-- Zalo OA -->
          <template v-if="newChannel.channel_type === 'zalo_oa'">
            <v-btn variant="tonal" color="info" prepend-icon="mdi-book-open-variant" href="https://khanhpro303.github.io/cqa-bbi/usage/channels.html#zalo-oa" target="_blank" class="mb-3">
              {{ $t('zalo_app_id_guide') }}
            </v-btn>
            <v-text-field v-model="newChannel.creds.app_id" :label="$t('zalo_app_id')" density="compact" class="mb-2" :hint="$t('zalo_app_id_hint')" persistent-hint />
            <v-text-field v-model="newChannel.creds.app_secret" :label="$t('zalo_app_secret')" type="password" density="compact" class="mb-2" />
            <div class="text-caption text-grey-darken-1 mb-2">
              <v-icon size="14" class="mr-1">mdi-information-outline</v-icon>
              {{ $t('zalo_oa_selection_note') }}
            </div>
          </template>

          <!-- Facebook -->
          <template v-else-if="newChannel.channel_type === 'facebook'">
            <v-btn variant="tonal" color="info" prepend-icon="mdi-book-open-variant" href="https://khanhpro303.github.io/cqa-bbi/usage/facebook.html" target="_blank" class="mb-3">
              {{ $t('fb_guide') }}
            </v-btn>
            <v-text-field v-model="newChannel.creds.page_id" :label="$t('fb_page_id')" density="compact" class="mb-2" hint="Page ID từ Cài đặt trang Facebook" persistent-hint />
            <v-text-field v-model="newChannel.creds.access_token" :label="$t('fb_access_token')" density="compact" class="mb-2" hint="Page Access Token (nên dùng long-lived token)" persistent-hint />
          </template>
          <template v-else>
            <v-alert type="info" variant="tonal" class="mb-3">
              {{ $t('personal_zalo_note') }}
            </v-alert>
            <v-select
              v-model="newChannel.sync_scope"
              :items="[
                { title: $t('sync_scope_all'), value: 'all' },
                { title: $t('sync_scope_direct'), value: 'direct' },
                { title: $t('sync_scope_group'), value: 'group' },
              ]"
              :label="$t('sync_scope')"
              density="compact"
              class="mb-3"
              :hint="$t('sync_scope_hint')"
              persistent-hint
            />
          </template>

          <!-- Sync settings -->
          <template v-if="newChannel.channel_type !== 'personal_zalo_import'">
            <v-divider class="my-3" />
            <v-select
              v-model="newChannel.sync_interval"
              :items="syncIntervalOptions"
              :label="$t('sync_interval')"
              density="compact"
              class="mb-3"
              :hint="$t('sync_interval_hint')"
              persistent-hint
            />
            <v-alert v-if="newChannel.sync_interval <= 5" type="warning" variant="tonal" density="compact" class="mb-3">
              {{ $t('sync_interval_warning') }}
            </v-alert>
            <v-switch
              v-model="newChannel.sync_files"
              :label="$t('sync_files')"
              :hint="$t('sync_files_hint')"
              persistent-hint
              density="compact"
              color="primary"
            />
          </template>
        </v-card-text>

        <v-card-actions class="px-6 pb-6 pt-2">
          <v-spacer />
          <v-btn variant="text" @click="showDialog = false">{{ $t('cancel') }}</v-btn>
          <v-btn
            v-if="newChannel.channel_type === 'zalo_oa'"
            color="blue"
            :loading="creating"
            :disabled="!newChannel.name || !newChannel.creds.app_id || !newChannel.creds.app_secret"
            @click="createAndAuthZalo"
          >
            {{ $t('zalo_authorize') }}
          </v-btn>
          <v-btn
            v-else-if="newChannel.channel_type === 'personal_zalo_import'"
            color="teal"
            :loading="creating"
            :disabled="!newChannel.name"
            @click="createPersonalZalo"
          >
            {{ $t('create_personal_zalo') }}
          </v-btn>
          <v-btn
            v-else
            color="indigo"
            :loading="creating"
            :disabled="!newChannel.name || !newChannel.creds.page_id || !newChannel.creds.access_token"
            @click="createFacebook"
          >
            {{ $t('create') }}
          </v-btn>
        </v-card-actions>
      </v-card>
    </v-dialog>

    <!-- Edit Channel Dialog -->
    <v-dialog v-model="editDialog" max-width="600">
      <v-card>
        <v-card-title class="px-6 pt-6 pb-2">{{ $t('edit_channel') }}</v-card-title>
        
        <v-card-text class="px-6 py-2">
          <v-tabs v-model="editTab" color="primary" class="mb-4">
            <v-tab value="general">{{ $t('general_settings') }}</v-tab>
            <v-tab value="chatbot" v-if="editChannelType === 'zalo_oa'">{{ $t('chatbot_session') }}</v-tab>
          </v-tabs>

          <div style="max-height: 60vh; overflow-y: auto;" class="px-1">
            <v-window v-model="editTab">
              <v-window-item value="general" class="pt-3">
                <v-text-field v-model="editForm.name" :label="$t('channel_name')" class="mb-3" />
                <v-switch v-model="editForm.is_active" :label="$t('active')" color="primary" density="compact" class="mb-3" />
                <template v-if="editChannelType !== 'personal_zalo_import'">
                  <v-select
                    v-model="editForm.sync_interval"
                    :items="syncIntervalOptions"
                    :label="$t('sync_interval')"
                    density="compact"
                    class="mb-3"
                    :hint="$t('sync_interval_hint')"
                    persistent-hint
                  />
                  <v-alert v-if="editForm.sync_interval <= 5" type="warning" variant="tonal" density="compact" class="mb-3">
                    {{ $t('sync_interval_warning_fb_zalo') }}
                  </v-alert>
                  <v-switch v-model="editForm.sync_files" :label="$t('sync_files')" color="primary" density="compact" :hint="$t('sync_files_hint_short')" persistent-hint />
                </template>
                <template v-if="editChannelType === 'zalo_oa'">
                  <v-divider class="my-3" />
                  <div class="text-body-2 font-weight-medium mb-1">Cảnh báo lỗi gửi chiến dịch</div>
                  <v-switch
                    v-model="editForm.campaign_alerts_enabled"
                    label="Gửi cảnh báo qua Zalo khi chiến dịch gửi lỗi"
                    density="compact"
                    color="primary"
                    class="mb-1"
                  />
                  <v-select
                    v-if="editForm.campaign_alerts_enabled"
                    v-model="editForm.campaign_alert_group_id"
                    :items="alertGroupOptions"
                    :loading="loadingAlertGroups"
                    :no-data-text="loadingAlertGroups ? 'Đang tải…' : 'Chưa có nhóm GMF nào đã liên kết Zalo'"
                    label="Nhóm GMF nhận cảnh báo"
                    density="compact"
                    hint="Một tin tổng hợp sẽ gửi vào nhóm này khi có lượt gửi lỗi"
                    persistent-hint
                  />
                  <CampaignAlertOutputs v-if="editForm.campaign_alerts_enabled" v-model="editForm.campaign_alert_outputs" />
                </template>
              </v-window-item>
              
              <v-window-item value="chatbot" class="pt-3">
                <div class="text-subtitle-2 mb-2">{{ $t('session_config') }}</div>
                <v-text-field v-model="editForm.session_keyword" :label="$t('session_keyword')" :hint="$t('session_keyword_hint')" persistent-hint density="compact" class="mb-3" />
                <v-text-field v-model="editForm.session_end_keyword" :label="$t('session_end_keyword')" :hint="$t('session_end_keyword_hint')" persistent-hint density="compact" class="mb-3" />
                <v-text-field v-model="editForm.session_welcome_message" :label="$t('session_welcome')" :hint="$t('session_welcome_hint')" persistent-hint density="compact" class="mb-3" />
                <v-text-field v-model="editForm.session_goodbye_message" :label="$t('session_goodbye')" :hint="$t('session_goodbye_hint')" persistent-hint density="compact" class="mb-3" />
                <v-text-field v-model.number="editForm.session_timeout_minutes" :label="$t('session_timeout')" type="number" density="compact" class="mb-3" />
                <v-divider class="my-4" />
                <div class="text-subtitle-2 mb-2">{{ $t('langflow_integration') }}</div>
                <v-text-field v-model="editForm.langflow_api_url" label="Langflow API URL" density="compact" class="mb-3" />
                <v-text-field v-model="editForm.langflow_api_key" label="Langflow API Key" type="password" density="compact" class="mb-3" />
                <v-text-field v-model="editForm.langflow_flow_id" label="Langflow Flow ID" density="compact" class="mb-3" />
              </v-window-item>
            </v-window>
          </div>
        </v-card-text>
        
        <v-card-actions class="px-6 pb-6 pt-2">
          <v-spacer />
          <v-btn variant="text" @click="editDialog = false">{{ $t('cancel') }}</v-btn>
          <v-btn color="primary" :loading="savingEdit" @click="saveEdit">{{ $t('save_settings') }}</v-btn>
        </v-card-actions>
      </v-card>
    </v-dialog>

    <!-- Snackbar -->
    <v-snackbar v-model="snackbar" :color="snackColor" timeout="3000">{{ snackText }}</v-snackbar>
  </div>
</template>

<script setup lang="ts">
import { ref, computed, reactive, onMounted } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { useChannelStore } from '../stores/channels'
import { useAuthStore } from '../stores/auth'
import api from '../api'
import CampaignAlertOutputs from '../components/channels/CampaignAlertOutputs.vue'

const route = useRoute()
const router = useRouter()
const { t } = useI18n()
const channelStore = useChannelStore()
const authStore = useAuthStore()
const tenantId = computed(() => route.params.tenantId as string)

const showDialog = ref(false)
const creating = ref(false)
const syncing = ref('')
const reauthing = ref('')
const snackbar = ref(false)
const snackText = ref('')
const snackColor = ref('success')

// Per-channel auto-reply: only webhook channels expose the toggle; the tenant
// master switch (chatbot_active) gates them all.
const WEBHOOK_CHANNEL_TYPES = ['zalo_oa', 'facebook']
const togglingAutoReplyId = ref('')
const chatbotActive = ref(true)

async function fetchChatbotStatus() {
  try {
    const { data } = await api.get(`/tenants/${tenantId.value}/settings`)
    chatbotActive.value = data?.settings?.chatbot_active !== 'false'
  } catch { /* ignore — default to enabled */ }
}

async function toggleAutoReply(ch: any, val: boolean) {
  togglingAutoReplyId.value = ch.id
  try {
    await channelStore.updateChannel(tenantId.value, ch.id, { auto_reply_enabled: val })
    showSnack(val ? t('chatbot_status_on') : t('chatbot_status_off'), 'success')
    await channelStore.fetchChannels(tenantId.value)
  } catch (e: any) {
    showSnack(e?.response?.data?.error || t('error'), 'error')
  } finally {
    togglingAutoReplyId.value = ''
  }
}

const newChannel = reactive({
  channel_type: 'zalo_oa',
  name: '',
  creds: {} as Record<string, string>,
  sync_files: false,
  sync_interval: 15,
  sync_scope: 'all',
})

onMounted(() => {
  channelStore.fetchChannels(tenantId.value)
  fetchChatbotStatus()

  // Check if returning from OAuth callback
  const params = new URLSearchParams(window.location.search)
  if (params.get('zalo_auth') === 'success') {
    showSnack(t('zalo_auth_success'), 'success')
    channelStore.fetchChannels(tenantId.value)
    window.history.replaceState({}, '', window.location.pathname)
  } else if (params.get('zalo_auth') === 'error') {
    showSnack(params.get('message') || t('zalo_auth_failed'), 'error')
    window.history.replaceState({}, '', window.location.pathname)
  }
})

async function createAndAuthZalo() {
  creating.value = true
  try {
    // Step 1: Create channel with app_id + app_secret (no tokens yet)
    const created = await channelStore.createChannel(tenantId.value, {
      channel_type: newChannel.channel_type,
      name: newChannel.name,
      credentials: {
        app_id: newChannel.creds.app_id,
        app_secret: newChannel.creds.app_secret,
      },
      metadata: JSON.stringify({ sync_files: newChannel.sync_files, sync_interval: newChannel.sync_interval }),
    })
    showDialog.value = false

    // Step 2: Use backend reauth API to get signed OAuth URL (with HMAC state)
    const channelId = created?.id || created?.data?.id
    if (channelId && newChannel.channel_type === 'zalo_oa') {
      try {
        const { data: reauthData } = await api.post(`/tenants/${tenantId.value}/channels/${channelId}/reauth`)
        const redirectUrl = reauthData?.redirect_url
        if (redirectUrl) {
          window.location.href = redirectUrl
          return
        }
      } catch {
        // Fallback: redirect to channel detail
        router.push(`/${tenantId.value}/channels/${channelId}`)
        return
      }
    }
    showSnack(t('success'), 'success')
    channelStore.fetchChannels(tenantId.value)
    if (channelId) {
      router.push(`/${tenantId.value}/channels/${channelId}`)
    }

    newChannel.name = ''
    newChannel.creds = {}
  } catch {
    showSnack(t('error'), 'error')
  } finally {
    creating.value = false
  }
}

async function createFacebook() {
  creating.value = true
  try {
    await channelStore.createChannel(tenantId.value, {
      channel_type: newChannel.channel_type,
      name: newChannel.name,
      credentials: {
        page_id: newChannel.creds.page_id,
        access_token: newChannel.creds.access_token,
      },
      metadata: JSON.stringify({ sync_files: newChannel.sync_files, sync_interval: newChannel.sync_interval }),
    })
    showDialog.value = false
    newChannel.name = ''
    newChannel.creds = {}
    showSnack(t('success'), 'success')
    await channelStore.fetchChannels(tenantId.value)
  } catch {
    showSnack(t('error'), 'error')
  } finally {
    creating.value = false
  }
}

async function createPersonalZalo() {
  creating.value = true
  try {
    const created = await channelStore.createChannel(tenantId.value, {
      channel_type: 'personal_zalo_import',
      name: newChannel.name,
      credentials: {},
      metadata: JSON.stringify({ sync_scope: newChannel.sync_scope }),
    })
    showDialog.value = false
    newChannel.name = ''
    newChannel.creds = {}
    showSnack(t('personal_zalo_created'), 'success')
    await channelStore.fetchChannels(tenantId.value)
    const channelId = created?.id || created?.data?.id
    if (channelId) {
      router.push(`/${tenantId.value}/channels/${channelId}`)
    }
  } catch {
    showSnack(t('error'), 'error')
  } finally {
    creating.value = false
  }
}


async function toggleActive(ch: any) {
  try {
    const payload = {
      is_active: !ch.is_active
    }
    await channelStore.updateChannel(tenantId.value, ch.id, payload)
    showSnack(payload.is_active ? t('channel_activated') : t('channel_paused'), 'success')
    await channelStore.fetchChannels(tenantId.value)
  } catch {
    showSnack(t('error'), 'error')
  }
}

async function syncNow(channelId: string) {
  syncing.value = channelId
  try {
    const data = await channelStore.syncChannel(tenantId.value, channelId)
    const msg = data?.conversations_synced != null
      ? `${t('sync_now')}: ${data.conversations_synced} conversations, ${data.messages_synced} messages`
      : t('success')
    showSnack(msg, 'success')
    await channelStore.fetchChannels(tenantId.value)
  } catch (e: any) {
    showSnack(e?.response?.data?.error || t('error'), 'error')
  } finally {
    syncing.value = ''
  }
}

async function reauthChannel(channelId: string) {
  reauthing.value = channelId
  try {
    const { data } = await api.post(`/tenants/${tenantId.value}/channels/${channelId}/reauth`)
    if (data.redirect_url) {
      window.location.href = data.redirect_url
    }
  } catch (e: any) {
    showSnack(e?.response?.data?.error || t('reauth_error'), 'error')
  } finally {
    reauthing.value = ''
  }
}

async function testConn(channelId: string) {
  try {
    await channelStore.testConnection(tenantId.value, channelId)
    showSnack(t('connection_ok'), 'success')
  } catch {
    showSnack(t('connection_failed'), 'error')
  }
}

async function remove(channelId: string) {
  if (confirm('Delete this channel?')) {
    await channelStore.deleteChannel(tenantId.value, channelId)
  }
}

function syncColor(status: string) {
  if (status === 'success') return 'success'
  if (status === 'error') return 'error'
  return 'grey'
}

function showSnack(text: string, color: string) {
  snackText.value = text
  snackColor.value = color
  snackbar.value = true
}

function channelIcon(channelType: string) {
  if (channelType === 'zalo_oa') return 'mdi-message-text'
  if (channelType === 'facebook') return 'mdi-facebook-messenger'
  return 'mdi-cellphone-link'
}

function channelColor(channelType: string) {
  if (channelType === 'zalo_oa') return 'blue'
  if (channelType === 'facebook') return 'indigo'
  return 'teal'
}

function channelLabel(channelType: string) {
  if (channelType === 'zalo_oa') return t('channel_zalo')
  if (channelType === 'facebook') return t('channel_facebook')
  return 'Zalo cá nhân'
}



// Edit channel
const editDialog = ref(false)
const savingEdit = ref(false)
const editTab = ref('general')
const editChannelId = ref('')
const editChannelType = ref('')
const editForm = reactive({
  name: '', is_active: true, sync_files: false, sync_interval: 15,
  session_keyword: '', session_end_keyword: '', session_welcome_message: '', session_goodbye_message: '', session_timeout_minutes: 0,
  langflow_api_url: '', langflow_api_key: '', langflow_flow_id: '',
  campaign_alerts_enabled: false, campaign_alert_group_id: '', campaign_alert_outputs: [] as any[]
})
// Original parsed metadata of the channel being edited — spread back on save so
// keys this dialog doesn't manage (e.g. set in ChannelDetail) are never wiped.
const editOriginalMeta = ref<Record<string, unknown>>({})
const alertGroupOptions = ref<Array<{ title: string; value: string }>>([])
const loadingAlertGroups = ref(false)

async function loadAlertGroupOptions() {
  loadingAlertGroups.value = true
  try {
    const { data } = await api.get(`/tenants/${tenantId.value}/crm/groups`)
    alertGroupOptions.value = (Array.isArray(data) ? data : [])
      .filter((g: any) => g.zalo_group_id && (!g.channel_id || g.channel_id === editChannelId.value))
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

function openEdit(ch: any) {
  editChannelId.value = ch.id
  editChannelType.value = ch.channel_type
  editForm.name = ch.name
  editForm.is_active = ch.is_active
  editTab.value = 'general'
  try {
    const meta = JSON.parse(ch.metadata || '{}')
    editOriginalMeta.value = meta
    editForm.sync_files = meta.sync_files || false
    editForm.sync_interval = meta.sync_interval || 15
    editForm.session_keyword = meta.session_keyword || ''
    editForm.session_end_keyword = meta.session_end_keyword || ''
    editForm.session_welcome_message = meta.session_welcome_message || ''
    editForm.session_goodbye_message = meta.session_goodbye_message || ''
    editForm.session_timeout_minutes = meta.session_timeout_minutes || 0
    editForm.langflow_api_url = meta.langflow_api_url || ''
    editForm.langflow_api_key = meta.langflow_api_key || ''
    editForm.langflow_flow_id = meta.langflow_flow_id || ''
    editForm.campaign_alerts_enabled = meta.campaign_alerts_enabled || false
    editForm.campaign_alert_group_id = meta.campaign_alert_group_id || ''
    editForm.campaign_alert_outputs = Array.isArray(meta.campaign_alert_outputs) ? meta.campaign_alert_outputs : []
  } catch {
    editOriginalMeta.value = {}
    editForm.sync_files = false
    editForm.sync_interval = 15
    editForm.session_keyword = ''
    editForm.session_end_keyword = ''
    editForm.session_welcome_message = ''
    editForm.session_goodbye_message = ''
    editForm.session_timeout_minutes = 0
    editForm.langflow_api_url = ''
    editForm.langflow_api_key = ''
    editForm.langflow_flow_id = ''
    editForm.campaign_alerts_enabled = false
    editForm.campaign_alert_group_id = ''
    editForm.campaign_alert_outputs = []
  }
  if (ch.channel_type === 'zalo_oa') loadAlertGroupOptions()
  editDialog.value = true
}

async function saveEdit() {
  savingEdit.value = true
  try {
    const payload: Record<string, unknown> = {
      name: editForm.name,
      is_active: editForm.is_active,
    }
    if (editChannelType.value !== 'personal_zalo_import') {
      // Start from the channel's existing metadata so keys this dialog doesn't
      // manage (e.g. set in ChannelDetail) survive the save.
      const metaToSave: any = {
        ...editOriginalMeta.value,
        sync_files: editForm.sync_files,
        sync_interval: editForm.sync_interval,
      }
      if (editChannelType.value === 'zalo_oa') {
        metaToSave.session_keyword = editForm.session_keyword
        metaToSave.session_end_keyword = editForm.session_end_keyword
        metaToSave.session_welcome_message = editForm.session_welcome_message
        metaToSave.session_goodbye_message = editForm.session_goodbye_message
        metaToSave.session_timeout_minutes = editForm.session_timeout_minutes
        metaToSave.langflow_api_url = editForm.langflow_api_url
        metaToSave.langflow_api_key = editForm.langflow_api_key
        metaToSave.langflow_flow_id = editForm.langflow_flow_id
        metaToSave.campaign_alerts_enabled = editForm.campaign_alerts_enabled
        metaToSave.campaign_alert_group_id = editForm.campaign_alerts_enabled ? editForm.campaign_alert_group_id : ''
        metaToSave.campaign_alert_outputs = editForm.campaign_alerts_enabled ? editForm.campaign_alert_outputs : []
      }
      payload.metadata = JSON.stringify(metaToSave)
    }
    await channelStore.updateChannel(tenantId.value, editChannelId.value, payload)
    editDialog.value = false
    showSnack(t('success'), 'success')
    channelStore.fetchChannels(tenantId.value)
  } catch {
    showSnack(t('error'), 'error')
  } finally {
    savingEdit.value = false
  }
}
</script>
