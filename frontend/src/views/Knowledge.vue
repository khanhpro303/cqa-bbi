<template>
  <div>
    <h1 class="text-h5 font-weight-bold mb-2">{{ $t('nav_knowledge') }}</h1>
    <div class="text-grey-darken-1 mb-4">{{ $t('kb_page_desc') }}</div>

    <v-alert v-if="vectorStore === 'none'" type="warning" variant="tonal" density="compact" class="mb-4" data-testid="kb-keyword-warning">
      {{ $t('kb_keyword_only') }}
    </v-alert>

    <!-- Try a question -->
    <v-card class="pa-4 mb-4">
      <div class="text-subtitle-1 font-weight-medium mb-2">{{ $t('kb_try_title') }}</div>
      <v-row dense>
        <v-col cols="12" md="7">
          <v-text-field v-model="tryQuery" :label="$t('kb_try_query')" density="compact" hide-details prepend-inner-icon="mdi-magnify" @keyup.enter="runSearch" />
        </v-col>
        <v-col cols="8" md="3">
          <v-select v-model="tryAgent" :items="agentOptions" :label="$t('kb_try_as')" density="compact" hide-details />
        </v-col>
        <v-col cols="4" md="2">
          <v-btn color="primary" block :loading="searching" :disabled="!tryQuery.trim()" @click="runSearch">{{ $t('kb_try_run') }}</v-btn>
        </v-col>
      </v-row>
      <div v-if="searched" class="mt-3">
        <div v-if="hits.length === 0" class="text-medium-emphasis">{{ $t('kb_try_no_hits') }}</div>
        <v-sheet v-for="(h, i) in hits" :key="i" class="pa-3 mb-2 rounded" color="surface-variant">
          <div class="text-caption text-medium-emphasis mb-1">{{ h.title }} · {{ $t('kb_score') }} {{ h.score.toFixed(3) }}</div>
          <div class="passage">{{ h.content }}</div>
        </v-sheet>
      </div>
    </v-card>

    <div class="d-flex flex-wrap align-center ga-2 mb-3">
      <v-tabs v-model="view" color="primary" density="compact" @update:model-value="load">
        <v-tab value="active" data-testid="tab-active">{{ $t('kb_tab_active') }}</v-tab>
        <v-tab value="archived" data-testid="tab-archived">{{ $t('kb_tab_archived') }}</v-tab>
      </v-tabs>
      <v-spacer />
      <template v-if="canEdit">
        <v-btn color="primary" prepend-icon="mdi-upload" @click="uploadDialog = true">{{ $t('kb_upload') }}</v-btn>
        <v-btn variant="outlined" prepend-icon="mdi-text-box-plus-outline" @click="textDialog = true">{{ $t('kb_add_text') }}</v-btn>
        <v-btn v-if="isAdmin" variant="text" prepend-icon="mdi-database-import-outline" @click="importDialog = true">{{ $t('kb_import_astra') }}</v-btn>
      </template>
    </div>

    <v-card class="pa-2">
      <v-table density="comfortable" hover>
        <thead>
          <tr>
            <th>{{ $t('kb_col_title') }}</th>
            <th>{{ $t('kb_col_audience') }}</th>
            <th>{{ $t('kb_col_status') }}</th>
            <th class="text-right">{{ $t('kb_col_chunks') }}</th>
            <th>{{ $t('kb_col_added') }}</th>
            <th v-if="view === 'active'">{{ $t('kb_col_enabled') }}</th>
            <th class="text-right" />
          </tr>
        </thead>
        <tbody>
          <tr v-for="d in documents" :key="d.id">
            <td class="cursor-pointer" @click="openDoc(d)">
              <div class="font-weight-medium">{{ d.title }}</div>
              <div class="text-caption text-medium-emphasis">{{ d.file_name || sourceLabel(d.source) }}</div>
            </td>
            <td>
              <v-chip size="small" :color="d.audience === 'internal' ? 'deep-purple' : 'teal'" variant="tonal">
                {{ d.audience === 'internal' ? $t('kb_audience_internal') : $t('kb_audience_public') }}
              </v-chip>
            </td>
            <td>
              <v-chip size="small" :color="statusColor(d.status)" variant="tonal">{{ $t('kb_status_' + d.status) }}</v-chip>
              <div v-if="d.status === 'failed' && d.error" class="text-caption text-error error-cell">{{ d.error }}</div>
            </td>
            <td class="text-right">{{ d.chunk_count }}</td>
            <td>
              <div>{{ formatDateTime(d.created_at) }}</div>
              <div v-if="d.created_by" class="text-caption text-medium-emphasis">{{ d.created_by }}</div>
            </td>
            <td v-if="view === 'active'">
              <v-switch :model-value="d.enabled" :disabled="!canEdit" color="primary" density="compact" hide-details @update:model-value="(v) => patchDoc(d, { enabled: !!v })" />
            </td>
            <td class="text-right text-no-wrap">
              <template v-if="canEdit && view === 'active'">
                <v-btn icon="mdi-refresh" size="small" variant="text" :title="$t('kb_reindex')" :disabled="!d.enabled" @click="reindex(d)" />
                <v-btn icon="mdi-archive-arrow-down-outline" size="small" variant="text" :title="$t('kb_archive')" @click="archive(d)" />
              </template>
              <template v-if="canEdit && view === 'archived'">
                <v-btn icon="mdi-archive-arrow-up-outline" size="small" variant="text" :title="$t('kb_restore')" @click="restore(d)" />
                <v-btn v-if="isAdmin" icon="mdi-delete-forever" size="small" variant="text" color="error" :title="$t('kb_purge')" @click="purgeTarget = d" />
              </template>
            </td>
          </tr>
          <tr v-if="!loading && documents.length === 0">
            <td colspan="7" class="text-center text-medium-emphasis py-6">{{ view === 'archived' ? $t('kb_no_archived') : $t('kb_empty') }}</td>
          </tr>
        </tbody>
      </v-table>
    </v-card>

    <!-- Upload -->
    <v-dialog v-model="uploadDialog" max-width="560px">
      <v-card>
        <v-card-title>{{ $t('kb_upload') }}</v-card-title>
        <v-card-text>
          <v-file-input v-model="uploadFiles" :label="$t('kb_files')" :accept="acceptTypes" multiple show-size density="compact" prepend-icon="mdi-paperclip" />
          <div class="text-caption text-medium-emphasis mb-3">{{ $t('kb_supported', { types: supported.join(', '), mb: maxUploadMb }) }}</div>
          <v-select v-model="uploadAudience" :items="audienceOptions" :label="$t('kb_col_audience')" density="compact" />
          <v-alert v-if="uploadErrors.length" type="error" variant="tonal" density="compact">
            <div v-for="e in uploadErrors" :key="e">{{ e }}</div>
          </v-alert>
        </v-card-text>
        <v-card-actions>
          <v-spacer />
          <v-btn variant="text" @click="uploadDialog = false">{{ $t('cancel') }}</v-btn>
          <v-btn color="primary" variant="flat" :loading="uploading" :disabled="uploadList.length === 0" @click="upload">{{ $t('kb_upload') }}</v-btn>
        </v-card-actions>
      </v-card>
    </v-dialog>

    <!-- Text -->
    <v-dialog v-model="textDialog" max-width="760px">
      <v-card>
        <v-card-title>{{ $t('kb_add_text') }}</v-card-title>
        <v-card-text>
          <v-text-field v-model="textTitle" :label="$t('kb_col_title')" density="compact" />
          <v-textarea v-model="textContent" :label="$t('kb_text_content')" rows="12" auto-grow density="compact" />
          <v-select v-model="textAudience" :items="audienceOptions" :label="$t('kb_col_audience')" density="compact" />
        </v-card-text>
        <v-card-actions>
          <v-spacer />
          <v-btn variant="text" @click="textDialog = false">{{ $t('cancel') }}</v-btn>
          <v-btn color="primary" variant="flat" :loading="savingText" :disabled="!textTitle.trim() || !textContent.trim()" @click="saveText">{{ $t('save') }}</v-btn>
        </v-card-actions>
      </v-card>
    </v-dialog>

    <!-- Astra import -->
    <v-dialog v-model="importDialog" max-width="620px">
      <v-card>
        <v-card-title>{{ $t('kb_import_astra') }}</v-card-title>
        <v-card-text>
          <div class="text-body-2 mb-3">{{ $t('kb_import_desc') }}</div>
          <v-text-field v-model="importForm.api_endpoint" label="API endpoint" placeholder="https://<id>-<region>.apps.astra.datastax.com" density="compact" />
          <v-text-field v-model="importForm.token" label="Application token" type="password" autocomplete="off" density="compact" />
          <v-row dense>
            <v-col cols="6"><v-text-field v-model="importForm.keyspace" label="Keyspace" density="compact" /></v-col>
            <v-col cols="6"><v-text-field v-model="importForm.collection" label="Collection" density="compact" /></v-col>
          </v-row>
          <v-select v-model="importForm.audience" :items="audienceOptions" :label="$t('kb_col_audience')" density="compact" />
          <v-alert v-if="importResult" type="success" variant="tonal" density="compact">
            {{ $t('kb_import_done', { docs: importResult.documents, chunks: importResult.chunks }) }}
          </v-alert>
          <v-alert v-if="importError" type="error" variant="tonal" density="compact">{{ importError }}</v-alert>
        </v-card-text>
        <v-card-actions>
          <v-spacer />
          <v-btn variant="text" @click="importDialog = false">{{ $t('cm_close') }}</v-btn>
          <v-btn color="primary" variant="flat" :loading="importing" :disabled="!importForm.api_endpoint || !importForm.token || !importForm.collection" @click="runImport">{{ $t('kb_import_run') }}</v-btn>
        </v-card-actions>
      </v-card>
    </v-dialog>

    <!-- Document view -->
    <v-dialog v-model="docDialog" max-width="900px">
      <v-card v-if="selected">
        <v-card-title class="d-flex align-center">
          <v-icon start>mdi-file-document-outline</v-icon>
          <v-text-field v-if="canEdit && view === 'active'" v-model="editTitle" density="compact" hide-details variant="underlined" class="mr-2" />
          <span v-else>{{ selected.title }}</span>
          <v-spacer />
          <v-btn icon="mdi-close" variant="text" size="small" @click="docDialog = false" />
        </v-card-title>
        <v-card-text>
          <v-row dense class="mb-2">
            <v-col cols="12" md="5">
              <v-select v-model="editAudience" :items="audienceOptions" :label="$t('kb_col_audience')" :disabled="!canEdit || view !== 'active'" density="compact" hide-details />
            </v-col>
            <v-col cols="12" md="7" class="d-flex align-center text-caption text-medium-emphasis">
              {{ selected.file_name || sourceLabel(selected.source) }} · {{ selected.char_count }} {{ $t('kb_chars') }} · {{ selected.chunk_count }} {{ $t('kb_col_chunks').toLowerCase() }}
            </v-col>
          </v-row>
          <div v-if="loadingChunks" class="text-center py-4"><v-progress-circular indeterminate size="24" /></div>
          <v-sheet v-for="c in chunks" :key="c.id" class="pa-3 mb-2 rounded" color="surface-variant">
            <div class="text-caption text-medium-emphasis mb-1">#{{ c.position + 1 }}</div>
            <div class="passage">{{ c.content }}</div>
          </v-sheet>
        </v-card-text>
        <v-card-actions v-if="canEdit && view === 'active'">
          <v-spacer />
          <v-btn color="primary" variant="flat" :loading="savingDoc" @click="saveDoc">{{ $t('save') }}</v-btn>
        </v-card-actions>
      </v-card>
    </v-dialog>

    <v-dialog :model-value="!!purgeTarget" max-width="440px" @update:model-value="(v) => { if (!v) purgeTarget = null }">
      <v-card v-if="purgeTarget">
        <v-card-title>{{ $t('kb_purge') }}</v-card-title>
        <v-card-text>{{ $t('kb_purge_confirm', { title: purgeTarget.title }) }}</v-card-text>
        <v-card-actions>
          <v-spacer />
          <v-btn variant="text" @click="purgeTarget = null">{{ $t('cancel') }}</v-btn>
          <v-btn color="error" variant="flat" :loading="purging" @click="purge">{{ $t('delete') }}</v-btn>
        </v-card-actions>
      </v-card>
    </v-dialog>

    <v-snackbar v-model="snack" :color="snackColor" timeout="4000">{{ snackText }}</v-snackbar>
  </div>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { useRoute } from 'vue-router'
