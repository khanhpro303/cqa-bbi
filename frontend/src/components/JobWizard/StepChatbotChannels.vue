<template>
  <div>
    <h3 class="text-h6 mb-2">{{ $t('jobs_chatbot_toggle_select_channels') }}</h3>
    <div class="text-body-2 text-grey-darken-1 mb-4">{{ $t('jobs_chatbot_toggle_channels_hint') }}</div>

    <v-switch
      :model-value="allSelected"
      color="primary"
      hide-details
      :label="$t('chatbot_all_oa_channels')"
      class="mb-2"
      @update:model-value="(v: any) => toggleAll(!!v)"
    />

    <div v-if="!oaChannels.length" class="text-center text-grey py-8">
      {{ $t('no_data') }}
    </div>
    <v-list v-else>
      <v-list-item v-for="ch in oaChannels" :key="ch.id">
        <template #prepend>
          <v-checkbox-btn
            :model-value="allSelected || isChecked(ch.id)"
            :disabled="allSelected"
            @update:model-value="(v: any) => toggleOne(ch.id, !!v)"
          />
        </template>
        <v-list-item-title>{{ ch.name }}</v-list-item-title>
        <v-list-item-subtitle>
          <v-chip size="x-small" :color="ch.channel_type === 'zalo_oa' ? 'blue' : 'indigo'" variant="tonal">
            {{ ch.channel_type === 'zalo_oa' ? 'Zalo OA' : 'Facebook' }}
          </v-chip>
        </v-list-item-subtitle>
      </v-list-item>
    </v-list>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted } from 'vue'
import { storeToRefs } from 'pinia'
import { useRoute } from 'vue-router'
import { useChannelStore } from '../../stores/channels'

const form = defineModel<Record<string, any>>('form', { required: true })
const route = useRoute()
const channelStore = useChannelStore()
const { channels } = storeToRefs(channelStore)

// Only webhook-capable OA channels that are active can run the chatbot.
const OA_TYPES = ['zalo_oa', 'facebook']
const oaChannels = computed(() =>
  channels.value.filter((c) => OA_TYPES.includes(c.channel_type) && c.is_active)
)

// The sentinel ['global'] means "all OA channels" (matches the backend fallback).
const allSelected = computed(() => {
  const ids = (form.value.input_channel_ids as string[]) || []
  return ids.length === 1 && ids[0] === 'global'
})

function isChecked(id: string): boolean {
  return ((form.value.input_channel_ids as string[]) || []).includes(id)
}

function toggleAll(on: boolean) {
  form.value.input_channel_ids = on ? ['global'] : []
}

function toggleOne(id: string, on: boolean) {
  const ids = ((form.value.input_channel_ids as string[]) || []).filter((x) => x !== 'global')
  form.value.input_channel_ids = on ? [...ids, id] : ids.filter((x) => x !== id)
}

onMounted(() => {
  channelStore.fetchChannels(route.params.tenantId as string)
})
</script>
