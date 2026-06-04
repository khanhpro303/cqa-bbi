<template>
  <div>
    <v-alert
      v-if="affectedByInactiveChannel.length > 0"
      type="warning"
      variant="tonal"
      density="comfortable"
      class="mb-3"
      icon="mdi-bell-alert"
    >
      {{ $t('campaign_channel_inactive_warning', { count: affectedByInactiveChannel.length }) }}
    </v-alert>

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

    <!-- Send-before-scheduled-time warning (chỉ với lượt gửi 1 lần chưa tới hẹn) -->
    <v-dialog v-model="sendConfirmOpen" max-width="460">
      <v-card>
        <v-card-title class="text-subtitle-1 font-weight-bold px-6 pt-5">
          {{ $t('campaign_send_early_title') }}
        </v-card-title>
        <v-card-text class="px-6 pb-0">
          <v-alert type="warning" variant="tonal" density="comfortable" class="mb-3">
            {{ $t('campaign_send_early_warning', { time: sendConfirmInfo?.time ?? '', group: sendConfirmInfo?.groupName ?? '' }) }}
          </v-alert>
          <div class="text-body-2 text-grey-darken-1">{{ $t('campaign_send_early_note') }}</div>
        </v-card-text>
        <v-card-actions class="px-6 pb-5 pt-3 ga-2">
          <v-spacer />
          <v-btn variant="text" :disabled="busyId === sendConfirm?.id" @click="sendConfirm = null">
            {{ $t('cancel') }}
          </v-btn>
          <v-btn color="success" variant="flat" :loading="busyId === sendConfirm?.id" @click="confirmSendNow">
            {{ $t('campaign_send_now') }}
          </v-btn>
        </v-card-actions>
      </v-card>
    </v-dialog>
  </div>
</template>

<script setup lang="ts">
import { ref, computed, onMounted } from 'vue'
import { useRoute } from 'vue-router'
import { useI18n } from 'vue-i18n'
import CampaignFormDialog from './CampaignFormDialog.vue'
import CampaignDashboardModal from './CampaignDashboardModal.vue'
import {
  listCampaigns,
  sendNow,
  setCampaignStatus,
  deleteCampaign,
} from './campaignsApi'
import api from '../../api'
import { useChannelStore } from '../../stores/channels'
import { useCampaignWarningsStore } from '../../stores/campaignWarnings'
import type { Campaign, CampaignStatus, SelectOption } from './types'

const emit = defineEmits<{ notify: [text: string, color: string]; changed: [] }>()

const route = useRoute()
const { t } = useI18n()
const tenantId = computed(() => route.params.tenantId as string)
const channelStore = useChannelStore()
const warningsStore = useCampaignWarningsStore()

// Real reference data — GMF groups and Zalo OA channels (all of them, including
// deactivated ones, so the user can still see/pick them and get warned).
const groups = ref<SelectOption[]>([])
const channels = ref<SelectOption[]>([])
const inactiveChannelIds = ref<Set<string>>(new Set())

async function loadReferenceData() {
  try {
    const { data } = await api.get<{ id: string; name: string }[]>(`/tenants/${tenantId.value}/crm/groups`)
    groups.value = (data ?? []).map((g) => ({ id: g.id, name: g.name }))
  } catch {
    groups.value = []
  }
  try {
    await channelStore.fetchChannels(tenantId.value)
    const oa = channelStore.channels.filter((ch) => ch.channel_type === 'zalo_oa')
    inactiveChannelIds.value = new Set(oa.filter((ch) => !ch.is_active).map((ch) => ch.id))
    channels.value = oa.map((ch) => ({
      id: ch.id,
      name: ch.is_active ? ch.name : `${ch.name} ${t('campaign_channel_inactive_suffix')}`,
    }))
  } catch {
    channels.value = []
  }
}

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

// Campaigns currently targeting a deactivated Zalo OA channel — shown inline.
const affectedByInactiveChannel = computed(() =>
  campaigns.value.filter((c) => inactiveChannelIds.value.has(c.channelId)),
)

async function onSaved() {
  await load()
  await warningsStore.refresh()
  emit('changed')
  emit('notify', 'Đã lưu chiến dịch', 'success')
}

// "Gửi trước giờ hẹn" warning modal — only for one-time segments not yet due.
const sendConfirm = ref<Campaign | null>(null)
const sendConfirmOpen = computed({
  get: () => sendConfirm.value !== null,
  set: (v) => { if (!v) sendConfirm.value = null },
})

// Earliest one-time segment whose scheduled time hasn't arrived yet, if any.
function earliestPendingOnce(c: Campaign): { time: string; groupName: string } | null {
  const now = Date.now()
  const pending = c.segments
    .filter((s) => s.scheduleKind === 'once')
    .map((s) => ({ at: s.nextRunAt ?? s.runAt, groupName: s.groupName }))
    .filter((s): s is { at: string; groupName: string } => !!s.at && new Date(s.at).getTime() > now)
    .sort((a, b) => a.at.localeCompare(b.at))
  if (pending.length === 0) return null
  return { time: formatDateTime(pending[0].at), groupName: pending[0].groupName }
}

const sendConfirmInfo = computed(() =>
  sendConfirm.value ? earliestPendingOnce(sendConfirm.value) : null,
)

function onSendNow(c: Campaign) {
  // Warn before sending a one-time campaign ahead of its scheduled time;
  // otherwise (recurring, or already due) send immediately as before.
  if (earliestPendingOnce(c)) {
    sendConfirm.value = c
    return
  }
  void doSendNow(c)
}

async function confirmSendNow() {
  const c = sendConfirm.value
  if (!c) return
  sendConfirm.value = null
  await doSendNow(c)
}

async function doSendNow(c: Campaign) {
  busyId.value = c.id
  try {
    const { sent } = await sendNow(c.id)
    await load()
    emit('changed')
    emit('notify', `Đã gửi ${sent.toLocaleString()} tin`, 'success')
  } catch (e: any) {
    const code = e?.response?.data?.error
    if (code === 'channel_inactive' || code === 'channel_not_found') {
      emit('notify', t('campaign_send_channel_inactive'), 'error')
    } else {
      emit('notify', 'Gửi thất bại', 'error')
    }
  } finally {
    busyId.value = null
  }
}

async function onToggle(c: Campaign, status: CampaignStatus) {
  await setCampaignStatus(c.id, status)
  await load()
  emit('changed')
}

async function onDelete() {
  if (!confirmId.value) return
  await deleteCampaign(confirmId.value)
  confirmId.value = null
  await load()
  await warningsStore.refresh()
  emit('changed')
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

onMounted(async () => {
  await loadReferenceData()
  await load()
})
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
