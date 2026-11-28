// Shared AI provider lists and live model catalogs.
//
// Cài đặt > Cấu hình AI holds the provider keys and the "refresh model lists"
// button; Agents AI > Model tác vụ picks models from the same lists. The
// catalogs live at module level so a refresh on one page is what the other
// page's dropdowns show, without reloading the app.
import { reactive } from 'vue'
import api from '../api'

export type AIProvider = 'claude' | 'gemini' | 'openai' | 'openrouter' | '9router'
export type ModelOption = { title: string; value: string | number; type?: string }
export type CatalogStatus = 'idle' | 'loading' | 'loaded' | 'error'

export const providerOptions: { title: string; value: AIProvider }[] = [
  { title: 'Claude (Anthropic)', value: 'claude' },
  { title: 'Gemini (Google)', value: 'gemini' },
  { title: 'OpenAI API', value: 'openai' },
  { title: 'OpenRouter (nhiều hãng, có model free)', value: 'openrouter' },
  { title: '9router', value: '9router' },
]

export function isAIProvider(value: unknown): value is AIProvider {
  return providerOptions.some(option => option.value === value)
}

export const claudeModels: ModelOption[] = [
  { title: 'Claude Sonnet 4.6 (Recommended)', value: 'claude-sonnet-4-6' },
  { title: 'Claude Haiku 4.5 (Fast & Cheap)', value: 'claude-haiku-4-5-20251001' },
  { title: 'Claude Opus 4 (Most Capable)', value: 'claude-opus-4' },
  { title: 'Claude Sonnet 4.5 (CLIProxy)', value: 'claude-sonnet-4-5-20250929' },
  { title: 'Claude Opus 4.6 (CLIProxy)', value: 'claude-opus-4-6' },
]
export const geminiModels: ModelOption[] = [
  { title: 'Gemini 2.5 Flash (Fast & Cheap)', value: 'gemini-2.5-flash' },
  { title: 'Gemini 2.5 Flash Lite (Fastest)', value: 'gemini-2.5-flash-lite' },
  { title: 'Gemini 2.5 Pro (Most Capable)', value: 'gemini-2.5-pro' },
]
export const openAIModels: ModelOption[] = [
  { title: 'GPT-5.4 Mini (Recommended)', value: 'gpt-5.4-mini' },
  { title: 'GPT-5.3-Codex (Coding)', value: 'gpt-5.3-codex' },
  { title: 'GPT-5.3 Chat (General)', value: 'gpt-5.3-chat-latest' },
  { title: 'GPT-5 Mini (Low Cost)', value: 'gpt-5-mini' },
  { title: 'GPT-5 Nano (Cheapest)', value: 'gpt-5-nano' },
]

export const defaultModelByProvider: Record<AIProvider, string> = {
  claude: 'claude-sonnet-4-6',
  gemini: 'gemini-2.5-flash',
  openai: 'gpt-5.4-mini',
  openrouter: 'meta-llama/llama-3.3-70b-instruct:free',
  '9router': '',
}

export type OpenRouterModel = { id: string; name: string; context_length?: number; vision?: boolean; json_mode?: boolean }
export type NineRouterModel = { id: string; vision?: boolean }

// Saved provider credentials as GET /tenants/:id/settings returns them
// (keys come back masked; sending a masked key back keeps the stored one).
export type ProviderCredentials = {
  defaultProvider: AIProvider
  defaultModel: string
  apiKeys: Record<AIProvider, string>
  baseURLs: Record<AIProvider, string>
}

export function parseProviderCredentials(settings: Record<string, string> | undefined | null): ProviderCredentials {
  const s = settings || {}
  const defaultProvider: AIProvider = isAIProvider(s.ai_provider) ? s.ai_provider : 'claude'
  const apiKeys = {} as Record<AIProvider, string>
  const baseURLs = {} as Record<AIProvider, string>
  for (const { value } of providerOptions) {
    apiKeys[value] = s[`ai_api_key_${value}`] || ''
    baseURLs[value] = s[`ai_base_url_${value}`] ?? (value === defaultProvider ? s.ai_base_url || '' : '')
  }
  // Tenants set up before per-provider keys keep one legacy key for the
  // default provider (never for OpenRouter or 9router).
  if (!apiKeys[defaultProvider] && s.ai_api_key && defaultProvider !== 'openrouter' && defaultProvider !== '9router') {
    apiKeys[defaultProvider] = s.ai_api_key
  }
  return {
    defaultProvider,
    defaultModel: s.ai_model || defaultModelByProvider[defaultProvider],
    apiKeys,
    baseURLs,
  }
}

const catalog = reactive({
  tenant: '',
  openRouter: [] as OpenRouterModel[],
  openRouterStatus: 'idle' as CatalogStatus,
  nineRouter: [] as NineRouterModel[],
  nineRouterStatus: 'idle' as CatalogStatus,
})

