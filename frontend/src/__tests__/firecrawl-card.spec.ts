// @vitest-environment happy-dom
import { flushPromises, shallowMount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { defineComponent, h } from 'vue'
import FirecrawlCard from '../components/FirecrawlCard.vue'

const mocks = vi.hoisted(() => ({ get: vi.fn(), put: vi.fn(), post: vi.fn() }))
vi.mock('../api', () => ({ default: mocks }))
vi.mock('vue-i18n', () => ({ useI18n: () => ({ t: (key: string) => key }) }))

const saved = { has_key: true, enabled: true }
const cleared = { has_key: false, enabled: false }
const path = '/tenants/t1/knowledge/firecrawl'
const TextField = defineComponent({
  props: ['modelValue'],
  emits: ['update:modelValue'],
  setup: (props, { emit }) => () => h('input', {
    value: props.modelValue,
    onInput: (event: Event) => emit('update:modelValue', (event.target as HTMLInputElement).value),
  }),
})

function mountCard() {
  return shallowMount(FirecrawlCard, {
    props: { tenantId: 't1' },
    global: { stubs: { VTextField: TextField }, renderStubDefaultSlot: true },
  })
}

describe('FirecrawlCard', () => {
  beforeEach(() => {
    mocks.get.mockReset().mockResolvedValue({ data: saved })
    mocks.put.mockReset().mockResolvedValue({ data: saved })
    mocks.post.mockReset().mockResolvedValue({ data: { ok: true } })
  })

  it('shows only a placeholder for a saved key and clears a newly saved key from the field', async () => {
    const wrapper = mountCard()
    try {
      await flushPromises()
      expect(mocks.get).toHaveBeenCalledWith(path)
      const input = wrapper.get<HTMLInputElement>('[data-testid="firecrawl-key"]')
      expect(input.element.type).toBe('password')
      expect(input.element.value).toBe('')
      expect(input.element.placeholder).toBe('••••••••')
      await input.setValue('  fc-new-key  ')
      await wrapper.get('form').trigger('submit')
      await flushPromises()
      expect(mocks.put).toHaveBeenCalledWith(path, { api_key: 'fc-new-key' })
      expect(input.element.value).toBe('')
      expect(wrapper.get('[data-testid="firecrawl-feedback"]').text()).toBe('firecrawl_saved')
    } finally {
      wrapper.unmount()
    }
  })

  it('tests the saved key and clears it without sending any draft value', async () => {
    const wrapper = mountCard()
    try {
      await flushPromises()
      await wrapper.get('[data-testid="firecrawl-test"]').trigger('click')
      await flushPromises()
      expect(mocks.post).toHaveBeenCalledWith(`${path}/test`)
      expect(wrapper.get('[data-testid="firecrawl-feedback"]').text()).toBe('firecrawl_test_ok')

      mocks.put.mockResolvedValueOnce({ data: cleared })
      await wrapper.get<HTMLInputElement>('[data-testid="firecrawl-key"]').setValue('fc-unsaved')
      await wrapper.get('[data-testid="firecrawl-clear"]').trigger('click')
      await flushPromises()
      expect(mocks.put).toHaveBeenCalledWith(path, { clear_key: true })
      expect(wrapper.find('[data-testid="firecrawl-clear"]').exists()).toBe(false)
      expect(wrapper.get('[data-testid="firecrawl-feedback"]').text()).toBe('firecrawl_cleared')
    } finally {
      wrapper.unmount()
    }
  })
})
