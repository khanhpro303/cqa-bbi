// @vitest-environment happy-dom
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, shallowMount } from '@vue/test-utils'
import Settings from '../views/Settings.vue'

const mocks = vi.hoisted(() => ({ get: vi.fn(), put: vi.fn(), post: vi.fn() }))
vi.mock('../api', () => ({ default: mocks }))
vi.mock('vue-router', () => ({ useRoute: () => ({ params: { tenantId: 'tenant-1' }, query: {} }) }))
vi.mock('vue-i18n', () => ({ useI18n: () => ({ t: (key: string) => key }) }))

const taskResponse = {
  default_provider: 'claude',
  default_model: 'claude-sonnet-4-6',
  tasks: [
    {
      key: 'message_intent',
      label: 'Phân loại ý định tin nhắn',
      group: 'Chatbot',
      capability: 'text',
      provider: '',
      model: '',
    },
    {
      key: 'image_reader',
      label: 'Đọc ảnh khách gửi',
      group: 'Chatbot',
      capability: 'vision',
      provider: 'openai',
      model: 'gpt-5-mini',
    },
  ],
}

async function mountSettings() {
  const wrapper = shallowMount(Settings, {
    global: {
      stubs: Object.fromEntries([
        'VAlert', 'VAutocomplete', 'VBtn', 'VCard', 'VChip', 'VCol', 'VCombobox', 'VDivider', 'VIcon',
        'VList', 'VListItem', 'VProgressLinear', 'VRow', 'VSelect', 'VSnackbar',
        'VSwitch', 'VTextField',
      ].map(name => [name, true])),
      mocks: { $t: (key: string) => key },
      renderStubDefaultSlot: true,
    },
  })
  await flushPromises()
  return wrapper
}

describe('AI task model settings', () => {
  beforeEach(() => {
    mocks.get.mockReset().mockImplementation(async (url: string) => {
      if (url.endsWith('/settings/ai/tasks')) return { data: structuredClone(taskResponse) }
      if (url.endsWith('/settings')) {
        return { data: { settings: { ai_provider: 'claude', ai_model: 'claude-sonnet-4-6' } } }
      }
      return { data: {} }
    })
    mocks.put.mockReset().mockResolvedValue({ data: structuredClone(taskResponse) })
    mocks.post.mockReset().mockResolvedValue({ data: {} })
  })

  it('shows inherited defaults and saves per-task provider and model overrides', async () => {
    const wrapper = await mountSettings()
    const section = wrapper.get('[data-testid="ai-task-model-settings"]')
    expect(section.text()).toContain('Chatbot')
    expect(section.text()).toContain('Kế thừa claude / claude-sonnet-4-6')
    expect(section.text()).toContain('Đọc hình ảnh')

    const vm = wrapper.vm as any
    vm.selectTaskProvider(vm.taskAISettings[0], 'gemini')
    expect(vm.taskAISettings[0].model).toBe('gemini-2.5-flash')
    await vm.saveTaskAISettings()

    expect(mocks.put).toHaveBeenCalledWith('/tenants/tenant-1/settings/ai/tasks', {
      tasks: [
        { key: 'message_intent', provider: 'gemini', model: 'gemini-2.5-flash' },
      ],
    })
    wrapper.unmount()
  })

  it('clears both override fields when a task returns to the global default', async () => {
    const wrapper = await mountSettings()
    const vm = wrapper.vm as any
    vm.selectTaskProvider(vm.taskAISettings[1], '')
    expect(vm.taskAISettings[1]).toEqual(expect.objectContaining({ provider: '', model: '' }))
    await vm.saveTaskAISettings()
    expect(mocks.put.mock.calls.at(-1)?.[1].tasks[0]).toEqual({
      key: 'image_reader', provider: '', model: '',
    })
    wrapper.unmount()
  })

  it('renders an actionable error when task settings cannot be loaded', async () => {
    mocks.get.mockImplementation(async (url: string) => {
      if (url.endsWith('/settings/ai/tasks')) {
        throw { response: { data: { error: 'Lang cấu hình tác vụ chưa sẵn sàng' } } }
      }
      return { data: { settings: {} } }
    })
    const wrapper = await mountSettings()
    const error = wrapper.get('[data-testid="ai-task-settings-error"]')
    expect(error.text()).toContain('Lang cấu hình tác vụ chưa sẵn sàng')
    expect(error.text()).toContain('Tải lại')
    wrapper.unmount()
  })

  it('syncs the selected provider-model pair and reloads the applied flow', async () => {
    const model = { name: 'gpt-5.4', provider: 'OpenAI' }
    const next = { name: 'gpt-4o-mini', provider: 'OpenAI' }
    const modelView = {
      role: 'internal', flow_id: 'flow-1', name: 'BBI_RAG_Bot_Int',
      model: model.name, provider: model.provider, managed: true,
      options: [model, next], shared_roles: ['internal', 'public'],
    }
    let flowReads = 0
    mocks.get.mockImplementation(async (url: string) => {
      if (url.endsWith('/settings/ai/flows')) {
        flowReads++
        const internal = { ...modelView, model: flowReads > 1 ? next.name : model.name }
        return { data: { flows: { internal, public: { ...internal, role: 'public' } } } }
      }
      if (url.endsWith('/settings/ai/tasks')) return { data: structuredClone(taskResponse) }
      return { data: { settings: {} } }
    })
    mocks.put.mockResolvedValue({ data: { updated_roles: ['internal', 'public'] } })
    const wrapper = await mountSettings()
    const vm = wrapper.vm as any
    expect(wrapper.get('[data-testid="ai-flow-internal"]').text()).toContain('BBI_RAG_Bot_Int')
    vm.flowSelections.internal = next
    await vm.saveFlowAISetting('internal')
    expect(mocks.put).toHaveBeenCalledWith('/tenants/tenant-1/settings/ai/flows/internal', {
      provider: next.provider, model: next.name,
    })
    expect(vm.flowSettings.public.model).toBe(next.name)
    expect(flowReads).toBe(2)
    wrapper.unmount()
  })
})
