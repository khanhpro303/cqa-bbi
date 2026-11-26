<template>
  <div>
    <h1 class="text-h5 font-weight-bold mb-2">{{ $t('nav_knowledge') }}</h1>
    <div class="text-grey-darken-1 mb-4">{{ $t('kb_page_desc') }}</div>

    <v-tabs v-model="section" color="primary" class="mb-5" data-testid="knowledge-sections">
      <v-tab value="documents" prepend-icon="mdi-file-document-multiple-outline">{{ $t('kb_section_documents') }}</v-tab>
      <v-tab value="products" prepend-icon="mdi-package-variant-closed">{{ $t('kb_section_products') }}</v-tab>
    </v-tabs>

    <ProductAdvisory v-if="section === 'products'" :tenant-id="tenantId" :can-edit="canEdit" />

    <template v-else>
    <v-alert v-if="parser === 'local'" type="info" variant="tonal" density="compact" class="mb-4" data-testid="kb-local-parser">
      {{ $t('kb_parser_local') }} <router-link :to="`/${tenantId}/ai-engines`">{{ $t('nav_ai_engines') }}</router-link>.
    </v-alert>

    <v-alert v-if="vectorStore === 'none'" type="warning" variant="tonal" density="compact" class="mb-4" data-testid="kb-keyword-warning">
      {{ $t('kb_keyword_only') }}
    </v-alert>

    <KnowledgePlayground :base="base" :is-admin="isAdmin" />

    <div class="d-flex flex-wrap align-center ga-2 mb-3">
      <v-tabs v-model="view" color="primary" density="compact" @update:model-value="load">
        <v-tab value="active" data-testid="tab-active">{{ $t('kb_tab_active') }}</v-tab>
        <v-tab value="archived" data-testid="tab-archived">{{ $t('kb_tab_archived') }}</v-tab>
      </v-tabs>
      <v-spacer />
      <template v-if="canEdit">
        <v-btn color="primary" prepend-icon="mdi-upload" @click="uploadDialog = true">{{ $t('kb_upload') }}</v-btn>
        <v-btn variant="outlined" prepend-icon="mdi-text-box-plus-outline" @click="textDialog = true">{{ $t('kb_add_text') }}</v-btn>
        <v-btn variant="outlined" prepend-icon="mdi-link-variant" data-testid="kb-add-link" @click="linkDialog = true">{{ $t('kb_add_link') }}</v-btn>
      </template>
    </div>

    <v-alert v-if="listError" type="error" variant="tonal" density="compact" class="mb-3">
      {{ $t('kb_list_failed') }}
      <v-btn variant="text" size="small" @click="load">{{ $t('kb_retry') }}</v-btn>
    </v-alert>
    <v-card class="document-table" variant="outlined">
      <v-table density="comfortable" hover>
        <thead>
          <tr>
            <th>{{ $t('kb_col_title') }}</th>
            <th>{{ $t('kb_col_audience') }}</th>
            <th>{{ $t('kb_col_status') }}</th>
            <th>{{ $t('kb_col_added') }}</th>
            <th v-if="view === 'active'">{{ $t('kb_col_enabled') }}</th>
            <th class="text-right" />
          </tr>
        </thead>
        <tbody>
          <tr v-for="d in documents" :key="d.id">
            <td class="py-2">
              <button type="button" class="document-link" aria-haspopup="dialog" @click="openDoc(d, $event)">{{ d.title }}</button>
              <div class="text-caption text-medium-emphasis">{{ documentSource(d) }}</div>
            </td>
            <td>
              <v-chip size="small" :color="d.audience === 'internal' ? 'deep-purple' : 'teal'" variant="tonal">
                {{ d.audience === 'internal' ? $t('kb_audience_internal') : $t('kb_audience_public') }}
              </v-chip>
            </td>
            <td>
              <v-chip size="small" :color="statusColor(d.status)" variant="tonal">{{ $t(statusLabel(d)) }}</v-chip>
              <div v-if="d.status === 'failed'" class="text-caption text-medium-emphasis mt-1">
                {{ $t(d.chunk_count ? 'kb_index_failed' : d.source === 'url' ? 'kb_link_extract_failed' : 'kb_extract_failed') }}
              </div>
            </td>
            <td>
              <div>{{ formatDateTime(d.created_at) }}</div>
              <div v-if="d.created_by" class="text-caption text-medium-emphasis">{{ d.created_by }}</div>
            </td>
            <td v-if="view === 'active'">
              <v-switch :model-value="d.enabled" :disabled="!canEdit" :aria-label="`${$t('kb_col_enabled')} ${d.title}`" color="success" density="compact" hide-details @update:model-value="(v) => patchDoc(d, { enabled: !!v })" />
            </td>
            <td class="text-right text-no-wrap">
              <template v-if="canEdit && view === 'active'">
                <v-btn v-if="d.status === 'failed' && d.enabled && d.chunk_count" size="small" variant="tonal" prepend-icon="mdi-refresh" :title="$t('kb_reindex')" @click="reindex(d)">
                  {{ $t('kb_retry') }}
                </v-btn>
                <v-btn v-else icon="mdi-refresh" size="small" variant="text" :title="$t('kb_reindex')" :aria-label="$t('kb_reindex')" :disabled="!d.enabled || !d.chunk_count" @click="reindex(d)" />
                <v-btn icon="mdi-archive-arrow-down-outline" size="small" variant="text" :title="$t('kb_archive')" :aria-label="$t('kb_archive')" @click="archive(d)" />
              </template>
              <template v-if="canEdit && view === 'archived'">
                <v-btn icon="mdi-archive-arrow-up-outline" size="small" variant="text" :title="$t('kb_restore')" :aria-label="$t('kb_restore')" @click="restore(d)" />
                <v-btn v-if="isAdmin" icon="mdi-delete-forever" size="small" variant="text" color="error" :title="$t('kb_purge')" :aria-label="$t('kb_purge')" @click="purgeTarget = d" />
              </template>
            </td>
          </tr>
          <tr v-if="!loading && !listError && documents.length === 0">
            <td :colspan="view === 'active' ? 6 : 5" class="text-center text-medium-emphasis py-6">{{ view === 'archived' ? $t('kb_no_archived') : $t('kb_empty') }}</td>
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

    <v-dialog v-model="linkDialog" :aria-label="$t('kb_add_link')" :persistent="savingLink" max-width="560px">
      <v-card>
        <v-card-title>{{ $t('kb_add_link') }}</v-card-title>
        <form data-testid="kb-link-form" novalidate @submit.prevent="saveLink">
          <v-card-text>
            <v-alert v-if="firecrawlEnabled === false" type="info" variant="tonal" density="compact" class="mb-4" data-testid="kb-firecrawl-setup">
              {{ $t('kb_link_setup') }} <router-link :to="`/${tenantId}/ai-engines`">{{ $t('nav_ai_engines') }}</router-link>.
            </v-alert>
            <v-alert v-else-if="firecrawlEnabled === null && listError" type="error" variant="tonal" density="compact" class="mb-4">
              {{ $t('firecrawl_load_failed') }}
              <v-btn type="button" variant="text" size="small" @click="load">{{ $t('kb_retry') }}</v-btn>
            </v-alert>
            <v-text-field v-model="linkForm.url" :label="$t('kb_link_url')" :hint="$t('kb_link_public_hint')" persistent-hint type="url" placeholder="https://example.com" maxlength="2048" density="compact" :disabled="savingLink" data-testid="kb-link-url" />
            <v-text-field v-model="linkForm.title" :label="$t('kb_link_title')" density="compact" maxlength="250" :disabled="savingLink" data-testid="kb-link-title" />
            <v-select v-model="linkForm.audience" :items="audienceOptions" :label="$t('kb_col_audience')" density="compact" :disabled="savingLink" data-testid="kb-link-audience" />
            <v-alert v-if="linkError" type="error" variant="tonal" density="compact" data-testid="kb-link-error">
              {{ $t(linkError) }}
            </v-alert>
          </v-card-text>
          <v-card-actions>
            <v-spacer />
            <v-btn type="button" variant="text" :disabled="savingLink" @click="linkDialog = false">{{ $t('cancel') }}</v-btn>
            <v-btn type="submit" color="primary" variant="flat" :loading="savingLink" :disabled="!firecrawlEnabled || !linkForm.url.trim() || savingLink">
              {{ $t('kb_link_run') }}
            </v-btn>
          </v-card-actions>
        </form>
      </v-card>
    </v-dialog>

    <v-dialog v-model="docDialog" :aria-label="selected?.title" max-width="760px" scrollable @after-leave="restoreDocumentFocus">
      <v-card v-if="selected">
        <div class="d-flex align-start justify-space-between ga-3 pa-4 pa-sm-6">
          <div class="document-heading">
            <h2 class="text-h6">{{ selected.title }}</h2>
            <div class="d-flex align-center flex-wrap ga-2 mt-2">
              <span class="text-caption text-medium-emphasis">{{ documentSource(selected) }}</span>
              <a v-if="selectedSourceURL" :href="selectedSourceURL" target="_blank" rel="noopener noreferrer" class="text-caption" data-testid="kb-source-url">
                {{ $t('kb_link_open_source') }}
              </a>
              <v-chip size="small" variant="tonal">{{ $t(selected.audience === 'internal' ? 'kb_audience_internal' : 'kb_audience_public') }}</v-chip>
            </div>
          </div>
          <v-btn icon="mdi-close" variant="text" size="small" :aria-label="$t('cm_close')" @click="docDialog = false" />
        </div>
        <v-divider />
        <v-card-text class="px-4 px-sm-6 py-5">
          <section aria-live="polite">
            <h3 class="text-subtitle-1 font-weight-bold mb-3">{{ $t('kb_summary_title') }}</h3>
            <p v-if="summary?.text" class="document-summary">{{ summary.text }}</p>
            <template v-else-if="loadingDetail || generatingSummary || summary?.status === 'processing'">
              <div class="d-flex align-center ga-3 text-medium-emphasis" role="status">
                <v-progress-circular indeterminate size="18" width="2" color="primary" />
                {{ $t(loadingDetail ? 'kb_summary_loading' : 'kb_summary_processing') }}
              </div>
            </template>
            <p v-else-if="!summaryError && summary?.status !== 'failed'" class="text-body-2 text-medium-emphasis">
              {{ $t(detailExtracting ? statusLabel(selected) : selected.chunk_count ? 'kb_summary_missing' : 'kb_summary_no_text') }}
            </p>
            <v-alert v-if="summaryError || summary?.status === 'failed'" type="error" variant="tonal" density="compact" class="mt-3">
              {{ $t(summaryError || 'kb_summary_failed') }}
            </v-alert>
            <v-btn v-if="canGenerateSummary" data-testid="kb-generate-summary" class="mt-4" color="primary" variant="flat" prepend-icon="mdi-text-box-check-outline" @click="generateSummary">
              {{ $t(summary?.status === 'failed' || summaryError ? 'kb_retry' : 'kb_summary_generate') }}
            </v-btn>
            <v-btn v-else-if="summaryError === 'kb_summary_load_failed'" class="mt-3" variant="text" @click="openDoc(selected)">
              {{ $t('kb_retry') }}
            </v-btn>
          </section>
          <section v-if="editingDoc" class="mt-6">
            <v-divider class="mb-4" />
            <v-text-field v-model="editTitle" :label="$t('kb_col_title')" density="compact" variant="outlined" maxlength="250" />
            <v-select v-model="editAudience" :items="audienceOptions" :label="$t('kb_col_audience')" density="compact" variant="outlined" hide-details />
          </section>
        </v-card-text>
        <v-card-actions v-if="canEdit && selected.status !== 'archived'" class="px-4 px-sm-6">
          <v-btn variant="text" :prepend-icon="editingDoc ? undefined : 'mdi-cog-outline'" :disabled="savingDoc" @click="toggleDocSettings">
            {{ $t(editingDoc ? 'cancel' : 'kb_document_settings') }}
          </v-btn>
          <v-spacer />
          <v-btn v-if="editingDoc" color="primary" variant="flat" :disabled="!editTitle.trim()" :loading="savingDoc" @click="saveDoc">{{ $t('save') }}</v-btn>
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
    </template>

    <v-snackbar v-model="snack" :color="snackColor" timeout="4000">{{ snackText }}</v-snackbar>
  </div>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useRoute } from 'vue-router'
