<template>
  <v-card class="pa-4 mb-4">
    <div class="d-flex align-center mb-2">
      <h2 class="text-subtitle-1 font-weight-bold">
        <v-icon start size="small">mdi-shield-account</v-icon>
        {{ t('personal_zalo_approval_title') }}
      </h2>
      <v-spacer />
      <v-btn icon="mdi-refresh" variant="text" :loading="loading" :aria-label="t('refresh')" @click="loadApprovals" />
    </div>
    <div class="text-body-2 text-medium-emphasis mb-3">{{ t('personal_zalo_approval_hint') }}</div>
    <v-alert v-if="decisionError || loadError" type="error" variant="tonal" class="mb-3">
      {{ decisionError || loadError }}
    </v-alert>
    <v-tabs :model-value="status" density="compact" class="mb-3" @update:model-value="selectStatus">
      <v-tab value="pending">{{ t('personal_zalo_approval_pending') }}</v-tab>
      <v-tab value="approved">{{ t('personal_zalo_approval_approved') }}</v-tab>
      <v-tab value="rejected">{{ t('personal_zalo_approval_rejected') }}</v-tab>
    </v-tabs>
    <v-progress-linear v-if="loading" indeterminate class="mb-2" />
    <v-list v-if="items.length" lines="two" class="pa-0" :aria-busy="loading">
      <v-list-item v-for="item in items" :key="`${item.thread_type}:${item.thread_id}`">
        <template #prepend>
          <v-icon>{{ item.thread_type === 'group' ? 'mdi-account-group' : 'mdi-account' }}</v-icon>
        </template>
        <v-list-item-title>{{ item.display_name || item.thread_id }}</v-list-item-title>
        <v-list-item-subtitle>
          {{ t(item.thread_type === 'group' ? 'personal_zalo_approval_group' : 'personal_zalo_approval_user') }}
          <template v-if="item.display_name"> · {{ item.thread_id }}</template>
          · {{ new Date(item.first_seen_at).toLocaleString(locale) }}
        </v-list-item-subtitle>
        <template #append>
          <div class="d-flex flex-wrap justify-end ga-1">
            <v-btn
              v-if="item.status !== 'approved'" size="small" color="success" variant="tonal"
              :loading="actionKey === decisionKey(item, 'approved')" :disabled="Boolean(actionKey)"
              @click="decide(item, 'approved')"
            >{{ t('personal_zalo_approval_approve') }}</v-btn>
            <v-btn
              v-if="item.status !== 'rejected'" size="small" color="error" variant="text"
              :loading="actionKey === decisionKey(item, 'rejected')" :disabled="Boolean(actionKey)"
              @click="decide(item, 'rejected')"
            >{{ t('personal_zalo_approval_reject') }}</v-btn>
          </div>
        </template>
      </v-list-item>
    </v-list>
    <div v-else-if="!loading && !loadError" class="text-body-2 text-medium-emphasis py-3">
      {{ t('personal_zalo_approval_empty') }}
    </div>
    <div v-if="total > limit" class="d-flex align-center justify-end ga-2 mt-2">
      <v-btn size="small" variant="text" :disabled="offset === 0 || loading" @click="changePage(-limit)">
        {{ t('back') }}
      </v-btn>
      <span class="text-caption">{{ offset + 1 }}–{{ Math.min(offset + limit, total) }} / {{ total }}</span>
      <v-btn size="small" variant="text" :disabled="offset + limit >= total || loading" @click="changePage(limit)">
        {{ t('next') }}
      </v-btn>
    </div>
  </v-card>
</template>

<script setup lang="ts">
import { onUnmounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import api from '../../api'
import { useTenantEventRefresh } from '../../composables/useTenantEvents'

type Decision = 'approved' | 'rejected'
type ApprovalStatus = 'pending' | Decision
interface Approval {
  thread_type: 'user' | 'group'
  thread_id: string
  display_name: string
  status: ApprovalStatus
  first_seen_at: string
}

const props = defineProps<{ tenantId: string; channelId: string }>()
const { t, locale } = useI18n()
const status = ref<ApprovalStatus>('pending')
const items = ref<Approval[]>([])
const total = ref(0)
const offset = ref(0)
const limit = 50
const loading = ref(false)
const loadError = ref('')
const decisionError = ref('')
const actionKey = ref('')
let latestRequest = 0
const endpoint = () => `/tenants/${props.tenantId}/channels/${props.channelId}/personal-zalo/approvals`

function selectStatus(value: unknown) {
  if (value !== 'pending' && value !== 'approved' && value !== 'rejected') return
  if (value === status.value) return
  status.value = value
  offset.value = 0
  items.value = []
  total.value = 0
  decisionError.value = ''
}

function changePage(change: number) {
  offset.value += change
  items.value = []
}

async function loadApprovals() {
  const request = ++latestRequest
  loading.value = true
  loadError.value = ''
  try {
    const { data } = await api.get<{ items: Approval[]; total: number }>(endpoint(), {
      params: { status: status.value, limit, offset: offset.value },
    })
    if (request !== latestRequest) return
    if (offset.value > 0 && offset.value >= data.total) {
      offset.value = Math.floor(Math.max(0, data.total - 1) / limit) * limit
      return
    }
    items.value = data.items
    total.value = data.total
  } catch {
    if (request === latestRequest) loadError.value = t('personal_zalo_approval_load_error')
  } finally {
    if (request === latestRequest) loading.value = false
  }
}

function decisionKey(item: Approval, decision: Decision) {
  return `${item.thread_type}:${item.thread_id}:${decision}`
}

async function decide(item: Approval, decision: Decision) {
  if (actionKey.value || item.status === decision) return
  actionKey.value = decisionKey(item, decision)
  decisionError.value = ''
  try {
    await api.put(`${endpoint()}/${item.thread_type}/${encodeURIComponent(item.thread_id)}`, {
      status: decision, expected_status: item.status,
    })
    await loadApprovals()
  } catch (err) {
    if ((err as { response?: { status?: number } }).response?.status === 409) {
      decisionError.value = t('personal_zalo_approval_stale')
      await loadApprovals()
    } else {
      decisionError.value = t('personal_zalo_approval_update_error')
    }
  } finally {
    actionKey.value = ''
  }
}

watch([status, offset], loadApprovals, { immediate: true })
useTenantEventRefresh(() => props.tenantId, 'personal_zalo_approvals_updated', loadApprovals)
onUnmounted(() => { latestRequest++ })
</script>
