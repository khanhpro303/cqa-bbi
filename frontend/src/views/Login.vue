<template>
  <v-card class="pa-6" elevation="2">
    <v-card-title class="text-h6 text-center pb-4">{{ $t('login_title') }}</v-card-title>
    <v-alert v-if="errorMsg" type="error" variant="tonal" density="compact" class="mb-4">{{ errorMsg }}</v-alert>
    <v-form @submit.prevent="handleLogin">
      <v-text-field
        v-model="email"
        :label="$t('email')"
        type="email"
        prepend-inner-icon="mdi-email"
        required
        class="mb-2"
      />
      <v-text-field
        v-model="password"
        :label="$t('password')"
        :type="showPass ? 'text' : 'password'"
        prepend-inner-icon="mdi-lock"
        :append-inner-icon="showPass ? 'mdi-eye-off' : 'mdi-eye'"
        @click:append-inner="showPass = !showPass"
        required
        class="mb-4"
      />
      <v-btn type="submit" color="primary" block size="large" :loading="loading" :disabled="facebookLoading">
        {{ $t('login') }}
      </v-btn>
    </v-form>

    <template v-if="facebookEnabled">
      <div class="d-flex align-center my-5">
        <v-divider />
        <span class="text-caption text-medium-emphasis mx-3">{{ $t('or') }}</span>
        <v-divider />
      </div>
      <div class="facebook-login-button-slot" :aria-busy="loading || facebookLoading || facebookSDKLoading">
        <v-progress-circular
          v-if="loading || facebookLoading || facebookSDKLoading"
          color="#1877F2"
          indeterminate
          size="28"
          width="3"
        />
        <div v-show="!loading && !facebookLoading && !facebookSDKLoading" ref="facebookButtonContainer" />
      </div>
    </template>
  </v-card>
</template>

<script setup lang="ts">
import { nextTick, onMounted, onUnmounted, ref } from 'vue'
import { useRouter } from 'vue-router'
import { useI18n } from 'vue-i18n'
import api from '../api'
import { useAuthStore } from '../stores/auth'
import { getFacebookLoginStatus, loadFacebookSDK, renderFacebookLoginButton } from '../utils/facebook-sdk'

const router = useRouter()
const { t } = useI18n()
const authStore = useAuthStore()

const email = ref('')
const password = ref('')
const showPass = ref(false)
const loading = ref(false)
const errorMsg = ref('')
const facebookEnabled = ref(false)
const facebookLoading = ref(false)
const facebookSDKLoading = ref(false)
const facebookSDK = ref<Awaited<ReturnType<typeof loadFacebookSDK>> | null>(null)
const facebookConfig = ref<{ appId: string; apiVersion: string } | null>(null)
const facebookButtonContainer = ref<HTMLElement | null>(null)
const skipFacebookAutoLoginKey = 'cqa_skip_facebook_auto_login'
const facebookLoginStateCallback = () => { void checkLoginState() }

onMounted(async () => {
  try {
    const { data } = await api.get('/auth/facebook/config')
    if (!data.enabled) return
    window.checkLoginState = facebookLoginStateCallback
    facebookEnabled.value = true
    facebookConfig.value = { appId: data.app_id, apiVersion: data.api_version }
    await initializeFacebookSDK()
    await restoreFacebookSession()
  } catch (error: any) {
    if (error?.message?.startsWith('facebook_sdk_')) {
      errorMsg.value = t('facebook_sdk_load_failed')
    }
  }
})

onUnmounted(() => {
  if (window.checkLoginState === facebookLoginStateCallback) delete window.checkLoginState
})

async function restoreFacebookSession() {
  if (!facebookSDK.value || sessionStorage.getItem(skipFacebookAutoLoginKey) === '1') return

  await authenticateFacebookStatus(false)
}

async function checkLoginState() {
  await authenticateFacebookStatus(true)
}

async function authenticateFacebookStatus(showStatusError: boolean) {
  if (!facebookSDK.value || loading.value || facebookLoading.value) return

  try {
    facebookLoading.value = true
    errorMsg.value = ''
    const status = await getFacebookLoginStatus(facebookSDK.value)
    const accessToken = status.status === 'connected' ? status.authResponse?.accessToken : undefined
    if (!accessToken) {
      if (showStatusError) errorMsg.value = t('facebook_login_failed')
      return
    }

    await completeFacebookLogin(accessToken)
  } catch (error: any) {
    if (showStatusError || !error?.message?.startsWith('facebook_status_check_')) {
      setFacebookError(error)
    }
  } finally {
    facebookLoading.value = false
  }
}

async function completeFacebookLogin(accessToken: string) {
  await authStore.loginWithFacebook(accessToken)
  await router.push('/')
}

function setFacebookError(error: any) {
  const code = error?.response?.data?.error
  if (error?.message?.startsWith('facebook_sdk_')) {
    errorMsg.value = t('facebook_sdk_load_failed')
  } else if (code === 'facebook_account_not_provisioned') {
    errorMsg.value = t('facebook_account_not_provisioned')
  } else if (code === 'facebook_email_required') {
    errorMsg.value = t('facebook_email_required')
  } else if (code === 'facebook_service_unavailable') {
    errorMsg.value = t('facebook_service_unavailable')
  } else {
    errorMsg.value = t('facebook_login_failed')
  }
}

async function initializeFacebookSDK() {
  if (!facebookConfig.value || facebookSDKLoading.value) return
  facebookSDKLoading.value = true
  try {
    facebookSDK.value = await loadFacebookSDK(facebookConfig.value)
    await nextTick()
    if (!facebookButtonContainer.value) throw new Error('facebook_sdk_button_container_unavailable')
    renderFacebookLoginButton(facebookSDK.value, facebookButtonContainer.value)
  } finally {
    facebookSDKLoading.value = false
  }
}

async function handleLogin() {
  if (facebookLoading.value) return
  loading.value = true
  errorMsg.value = ''
  try {
    await authStore.login(email.value, password.value)
    router.push('/')
  } catch {
    errorMsg.value = t('invalid_credentials')
  } finally {
    loading.value = false
  }
}
</script>

<style scoped>
.facebook-login-button-slot {
  display: flex;
  min-height: 40px;
  align-items: center;
  justify-content: center;
}
</style>
