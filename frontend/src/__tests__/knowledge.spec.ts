// @vitest-environment happy-dom
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, shallowMount } from '@vue/test-utils'
import { defineComponent, h } from 'vue'
import Knowledge from '../views/Knowledge.vue'
import KnowledgeDocumentImages from '../components/KnowledgeDocumentImages.vue'
import KnowledgeImagePreview from '../components/KnowledgeImagePreview.vue'
import KnowledgePlayground from '../components/KnowledgePlayground.vue'

const mocks = vi.hoisted(() => ({ get: vi.fn(), post: vi.fn(), patch: vi.fn(), delete: vi.fn() }))
vi.mock('../api', () => ({ default: mocks }))
vi.mock('vue-router', () => ({ useRoute: () => ({ params: { tenantId: 'tenant-1' }, query: {} }) }))
vi.mock('vue-i18n', () => ({ useI18n: () => ({ t: (key: string) => key }) }))
vi.mock('../stores/auth', () => ({ useAuthStore: () => ({ canEdit: () => true, tenantPerms: { role: 'owner' } }) }))

const doc = { id: 'd1', title: 'Bảo hành', file_name: 'bh.pdf', source: 'upload', audience: 'public', enabled: true, status: 'ready', char_count: 100, chunk_count: 2, created_by: 'a@b', created_at: '2026-11-22T10:00:00Z' }
const linkDoc = { ...doc, file_name: '', source: 'url', source_url: 'https://example.com/warranty', parser: 'firecrawl' }
const base = '/tenants/tenant-1/knowledge'
const image = {
  id: 'img1', document_id: doc.id, url: `/api/v1${base}/${doc.id}/images/img1/content`,
  file_name: 'warranty.png', caption: 'Bảng bảo hành', enabled: true, width: 64, height: 32,
}

const LinkField = defineComponent({
  props: ['modelValue'],
  emits: ['update:modelValue'],
  setup: (props, { emit }) => () => h('input', {
    value: props.modelValue,
    onInput: (event: Event) => emit('update:modelValue', (event.target as HTMLInputElement).value),
  }),
})
const AudienceField = defineComponent({
  props: ['modelValue'],
  emits: ['update:modelValue'],
  setup: (props, { emit }) => () => h('select', {
    value: props.modelValue,
    onChange: (event: Event) => emit('update:modelValue', (event.target as HTMLSelectElement).value),
  }, [h('option', { value: 'public' }), h('option', { value: 'internal' })]),
})

