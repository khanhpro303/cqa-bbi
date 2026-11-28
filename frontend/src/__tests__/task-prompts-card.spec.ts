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

  it('keeps the save button off while a required marker is missing and explains a refused save', async () => {
    mocks.get.mockResolvedValueOnce({ data: { data: [
      { ...prompt('personal_zalo_handover', 'personal_zalo'), required: ['[CQA_REQUEST_HUMAN_REVIEW]'], default: 'trả [CQA_REQUEST_HUMAN_REVIEW]', value: 'trả [CQA_REQUEST_HUMAN_REVIEW]' },
    ] } })
    const wrapper = await mountCard()
    const vm = wrapper.vm as any
    vm.openEditor(vm.prompts[0])
    expect(vm.missingRequired).toEqual([])
    vm.draft = 'Báo nhân viên khi cần.'
    expect(vm.missingRequired).toEqual(['[CQA_REQUEST_HUMAN_REVIEW]'])

    mocks.put.mockRejectedValueOnce({ response: { data: { error: 'prompt_missing_required', missing: ['[CQA_REQUEST_HUMAN_REVIEW]'] } } })
    await vm.saveDraft()
    expect(vm.editorOpen).toBe(true)
  })

  it('filters prompts by search text and opens the matching groups', async () => {
    const wrapper = await mountCard()
    const vm = wrapper.vm as any
    expect(vm.groups.map((g: any) => g.key)).toEqual(['chat_memory', 'erp'])
    expect(vm.groups[0].customized).toBe(1)
    vm.search = 'product_code'
    await flushPromises()
    expect(vm.groups.map((g: any) => g.key)).toEqual(['erp'])
    expect(vm.openGroups).toEqual(['erp'])
  })
})

describe('TaskPromptsCard message templates', () => {
  it('previews a message template with sample data and the conditional line', async () => {
    mocks.get.mockReset().mockResolvedValue({ data: { data: [{
      key: 'personal_zalo_internbot_frame', group: 'personal_zalo', title: 'Khung', description: 'd', format: '', kind: 'message',
      required: ['{phan_hoi}'], customized: false,
      default: 'Internbot · {nhom}\n{neu_can_nhan_vien}Cần nhân viên xử lý\n{phan_hoi}',
      value: 'Internbot · {nhom}\n{neu_can_nhan_vien}Cần nhân viên xử lý\n{phan_hoi}',
      placeholders: [
        { name: 'nhom', description: 'n', example: 'Đại lý {phan_hoi}' },
        { name: 'neu_can_nhan_vien', description: 'c', line: true },
        { name: 'phan_hoi', description: 'p', example: 'Dạ còn ạ.' },
      ],
    }] } })
    const wrapper = shallowMount(TaskPromptsCard, {
      props: { tenantId: 'tenant-1' },
      global: { mocks: { $t: (key: string) => key }, renderStubDefaultSlot: true },
    })
    await flushPromises()
    const vm = wrapper.vm as any
    vm.openEditor(vm.prompts[0])
    expect(vm.isMessage).toBe(true)
    expect(vm.preview).toBe('Internbot · Đại lý {phan_hoi}\nDạ còn ạ.')
    vm.previewConditions = true
    expect(vm.preview).toBe('Internbot · Đại lý {phan_hoi}\nCần nhân viên xử lý\nDạ còn ạ.')
    vm.draft = '{khach_hang} {phan_hoi}'
    expect(vm.unknownPlaceholders).toEqual(['{khach_hang}'])
    vm.draft = 'Không có biến'
    expect(vm.missingRequired).toEqual(['{phan_hoi}'])
    vm.insertPlaceholder('phan_hoi')
    expect(vm.missingRequired).toEqual([])
  })
})
