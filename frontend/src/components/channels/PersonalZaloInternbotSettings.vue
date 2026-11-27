<template>
  <div>
    <div class="d-flex align-start ga-2">
      <v-autocomplete
        :model-value="settings.supervisor_group_id || null"
        v-model:search="search"
        :items="options"
        :label="$t('personal_zalo_internbot_group')"
        :hint="$t('personal_zalo_internbot_group_hint')"
        :loading="loading"
        :disabled="disabled || status !== 'ready'"
        :no-data-text="$t('personal_zalo_internbot_no_groups')"
        density="compact"
        persistent-hint
        clearable
        no-filter
        class="flex-grow-1"
        @update:model-value="selectGroup"
      >
        <template v-if="groups.length < total" #append-item>
          <v-list-item
            :title="$t('personal_zalo_internbot_load_more')"
            :disabled="loading || disabled"
            @click.stop="loadGroups({ append: true })"
          />
        </template>
      </v-autocomplete>
      <v-btn
        icon="mdi-refresh"
        variant="text"
        density="compact"
        :aria-label="$t('personal_zalo_internbot_refresh_groups')"
        :title="$t('personal_zalo_internbot_refresh_groups')"
        :disabled="disabled"
        :loading="loading"
        @click="loadGroups({ refresh: true })"
      />
    </div>

    <div class="text-caption text-medium-emphasis mt-2" role="status">{{ $t(statusKey) }}</div>
    <div v-if="capturedAt" class="text-caption text-medium-emphasis">
      {{ $t('personal_zalo_internbot_groups_synced', { time: new Date(capturedAt).toLocaleString(locale) }) }}
    </div>

    <v-checkbox
      v-if="canChooseToEnable"
      v-model="confirmed"
      :disabled="disabled"
      :label="$t('personal_zalo_internbot_confirm_group')"
      density="compact"
      hide-details
      class="mt-2"
    />
    <v-switch
      :model-value="settings.internbot_enabled"
      :label="$t('personal_zalo_internbot_enable')"
      :disabled="disabled || (!settings.internbot_enabled && (!canChooseToEnable || !confirmed))"
      color="primary"
      density="compact"
      hide-details
      @update:model-value="settings = { ...settings, internbot_enabled: $event === true }"
    />
  </div>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useChannelStore, type PersonalZaloGroup, type PersonalZaloGroupsResponse } from '../../stores/channels'
import type { PersonalZaloBotSettings } from './personalZaloBotSettings'

const props = defineProps<{
  tenantId: string
  channelId: string
  channelActive: boolean
  masterActive: boolean | undefined
  savedGroupId: string
  savedEnabled: boolean
  disabled?: boolean
}>()
const settings = defineModel<PersonalZaloBotSettings>({ required: true })
const channelStore = useChannelStore()
const { locale } = useI18n()
const groups = ref<PersonalZaloGroup[]>([])
const selectedGroup = ref<PersonalZaloGroup>()
const selectedAvailable = ref<boolean>()
const search = ref('')
const total = ref(0)
const loading = ref(true)
const capturedAt = ref('')
const status = ref<PersonalZaloGroupsResponse['status']>('waiting')
const confirmed = ref(props.savedEnabled)
let request = 0

function groupTitle(group: PersonalZaloGroup) {
  return group.name ? `${group.name} · ${group.id}` : group.id
}

const query = computed(() => {
  const value = search.value.trim()
  return selectedGroup.value && value === groupTitle(selectedGroup.value) ? '' : value
})
const options = computed(() => {
  const items = new Map(groups.value.map(group => [group.id, group]))
  const id = settings.value.supervisor_group_id
  if (id && !items.has(id)) items.set(id, selectedGroup.value?.id === id ? selectedGroup.value : { id, name: '' })
  return [...items.values()].map(group => ({ title: groupTitle(group), value: group.id }))
})
const canChooseToEnable = computed(() =>
  props.channelActive && status.value === 'ready' && selectedAvailable.value === true,
)
const statusKey = computed(() => {
  if (loading.value && (status.value !== 'ready' || (settings.value.supervisor_group_id && selectedAvailable.value === undefined))) return 'loading'
  if (status.value === 'waiting') return 'personal_zalo_internbot_waiting'
  if (status.value === 'unavailable') return 'personal_zalo_internbot_groups_unavailable'
  if (!settings.value.supervisor_group_id) return 'personal_zalo_internbot_no_group_selected'
  if (selectedAvailable.value === false) return 'personal_zalo_internbot_group_unavailable'
  if (!props.channelActive || !props.savedEnabled || props.savedGroupId !== settings.value.supervisor_group_id) return 'personal_zalo_internbot_paused'
  if (props.masterActive === undefined) return 'personal_zalo_internbot_status_unknown'
  if (!props.masterActive) return 'chatbot_master_off_hint'
  return 'personal_zalo_internbot_running'
})

function selectGroup(groupId: string | null) {
  const id = groupId ?? ''
  if (id === settings.value.supervisor_group_id) return
  const group = groups.value.find(item => item.id === id)
  if (id && !group) return
  selectedGroup.value = group
  selectedAvailable.value = group !== undefined
  confirmed.value = false
  settings.value = { ...settings.value, supervisor_group_id: id, internbot_enabled: false }
}

async function loadGroups({ refresh = false, append = false } = {}) {
  const currentRequest = ++request
  const currentQuery = query.value
  loading.value = true
  if (refresh) selectedAvailable.value = undefined
  try {
    const response = await channelStore.fetchPersonalZaloGroups(props.tenantId, props.channelId, {
      q: currentQuery || undefined,
      offset: append ? groups.value.length : 0,
      limit: 50,
      refresh: refresh || undefined,
    })
    if (currentRequest !== request || currentQuery !== query.value) return
    status.value = response.status
    groups.value = response.status === 'ready' ? [...(append ? groups.value : []), ...response.items] : []
    total.value = response.total
    capturedAt.value = response.captured_at ?? ''
    const groupId = settings.value.supervisor_group_id
    if (!groupId || response.status !== 'ready') return

    const fromPage = groups.value.find(group => group.id === groupId)
    if (groupId === props.savedGroupId && response.supervisor_available === false) {
      selectedGroup.value = fromPage
      selectedAvailable.value = false
    } else if (fromPage) {
      selectedGroup.value = fromPage
      selectedAvailable.value = true
    } else if (selectedAvailable.value === undefined) {
      const exact = await channelStore.fetchPersonalZaloGroups(props.tenantId, props.channelId, { group_id: groupId, limit: 1 })
      if (currentRequest !== request || groupId !== settings.value.supervisor_group_id) return
      status.value = exact.status
      const group = exact.items.find(item => item.id === groupId)
      selectedGroup.value = group
      selectedAvailable.value = exact.status === 'ready' && group !== undefined
    }
  } catch {
    if (currentRequest !== request) return
    status.value = 'unavailable'
    groups.value = []
    total.value = 0
    capturedAt.value = ''
  } finally {
    if (currentRequest === request) loading.value = false
  }
}

watch(query, (_value, _previous, onCleanup) => {
  const timer = window.setTimeout(() => { void loadGroups() }, 350)
  onCleanup(() => window.clearTimeout(timer))
})
onMounted(() => { void loadGroups({ refresh: true }) })
onBeforeUnmount(() => { request++ })
</script>
