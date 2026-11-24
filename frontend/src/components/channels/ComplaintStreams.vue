<template>
  <div class="mt-3" data-testid="complaint-streams">
    <div class="text-body-2 font-weight-medium mb-1">{{ $t('complaint_streams_title') }}</div>
    <div class="text-caption text-medium-emphasis mb-2">{{ $t('complaint_streams_hint') }}</div>

    <div v-for="(row, index) in streams" :key="index" class="d-flex flex-wrap ga-2 align-start mb-2">
      <v-text-field
        v-model="row.name"
        :label="$t('complaint_stream_name')"
        density="compact"
        hide-details
        style="min-width: 140px; flex: 1 1 140px"
        :data-testid="`complaint-stream-name-${index}`"
      />
      <v-text-field
        v-model="row.description"
        :label="$t('complaint_stream_description')"
        density="compact"
        hide-details
        style="min-width: 200px; flex: 2 1 200px"
      />
      <v-select
        v-model="row.alert_group_id"
        :items="groupOptions"
        :loading="loadingGroups"
        :label="$t('complaint_stream_group')"
        :placeholder="$t('complaint_stream_group_default')"
        persistent-placeholder
        density="compact"
        clearable
        hide-details
        style="min-width: 180px; flex: 1 1 180px"
      />
      <v-btn
        icon="mdi-delete-outline"
        variant="text"
        size="small"
        :aria-label="$t('complaint_stream_remove')"
        @click="streams.splice(index, 1)"
      />
    </div>

    <div class="d-flex ga-2">
      <v-btn size="small" variant="tonal" prepend-icon="mdi-plus" @click="addStream">
        {{ $t('complaint_stream_add') }}
      </v-btn>
      <v-btn v-if="!streams.length" size="small" variant="text" data-testid="complaint-streams-suggest" @click="addSuggested">
        {{ $t('complaint_stream_suggest') }}
      </v-btn>
    </div>
  </div>
</template>

<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import type { ComplaintStream } from './zaloGroupSettings'

defineProps<{ groupOptions: Array<{ title: string; value: string }>; loadingGroups: boolean }>()
const streams = defineModel<ComplaintStream[]>({ required: true })
const { t } = useI18n()

function addStream() {
  streams.value.push({ key: '', name: '', description: '', alert_group_id: '' })
}

// Suggested starting streams; staff rename or remove them as they like.
function addSuggested() {
  const suggestions: Array<[string, string, string]> = [
    ['bao_hanh', t('complaint_stream_warranty'), t('complaint_stream_warranty_desc')],
    ['khach_hang', t('complaint_stream_customer'), t('complaint_stream_customer_desc')],
    ['san_pham', t('complaint_stream_product'), t('complaint_stream_product_desc')],
    ['marketing', t('complaint_stream_marketing'), t('complaint_stream_marketing_desc')],
  ]
  for (const [key, name, description] of suggestions) {
    streams.value.push({ key, name, description, alert_group_id: '' })
  }
}
</script>
