<template>
  <div>
    <h1 class="text-h5 font-weight-bold mb-2">{{ $t('nav_chat_memory') }}</h1>
    <div class="text-grey-darken-1 mb-4">{{ $t('cm_page_desc') }}</div>

    <v-tabs v-model="tab" class="mb-4" color="primary">
      <v-tab value="cases" data-testid="tab-cases">{{ $t('cm_tab_cases') }}</v-tab>
      <v-tab value="profiles" data-testid="tab-profiles">{{ $t('cm_tab_profiles') }}</v-tab>
      <v-tab value="sessions" data-testid="tab-sessions">{{ $t('cm_tab_sessions') }}</v-tab>
    </v-tabs>

    <!-- Complaint cases -->
    <v-card v-if="tab === 'cases'" class="pa-4">
      <CasesPanel :tenant-id="tenantId" @open-session="openSessionById" />
    </v-card>

    <!-- Profiles -->
    <v-card v-if="tab === 'profiles'" class="pa-4">
      <v-row dense class="mb-2">
        <v-col cols="12" md="6">
          <v-text-field
            v-model="profileQuery"
            :label="$t('cm_search_profiles')"
            prepend-inner-icon="mdi-magnify"
            density="compact"
            clearable
            hide-details
            @update:model-value="debouncedProfiles"
          />
        </v-col>
        <v-col cols="12" md="3">
          <v-select v-model="profileType" :items="subjectTypeOptions" :label="$t('cm_subject_type')" density="compact" hide-details @update:model-value="loadProfiles(1)" />
        </v-col>
      </v-row>
      <v-table density="comfortable" hover>
        <thead>
          <tr>
            <th>{{ $t('cm_customer') }}</th>
            <th>{{ $t('cm_subject_type') }}</th>
            <th class="text-right">{{ $t('cm_sessions_count') }}</th>
            <th>{{ $t('cm_last_session') }}</th>
            <th>{{ $t('cm_card') }}</th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="p in profiles" :key="p.id" class="cursor-pointer" @click="openProfile(p)">
            <td>
              <div class="font-weight-medium">{{ p.customer_name || p.subject_id }}</div>
              <div v-if="p.customer_code" class="text-caption text-medium-emphasis">{{ p.customer_code }}</div>
            </td>
            <td>{{ p.subject_type === 'group' ? $t('cm_group_chat') : $t('cm_direct_chat') }}</td>
            <td class="text-right">{{ p.sessions_count }}</td>
            <td>{{ formatDateTime(p.last_session_at) }}</td>
            <td class="card-cell">{{ p.card }}</td>
          </tr>
          <tr v-if="!loadingProfiles && profiles.length === 0">
            <td colspan="5" class="text-center text-medium-emphasis py-6">{{ $t('cm_no_profiles') }}</td>
          </tr>
        </tbody>
      </v-table>
      <div class="d-flex justify-end mt-2">
        <v-pagination v-if="profileTotal > perPage" v-model="profilePage" :length="Math.ceil(profileTotal / perPage)" density="compact" @update:model-value="loadProfiles" />
      </div>
    </v-card>

    <!-- Sessions -->
    <v-card v-if="tab === 'sessions'" class="pa-4">
      <v-row dense class="mb-2">
        <v-col cols="12" md="3">
          <v-select v-model="issueFilter" :items="issueOptions" :label="$t('cm_issue_filter')" density="compact" hide-details data-testid="issue-filter" @update:model-value="loadSessions(1)" />
        </v-col>
        <v-col cols="12" md="3">
          <v-select v-model="statusFilter" :items="statusOptions" :label="$t('cm_status')" density="compact" hide-details @update:model-value="loadSessions(1)" />
        </v-col>
        <v-col cols="12" md="3" class="d-flex align-center">
          <v-switch v-model="alertedOnly" :label="$t('cm_alerted_only')" density="compact" color="primary" hide-details @update:model-value="loadSessions(1)" />
        </v-col>
      </v-row>
      <SessionTable :sessions="sessions" :loading="loadingSessions" @open="openSession" />
      <div class="d-flex justify-end mt-2">
        <v-pagination v-if="sessionTotal > perPage" v-model="sessionPage" :length="Math.ceil(sessionTotal / perPage)" density="compact" @update:model-value="loadSessions" />
      </div>
    </v-card>

    <!-- Profile dialog -->
    <v-dialog v-model="profileDialog" max-width="900px">
      <v-card v-if="selectedProfile">
        <v-card-title class="d-flex align-center">
          <v-icon start>mdi-account-heart-outline</v-icon>
          {{ selectedProfile.customer_name || selectedProfile.subject_id }}
          <v-spacer />
          <v-btn icon="mdi-close" variant="text" size="small" @click="profileDialog = false" />
        </v-card-title>
        <v-card-text>
          <div class="text-caption text-medium-emphasis mb-3">
            {{ selectedProfile.subject_type === 'group' ? $t('cm_group_chat') : $t('cm_direct_chat') }}
            <span v-if="selectedProfile.customer_code"> · {{ selectedProfile.customer_code }}</span>
            · {{ $t('cm_sessions_count') }}: {{ selectedProfile.sessions_count }}
          </div>
          <div class="text-subtitle-2 mb-1">{{ $t('cm_card') }}</div>
          <v-sheet class="pa-3 mb-4 rounded" color="surface-variant" style="white-space: pre-wrap">{{ selectedProfile.card || '—' }}</v-sheet>
          <div class="text-subtitle-2 mb-1">{{ $t('cm_profile') }}</div>
          <v-table density="compact" class="mb-4">
            <tbody>
              <tr v-for="row in profileRows(selectedProfile.profile)" :key="row.key">
                <td class="text-medium-emphasis" style="width: 200px">{{ row.label }}</td>
                <td>{{ row.value }}</td>
              </tr>
            </tbody>
          </v-table>
          <div class="text-subtitle-2 mb-1">{{ $t('cc_customer_cases') }}</div>
          <CasesPanel
            :tenant-id="tenantId"
            compact
            :subject-type="selectedProfile.subject_type"
            :subject-id="selectedProfile.subject_id"
            :customer-code="selectedProfile.customer_code"
            class="mb-4"
            @open-session="openSessionById"
          />
          <div class="text-subtitle-2 mb-1">{{ $t('cm_recent_sessions') }}</div>
          <SessionTable :sessions="profileSessions" :loading="loadingProfileSessions" compact @open="openSession" />
        </v-card-text>
        <v-card-actions>
          <v-btn color="error" variant="text" prepend-icon="mdi-delete" @click="confirmDelete = true">{{ $t('cm_delete_profile') }}</v-btn>
          <v-spacer />
          <v-btn variant="outlined" @click="profileDialog = false">{{ $t('cm_close') }}</v-btn>
        </v-card-actions>
      </v-card>
    </v-dialog>

    <v-dialog v-model="confirmDelete" max-width="420px">
      <v-card>
        <v-card-title>{{ $t('cm_delete_profile') }}</v-card-title>
        <v-card-text>{{ $t('cm_delete_profile_confirm') }}</v-card-text>
        <v-card-actions>
          <v-spacer />
          <v-btn variant="text" @click="confirmDelete = false">{{ $t('cancel') }}</v-btn>
          <v-btn color="error" variant="flat" :loading="deleting" @click="deleteProfile">{{ $t('delete') }}</v-btn>
        </v-card-actions>
      </v-card>
    </v-dialog>

    <!-- Session dialog -->
    <v-dialog v-model="sessionDialog" max-width="900px">
      <v-card v-if="selectedSession">
        <v-card-title class="d-flex align-center">
          <v-icon start>mdi-forum-outline</v-icon>
          {{ selectedSession.customer_name || selectedSession.subject_id }}
          <v-spacer />
          <v-btn icon="mdi-close" variant="text" size="small" @click="sessionDialog = false" />
        </v-card-title>
        <v-card-text>
          <div class="text-caption text-medium-emphasis mb-3">
            {{ formatDateTime(selectedSession.started_at) }} – {{ formatDateTime(selectedSession.last_activity_at) }}
            · {{ statusLabel(selectedSession.status) }}
            <span v-if="selectedSession.alerted_at"> · {{ $t('cm_alerted_at') }} {{ formatDateTime(selectedSession.alerted_at) }}</span>
          </div>
          <template v-if="selectedSession.summary">
            <div class="text-subtitle-2 mb-1">{{ $t('cm_summary') }}</div>
            <p class="mb-3">{{ selectedSession.summary }}</p>
            <div class="d-flex flex-wrap ga-2 mb-3">
              <v-chip v-if="selectedSession.outcome" size="small" variant="tonal">{{ outcomeLabel(selectedSession.outcome) }}</v-chip>
              <v-chip v-if="selectedSession.sentiment" size="small" variant="tonal" :color="sentimentColor(selectedSession.sentiment)">{{ sentimentLabel(selectedSession.sentiment) }}</v-chip>
              <v-chip v-for="i in parseList(selectedSession.intents)" :key="'i' + i" size="small" variant="outlined">{{ i }}</v-chip>
              <v-chip v-for="p in parseList(selectedSession.products)" :key="'p' + p" size="small" variant="outlined" prepend-icon="mdi-package-variant">{{ p }}</v-chip>
            </div>
          </template>
          <v-alert v-if="selectedSession.summary_error" type="warning" variant="tonal" density="compact" class="mb-3">{{ selectedSession.summary_error }}</v-alert>
          <template v-if="parseIssues(selectedSession.issues).length">
            <div class="text-subtitle-2 mb-1">{{ $t('cm_issues') }}</div>
            <v-list density="compact" class="mb-3 py-0" border rounded>
              <v-list-item v-for="(is, idx) in parseIssues(selectedSession.issues)" :key="idx">
                <template #prepend>
                  <v-chip size="x-small" :color="severityColor(is.severity)" class="mr-2">{{ severityLabel(is.severity) }}</v-chip>
                </template>
                <v-list-item-title>{{ issueTypeLabel(is.type) }}: {{ is.detail }}</v-list-item-title>
                <v-list-item-subtitle v-if="is.evidence">“{{ is.evidence }}”</v-list-item-subtitle>
              </v-list-item>
            </v-list>
          </template>
          <div class="text-subtitle-2 mb-1">{{ $t('cm_transcript') }}</div>
          <div v-if="loadingTranscript" class="text-center py-4"><v-progress-circular indeterminate size="24" /></div>
          <div v-else class="transcript">
            <div v-for="m in transcript" :key="m.id" class="mb-2" :class="m.speaker === 'customer' ? 'text-left' : 'text-right'">
              <div class="text-caption text-medium-emphasis">{{ speakerLabel(m) }} · {{ formatTime(m.sent_at) }}</div>
              <div class="bubble d-inline-block pa-2 rounded" :class="`bubble-${m.speaker}`">{{ m.content }}</div>
            </div>
            <div v-if="transcript.length === 0" class="text-medium-emphasis">—</div>
          </div>
        </v-card-text>
        <v-card-actions>
          <v-btn
            v-if="['summarized', 'skipped', 'failed'].includes(selectedSession.status)"
            variant="text"
            prepend-icon="mdi-refresh"
            :loading="resummarizing"
            @click="resummarize"
          >{{ $t('cm_resummarize') }}</v-btn>
          <v-spacer />
          <v-btn variant="outlined" @click="sessionDialog = false">{{ $t('cm_close') }}</v-btn>
        </v-card-actions>
      </v-card>
    </v-dialog>

    <v-snackbar v-model="snack" :color="snackColor" timeout="2500">{{ snackText }}</v-snackbar>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import { useRoute } from 'vue-router'
