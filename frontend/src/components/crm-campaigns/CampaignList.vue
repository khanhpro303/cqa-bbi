<template>
  <div>
    <v-card :loading="loading">
      <v-table density="comfortable">
        <thead>
          <tr>
            <th>{{ $t('campaign_name') }}</th>
            <th>{{ $t('status') }}</th>
            <th>{{ $t('campaign_group_gmf') }}</th>
            <th>{{ $t('campaign_next_run') }}</th>
            <th class="text-right">{{ $t('campaign_sent_month') }}</th>
            <th>{{ $t('actions') }}</th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="c in campaigns" :key="c.id" class="campaign-row" @click="openDashboard(c)">
            <td class="py-3">
              <div class="font-weight-bold text-primary">{{ c.name }}</div>
              <div class="text-caption text-grey-darken-1">{{ c.description || '—' }}</div>
            </td>
            <td>
              <v-chip :color="statusColor(c.status)" size="small" variant="flat" class="font-weight-bold">
                <v-icon start size="12">{{ statusIcon(c.status) }}</v-icon>
                {{ $t('campaign_status_' + c.status) }}
              </v-chip>
            </td>
            <td>
              <v-chip size="small" color="teal" variant="tonal">
                <v-icon start size="14">mdi-account-group</v-icon>
                {{ c.segments.length }} {{ $t('campaign_groups_unit') }}
              </v-chip>
            </td>
            <td class="text-body-2">{{ nextRunLabel(c) }}</td>
            <td class="text-right font-weight-medium">{{ c.sentThisMonth.toLocaleString() }}</td>
            <td class="py-3">
              <v-btn
                icon="mdi-send"
                size="small"
                variant="text"
                color="success"
                :loading="busyId === c.id"
                :title="$t('campaign_send_now')"
                @click.stop="onSendNow(c)"
              />
              <v-btn
                v-if="c.status === 'active'"
                icon="mdi-pause"
                size="small"
                variant="text"
                color="warning"
                :title="$t('campaign_pause')"
                @click.stop="onToggle(c, 'paused')"
              />
              <v-btn
                v-else-if="c.status === 'paused' || c.status === 'draft'"
                icon="mdi-play"
                size="small"
                variant="text"
                color="success"
                :title="$t('campaign_activate')"
                @click.stop="onToggle(c, 'active')"
              />
              <v-btn
                icon="mdi-pencil"
                size="small"
                variant="text"
                color="blue"
                :title="$t('edit')"
                @click.stop="openEdit(c)"
              />
              <v-btn
                icon="mdi-delete"
                size="small"
                variant="text"
                color="error"
                :title="$t('delete')"
                @click.stop="confirmId = c.id"
              />
            </td>
          </tr>
          <tr v-if="campaigns.length === 0 && !loading">
            <td colspan="6" class="text-center py-8 text-grey text-body-2">
              <v-icon size="40" color="grey-lighten-1" class="mb-2">mdi-bullhorn-outline</v-icon>
              <div>{{ $t('campaign_empty') }}</div>
            </td>
          </tr>
        </tbody>
      </v-table>
    </v-card>

    <!-- Create / edit dialog -->
    <CampaignFormDialog
      v-model="formOpen"
      :groups="groups"
      :channels="channels"
      :editing="editing"
      @saved="onSaved"
      @error="(key) => $emit('notify', $t(key), 'error')"
    />

    <!-- Per-campaign dashboard (row drill-down) -->
    <CampaignDashboardModal v-model="dashboardOpen" :campaign="dashboardCampaign" />

    <!-- Delete confirm -->
    <v-dialog v-model="confirmOpen" max-width="420">
      <v-card>
        <v-card-title class="text-body-1 font-weight-bold">{{ $t('campaign_delete_title') }}</v-card-title>
        <v-card-text class="text-body-2">{{ $t('campaign_delete_confirm') }}</v-card-text>
        <v-card-actions>
          <v-spacer />
          <v-btn variant="text" @click="confirmId = null">{{ $t('cancel') }}</v-btn>
          <v-btn color="error" variant="tonal" @click="onDelete">{{ $t('delete') }}</v-btn>
        </v-card-actions>
      </v-card>
    </v-dialog>
  </div>
