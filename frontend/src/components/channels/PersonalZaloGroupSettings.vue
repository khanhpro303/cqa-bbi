<template>
  <div>
    <v-alert type="info" variant="tonal" density="compact" class="mb-4">
      {{ $t('personal_zalo_group_reply_desc') }}
    </v-alert>

    <v-switch
      v-model="settings.personal_zalo_group_reply_enabled"
      :label="$t('personal_zalo_group_reply_enabled')"
      :hint="$t('personal_zalo_group_reply_enabled_hint')"
      persistent-hint
      density="compact"
      color="primary"
      data-testid="personal-zalo-group-reply-enabled"
    />
    <v-text-field
      v-model="settings.group_bot_names"
      :label="$t('group_bot_names')"
      :hint="$t('personal_zalo_group_bot_names_hint')"
      placeholder="bot"
      persistent-hint
      density="compact"
      class="mt-3"
      data-testid="personal-zalo-group-bot-names"
    />

    <v-divider class="my-4" />
    <div class="text-body-2 font-weight-medium mb-1">{{ $t('handover_alert_settings_title') }}</div>
    <v-select
      v-model="settings.handover_alert_group_id"
      :items="handoverGroupOptions"
      :loading="loadingGroups"
      :no-data-text="loadingGroups ? $t('loading') : $t('personal_zalo_handover_no_groups')"
      :label="$t('personal_zalo_handover_group_label')"
      :hint="$t('personal_zalo_handover_group_hint')"
      persistent-hint
      density="compact"
      clearable
      data-testid="personal-zalo-handover-group"
      @update:model-value="selectGroup"
    />
    <div class="text-caption text-medium-emphasis text-center my-2">{{ $t('or') }}</div>
    <v-select
      v-model="settings.personal_zalo_handover_staff_id"
      :items="handoverStaffOptions"
      :loading="loadingStaff"
      :no-data-text="loadingStaff ? $t('loading') : $t('personal_zalo_handover_no_staff')"
      :label="$t('personal_zalo_handover_staff_label')"
      :hint="$t('personal_zalo_handover_staff_hint')"
      persistent-hint
      density="compact"
      clearable
      data-testid="personal-zalo-handover-staff"
      @update:model-value="selectStaff"
    />
  </div>
</template>

<script setup lang="ts">
import { onMounted, ref } from 'vue'
import api from '../../api'
import {
  personalZaloHandoverGroupOptions,
  personalZaloHandoverStaffOptions,
  type PersonalZaloGroupSettings,
  type PersonalZaloHandoverGroup,
  type PersonalZaloHandoverStaff,
} from './personalZaloGroupSettings'

const props = defineProps<{ tenantId: string; activeChannelIds: string[] }>()
const settings = defineModel<PersonalZaloGroupSettings>({ required: true })
const handoverGroupOptions = ref<Array<{ title: string; value: string }>>([])
const handoverStaffOptions = ref<Array<{ title: string; value: string }>>([])
const loadingGroups = ref(true)
const loadingStaff = ref(true)

function selectGroup(value: string | null) {
  if (value) settings.value.personal_zalo_handover_staff_id = ''
}

function selectStaff(value: string | null) {
  if (value) settings.value.handover_alert_group_id = ''
}

onMounted(async () => {
  await Promise.all([
    api.get(`/tenants/${props.tenantId}/crm/groups`)
      .then(({ data }) => {
        handoverGroupOptions.value = personalZaloHandoverGroupOptions(
          Array.isArray(data) ? data as PersonalZaloHandoverGroup[] : [],
        )
      })
      .catch(() => { handoverGroupOptions.value = [] })
      .finally(() => { loadingGroups.value = false }),
    api.get(`/tenants/${props.tenantId}/zalo-whitelist`)
      .then(({ data }) => {
        handoverStaffOptions.value = personalZaloHandoverStaffOptions(
          Array.isArray(data) ? data as PersonalZaloHandoverStaff[] : [],
          props.activeChannelIds,
        )
      })
      .catch(() => { handoverStaffOptions.value = [] })
      .finally(() => { loadingStaff.value = false }),
  ])
})
</script>
