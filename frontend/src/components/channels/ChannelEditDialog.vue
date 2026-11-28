<template>
  <v-dialog
    v-model="open"
    :activator="activator"
    :open-on-click="false"
    :persistent="saving"
    :aria-labelledby="dialogId"
    max-width="600"
  >
    <v-card>
      <v-card-title :id="dialogId" class="px-6 pt-6 pb-2">{{ $t('edit_channel') }}</v-card-title>

      <v-card-text class="px-6 py-2">
        <v-tabs v-model="editTab" :aria-label="$t('edit_channel')" color="primary" class="mb-4" show-arrows>
          <v-tab :id="dialogId + '-general-tab'" value="general" :aria-controls="dialogId + '-general'">{{ $t('general_settings') }}</v-tab>
          <v-tab v-if="isZaloChatbotChannel" :id="dialogId + '-chatbot-tab'" value="chatbot" :aria-controls="dialogId + '-chatbot'">{{ $t('chatbot_session') }}</v-tab>
          <v-tab v-if="isZaloChatbotChannel" :id="dialogId + '-groups-tab'" value="groups" :aria-controls="dialogId + '-groups'">{{ $t('zalo_group_settings_tab') }}</v-tab>
          <v-tab v-if="isZaloChatbotChannel" :id="dialogId + '-complaints-tab'" value="complaints" :aria-controls="dialogId + '-complaints'">{{ $t('complaint_settings_tab') }}</v-tab>
        </v-tabs>

        <div style="max-height: 60vh; overflow-y: auto;" class="px-1">
          <v-window v-model="editTab">
            <v-window-item :id="dialogId + '-general'" value="general" role="tabpanel" :aria-labelledby="dialogId + '-general-tab'" class="pt-3">
              <v-text-field v-model="editForm.name" :label="$t('channel_name')" class="mb-3" />
              <v-switch v-model="editForm.is_active" :label="$t('active')" color="primary" density="compact" class="mb-3" />
              <template v-if="editChannelType === 'personal_zalo_import'">
                <PersonalZaloBotModePicker
                  :model-value="editBotSettings.chatbot_mode"
                  :disabled="!canManageBotMode || saving"
                  @update:model-value="changeBotMode"
                />
                <template v-if="open && initialChannel && canManageBotMode && editBotSettings.chatbot_mode === 'internbot'">
                  <PersonalZaloInternbotSettings
                    v-if="savedBotSettings.chatbot_mode === 'internbot'"
                    :key="editChannelId"
                    v-model="editBotSettings"
                    :tenant-id="tenantId"
                    :channel-id="initialChannel.id"
                    :channel-active="editForm.is_active"
                    :master-active="chatbotStore.isLoaded(tenantId) ? chatbotStore.isActive(tenantId) : undefined"
                    :saved-group-id="savedBotSettings.supervisor_group_id"
                    :saved-enabled="savedBotSettings.internbot_enabled"
                    :disabled="saving"
                  />
                  <v-alert v-else type="info" variant="tonal" density="compact" class="mb-3">
                    {{ $t('personal_zalo_internbot_save_mode_first') }}
                  </v-alert>
                </template>
              </template>
              <v-select
                v-if="editChannelType === 'personal_zalo_import'"
                v-model="editForm.sync_scope"
                :items="[
                  { title: $t('sync_scope_all'), value: 'all' },
                  { title: $t('sync_scope_direct'), value: 'direct' },
                  { title: $t('sync_scope_group'), value: 'group' },
                ]"
                :label="$t('sync_scope')"
                :hint="$t('sync_scope_hint')"
                density="compact"
                persistent-hint
              />
              <template v-if="editChannelType === 'personal_zalo_import'">
                <v-select
                  v-model="editForm.personal_sync_interval"
                  :items="personalSyncIntervalOptions"
                  :label="$t('sync_interval')"
                  :hint="$t('personal_zalo_sync_interval_hint')"
                  density="compact"
                  class="mt-3"
                  persistent-hint
                />
              </template>
              <template v-else>
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
              </template>
              <v-switch
                v-model="editForm.sync_files"
                :label="$t('sync_files')"
                :hint="$t(editChannelType === 'personal_zalo_import' ? 'personal_zalo_sync_files_hint' : 'sync_files_hint_short')"
                color="primary"
                density="compact"
                persistent-hint
              />
            </v-window-item>

            <v-window-item v-if="isZaloChatbotChannel" :id="dialogId + '-chatbot'" value="chatbot" role="tabpanel" :aria-labelledby="dialogId + '-chatbot-tab'" class="pt-3">
              <div class="text-subtitle-2 mb-2">{{ $t('session_config') }}</div>
              <v-alert v-if="editChannelType === 'personal_zalo_import'" type="info" variant="tonal" density="compact" class="mb-3">
                {{ $t('personal_zalo_session_hint') }}
              </v-alert>
              <v-text-field v-model="editForm.session_keyword" :label="$t('session_keyword')" :hint="$t('session_keyword_hint')" persistent-hint density="compact" class="mb-3" />
              <v-text-field v-model="editForm.session_end_keyword" :label="$t('session_end_keyword')" :hint="$t('session_end_keyword_hint')" persistent-hint density="compact" class="mb-3" />
              <v-text-field v-model="editForm.session_welcome_message" :label="$t('session_welcome')" :hint="$t('session_welcome_hint')" persistent-hint density="compact" class="mb-3" />
              <v-text-field v-model="editForm.session_goodbye_message" :label="$t('session_goodbye')" :hint="$t('session_goodbye_hint')" persistent-hint density="compact" class="mb-3" />
              <v-text-field v-model.number="editForm.session_timeout_minutes" :label="$t('session_timeout')" :min="editChannelType === 'personal_zalo_import' ? 0 : undefined" :max="editChannelType === 'personal_zalo_import' ? 1440 : undefined" type="number" density="compact" class="mb-3" />
              <template v-if="editChannelType === 'zalo_oa'">
                <v-divider class="my-4" />
                <div class="text-subtitle-2 mb-1">{{ $t('casual_budget_title') }}</div>
                <div class="text-caption text-medium-emphasis mb-3">{{ $t('casual_budget_desc') }}</div>
                <v-row dense>
                  <v-col cols="12" sm="6">
                    <v-text-field v-model.number="editForm.casual_full_agent_turns" :label="$t('casual_full_agent_turns')" :hint="$t('casual_full_agent_turns_hint')" persistent-hint type="number" min="0" max="1000" step="1" :rules="[casualWholeRule]" density="compact" class="mb-3" data-testid="casual-full-agent-turns" />
                  </v-col>
                  <v-col cols="12" sm="6">
                    <v-text-field v-model.number="editForm.casual_quiet_after" :label="$t('casual_quiet_after')" :hint="$t('casual_quiet_after_hint')" persistent-hint type="number" min="0" max="1000" step="1" :rules="[casualWholeRule, casualQuietRule]" density="compact" class="mb-3" data-testid="casual-quiet-after" />
                  </v-col>
                </v-row>
              </template>
              <v-divider class="my-4" />
              <div class="text-subtitle-2 mb-2">{{ $t('langflow_integration') }}</div>
              <v-alert type="info" variant="tonal" density="compact" class="mb-3">
                {{ $t('langflow_managed_in_ai_engines') }}
              </v-alert>
              <v-alert v-if="hasLegacyLangflowOverride" type="warning" variant="tonal" density="compact" class="mb-3">
                {{ $t('langflow_legacy_override_notice') }}
              </v-alert>
            </v-window-item>
            <v-window-item v-if="isZaloChatbotChannel" :id="dialogId + '-groups'" value="groups" role="tabpanel" :aria-labelledby="dialogId + '-groups-tab'" class="pt-3">
              <ZaloGroupSettings
                v-if="open && initialChannel?.channel_type === 'zalo_oa'"
                :key="editChannelId"
                v-model="editGroupSettings"
                :tenant-id="tenantId"
                :channel-id="initialChannel.id"
              />
              <PersonalZaloGroupSettings
                v-else-if="open"
                :key="editChannelId"
                v-model="editPersonalGroupSettings"
                :tenant-id="tenantId"
              />
            </v-window-item>
            <v-window-item v-if="isZaloChatbotChannel" :id="dialogId + '-complaints'" value="complaints" role="tabpanel" :aria-labelledby="dialogId + '-complaints-tab'" class="pt-3">
              <ComplaintCaseSettings
                v-if="open && initialChannel && (initialChannel.channel_type === 'zalo_oa' || initialChannel.channel_type === 'personal_zalo_import')"
                :key="editChannelId"
                v-model="editComplaintSettings"
                :tenant-id="tenantId"
                :channel-id="initialChannel.id"
                :channel-type="initialChannel.channel_type"
              />
            </v-window-item>
          </v-window>
        </div>
      </v-card-text>

      <v-alert v-if="saveError" role="alert" type="error" variant="tonal" density="compact" class="mx-6 mb-2">
        {{ saveError }}
      </v-alert>

      <v-card-actions class="px-6 pb-6 pt-2">
        <v-spacer />
        <v-btn variant="text" :disabled="saving" @click="open = false">{{ $t('cancel') }}</v-btn>
        <v-btn color="primary" :loading="saving" @click="saveEdit">{{ $t('save_settings') }}</v-btn>
      </v-card-actions>
    </v-card>
  </v-dialog>
  <v-snackbar v-model="showSaved" color="success" :timeout="3000">{{ $t('success') }}</v-snackbar>
