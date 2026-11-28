// @vitest-environment happy-dom
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, shallowMount } from '@vue/test-utils'
import TaskModelsPanel from '../components/ai-agents/TaskModelsPanel.vue'
import { resetAIModelCatalog } from '../composables/aiModelCatalog'

const mocks = vi.hoisted(() => ({ get: vi.fn(), put: vi.fn(), post: vi.fn() }))
vi.mock('../api', () => ({ default: mocks }))
vi.mock('vue-i18n', () => ({
  useI18n: () => ({ t: (key: string, params?: Record<string, unknown>) => params ? `${key}:${JSON.stringify(params)}` : key }),
}))

const taskResponse = {
  default_provider: 'claude',
  default_model: 'claude-sonnet-4-6',
  tasks: [
    { key: 'message_intent', label: 'Phân loại ý định tin nhắn', group: 'Chatbot', capability: 'text', provider: '', model: '' },
    { key: 'image_reader', label: 'Đọc ảnh khách gửi', group: 'Chatbot', capability: 'vision', provider: 'openai', model: 'gpt-5-mini' },
    { key: 'cloudify_order_approval', label: 'Nhận diện duyệt đơn Cloudify', group: 'ERP', capability: 'text', provider: '', model: '' },
  ],
}

const savedSettings = {
  ai_provider: 'claude',
  ai_model: 'claude-sonnet-4-6',
  ai_api_key_claude: '••••',
  ai_api_key_openai: '••••',
  ai_base_url_openai: 'https://proxy.example.com/v1',
}

function routeGet(overrides: Record<string, unknown> = {}) {
  return async (url: string) => {
    const table: Record<string, unknown> = {
      '/tenants/tenant-1/settings': { settings: savedSettings },
      '/tenants/tenant-1/settings/ai/tasks': structuredClone(taskResponse),
      '/tenants/tenant-1/settings/ai/flows': { flows: {} },
      ...overrides,
    }
    const value = table[url]
    if (value instanceof Error) throw value
    if (value && typeof value === 'object' && 'throw' in value) throw (value as { throw: unknown }).throw
    return { data: value ?? {} }
  }
}

const agents = [
  { id: 'a1', key: 'khach', name: 'Trợ lý khách hàng', description: '', audience: 'customer', runtime: 'langflow', langflow_flow_id: '', provider: '', model: '', tools: [], knowledge_tags: [], options: {}, is_active: true },
  { id: 'a2', key: 'cong-no', name: 'Bot công nợ', description: '', audience: 'staff', runtime: 'native', langflow_flow_id: '', provider: '9router', model: 'cx/gpt-5.6-luna', tools: [], knowledge_tags: [], options: {}, is_active: true },
  { id: 'a3', key: 'rieng', name: 'Agent flow riêng', description: '', audience: 'all', runtime: 'langflow', langflow_flow_id: 'flow-xyz', provider: '', model: '', tools: [], knowledge_tags: [], options: {}, is_active: false },
]

async function mountPanel(props: Record<string, unknown> = {}) {
  const wrapper = shallowMount(TaskModelsPanel, {
    props: { tenantId: 'tenant-1', canEdit: true, agents: [], ...props },
    global: {
      stubs: Object.fromEntries([
        'VAlert', 'VAutocomplete', 'VBtn', 'VCard', 'VChip', 'VCol', 'VCombobox', 'VDivider', 'VIcon',
        'VProgressLinear', 'VRow', 'VSelect', 'VTable', 'RouterLink',
      ].map(name => [name, true])),
      mocks: { $t: (key: string) => key },
      renderStubDefaultSlot: true,
    },
  })
  await flushPromises()
  return wrapper
}

