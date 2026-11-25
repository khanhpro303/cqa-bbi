<template>
  <v-dialog :model-value="modelValue" max-width="960px" scrollable @update:model-value="v => $emit('update:modelValue', v)">
    <v-card v-if="item">
      <v-card-title class="d-flex align-center">
        <v-icon start :color="severityColor(item.severity)">mdi-alert-circle-outline</v-icon>
        <span class="text-truncate">{{ item.title }}</span>
        <v-spacer />
        <v-btn icon="mdi-close" variant="text" size="small" @click="$emit('update:modelValue', false)" />
      </v-card-title>
      <v-card-text>
        <div class="d-flex flex-wrap ga-2 mb-3">
          <v-chip size="small" variant="tonal" :color="caseStatusColor(item.status)">{{ $t(`cc_status_${item.status}`) }}</v-chip>
          <v-chip size="small" :color="severityColor(item.severity)">{{ $t(`cm_severity_${item.severity}`) }}</v-chip>
          <v-chip size="small" variant="outlined">{{ $t(issueTypeLabelKey(item.type)) }}</v-chip>
          <v-chip v-if="item.stream" size="small" variant="outlined" prepend-icon="mdi-source-fork">{{ streamName(streams || [], item.stream) }}</v-chip>
          <v-chip size="small" variant="outlined" prepend-icon="mdi-source-branch">{{ $t(`cc_source_${item.source}`) }}</v-chip>
          <v-chip v-if="item.followup_count" size="small" color="warning" variant="tonal">{{ $t('cc_followups') }}: {{ item.followup_count }}</v-chip>
        </div>

        <v-row dense>
          <v-col cols="12" md="7">
            <div class="text-caption text-medium-emphasis mb-2">
              {{ item.customer_name || item.reporter_name || item.subject_id }}
              <span v-if="item.customer_code"> · {{ item.customer_code }}</span>
              · {{ item.subject_type === 'group' ? $t('cm_group_chat') : $t('cm_direct_chat') }}
              <span v-if="item.reporter_name && item.reporter_name !== item.customer_name"> · {{ $t('cc_reporter') }}: {{ item.reporter_name }}</span>
              · {{ $t('cc_opened') }} {{ formatDateTime(item.created_at) }}
            </div>
            <div class="text-subtitle-2 mb-1">{{ $t('cc_detail') }}</div>
            <p class="mb-3 pre-wrap">{{ item.detail || '—' }}</p>
            <template v-if="item.evidence">
              <div class="text-subtitle-2 mb-1">{{ $t('cc_evidence') }}</div>
              <v-sheet class="pa-2 mb-3 rounded pre-wrap" color="surface-variant">“{{ item.evidence }}”</v-sheet>
            </template>
            <template v-if="images.length">
              <div class="text-subtitle-2 mb-1">{{ $t('cc_images') }}</div>
              <div class="d-flex flex-wrap ga-2 mb-3">
                <div v-for="im in images" :key="im.image_id || im.url" class="image-link">
                  <template v-if="im.image_id">
                    <a
                      v-if="activeImageId === im.image_id && activeImageUrl"
                      :href="activeImageUrl"
                      :aria-label="$t('cc_image_full_size')"
                      target="_blank"
                      rel="noopener noreferrer"
                      data-testid="case-image-preview"
                    >
                      <v-img :src="activeImageUrl" :alt="im.description || $t('cc_images')" width="120" height="120" class="rounded border" />
                      <span class="text-caption">{{ $t('cc_image_full_size') }}</span>
                    </a>
                    <v-btn
                      v-else
                      variant="outlined"
                      size="small"
                      block
                      prepend-icon="mdi-image-search-outline"
                      :loading="imageLoadingId === im.image_id"
                      :aria-describedby="im.description ? 'case-image-description-' + im.image_id : undefined"
                      data-testid="case-image-show"
                      @click="loadCaseImage(im)"
                    >{{ imageErrorId === im.image_id ? $t('crm_order_tracking_retry') : $t('cc_view_image') }}</v-btn>
                    <div v-if="imageErrorId === im.image_id" class="text-error image-error" role="alert">{{ $t('cc_image_unavailable') }}</div>
                  </template>
                  <a v-else-if="im.url" :href="im.url" target="_blank" rel="noopener noreferrer" :title="im.description || ''">
                    <v-img :src="im.url" :alt="im.description || $t('cc_images')" width="120" height="120" cover class="rounded border" />
                  </a>
                  <div v-if="im.description" :id="im.image_id ? 'case-image-description-' + im.image_id : undefined" class="image-desc">{{ im.description }}</div>
                </div>
              </div>
            </template>
            <v-btn
              v-if="item.opened_session_id"
              variant="text"
              size="small"
              prepend-icon="mdi-forum-outline"
              class="mb-3 px-0"
              data-testid="case-open-session"
              @click="$emit('open-session', item.opened_session_id)"
            >{{ $t('cc_view_session') }}</v-btn>

            <div class="text-subtitle-2 mb-1">{{ $t('cc_history') }}</div>
            <div class="mb-2">
              <div v-for="e in events" :key="e.id" class="d-flex ga-2 mb-3" :data-testid="`case-event-${e.kind}`">
                <v-icon size="small" class="mt-1" color="medium-emphasis">{{ caseEventIcon(e.kind) }}</v-icon>
                <div>
                  <div class="text-caption text-medium-emphasis">
                    {{ $t(`cc_event_${e.kind}`) }} · {{ actorLabel(e) }} · {{ formatDateTime(e.created_at) }}
                  </div>
                  <div class="pre-wrap text-body-2">{{ e.text }}</div>
                </div>
              </div>
            </div>
          </v-col>

          <v-col cols="12" md="5">
            <v-select v-model="form.status" :items="statusOptions" :label="$t('cm_status')" density="compact" class="mb-2" hide-details data-testid="case-status" />
            <v-row dense class="mb-2">
              <v-col cols="6"><v-select v-model="form.severity" :items="severityOptions" :label="$t('cc_severity')" density="compact" hide-details /></v-col>
              <v-col cols="6"><v-select v-model="form.type" :items="typeOptions" :label="$t('cc_type')" density="compact" hide-details /></v-col>
            </v-row>
            <v-select
              v-if="streamOptions.length > 1"
              v-model="form.stream"
              :items="streamOptions"
              :label="$t('cc_stream')"
              :hint="$t('cc_stream_hint')"
              persistent-hint
              density="compact"
              class="mb-2"
              data-testid="case-stream"
            />
            <v-combobox
              v-model="form.assignee"
              :items="userOptions"
              item-title="title"
              item-value="value"
              :label="$t('cc_assignee')"
              density="compact"
              clearable
              class="mb-2"
              hide-details
            />
            <v-textarea
              v-model="form.customer_update"
              :label="$t('cc_customer_update')"
              :hint="$t('cc_customer_update_hint')"
              persistent-hint
              rows="3"
              auto-grow
              density="compact"
              class="mb-3"
              data-testid="case-customer-update"
            />
            <v-textarea v-model="form.resolution" :label="$t('cc_resolution')" rows="2" auto-grow density="compact" class="mb-2" hide-details />
            <v-textarea v-model="form.note" :label="$t('cc_note')" rows="2" auto-grow density="compact" hide-details />
          </v-col>
        </v-row>
      </v-card-text>
      <v-card-actions>
        <v-spacer />
        <v-btn variant="text" @click="$emit('update:modelValue', false)">{{ $t('cm_close') }}</v-btn>
        <v-btn color="primary" variant="flat" :loading="saving" data-testid="case-save" @click="save">{{ $t('cc_save') }}</v-btn>
      </v-card-actions>
    </v-card>
    <v-card v-else class="pa-8 text-center"><v-progress-circular indeterminate /></v-card>
    <v-snackbar v-model="snack" :color="snackColor" timeout="2500">{{ snackText }}</v-snackbar>
  </v-dialog>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, reactive, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import api from '../../api'