import { useI18n } from 'vue-i18n'
import api from '../api'
import SessionTable from '../components/chat-memory/SessionTable.vue'
import CasesPanel from '../components/chat-memory/CasesPanel.vue'
import {
  type ChatProfile, type ChatSession, type TranscriptLine,
  parseIssues, parseList, issueTypeLabelKey, severityColor, sentimentColor, profileFieldKeys,
} from '../components/chat-memory/labels'

const route = useRoute()
const { t } = useI18n()
const tenantId = computed(() => route.params.tenantId as string)
const perPage = 20

const tab = ref<'cases' | 'profiles' | 'sessions'>(
  route.query.tab === 'sessions' ? 'sessions' : route.query.tab === 'profiles' ? 'profiles' : 'cases',
)

const profiles = ref<ChatProfile[]>([])
const profileTotal = ref(0)
const profilePage = ref(1)
const profileQuery = ref('')
const profileType = ref('')
const loadingProfiles = ref(false)

const sessions = ref<ChatSession[]>([])
const sessionTotal = ref(0)
const sessionPage = ref(1)
const issueFilter = ref(route.query.issues === 'high' ? 'high' : '')
const statusFilter = ref('')
const alertedOnly = ref(false)
const loadingSessions = ref(false)

const profileDialog = ref(false)
const selectedProfile = ref<ChatProfile | null>(null)
const profileSessions = ref<ChatSession[]>([])
const loadingProfileSessions = ref(false)
const confirmDelete = ref(false)
const deleting = ref(false)

