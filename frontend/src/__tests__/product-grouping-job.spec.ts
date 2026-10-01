// @vitest-environment happy-dom
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { defineComponent, h, reactive } from 'vue'
import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import { createI18n } from 'vue-i18n'
import ProductGroupingJob from '../components/ProductGroupingJob.vue'
import JobList from '../views/Jobs/JobList.vue'
import messages from '../i18n/vi'

const mocks = vi.hoisted(() => ({ auth: { tenantPerms: { role: 'admin', permissions: { jobs: 'rwd' } }, canEdit: vi.fn(() => true), canView: vi.fn((_resource: string) => true) }, jobs: { jobs: [] as any[], fetchJobs: vi.fn(), deleteJob: vi.fn() }, get: vi.fn(), post: vi.fn(), put: vi.fn(), route: { params: { tenantId: 'tenant-a' } } }))
vi.mock('../stores/auth', () => ({ useAuthStore: () => mocks.auth }))
vi.mock('../stores/jobs', () => ({ useJobStore: () => mocks.jobs }))
vi.mock('../api', () => ({ default: { get: mocks.get, post: mocks.post, put: mocks.put } }))
vi.mock('vue-router', () => ({ useRoute: () => mocks.route }))
const Container = defineComponent({ setup: (_, { slots, attrs }) => () => h('div', attrs, slots.default?.()) })
const Button = defineComponent({ props: ['disabled', 'icon', 'to'], setup: (props, { slots, attrs }) => () => h('button', { ...attrs, disabled: props.disabled, 'data-icon': props.icon, 'data-to': props.to }, slots.default?.()) })
const Textarea = defineComponent({ props: ['modelValue', 'readonly', 'disabled'], emits: ['update:modelValue'], setup: (props, { emit }) => () => h('textarea', { value: props.modelValue, readonly: props.readonly, disabled: props.disabled, onInput: (event: Event) => emit('update:modelValue', (event.target as HTMLTextAreaElement).value) }) })
const Field = defineComponent({ props: ['modelValue', 'label', 'type'], emits: ['update:modelValue'], setup: (props, { emit }) => () => h('input', { value: props.modelValue, 'aria-label': props.label, type: props.type || 'text', onInput: (event: Event) => emit('update:modelValue', (event.target as HTMLInputElement).value) }) })
const Dialog = defineComponent({ props: ['modelValue'], setup: (props, { slots }) => () => props.modelValue ? h('section', { role: 'dialog' }, slots.default?.()) : null })
const Tabs = defineComponent({ emits: ['update:modelValue'], setup: (_, { slots, emit }) => () => h('div', { onClick: (event: Event) => { const value = (event.target as HTMLElement).getAttribute('value'); if (value) emit('update:modelValue', value) } }, slots.default?.()) })
const wrappers: VueWrapper[] = []
const job = { id: 'group-job', tenant_id: 'tenant-a', job_type: 'messenger_product_groups', name: 'Tổng hợp sản phẩm CSKH', is_active: true, created_at: '2026-09-17T02:30:00Z', system_prompt: 'Prompt thật đang dùng: EGO, LS2, BULLDOG, YOHE, ZEUS; FF818; E-24' }
function render(component: any, props = {}) {
  const wrapper = mount(component, { props, global: { plugins: [createI18n({ legacy: false, locale: 'vi', messages: { vi: messages } })], stubs: { VBtn: Button, VCard: Container, VCardText: Container, VAlert: Container, VTabs: Tabs, VTab: Button, VDivider: Container, VTable: Container, VChip: Container, VIcon: Container, VTextarea: Textarea, VTextField: Field, VSelect: Field, VDialog: Dialog, VSnackbar: Dialog, VCardTitle: Container, VCardActions: Container, VProgressCircular: Container, VSpacer: Container, VSwitch: Field, VPagination: Field, RouterLink: Container } } })
  wrappers.push(wrapper)
  return wrapper
}
beforeEach(() => {
  mocks.auth.tenantPerms.role = 'admin'
  mocks.auth.canEdit.mockReturnValue(true)
  mocks.auth.canView.mockReturnValue(true)
  mocks.get.mockReset().mockImplementation(async (url: string) => ({ data: url.endsWith('/runs') ? history : url.endsWith('/settings') ? { settings: { ai_provider: 'openai', ai_model: 'gpt-5-mini' } } : structuredClone(results) }))
  mocks.jobs.jobs = reactive([])
  mocks.jobs.fetchJobs.mockReset().mockResolvedValue(undefined)
  mocks.jobs.deleteJob.mockReset().mockResolvedValue(undefined)
  mocks.post.mockReset().mockResolvedValue({ data: job })
  mocks.put.mockReset().mockImplementation(async (_url, payload) => ({ data: { ...job, ...payload } }))
  vi.stubGlobal('confirm', vi.fn(() => true))
})
afterEach(() => { wrappers.splice(0).forEach(wrapper => wrapper.unmount()); vi.unstubAllGlobals() })