import {
  type CaseEvent, type CaseStream, type CustomerCase, caseEventIcon, caseStatusColor, caseStatuses, caseTypes,
  issueTypeLabelKey, mergeCaseImages, parseCaseImages, severityColor, streamName,
} from './labels'

const props = defineProps<{ modelValue: boolean; tenantId: string; caseId: string; streams?: CaseStream[] }>()
const emit = defineEmits<{
  (e: 'update:modelValue', v: boolean): void
  (e: 'saved', c: CustomerCase): void
  (e: 'open-session', id: string): void
}>()

const { t } = useI18n()
const item = ref<CustomerCase | null>(null)
const events = ref<CaseEvent[]>([])
const users = ref<{ user_id: string; name: string; email: string }[]>([])
const saving = ref(false)
const snack = ref(false)
const snackText = ref('')
const snackColor = ref('success')
const activeImageId = ref('')
const activeImageUrl = ref('')
const imageLoadingId = ref('')
const imageErrorId = ref('')
let imageRequest: AbortController | undefined

type AssigneeOption = { title: string; value: string }
const form = reactive({
  status: '', severity: '', type: '', stream: '',
  assignee: null as AssigneeOption | string | null,
  customer_update: '', resolution: '', note: '',
})

const images = computed(() => mergeCaseImages(
  parseCaseImages(item.value?.images),
  ...events.value.map(event => parseCaseImages(event.images)),
))
const statusOptions = computed(() => caseStatuses.map(s => ({ title: t(`cc_status_${s}`), value: s })))
const severityOptions = computed(() => ['high', 'medium', 'low'].map(s => ({ title: t(`cm_severity_${s}`), value: s })))
const typeOptions = computed(() => caseTypes.map(v => ({ title: t(issueTypeLabelKey(v)), value: v })))
// The case's current stream stays pickable even if it was removed from the
// channel settings.
const streamOptions = computed(() => {
  const list = (props.streams || []).map(s => ({ title: s.name, value: s.key }))
  const cur = item.value?.stream
  if (cur && !list.some(o => o.value === cur)) list.push({ title: cur, value: cur })
  return [{ title: t('cc_stream_none'), value: '' }, ...list]
})
// Staff sharing a display name are told apart by their email.
const userOptions = computed<AssigneeOption[]>(() => {
  const count = new Map<string, number>()
  for (const u of users.value) count.set(u.name || u.email, (count.get(u.name || u.email) || 0) + 1)
  return users.value.map(u => {
    const name = u.name || u.email
    return { title: (count.get(name) || 0) > 1 && u.name ? `${u.name} (${u.email})` : name, value: u.user_id }
  })
})

