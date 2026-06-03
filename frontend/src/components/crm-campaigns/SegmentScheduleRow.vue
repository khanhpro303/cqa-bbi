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
    </v-card-text>
  </v-card>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import CronPicker from '../CronPicker.vue'
import type { CampaignSegment, ScheduleKind, SelectOption } from './types'

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
    patch({ groupId: v, groupName: name })
  },
})

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
