// @vitest-environment happy-dom
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, shallowMount } from '@vue/test-utils'
import ProductAdvisoryImportImageReviewModal from '../components/ProductAdvisoryImportImageReviewModal.vue'

const mocks = vi.hoisted(() => ({
  get: vi.fn(),
  post: vi.fn(),
}))

vi.mock('../api', () => ({ default: mocks }))
vi.mock('vue-i18n', () => ({ useI18n: () => ({ t: (key: string) => key }) }))

const groups = [
  { id: 'item-1', parent_code: 'PARENT/1', web_name: 'Mũ Một', image_count: 2, pending_image_count: 2 },
  { id: 'item-2', parent_code: 'PARENT-2', web_name: 'Mũ Hai', image_count: 1, pending_image_count: 1 },
]

const image = (id: string, sortOrder = 0) => ({
  id,
  url: `https://cdn.example.com/${id}.jpg`,
  enabled: false,
  sort_order: sortOrder,
  source: 'scraped',
  caption: '',
})

function mountReview(overrides: Record<string, unknown> = {}) {
  return shallowMount(ProductAdvisoryImportImageReviewModal, {
    props: {
      modelValue: true,
      tenantId: 'tenant 1',
      jobId: 'job/1',
      groups: groups.map(group => ({ ...group })),
      ...overrides,
    },
    global: {
      mocks: { $t: (key: string) => key },
      stubs: {
        VVirtualScroll: {
          props: ['items'],
          methods: { scrollToIndex() {} },
          template: '<div><slot v-for="(item, index) in items" :item="item" :index="index" /></div>',
        },
      },
      renderStubDefaultSlot: true,
    },
  })
}

describe('ProductAdvisoryImportImageReviewModal', () => {
  beforeEach(() => {
    mocks.get.mockReset()
    mocks.post.mockReset()
    mocks.get.mockResolvedValue({ data: { images: [] } })
    mocks.post.mockResolvedValue({ data: { approved_count: 1 } })
  })

  it('loads the first pending group and orders its imported images', async () => {
    mocks.get.mockResolvedValueOnce({ data: { images: [image('later', 2), image('first', 0)] } })
    const wrapper = mountReview()
    await flushPromises()

    expect(mocks.get).toHaveBeenCalledWith(
      '/tenants/tenant%201/product-advisory-imports/job%2F1/images',
      expect.objectContaining({ params: { item_id: 'item-1' } }),
    )
    expect((wrapper.vm as any).images.map((entry: { id: string }) => entry.id)).toEqual(['first', 'later'])
    wrapper.unmount()
  })

  it('ignores a late image response after the user switches groups', async () => {
    let resolveFirst!: (value: unknown) => void
    let resolveSecond!: (value: unknown) => void
    mocks.get
      .mockReturnValueOnce(new Promise(resolve => { resolveFirst = resolve }))
      .mockReturnValueOnce(new Promise(resolve => { resolveSecond = resolve }))
    const wrapper = mountReview()
    await flushPromises()

    ;(wrapper.vm as any).selectGroup(1)
    resolveSecond({ data: { images: [image('second-group')] } })
    await flushPromises()
    resolveFirst({ data: { images: [image('stale-first-group')] } })
    await flushPromises()

    expect((wrapper.vm as any).images.map((entry: { id: string }) => entry.id)).toEqual(['second-group'])
    expect(mocks.get).toHaveBeenLastCalledWith(
      '/tenants/tenant%201/product-advisory-imports/job%2F1/images',
      expect.objectContaining({ params: { item_id: 'item-2' } }),
    )
    wrapper.unmount()
  })

  it('approves one imported image without mutating the groups prop', async () => {
    const sourceGroups = groups.map(group => ({ ...group }))
    const target = image('approve-me')
    mocks.get.mockResolvedValueOnce({ data: { images: [target] } })
    const wrapper = mountReview({ groups: sourceGroups })
    await flushPromises()

    await (wrapper.vm as any).approveImage((wrapper.vm as any).images[0])

    expect(mocks.post).toHaveBeenCalledWith(
      '/tenants/tenant%201/product-advisory-imports/job%2F1/images/approve',
      { image_id: 'approve-me' },
    )
    expect((wrapper.vm as any).images[0].enabled).toBe(true)
    expect(sourceGroups[0]!.pending_image_count).toBe(2)
    expect(wrapper.emitted('updated')).toHaveLength(1)
    wrapper.unmount()
  })

  it('approves every eligible image in the import through the job-scoped endpoint', async () => {
    mocks.get.mockResolvedValueOnce({ data: { images: [image('visible')] } })
    mocks.post.mockResolvedValueOnce({ data: { approved_count: 3 } })
    const wrapper = mountReview()
    await flushPromises()

    await (wrapper.vm as any).approveAll()

    expect(mocks.post).toHaveBeenCalledWith(
      '/tenants/tenant%201/product-advisory-imports/job%2F1/images/approve',
      { all: true },
    )
    expect((wrapper.vm as any).images[0].enabled).toBe(true)
    expect(wrapper.emitted('updated')).toHaveLength(1)
    wrapper.unmount()
  })

  it('reloads with the new tenant and ignores the old tenant response', async () => {
    let resolveOld!: (value: unknown) => void
    mocks.get
      .mockReturnValueOnce(new Promise(resolve => { resolveOld = resolve }))
      .mockResolvedValueOnce({ data: { images: [image('new-tenant')] } })
    const wrapper = mountReview()
    await flushPromises()

    await wrapper.setProps({ tenantId: 'tenant-2' })
    await flushPromises()
    resolveOld({ data: { images: [image('old-tenant')] } })
    await flushPromises()

    expect((wrapper.vm as any).images.map((entry: { id: string }) => entry.id)).toEqual(['new-tenant'])
    expect(mocks.get).toHaveBeenLastCalledWith(
      '/tenants/tenant-2/product-advisory-imports/job%2F1/images',
      expect.objectContaining({ params: { item_id: 'item-1' } }),
    )
    wrapper.unmount()
  })
})
