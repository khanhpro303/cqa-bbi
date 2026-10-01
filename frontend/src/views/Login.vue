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
      <v-btn color="#1877F2" block size="large" prepend-icon="mdi-facebook"
        :loading="facebookLoading" :disabled="loading" @click="startFacebookLogin">
        {{ $t('continue_with_facebook') }}
      </v-btn>
    </template>
  </v-card>
</template>

<script setup lang="ts">
import { onMounted, onUnmounted, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { useI18n } from 'vue-i18n'
import api from '../api'
import { useAuthStore } from '../stores/auth'

const router = useRouter()
const route = useRoute()
const { t } = useI18n()
const authStore = useAuthStore()

const email = ref('')
const password = ref('')
const showPass = ref(false)
const loading = ref(false)
const errorMsg = ref('')
const facebookEnabled = ref(false)
const facebookLoading = ref(false)
const startController = new AbortController()

onMounted(async () => {
  const result = route.query.facebook_login
  if (typeof result === 'string') {
    const { facebook_login: _, ...query } = route.query
    await router.replace({ path: '/login', query, hash: route.hash })
    if (result === 'success') {
      facebookLoading.value = true
      try {
        await authStore.completeFacebookRedirect()
        await router.replace('/')
        return
      } catch (error) {
        setFacebookError(error)
      } finally {
        facebookLoading.value = false
      }
    } else {
      setFacebookError({ response: { data: { error: result } } })
    }
  }
  try {
    const { data } = await api.get('/auth/facebook/config', { timeout: 15000, signal: startController.signal })
    facebookEnabled.value = !!data.enabled
  } catch {
    // Email/password sign-in remains available if the provider configuration cannot load.
  }
})

onUnmounted(() => startController.abort())

async function startFacebookLogin() {
  if (!facebookEnabled.value || loading.value || facebookLoading.value) return
  facebookLoading.value = true
  errorMsg.value = ''
  try {
    const { data } = await api.post('/auth/facebook/start', {}, { timeout: 15000, signal: startController.signal })
    const url = new URL(data.redirect_url)
    if (url.protocol !== 'https:' || url.hostname !== 'www.facebook.com' || url.port || url.username || url.password) {
      throw new Error('invalid_facebook_redirect')
    }
    window.location.assign(url.href)
  } catch (error: any) {
    if (startController.signal.aborted) return
    setFacebookError(error)
    facebookLoading.value = false
  }
}

function setFacebookError(error: any) {
  const code = error?.response?.data?.error
  if (code === 'facebook_account_not_linked') {
    errorMsg.value = t('facebook_account_not_linked')
  } else if (['invalid_state', 'session_expired', 'browser_mismatch'].includes(code)) {
    errorMsg.value = t('facebook_signin_session_expired')
  } else if (code === 'oauth_denied') {
    errorMsg.value = t('facebook_signin_cancelled')
  } else if (code === 'facebook_login_not_configured') {
    errorMsg.value = t('facebook_login_not_configured')
  } else if (code === 'facebook_service_unavailable') {
    errorMsg.value = t('facebook_service_unavailable')
  } else {
    errorMsg.value = t('facebook_login_failed')
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
