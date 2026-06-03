import { defineStore } from 'pinia'
import { ref } from 'vue'
import api from '../api'

export interface Channel {
  id: string
  tenant_id: string
  channel_type: string
  name: string
  external_id: string
  is_active: boolean
  auto_reply_enabled: boolean
  metadata: string
  last_sync_at: string | null
  last_sync_status: string
  last_sync_error?: string
  conversation_count: number
  import_endpoint?: string
  import_endpoint_internal?: string
  import_secret?: string
  import_secret_masked?: string
  account_owners?: PersonalZaloAccountOwner[]
  created_at: string
}

export interface PersonalZaloAccountOwner {
  account_external_id: string
  user_id: string
  user_name?: string
  user_email?: string
}

export interface PersonalZaloGatewayAccount {
  id: string
  account_external_id?: string
  status: 'connected' | 'disconnected' | 'qr_pending' | 'connecting'
  display_name?: string
  avatar_url?: string
  zalo_uid?: string
  qr_image?: string
  qr_generated_at?: string
  last_error?: string
  last_imported_at?: string
  created_at?: string
  updated_at?: string
}

export interface PersonalZaloGatewayState {
  gateway_configured: boolean
  gateway_reachable: boolean
  account_exists: boolean
  next_action?: string
  message?: string
  account?: PersonalZaloGatewayAccount
}

export const useChannelStore = defineStore('channels', () => {
  const channels = ref<Channel[]>([])
  const accountOwners = ref<PersonalZaloAccountOwner[]>([])

  async function fetchChannels(tenantId: string) {
    const { data } = await api.get(`/tenants/${tenantId}/channels`)
    channels.value = data
  }

  async function createChannel(tenantId: string, payload: Record<string, unknown>) {
    const { data } = await api.post(`/tenants/${tenantId}/channels`, payload)
    channels.value.unshift(data)
    return data
  }

  async function updateChannel(tenantId: string, channelId: string, payload: Record<string, unknown>) {
    await api.put(`/tenants/${tenantId}/channels/${channelId}`, payload)
    const idx = channels.value.findIndex((c) => c.id === channelId)
    if (idx >= 0) Object.assign(channels.value[idx], payload)
  }

  async function deleteChannel(tenantId: string, channelId: string) {
    await api.delete(`/tenants/${tenantId}/channels/${channelId}`)
    channels.value = channels.value.filter((c) => c.id !== channelId)
  }

  async function testConnection(tenantId: string, channelId: string) {
    const { data } = await api.post(`/tenants/${tenantId}/channels/${channelId}/test`)
    return data
  }

  async function syncChannel(tenantId: string, channelId: string) {
    const { data } = await api.post(`/tenants/${tenantId}/channels/${channelId}/sync`)
    return data
  }

  const currentChannel = ref<Channel | null>(null)
  const syncHistory = ref<any[]>([])
  const syncHistoryTotal = ref(0)

  async function fetchChannel(tenantId: string, channelId: string) {
    const { data } = await api.get(`/tenants/${tenantId}/channels/${channelId}`)
    currentChannel.value = data
    return data
  }

  async function fetchSyncHistory(tenantId: string, channelId: string, page = 1, status = '') {
    const params: Record<string, any> = { page, per_page: 10 }
    if (status) params.status = status
    const { data } = await api.get(`/tenants/${tenantId}/channels/${channelId}/sync-history`, { params })
    syncHistory.value = data.data || []
    syncHistoryTotal.value = data.total || 0
    return data
  }

  async function purgeConversations(tenantId: string, channelId: string) {
    const { data } = await api.delete(`/tenants/${tenantId}/channels/${channelId}/conversations`)
    return data
  }

  async function fetchAccountOwners(tenantId: string, channelId: string) {
    const { data } = await api.get(`/tenants/${tenantId}/channels/${channelId}/account-owners`)
    accountOwners.value = data.account_owners || []
    return accountOwners.value
  }

  async function updateAccountOwners(tenantId: string, channelId: string, owners: PersonalZaloAccountOwner[]) {
    const { data } = await api.put(`/tenants/${tenantId}/channels/${channelId}/account-owners`, { account_owners: owners })
    accountOwners.value = data.account_owners || []
    if (currentChannel.value) {
      currentChannel.value.account_owners = accountOwners.value
    }
    return accountOwners.value
  }

  async function fetchPersonalZaloGatewayState(tenantId: string, channelId: string): Promise<PersonalZaloGatewayState> {
    const { data } = await api.get(`/tenants/${tenantId}/channels/${channelId}/personal-zalo-gateway`)
    return data
  }

  async function connectPersonalZaloGateway(tenantId: string, channelId: string): Promise<PersonalZaloGatewayState> {
    const { data } = await api.post(`/tenants/${tenantId}/channels/${channelId}/personal-zalo-gateway/connect`)
    return data
  }

  async function reconnectPersonalZaloGateway(tenantId: string, channelId: string): Promise<PersonalZaloGatewayState> {
    const { data } = await api.post(`/tenants/${tenantId}/channels/${channelId}/personal-zalo-gateway/reconnect`)
    return data
  }

  async function syncPersonalZaloGateway(tenantId: string, channelId: string): Promise<PersonalZaloGatewayState> {
    const { data } = await api.post(`/tenants/${tenantId}/channels/${channelId}/personal-zalo-gateway/sync`)
    return data.state || data
  }

  return {
    channels,
    currentChannel,
    syncHistory,
    syncHistoryTotal,
    accountOwners,
    fetchChannels,
    fetchChannel,
    createChannel,
    updateChannel,
    deleteChannel,
    testConnection,
    syncChannel,
    fetchSyncHistory,
    purgeConversations,
    fetchAccountOwners,
    updateAccountOwners,
    fetchPersonalZaloGatewayState,
    connectPersonalZaloGateway,
    reconnectPersonalZaloGateway,
    syncPersonalZaloGateway,
  }
})
