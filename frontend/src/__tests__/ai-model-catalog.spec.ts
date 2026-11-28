import { beforeEach, describe, expect, it, vi } from 'vitest'
import {
  clearNineRouterModels,
  loadNineRouterModels,
  loadOpenRouterModels,
  resetAIModelCatalog,
  useAIModelCatalog,
} from '../composables/aiModelCatalog'

const get = vi.hoisted(() => vi.fn())
vi.mock('../api', () => ({ default: { get } }))

function pendingRequest() {
  let complete!: (response: { data: { models: { id: string }[] } }) => void
  const response = new Promise<{ data: { models: { id: string }[] } }>(resolve => { complete = resolve })
  get.mockReturnValueOnce(response)
  return (id: string) => complete({ data: { models: [{ id }] } })
}

describe('AI model catalog', () => {
  beforeEach(() => {
    get.mockReset()
    resetAIModelCatalog()
  })

  it('discards a 9router response from before the connection changed', async () => {
    const finishOld = pendingRequest()
    const old = loadNineRouterModels('tenant-1')
    clearNineRouterModels()
    const finishCurrent = pendingRequest()
    const current = loadNineRouterModels('tenant-1', true)
    finishCurrent('current-route')
    await current
    finishOld('old-route')
    await old
    expect(useAIModelCatalog().nineRouter).toEqual([{ id: 'current-route' }])
  })

  it('issues and prioritizes an explicit OpenRouter refresh during a cached load', async () => {
    const finishCached = pendingRequest()
    const cached = loadOpenRouterModels('tenant-1')
    const finishRefresh = pendingRequest()
    const refresh = loadOpenRouterModels('tenant-1', true)
    expect(get).toHaveBeenLastCalledWith('/tenants/tenant-1/settings/ai/openrouter-models', { params: { refresh: 1 } })
    finishRefresh('new-catalog')
    await refresh
    finishCached('old-catalog')
    await cached
    expect(useAIModelCatalog().openRouter).toEqual([{ id: 'new-catalog' }])
  })
})
