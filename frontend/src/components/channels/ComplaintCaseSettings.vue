<template>
  <div data-testid="complaint-case-settings">
    <v-alert type="info" variant="tonal" density="compact" class="mb-4">
      {{ $t(channelType === 'personal_zalo_import' ? 'personal_zalo_complaint_settings_desc' : 'zalo_oa_complaint_settings_desc') }}
    </v-alert>
    <v-switch
      v-model="settings.complaint_cases"
      :label="$t('complaint_cases_switch')"
      :hint="$t(channelType === 'personal_zalo_import' ? 'personal_zalo_complaint_cases_switch_hint' : 'complaint_cases_switch_hint')"
      persistent-hint
      density="compact"
      color="primary"
      :data-testid="channelType === 'personal_zalo_import' ? 'personal-zalo-complaint-cases' : 'complaint-cases'"
    />
    <template v-if="settings.complaint_cases">
      <v-text-field
        v-model.number="settings.complaint_resolved_days"
        type="number"
        min="1"
        :max="MAX_COMPLAINT_RESOLVED_DAYS"
        :label="$t('complaint_resolved_days')"
        :hint="$t('complaint_resolved_days_hint')"
        persistent-hint
        density="compact"
        class="mt-3"
        style="max-width: 320px"
        :data-testid="channelType === 'personal_zalo_import' ? 'personal-zalo-complaint-resolved-days' : 'complaint-resolved-days'"
      />
      <section v-if="channelType === 'personal_zalo_import'" class="mt-5" data-testid="personal-zalo-complaint-group-customers">
        <div class="text-subtitle-1 font-weight-medium">{{ $t('personal_zalo_complaint_group_customers_title') }}</div>
        <p class="text-body-2 text-medium-emphasis mb-3">
          {{ $t('personal_zalo_complaint_group_customers_hint') }}
        </p>
        <v-alert v-if="conversationLoadError" type="error" variant="tonal" density="compact" class="mb-3">
          {{ $t('personal_zalo_complaint_group_load_error') }}
        </v-alert>
        <v-alert v-if="!customerMappingsValid" type="error" variant="tonal" density="compact" class="mb-3">
          {{ $t('personal_zalo_complaint_mapping_incomplete') }}
        </v-alert>
        <div v-for="(row, index) in customerRows" :key="row.key" class="d-flex flex-wrap ga-2 align-start mb-2">
          <v-autocomplete
            v-model="row.conversationId"
            :items="conversationItemsFor(index)"
            :label="$t('personal_zalo_complaint_source_group')"
            :no-data-text="$t('personal_zalo_complaint_no_source_groups')"
            :loading="loadingConversations"
            density="compact"
            variant="outlined"
            clearable
            no-filter
            class="complaint-customer-field"
            :data-testid="`personal-zalo-complaint-group-${index}`"
            @update:model-value="syncCustomerMappings"
            @update:search="searchConversations"
          />
          <v-text-field
            v-model="row.customerCode"
            :label="$t('personal_zalo_complaint_customer_code')"
            :hint="$t('personal_zalo_complaint_customer_code_hint')"
            persistent-hint
            density="compact"
            variant="outlined"
            maxlength="50"
            class="complaint-customer-field"
            :data-testid="`personal-zalo-complaint-customer-code-${index}`"
            @update:model-value="syncCustomerMappings"
          />
          <v-btn
            icon="mdi-delete-outline"
            variant="text"
            color="error"
            :aria-label="$t('personal_zalo_complaint_remove_mapping')"
            @click="removeCustomerRow(index)"
          />
        </div>
        <v-btn
          variant="tonal"
          prepend-icon="mdi-plus"
          data-testid="personal-zalo-complaint-add-mapping"
          @click="addCustomerRow"
        >
          {{ $t('personal_zalo_complaint_add_mapping') }}
        </v-btn>
      </section>
      <ComplaintStreams v-model="settings.complaint_streams" :group-options="groupOptions" />
    </template>
  </div>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import api from '../../api'
import type { Conversation } from '../../stores/conversations'
import ComplaintStreams from './ComplaintStreams.vue'
import { personalZaloHandoverGroupOptions, type PersonalZaloHandoverGroup } from './personalZaloGroupSettings'
import { MAX_COMPLAINT_RESOLVED_DAYS, type ComplaintStream } from './zaloGroupSettings'

