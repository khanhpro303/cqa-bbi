import { defineStore } from 'pinia'
import { ref } from 'vue'
import api from '../api'

export const useChatbotStore = defineStore('chatbot', () => {
  const activeByTenant = ref<Record<string, boolean>>({})
  const loadingByTenant = ref<Record<string, boolean>>({})
  const errorByTenant = ref<Record<string, boolean>>({})
  const mutatingByTenant = ref<Record<string, boolean>>({})
  const requestVersionByTenant = new Map<string, number>()
  const mutationVersionByTenant = new Map<string, number>()
  const pendingByTenant = new Map<string, { version: number; promise: Promise<boolean | null> }>()
  const pendingMutationByTenant = new Map<string, Promise<boolean>>()

  function isLoaded(tenantId: string) {
    return tenantId in activeByTenant.value
  }

  function isActive(tenantId: string) {
    return activeByTenant.value[tenantId] ?? true
  }

  function isLoading(tenantId: string) {
    return loadingByTenant.value[tenantId] ?? false
  }

  function hasError(tenantId: string) {
    return errorByTenant.value[tenantId] ?? false
  }

  function isMutating(tenantId: string) {
    return mutatingByTenant.value[tenantId] ?? false
  }

  function nextRequestVersion(tenantId: string) {
    const version = (requestVersionByTenant.get(tenantId) ?? 0) + 1
    requestVersionByTenant.set(tenantId, version)
    return version
  }

  function commitActive(tenantId: string, active: boolean) {
    activeByTenant.value[tenantId] = active
    errorByTenant.value[tenantId] = false
  }

  function setActive(tenantId: string, active: boolean) {
    nextRequestVersion(tenantId)
    pendingByTenant.delete(tenantId)
    loadingByTenant.value[tenantId] = false
    commitActive(tenantId, active)
  }

  async function fetchStatus(tenantId: string) {
    const pending = pendingByTenant.get(tenantId)
    if (pending) return pending.promise

    const version = nextRequestVersion(tenantId)
    loadingByTenant.value[tenantId] = true
    errorByTenant.value[tenantId] = false

    const promise = (async () => {
      try {
        const { data } = await api.get(`/tenants/${tenantId}/settings`)
        const active = data?.settings?.chatbot_active !== 'false'
        if (requestVersionByTenant.get(tenantId) === version) {
          commitActive(tenantId, active)
        }
        return active
      } catch {
        if (requestVersionByTenant.get(tenantId) === version) {
          errorByTenant.value[tenantId] = true
        }
        return null
      } finally {
        if (pendingByTenant.get(tenantId)?.version === version) {
          pendingByTenant.delete(tenantId)
        }
        if (requestVersionByTenant.get(tenantId) === version) {
          loadingByTenant.value[tenantId] = false
        }
      }
    })()

    pendingByTenant.set(tenantId, { version, promise })
    return promise
  }

  async function updateStatus(tenantId: string, active: boolean) {
    const pending = pendingMutationByTenant.get(tenantId)
    if (pending) return pending

    const previousActive = isActive(tenantId)
    const mutationVersion = (mutationVersionByTenant.get(tenantId) ?? 0) + 1
    mutationVersionByTenant.set(tenantId, mutationVersion)
    setActive(tenantId, active)
    mutatingByTenant.value[tenantId] = true

    const promise = (async () => {
      try {
        await api.put(`/tenants/${tenantId}/settings`, {
          key: 'chatbot_active',
          value: active ? 'true' : 'false',
        })
        setActive(tenantId, active)
        return true
      } catch (error) {
        setActive(tenantId, previousActive)
        throw error
      } finally {
        if (mutationVersionByTenant.get(tenantId) === mutationVersion) {
          pendingMutationByTenant.delete(tenantId)
          mutatingByTenant.value[tenantId] = false
        }
      }
    })()

    pendingMutationByTenant.set(tenantId, promise)
    return promise
  }

  return {
    activeByTenant,
    loadingByTenant,
    errorByTenant,
    mutatingByTenant,
    isLoaded,
    isActive,
    isLoading,
    hasError,
    isMutating,
    setActive,
    fetchStatus,
    updateStatus,
  }
})