const sessionDialog = ref(false)
const selectedSession = ref<ChatSession | null>(null)
const transcript = ref<TranscriptLine[]>([])
const loadingTranscript = ref(false)
const resummarizing = ref(false)

const snack = ref(false)
const snackText = ref('')
const snackColor = ref('success')

const subjectTypeOptions = computed(() => [
  { title: t('all'), value: '' },
  { title: t('cm_direct_chat'), value: 'user' },
  { title: t('cm_group_chat'), value: 'group' },
])
const issueOptions = computed(() => [
  { title: t('all'), value: '' },
  { title: t('cm_issue_any'), value: 'any' },
  { title: t('cm_issue_high'), value: 'high' },
])
const statusOptions = computed(() => [
  { title: t('all'), value: '' },
  ...['summarized', 'open', 'skipped', 'failed'].map(s => ({ title: statusLabel(s), value: s })),
])

function notify(text: string, color = 'success') {
  snackText.value = text
  snackColor.value = color
  snack.value = true
}

async function loadProfiles(page = profilePage.value) {
  profilePage.value = page
  loadingProfiles.value = true
  try {
    const { data } = await api.get(`/tenants/${tenantId.value}/chat-memory/profiles`, {
      params: { page, per_page: perPage, q: profileQuery.value || undefined, subject_type: profileType.value || undefined },
    })
    profiles.value = data?.data || []
    profileTotal.value = data?.total || 0
  } catch {
    notify(t('cm_load_failed'), 'error')
  } finally {
    loadingProfiles.value = false
  }
}

