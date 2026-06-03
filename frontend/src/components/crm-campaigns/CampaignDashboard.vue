<template>
  <v-card :loading="loading" class="pa-4">
    <CampaignDashboardPanel
      :stats="stats"
      :loading="loading"
      :date-from="dateFrom"
      :date-to="dateTo"
      :date-preset="datePreset"
      :presets="presets"
      @preset="onPreset"
      @update:date-from="onFrom"
      @update:date-to="onTo"
    >
      <template #title>
        <div class="d-flex align-center">
          <v-icon color="primary" class="mr-2">mdi-chart-box-outline</v-icon>
          <span class="text-subtitle-1 font-weight-bold">{{ $t('dash_overview') }}</span>
        </div>
      </template>
    </CampaignDashboardPanel>
  </v-card>
</template>

<script setup lang="ts">
import { ref, onMounted } from 'vue'
import CampaignDashboardPanel from './CampaignDashboardPanel.vue'
import { useCampaignDateRange } from './useCampaignDateRange'
import { getStats } from './campaignsApi'
import type { CampaignStats } from './types'

const { dateFrom, dateTo, datePreset, presets, applyPreset, setFrom, setTo } = useCampaignDateRange()

const loading = ref(false)
const stats = ref<CampaignStats | null>(null)

async function load() {
  loading.value = true
  try {
    stats.value = await getStats(dateFrom.value, dateTo.value)
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

onMounted(load)
</script>
