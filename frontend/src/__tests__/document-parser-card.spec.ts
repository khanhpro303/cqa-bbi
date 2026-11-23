// @vitest-environment happy-dom
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, shallowMount } from '@vue/test-utils'
import DocumentParserCard from '../components/DocumentParserCard.vue'

const mocks = vi.hoisted(() => ({ get: vi.fn(), put: vi.fn(), post: vi.fn() }))
vi.mock('../api', () => ({ default: mocks }))

const saved = { api_url: '', default_api_url: 'https://api.unstructuredapp.io/general/v0/general', strategy: 'hi_res', languages: 'vie', has_key: true, env_configured: false, enabled: true }

describe('DocumentParserCard', () => {
  beforeEach(() => {
    mocks.get.mockReset().mockResolvedValue({ data: saved })
    mocks.put.mockReset().mockResolvedValue({ data: saved })
    mocks.post.mockReset().mockResolvedValue({ data: { ok: true } })
  })

  it('loads the setup without exposing the key and saves edits', async () => {
    const wrapper = shallowMount(DocumentParserCard, { props: { tenantId: 't1' }, global: { mocks: { $t: (k: string) => k }, renderStubDefaultSlot: true } })
    await flushPromises()
    const vm = wrapper.vm as any
    expect(mocks.get).toHaveBeenCalledWith('/tenants/t1/knowledge/parser')
    expect(vm.form).toEqual({ api_url: '', api_key: '', strategy: 'hi_res', languages: 'vie' })
    vm.form.api_key = 'new-key'
    await vm.save(false)
    expect(mocks.put).toHaveBeenCalledWith('/tenants/t1/knowledge/parser', { api_url: '', api_key: 'new-key', strategy: 'hi_res', languages: 'vie', clear_key: false })
    expect(vm.form.api_key).toBe('')
  })

  it('tests the connection', async () => {
    const wrapper = shallowMount(DocumentParserCard, { props: { tenantId: 't1' }, global: { mocks: { $t: (k: string) => k }, renderStubDefaultSlot: true } })
    await flushPromises()
    await (wrapper.vm as any).test()
    expect(mocks.post).toHaveBeenCalledWith('/tenants/t1/knowledge/parser/test')
    expect((wrapper.vm as any).testResult).toEqual({ ok: true })
  })
})