function fill(c: CustomerCase) {
  item.value = c
  form.status = c.status
  form.severity = c.severity
  form.type = c.type
  form.stream = c.stream || ''
  form.assignee = c.assignee_name ? { title: c.assignee_name, value: c.assignee_id } : null
  form.customer_update = c.customer_update || ''
  form.resolution = c.resolution || ''
  form.note = ''
}

// Only the latest load fills the dialog: a slower earlier one (another
// case, or the same case opened again) must not overwrite it.
let loadSeq = 0

function clearCaseImage() {
  imageRequest?.abort()
  imageRequest = undefined
  if (activeImageUrl.value) URL.revokeObjectURL(activeImageUrl.value)
  activeImageId.value = ''
  activeImageUrl.value = ''
  imageLoadingId.value = ''
  imageErrorId.value = ''
}

async function loadCaseImage(image: { image_id?: string }) {
  const imageId = image.image_id
  const caseId = item.value?.id
  if (!imageId || !caseId) return
  clearCaseImage()
  const request = new AbortController()
  imageRequest = request
  imageLoadingId.value = imageId
  try {
    const { data } = await api.get<Blob>(
      `/tenants/${encodeURIComponent(props.tenantId)}/customer-cases/${encodeURIComponent(caseId)}/images/${encodeURIComponent(imageId)}`,
      { responseType: 'blob', signal: request.signal },
    )
    if (request.signal.aborted) return
    if (!['image/jpeg', 'image/png', 'image/gif', 'image/webp'].includes(data.type)) throw new Error('Unsupported case image type')
    activeImageId.value = imageId
    activeImageUrl.value = URL.createObjectURL(data)
  } catch {
    if (!request.signal.aborted) imageErrorId.value = imageId
  } finally {
    if (!request.signal.aborted) imageLoadingId.value = ''
    if (imageRequest === request) imageRequest = undefined
  }
}

