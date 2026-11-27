import { defineStore } from 'pinia'
import { ref } from 'vue'
import api from '../api'

export interface Conversation {
  id: string
  channel_id: string
  channel_name: string
  channel_type: string
  customer_name: string
  last_message_at: string | null
  message_count: number
  thread_type?: string
  account_external_id?: string
  owner_user_id?: string
  owner_name?: string
  owner_email?: string
  created_at: string
}

export interface Message {
  id: string
  sender_type: string
  sender_name: string
  content: string
  content_type: string
  attachments: string
  sent_at: string
}

export const useConversationStore = defineStore('conversations', () => {
  const conversations = ref<Conversation[]>([])
  const messages = ref<Message[]>([])
  const total = ref(0)
  const currentConversation = ref<{ id: string; channel_id?: string; customer_name: string; message_count: number } | null>(null)

  async function fetchConversations(tenantId: string, params: Record<string, string | number> = {}) {
    const { data } = await api.get(`/tenants/${tenantId}/conversations`, { params })
    conversations.value = data.data
    total.value = data.total
  }

  async function fetchMessages(tenantId: string, conversationId: string) {
    const { data } = await api.get(`/tenants/${tenantId}/conversations/${conversationId}/messages`)
    messages.value = data.messages
    currentConversation.value = data.conversation
  }

  return { conversations, messages, total, currentConversation, fetchConversations, fetchMessages }
})
