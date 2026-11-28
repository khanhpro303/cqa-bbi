// @vitest-environment happy-dom
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, shallowMount } from '@vue/test-utils'
import Settings from '../views/Settings.vue'
import { resetAIModelCatalog, useAIModelCatalog } from '../composables/aiModelCatalog'

const mocks = vi.hoisted(() => ({ get: vi.fn(), put: vi.fn(), post: vi.fn() }))
vi.mock('../api', () => ({ default: mocks }))
vi.mock('vue-router', () => ({ useRoute: () => ({ params: { tenantId: 'tenant-1' }, query: {} }) }))
vi.mock('vue-i18n', () => ({
  useI18n: () => ({ t: (key: string, params?: Record<string, unknown>) => params ? `${key}:${JSON.stringify(params)}` : key }),
}))

let settings: Record<string, string> = {}

async function mountSettings() {
  const wrapper = shallowMount(Settings, {
    global: {
      stubs: Object.fromEntries([
        'VAlert', 'VBtn', 'VCard', 'VChip', 'VCol', 'VDivider', 'VIcon', 'VExpansionPanels', 'VExpansionPanel',
        'VExpansionPanelTitle', 'VExpansionPanelText', 'VList', 'VListItem', 'VRow', 'VSelect', 'VSnackbar',
        'VSwitch', 'VTable', 'VTextField', 'RouterLink', 'FacebookAccountManager',
      ].map(name => [name, true])),
      mocks: { $t: (key: string) => key },
      renderStubDefaultSlot: true,
    },
  })
  await flushPromises()
  return wrapper
}