async function load() {
  if (!props.caseId) return
  clearCaseImage()
  const seq = ++loadSeq
  item.value = null
  events.value = []
  try {
    const { data } = await api.get(`/tenants/${props.tenantId}/customer-cases/${props.caseId}`)
    if (seq !== loadSeq) return
    fill(data.case)
    events.value = data.events || []
  } catch {
    notify(t('cm_load_failed'), 'error')
  }
  if (users.value.length === 0) {
    try {
      const { data } = await api.get(`/tenants/${props.tenantId}/users`)
      users.value = Array.isArray(data) ? data : []
    } catch {
      users.value = []
    }
  }
}

function assigneeFields() {
  const a = form.assignee
  if (!a) return { assignee_name: '', assignee_id: '' }
  if (typeof a === 'string') return { assignee_name: a.trim(), assignee_id: '' }
  const u = users.value.find(x => x.user_id === a.value)
  return { assignee_name: u ? (u.name || u.email) : a.title, assignee_id: a.value }
}

// assigneeChanged compares the picked person (by user id when there is one),
// not the name: a renamed user is still the same assignee.
function assigneeChanged(c: CustomerCase) {
  const a = form.assignee
  if (!a) return !!(c.assignee_name || c.assignee_id)
  if (typeof a === 'string') return a.trim() !== (c.assignee_name || '') || !!c.assignee_id
  if (a.value) return a.value !== (c.assignee_id || '')
  return a.title !== (c.assignee_name || '')
}

// changedFields sends only what staff changed in the form, so saving a note
// does not write back fields the bot or another user changed meanwhile.
function changedFields(c: CustomerCase) {
  const body: Record<string, string> = {}
  if (form.status !== c.status) body.status = form.status
  if (form.severity !== c.severity) body.severity = form.severity
  if (form.type !== c.type) body.type = form.type
  if (form.stream !== (c.stream || '')) body.stream = form.stream
  if (assigneeChanged(c)) Object.assign(body, assigneeFields())
  if (form.customer_update !== (c.customer_update || '')) body.customer_update = form.customer_update
  if (form.resolution !== (c.resolution || '')) body.resolution = form.resolution
  if (form.note.trim()) body.note = form.note
  return body
}

async function save() {
  if (!item.value) return
  saving.value = true
  try {
    const { data } = await api.patch(`/tenants/${props.tenantId}/customer-cases/${item.value.id}`, changedFields(item.value))
    notify(t('cc_saved'))
    emit('saved', data.case)
    await load()
  } catch {
    notify(t('cc_save_failed'), 'error')
  } finally {
    saving.value = false
  }
}

function notify(text: string, color = 'success') {
  snackText.value = text
  snackColor.value = color
  snack.value = true
}

function actorLabel(e: CaseEvent) {
  if (e.actor === 'staff') return e.actor_name || t('cm_speaker_staff')
  return t(`cc_actor_${e.actor}`)
}
function formatDateTime(v?: string | null) {
  if (!v) return '—'
  return new Date(v).toLocaleString('vi-VN', { hour: '2-digit', minute: '2-digit', day: '2-digit', month: '2-digit', year: 'numeric' })
}

watch(() => [props.modelValue, props.caseId], ([open]) => {
  clearCaseImage()
  if (open) load()
}, { immediate: true })

onBeforeUnmount(clearCaseImage)

defineExpose({ form, save, item, images, loadCaseImage, activeImageUrl, imageErrorId })
</script>

<style scoped>
.pre-wrap { white-space: pre-wrap; }
.image-link { width: 120px; text-decoration: none; color: inherit; }
.image-link > a { color: inherit; text-decoration: none; }
.image-desc {
  font-size: 0.75rem;
  line-height: 1.2;
  display: -webkit-box;
  -webkit-line-clamp: 3;
  -webkit-box-orient: vertical;
  overflow: hidden;
}
.image-error { font-size: 0.75rem; line-height: 1.2; }
</style>