import { useI18n } from 'vue-i18n'
import api from '../api'
import { useAuthStore } from '../stores/auth'

export interface KnowledgeDoc {
  id: string
  title: string
  file_name: string
  source: string
  audience: 'public' | 'internal'
  enabled: boolean
  status: 'processing' | 'ready' | 'failed' | 'archived'
  error?: string
  char_count: number
  chunk_count: number
  created_by: string
  created_at: string
}
interface Chunk { id: string; position: number; content: string }
interface Hit { title: string; content: string; score: number }

const { t } = useI18n()
const route = useRoute()
const authStore = useAuthStore()
const tenantId = computed(() => route.params.tenantId as string)
const base = computed(() => `/tenants/${tenantId.value}/knowledge`)
const canEdit = computed(() => authStore.canEdit('settings'))
const isAdmin = computed(() => ['owner', 'admin'].includes(authStore.tenantPerms?.role || ''))

const view = ref<'active' | 'archived'>('active')
const documents = ref<KnowledgeDoc[]>([])
const loading = ref(false)
const vectorStore = ref('')
const supported = ref<string[]>(['.pdf', '.docx', '.xlsx', '.txt', '.md', '.csv', '.html'])
const maxUploadMb = ref(20)
const acceptTypes = computed(() => supported.value.join(','))

