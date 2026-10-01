// @vitest-environment happy-dom
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { defineComponent, h, reactive } from 'vue'
import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import { createI18n } from 'vue-i18n'
import ProductGroupingJob from '../components/ProductGroupingJob.vue'
import JobList from '../views/Jobs/JobList.vue'
import messages from '../i18n/vi'

const mocks = vi.hoisted(() => ({ auth: { tenantPerms: { role: 'admin', permissions: { jobs: 'rwd' } }, canEdit: vi.fn(() => true) }, jobs: { jobs: [] as any[], fetchJobs: vi.fn(), deleteJob: vi.fn() }, post: vi.fn(), put: vi.fn(), route: { params: { tenantId: 'tenant-a' } } }))
vi.mock('../stores/auth', () => ({ useAuthStore: () => mocks.auth }))
vi.mock('../stores/jobs', () => ({ useJobStore: () => mocks.jobs }))
vi.mock('../api', () => ({ default: { post: mocks.post, put: mocks.put } }))
vi.mock('vue-router', () => ({ useRoute: () => mocks.route }))
const Container = defineComponent({ setup: (_, { slots, attrs }) => () => h('div', attrs, slots.default?.()) })
const Button = defineComponent({ props: ['disabled', 'icon', 'to'], setup: (props, { slots, attrs }) => () => h('button', { ...attrs, disabled: props.disabled, 'data-icon': props.icon, 'data-to': props.to }, slots.default?.()) })
const Textarea = defineComponent({ props: ['modelValue', 'readonly', 'disabled'], emits: ['update:modelValue'], setup: (props, { emit }) => () => h('textarea', { value: props.modelValue, readonly: props.readonly, disabled: props.disabled, onInput: (event: Event) => emit('update:modelValue', (event.target as HTMLTextAreaElement).value) }) })
const wrappers: VueWrapper[] = []
const job = { id: 'group-job', tenant_id: 'tenant-a', job_type: 'messenger_product_groups', name: 'Tổng hợp sản phẩm CSKH', system_prompt: 'Prompt thật đang dùng: EGO, LS2, BULLDOG, YOHE, ZEUS; FF818; E-24' }
function render(component: any, props = {}) {
  const wrapper = mount(component, { props, global: { plugins: [createI18n({ legacy: false, locale: 'vi', messages: { vi: messages } })], stubs: { VBtn: Button, VCard: Container, VCardText: Container, VAlert: Container, VTabs: Container, VTab: Container, VDivider: Container, VTable: Container, VChip: Container, VIcon: Container, VTextarea: Textarea, RouterLink: Container } } })
  wrappers.push(wrapper)
  return wrapper
}
beforeEach(() => {
  mocks.auth.tenantPerms.role = 'admin'
  mocks.auth.canEdit.mockReturnValue(true)
  mocks.jobs.jobs = reactive([])
  mocks.jobs.fetchJobs.mockReset().mockResolvedValue(undefined)
  mocks.jobs.deleteJob.mockReset().mockResolvedValue(undefined)
  mocks.post.mockReset().mockResolvedValue({ data: job })
  mocks.put.mockReset().mockImplementation(async (_url, payload) => ({ data: { ...job, ...payload } }))
  vi.stubGlobal('confirm', vi.fn(() => true))
})
afterEach(() => { wrappers.splice(0).forEach(wrapper => wrapper.unmount()); vi.unstubAllGlobals() })

describe('fixed product aggregation AI task', () => {
  it('renders the actual prompt without a wizard and saves the edited prompt directly', async () => {
    const wrapper = render(ProductGroupingJob, { job, tenantId: 'tenant-a' })
    expect(wrapper.text()).toContain('Prompt hệ thống')
    expect(wrapper.get('textarea').element.value).toBe(job.system_prompt)
    await wrapper.get('textarea').setValue('Prompt mới: giữ groups name members')
    await wrapper.findAll('button').find(button => button.text() === 'Lưu prompt')!.trigger('click')
    await flushPromises()
    expect(mocks.put).toHaveBeenCalledWith('/tenants/tenant-a/jobs/group-job/product-grouping-prompt', { system_prompt: 'Prompt mới: giữ groups name members' })
    expect(wrapper.emitted('updated')?.[0]?.[0]).toMatchObject({ system_prompt: 'Prompt mới: giữ groups name members' })
    expect(wrapper.text()).toContain('Đã lưu prompt hệ thống')
  })
  it('lets a member view the prompt read-only even with jobs write/delete permission', () => {
    mocks.auth.tenantPerms.role = 'member'
    const wrapper = render(ProductGroupingJob, { job, tenantId: 'tenant-a' })
    expect(wrapper.get('textarea').attributes('readonly')).toBeDefined()
    expect(wrapper.text()).not.toContain('Lưu prompt')
    expect(mocks.put).not.toHaveBeenCalled()
  })
  it('keeps edits available if saving fails', async () => {
    mocks.put.mockRejectedValueOnce(new Error('offline'))
    const wrapper = render(ProductGroupingJob, { job, tenantId: 'tenant-a' })
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