let profileTimer: ReturnType<typeof setTimeout> | undefined
function debouncedProfiles() {
  clearTimeout(profileTimer)
  profileTimer = setTimeout(() => loadProfiles(1), 300)
}

async function loadSessions(page = sessionPage.value) {
  sessionPage.value = page
  loadingSessions.value = true
  try {
    const { data } = await api.get(`/tenants/${tenantId.value}/chat-memory/sessions`, {
      params: {
        page, per_page: perPage,
        issues: issueFilter.value || undefined,
        status: statusFilter.value || undefined,
        alerted: alertedOnly.value ? 'true' : undefined,
      },
    })
    sessions.value = data?.data || []
    sessionTotal.value = data?.total || 0
  } catch {
    notify(t('cm_load_failed'), 'error')
  } finally {
    loadingSessions.value = false
  }
}

async function openProfile(p: ChatProfile) {
  selectedProfile.value = p
  profileDialog.value = true
  loadingProfileSessions.value = true
  profileSessions.value = []
  try {
    const { data } = await api.get(`/tenants/${tenantId.value}/chat-memory/sessions`, {
      params: { subject_type: p.subject_type, subject_id: p.subject_id, per_page: 10 },
    })
    profileSessions.value = data?.data || []
  } finally {
    loadingProfileSessions.value = false
  }
}

async function deleteProfile() {
  if (!selectedProfile.value) return
  deleting.value = true
  try {
    await api.delete(`/tenants/${tenantId.value}/chat-memory/profiles/${selectedProfile.value.id}`)
    confirmDelete.value = false
    profileDialog.value = false
    notify(t('cm_profile_deleted'))
    await loadProfiles()
  } catch {
    notify(t('cm_delete_failed'), 'error')
  } finally {
    deleting.value = false
  }
}

