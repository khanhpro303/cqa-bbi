<template>
  <v-dialog v-model="open" max-width="960" scrollable>
    <v-card :loading="loading">
      <v-card-title class="d-flex align-center py-3">
        <v-icon color="primary" class="mr-2">mdi-chart-box-outline</v-icon>
        <span class="text-truncate">
          {{ $t('dash_campaign_scope') }}<template v-if="campaign">: {{ campaign.name }}</template>
        </span>
        <v-spacer />
        <v-btn icon="mdi-close" variant="text" size="small" class="ml-2" @click="open = false" />
      </v-card-title>
      <v-divider />

      <v-card-text style="max-height: 74vh">
        <CampaignDashboardPanel
          :stats="stats"
          :loading="loading"
          :date-from="dateFrom"
          :date-to="dateTo"
          :date-preset="datePreset"
          :presets="presets"
          :first-card-label="$t('dash_segments')"
          @preset="onPreset"
          @update:date-from="onFrom"
          @update:date-to="onTo"
        />
      </v-card-text>
    </v-card>
  </v-dialog>
</template>

<script setup lang="ts">
import { ref, watch } from 'vue'
import CampaignDashboardPanel from './CampaignDashboardPanel.vue'
import { useCampaignDateRange } from './useCampaignDateRange'
import { getStats } from './campaignsApi'
import type { CampaignStats } from './types'

const props = defineProps<{
  modelValue: boolean
  campaign: { id: string; name: string } | null
}>()
const emit = defineEmits<{ 'update:modelValue': [boolean] }>()

const { dateFrom, dateTo, datePreset, presets, applyPreset, setFrom, setTo } = useCampaignDateRange()

const open = ref(props.modelValue)
watch(() => props.modelValue, (v) => { open.value = v; if (v) load() })
watch(open, (v) => emit('update:modelValue', v))

const loading = ref(false)
const stats = ref<CampaignStats | null>(null)

async function load() {
  if (!props.campaign) return
  loading.value = true
  try {
    stats.value = await getStats(dateFrom.value, dateTo.value, props.campaign.id)
  } finally {
    loading.value = false
  }
}

function onPreset(v: string) {
  applyPreset(v)
  load()
}
function onFrom(v: string) {
  setFrom(v)
  load()
}
function onTo(v: string) {
  setTo(v)
  load()
}
</script>