function mountLinkImport() {
  return shallowMount(Knowledge, {
    global: {
      mocks: { $t: (key: string) => key },
      stubs: { VTextField: LinkField, VSelect: AudienceField },
      renderStubDefaultSlot: true,
    },
  })
}

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

  it('offers file and text input and still lists previously imported documents', async () => {
    const imported = { ...doc, source: 'astra_import', file_name: doc.title }
    mocks.get.mockResolvedValueOnce({ data: { documents: [imported] } })
    const wrapper = shallowMount(Knowledge, { global: { mocks: { $t: (k: string) => k }, renderStubDefaultSlot: true } })
    try {
      await flushPromises()
      expect(wrapper.get('[prepend-icon="mdi-upload"]').text()).toBe('kb_upload')
      expect(wrapper.get('[prepend-icon="mdi-text-box-plus-outline"]').text()).toBe('kb_add_text')
      expect(wrapper.text()).not.toContain('kb_import_astra')
      expect(wrapper.get('.document-link').text()).toBe(imported.title)
      expect(wrapper.text()).toContain('kb_source_astra')
    } finally {
      wrapper.unmount()
    }
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

  it('creates a text document and sends its images and captions in the same order', async () => {
    const textDoc = { ...doc, source: 'text' }
    mocks.post
      .mockResolvedValueOnce({ data: textDoc })
      .mockResolvedValueOnce({ data: { images: [image] } })
    const wrapper = shallowMount(Knowledge, { global: { mocks: { $t: (k: string) => k }, renderStubDefaultSlot: true } })
    try {
      const vm = wrapper.vm as any
      vm.textDialog = true
      vm.textTitle = doc.title
      vm.textContent = 'Điều kiện bảo hành'
      vm.textImageDrafts = [
        { key: 1, file: new File(['first'], image.file_name, { type: 'image/png' }), caption: image.caption },
        { key: 2, file: new File(['second'], 'terms.jpg', { type: 'image/jpeg' }), caption: '' },
      ]
      await vm.saveText()
      expect(mocks.post.mock.calls[0]).toEqual([`${base}/text`, {
        title: doc.title, content: 'Điều kiện bảo hành', audience: 'public',
      }])
      const [url, form] = mocks.post.mock.calls[1]!
      expect(url).toBe(`${base}/${doc.id}/images`)
      expect((form as FormData).getAll('images').map((file) => (file as File).name)).toEqual([image.file_name, 'terms.jpg'])
      expect((form as FormData).get('captions')).toBe(JSON.stringify([image.caption, '']))
      expect(vm.textImageDrafts).toEqual([])
    } finally {
      wrapper.unmount()
    }
  })

  it('opens the saved document with the original attachment draft when upload fails', async () => {
    const textDoc = { ...doc, source: 'text' }
    mocks.get.mockImplementation(async (path: string) => ({
      data: path === `${base}/${doc.id}` ? { document: textDoc, images: [], summary: { status: 'missing' } } : { documents: [textDoc] },
    }))
    mocks.post.mockResolvedValueOnce({ data: textDoc }).mockRejectedValueOnce(new Error('upload failed'))
    const wrapper = shallowMount(Knowledge, { global: { mocks: { $t: (k: string) => k }, renderStubDefaultSlot: true } })
    try {
      const vm = wrapper.vm as any
      const draft = { key: 1, file: new File(['png'], image.file_name, { type: 'image/png' }), caption: image.caption }
      vm.textDialog = true
      vm.textTitle = doc.title
      vm.textContent = 'Điều kiện bảo hành'
      vm.textImageDrafts = [draft]
      await vm.saveText()
      await flushPromises()
      expect(vm.textDialog).toBe(false)
      expect(vm.docDialog).toBe(true)
      expect(wrapper.getComponent(KnowledgeDocumentImages).props('drafts')).toEqual([draft])
      expect(wrapper.text()).toContain('kb_images_partial_upload')
      expect(mocks.post).toHaveBeenCalledTimes(2)
    } finally {
      wrapper.unmount()
    }
  })

  it('toggles a document off through PATCH', async () => {
    const wrapper = shallowMount(Knowledge, { global: { mocks: { $t: (k: string) => k }, renderStubDefaultSlot: true } })
    await flushPromises()
    await (wrapper.vm as any).patchDoc(doc, { enabled: false })
    expect(mocks.patch).toHaveBeenCalledWith('/tenants/tenant-1/knowledge/d1', { enabled: false })
  })

  it('imports a public URL with the chosen audience and reloads the processing document', async () => {
    mocks.get
      .mockResolvedValueOnce({ data: { documents: [], firecrawl_enabled: true } })
      .mockResolvedValueOnce({ data: { documents: [{ ...linkDoc, status: 'processing', chunk_count: 0 }], firecrawl_enabled: true } })
      .mockResolvedValueOnce({ data: { document: linkDoc, summary: { status: 'missing' } } })
    const wrapper = mountLinkImport()
    try {
      await flushPromises()
      await wrapper.get('[data-testid="kb-add-link"]').trigger('click')
      await wrapper.get('[data-testid="kb-link-url"]').setValue(`  ${linkDoc.source_url}  `)
      await wrapper.get('[data-testid="kb-link-title"]').setValue(' Bảo hành ')
      await wrapper.get('[data-testid="kb-link-audience"]').setValue('internal')
      await wrapper.get('[data-testid="kb-link-form"]').trigger('submit')
      await flushPromises()
      expect(mocks.post).toHaveBeenCalledWith('/tenants/tenant-1/knowledge/url', {
        url: linkDoc.source_url,
        title: linkDoc.title,
        audience: 'internal',
      })
      expect(wrapper.text()).toContain('kb_link_extracting')
      expect(wrapper.text()).toContain('kb_source_link')
      await wrapper.get('.document-link').trigger('click')
      await flushPromises()
      const source = wrapper.get('[data-testid="kb-source-url"]')
      expect(source.attributes('href')).toBe(linkDoc.source_url)
      expect(source.attributes('rel')).toContain('noopener')
    } finally {
      wrapper.unmount()
    }
  })

  it('directs the user to AI Engine when Firecrawl is missing', async () => {
    const wrapper = mountLinkImport()
    try {
      await flushPromises()
      await wrapper.get('[data-testid="kb-add-link"]').trigger('click')
      expect(wrapper.get('[data-testid="kb-firecrawl-setup"]').text()).toContain('nav_ai_engines')
      await wrapper.get('[data-testid="kb-link-url"]').setValue(linkDoc.source_url)
      await wrapper.get('[data-testid="kb-link-form"]').trigger('submit')
      expect(mocks.post).not.toHaveBeenCalled()
    } finally {
      wrapper.unmount()
    }
  })

  it('rejects unsafe URL schemes in the form and as a stored source', async () => {
    const unsafeURL = 'javascript:alert(1)'
    const unsafeDoc = { ...linkDoc, source_url: unsafeURL }
    mocks.get
      .mockResolvedValueOnce({ data: { documents: [unsafeDoc], firecrawl_enabled: true } })
      .mockResolvedValueOnce({ data: { document: unsafeDoc, summary: { status: 'missing' } } })
    const wrapper = mountLinkImport()
    try {
      await flushPromises()
      await wrapper.get('[data-testid="kb-add-link"]').trigger('click')
      await wrapper.get('[data-testid="kb-link-url"]').setValue(unsafeURL)
      await wrapper.get('[data-testid="kb-link-form"]').trigger('submit')
      expect(wrapper.get('[data-testid="kb-link-error"]').text()).toBe('kb_link_invalid')
      expect(mocks.post).not.toHaveBeenCalled()
      await wrapper.get('.document-link').trigger('click')
      await flushPromises()
      expect(wrapper.find('[data-testid="kb-source-url"]').exists()).toBe(false)
    } finally {
      wrapper.unmount()
    }
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

  it('keeps the attachment editor mounted through a failed summary poll and retry', async () => {
    vi.useFakeTimers({ toFake: ['setTimeout', 'clearTimeout'] })
    mocks.get
      .mockResolvedValueOnce({ data: { documents: [doc] } })
      .mockResolvedValueOnce({ data: { document: doc, images: [image], summary: { status: 'processing' } } })
      .mockRejectedValueOnce(new Error('temporary connection problem'))
      .mockResolvedValueOnce({ data: { document: doc, images: [image], summary: { status: 'ready', text: 'Tóm tắt bảo hành' } } })
    const wrapper = shallowMount(Knowledge, { global: { mocks: { $t: (k: string) => k }, renderStubDefaultSlot: true } })
    try {
      await flushPromises()
      await wrapper.get('.document-link').trigger('click')
      await flushPromises()
      const editor = wrapper.getComponent(KnowledgeDocumentImages).vm
      await vi.advanceTimersByTimeAsync(4000)
      expect(wrapper.text()).toContain('kb_summary_load_failed')
      expect(wrapper.getComponent(KnowledgeDocumentImages).vm).toBe(editor)
      await (wrapper.vm as any).retryDetail()
      expect(wrapper.getComponent(KnowledgeDocumentImages).vm).toBe(editor)
      expect(wrapper.get('.document-summary').text()).toBe('Tóm tắt bảo hành')
    } finally {
      wrapper.unmount()
      vi.useRealTimers()
    }
  })
})

describe('Knowledge document images', () => {
  it('keeps the AI caption as a draft, saves it explicitly, and can disable and delete the image', async () => {
    const caption = 'Bảng thời gian bảo hành'
    mocks.post.mockReset().mockResolvedValueOnce({ data: { caption } })
    mocks.patch.mockReset()
      .mockResolvedValueOnce({ data: { ...image, caption } })
      .mockResolvedValueOnce({ data: { ...image, caption, enabled: false } })
    mocks.delete.mockReset().mockResolvedValueOnce({ status: 204 })
    const wrapper = shallowMount(KnowledgeDocumentImages, {
      props: { base, documentId: doc.id, images: [image], drafts: [], canEdit: true },
      global: { stubs: { VTextField: LinkField }, renderStubDefaultSlot: true },
    })
    try {
      const vm = wrapper.vm as any
      await vm.describe(image)
      expect(mocks.post).toHaveBeenCalledWith(`${base}/${doc.id}/images/${image.id}/describe`, undefined, expect.objectContaining({ signal: expect.any(AbortSignal) }))
      expect(mocks.patch).not.toHaveBeenCalled()
      expect(wrapper.get('input[type="text"], input:not([type])').element).toHaveProperty('value', caption)
      await vm.patch(image, { caption })
      expect(wrapper.emitted('update:images')?.[0]).toEqual([[{ ...image, caption }]])
      await vm.patch({ ...image, caption }, { enabled: false })
      expect(mocks.patch).toHaveBeenLastCalledWith(`${base}/${doc.id}/images/${image.id}`, { enabled: false })
      await vm.removeImage(image)
      expect(mocks.delete).toHaveBeenCalledWith(`${base}/${doc.id}/images/${image.id}`)
      expect(wrapper.emitted('update:images')?.at(-1)).toEqual([[]])
    } finally {
      wrapper.unmount()
    }
  })

  it('uses protected same-tenant URLs for preview and releases the blob when invalidated', async () => {
    mocks.get.mockReset().mockResolvedValueOnce({ data: new Blob(['png'], { type: 'image/png' }) })
    const createURL = vi.spyOn(URL, 'createObjectURL')
    const revokeURL = vi.spyOn(URL, 'revokeObjectURL')
    const wrapper = shallowMount(KnowledgeImagePreview, { props: { base, image, alt: image.caption } })
    try {
      await flushPromises()
      expect(mocks.get).toHaveBeenCalledWith(image.url.slice('/api/v1'.length), expect.objectContaining({ responseType: 'blob' }))
      const blobURL = createURL.mock.results[0]?.value
      expect(wrapper.get('img').attributes('src')).toBe(blobURL)
      await wrapper.setProps({ image: { ...image, url: image.url.replace('tenant-1', 'tenant-2') } })
      expect(wrapper.text()).toBe('kb_images_preview_failed')
      expect(mocks.get).toHaveBeenCalledTimes(1)
      expect(revokeURL).toHaveBeenCalledWith(blobURL)
    } finally {
      wrapper.unmount()
      createURL.mockRestore()
      revokeURL.mockRestore()
    }
  })
})

describe('Knowledge playground images', () => {
  it('shows the image in the bot response and counts matched images by document in search', async () => {
    mocks.post.mockReset()
      .mockResolvedValueOnce({ data: { answer: 'Xem bảng bên dưới.', session_id: 'preview-1', images: [image] } })
      .mockResolvedValueOnce({ data: {
        hits: [{ document_id: doc.id, title: doc.title, content: 'Một đoạn', score: 1 },
          { document_id: 'd2', title: 'Tài liệu khác', content: 'Đoạn khác', score: 0.6 }],
        images: [image],
      } })
    const wrapper = shallowMount(KnowledgePlayground, {
      props: { base, isAdmin: true },
      global: { renderStubDefaultSlot: true },
    })
    try {
      const vm = wrapper.vm as any
      vm.question = 'Thời gian bảo hành?'
      vm.chatInput = { focus: vi.fn() }
      await vm.sendQuestion()
      await flushPromises()
      expect(wrapper.getComponent(KnowledgeImagePreview).props('image')).toEqual(image)
      expect(wrapper.get('figcaption').text()).toBe(image.caption)
      vm.mode = 'search'
      vm.searchQuery = 'bảo hành'
      await vm.runSearch()
      await flushPromises()
      const results = wrapper.findAll('.search-hit')
      expect(results[0]?.find('.hit-image-count').exists()).toBe(true)
      expect(results[1]?.find('.hit-image-count').exists()).toBe(false)
    } finally {
      wrapper.unmount()
    }
  })
})
