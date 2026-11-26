<template>
  <v-sheet v-if="visible" color="indigo-lighten-5" rounded class="d-flex align-center pa-2 mb-4 ga-2">
    <v-icon color="primary" class="flex-shrink-0">mdi-rocket-launch</v-icon>
    <div class="onboarding-steps d-flex align-center ga-1">
      <span class="text-body-2 font-weight-medium" style="white-space: nowrap;">Bắt đầu:</span>
      <v-chip
        v-for="step in steps"
        :key="step.key"
        size="small"
        :color="step.done ? 'success' : 'default'"
        :variant="step.done ? 'flat' : 'outlined'"
        :prepend-icon="step.done ? 'mdi-check-circle' : 'mdi-circle-outline'"
        @click="goToStep(step)"
      >
        {{ step.title }}
      </v-chip>
      <v-chip
        v-if="completedCount === steps.length"
        color="success"
        size="small"
        variant="flat"
        prepend-icon="mdi-party-popper"
      >
        Hoàn thành!
      </v-chip>
    </div>
    <v-btn icon="mdi-close" variant="text" size="x-small" class="flex-shrink-0" @click="dismiss" />
  </v-sheet>
</template>

<script setup lang="ts">
import { ref, computed, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import api from '../api'
import { readJsonStorage, writeJsonStorage } from '../utils/storage'

const route = useRoute()
const router = useRouter()
const tenantId = computed(() => route.params.tenantId as string)

interface OnboardingStep {
  key: string
  title: string
  done: boolean
  link: string
}

interface CachedOnboarding {
  dismissed: boolean
  steps: OnboardingStep[]
}

const onboardingCacheKey = (id: string) => `cqa_onboarding_status:${id}`

const steps = ref<OnboardingStep[]>([])
const dismissed = ref(false)
const loaded = ref(false)
let activeRequestId = 0

const visible = computed(() => {
  if (!loaded.value || dismissed.value) return false
  return steps.value.length > 0
})

const completedCount = computed(() => steps.value.filter(s => s.done).length)

function hydrateFromCache(id: string) {
  const cached = readJsonStorage<CachedOnboarding>(onboardingCacheKey(id))
  if (cached && Array.isArray(cached.steps)) {
    steps.value = cached.steps
    dismissed.value = Boolean(cached.dismissed)
    loaded.value = true
    return
  }
  steps.value = []
  dismissed.value = false
  loaded.value = false
}

async function loadOnboarding() {
  const id = tenantId.value
  if (!id) return
  const requestId = ++activeRequestId
  try {
    const { data } = await api.get(`/tenants/${id}/onboarding-status`)
    if (requestId !== activeRequestId || tenantId.value !== id) return
    const nextSteps = Array.isArray(data.steps) ? data.steps : []
    const nextDismissed = Boolean(data.dismissed)
    steps.value = nextSteps
    dismissed.value = nextDismissed
    loaded.value = true
    writeJsonStorage(onboardingCacheKey(id), {
      dismissed: nextDismissed,
      steps: nextSteps,
    })
  } catch {
    if (requestId === activeRequestId && !loaded.value) {
      loaded.value = false
    }
  }
}

watch(
  () => [tenantId.value, route.path] as const,
  ([id], prev) => {
    if (!id) {
      steps.value = []
      dismissed.value = false
      loaded.value = false
      return
    }
    if (!prev || prev[0] !== id) {
      hydrateFromCache(id)
    }
    loadOnboarding()
  },
  { immediate: true },
)

function goToStep(step: OnboardingStep) {
  if (!step.done) {
    router.push(`/${tenantId.value}/${step.link}`)
  }
}

async function dismiss() {
  const id = tenantId.value
  dismissed.value = true
  if (id) {
    writeJsonStorage(onboardingCacheKey(id), {
      dismissed: true,
      steps: steps.value,
    })
  }
  try {
    await api.put(`/tenants/${id}/settings`, {
      key: 'onboarding_dismissed',
      value: 'true',
    })
  } catch { /* ignore */ }
}
</script>

<style scoped>
.onboarding-steps {
  overflow-x: auto;
  flex-wrap: nowrap;
  -webkit-overflow-scrolling: touch;
  scrollbar-width: none;
  min-width: 0;
}
.onboarding-steps::-webkit-scrollbar {
  display: none;
}
</style>
