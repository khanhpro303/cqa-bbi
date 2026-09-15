// @vitest-environment happy-dom
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, shallowMount } from '@vue/test-utils'
import AIEngines from '../views/AIEngines.vue'

const mocks = vi.hoisted(() => ({ get: vi.fn(), put: vi.fn() }))
vi.mock('../api', () => ({ default: mocks }))
vi.mock('vue-router', () => ({ useRoute: () => ({ params: { tenantId: 'tenant-1' } }) }))
vi.mock('vuetify', () => ({ useTheme: () => ({ global: { current: { value: { dark: false } } } }) }))
vi.mock('vue-i18n', () => ({ useI18n: () => ({ t: (key: string) => key }) }))

async function mountSettings() {
  const wrapper = shallowMount(AIEngines, {
    global: {
      stubs: Object.fromEntries([
        'VCard', 'VCardTitle', 'VCardText', 'VCardActions', 'VIcon', 'VSpacer', 'VDivider',
        'VBtn', 'VTextField', 'VTextarea', 'VDialog', 'VAlert', 'VSwitch', 'VCheckbox',
        'VSelect', 'VChip', 'VTooltip', 'VExpansionPanels', 'VExpansionPanel',
        'VExpansionPanelTitle', 'VExpansionPanelText', 'VSnackbar', 'VTable',
        'VAutocomplete', 'VRow', 'VCol', 'VList', 'VListItem',
      ].map(name => [name, true])),
      mocks: { $t: (key: string) => key },
      renderStubDefaultSlot: true,
    },
  })
  await flushPromises()
  return wrapper
}

describe('Messenger insights prompt settings', () => {
  beforeEach(() => {
    mocks.get.mockReset().mockImplementation(async (url: string) => ({
      data: url.endsWith('/settings') ? { settings: {
        ai_engine_system_prompt: 'public prompt',
        ai_engine_system_prompt_internal: 'internal prompt',
        ai_engine_system_prompt_crm_analysis: 'Zalo prompt',
        ai_engine_system_prompt_messenger_insights: 'Messenger override',
        ai_engine_system_prompt_messenger_insights_default: 'Default {{rules}}',
      } } : [],
    }))
    mocks.put.mockReset().mockResolvedValue({ data: {} })
  })

  it('keeps edits in the modal until confirmed, then persists the distinct prompt', async () => {
    const wrapper = await mountSettings()
    const langflowSection = wrapper.get('[data-testid="langflow-config"]')
    const crmSection = wrapper.get('[data-testid="crm-system-prompts"]')
    expect(crmSection.text()).toContain('crm_system_prompts_section')
    const crmLabels = crmSection.findAll('v-text-field-stub').map(field => field.attributes('label'))
    expect(crmLabels).toEqual(['crm_system_prompt_crm_analysis', 'messenger_insights_system_prompt'])
    expect(langflowSection.findAll('v-text-field-stub').map(field => field.attributes('label'))).not.toContain('crm_system_prompt_crm_analysis')
    expect(langflowSection.text()).toContain('test_connection')
    expect(crmSection.text()).not.toContain('test_connection')
    const vm = wrapper.vm as any
    vm.openPromptModal('messenger_insights')
    expect(vm.promptModalText).toBe('Messenger override')
    vm.promptModalText = 'Edited {{rules}}'
    vm.promptModalOpen = false
    vm.openPromptModal('messenger_insights')
    expect(vm.promptModalText).toBe('Messenger override')
    vm.promptModalText = 'Edited {{rules}}'
    vm.savePromptModal()
    expect(mocks.put).not.toHaveBeenCalled()
    await vm.save()
    expect(mocks.put).toHaveBeenCalledWith('/tenants/tenant-1/settings/ai-engines', expect.objectContaining({
      system_prompt: 'public prompt',
      system_prompt_internal: 'internal prompt',
      system_prompt_crm_analysis: 'Zalo prompt',
      system_prompt_messenger_insights: 'Edited {{rules}}',
    }))
    wrapper.unmount()
  })

  it('restores the backend default in the editor and allows clearing the override', async () => {
    const wrapper = await mountSettings()
    const vm = wrapper.vm as any
    vm.openPromptModal('messenger_insights')
    await wrapper.vm.$nextTick()
    const restore = wrapper.findAll('v-btn-stub').find(button => button.text() === 'messenger_insights_restore_default')
    expect(restore).toBeDefined()
    await restore!.trigger('click')
    expect(vm.promptModalText).toBe('Default {{rules}}')
    expect(vm.langflow.systemPromptMessengerInsights).toBe('Messenger override')
    vm.savePromptModal()
    await vm.save()
    expect(mocks.put.mock.calls.at(-1)?.[1].system_prompt_messenger_insights).toBe('Default {{rules}}')
    vm.openPromptModal('messenger_insights')
    vm.promptModalText = ''
    vm.savePromptModal()
    await vm.save()
    expect(mocks.put.mock.calls.at(-1)?.[1].system_prompt_messenger_insights).toBe('')
    wrapper.unmount()
  })
})
