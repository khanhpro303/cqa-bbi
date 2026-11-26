// @vitest-environment happy-dom
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, shallowMount } from '@vue/test-utils'
import ProductAdvisory from '../components/ProductAdvisory.vue'

const mocks = vi.hoisted(() => ({
  get: vi.fn(),
  put: vi.fn(),
  post: vi.fn(),
  patch: vi.fn(),
  delete: vi.fn(),
}))
vi.mock('../api', () => ({ default: mocks }))
vi.mock('vue-i18n', () => ({ useI18n: () => ({ t: (key: string) => key }) }))

const product = {
  parent_code: 'LS2-FF800',
  web_name: 'Storm II',
  brand: 'LS2',
  variant_count: 12,
  has_content: false,
  thumbnail_url: 'https://cdn.example.com/storm.jpg',
}
const detail = {
  ...product,
  notes: '',
  source_url: '',
  source_title: '',
  scraped_content: '',
  images: [],
}

describe('ProductAdvisory', () => {
  beforeEach(() => {
    Object.values(mocks).forEach((mock) => mock.mockReset())
    mocks.get.mockResolvedValue({ data: { items: [product], total: 1 } })
    mocks.put.mockResolvedValue({ data: detail })
    mocks.post.mockResolvedValue({ data: detail })
    mocks.patch.mockResolvedValue({ data: {} })
    mocks.delete.mockResolvedValue({ data: {} })
  })

  it('loads parent product groups from the tenant API', async () => {
    const wrapper = shallowMount(ProductAdvisory, {
      props: { tenantId: 'tenant-1', canEdit: true },
      global: { mocks: { $t: (key: string) => key }, renderStubDefaultSlot: true },
    })
    await flushPromises()

    expect(mocks.get).toHaveBeenCalledWith('/tenants/tenant-1/product-advisory', {
      params: { page: 1, per_page: 20, q: undefined, configured: 'all' },
    })
    expect((wrapper.vm as any).products).toEqual([product])
    wrapper.unmount()
  })

  it('loads and saves advisory notes for one parent code', async () => {
    mocks.get
      .mockResolvedValueOnce({ data: { items: [product], total: 1 } })
      .mockResolvedValueOnce({ data: detail })
    const wrapper = shallowMount(ProductAdvisory, {
      props: { tenantId: 'tenant-1', canEdit: true },
      global: { mocks: { $t: (key: string) => key }, renderStubDefaultSlot: true },
    })
    await flushPromises()

    await (wrapper.vm as any).openProduct(product)
    ;(wrapper.vm as any).form.notes = 'Êm, phù hợp đi phố.'
    await (wrapper.vm as any).save()
    await flushPromises()

    expect(mocks.get).toHaveBeenCalledWith('/tenants/tenant-1/product-advisory/LS2-FF800')
    expect(mocks.put).toHaveBeenCalledWith('/tenants/tenant-1/product-advisory/LS2-FF800', {
      notes: 'Êm, phù hợp đi phố.',
      source_url: '',
    })
    wrapper.unmount()
  })

  it('scrapes a public product page with the configured Firecrawl flow', async () => {
    mocks.get
      .mockResolvedValueOnce({ data: { items: [product], total: 1 } })
      .mockResolvedValueOnce({ data: detail })
    mocks.post.mockResolvedValueOnce({
      data: { ...detail, source_url: 'https://example.com/storm', scraped_content: 'Mũ fullface đi phố.' },
    })
    const wrapper = shallowMount(ProductAdvisory, {
      props: { tenantId: 'tenant-1', canEdit: true },
      global: { mocks: { $t: (key: string) => key }, renderStubDefaultSlot: true },
    })
    await flushPromises()
    await (wrapper.vm as any).openProduct(product)
    ;(wrapper.vm as any).form.source_url = 'https://example.com/storm'
    await (wrapper.vm as any).scrape()
    await flushPromises()

    expect(mocks.post).toHaveBeenCalledWith('/tenants/tenant-1/product-advisory/LS2-FF800/scrape', {
      url: 'https://example.com/storm',
    })
    wrapper.unmount()
  })

  it('loads protected uploaded images through the authenticated API client', async () => {
    const protectedURL = '/api/v1/tenants/tenant-1/product-advisory/LS2-FF800/images/image-1/content'
    mocks.get
      .mockResolvedValueOnce({ data: { items: [product], total: 1 } })
      .mockResolvedValueOnce({
        data: {
          ...detail,
          images: [{ id: 'image-1', url: protectedURL, source: 'upload', caption: 'Mặt trước', enabled: true, sort_order: 0 }],
        },
      })
      .mockResolvedValueOnce({ data: new Blob(['image'], { type: 'image/png' }) })
    const wrapper = shallowMount(ProductAdvisory, {
      props: { tenantId: 'tenant-1', canEdit: true },
      global: { mocks: { $t: (key: string) => key }, renderStubDefaultSlot: true },
    })
    await flushPromises()
    await (wrapper.vm as any).openProduct(product)
    await flushPromises()

    expect(mocks.get).toHaveBeenCalledWith(
      '/tenants/tenant-1/product-advisory/LS2-FF800/images/image-1/content',
      { responseType: 'blob' },
    )
    wrapper.unmount()
  })
})
