<template>
  <div>
    <div class="d-flex align-center justify-space-between flex-wrap ga-3 mb-6">
      <h1 class="text-h5 font-weight-bold">{{ $t('jobs') }}</h1>
      <div class="d-flex ga-2 flex-wrap justify-end">
      <v-btn v-if="isAdmin && !hasProductGrouping && !loadingJobs" class="job-action-button" variant="outlined" color="primary" :loading="creatingGrouping" @click="addProductGrouping">{{ $t('sq_add_product_grouping') }}</v-btn>
      <v-btn v-if="authStore.canEdit('jobs')" class="job-action-button" color="primary" prepend-icon="mdi-plus" :to="`/${tenantId}/jobs/create`">
        {{ $t('create_job') }}
      </v-btn>
      </div>
    </div>

    <v-alert v-if="error" type="error" variant="tonal" class="mb-4">{{ error }}</v-alert>
    <v-card :loading="loadingJobs">
      <v-table v-if="jobStore.jobs.length">
        <thead>
          <tr>
            <th>{{ $t('job_name') }}</th>
            <th>{{ $t('status') }}</th>
            <th>{{ $t('last_run') }}</th>
            <th>{{ $t('actions') }}</th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="job in jobStore.jobs" :key="job.id">
            <td>
              <router-link :to="`/${tenantId}/jobs/${job.id}`" class="text-primary font-weight-medium text-decoration-none">
                {{ job.name }}
              </router-link>
              <div class="text-caption text-grey">{{ job.description }}</div>
            </td>
            <td>
              <v-chip size="small" :color="job.is_active ? 'success' : 'grey'" variant="tonal">
                {{ job.is_active ? $t('active') : $t('inactive') }}
              </v-chip>
            </td>
            <td>
              <span v-if="job.last_run_at" class="text-body-2">
                {{ new Date(job.last_run_at).toLocaleString() }}
                <v-chip size="x-small" :color="job.last_run_status === 'success' ? 'success' : 'error'" variant="tonal" class="ml-1">
                  {{ job.last_run_status }}
                </v-chip>
              </span>
              <span v-else class="text-grey text-body-2">—</span>
            </td>
            <td>
              <v-btn v-if="job.job_type !== 'messenger_product_groups' && authStore.canEdit('jobs')" icon="mdi-pencil" size="small" variant="text" :to="`/${tenantId}/jobs/${job.id}/edit`" />
              <v-btn v-if="canDelete(job)" icon="mdi-delete" size="small" variant="text" color="error" @click="remove(job)" />
            </td>
          </tr>
        </tbody>
      </v-table>
      <div v-else class="text-center pa-8">
        <v-icon size="48" color="grey-lighten-1" class="mb-3">mdi-briefcase-plus</v-icon>
        <div class="text-grey-darken-1 mb-2">{{ $t('job_empty_desc') }}</div>
        <v-btn v-if="authStore.canEdit('jobs')" color="primary" prepend-icon="mdi-plus" :to="`/${tenantId}/jobs/create`" size="small">{{ $t('create_task') }}</v-btn>
      </div>
    </v-card>
  </div>
</template>

<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useRoute } from 'vue-router'
import api from '../../api'
import { useI18n } from 'vue-i18n'
import { useJobStore, type Job } from '../../stores/jobs'
import { useAuthStore } from '../../stores/auth'

const route = useRoute()
const jobStore = useJobStore()
const authStore = useAuthStore()
const tenantId = computed(() => route.params.tenantId as string)

const { t } = useI18n()
const isAdmin = computed(() => ['owner', 'admin'].includes(authStore.tenantPerms.role))
const hasProductGrouping = computed(() => jobStore.jobs.some(job => job.tenant_id === tenantId.value && job.job_type === 'messenger_product_groups'))
const loadingJobs = ref(true)
const creatingGrouping = ref(false)
const error = ref('')
let fetchSequence = 0
watch(tenantId, async (id) => {
  const sequence = ++fetchSequence
  loadingJobs.value = true
  error.value = ''
  try { await jobStore.fetchJobs(id) }
  catch { if (sequence === fetchSequence) error.value = t('sq_task_load_error') }
  finally { if (sequence === fetchSequence) loadingJobs.value = false }
}, { immediate: true })

function canDelete(job: Job) {
  return job.job_type === 'messenger_product_groups' ? isAdmin.value : isAdmin.value || !!authStore.tenantPerms.permissions.jobs?.includes('d')
}
async function addProductGrouping() {
  if (!isAdmin.value || hasProductGrouping.value || creatingGrouping.value) return
  const id = tenantId.value
  creatingGrouping.value = true
  error.value = ''
  try {
    await api.post(`/tenants/${id}/jobs/product-grouping`)
    if (id === tenantId.value) await jobStore.fetchJobs(id)
  } catch { if (id === tenantId.value) error.value = t('sq_task_create_error') }
  finally { creatingGrouping.value = false }
}
async function remove(job: Job) {
  if (!canDelete(job)) return
  if (confirm(t('sq_task_delete_confirm'))) {
    error.value = ''
    try { await jobStore.deleteJob(tenantId.value, job.id) }
    catch { error.value = t('sq_task_delete_error') }
  }
}
</script>

<style scoped>
.job-action-button { height: 48px; }
</style>
