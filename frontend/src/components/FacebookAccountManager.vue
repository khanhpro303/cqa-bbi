<template>
  <v-alert v-if="linkFeedback" type="success" variant="tonal" class="mb-4" role="status">
    {{ linkFeedback }}
  </v-alert>
  <v-alert v-if="statusError" type="error" variant="tonal" class="mb-4">
    {{ statusError }}
    <template #append>
      <v-btn variant="text" :loading="statusLoading" @click="loadStatus">
        {{ t('retry') }}
      </v-btn>
    </template>
  </v-alert>

  <template v-if="status.enabled || status.linked">
    <div class="d-flex align-center mb-2">
      <div class="text-subtitle-2 font-weight-bold">
        <v-icon start size="small" color="#1877F2">mdi-facebook</v-icon>
        {{ t('facebook_account') }}
      </div>
      <v-spacer />
      <v-chip size="small" variant="tonal" :color="status.linked ? 'success' : 'default'">
        {{ status.linked ? t('facebook_linked') : t('facebook_not_linked') }}
      </v-chip>
    </div>

    <div v-if="status.linked && status.facebookName" class="text-body-2 mb-3">
      {{ status.facebookName }}
    </div>
    <div v-else class="text-body-2 text-medium-emphasis mb-3">
      {{ t('facebook_link_description') }}
    </div>

    <v-text-field
      v-model="currentPassword"
      :label="t('current_password')"
      type="password"
      autocomplete="current-password"
      prepend-inner-icon="mdi-lock"
      density="comfortable"
      class="mb-2"
    />

    <template v-if="status.enabled && !status.linked">
      <div class="d-flex align-center ga-2 flex-wrap">
        <v-btn
          color="#1877F2"
          variant="outlined"
          prepend-icon="mdi-facebook"
          :loading="linking"
          :disabled="statusLoading || !currentPassword"
          @click="linkAccount"
        >
          {{ t('facebook_link_action') }}
        </v-btn>
        <v-btn v-if="linking" variant="text" @click="cancelLinkAccount">
          {{ t('cancel') }}
        </v-btn>
      </div>
      <div v-if="linking" class="text-caption text-medium-emphasis mt-2" aria-live="polite">
        {{ t('facebook_link_redirecting') }}
      </div>
    </template>
    <v-btn
      v-else
      color="error"
      variant="text"
      prepend-icon="mdi-link-off"
      :loading="unlinking"
      :disabled="!currentPassword"
      @click="unlinkAccount"
    >
      {{ t('facebook_unlink_action') }}
    </v-btn>
  </template>

  <v-divider v-if="showDivider && (status.enabled || status.linked)" class="my-4" />

  <v-alert v-if="showUnavailable && !statusLoading && !statusError && !status.enabled && !status.linked" type="info" variant="tonal" class="mt-4">
    {{ t('facebook_login_not_configured') }}
  </v-alert>

  <v-snackbar v-model="snackbar" :color="snackbarColor" timeout="3000">{{ snackbarText }}</v-snackbar>
</template>

<script setup lang="ts">
import { onUnmounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRoute, useRouter } from 'vue-router'
import api from '../api'

const props = withDefaults(defineProps<{ active: boolean; showUnavailable?: boolean; showDivider?: boolean }>(), {
  showUnavailable: false,
  showDivider: false,
})

const { t } = useI18n()
const route = useRoute()
const router = useRouter()
const status = ref({ enabled: false, linked: false, facebookName: '' })
const currentPassword = ref('')
const linking = ref(false)
const unlinking = ref(false)
const statusLoading = ref(false)
const statusError = ref('')
const linkFeedback = ref('')
const snackbar = ref(false)
const snackbarText = ref('')
const snackbarColor = ref('success')
const facebookLoginAbort = ref<AbortController | null>(null)

function showSnack(text: string, color: string) {
  snackbarText.value = text
  snackbarColor.value = color
  snackbar.value = true
}

watch(() => props.active, (active) => {
  if (active) void loadStatus()
  else cancelLinkAccount()
}, { immediate: true })

onUnmounted(cancelLinkAccount)

function linkErrorMessage(code: string | undefined) {
  if (code === 'facebook_account_already_linked') return t('facebook_account_already_linked')
  if (code === 'wrong_current_password') return t('wrong_password')
  if (code === 'facebook_service_unavailable' || code === 'facebook_unavailable') return t('facebook_service_unavailable')
  if (code === 'oauth_denied') return t('facebook_link_cancelled')
  if (['invalid_state', 'browser_mismatch', 'session_expired', 'session_used'].includes(code || '')) return t('facebook_link_session_expired')
  return t('facebook_link_failed')
}

async function loadStatus() {
  statusLoading.value = true
  statusError.value = ''
  try {
    const { data } = await api.get('/profile/facebook', { timeout: 15000 })
    status.value = {
      enabled: data.enabled === true,
      linked: data.linked === true,
      facebookName: data.facebook_name || '',
    }
    const result = route.query.facebook_link
    if (typeof result === 'string') {
      if (result === 'success' && status.value.linked) linkFeedback.value = t('facebook_link_success')
      else statusError.value = linkErrorMessage(result)
      const { facebook_link: _result, ...query } = route.query
      await router.replace({ path: route.path, query, hash: route.hash })
    }
  } catch (error: any) {
    statusError.value = t('facebook_link_status_failed')
  } finally {
    statusLoading.value = false
  }
}

async function linkAccount() {
  if (!status.value.enabled || !currentPassword.value || linking.value) return
  linking.value = true
  statusError.value = ''
  linkFeedback.value = ''
  const abortController = new AbortController()
  facebookLoginAbort.value = abortController
  try {
    const { data } = await api.post('/profile/facebook/link/start', {
      current_password: currentPassword.value,
      return_path: route.name === 'settings' ? route.path : '/',
    }, { timeout: 15000, signal: abortController.signal })
    if (abortController.signal.aborted) return
    const redirect = new URL(data.redirect_url)
    if (redirect.protocol !== 'https:' || redirect.hostname !== 'www.facebook.com' || redirect.port || redirect.username || redirect.password) {
      throw new Error('invalid_facebook_redirect')
    }
    currentPassword.value = ''
    window.location.assign(redirect.href)
  } catch (error: any) {
    if (abortController.signal.aborted) return
    statusError.value = error?.code === 'ECONNABORTED'
      ? t('facebook_link_start_timeout')
      : linkErrorMessage(error?.response?.data?.error)
    showSnack(statusError.value, 'error')
  } finally {
    if (facebookLoginAbort.value === abortController) facebookLoginAbort.value = null
    linking.value = false
  }
}

function cancelLinkAccount() {
  facebookLoginAbort.value?.abort()
}

async function unlinkAccount() {
  if (unlinking.value || !window.confirm(t('facebook_unlink_confirm'))) return
  unlinking.value = true
  statusError.value = ''
  linkFeedback.value = ''
  try {
    await api.delete('/profile/facebook/link', { data: { current_password: currentPassword.value } })
    status.value = { ...status.value, linked: false, facebookName: '' }
    currentPassword.value = ''
    showSnack(t('facebook_unlink_success'), 'success')
  } catch (error: any) {
    statusError.value = error?.response?.data?.error === 'wrong_current_password'
      ? t('wrong_password')
      : t('facebook_unlink_failed')
    showSnack(statusError.value, 'error')
  } finally {
    unlinking.value = false
  }
}
</script>
