import { defineStore } from 'pinia'
import { ref } from 'vue'
import api from '../api'

export interface Job {
  id: string
  tenant_id: string
  name: string
  description: string
  job_type: string
  input_channel_ids: string
  rules_content: string
  rules_config: string
  skip_conditions: string
  ai_provider: string
  ai_model: string
  outputs: string
  output_schedule: string
  output_cron: string
  output_at: string | null
  schedule_type: string
  schedule_cron: string
  is_active: boolean
  last_run_at: string | null
  last_run_status: string
  created_at: string
}

export interface JobRun {
  id: string
  job_id: string
  started_at: string
  finished_at: string | null
  status: string
  summary: string
  error_message: string
}

export interface JobResult {
  id: string
  job_run_id: string
  conversation_id: string
  result_type: string
  severity: string
  rule_name: string
  evidence: string
  detail: string
  confidence: number
  created_at: string
  conversation_date?: string
  customer_name?: string
}

export const useJobStore = defineStore('jobs', () => {
  const jobs = ref<Job[]>([])
  const currentJob = ref<Job | null>(null)
  const jobRuns = ref<JobRun[]>([])
  const jobResults = ref<JobResult[]>([])
  let jobsRequest = 0

  async function fetchJobs(tenantId: string) {
    const request = ++jobsRequest
    const { data } = await api.get(`/tenants/${tenantId}/jobs`)
    if (request === jobsRequest) jobs.value = data
  }

  async function fetchJob(tenantId: string, jobId: string) {
    const { data } = await api.get(`/tenants/${tenantId}/jobs/${jobId}`)
    currentJob.value = data
    return data
  }

  async function createJob(tenantId: string, payload: Record<string, unknown>) {
    const { data } = await api.post(`/tenants/${tenantId}/jobs`, payload)
    jobs.value.unshift(data)
    return data
  }

  async function updateJob(tenantId: string, jobId: string, payload: Record<string, unknown>) {
    const { data } = await api.put(`/tenants/${tenantId}/jobs/${jobId}`, payload)
    return data
  }

  async function deleteJob(tenantId: string, jobId: string) {
    await api.delete(`/tenants/${tenantId}/jobs/${jobId}`)
    jobs.value = jobs.value.filter((j) => j.id !== jobId)
  }

  async function triggerJob(tenantId: string, jobId: string, mode: string = 'since_last', params: Record<string, string> = {}) {
    const qp = new URLSearchParams()
    qp.set('mode', mode)
    Object.entries(params).forEach(([k, v]) => qp.set(k, v))
    await api.post(`/tenants/${tenantId}/jobs/${jobId}/trigger?${qp.toString()}`)
  }

  async function testRunJob(tenantId: string, jobId: string) {
    const { data } = await api.post(`/tenants/${tenantId}/jobs/${jobId}/test-run`)
    return data
  }

  async function fetchJobRuns(tenantId: string, jobId: string) {
    const { data } = await api.get(`/tenants/${tenantId}/jobs/${jobId}/runs`)
    jobRuns.value = data
  }

  async function fetchJobResults(tenantId: string, jobId: string, runId: string) {
    const { data } = await api.get(`/tenants/${tenantId}/jobs/${jobId}/runs/${runId}/results`)
    jobResults.value = data
  }

  async function fetchAllJobResults(tenantId: string, jobId: string) {
    const { data } = await api.get(`/tenants/${tenantId}/jobs/${jobId}/results`)
    jobResults.value = data
  }

  return { jobs, currentJob, jobRuns, jobResults, fetchJobs, fetchJob, createJob, updateJob, deleteJob, triggerJob, testRunJob, fetchJobRuns, fetchJobResults, fetchAllJobResults }
})
