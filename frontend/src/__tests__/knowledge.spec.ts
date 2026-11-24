// @vitest-environment happy-dom
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, shallowMount } from '@vue/test-utils'
import Knowledge from '../views/Knowledge.vue'
import KnowledgePlayground from '../components/KnowledgePlayground.vue'

const mocks = vi.hoisted(() => ({ get: vi.fn(), post: vi.fn(), patch: vi.fn(), delete: vi.fn() }))
vi.mock('../api', () => ({ default: mocks }))
vi.mock('vue-router', () => ({ useRoute: () => ({ params: { tenantId: 'tenant-1' }, query: {} }) }))
vi.mock('vue-i18n', () => ({ useI18n: () => ({ t: (key: string) => key }) }))
vi.mock('../stores/auth', () => ({ useAuthStore: () => ({ canEdit: () => true, tenantPerms: { role: 'owner' } }) }))

const doc = { id: 'd1', title: 'Bảo hành', file_name: 'bh.pdf', source: 'upload', audience: 'public', enabled: true, status: 'ready', char_count: 100, chunk_count: 2, created_by: 'a@b', created_at: '2026-11-22T10:00:00Z' }

describe('Knowledge page', () => {
  beforeEach(() => {
    mocks.get.mockReset().mockResolvedValue({ data: { documents: [doc], vector_store: 'none', supported_extensions: ['.pdf'], max_upload_mb: 20 } })
    mocks.post.mockReset().mockResolvedValue({ data: {} })
    mocks.patch.mockReset().mockResolvedValue({ data: {} })
  })

  it('lists documents and shows the keyword-search warning', async () => {
    const wrapper = shallowMount(Knowledge, { global: { mocks: { $t: (k: string) => k }, renderStubDefaultSlot: true } })
    await flushPromises()
    expect(mocks.get).toHaveBeenCalledWith('/tenants/tenant-1/knowledge', { params: { view: 'active' } })
    const vm = wrapper.vm as any
    expect(vm.documents).toHaveLength(1)
    expect(vm.vectorStore).toBe('none')
    expect(wrapper.find('[data-testid="kb-keyword-warning"]').exists()).toBe(true)
  })

  it('mounts the playground in the current tenant', () => {
    const wrapper = shallowMount(Knowledge, { global: { mocks: { $t: (k: string) => k }, renderStubDefaultSlot: true } })
    expect(wrapper.getComponent(KnowledgePlayground).props()).toEqual({
      base: '/tenants/tenant-1/knowledge',
      isAdmin: true,
    })
    wrapper.unmount()
  })

  it('uploads each file as multipart with the chosen audience', async () => {
    const wrapper = shallowMount(Knowledge, { global: { mocks: { $t: (k: string) => k }, renderStubDefaultSlot: true } })
    await flushPromises()
    const vm = wrapper.vm as any
    vm.uploadFiles = [new File(['a'], 'a.txt'), new File(['b'], 'b.txt')]
    await vm.upload()
    const uploads = mocks.post.mock.calls.filter((c: any[]) => c[0] === '/tenants/tenant-1/knowledge/upload')
    expect(uploads).toHaveLength(2)
    expect((uploads[0][1] as FormData).get('audience')).toBe('public')
  })

  it('toggles a document off through PATCH', async () => {
    const wrapper = shallowMount(Knowledge, { global: { mocks: { $t: (k: string) => k }, renderStubDefaultSlot: true } })
    await flushPromises()
    await (wrapper.vm as any).patchDoc(doc, { enabled: false })
    expect(mocks.patch).toHaveBeenCalledWith('/tenants/tenant-1/knowledge/d1', { enabled: false })
  })

  it('shows a saved summary for an indexing failure without rendering technical errors or chunks', async () => {
    const failedDoc = { ...doc, status: 'failed', error: 'vector upsert: context deadline exceeded' }
    mocks.get
      .mockResolvedValueOnce({ data: { documents: [failedDoc] } })
      .mockResolvedValueOnce({
        data: { document: failedDoc, summary: { status: 'ready', text: 'Bảo hành trong 12 tháng.' }, chunks: [{ content: 'Raw passage' }] },
      })
    const wrapper = shallowMount(Knowledge, { global: { mocks: { $t: (k: string) => k }, renderStubDefaultSlot: true } })
    try {
      await flushPromises()
      await wrapper.get('.document-link').trigger('click')
      await flushPromises()
      expect(wrapper.get('.document-summary').text()).toBe('Bảo hành trong 12 tháng.')
      expect(wrapper.text()).toContain('kb_index_failed')
      expect(wrapper.text()).not.toContain('context deadline exceeded')
      expect(wrapper.text()).not.toContain('Raw passage')
      expect(mocks.post).not.toHaveBeenCalled()
    } finally {
      wrapper.unmount()
    }
  })

  it('generates a missing summary and refreshes the dialog when it is ready', async () => {
    vi.useFakeTimers({ toFake: ['setTimeout', 'clearTimeout'] })
    mocks.get
      .mockResolvedValueOnce({ data: { documents: [doc] } })
      .mockResolvedValueOnce({ data: { document: doc, summary: { status: 'missing' } } })
      .mockResolvedValueOnce({ data: { document: doc, summary: { status: 'ready', text: 'Mũ bảo hành 12 tháng.' } } })
    mocks.post.mockResolvedValueOnce({ data: { summary: { status: 'processing' } } })
    const wrapper = shallowMount(Knowledge, { global: { mocks: { $t: (k: string) => k }, renderStubDefaultSlot: true } })
    try {
      await flushPromises()
      await wrapper.get('.document-link').trigger('click')
      await flushPromises()
      await wrapper.get('[data-testid="kb-generate-summary"]').trigger('click')
      await flushPromises()
      expect(mocks.post).toHaveBeenCalledWith('/tenants/tenant-1/knowledge/d1/summary')
      expect(wrapper.text()).toContain('kb_summary_processing')

      await vi.advanceTimersByTimeAsync(4000)
      expect(wrapper.get('.document-summary').text()).toBe('Mũ bảo hành 12 tháng.')
      expect(wrapper.find('[data-testid="kb-generate-summary"]').exists()).toBe(false)
    } finally {
      wrapper.unmount()
      vi.useRealTimers()
    }
  })
})