describe('fixed product aggregation AI task', () => {
  it('hides the system prompt until Edit is opened and saves it directly', async () => {
    const wrapper = render(ProductGroupingJob, { job, tenantId: 'tenant-a' })
    await flushPromises()
    expect(wrapper.find('textarea').exists()).toBe(false)
    expect(wrapper.text()).not.toContain('Prompt hệ thống')
    await button(wrapper, 'Sửa').trigger('click')
    expect(wrapper.get('textarea').element.value).toBe(job.system_prompt)
    await wrapper.get('textarea').setValue('Prompt mới: giữ groups name members')
    await wrapper.findAll('button').find(button => button.text() === 'Lưu prompt')!.trigger('click')
    await flushPromises()
    expect(mocks.put).toHaveBeenCalledWith('/tenants/tenant-a/jobs/group-job/product-grouping-prompt', { system_prompt: 'Prompt mới: giữ groups name members' })
    expect(wrapper.emitted('updated')?.[0]?.[0]).toMatchObject({ system_prompt: 'Prompt mới: giữ groups name members' })
    expect(wrapper.text()).toContain('Đã lưu prompt hệ thống')
  })
  it('lets a member view the prompt read-only inside Edit', async () => {
    mocks.auth.tenantPerms.role = 'member'
    const wrapper = render(ProductGroupingJob, { job, tenantId: 'tenant-a' })
    await button(wrapper, 'Sửa').trigger('click')
    expect(wrapper.get('textarea').attributes('readonly')).toBeDefined()
    expect(wrapper.text()).not.toContain('Lưu prompt')
    expect(mocks.put).not.toHaveBeenCalled()
  })
  it('keeps edits available if saving fails', async () => {
    mocks.put.mockRejectedValueOnce(new Error('offline'))
    const wrapper = render(ProductGroupingJob, { job, tenantId: 'tenant-a' })
    await button(wrapper, 'Sửa').trigger('click')
    await wrapper.get('textarea').setValue('Chỉnh sửa chưa lưu')
    await wrapper.findAll('button').find(button => button.text() === 'Lưu prompt')!.trigger('click')
    await flushPromises()
    expect(wrapper.text()).toContain('Không thể lưu prompt')
    expect(wrapper.get('textarea').element.value).toBe('Chỉnh sửa chưa lưu')
  })
  it('creates directly without a configuration payload and reloads the list', async () => {
    const wrapper = render(JobList)
    await flushPromises()
    await wrapper.findAll('button').find(button => button.text() === 'Thêm tác vụ tổng hợp sản phẩm')!.trigger('click')
    await flushPromises()
    expect(mocks.post).toHaveBeenCalledWith('/tenants/tenant-a/jobs/product-grouping')
    expect(mocks.jobs.fetchJobs).toHaveBeenCalledTimes(2)
  })
  it('hides duplicate creation and generic editing, and allows admin deletion', async () => {
    mocks.jobs.jobs.push(job)
    const wrapper = render(JobList)
    await flushPromises()
    expect(wrapper.text()).not.toContain('Thêm tác vụ tổng hợp sản phẩm')
    expect(wrapper.find('button[data-icon="mdi-pencil"]').exists()).toBe(false)
    await wrapper.get('button[data-icon="mdi-delete"]').trigger('click')
    await flushPromises()
    expect(mocks.jobs.deleteJob).toHaveBeenCalledWith('tenant-a', 'group-job')
  })
  it('hides special task creation/deletion from a member with jobs write/delete permission', async () => {
    mocks.auth.tenantPerms.role = 'member'
    mocks.jobs.jobs.push(job)
    const wrapper = render(JobList)
    await flushPromises()
    expect(wrapper.text()).not.toContain('Thêm tác vụ tổng hợp sản phẩm')
    expect(wrapper.find('button[data-icon="mdi-delete"]').exists()).toBe(false)
  })
})

