<template>
  <v-card :loading="loading" class="pa-4">
    <CampaignDashboardPanel
      :stats="stats"
      :loading="loading"
      :month="month"
      :month-options="monthOptions"
      @update:month="onMonth"
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
import { useCampaignMonths } from './useCampaignMonths'
import { getStats } from './mockCampaigns'
import type { CampaignStats } from './types'

const { monthOptions, month } = useCampaignMonths()

const loading = ref(false)
const stats = ref<CampaignStats | null>(null)

async function load() {
  loading.value = true
  try {
    stats.value = await getStats(month.value)
  } finally {
    loading.value = false
  }
}

function onMonth(v: string) {
  month.value = v
  load()
}

onMounted(load)
</script>