describe('Settings > AI configuration (keys only)', () => {
  beforeEach(() => {
    resetAIModelCatalog()
    settings = {
      ai_provider: 'openai',
      ai_model: 'gpt-5-mini',
      ai_api_key: 'legacy-key',
      ai_api_key_openai: 'masked-openai-key',
      ai_api_key_9router: '••••••••',
      ai_base_url_9router: 'https://router.example.com/v1',
    }
    mocks.get.mockReset().mockImplementation(async (url: string) => {
      if (url.endsWith('/settings')) return { data: { settings } }
      if (url.endsWith('/settings/ai/9router-models')) return { data: { models: [{ id: 'cx/gpt-5.6-luna' }, { id: 'ag/gemini-4-neon', vision: true }] } }
      if (url.endsWith('/settings/ai/openrouter-models')) return { data: { models: [{ id: 'a/b:free', name: 'B' }] } }
      return { data: {} }
    })
    mocks.put.mockReset().mockResolvedValue({ data: { message: 'saved' } })
    mocks.post.mockReset().mockResolvedValue({ data: { provider: 'openai', message: 'API key works' } })
  })

  it('has no model pickers and links to Agents AI task models', async () => {
    const wrapper = await mountSettings()
    expect(wrapper.find('[data-testid="ai-task-model-settings"]').exists()).toBe(false)
    expect(wrapper.find('[data-testid="ai-flow-model-settings"]').exists()).toBe(false)
    expect(wrapper.find('[data-testid="ai-task-models-link"]').exists()).toBe(true)
    for (const provider of ['claude', 'gemini', 'openai', 'openrouter', '9router']) {
      expect(wrapper.find(`[data-testid="ai-provider-${provider}"]`).exists()).toBe(true)
    }
    wrapper.unmount()
  })

  it('keeps unsaved drafts for other providers when one key is saved', async () => {
    const wrapper = await mountSettings()
    const vm = wrapper.vm as any
    vm.drafts.gemini.apiKey = 'draft-g'
    vm.drafts.openai.apiKey = 'edited-o'
    await vm.saveProvider('openai')
    expect(vm.drafts.gemini.apiKey).toBe('draft-g')
    expect(vm.drafts.openai.apiKey).toBe(settings.ai_api_key_openai)
    wrapper.unmount()
  })

  it('keeps edits made to the saved provider while its request is pending', async () => {
    let finishSave!: () => void
    mocks.put.mockImplementationOnce(() => new Promise(resolve => {
      finishSave = () => resolve({ data: { message: 'saved' } })
    }))
    const wrapper = await mountSettings()
    const vm = wrapper.vm as any
    vm.drafts.openai.apiKey = 'submitted-o'
    const saving = vm.saveProvider('openai')
    await Promise.resolve()
    vm.drafts.openai.apiKey = 'newer-o'
    finishSave()
    await saving
    expect(vm.drafts.openai.apiKey).toBe('newer-o')
    wrapper.unmount()
  })

  it('saves a key as credentials only and keeps per-provider keys apart', async () => {
    const wrapper = await mountSettings()
    const vm = wrapper.vm as any
    expect(vm.drafts.openai.apiKey).toBe('masked-openai-key')
    expect(vm.drafts['9router'].apiKey).toBe('••••••••')
    expect(vm.drafts.claude.apiKey).toBe('')

    vm.drafts.openai.apiKey = 'sk-new'
    await vm.saveProvider('openai')
    expect(mocks.put).toHaveBeenCalledWith('/tenants/tenant-1/settings/ai', {
      provider: 'openai', api_key: 'sk-new', base_url: '', credentials_only: true,
    })
    wrapper.unmount()
  })

  it('makes the first provider with a key the default when the default has none', async () => {
    settings = { ai_provider: 'claude' }
    const wrapper = await mountSettings()
    const vm = wrapper.vm as any
    vm.drafts.gemini.apiKey = 'g-key'
    await vm.saveProvider('gemini')
    expect(mocks.put).toHaveBeenCalledWith('/tenants/tenant-1/settings/ai', {
      provider: 'gemini', api_key: 'g-key', base_url: '', model: 'gemini-2.5-flash',
    })
    wrapper.unmount()
  })

  it('requires the 9router endpoint and checks the token by listing its models', async () => {
    const wrapper = await mountSettings()
    const vm = wrapper.vm as any
    const catalog = useAIModelCatalog()

    vm.drafts['9router'].baseUrl = ''
    await vm.saveProvider('9router')
    expect(mocks.put).not.toHaveBeenCalled()

    // An unsaved token cannot be checked.
    vm.drafts['9router'].baseUrl = 'https://router.example.com/v1'
    vm.drafts['9router'].apiKey = 'new-token'
    const before = mocks.get.mock.calls.length
    await vm.testProvider('9router')
    expect(mocks.get.mock.calls.length).toBe(before)

    vm.drafts['9router'].apiKey = '••••••••'
    await vm.testProvider('9router')
    expect(catalog.nineRouterStatus).toBe('loaded')
    expect(catalog.nineRouter).toHaveLength(2)
    expect(mocks.post).not.toHaveBeenCalled()
    wrapper.unmount()
  })

  it('checks other keys with a one-line call, using the saved default model for the default provider', async () => {
    const wrapper = await mountSettings()
    const vm = wrapper.vm as any
    await vm.testProvider('openai')
    expect(mocks.post).toHaveBeenLastCalledWith('/tenants/tenant-1/settings/ai/test', {
      provider: 'openai', api_key: settings.ai_api_key_openai, base_url: '', model: '',
    })
    vm.drafts.gemini.apiKey = 'g-key'
    await vm.testProvider('gemini')
    expect(mocks.post).toHaveBeenLastCalledWith('/tenants/tenant-1/settings/ai/test', {
      provider: 'gemini', api_key: 'g-key', base_url: '', model: 'gemini-2.5-flash',
    })
    wrapper.unmount()
  })

  it('refreshes the shared model lists that feed the task model dropdowns', async () => {
    const wrapper = await mountSettings()
    const vm = wrapper.vm as any
    const catalog = useAIModelCatalog()
    expect(catalog.nineRouterStatus).toBe('loaded')
    await vm.refreshCatalogs()
    const calls = mocks.get.mock.calls.map(([url, config]) => [url, config])
    expect(calls.filter(([url]) => url.endsWith('/9router-models'))).toHaveLength(2)
    expect(calls.filter(([url]) => url.endsWith('/openrouter-models')).at(-1)?.[1]).toEqual({ params: { refresh: 1 } })
    expect(wrapper.get('[data-testid="ai-catalog-9router"]').text()).toContain('nine_router_models_count')
    wrapper.unmount()
  })
})
