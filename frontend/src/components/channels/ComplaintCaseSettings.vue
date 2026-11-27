<template>
  <div data-testid="complaint-case-settings">
    <v-alert type="info" variant="tonal" density="compact" class="mb-4">
      {{ $t(channelType === 'personal_zalo_import' ? 'personal_zalo_complaint_settings_desc' : 'zalo_oa_complaint_settings_desc') }}
    </v-alert>
    <v-switch
      v-model="settings.complaint_cases"
      :label="$t('complaint_cases_switch')"
      :hint="$t(channelType === 'personal_zalo_import' ? 'personal_zalo_complaint_cases_switch_hint' : 'complaint_cases_switch_hint')"
      persistent-hint
      density="compact"
      color="primary"
      :data-testid="channelType === 'personal_zalo_import' ? 'personal-zalo-complaint-cases' : 'complaint-cases'"
    />
    <template v-if="settings.complaint_cases">
      <v-text-field
        v-model.number="settings.complaint_resolved_days"
        type="number"
        min="1"
        :max="MAX_COMPLAINT_RESOLVED_DAYS"
        :label="$t('complaint_resolved_days')"
        :hint="$t('complaint_resolved_days_hint')"
        persistent-hint
        density="compact"
        class="mt-3"
        style="max-width: 320px"
        :data-testid="channelType === 'personal_zalo_import' ? 'personal-zalo-complaint-resolved-days' : 'complaint-resolved-days'"
      />
      <ComplaintStreams v-model="settings.complaint_streams" :group-options="groupOptions" />
    </template>
  </div>
</template>

<script setup lang="ts">
import { onMounted, ref } from 'vue'
import api from '../../api'
import ComplaintStreams from './ComplaintStreams.vue'
import { personalZaloHandoverGroupOptions, type PersonalZaloHandoverGroup } from './personalZaloGroupSettings'
import { MAX_COMPLAINT_RESOLVED_DAYS, type ComplaintStream } from './zaloGroupSettings'

const props = defineProps<{
  tenantId: string
  channelId: string
  channelType: 'zalo_oa' | 'personal_zalo_import'
}>()
const settings = defineModel<{
  complaint_cases: boolean
  complaint_resolved_days: number
  complaint_streams: ComplaintStream[]
}>({ required: true })
const groupOptions = ref<Array<{ title: string; value: string }>>([])

onMounted(async () => {
  try {
    const { data } = await api.get(`/tenants/${props.tenantId}/crm/groups`)
    const groups = Array.isArray(data) ? data : []
    groupOptions.value = props.channelType === 'personal_zalo_import'
      ? personalZaloHandoverGroupOptions(groups as PersonalZaloHandoverGroup[])
      : groups
        .filter((group) => group.zalo_group_id && (!group.channel_id || group.channel_id === props.channelId))
        .map((group) => ({ title: group.name, value: group.id }))
  } catch {
    groupOptions.value = []
  }
})
</script>
