<template>
  <div>
    <v-alert
      v-if="errorMessage"
      type="error"
      variant="tonal"
      density="compact"
      class="mb-3"
      data-testid="facebook-connect-error"
    >
      <div>{{ errorMessage }}</div>
      <v-btn
        v-if="canRetry"
        class="mt-2"
        size="small"
        variant="text"
        :disabled="!canEdit"
        data-testid="facebook-connect-retry"
        @click="retryConnect"
      >
        {{ t('retry') }}
      </v-btn>
    </v-alert>

    <v-alert v-else-if="configLoaded && !configEnabled" type="warning" variant="tonal" density="compact" class="mb-3">
      {{ t('facebook_page_connect_disabled') }}
    </v-alert>

    <div class="rounded border pa-4 mb-3">
      <div class="d-flex align-start ga-3">
        <v-icon color="indigo" size="32">mdi-facebook</v-icon>
        <div class="flex-grow-1">
          <div class="text-subtitle-1 font-weight-bold">{{ t('facebook_page_connect_title') }}</div>
          <div class="text-body-2 text-medium-emphasis mt-1">{{ t('facebook_page_connect_description') }}</div>
        </div>
      </div>
      <v-btn
        block
        color="indigo"
        prepend-icon="mdi-facebook"
        class="mt-4"
        :loading="starting || loadingSession"
        :disabled="!canEdit || !configLoaded || !configEnabled || starting || loadingSession"
        data-testid="facebook-connect-start"
        @click="startConnect"
      >
        {{ t('facebook_page_connect_action') }}
      </v-btn>
      <div class="text-caption text-medium-emphasis mt-2">{{ t('facebook_page_connect_permissions_hint') }}</div>
    </div>

    <v-dialog v-model="selectionDialog" max-width="600" persistent>
      <v-card>
        <v-card-title class="px-6 pt-6 pb-2">{{ t('facebook_page_select_title') }}</v-card-title>
        <v-card-text class="px-6 py-2">
          <div class="text-body-2 text-medium-emphasis mb-4">{{ t('facebook_page_select_description') }}</div>

          <v-alert
            v-if="selectionError"
            type="error"
            variant="tonal"
            density="compact"
            class="mb-4"
            data-testid="facebook-page-selection-error"
          >
            {{ selectionError }}
          </v-alert>

          <v-alert v-if="!pages.length" type="warning" variant="tonal" class="mb-4">
            {{ t('facebook_page_no_pages') }}
          </v-alert>

          <v-list v-else class="border rounded mb-4" data-testid="facebook-page-list">
            <v-list-item
              v-for="page in pages"
              :key="page.id"
              :active="selectedPageId === page.id"
              :disabled="selecting"
              color="indigo"
              :data-testid="`facebook-page-${page.id}`"
              @click="selectedPageId = page.id"
            >
              <template #prepend>
                <v-radio :model-value="selectedPageId" :value="page.id" color="indigo" />
              </template>
              <v-list-item-title>{{ page.name }}</v-list-item-title>
              <v-list-item-subtitle>{{ page.id }}</v-list-item-subtitle>
            </v-list-item>
          </v-list>

          <v-select
            v-model="syncInterval"
            :items="syncIntervalOptions"
            :label="t('sync_interval')"
            density="compact"
            class="mb-3"
            :hint="t('sync_interval_hint')"
            persistent-hint
          />
          <v-alert v-if="syncInterval <= 5" type="warning" variant="tonal" density="compact" class="mb-3">
            {{ t('sync_interval_warning') }}
          </v-alert>
          <v-switch
            v-model="syncFiles"
            :label="t('sync_files')"
            :hint="t('sync_files_hint')"
            persistent-hint
            density="compact"
            color="primary"
          />
        </v-card-text>
        <v-card-actions class="px-6 pb-6 pt-2">
          <v-btn variant="text" :disabled="selecting" data-testid="facebook-page-cancel" @click="cancelSelection">{{ t('cancel') }}</v-btn>
          <v-spacer />
          <v-btn
            color="indigo"
            :loading="selecting"
            :disabled="!canEdit || !selectedPageId || selecting"
            data-testid="facebook-page-confirm"
            @click="selectPage"
          >
            {{ t('facebook_page_connect_selected') }}
          </v-btn>
        </v-card-actions>
      </v-card>
    </v-dialog>
  </div>
</template>

<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import api from '../../api'

interface FacebookPageOption {
  id: string
  name: string
}

const props = defineProps<{
  tenantId: string
  canEdit: boolean
  syncIntervalOptions: Array<{ title: string; value: number }>
}>()

const emit = defineEmits<{
  connected: [channel: Record<string, any>]
}>()

const { t } = useI18n()
const configLoaded = ref(false)
const configEnabled = ref(false)
const starting = ref(false)
const loadingSession = ref(false)
const selecting = ref(false)
const selectionDialog = ref(false)
const sessionId = ref('')
const pages = ref<FacebookPageOption[]>([])
const selectedPageId = ref('')
const syncInterval = ref(15)
const syncFiles = ref(false)
const errorMessage = ref('')
const selectionError = ref('')
const canRetry = ref(false)