let openRouterRequest: Promise<void> | null = null
let openRouterRequestForced = false
let nineRouterRequest: Promise<void> | null = null

function useTenant(tenantId: string) {
  if (catalog.tenant === tenantId) return
  catalog.tenant = tenantId
  catalog.openRouter = []
  catalog.openRouterStatus = 'idle'
  catalog.nineRouter = []
  catalog.nineRouterStatus = 'idle'
  openRouterRequest = null
  nineRouterRequest = null
}

// OpenRouter's free list is public. Explicit refresh uses the server's shorter cooldown.
export function loadOpenRouterModels(tenantId: string, force = false): Promise<void> {
  useTenant(tenantId)
  if (openRouterRequest && (!force || openRouterRequestForced)) return openRouterRequest
  if (!force && catalog.openRouterStatus === 'loaded') return Promise.resolve()
  catalog.openRouterStatus = 'loading'
  const request = api.get(`/tenants/${tenantId}/settings/ai/openrouter-models`, force ? { params: { refresh: 1 } } : undefined)
    .then(({ data }) => {
      if (openRouterRequest !== request) return
      catalog.openRouter = Array.isArray(data?.models) ? data.models : []
      catalog.openRouterStatus = 'loaded'
    })
    .catch(() => {
      if (openRouterRequest === request) catalog.openRouterStatus = 'error'
    })
    .finally(() => {
      if (openRouterRequest === request) openRouterRequest = null
    })
  openRouterRequest = request
  openRouterRequestForced = force
  return request
}

// 9router lists the routes of the saved connection, so it needs a saved token.
export function loadNineRouterModels(tenantId: string, force = false): Promise<void> {
  useTenant(tenantId)
  if (nineRouterRequest) return nineRouterRequest
  if (!force && catalog.nineRouterStatus === 'loaded') return Promise.resolve()
  catalog.nineRouterStatus = 'loading'
  const request = api.get(`/tenants/${tenantId}/settings/ai/9router-models`)
    .then(({ data }) => {
      if (nineRouterRequest !== request) return
      catalog.nineRouter = Array.isArray(data?.models) ? data.models : []
      catalog.nineRouterStatus = 'loaded'
    })
    .catch(() => {
      if (nineRouterRequest === request) {
        catalog.nineRouter = []
        catalog.nineRouterStatus = 'error'
      }
    })
    .finally(() => {
      if (nineRouterRequest === request) nineRouterRequest = null
    })
  nineRouterRequest = request
  return request
}

// Forget the 9router list, e.g. after the token or URL changed.
export function clearNineRouterModels() {
  catalog.nineRouter = []
  catalog.nineRouterStatus = 'idle'
  nineRouterRequest = null
}

// Drops every cached list (tests, tenant switch).
export function resetAIModelCatalog() {
  catalog.tenant = '\u0000'
  useTenant('')
}

export function useAIModelCatalog() {
  return catalog
}

export function openRouterOptions(models: OpenRouterModel[], visionOnly = false): ModelOption[] {
  return models.filter(model => !visionOnly || model.vision).map(model => {
    const tags = [model.vision ? 'đọc ảnh' : '', model.json_mode ? 'JSON' : '', model.context_length ? `${Math.round(model.context_length / 1000)}k` : '']
      .filter(Boolean).join(', ')
    return { title: tags ? `${model.name} (${tags})` : model.name, value: model.id }
  })
}

export function nineRouterOptions(models: NineRouterModel[], otherLabel: string, visionOnly = false): ModelOption[] {
  const groups = [
    { label: 'Codex', matches: (id: string) => /^(cx|codex)\//i.test(id) },
    { label: 'Gemini', matches: (id: string) => /^(?:[^/]+\/)?(?:gemini|gemma)[-/.]/i.test(id) },
    { label: 'Claude', matches: (id: string) => /^(?:[^/]+\/)?claude[-/.]/i.test(id) },
    { label: otherLabel, matches: () => true },
  ]
  const grouped = groups.map(group => ({ ...group, ids: [] as string[] }))
  for (const model of models) {
    if (visionOnly && !model.vision) continue
    grouped.find(group => group.matches(model.id))?.ids.push(model.id)
  }
  return grouped.flatMap(({ label, ids }, index) => ids.length ? [
    { title: label, value: index, type: 'subheader' },
    ...ids.map(id => ({ title: id, value: id, type: 'item' })),
  ] : [])
}

// Suggestions for a model field; the field still accepts any model id.
export function modelOptionsFor(provider: AIProvider | '', vision: boolean, otherLabel: string): ModelOption[] {
  switch (provider) {
    case 'claude':
      return claudeModels
    case 'gemini':
      return geminiModels
    case 'openai':
      return openAIModels
    case 'openrouter':
      return openRouterOptions(catalog.openRouter, vision)
    case '9router':
      return nineRouterOptions(catalog.nineRouter, otherLabel, vision)
    default:
      return []
  }
}
