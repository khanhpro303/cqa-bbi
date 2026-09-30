<template>
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

    <v-btn
      v-if="status.enabled && !status.linked"
      color="#1877F2"
      variant="outlined"
      prepend-icon="mdi-facebook"
      :loading="linking"
      :disabled="!facebookSDK || !currentPassword"
      @click="linkAccount"
    >
      {{ t('facebook_link_action') }}
    </v-btn>
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

  <v-alert v-else-if="showUnavailable" type="info" variant="tonal">
    {{ t('facebook_login_not_configured') }}
  </v-alert>

  <v-snackbar v-model="snackbar" :color="snackbarColor" timeout="3000">{{ snackbarText }}</v-snackbar>
</template>

<script setup lang="ts">
import { ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import api from '../api'
import { loadFacebookSDK, requestFacebookLogin } from '../utils/facebook-sdk'

const props = withDefaults(defineProps<{ active: boolean; showUnavailable?: boolean; showDivider?: boolean }>(), {
  showUnavailable: false,
  showDivider: false,
})

const { t } = useI18n()
const status = ref({ enabled: false, linked: false, facebookName: '' })
const facebookSDK = ref<Awaited<ReturnType<typeof loadFacebookSDK>> | null>(null)
const currentPassword = ref('')
const linking = ref(false)
const unlinking = ref(false)
const statusLoading = ref(false)
const statusError = ref('')
const snackbar = ref(false)
const snackbarText = ref('')
const snackbarColor = ref('success')

function showSnack(text: string, color: string) {
  snackbarText.value = text
  snackbarColor.value = color
  snackbar.value = true
}

watch(() => props.active, (active) => {
  if (active) void loadStatus()
}, { immediate: true })

async function loadStatus() {
  statusLoading.value = true
  statusError.value = ''
  try {
    const { data } = await api.get('/profile/facebook')
    status.value = {
      enabled: data.enabled === true,
      linked: data.linked === true,
      facebookName: data.facebook_name || '',
    }
    if (!data.enabled || facebookSDK.value) return

    const configResponse = await api.get('/auth/facebook/config')
    if (!configResponse.data.enabled) return
    facebookSDK.value = await loadFacebookSDK({
      appId: configResponse.data.app_id,
      apiVersion: configResponse.data.api_version,
    })
  } catch (error: any) {
    facebookSDK.value = null
    statusError.value = error?.message?.startsWith('facebook_sdk_')
      ? t('facebook_sdk_load_failed')
      : t('facebook_link_status_failed')
  } finally {
    statusLoading.value = false
  }
}

async function linkAccount() {
  if (!facebookSDK.value || linking.value) return
  linking.value = true
  statusError.value = ''
  try {
    const response = await requestFacebookLogin(facebookSDK.value)
    const accessToken = response.status === 'connected' ? response.authResponse?.accessToken : undefined
    if (!accessToken) {
      statusError.value = t('facebook_login_failed')
      return
    }

    const { data } = await api.post('/profile/facebook/link', {
      access_token: accessToken,
      current_password: currentPassword.value,
    })
    status.value = { enabled: true, linked: true, facebookName: data.facebook_name || '' }
    currentPassword.value = ''
    showSnack(t('facebook_link_success'), 'success')
  } catch (error: any) {
    const code = error?.response?.data?.error
    if (code === 'facebook_account_already_linked') {
      statusError.value = t('facebook_account_already_linked')
    } else if (code === 'wrong_current_password') {
      statusError.value = t('wrong_password')
    } else if (code === 'facebook_service_unavailable') {
      statusError.value = t('facebook_service_unavailable')
    } else {
      statusError.value = t('facebook_link_failed')
    }
    showSnack(statusError.value, 'error')
  } finally {
    linking.value = false
  }
}

async function unlinkAccount() {
  if (unlinking.value || !window.confirm(t('facebook_unlink_confirm'))) return
  unlinking.value = true
  statusError.value = ''
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
