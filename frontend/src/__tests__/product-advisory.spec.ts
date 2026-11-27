// @vitest-environment happy-dom
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, shallowMount } from '@vue/test-utils'
import ProductAdvisory from '../components/ProductAdvisory.vue'
import ProductAdvisoryLinkImportModal from '../components/ProductAdvisoryLinkImportModal.vue'
import ProductAdvisoryImportImageReviewModal from '../components/ProductAdvisoryImportImageReviewModal.vue'

const mocks = vi.hoisted(() => ({
  get: vi.fn(),
  importGet: vi.fn(),
  put: vi.fn(),
  post: vi.fn(),
  patch: vi.fn(),
  delete: vi.fn(),
}))
vi.mock('../api', () => ({
  default: {
    ...mocks,
    get: (url: string, ...args: unknown[]) => (
      url.includes('/product-advisory-imports/') || url.endsWith('/knowledge/firecrawl')
        ? mocks.importGet(url, ...args)
        : mocks.get(url, ...args)
    ),
  },
}))
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
    mocks.importGet.mockResolvedValue({ data: { job: null, items: [] } })
    mocks.put.mockResolvedValue({ data: detail })
    mocks.post.mockResolvedValue({ data: detail })
    mocks.patch.mockResolvedValue({ data: {} })
    mocks.delete.mockResolvedValue({ data: {} })
  })

  it('reopens the import, polls to completion, and shows review for the imported product group', async () => {
    vi.useFakeTimers()
    const job = {
      id: 'job-1', file_name: 'links.csv', status: 'running', total_rows: 1, matched_count: 1,
      skipped_count: 0, unmatched_count: 0, failed_count: 0, image_count: 0, pending_image_count: 0,
      succeeded_count: 0, created_at: '',
    }
    const completed = { ...job, status: 'completed', succeeded_count: 1, image_count: 2, pending_image_count: 2 }
    const item = {
      id: 'item-1', status: 'succeeded', parent_code: product.parent_code, web_name: product.web_name,
      image_count: 2, pending_image_count: 2,
    }
    mocks.importGet.mockImplementation(async (url: string) => {
      if (url.endsWith('/knowledge/firecrawl')) return { data: { enabled: true } }
      if (url.endsWith('/latest')) return { data: { job, items: [] } }
      return { data: { job: completed, items: [item] } }
    })
    const wrapper = shallowMount(ProductAdvisory, {
      props: { tenantId: 'tenant-1', canEdit: true },
      global: { mocks: { $t: (key: string) => key }, renderStubDefaultSlot: true },
    })
    try {
      await vi.advanceTimersByTimeAsync(0)
      expect(wrapper.find('[data-testid="product-import-running"]').exists()).toBe(true)
      await wrapper.get('[data-testid="product-import-open"]').trigger('click')
      await vi.advanceTimersByTimeAsync(0)
      expect(wrapper.getComponent(ProductAdvisoryLinkImportModal).props('firecrawlEnabled')).toBe(true)

      await vi.advanceTimersByTimeAsync(2000)
      const review = wrapper.getComponent(ProductAdvisoryImportImageReviewModal)
      expect(review.props('modelValue')).toBe(true)
      expect(review.props('jobId')).toBe(job.id)
      expect(review.props('groups')).toEqual([{
        id: item.id, parent_code: product.parent_code, web_name: product.web_name, image_count: 2, pending_image_count: 2,
      }])
      expect(mocks.get).toHaveBeenCalledTimes(2)
    } finally {
      wrapper.unmount()
      vi.useRealTimers()
    }
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

  it.each([product.thumbnail_url, ''])('renders the editor thumbnail or placeholder for %j', async (imageURL) => {
    mocks.get.mockResolvedValueOnce({ data: { items: [product], total: 1 } }).mockResolvedValueOnce({
      data: { ...detail, default_image_url: imageURL },
    })
    const wrapper = shallowMount(ProductAdvisory, {
      props: { tenantId: 'tenant-1', canEdit: true },
      global: { mocks: { $t: (key: string) => key }, renderStubDefaultSlot: true },
    })
    await flushPromises()
    await (wrapper.vm as any).openProduct(product)
    await flushPromises()

    const header = wrapper.get('v-card-title')
    expect(header.get('h3').text()).toBe(product.web_name)
    if (imageURL) {
      expect(header.get('v-img').attributes('src')).toBe(imageURL)
    } else {
      expect(header.find('v-img').exists()).toBe(false)
      expect(header.get('v-icon').attributes('icon')).toBe('mdi-image-outline')
    }
    wrapper.unmount()
  })

  it('opens the file picker from the upload button and posts the chosen images', async () => {
    mocks.get
      .mockResolvedValueOnce({ data: { items: [product], total: 1 } })
      .mockResolvedValueOnce({ data: detail })
    const wrapper = shallowMount(ProductAdvisory, {
      props: { tenantId: 'tenant-1', canEdit: true },
      global: {
        mocks: { $t: (key: string) => key },
        stubs: { VBtn: { template: '<button type="button"><slot /></button>' } },
        renderStubDefaultSlot: true,
      },
    })
    await flushPromises()
    await (wrapper.vm as any).openProduct(product)
    await flushPromises()

    const input = wrapper.get<HTMLInputElement>('input[type="file"]')
    const openPicker = vi.spyOn(input.element, 'click').mockImplementation(() => {})
    const button = wrapper.findAll('button').find((item) => item.text() === 'product_advisory_add_images')
    expect(button).toBeDefined()
    await button!.trigger('click')
    expect(openPicker).toHaveBeenCalledOnce()

    const file = new File(['image'], 'storm.png', { type: 'image/png' })
    Object.defineProperty(input.element, 'files', { value: [file] })
    await input.trigger('change')
    await flushPromises()

    expect(mocks.post).toHaveBeenCalledOnce()
    const [url, body] = mocks.post.mock.calls[0]!
    expect(url).toBe('/tenants/tenant-1/product-advisory/LS2-FF800/images')
    expect(body.getAll('images')).toEqual([file])
    wrapper.unmount()
  })

  it('renders the AI summary and image badges after reading a product page', async () => {
    mocks.get
      .mockResolvedValueOnce({ data: { items: [product], total: 1 } })
      .mockResolvedValueOnce({ data: detail })
    const result = {
      ...detail,
      source_url: 'https://example.com/storm',
      source_title: 'Storm II - Cửa hàng',
      scraped_content: '[Skip to content](https://example.com/storm#main)',
      source_summary: 'Mũ fullface phù hợp đi phố.\n\n• Kính chống UV.',
      images: [{ id: 'storm', url: product.thumbnail_url, source: 'scraped', caption: '', enabled: false, sort_order: 0 }],
    }
    let finishScrape!: (response: { data: typeof result }) => void
    mocks.post.mockReturnValueOnce(new Promise((resolve) => { finishScrape = resolve }))
    const wrapper = shallowMount(ProductAdvisory, {
      props: { tenantId: 'tenant-1', canEdit: true },
      global: { mocks: { $t: (key: string) => key }, renderStubDefaultSlot: true },
    })
    await flushPromises()
    await (wrapper.vm as any).openProduct(product)
    ;(wrapper.vm as any).form.source_url = 'https://example.com/storm'
    const scrape = (wrapper.vm as any).scrape()
    await flushPromises()
    expect(wrapper.text()).toContain('product_advisory_summary_reading')
    await (wrapper.vm as any).save()
    expect(mocks.put).not.toHaveBeenCalled()

    finishScrape({ data: result })
    await scrape
    await flushPromises()
    expect(wrapper.get('.source-summary').element.textContent).toBe(result.source_summary)
    expect(wrapper.text()).toContain(result.source_title)
    expect(wrapper.text()).not.toContain('Skip to content')
    expect(wrapper.get('.image-card').findAll('v-chip').map((chip) => chip.text())).toEqual([
      'product_advisory_scraped',
      'product_advisory_pending_review',
    ])

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