import { useI18n } from 'vue-i18n'
import api from '../api'
import KnowledgePlayground from '../components/KnowledgePlayground.vue'
import ProductAdvisory from '../components/ProductAdvisory.vue'
import { useAuthStore } from '../stores/auth'

export interface KnowledgeDoc {
  id: string
  title: string
  file_name: string
  source: string
  source_url?: string
  audience: 'public' | 'internal'
  enabled: boolean
  status: 'processing' | 'ready' | 'failed' | 'archived'
  error?: string
  parser?: string
  char_count: number
  chunk_count: number
  created_by: string
  created_at: string
}
type Summary = { status: 'missing' | 'processing' | 'ready' | 'failed'; text?: string }

const { t } = useI18n()
const route = useRoute()
const authStore = useAuthStore()
const tenantId = computed(() => route.params.tenantId as string)
const base = computed(() => `/tenants/${tenantId.value}/knowledge`)
const canEdit = computed(() => authStore.canEdit('settings'))
const isAdmin = computed(() => ['owner', 'admin'].includes(authStore.tenantPerms?.role || ''))

const section = ref<'documents' | 'products'>('documents')
const view = ref<'active' | 'archived'>('active')
const documents = ref<KnowledgeDoc[]>([])
const loading = ref(false)
const listError = ref(false)
const vectorStore = ref('')
const parser = ref('')
const firecrawlEnabled = ref<boolean | null>(null)
const supported = ref<string[]>(['.pdf', '.docx', '.xlsx', '.txt', '.md', '.csv', '.html'])
const maxUploadMb = ref(20)
const acceptTypes = computed(() => supported.value.join(','))