const audienceOptions = computed(() => [
  { title: t('kb_audience_public'), value: 'public' },
  { title: t('kb_audience_internal'), value: 'internal' },
])
const agentOptions = computed(() => [
  { title: t('kb_as_customer'), value: 'public' },
  { title: t('kb_as_staff'), value: 'private' },
])

const snack = ref(false)
const snackText = ref('')
const snackColor = ref('success')
function notify(text: string, color = 'success') {
  snackText.value = text
  snackColor.value = color
  snack.value = true
}
function errMsg(e: any): string {
  return e?.response?.data?.message || e?.response?.data?.error || e?.message || 'error'
}

let poll: ReturnType<typeof setTimeout> | null = null
async function load() {
  loading.value = true
  try {
    const { data } = await api.get(base.value, { params: { view: view.value } })
    documents.value = data?.documents || []
    vectorStore.value = data?.vector_store || ''
    if (data?.supported_extensions?.length) supported.value = data.supported_extensions
    if (data?.max_upload_mb) maxUploadMb.value = data.max_upload_mb
  } finally {
    loading.value = false
  }
  schedulePoll()
}
function schedulePoll() {
  if (poll) clearTimeout(poll)
  poll = null
  if (documents.value.some((d) => d.status === 'processing')) {
    poll = setTimeout(load, 4000)
  }
}
onMounted(load)
onBeforeUnmount(() => { if (poll) clearTimeout(poll) })

