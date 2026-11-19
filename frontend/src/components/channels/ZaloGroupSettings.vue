<template>
  <div>
    <div class="text-body-2 font-weight-medium mb-1">{{ $t('group_assistant_settings_title') }}</div>
    <v-switch
      v-model="settings.group_assistant_mode"
      :label="$t('group_assistant_mode')"
      :hint="$t('group_assistant_mode_hint')"
      persistent-hint
      density="compact"
      color="primary"
      data-testid="group-assistant-mode"
    />

    <v-divider class="my-3" />
    <div class="text-body-2 font-weight-medium mb-1">{{ $t('campaign_alert_settings_title') }}</div>
    <v-switch
      v-model="settings.campaign_alerts_enabled"
      :label="$t('campaign_alert_enable_switch')"
      density="compact"
      color="primary"
      class="mb-1"
    />
    <v-select
      v-if="settings.campaign_alerts_enabled"
      v-model="settings.campaign_alert_group_id"
      :items="alertGroupOptions"
      :loading="loadingAlertGroups"
      :no-data-text="loadingAlertGroups ? $t('loading') : $t('campaign_alert_no_groups')"
      :label="$t('campaign_alert_group_label')"
      density="compact"
      clearable
      :hint="$t('campaign_alert_group_hint')"
      persistent-hint
    />
    <CampaignAlertOutputs v-if="settings.campaign_alerts_enabled" v-model="settings.campaign_alert_outputs" />

    <v-divider class="my-3" />
    <div class="text-body-2 font-weight-medium mb-1">{{ $t('handover_alert_settings_title') }}</div>
    <v-select
      v-model="settings.handover_alert_group_id"
      :items="alertGroupOptions"
      :loading="loadingAlertGroups"
      :no-data-text="loadingAlertGroups ? $t('loading') : $t('campaign_alert_no_groups')"
      :label="$t('handover_alert_group_label')"
      density="compact"
      clearable
      :hint="$t('handover_alert_group_hint')"
      persistent-hint
    />

    <v-divider class="my-3" />
    <div class="text-body-2 font-weight-medium mb-1">{{ $t('chat_memory_settings_title') }}</div>
    <v-switch
      v-model="settings.chat_memory_issue_alerts"
      :label="$t('chat_memory_issue_alerts')"
      :hint="$t('chat_memory_issue_alerts_hint')"
      persistent-hint
      density="compact"
      color="primary"
      class="mb-2"
      data-testid="chat-memory-issue-alerts"
    />
    <v-switch
      v-model="settings.chat_memory_profile_in_prompt"
      :label="$t('chat_memory_profile_in_prompt')"
      :hint="$t('chat_memory_profile_in_prompt_hint')"
      persistent-hint
      density="compact"
      color="primary"
      data-testid="chat-memory-profile-in-prompt"
    />
  </div>
</template>

<script setup lang="ts">
import { onMounted, ref } from 'vue'
import api from '../../api'
import CampaignAlertOutputs from './CampaignAlertOutputs.vue'
import type { ZaloGroupSettings } from './zaloGroupSettings'

const props = defineProps<{ tenantId: string; channelId: string }>()
const settings = defineModel<ZaloGroupSettings>({ required: true })
const alertGroupOptions = ref<Array<{ title: string; value: string }>>([])
const loadingAlertGroups = ref(true)

onMounted(async () => {
  try {
    const { data } = await api.get(`/tenants/${props.tenantId}/crm/groups`)
    alertGroupOptions.value = (Array.isArray(data) ? data : [])
      .filter((group) => group.zalo_group_id && (!group.channel_id || group.channel_id === props.channelId))
      .map((group) => ({ title: group.name, value: group.id }))
  } catch {
    alertGroupOptions.value = []
  } finally {
    loadingAlertGroups.value = false
  }
})
</script>
