// @vitest-environment happy-dom

import { beforeEach, describe, expect, it, vi } from 'vitest'
import { createPinia, setActivePinia } from 'pinia'
import { useChatbotStore } from '../stores/chatbot'

const apiMock = vi.hoisted(() => ({
  get: vi.fn(),
  put: vi.fn(),
}))

vi.mock('../api', () => ({ default: apiMock }))

describe('chatbot status store', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    apiMock.get.mockReset()
    apiMock.put.mockReset()
  })

  it('caches a disabled master switch across view remounts', async () => {
    apiMock.get.mockResolvedValueOnce({
      data: { settings: { chatbot_active: 'false' } },
    })

    const firstViewStore = useChatbotStore()
    await firstViewStore.fetchStatus('tenant-1')

    const remountedViewStore = useChatbotStore()
    expect(remountedViewStore.isLoaded('tenant-1')).toBe(true)
    expect(remountedViewStore.isActive('tenant-1')).toBe(false)
  })

  it('keeps the cached value while refreshing settings', async () => {
    let resolveRequest: ((value: unknown) => void) | undefined
    apiMock.get.mockReturnValueOnce(new Promise((resolve) => {
      resolveRequest = resolve
    }))

    const store = useChatbotStore()
    store.setActive('tenant-1', false)
    const refresh = store.fetchStatus('tenant-1')

    expect(store.isActive('tenant-1')).toBe(false)
    expect(store.loadingByTenant['tenant-1']).toBe(true)

    resolveRequest?.({ data: { settings: { chatbot_active: 'false' } } })
    await refresh

    expect(store.isActive('tenant-1')).toBe(false)
    expect(store.loadingByTenant['tenant-1']).toBe(false)
  })

  it('keeps an unknown status disabled after the initial request fails', async () => {
    apiMock.get.mockRejectedValueOnce(new Error('network unavailable'))

    const store = useChatbotStore()
    await store.fetchStatus('tenant-1')

    expect(store.isLoaded('tenant-1')).toBe(false)
    expect(store.hasError('tenant-1')).toBe(true)
    expect(store.isLoading('tenant-1')).toBe(false)
  })

  it('deduplicates concurrent refreshes for the same tenant', async () => {
    apiMock.get.mockResolvedValueOnce({
      data: { settings: { chatbot_active: 'true' } },
    })

    const store = useChatbotStore()
    await Promise.all([
      store.fetchStatus('tenant-1'),
      store.fetchStatus('tenant-1'),
    ])

    expect(apiMock.get).toHaveBeenCalledTimes(1)
    expect(store.isActive('tenant-1')).toBe(true)
  })

  it('does not let a stale refresh overwrite a newer toggle', async () => {
    let resolveRequest: ((value: unknown) => void) | undefined
    apiMock.get.mockReturnValueOnce(new Promise((resolve) => {
      resolveRequest = resolve
    }))

    const store = useChatbotStore()
    const refresh = store.fetchStatus('tenant-1')
    store.setActive('tenant-1', false)

    resolveRequest?.({ data: { settings: { chatbot_active: 'true' } } })
    await refresh

    expect(store.isActive('tenant-1')).toBe(false)
    expect(store.isLoading('tenant-1')).toBe(false)
  })

  it('lets a confirmed mutation invalidate a refresh started after the optimistic update', async () => {
    let resolveRequest: ((value: unknown) => void) | undefined
    apiMock.get.mockReturnValueOnce(new Promise((resolve) => {
      resolveRequest = resolve
    }))

    const store = useChatbotStore()
    store.setActive('tenant-1', false)
    const refresh = store.fetchStatus('tenant-1')

    store.setActive('tenant-1', false)
    resolveRequest?.({ data: { settings: { chatbot_active: 'true' } } })
    await refresh

    expect(store.isActive('tenant-1')).toBe(false)
    expect(store.isLoading('tenant-1')).toBe(false)
  })

  it('shares the mutation lock across remounted views', async () => {
    let resolveMutation: ((value: unknown) => void) | undefined
    apiMock.put.mockReturnValueOnce(new Promise((resolve) => {
      resolveMutation = resolve
    }))

    const firstViewStore = useChatbotStore()
    firstViewStore.setActive('tenant-1', true)
    const firstMutation = firstViewStore.updateStatus('tenant-1', false)

    const remountedViewStore = useChatbotStore()
    expect(remountedViewStore.isMutating('tenant-1')).toBe(true)
    const blockedMutation = remountedViewStore.updateStatus('tenant-1', true)

    expect(apiMock.put).toHaveBeenCalledTimes(1)
    expect(remountedViewStore.isActive('tenant-1')).toBe(false)

    resolveMutation?.({ data: {} })
    await Promise.all([firstMutation, blockedMutation])

    expect(remountedViewStore.isMutating('tenant-1')).toBe(false)
    expect(remountedViewStore.isActive('tenant-1')).toBe(false)
  })
})