// Upload
const uploadDialog = ref(false)
const uploadFiles = ref<File[] | File | null>([])
const uploadList = computed<File[]>(() => {
  const v = uploadFiles.value
  if (!v) return []
  return Array.isArray(v) ? v : [v]
})
const uploadAudience = ref<'public' | 'internal'>('public')
const uploading = ref(false)
const uploadErrors = ref<string[]>([])
async function upload() {
  uploading.value = true
  uploadErrors.value = []
  let ok = 0
  for (const file of uploadList.value) {
    const form = new FormData()
    form.append('file', file)
    form.append('audience', uploadAudience.value)
    try {
      await api.post(`${base.value}/upload`, form, { headers: { 'Content-Type': 'multipart/form-data' } })
      ok++
    } catch (e) {
      uploadErrors.value.push(`${file.name}: ${errMsg(e)}`)
    }
  }
  uploading.value = false
  if (ok > 0) notify(t('kb_uploaded', { n: ok }))
  if (uploadErrors.value.length === 0) {
    uploadDialog.value = false
    uploadFiles.value = []
  }
  view.value = 'active'
  await load()
}

// Text
const textDialog = ref(false)
const textTitle = ref('')
const textContent = ref('')
const textAudience = ref<'public' | 'internal'>('public')
const savingText = ref(false)
async function saveText() {
  savingText.value = true
  try {
    await api.post(`${base.value}/text`, { title: textTitle.value, content: textContent.value, audience: textAudience.value })
    textDialog.value = false
    textTitle.value = ''
    textContent.value = ''
    notify(t('kb_uploaded', { n: 1 }))
    view.value = 'active'
    await load()
  } catch (e) {
    notify(errMsg(e), 'error')
  } finally {
    savingText.value = false
  }
}