function button(wrapper: VueWrapper, label: string) { return wrapper.findAll('button').find(item => item.text() === label)! }
const results = {
  status: 'ready', grouping_ready: true, excluded_product_names: ['LS2'],
  report: { from: '2026-09-25T00:00:00+07:00', to: '2026-10-02T00:00:00+07:00', generated_at: '2026-10-01T00:00:00+07:00', pages: [{ id: 'page-a', name: 'Fanpage A' }] },
  product_names: ['LS2 FF 818', 'Mũ FF818', 'LS2'],
  groups: [{ name: 'FF818', members: ['LS2 FF 818', 'Mũ FF818'], count: 1, sources: [{
    conversation_id: 'conv-a', customer_name: 'Lâm Khải', channel_id: 'page-a', channel_name: 'Fanpage A', insight_at: '2026-10-01T03:00:00Z',
    summary: 'Khách hỏi giá mũ bảo hiểm.', matched_products: [{ name: 'LS2 FF 818', evidence: 'FF 818 còn màu đen không?' }, { name: 'Mũ FF818', evidence: 'Mũ FF818 giá bao nhiêu?' }],
    lead_quality: { level: 'high', reason: 'Khách xác nhận nhu cầu mua.', evidence: 'Cho mình đặt một chiếc.' },
    quality_analysis: { job_name: 'Tác vụ chất lượng CSKH', evaluated_at: '2026-10-01T03:02:00Z', verdict: 'FAIL', score: 65, review: 'Chưa trả lời đầy đủ.', violations: [{ rule_name: 'Thiếu báo giá', explanation: 'Chưa cung cấp giá.', evidence: 'Bạn chờ mình nhé.' }] }, quality_analysis_stale: true,
  }] }],
}
const history = [{ id: 'run-error', status: 'error', started_at: '2026-10-01T03:00:00Z', finished_at: '2026-10-01T03:00:05Z', error_message: 'AI trả về ánh xạ sản phẩm không hợp lệ.', summary: '{}' }, { id: 'run-ok', status: 'success', started_at: '2026-09-30T03:00:00Z', finished_at: '2026-09-30T03:00:02Z', error_message: '', summary: '{}' }]