const props = defineProps<{
  tenantId: string
  channelId: string
  channelType: 'zalo_oa' | 'personal_zalo_import'
}>()
const emit = defineEmits<{ validity: [valid: boolean] }>()
const settings = defineModel<{
  complaint_cases: boolean
  complaint_resolved_days: number
  complaint_streams: ComplaintStream[]
  complaint_group_customers?: Record<string, string>
}>({ required: true })
const groupOptions = ref<Array<{ title: string; value: string }>>([])
type CustomerRow = { key: number; conversationId: string | null; customerCode: string }
type ConversationItem = { title: string; value: string }
const knownConversations = ref<Record<string, ConversationItem>>({})
const loadingConversations = ref(false)
const conversationLoadError = ref(false)
let nextCustomerRowKey = 0
let searchTimer: ReturnType<typeof setTimeout> | undefined
let searchController: AbortController | undefined
const customerRows = ref<CustomerRow[]>(Object.entries(settings.value.complaint_group_customers || {}).map(([conversationId, customerCode]) => ({
  key: nextCustomerRowKey++,
  conversationId,
  customerCode,
})))
const customerMappingsValid = computed(() => customerRows.value.every((row) => {
  const hasConversation = Boolean(row.conversationId?.trim())
  const hasCustomerCode = Boolean(row.customerCode.trim())
  return hasConversation === hasCustomerCode
}))
const conversationItems = computed(() => {
  const items = { ...knownConversations.value }
  customerRows.value.forEach((row) => {
    if (row.conversationId && !items[row.conversationId]) {
      items[row.conversationId] = { title: row.conversationId, value: row.conversationId }
    }
  })
  return Object.values(items)
})

function conversationItemsFor(rowIndex: number) {
  const selectedElsewhere = new Set(customerRows.value
    .filter((_, index) => index !== rowIndex)
    .map((row) => row.conversationId)
    .filter(Boolean))
  return conversationItems.value.filter((item) => !selectedElsewhere.has(item.value))
}

function syncCustomerMappings() {
  settings.value.complaint_group_customers = Object.fromEntries(customerRows.value.flatMap((row) => {
    const conversationID = row.conversationId?.trim() || ''
    const customerCode = row.customerCode.trim()
    return conversationID ? [[conversationID, customerCode]] : []
  }))
  emit('validity', customerMappingsValid.value)
}

function addCustomerRow() {
  customerRows.value.push({ key: nextCustomerRowKey++, conversationId: '', customerCode: '' })
}

function removeCustomerRow(index: number) {
  customerRows.value.splice(index, 1)
  syncCustomerMappings()
}

async function loadConversations(search = '') {
  searchController?.abort()
  const controller = new AbortController()
  searchController = controller
  loadingConversations.value = true
  conversationLoadError.value = false
  try {
    const { data } = await api.get<{ data?: Conversation[] }>(`/tenants/${props.tenantId}/conversations`, {
      params: {
        channel_id: props.channelId,
        thread_type: 'group',
        search: search.trim() || undefined,
        per_page: 30,
      },
      signal: controller.signal,
    })
    if (controller.signal.aborted) return
    const conversations = Array.isArray(data?.data) ? data.data : []
    knownConversations.value = {
      ...knownConversations.value,
      ...Object.fromEntries(conversations.map((conversation) => [conversation.id, {
        title: conversation.customer_name ? `${conversation.customer_name} · ${conversation.id}` : conversation.id,
        value: conversation.id,
      }])),
    }
  } catch {
    if (!controller.signal.aborted) conversationLoadError.value = true
  } finally {
    if (!controller.signal.aborted) loadingConversations.value = false
  }
}

function searchConversations(search: string | null) {
  clearTimeout(searchTimer)
  searchTimer = setTimeout(() => loadConversations(search || ''), search ? 250 : 0)
}

onMounted(async () => {
  if (props.channelType === 'personal_zalo_import') void loadConversations()
  try {
    const { data } = await api.get(`/tenants/${props.tenantId}/crm/groups`)
    const groups = Array.isArray(data) ? data : []
    groupOptions.value = props.channelType === 'personal_zalo_import'
      ? personalZaloHandoverGroupOptions(groups as PersonalZaloHandoverGroup[])
      : groups
        .filter((group) => group.zalo_group_id && (!group.channel_id || group.channel_id === props.channelId))
        .map((group) => ({ title: group.name, value: group.id }))
  } catch {
    groupOptions.value = []
  }
})

onBeforeUnmount(() => {
  clearTimeout(searchTimer)
  searchController?.abort()
})
</script>

<style scoped>
.complaint-customer-field {
  flex: 1 1 280px;
}
</style>