async function openSession(s: ChatSession) {
  selectedSession.value = s
  sessionDialog.value = true
  loadingTranscript.value = true
  transcript.value = []
  try {
    const { data } = await api.get(`/tenants/${tenantId.value}/chat-memory/sessions/${s.id}/messages`)
    transcript.value = data?.messages || []
  } finally {
    loadingTranscript.value = false
  }
}

async function openSessionById(id: string) {
  try {
    const { data } = await api.get(`/tenants/${tenantId.value}/chat-memory/sessions`, { params: { id } })
    const s = (data?.data || [])[0] as ChatSession | undefined
    if (s) await openSession(s)
    else notify(t('cm_load_failed'), 'error')
  } catch {
    notify(t('cm_load_failed'), 'error')
  }
}

async function resummarize() {
  if (!selectedSession.value) return
  resummarizing.value = true
  try {
    await api.post(`/tenants/${tenantId.value}/chat-memory/sessions/${selectedSession.value.id}/resummarize`)
    selectedSession.value.status = 'open'
    notify(t('cm_resummarize_queued'))
  } catch {
    notify(t('cm_resummarize_failed'), 'error')
  } finally {
    resummarizing.value = false
  }
}

function profileRows(raw: string) {
  let obj: Record<string, unknown> = {}
  try { obj = JSON.parse(raw || '{}') || {} } catch { obj = {} }
  const rows: { key: string; label: string; value: string }[] = []
  const keys = [...profileFieldKeys, ...Object.keys(obj).filter(k => !profileFieldKeys.includes(k))]
  for (const key of keys) {
    const v = obj[key]
    const text = Array.isArray(v) ? v.join(', ') : (v == null ? '' : String(v))
    if (!text.trim()) continue
    // Known fields are translated; extra fields a custom prompt adds show their key.
    const label = profileFieldKeys.includes(key) ? t(`cm_profile_${key}`) : key
    rows.push({ key, label, value: text })
  }
  return rows
}

function statusLabel(s: string) { return t(`cm_status_${s}`) }
function outcomeLabel(s: string) { const k = `cm_outcome_${s}`; const v = t(k); return v === k ? s : v }
function sentimentLabel(s: string) { const k = `cm_sentiment_${s}`; const v = t(k); return v === k ? s : v }
function severityLabel(s: string) { const k = `cm_severity_${s}`; const v = t(k); return v === k ? s : v }
function issueTypeLabel(s: string) { return t(issueTypeLabelKey(s)) }
function speakerLabel(m: TranscriptLine) {
  if (m.speaker === 'customer') return m.sender_name || t('cm_speaker_customer')
  if (m.speaker === 'bot') return t('cm_speaker_bot')
  return m.sender_name || t('cm_speaker_staff')
}
function formatDateTime(v?: string | null) {
  if (!v) return '—'
  return new Date(v).toLocaleString('vi-VN', { hour: '2-digit', minute: '2-digit', day: '2-digit', month: '2-digit', year: 'numeric' })
}
function formatTime(v: string) {
  return new Date(v).toLocaleTimeString('vi-VN', { hour: '2-digit', minute: '2-digit' })
}

watch(tab, v => {
  if (v === 'sessions' && sessions.value.length === 0) loadSessions(1)
  if (v === 'profiles' && profiles.value.length === 0) loadProfiles(1)
})
onMounted(() => {
  if (tab.value === 'sessions') loadSessions(1)
  else if (tab.value === 'profiles') loadProfiles(1)
})

defineExpose({ loadProfiles, loadSessions, profileRows, openSession, openSessionById, sessions, profiles, issueFilter, tab })
</script>

<style scoped>
.card-cell {
  max-width: 420px;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}
.cursor-pointer { cursor: pointer; }
.transcript {
  max-height: 420px;
  overflow-y: auto;
}
.bubble {
  max-width: 80%;
  white-space: pre-wrap;
  text-align: left;
}
.bubble-customer { background: rgba(128, 128, 128, 0.12); }
.bubble-bot { background: rgba(33, 150, 243, 0.12); }
.bubble-staff { background: rgba(76, 175, 80, 0.14); }
</style>