</template>

<script setup lang="ts">
import { ref, computed, onMounted } from 'vue'
import CampaignFormDialog from './CampaignFormDialog.vue'
import CampaignDashboardModal from './CampaignDashboardModal.vue'
import {
  listCampaigns,
  sendNow,
  setCampaignStatus,
  deleteCampaign,
  MOCK_GMF_GROUPS,
  MOCK_OA_CHANNELS,
} from './mockCampaigns'
import type { Campaign, CampaignStatus } from './types'

const emit = defineEmits<{ notify: [text: string, color: string] }>()

const groups = MOCK_GMF_GROUPS
const channels = MOCK_OA_CHANNELS

const campaigns = ref<Campaign[]>([])
const loading = ref(false)
const busyId = ref<string | null>(null)

const formOpen = ref(false)
const editing = ref<Campaign | null>(null)

const dashboardOpen = ref(false)
const dashboardCampaign = ref<Campaign | null>(null)

function openDashboard(c: Campaign) {
  dashboardCampaign.value = c
  dashboardOpen.value = true
}

const confirmId = ref<string | null>(null)
const confirmOpen = computed({
  get: () => confirmId.value !== null,
  set: (v) => { if (!v) confirmId.value = null },
})

async function load() {
  loading.value = true
  try {
    campaigns.value = await listCampaigns()
  } catch {
    emit('notify', 'Lỗi tải chiến dịch', 'error')
  } finally {
    loading.value = false
  }
}

function openCreate() {
  editing.value = null
  formOpen.value = true
}

function openEdit(c: Campaign) {
  editing.value = c
  formOpen.value = true
}

async function onSaved() {
  await load()
  emit('notify', 'Đã lưu chiến dịch', 'success')
}

async function onSendNow(c: Campaign) {
  busyId.value = c.id
  try {
    const { sent } = await sendNow(c.id)
    await load()
    emit('notify', `Đã gửi ${sent.toLocaleString()} tin`, 'success')
  } catch {
    emit('notify', 'Gửi thất bại', 'error')
  } finally {
    busyId.value = null
  }
}

async function onToggle(c: Campaign, status: CampaignStatus) {
  await setCampaignStatus(c.id, status)
  await load()
}

async function onDelete() {
  if (!confirmId.value) return
  await deleteCampaign(confirmId.value)
  confirmId.value = null
  await load()
  emit('notify', 'Đã xóa chiến dịch', 'success')
}

// --- helpers ---
function statusColor(s: CampaignStatus): string {
  return { draft: 'grey', active: 'success', paused: 'warning', done: 'blue-grey' }[s]
}
function statusIcon(s: CampaignStatus): string {
  return {
    draft: 'mdi-file-outline',
    active: 'mdi-play-circle',
    paused: 'mdi-pause-circle',
    done: 'mdi-check-circle',
  }[s]
}
function nextRunLabel(c: Campaign): string {
  const upcoming = c.segments
    .map((s) => s.nextRunAt)
    .filter((d): d is string => !!d)
    .sort()
  if (upcoming.length === 0) return '—'
  const next = c.segments.find((s) => s.nextRunAt === upcoming[0])
  return `${formatDateTime(upcoming[0])} · ${next?.groupName ?? ''}`
}
function formatDateTime(iso?: string): string {
  if (!iso) return '—'
  const d = new Date(iso)
  const pad = (n: number) => String(n).padStart(2, '0')
  return `${pad(d.getDate())}/${pad(d.getMonth() + 1)} ${pad(d.getHours())}:${pad(d.getMinutes())}`
}

onMounted(load)
defineExpose({ openCreate, reload: load })
</script>

<style scoped>
.campaign-row {
  cursor: pointer;
  transition: background-color 0.15s ease;
}
.campaign-row:hover {
  background-color: rgba(0, 0, 0, 0.035);
}
</style>