const audienceOptions = computed(() => [
  { title: t('kb_audience_public'), value: 'public' },
  { title: t('kb_audience_internal'), value: 'internal' },
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
let listRequest = 0
let listScope = ''
async function load() {
  const request = ++listRequest
  if (poll) clearTimeout(poll)
  const scope = `${base.value}/${view.value}`
  if (scope !== listScope) {
    listScope = scope
    documents.value = []
    listError.value = false
    firecrawlEnabled.value = null
  }
  loading.value = true
  try {
    const { data } = await api.get(base.value, { params: { view: view.value } })
    if (request !== listRequest) return
    documents.value = data?.documents || []
    listError.value = false
    vectorStore.value = data?.vector_store || ''
    parser.value = data?.parser || ''
    firecrawlEnabled.value = data?.firecrawl_enabled === true
    if (data?.supported_extensions?.length) supported.value = data.supported_extensions
    if (data?.max_upload_mb) maxUploadMb.value = data.max_upload_mb
  } catch {
    if (request === listRequest) listError.value = true
  } finally {
    if (request === listRequest) {
      loading.value = false
      schedulePoll()
    }
  }
}
function schedulePoll() {
  if (poll) clearTimeout(poll)
  poll = null
  if (documents.value.some((d) => d.enabled && d.status === 'processing')) {
    poll = setTimeout(load, 4000)
  }
}
onMounted(load)
onBeforeUnmount(() => {
  ++listRequest
  if (poll) clearTimeout(poll)
  closeDetail()
})

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

const linkDialog = ref(false)
const linkForm = ref({ url: '', title: '', audience: 'public' })
const savingLink = ref(false)
const linkError = ref('')
let linkRequest = 0

function httpURL(raw: string) {
  if (!/^https?:\/\//i.test(raw)) return null
  try {
    const url = new URL(raw)
    return url.hostname && !url.username && !url.password ? url.href : null
  } catch {
    return null
  }
}

async function saveLink() {
  if (savingLink.value || !firecrawlEnabled.value) return
  const url = linkForm.value.url.trim()
  if (!httpURL(url) || new TextEncoder().encode(url).length > 2048) {
    linkError.value = 'kb_link_invalid'
    return
  }
  const request = ++linkRequest
  const endpoint = base.value
  savingLink.value = true
  linkError.value = ''
  try {
    await api.post(`${endpoint}/url`, { ...linkForm.value, url, title: linkForm.value.title.trim() })
    if (request !== linkRequest) return
    linkDialog.value = false
    linkForm.value = { url: '', title: '', audience: 'public' }
    notify(t('kb_link_submitted'))
    view.value = 'active'
    await load()
  } catch (e: any) {
    if (request !== linkRequest) return
    switch (e?.response?.data?.error) {
      case 'firecrawl_not_configured':
        firecrawlEnabled.value = false
        break
      case 'invalid_url':
        linkError.value = 'kb_link_invalid'
        break
      default:
        linkError.value = 'kb_link_submit_failed'
    }
  } finally {
    if (request === linkRequest) savingLink.value = false
  }
}

// Row actions
async function patchDoc(d: KnowledgeDoc, body: Partial<Pick<KnowledgeDoc, 'title' | 'audience' | 'enabled'>>) {
  try {
    const { data } = await api.patch<KnowledgeDoc>(`${base.value}/${d.id}`, body)
    await load()
    return data
  } catch (e) {
    notify(t(errMsg(e) === 'index_busy' ? 'kb_index_busy' : 'kb_update_failed'), 'error')
    return null
  }
}
async function reindex(d: KnowledgeDoc) {
  try {
    await api.post(`${base.value}/${d.id}/reindex`)
    await load()
  } catch (e) {
    notify(t(errMsg(e) === 'index_busy' ? 'kb_index_busy' : 'kb_reindex_failed'), 'error')
  }
}
async function archive(d: KnowledgeDoc) {
  try {
    await api.post(`${base.value}/${d.id}/archive`)
    notify(t('kb_archived_msg', { title: d.title }))
    await load()
  } catch (e) {
    notify(t(errMsg(e) === 'index_busy' ? 'kb_index_busy' : 'kb_archive_failed'), 'error')
  }
}
async function restore(d: KnowledgeDoc) {
  try {
    await api.post(`${base.value}/${d.id}/restore`)
    notify(t('kb_restored_msg', { title: d.title }))
    await load()
  } catch (e) {
    notify(t(errMsg(e) === 'index_busy' ? 'kb_index_busy' : 'kb_restore_failed'), 'error')
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
    notify(t(errMsg(e) === 'index_busy' ? 'kb_index_busy' : 'kb_purge_failed'), 'error')
  } finally {
    purging.value = false
  }
}

// Document dialog
const docDialog = ref(false)
const selected = ref<KnowledgeDoc | null>(null)
const selectedSourceURL = computed(() => httpURL(selected.value?.source_url || ''))
const summary = ref<Summary | null>(null)
const loadingDetail = ref(false)
const generatingSummary = ref(false)
const summaryError = ref('')
const editingDoc = ref(false)
const editTitle = ref('')
const editAudience = ref<'public' | 'internal'>('public')
let editOriginal: Pick<KnowledgeDoc, 'title' | 'audience'> | null = null
const savingDoc = ref(false)
const detailExtracting = computed(() => selected.value?.status === 'processing' && !selected.value.chunk_count)
const canGenerateSummary = computed(() => canEdit.value && !!selected.value?.chunk_count && !summary.value?.text &&
  !loadingDetail.value && !generatingSummary.value && summary.value?.status !== 'processing' &&
  !['kb_summary_load_failed', 'kb_summary_too_large'].includes(summaryError.value))
let detailRequest = 0
let documentRevision = 0
let detailPoll: ReturnType<typeof setTimeout> | undefined
let docOpener: HTMLElement | null = null

function closeDetail() {
  ++detailRequest
  clearTimeout(detailPoll)
}
watch(docDialog, (open) => { if (!open) closeDetail() })
watch(base, () => {
  closeDetail()
  docDialog.value = false
  linkRequest++
  linkDialog.value = false
  linkForm.value = { url: '', title: '', audience: 'public' }
  savingLink.value = false
  linkError.value = ''
  documents.value = []
  view.value = 'active'
  void load()
})

function restoreDocumentFocus() {
  if (!docDialog.value && docOpener?.isConnected) docOpener.focus()
}

async function openDoc(d: KnowledgeDoc, event?: MouseEvent) {
  closeDetail()
  if (event?.currentTarget instanceof HTMLElement) docOpener = event.currentTarget
  selected.value = d
  summary.value = null
  summaryError.value = ''
  loadingDetail.value = true
  generatingSummary.value = false
  savingDoc.value = false
  editingDoc.value = false
  docDialog.value = true
  await loadDetail(d.id, detailRequest)
}

async function loadDetail(id: string, request: number) {
  const revision = documentRevision
  try {
    const { data } = await api.get<{ document: KnowledgeDoc; summary: Summary }>(`${base.value}/${id}`)
    if (!docDialog.value || request !== detailRequest) return
    if (revision === documentRevision) selected.value = data.document
    summary.value = data.summary
    summaryError.value = ''
  } catch {
    if (docDialog.value && request === detailRequest) summaryError.value = 'kb_summary_load_failed'
  } finally {
    if (docDialog.value && request === detailRequest) {
      loadingDetail.value = false
      scheduleDetailPoll(id, request)
    }
  }
}

function scheduleDetailPoll(id: string, request: number) {
  clearTimeout(detailPoll)
  if (summary.value?.status === 'processing' || detailExtracting.value) {
    detailPoll = setTimeout(() => loadDetail(id, request), 4000)
  }
}

async function generateSummary() {
  if (!selected.value || !canGenerateSummary.value) return
  const { id } = selected.value
  const request = detailRequest
  generatingSummary.value = true
  summaryError.value = ''
  clearTimeout(detailPoll)
  try {
    const { data } = await api.post<{ summary: Summary }>(`${base.value}/${id}/summary`)
    if (!docDialog.value || request !== detailRequest) return
    summary.value = data.summary
    scheduleDetailPoll(id, request)
  } catch (e) {
    if (!docDialog.value || request !== detailRequest) return
    const code = errMsg(e)
    switch (code) {
      case 'summary_no_ai':
      case 'summary_no_text':
      case 'summary_too_large':
      case 'summary_busy':
        summaryError.value = `kb_${code}`
        break
      default:
        summaryError.value = 'kb_summary_failed'
    }
  } finally {
    if (request === detailRequest) generatingSummary.value = false
  }
}

function toggleDocSettings() {
  if (!selected.value) return
  editingDoc.value = !editingDoc.value
  if (editingDoc.value) {
    const { title, audience } = selected.value
    editOriginal = { title, audience }
    editTitle.value = title
    editAudience.value = audience
  }
}

async function saveDoc() {
  if (!selected.value || !editOriginal) return
  const request = detailRequest
  const body: Partial<Pick<KnowledgeDoc, 'title' | 'audience'>> = {}
  const title = editTitle.value.trim()
  if (title && title !== editOriginal.title) body.title = title
  if (editAudience.value !== editOriginal.audience) body.audience = editAudience.value
  if (Object.keys(body).length === 0) {
    editingDoc.value = false
    return
  }
  savingDoc.value = true
  try {
    const updated = await patchDoc(selected.value, body)
    if (updated && docDialog.value && request === detailRequest) {
      ++documentRevision
      selected.value = updated
      editingDoc.value = false
    }
  } finally {
    if (request === detailRequest) savingDoc.value = false
  }
}

function statusColor(s: string) {
  return ({ ready: 'success', processing: 'info', failed: 'error', archived: 'grey' } as Record<string, string>)[s] || 'grey'
}
function statusLabel(doc: KnowledgeDoc) {
  if (doc.status === 'processing') {
    if (!doc.enabled) return 'kb_status_paused'
    if (!doc.chunk_count) return doc.source === 'url' ? 'kb_link_extracting' : 'kb_status_extracting'
  }
  return 'kb_status_' + doc.status
}
function documentSource(doc: KnowledgeDoc) {
  return doc.file_name && doc.file_name !== doc.title ? doc.file_name : sourceLabel(doc.source)
}
function sourceLabel(s: string) {
  if (s === 'url') return t('kb_source_link')
  return s === 'text' ? t('kb_source_text') : s === 'astra_import' ? t('kb_source_astra') : t('kb_source_upload')
}
function formatDateTime(v: string) {
  if (!v) return ''
  return new Date(v).toLocaleString('vi-VN', { dateStyle: 'short', timeStyle: 'short' })
}

defineExpose({ load, documents, parser, upload, uploadFiles, patchDoc, archive, vectorStore })
</script>

<style scoped>
.document-table { border-color: rgba(var(--v-border-color), var(--v-border-opacity)); }
.document-link {
  appearance: none;
  background: transparent;
  border: 0;
  padding: 0;
  color: inherit;
  font: inherit;
  font-weight: 600;
  text-align: start;
  overflow-wrap: anywhere;
  cursor: pointer;
}
.document-link:hover, .document-link:focus-visible { text-decoration: underline; }
.document-link:focus-visible { outline: 2px solid rgb(var(--v-theme-primary)); outline-offset: 3px; }
.document-heading { min-width: 0; overflow-wrap: anywhere; }
.document-summary { white-space: pre-wrap; overflow-wrap: anywhere; font-size: 1rem; line-height: 1.75; }
</style>