</template>

<script setup lang="ts">
import { isAxiosError } from 'axios'
import { computed, reactive, ref, shallowRef, useId, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useAuthStore } from '../../stores/auth'
import { useChannelStore, type Channel } from '../../stores/channels'
import { useChatbotStore } from '../../stores/chatbot'
import ComplaintCaseSettings from './ComplaintCaseSettings.vue'
import PersonalZaloBotModePicker from './PersonalZaloBotModePicker.vue'
import PersonalZaloGroupSettings from './PersonalZaloGroupSettings.vue'
import PersonalZaloInternbotSettings from './PersonalZaloInternbotSettings.vue'
import ZaloGroupSettings from './ZaloGroupSettings.vue'
import { readPersonalZaloBotSettings, type PersonalZaloBotMode } from './personalZaloBotSettings'
import { personalZaloGroupSettingsPatch, readPersonalZaloGroupSettings } from './personalZaloGroupSettings'
import { readZaloGroupSettings, zaloGroupSettingsPatch } from './zaloGroupSettings'

const props = defineProps<{ tenantId: string; channel: Channel; activator?: HTMLElement }>()
const open = defineModel<boolean>({ required: true })
const emit = defineEmits<{ saved: [channelId: string] }>()
const { t } = useI18n()
const channelStore = useChannelStore()
const authStore = useAuthStore()
const chatbotStore = useChatbotStore()
const dialogId = useId()
const showSaved = ref(false)

