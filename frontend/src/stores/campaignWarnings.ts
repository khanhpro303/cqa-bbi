import { defineStore } from 'pinia'
import { ref, computed } from 'vue'
import api from '../api'
import { useChannelStore } from './channels'
import type { Campaign } from '../components/crm-campaigns/types'

// A campaign that targets a Zalo OA channel which is currently deactivated
// (is_active=false). Such campaigns cannot run — the webhook/automation is off —
// so the app surfaces a global warning bell.
export interface CampaignWarning {
  campaignId: string
  campaignName: string
  channelId: string
  channelName: string
}

export const useCampaignWarningsStore = defineStore('campaignWarnings', () => {
  const warnings = ref<CampaignWarning[]>([])
  const currentTenantId = ref<string>('')
  const count = computed(() => warnings.value.length)

  async function fetchWarnings(tenantId: string): Promise<void> {
    if (!tenantId) return
    currentTenantId.value = tenantId
    const channelStore = useChannelStore()
    try {
      const [campaignsRes] = await Promise.all([
        api.get<Campaign[]>(`/tenants/${tenantId}/crm/campaigns`),
        channelStore.fetchChannels(tenantId),
      ])
      const inactive = new Map(
        channelStore.channels
          .filter((ch) => ch.channel_type === 'zalo_oa' && !ch.is_active)
          .map((ch) => [ch.id, ch.name] as const),
      )
      const campaigns = campaignsRes.data ?? []
      warnings.value = campaigns
        .filter((c) => inactive.has(c.channelId))
        .map((c) => ({
          campaignId: c.id,
          campaignName: c.name,
          channelId: c.channelId,
          channelName: inactive.get(c.channelId) ?? '',
        }))
    } catch {
      warnings.value = []
    }
  }

  async function refresh(): Promise<void> {
    if (currentTenantId.value) await fetchWarnings(currentTenantId.value)
  }

  return { warnings, count, fetchWarnings, refresh }
})