describe('product aggregation monitoring and provenance', () => {
  it('shows metadata, unique counts and keyword-to-conversation assessments', async () => {
    const wrapper = render(ProductGroupingJob, { job, tenantId: 'tenant-a' })
    await flushPromises()
    expect(wrapper.text()).toContain('Thông tin công việc')
    expect(wrapper.text()).toContain('openai / gpt-5-mini')
    expect(wrapper.get('.product-row').text()).toContain('FF818')
    expect(wrapper.get('.product-row').findAll('td')[1]!.text()).toBe('1')
    expect(wrapper.text()).toContain('Keyword được AI bỏ qua')
    await button(wrapper, 'Chi tiết').trigger('click')
    const detail = wrapper.get('.source-panel')
    for (const text of ['Lâm Khải', 'FF 818 còn màu đen không?', 'Cho mình đặt một chiếc.', 'Không đạt', '65/100', 'Thiếu báo giá', 'Cần đánh giá lại', 'Tác vụ chất lượng CSKH']) expect(detail.text()).toContain(text)
    expect(mocks.post).not.toHaveBeenCalled()
  })
  it('renders actual run errors and filters the history to errors', async () => {
    const wrapper = render(ProductGroupingJob, { job, tenantId: 'tenant-a' })
    await flushPromises()
    expect(wrapper.get('[data-testid="latest-run-error"]').text()).toContain(history[0]!.error_message)
    await button(wrapper, 'Xem lỗi trong lịch sử').trigger('click')
    expect(wrapper.get('.history-table').text()).toContain('run-error')
    expect(wrapper.get('.history-table').text()).not.toContain('run-ok')
    expect(wrapper.get('.history-table').text()).toContain('5 giây')
  })
  it('distinguishes pending results and runs with the current source snapshot', async () => {
    mocks.get.mockImplementation(async (url: string) => ({ data: url.endsWith('/runs') ? [] : url.endsWith('/settings') ? {} : { ...results, status: 'pending', grouping_ready: false, groups: [] } }))
    mocks.post.mockResolvedValue({ data: { enabled: true, groups: results.groups } })
    const wrapper = render(ProductGroupingJob, { job, tenantId: 'tenant-a' })
    await flushPromises()
    expect(wrapper.text()).toContain('Chưa có kết quả tổng hợp cho bộ lọc này')
    await button(wrapper, 'Chạy ngay').trigger('click')
    await flushPromises()
    expect(mocks.post).toHaveBeenCalledWith('/tenants/tenant-a/service-quality/product-groups', expect.objectContaining({ product_names: results.product_names, force: true }))
    expect(mocks.get.mock.calls.filter(([url]) => url.endsWith('product-grouping-results'))).toHaveLength(2)
  })
  it('requires applying changed date filters before running a loaded snapshot', async () => {
    const wrapper = render(ProductGroupingJob, { job, tenantId: 'tenant-a' })
    await flushPromises()
    await wrapper.get('input[aria-label="Từ ngày"]').setValue('2026-09-01')
    expect(button(wrapper, 'Chạy ngay').attributes('disabled')).toBeDefined()
    expect(wrapper.text()).toContain('Bộ lọc đã thay đổi')
    await button(wrapper, 'Áp dụng').trigger('click')
    await flushPromises()
    expect(mocks.get).toHaveBeenCalledWith('/tenants/tenant-a/jobs/group-job/product-grouping-results', expect.objectContaining({ params: expect.objectContaining({ from: '2026-09-01' }) }))
    expect(button(wrapper, 'Chạy ngay').attributes('disabled')).toBeUndefined()
  })
  it('shows load failures instead of claiming results are empty', async () => {
    mocks.get.mockRejectedValue(new Error('offline'))
    const wrapper = render(ProductGroupingJob, { job, tenantId: 'tenant-a' })
    await flushPromises()
    expect(wrapper.text()).toContain('Không thể tải kết quả tổng hợp sản phẩm')
    expect(wrapper.text()).not.toContain('Chưa có dữ liệu sản phẩm trong kỳ')
  })
  it('does not request conversation results without messages read permission', async () => {
    mocks.auth.canView.mockImplementation(resource => resource !== 'messages')
    const wrapper = render(ProductGroupingJob, { job, tenantId: 'tenant-a' })
    await flushPromises()
    expect(mocks.get.mock.calls.some(([url]) => url.endsWith('product-grouping-results'))).toBe(false)
    expect(wrapper.text()).toContain('Bạn cần quyền đọc tin nhắn')
    expect(button(wrapper, 'Chạy ngay')).toBeUndefined()
    await button(wrapper, 'Lịch sử chạy').trigger('click')
    expect(wrapper.get('.history-table').text()).toContain('run-error')
  })
  it('reports unavailable persisted results instead of a false run success', async () => {
    mocks.get.mockImplementation(async (url: string) => ({ data: url.endsWith('/runs') ? [] : url.endsWith('/settings') ? {} : { ...results, status: 'pending', grouping_ready: false, groups: [] } }))
    mocks.post.mockResolvedValue({ data: { enabled: true } })
    const wrapper = render(ProductGroupingJob, { job, tenantId: 'tenant-a' })
    await flushPromises()
    await button(wrapper, 'Chạy ngay').trigger('click')
    await flushPromises()
    expect(wrapper.text()).toContain('AI đã xử lý nhưng chưa tải được kết quả tổng hợp')
    expect(wrapper.text()).not.toContain('Đã tổng hợp sản phẩm.')
  })
  it('renders omitted keyword provenance while excluding it from product totals', async () => {
    const omitted = { name: '', members: ['LS2'], count: 1, sources: [{ ...results.groups[0]!.sources[0]!, matched_products: [{ name: 'LS2', evidence: 'Shop có mũ LS2 không?' }] }] }
    mocks.get.mockImplementation(async (url: string) => ({ data: url.endsWith('/runs') ? [] : url.endsWith('/settings') ? {} : { ...results, groups: [omitted] } }))
    const wrapper = render(ProductGroupingJob, { job, tenantId: 'tenant-a' })
    await flushPromises()
    expect(wrapper.get('.scorecards').text()).toContain('Sản phẩm đã tổng hợp0')
    expect(wrapper.get('.product-row').text()).toContain('Bỏ qua')
    await button(wrapper, 'Chi tiết').trigger('click')
    expect(wrapper.get('.source-panel').text()).toContain('Shop có mũ LS2 không?')
  })
  it('discards stale results after switching tenant and task', async () => {
    let resolveOld!: (value: any) => void
    mocks.get.mockImplementation((url: string) => url.includes('tenant-a') && url.endsWith('product-grouping-results') ? new Promise(resolve => { resolveOld = resolve }) : Promise.resolve({ data: url.endsWith('/runs') ? [] : url.endsWith('/settings') ? {} : { ...results, groups: [], product_names: [], excluded_product_names: [] } }))
    const wrapper = render(ProductGroupingJob, { job, tenantId: 'tenant-a' })
    await wrapper.setProps({ tenantId: 'tenant-b', job: { ...job, id: 'new-job' } })
    await flushPromises()
    resolveOld({ data: results })
    await flushPromises()
    expect(wrapper.find('.product-row').exists()).toBe(false)
    expect(wrapper.text()).toContain('Chưa có dữ liệu sản phẩm trong kỳ')
  })
})
