<template>
  <v-card variant="outlined" class="mb-3">
    <v-card-text class="pb-2">
      <div class="d-flex align-center mb-2">
        <span class="text-body-2 font-weight-bold text-primary">
          {{ $t('campaign_segment') }} {{ index + 1 }}
        </span>
        <v-spacer />
        <v-btn
          icon="mdi-delete-outline"
          size="x-small"
          variant="text"
          color="error"
          :title="$t('delete')"
          @click="$emit('remove')"
        />
      </div>

      <v-row dense align="center">
        <v-col cols="12" sm="6">
          <v-select
            v-model="groupId"
            :items="groups"
            item-title="name"
            item-value="id"
            :label="$t('campaign_group_gmf')"
            variant="outlined"
            density="compact"
            prepend-inner-icon="mdi-account-group"
            :rules="[v => !!v || $t('campaign_group_required')]"
            hide-details="auto"
          />
        </v-col>
        <v-col cols="12" sm="6">
          <v-radio-group v-model="scheduleKind" inline density="compact" hide-details>
            <v-radio value="recurring" :label="$t('campaign_schedule_recurring')" />
            <v-radio value="once" :label="$t('campaign_schedule_once')" />
          </v-radio-group>
        </v-col>
      </v-row>

      <!-- Recurring: reuse the shared CronPicker -->
      <div v-if="scheduleKind === 'recurring'" class="mt-1">
        <CronPicker v-model="cron" />
      </div>

      <!-- One-time: date + time -->
      <v-row v-else dense class="mt-1">
        <v-col cols="6">
          <v-text-field
            v-model="onceDate"
            type="date"
            :label="$t('campaign_once_date')"
            variant="outlined"
            density="compact"
            hide-details="auto"
          />
        </v-col>
        <v-col cols="6">
          <v-text-field
            v-model="onceTime"
            type="time"
            :label="$t('campaign_once_time')"
            variant="outlined"
            density="compact"
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

function combine(date: string, time: string): string | undefined {
  if (!date) return undefined
  return new Date(`${date}T${time || '09:00'}:00`).toISOString()
}
</script>