// Small-talk budget (backend/workers/casual_budget.go). Same defaults as the
// backend uses when the channel has no value.
const CASUAL_FULL_AGENT_TURNS_DEFAULT = 3
const CASUAL_QUIET_AFTER_DEFAULT = 20
const CASUAL_SETTING_MAX = 1000 // same bound as casualSettingMax in the backend
function parseCasualSetting(value: unknown): number | null {
  const n = typeof value === 'string' && value.trim() !== '' ? Number(value) : value
  return typeof n === 'number' && Number.isInteger(n) && n >= 0 && n <= CASUAL_SETTING_MAX ? n : null
}
const casualWholeRule = (v: unknown) => parseCasualSetting(v) !== null || t('casual_setting_whole_number')
const casualQuietRule = (v: unknown) => {
  const quiet = parseCasualSetting(v)
  const full = parseCasualSetting(editForm.casual_full_agent_turns)
  return quiet === null || full === null || quiet === 0 || quiet > full || t('casual_quiet_after_too_low')
}

const saving = ref(false)
const saveError = ref('')
const editTab = ref('general')
const initialChannel = shallowRef<Channel>()
const editChannelId = computed(() => initialChannel.value?.id)
const editChannelType = computed(() => initialChannel.value?.channel_type)
const isZaloChatbotChannel = computed(() => editChannelType.value === 'zalo_oa' || editChannelType.value === 'personal_zalo_import')
const canManageBotMode = computed(() => authStore.canEdit('channels') && ['owner', 'admin'].includes(authStore.tenantPerms?.role ?? ''))
const editBotSettings = ref(readPersonalZaloBotSettings())
const savedBotSettings = ref(readPersonalZaloBotSettings())
const editForm = reactive({
  name: '', is_active: true, sync_files: false, sync_interval: 15, sync_scope: 'all',
  personal_sync_interval: 'gateway' as number | 'gateway',
  session_keyword: '', session_end_keyword: '', session_welcome_message: '', session_goodbye_message: '', session_timeout_minutes: 0,
  casual_full_agent_turns: CASUAL_FULL_AGENT_TURNS_DEFAULT as number | string, casual_quiet_after: CASUAL_QUIET_AFTER_DEFAULT as number | string,
})
const editGroupSettings = ref(readZaloGroupSettings())
const editPersonalGroupSettings = ref(readPersonalZaloGroupSettings())
const editComplaintSettings = computed({
  get: () => editChannelType.value === 'zalo_oa' ? editGroupSettings.value : editPersonalGroupSettings.value,
  set: (settings) => {
    if (editChannelType.value === 'zalo_oa') editGroupSettings.value = settings as typeof editGroupSettings.value
    else editPersonalGroupSettings.value = settings as typeof editPersonalGroupSettings.value
  },
})
let editOriginalMeta: Record<string, unknown> = {}
let initialSettings: Record<string, unknown> = {}
const hasLegacyLangflowOverride = ref(false)
const LEGACY_LANGFLOW_META_KEYS = ['langflow_api_url', 'langflow_api_key', 'langflow_flow_id', 'langflow_public_flow_id']
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
const personalSyncIntervalOptions = computed(() => {
  const selected = editForm.personal_sync_interval
  const customOption = typeof selected === 'number' && !syncIntervalOptions.value.some(option => option.value === selected)
    ? [{ title: t('minutes_unit', { count: selected }), value: selected }]
    : []
  return [{ title: t('personal_zalo_sync_gateway_default'), value: 'gateway' }, ...syncIntervalOptions.value, ...customOption]
})