describe('Task models panel (Agents AI)', () => {
  beforeEach(() => {
    resetAIModelCatalog()
    mocks.get.mockReset().mockImplementation(routeGet())
    mocks.put.mockReset().mockResolvedValue({ data: structuredClone(taskResponse) })
    mocks.post.mockReset().mockResolvedValue({ data: {} })
  })

  it('splits chatbot tasks from other business tasks', async () => {
    const wrapper = await mountPanel()
    const vm = wrapper.vm as any
    expect(vm.taskSections.map((s: any) => [s.id, s.groups.map((g: any) => g.name)])).toEqual([
      ['chatbot', ['Chatbot']],
      ['other', ['ERP']],
    ])
    expect(wrapper.find('[data-testid="tm-group-Chatbot"]').text()).toContain('Đọc ảnh khách gửi')
    expect(wrapper.find('[data-testid="tm-group-ERP"]').text()).toContain('Nhận diện duyệt đơn Cloudify')
    expect(wrapper.find('[data-testid="tm-section-other"]').exists()).toBe(true)
    wrapper.unmount()
  })

  it('saves per-task provider and model overrides', async () => {
    const wrapper = await mountPanel()
    const vm = wrapper.vm as any
    vm.selectTaskProvider(vm.taskAISettings[0], 'gemini')
    expect(vm.taskAISettings[0].model).toBe('gemini-2.5-flash')
    await vm.saveTaskAISettings()
    expect(mocks.put).toHaveBeenCalledWith('/tenants/tenant-1/settings/ai/tasks', {
      tasks: [{ key: 'message_intent', provider: 'gemini', model: 'gemini-2.5-flash' }],
    })
    wrapper.unmount()
  })

  it('clears both override fields when a task returns to the default model', async () => {
    const wrapper = await mountPanel()
    const vm = wrapper.vm as any
    vm.selectTaskProvider(vm.taskAISettings[1], '')
    expect(vm.taskAISettings[1]).toEqual(expect.objectContaining({ provider: '', model: '' }))
    await vm.saveTaskAISettings()
    expect(mocks.put.mock.calls.at(-1)?.[1].tasks[0]).toEqual({ key: 'image_reader', provider: '', model: '' })
    wrapper.unmount()
  })

  it('saves the default model with the saved key and base URL only', async () => {
    mocks.put.mockResolvedValue({ data: { message: 'saved' } })
    const wrapper = await mountPanel()
    const vm = wrapper.vm as any
    expect(vm.defaultDraft).toEqual({ provider: 'claude', model: 'claude-sonnet-4-6' })
    vm.selectDefaultProvider('openai')
    vm.defaultDraft.model = 'gpt-5-mini'
    await vm.saveDefault()
    expect(mocks.put).toHaveBeenCalledWith('/tenants/tenant-1/settings/ai', {
      provider: 'openai', api_key: '••••', base_url: 'https://proxy.example.com/v1', model: 'gpt-5-mini',
    })
    expect(vm.defaultSaved).toEqual({ provider: 'openai', model: 'gpt-5-mini' })

    // A provider without a saved key cannot become the default.
    mocks.put.mockClear()
    vm.selectDefaultProvider('gemini')
    await vm.saveDefault()
    expect(mocks.put).not.toHaveBeenCalled()
    wrapper.unmount()
  })

  it('renders an actionable error when task settings cannot be loaded', async () => {
    mocks.get.mockImplementation(routeGet({
      '/tenants/tenant-1/settings/ai/tasks': { throw: { response: { data: { error: 'Lang cấu hình tác vụ chưa sẵn sàng' } } } },
    }))
    const wrapper = await mountPanel()
    const error = wrapper.get('[data-testid="ai-task-settings-error"]')
    expect(error.text()).toContain('Lang cấu hình tác vụ chưa sẵn sàng')
    expect(error.text()).toContain('tm_reload')
    wrapper.unmount()
  })

  it('syncs the selected provider-model pair to Langflow and reloads the flow', async () => {
    const model = { name: 'gpt-5.4', provider: 'OpenAI' }
    const next = { name: 'gpt-4o-mini', provider: 'OpenAI' }
    const modelView = {
      role: 'internal', flow_id: 'flow-1', name: 'BBI_RAG_Bot_Int',
      model: model.name, provider: model.provider, managed: true,
      options: [model, next], shared_roles: ['internal', 'public'],
    }
    let flowReads = 0
    const base = routeGet()
    mocks.get.mockImplementation(async (url: string) => {
      if (url.endsWith('/settings/ai/flows')) {
        flowReads++
        const internal = { ...modelView, model: flowReads > 1 ? next.name : model.name }
        return { data: { flows: { internal, public: { ...internal, role: 'public', name: 'BBI_RAG_Bot_Ext' } } } }
      }
      return base(url)
    })
    mocks.put.mockResolvedValue({ data: { updated_roles: ['internal', 'public'] } })
    const wrapper = await mountPanel({ agents })
    const vm = wrapper.vm as any
    expect(wrapper.get('[data-testid="ai-flow-internal"]').text()).toContain('BBI_RAG_Bot_Int')
    vm.flowSelections.internal = next
    await vm.saveFlowAISetting('internal')
    expect(mocks.put).toHaveBeenCalledWith('/tenants/tenant-1/settings/ai/flows/internal', {
      provider: next.provider, model: next.name,
    })
    expect(vm.flowSettings.public.model).toBe(next.name)
    expect(flowReads).toBe(2)

    // Agents show their model (native) or the flow they run on (Langflow).
    expect(vm.agentFlowLabel(agents[0])).toBe('BBI_RAG_Bot_Ext')
    expect(vm.agentFlowLabel(agents[2])).toContain('flow-xyz')
    expect(wrapper.get('[data-testid="tm-agent-cong-no"]').text()).toContain('9router / cx/gpt-5.6-luna')
    wrapper.unmount()
  })

  it('suggests live 9router routes for tasks and filters vision models', async () => {
    const catalog = [
      { id: 'ag/gemini-4-neon', vision: true },
      { id: 'cx/gpt-5.6-luna', vision: true },
      { id: 'ag/claude-sonnet-5-5' },
    ]
    mocks.get.mockImplementation(routeGet({
      '/tenants/tenant-1/settings': { settings: { ...savedSettings, ai_api_key_9router: '••••', ai_base_url_9router: 'https://router.example.com/v1' } },
      '/tenants/tenant-1/settings/ai/9router-models': { models: catalog },
    }))
    const wrapper = await mountPanel()
    const vm = wrapper.vm as any
    vm.selectTaskProvider(vm.taskAISettings[1], '9router')
    await flushPromises()
    expect(vm.modelItems('9router', false).map((o: any) => o.title)).toEqual([
      'Codex', 'cx/gpt-5.6-luna', 'Gemini', 'ag/gemini-4-neon', 'Claude', 'ag/claude-sonnet-5-5',
    ])
    expect(vm.modelItems('9router', true).map((o: any) => o.title)).toEqual([
      'Codex', 'cx/gpt-5.6-luna', 'Gemini', 'ag/gemini-4-neon',
    ])
    // Picking 9router on another task reuses the loaded list.
    vm.selectTaskProvider(vm.taskAISettings[0], '9router')
    await flushPromises()
    expect(mocks.get.mock.calls.filter(([url]) => url.endsWith('/settings/ai/9router-models'))).toHaveLength(1)
    wrapper.unmount()
  })

  it('stays read-only for users without edit rights', async () => {
    const wrapper = await mountPanel({ canEdit: false })
    expect(wrapper.find('[data-testid="save-ai-task-settings"]').exists()).toBe(false)
    expect(wrapper.find('[data-testid="tm-default-save"]').exists()).toBe(false)
    wrapper.unmount()
  })
})
