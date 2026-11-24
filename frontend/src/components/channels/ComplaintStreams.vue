<template>
  <div data-testid="complaint-streams">
    <div class="text-body-2 font-weight-medium mt-3">{{ $t('complaint_streams_title') }}</div>
    <div class="text-caption text-medium-emphasis mb-2">{{ $t('complaint_streams_hint') }}</div>
    <v-sheet v-for="(s, i) in streams" :key="i" class="pa-2 mb-2 rounded border" :data-testid="`complaint-stream-${i}`">
      <div class="d-flex ga-2 align-start">
        <v-text-field
          v-model="s.name"
          :label="$t('complaint_stream_name')"
          :placeholder="$t('complaint_stream_name_placeholder')"
          density="compact"
          hide-details
          maxlength="60"
          class="flex-1-1"
        />
        <v-select
          v-model="s.alert_group_id"
          :items="groupOptions"
          :label="$t('complaint_stream_group')"
          density="compact"
          hide-details
          clearable
          class="flex-1-1"
        />
        <v-btn icon="mdi-delete-outline" variant="text" size="small" :aria-label="$t('delete')" @click="remove(i)" />
      </div>
      <v-text-field
        v-model="s.description"
        :label="$t('complaint_stream_description')"
        :placeholder="$t('complaint_stream_description_placeholder')"
        density="compact"
        hide-details
        maxlength="400"
        class="mt-2"
      />
    </v-sheet>
    <v-btn
      v-if="streams.length < 12"
      variant="tonal"
      size="small"
      prepend-icon="mdi-plus"
      data-testid="complaint-stream-add"
      @click="add"
    >{{ $t('complaint_stream_add') }}</v-btn>
  </div>
</template>

<script setup lang="ts">
import type { ComplaintStream } from './zaloGroupSettings'

defineProps<{ groupOptions: Array<{ title: string; value: string }> }>()
const streams = defineModel<ComplaintStream[]>({ required: true })

function add() {
  streams.value.push({ key: '', name: '', description: '', alert_group_id: '' })
}
function remove(i: number) {
  streams.value.splice(i, 1)
}
</script>