watch(open, (isOpen) => {
  if (!isOpen) return

  const channel = props.channel
  const metadata = readMetadata(channel.metadata)
  editTab.value = 'general'
  saveError.value = ''
  initialChannel.value = { ...channel }
  editOriginalMeta = metadata
  savedBotSettings.value = readPersonalZaloBotSettings(metadata)
  editBotSettings.value = { ...savedBotSettings.value }
  if (channel.channel_type === 'personal_zalo_import' && savedBotSettings.value.chatbot_mode === 'internbot' && canManageBotMode.value) {
    void chatbotStore.fetchStatus(props.tenantId)
  }
  hasLegacyLangflowOverride.value = LEGACY_LANGFLOW_META_KEYS.some((key) => Boolean(metadata[key]))
  Object.assign(editForm, {
    name: channel.name,
    is_active: channel.is_active,
    sync_files: metadata.sync_files === true,
    sync_interval: typeof metadata.sync_interval === 'number' && metadata.sync_interval > 0 ? metadata.sync_interval : 15,
    personal_sync_interval: typeof metadata.sync_interval === 'number' && Number.isInteger(metadata.sync_interval) &&
      metadata.sync_interval >= 1 && metadata.sync_interval <= 1440 ? metadata.sync_interval : 'gateway',
    sync_scope: metadata.sync_scope === 'direct' || metadata.sync_scope === 'group' ? metadata.sync_scope : 'all',
    session_keyword: typeof metadata.session_keyword === 'string' ? metadata.session_keyword : '',
    session_end_keyword: typeof metadata.session_end_keyword === 'string' ? metadata.session_end_keyword : '',
    session_welcome_message: typeof metadata.session_welcome_message === 'string' ? metadata.session_welcome_message : '',
    session_goodbye_message: typeof metadata.session_goodbye_message === 'string' ? metadata.session_goodbye_message : '',
    session_timeout_minutes: typeof metadata.session_timeout_minutes === 'number' ? metadata.session_timeout_minutes : 0,
    casual_full_agent_turns: parseCasualSetting(metadata.casual_full_agent_turns) ?? CASUAL_FULL_AGENT_TURNS_DEFAULT,
    casual_quiet_after: parseCasualSetting(metadata.casual_quiet_after) ?? CASUAL_QUIET_AFTER_DEFAULT,
  })
  editGroupSettings.value = readZaloGroupSettings(metadata)
  editPersonalGroupSettings.value = readPersonalZaloGroupSettings(metadata)
  initialSettings = settingsMetadata()
}, { immediate: true })

function changeBotMode(mode: PersonalZaloBotMode) {
  if (!canManageBotMode.value || mode === editBotSettings.value.chatbot_mode) return
  editBotSettings.value = { ...editBotSettings.value, chatbot_mode: mode, internbot_enabled: false }
}

