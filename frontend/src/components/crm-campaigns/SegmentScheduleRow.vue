<template>
  <v-card variant="outlined" class="segment-card mb-4">
    <v-card-text class="pa-4">
      <div class="d-flex align-center mb-4">
        <span class="text-subtitle-2 font-weight-bold text-primary">
          {{ $t('campaign_segment') }} {{ index + 1 }}
        </span>
        <v-spacer />
        <v-btn
          icon="mdi-delete-outline"
          size="small"
          variant="text"
          color="error"
          :title="$t('delete')"
          @click="$emit('remove')"
        />
      </div>

      <v-row align="center">
        <v-col cols="12" sm="6">
          <v-select
            v-model="groupId"
            :items="groups"
            item-title="name"
            item-value="id"
            :label="$t('campaign_group_gmf')"
            variant="outlined"
            density="comfortable"
            prepend-inner-icon="mdi-account-group"
            :rules="[v => !!v || $t('campaign_group_required')]"
            hide-details="auto"
          />
        </v-col>
        <v-col cols="12" sm="6">
          <v-radio-group v-model="scheduleKind" inline hide-details>
            <v-radio value="recurring" :label="$t('campaign_schedule_recurring')" />
            <v-radio value="once" :label="$t('campaign_schedule_once')" />
          </v-radio-group>
        </v-col>
      </v-row>

      <!-- Recurring: reuse the shared CronPicker -->
      <div v-if="scheduleKind === 'recurring'" class="mt-4">
        <CronPicker v-model="cron" />
      </div>

      <!-- One-time: date + time -->
      <v-row v-else class="mt-2">
        <v-col cols="12" sm="6">
          <v-text-field
            v-model="onceDate"
            type="date"
            :label="$t('campaign_once_date')"
            variant="outlined"
            density="comfortable"
            class="date-time-field"
            hide-details="auto"
          />
        </v-col>
        <v-col cols="12" sm="6">
          <v-text-field
            v-model="onceTime"
            type="time"
            :label="$t('campaign_once_time')"
            variant="outlined"
            density="comfortable"
            class="date-time-field"
            hide-details="auto"
          />
        </v-col>
      </v-row>

      <!-- Tag (mention) thành viên trong nhóm GMF -->
      <v-divider class="my-4" />
      <div class="d-flex align-center mb-2">
        <v-icon size="16" class="mr-1 text-primary">mdi-at</v-icon>
        <span class="text-caption font-weight-medium">{{ $t('campaign_mention_title') }}</span>
        <v-spacer />
        <v-btn
          v-if="groupId"
          :title="$t('campaign_mention_sync')"
          icon="mdi-refresh"
          size="x-small"
          variant="text"
          :loading="membersLoading"
          @click="loadMembers(true)"
        />
      </div>

      <v-btn-toggle
        v-model="mentionMode"
        mandatory
        divided
        density="comfortable"
        color="primary"
        class="mention-toggle mb-1"
      >
        <v-btn value="none" size="small">{{ $t('campaign_mention_none') }}</v-btn>
        <v-btn value="all" size="small">{{ $t('campaign_mention_all') }}</v-btn>
        <v-btn value="selected" size="small">{{ $t('campaign_mention_selected') }}</v-btn>
      </v-btn-toggle>

      <!-- 'all' mode: show how many members will be tagged -->
      <div v-if="mentionMode === 'all'" class="text-caption text-grey-darken-1 mt-1">
        <template v-if="membersLoading">{{ $t('campaign_mention_loading') }}</template>
        <template v-else-if="!groupId">{{ $t('campaign_mention_pick_group_first') }}</template>
        <template v-else>
          {{ $t('campaign_mention_all_count', { n: memberOptions.length }) }}
          <span v-if="memberOptions.length >= 50"> · {{ $t('campaign_mention_cap_hint') }}</span>
        </template>
      </div>

      <!-- 'selected' mode: pick specific members -->
      <v-autocomplete
        v-if="mentionMode === 'selected'"
        v-model="mentionUserIds"
        :items="memberOptions"
        item-title="name"
        item-value="zaloUserId"
        :label="$t('campaign_mention_select_label')"
        :no-data-text="groupId ? $t('campaign_mention_no_members') : $t('campaign_mention_pick_group_first')"
        :loading="membersLoading"
        :disabled="!groupId"
        multiple
        chips
        closable-chips
        variant="outlined"
        density="comfortable"
        prepend-inner-icon="mdi-account-multiple-check"
        hide-details="auto"
        class="mt-2"
      />
    </v-card-text>
  </v-card>
</template>

<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import CronPicker from '../CronPicker.vue'
import { listGroupMembers } from './campaignsApi'
import type { CampaignSegment, GroupMember, MentionMode, ScheduleKind, SelectOption } from './types'

const props = defineProps<{
  index: number
  groups: SelectOption[]
}>()

defineEmits<{ remove: [] }>()

const model = defineModel<CampaignSegment>({ required: true })