const errorKeys: Record<string, string> = {
  oauth_denied: 'facebook_page_error_denied',
  access_denied: 'facebook_page_error_denied',
  invalid_state: 'facebook_page_error_invalid_state',
  session_not_found: 'facebook_page_error_invalid_state',
  browser_mismatch: 'facebook_page_error_invalid_state',
  facebook_connect_session_not_found: 'facebook_page_error_invalid_state',
  facebook_connect_session_invalid: 'facebook_page_error_invalid_state',
  session_expired: 'facebook_page_error_expired',
  oauth_session_expired: 'facebook_page_error_expired',
  facebook_connect_session_expired: 'facebook_page_error_expired',
  session_used: 'facebook_page_error_replayed',
  oauth_session_used: 'facebook_page_error_replayed',
  state_already_used: 'facebook_page_error_replayed',
  facebook_connect_session_used: 'facebook_page_error_replayed',
  no_pages: 'facebook_page_error_no_pages',
  missing_permissions: 'facebook_page_error_permissions',
  permissions_missing: 'facebook_page_error_permissions',
  permission_denied: 'facebook_page_error_permissions',
  page_not_found: 'facebook_page_error_page_not_found',
  page_not_authorized: 'facebook_page_error_page_not_found',
  facebook_page_not_authorized: 'facebook_page_error_page_not_found',
  subscription_failed: 'facebook_page_error_subscription',
  failed_subscription: 'facebook_page_error_subscription',
  facebook_page_subscription_failed: 'facebook_page_error_subscription',
  facebook_unavailable: 'facebook_page_error_service',
  provider_unavailable: 'facebook_page_error_service',
  graph_api_error: 'facebook_page_error_service',
  facebook_page_oauth_unavailable: 'facebook_page_connect_disabled',
  connect_failed: 'facebook_page_error_generic',
  facebook_connect_failed: 'facebook_page_error_generic',
  facebook_channel_save_failed: 'facebook_page_error_generic',
  session_store_failed: 'facebook_page_error_generic',
}

function errorCode(error: any): string {
  return String(error?.response?.data?.code || error?.response?.data?.error || error?.message || '').trim()
}

function showError(code: string, retry = true) {
  const key = errorKeys[code] || 'facebook_page_error_generic'
  errorMessage.value = t(key)
  canRetry.value = retry
}

function translatedError(code: string) {
  return t(errorKeys[code] || 'facebook_page_error_generic')
}

function clearCallbackParams() {
  const url = new URL(window.location.href)
  url.searchParams.delete('facebook_connect')
  url.searchParams.delete('facebook_connect_error')
  window.history.replaceState({}, '', `${url.pathname}${url.search}${url.hash}`)
}

async function loadConfig() {
  errorMessage.value = ''
  canRetry.value = false
  try {
    const { data } = await api.get(`/tenants/${props.tenantId}/facebook/connect/config`)
    configEnabled.value = data?.enabled === true
  } catch (error) {
    showError(errorCode(error), true)
  } finally {
    configLoaded.value = true
  }
}

async function retryConnect() {
  if (!configEnabled.value) {
    await loadConfig()
    return
  }
  await startConnect()
}

async function startConnect() {
  if (!props.canEdit || starting.value) return
  starting.value = true
  errorMessage.value = ''
  canRetry.value = false
  try {
    const { data } = await api.post(`/tenants/${props.tenantId}/facebook/connect`)
    if (!data?.redirect_url) throw new Error('connect_failed')
    window.location.href = data.redirect_url
  } catch (error) {
    showError(errorCode(error), true)
  } finally {
    starting.value = false
  }
}

async function loadSession(id: string) {
  loadingSession.value = true
  errorMessage.value = ''
  try {
    const { data } = await api.get(`/tenants/${props.tenantId}/facebook/connect/${encodeURIComponent(id)}`)
    pages.value = Array.isArray(data?.pages) ? data.pages : []
    if (!pages.value.length) {
      showError('no_pages', true)
      clearCallbackParams()
      return
    }
    sessionId.value = id
    selectionError.value = ''
    selectedPageId.value = pages.value.length === 1 ? pages.value[0]!.id : ''
    selectionDialog.value = true
  } catch (error) {
    showError(errorCode(error), true)
    clearCallbackParams()
  } finally {
    loadingSession.value = false
  }
}

function cancelSelection() {
  selectionDialog.value = false
  sessionId.value = ''
  selectedPageId.value = ''
  pages.value = []
  selectionError.value = ''
  clearCallbackParams()
}

async function selectPage() {
  if (!props.canEdit || !sessionId.value || !selectedPageId.value || selecting.value) return
  selecting.value = true
  selectionError.value = ''
  try {
    const { data } = await api.post(
      `/tenants/${props.tenantId}/facebook/connect/${encodeURIComponent(sessionId.value)}/select`,
      {
        page_id: selectedPageId.value,
        sync_interval: syncInterval.value,
        sync_files: syncFiles.value,
      },
    )
    selectionDialog.value = false
    clearCallbackParams()
    emit('connected', data)
  } catch (error) {
    selectionError.value = translatedError(errorCode(error))
  } finally {
    selecting.value = false
  }
}

onMounted(async () => {
  await loadConfig()
  const params = new URLSearchParams(window.location.search)
  const callbackError = params.get('facebook_connect_error')
  if (callbackError) {
    showError(callbackError, true)
    clearCallbackParams()
    return
  }
  const callbackSession = params.get('facebook_connect')
  if (callbackSession) await loadSession(callbackSession)
})
</script>
