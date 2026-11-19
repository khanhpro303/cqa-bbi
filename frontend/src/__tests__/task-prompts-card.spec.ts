// @vitest-environment happy-dom
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, shallowMount } from '@vue/test-utils'
import TaskPromptsCard from '../components/TaskPromptsCard.vue'

const mocks = vi.hoisted(() => ({ get: vi.fn(), put: vi.fn() }))
vi.mock('../api', () => ({ default: mocks }))
vi.mock('vue-i18n', () => ({ useI18n: () => ({ t: (key: string) => key }) }))

const prompt = (key: string, group: string, customized = false) => ({
  key, group, title: key, description: 'd', default: `default ${key}`, format: group === 'erp' ? 'kw: {{keyword}}' : '',
  value: customized ? `custom ${key}` : `default ${key}`, customized,
})

async function mountCard() {
  const wrapper = shallowMount(TaskPromptsCard, {
    props: { tenantId: 'tenant-1' },
    global: { mocks: { $t: (key: string) => key }, renderStubDefaultSlot: true },
  })
  await flushPromises()
  return wrapper
}

describe('TaskPromptsCard', () => {
  beforeEach(() => {
    mocks.get.mockReset().mockResolvedValue({ data: { data: [
      prompt('chat_memory_summary', 'chat_memory', true),
      prompt('chat_memory_issues', 'chat_memory'),
      prompt('erp_product_code_match', 'erp'),
    ] } })
    mocks.put.mockReset().mockImplementation(async (_url: string, body: { value: string }) => ({
      data: { ...prompt('chat_memory_issues', 'chat_memory', true), value: body.value },
    }))
  })

  it('lists prompts by group and saves an edited prompt', async () => {
    const wrapper = await mountCard()
    expect(mocks.get).toHaveBeenCalledWith('/tenants/tenant-1/settings/ai-prompts')
    expect(wrapper.find('[data-testid="task-prompt-chat_memory_summary"]').exists()).toBe(true)
    expect(wrapper.find('[data-testid="task-prompt-erp_product_code_match"]').exists()).toBe(true)

    const vm = wrapper.vm as any
    vm.openEditor(vm.prompts[1])
    expect(vm.draft).toBe('default chat_memory_issues')
    vm.draft = 'Chỉ báo khi khách khiếu nại.'
    await vm.saveDraft()
    expect(mocks.put).toHaveBeenCalledWith('/tenants/tenant-1/settings/ai-prompts/chat_memory_issues', { value: 'Chỉ báo khi khách khiếu nại.' })
    expect(vm.prompts[1].customized).toBe(true)
    expect(vm.editorOpen).toBe(false)
  })
})
