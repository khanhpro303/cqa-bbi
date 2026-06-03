<template>
  <v-menu v-if="store.count > 0" location="bottom end" :close-on-content-click="false">
    <template #activator="{ props }">
      <v-btn icon variant="text" :size="size" v-bind="props" :title="$t('campaign_warning_bell_title')">
        <v-badge :content="store.count" color="error" floating>
          <v-icon color="warning">mdi-bell-alert</v-icon>
        </v-badge>
      </v-btn>
    </template>

    <v-card min-width="320" max-width="420">
      <v-card-title class="text-body-1 font-weight-bold d-flex align-center py-3">
        <v-icon color="warning" class="mr-2" size="20">mdi-bell-alert</v-icon>
        {{ $t('campaign_warning_bell_title') }}
      </v-card-title>
      <v-divider />
      <v-list density="compact" lines="two">
        <v-list-item
          v-for="w in store.warnings"
          :key="w.campaignId"
          @click="goToCampaign(w.campaignId)"
        >
          <v-list-item-title class="font-weight-medium">{{ w.campaignName }}</v-list-item-title>
          <v-list-item-subtitle>
            {{ $t('campaign_warning_channel_off', { channel: w.channelName }) }}
          </v-list-item-subtitle>
          <template #append>
            <v-icon size="18" color="grey">mdi-chevron-right</v-icon>
          </template>
        </v-list-item>
      </v-list>
    </v-card>
  </v-menu>
</template>

<script setup lang="ts">
import { useRoute, useRouter } from 'vue-router'
import { useCampaignWarningsStore } from '../stores/campaignWarnings'

withDefaults(defineProps<{ size?: string }>(), { size: 'small' })

const route = useRoute()
const router = useRouter()
const store = useCampaignWarningsStore()

function goToCampaign(_campaignId: string) {
  const tenantId = route.params.tenantId as string
  router.push({ path: `/${tenantId}/crm`, query: { tab: 'campaigns' } })
}
</script>