function patch(patchObj: Partial<CampaignSegment>) {
  model.value = { ...model.value, ...patchObj }
}

const groupId = computed({
  get: () => model.value.groupId,
  set: (v) => {
    const name = props.groups.find((g) => g.id === v)?.name ?? ''
    // Switching group: drop any selected member IDs (they are group-scoped).
    patch({ groupId: v, groupName: name, mentionUserIds: [] })
  },
})

// ---- Mention (tag) state -------------------------------------------------
const mentionMode = computed<MentionMode>({
  get: () => model.value.mentionMode ?? 'none',
  set: (v) => patch({ mentionMode: v }),
})

const mentionUserIds = computed<string[]>({
  get: () => model.value.mentionUserIds ?? [],
  set: (v) => patch({ mentionUserIds: v }),
})

const memberOptions = ref<GroupMember[]>([])
const membersLoading = ref(false)
let loadedForGroup = ''

// loadMembers fetches the GMF group's taggable members (employees + customers).
// `force` re-fetches even if already loaded for the current group (the sync button).
async function loadMembers(force = false): Promise<void> {
  const gid = groupId.value
  if (!gid) {
    memberOptions.value = []
    loadedForGroup = ''
    return
  }
  if (!force && loadedForGroup === gid) return
  membersLoading.value = true
  try {
    const { employees, customers } = await listGroupMembers(gid)
    memberOptions.value = [...employees, ...customers]
    loadedForGroup = gid
    // Drop any previously-selected IDs that are no longer group members.
    const valid = new Set(memberOptions.value.map((m) => m.zaloUserId))
    const pruned = mentionUserIds.value.filter((id) => valid.has(id))
    if (pruned.length !== mentionUserIds.value.length) mentionUserIds.value = pruned
  } catch {
    memberOptions.value = []
  } finally {
    membersLoading.value = false
  }
}

// Load members when the group changes or when a mode needing them is active.
watch(
  [groupId, mentionMode],
  ([gid, mode]) => {
    if (gid && (mode === 'selected' || mode === 'all')) loadMembers()
  },
  { immediate: true },
)

const scheduleKind = computed<ScheduleKind>({
  get: () => model.value.scheduleKind,
  set: (v) => patch({ scheduleKind: v }),
})

const cron = computed({
  get: () => model.value.cron || '0 9 * * *',
  set: (v) => patch({ cron: v }),
})

// Split the stored ISO `runAt` into date + time inputs.
const onceDate = computed({
  get: () => model.value.runAt?.split('T')[0] ?? '',
  set: (v) => patch({ runAt: combine(v, onceTime.value) }),
})
const onceTime = computed({
  get: () => {
    const t = model.value.runAt?.split('T')[1]
    return t ? t.slice(0, 5) : '09:00'
  },
  set: (v) => patch({ runAt: combine(onceDate.value, v) }),
})

// Build a stable RFC3339 string that preserves the wall-clock time the user
// typed. The previous `new Date().toISOString()` converted local -> UTC, so a
// "19:00" entry round-tripped back as the UTC-shifted time and the date/year
// "jumped" on every keystroke. We keep the typed date+time verbatim and just
// append the local timezone offset (backend's parseISO requires RFC3339, so a
// bare naive string would fail to parse and silently drop the schedule).
const pad = (n: number) => String(n).padStart(2, '0')

function localOffset(date: string, time: string): string {
  const [y, mo, d] = date.split('-').map(Number)
  const [h, mi] = (time || '09:00').split(':').map(Number)
  const off = -new Date(y, mo - 1, d, h, mi, 0).getTimezoneOffset()
  const sign = off >= 0 ? '+' : '-'
  const abs = Math.abs(off)
  return `${sign}${pad(Math.floor(abs / 60))}:${pad(abs % 60)}`
}

function combine(date: string, time: string): string | undefined {
  if (!date) return undefined
  const t = time || '09:00'
  return `${date}T${t}:00${localOffset(date, t)}`
}
</script>

<style scoped>
/* Let the mention mode toggle wrap instead of overflowing on narrow dialogs. */
.mention-toggle {
  flex-wrap: wrap;
  height: auto;
}

/* Give the native calendar/clock picker icon breathing room from the field
   border so it doesn't look glued to the edge. */
.date-time-field :deep(input[type='date']),
.date-time-field :deep(input[type='time']) {
  padding-right: 4px;
}
.date-time-field :deep(input[type='date']::-webkit-calendar-picker-indicator),
.date-time-field :deep(input[type='time']::-webkit-calendar-picker-indicator) {
  margin-inline-start: 8px;
  margin-inline-end: 2px;
  opacity: 0.65;
  cursor: pointer;
}
.date-time-field :deep(input[type='date']::-webkit-calendar-picker-indicator:hover),
.date-time-field :deep(input[type='time']::-webkit-calendar-picker-indicator:hover) {
  opacity: 1;
}
</style>
