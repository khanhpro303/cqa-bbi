<template>
  <v-alert v-if="channels.length" type="warning" variant="tonal" class="mb-4 elevation-2" border="start">
    <template #prepend>
      <slot name="icon" :count="pendingCount" />
    </template>
    <div class="d-flex align-center justify-space-between flex-wrap ga-3">
      <span class="text-subtitle-2 font-weight-bold">
        {{ t('personal_zalo_approval_notice', { count: pendingCount }) }}
      </span>
      <div class="d-flex flex-wrap ga-2">
        <v-btn
          v-for="channel in channels" :key="channel.channel_id"
          :to="{ name: 'channel-detail', params: { tenantId, channelId: channel.channel_id }, hash: '#personal-zalo-approvals' }"
          color="warning" variant="flat" class="text-none" prepend-icon="mdi-account-check"
        >
          {{ t('personal_zalo_approval_open_channel', { name: channel.name, count: channel.count }) }}
        </v-btn>
      </div>
    </div>
  </v-alert>
</template>

<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import api from '../../api'
import { useTenantEventRefresh } from '../../composables/useTenantEvents'

interface PendingChannel {
  channel_id: string
  name: string
  count: number
}

const props = defineProps<{ tenantId: string }>()
const { t } = useI18n()
const channels = ref<PendingChannel[]>([])
const pendingCount = computed(() => channels.value.reduce((total, channel) => total + channel.count, 0))
let request: AbortController | undefined

async function refreshPending() {
  request?.abort()
  const attempt = new AbortController()
  request = attempt
  try {
    const { data } = await api.get<{ channels: PendingChannel[] }>(
      `/tenants/${props.tenantId}/personal-zalo/approvals/pending`,
      { signal: attempt.signal },
    )
    if (!attempt.signal.aborted) channels.value = data.channels
  } catch (err) {
    const status = (err as { response?: { status?: number } }).response?.status
    if (!attempt.signal.aborted && (status === 401 || status === 403 || status === 404)) {
      channels.value = []
    }
  }
}

onMounted(refreshPending)
useTenantEventRefresh(() => props.tenantId, 'personal_zalo_approvals_updated', refreshPending)
onUnmounted(() => request?.abort())
</script>