// Row actions
async function patchDoc(d: KnowledgeDoc, body: Partial<Pick<KnowledgeDoc, 'title' | 'audience' | 'enabled'>>) {
  try {
    await api.patch(`${base.value}/${d.id}`, body)
    await load()
  } catch (e) {
    notify(errMsg(e), 'error')
  }
}
async function reindex(d: KnowledgeDoc) {
  try {
    await api.post(`${base.value}/${d.id}/reindex`)
    await load()
  } catch (e) {
    notify(errMsg(e), 'error')
  }
}
async function archive(d: KnowledgeDoc) {
  try {
    await api.post(`${base.value}/${d.id}/archive`)
    notify(t('kb_archived_msg', { title: d.title }))
    await load()
  } catch (e) {
    notify(errMsg(e), 'error')
  }
}
async function restore(d: KnowledgeDoc) {
  try {
    await api.post(`${base.value}/${d.id}/restore`)
    notify(t('kb_restored_msg', { title: d.title }))
    await load()
  } catch (e) {
    notify(errMsg(e), 'error')
  }
}
const purgeTarget = ref<KnowledgeDoc | null>(null)
const purging = ref(false)
async function purge() {
  if (!purgeTarget.value) return
  purging.value = true
  try {
    await api.delete(`${base.value}/${purgeTarget.value.id}`)
    purgeTarget.value = null
    await load()
  } catch (e) {
    notify(errMsg(e), 'error')
  } finally {
    purging.value = false
  }
}

// Document dialog
const docDialog = ref(false)
const selected = ref<KnowledgeDoc | null>(null)
const chunks = ref<Chunk[]>([])
const loadingChunks = ref(false)
const editTitle = ref('')
const editAudience = ref<'public' | 'internal'>('public')
const savingDoc = ref(false)
async function openDoc(d: KnowledgeDoc) {
  selected.value = d
  editTitle.value = d.title
  editAudience.value = d.audience
  docDialog.value = true
  loadingChunks.value = true
  chunks.value = []
  try {
    const { data } = await api.get(`${base.value}/${d.id}`)
    chunks.value = data?.chunks || []
  } finally {
    loadingChunks.value = false
  }
}
async function saveDoc() {
  if (!selected.value) return
  const body: Record<string, unknown> = {}
  if (editTitle.value.trim() && editTitle.value.trim() !== selected.value.title) body.title = editTitle.value.trim()
  if (editAudience.value !== selected.value.audience) body.audience = editAudience.value
  if (Object.keys(body).length === 0) {
    docDialog.value = false
    return
  }
  savingDoc.value = true
  try {
    await patchDoc(selected.value, body)
    docDialog.value = false
  } finally {
    savingDoc.value = false
  }
}

// Try a question
const tryQuery = ref('')
const tryAgent = ref<'public' | 'private'>('public')
const searching = ref(false)
const searched = ref(false)
const hits = ref<Hit[]>([])
async function runSearch() {
  if (!tryQuery.value.trim()) return
  searching.value = true
  try {
    const { data } = await api.post(`${base.value}/search`, { query: tryQuery.value, agent_type: tryAgent.value })
    hits.value = data?.hits || []
    searched.value = true
  } catch (e) {
    notify(errMsg(e), 'error')
  } finally {
    searching.value = false
  }
}

// Astra import
const importDialog = ref(false)
const importForm = ref({ api_endpoint: '', token: '', keyspace: 'bbi_rag', collection: 'bbi_rag', audience: 'public' })
const importing = ref(false)
const importResult = ref<{ documents: number; chunks: number } | null>(null)
const importError = ref('')
async function runImport() {
  importing.value = true
  importResult.value = null
  importError.value = ''
  try {
    const { data } = await api.post(`${base.value}/import-astra`, importForm.value, { timeout: 330000 })
    importResult.value = data
    importForm.value.token = ''
    view.value = 'active'
    await load()
  } catch (e) {
    importError.value = errMsg(e)
  } finally {
    importing.value = false
  }
}

function statusColor(s: string) {
  return ({ ready: 'success', processing: 'info', failed: 'error', archived: 'grey' } as Record<string, string>)[s] || 'grey'
}
function sourceLabel(s: string) {
  return s === 'text' ? t('kb_source_text') : s === 'astra_import' ? t('kb_source_astra') : t('kb_source_upload')
}
function formatDateTime(v: string) {
  if (!v) return ''
  return new Date(v).toLocaleString('vi-VN', { dateStyle: 'short', timeStyle: 'short' })
}

defineExpose({ load, documents, upload, uploadFiles, runSearch, tryQuery, tryAgent, hits, patchDoc, archive, vectorStore })
</script>

<style scoped>
.passage { white-space: pre-wrap; font-size: 0.9rem; }
.error-cell { max-width: 280px; white-space: normal; }
</style>
