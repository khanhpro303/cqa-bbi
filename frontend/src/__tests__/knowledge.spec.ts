// @vitest-environment happy-dom
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, shallowMount } from '@vue/test-utils'
import Knowledge from '../views/Knowledge.vue'

const mocks = vi.hoisted(() => ({ get: vi.fn(), post: vi.fn(), patch: vi.fn(), delete: vi.fn() }))
vi.mock('../api', () => ({ default: mocks }))
vi.mock('vue-router', () => ({ useRoute: () => ({ params: { tenantId: 'tenant-1' }, query: {} }) }))
vi.mock('vue-i18n', () => ({ useI18n: () => ({ t: (key: string) => key }) }))
vi.mock('../stores/auth', () => ({ useAuthStore: () => ({ canEdit: () => true, tenantPerms: { role: 'owner' } }) }))

const doc = { id: 'd1', title: 'Bảo hành', file_name: 'bh.pdf', source: 'upload', audience: 'public', enabled: true, status: 'ready', char_count: 100, chunk_count: 2, created_by: 'a@b', created_at: '2026-11-22T10:00:00Z' }

describe('Knowledge page', () => {
  beforeEach(() => {
    mocks.get.mockReset().mockResolvedValue({ data: { documents: [doc], vector_store: 'none', supported_extensions: ['.pdf'], max_upload_mb: 20 } })
    mocks.post.mockReset().mockResolvedValue({ data: { hits: [{ title: 'Bảo hành', content: '12 tháng', score: 0.9 }] } })
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

  it('tests a question as the customer bot', async () => {
    const wrapper = shallowMount(Knowledge, { global: { mocks: { $t: (k: string) => k }, renderStubDefaultSlot: true } })
    await flushPromises()
    const vm = wrapper.vm as any
    vm.tryQuery = 'bảo hành bao lâu'
    await vm.runSearch()
    expect(mocks.post).toHaveBeenCalledWith('/tenants/tenant-1/knowledge/search', { query: 'bảo hành bao lâu', agent_type: 'public' })
    expect(vm.hits[0].content).toBe('12 tháng')
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
})