function readMetadata(raw: string): Record<string, unknown> {
  try {
    const metadata = JSON.parse(raw || '{}')
    return metadata && typeof metadata === 'object' && !Array.isArray(metadata) ? metadata : {}
  } catch {
    return {}
  }
}

function settingsMetadata(): Record<string, unknown> {
  const sessionSettings = {
    session_keyword: editForm.session_keyword,
    session_end_keyword: editForm.session_end_keyword,
    session_welcome_message: editForm.session_welcome_message,
    session_goodbye_message: editForm.session_goodbye_message,
    session_timeout_minutes: Number(editForm.session_timeout_minutes),
  }
  if (editChannelType.value === 'personal_zalo_import') {
    return {
      sync_scope: editForm.sync_scope,
      sync_interval: editForm.personal_sync_interval === 'gateway' ? null : editForm.personal_sync_interval,
      sync_files: editForm.sync_files,
      ...sessionSettings,
      ...personalZaloGroupSettingsPatch(editPersonalGroupSettings.value),
      ...editBotSettings.value,
    }
  }

  const syncSettings = { sync_files: editForm.sync_files, sync_interval: editForm.sync_interval }
  if (editChannelType.value === 'zalo_oa') {
    return {
      ...syncSettings,
      ...sessionSettings,
      casual_full_agent_turns: parseCasualSetting(editForm.casual_full_agent_turns),
      casual_quiet_after: parseCasualSetting(editForm.casual_quiet_after),
      ...zaloGroupSettingsPatch(editGroupSettings.value, editOriginalMeta),
    }
  }

  return syncSettings
}

async function saveEdit() {
  const channel = initialChannel.value
  if (!channel || saving.value) return
  saveError.value = ''

  if (channel.channel_type === 'zalo_oa') {
    const problem = [casualWholeRule(editForm.casual_full_agent_turns), casualWholeRule(editForm.casual_quiet_after), casualQuietRule(editForm.casual_quiet_after)]
      .find((rule) => rule !== true)
    if (problem) {
      editTab.value = 'chatbot'
      saveError.value = String(problem)
      return
    }
  }

  const settings = settingsMetadata()
  const changedSettings = Object.entries(settings)
    .filter(([key, value]) => JSON.stringify(value) !== JSON.stringify(initialSettings[key]))
  const changes = Object.fromEntries(changedSettings)
  if (channel.channel_type === 'personal_zalo_import' &&
      ('handover_alert_group_id' in changes || 'personal_zalo_handover_staff_id' in changes)) {
    // The server validates the two mutually exclusive destinations together.
    changes.handover_alert_group_id = settings.handover_alert_group_id
    changes.personal_zalo_handover_staff_id = settings.personal_zalo_handover_staff_id
  }
  if (channel.channel_type === 'personal_zalo_import' && 'supervisor_group_id' in changes) {
    changes.internbot_enabled = settings.internbot_enabled
  }
  if (!editForm.is_active && changes.internbot_enabled === true) changes.internbot_enabled = false
  const payload: Record<string, unknown> = {}
  if (editForm.name && editForm.name !== channel.name) payload.name = editForm.name
  if (editForm.is_active !== channel.is_active) payload.is_active = editForm.is_active
  if (Object.keys(changes).length) {
    // Zalo accepts a patch. Facebook replaces the full metadata object.
    payload.metadata = JSON.stringify(isZaloChatbotChannel.value ? changes : { ...editOriginalMeta, ...settings })
  }
  if (!Object.keys(payload).length) {
    open.value = false
    return
  }

  saving.value = true
  try {
    await channelStore.updateChannel(props.tenantId, channel.id, payload)
    open.value = false
    showSaved.value = true
    emit('saved', channel.id)
  } catch (error: unknown) {
    const response = isAxiosError<{ error?: string; details?: string }>(error) ? error.response?.data : undefined
    const errorLabels = {
      internbot_enable_separately: 'personal_zalo_internbot_save_mode_first',
      internbot_supervisor_unavailable: 'personal_zalo_internbot_group_unavailable',
      personal_zalo_roster_unavailable: 'personal_zalo_internbot_groups_unavailable',
    } as const
    const label = response?.error && errorLabels[response.error as keyof typeof errorLabels]
    saveError.value = label ? t(label) : response?.details || t('error')
  } finally {
    saving.value = false
  }
}
</script>
