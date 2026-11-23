// @vitest-environment happy-dom
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, shallowMount } from '@vue/test-utils'
import CasesPanel from '../components/chat-memory/CasesPanel.vue'
import CaseDialog from '../components/chat-memory/CaseDialog.vue'
import { parseCaseImages, waitingHours } from '../components/chat-memory/labels'

const mocks = vi.hoisted(() => ({ get: vi.fn(), patch: vi.fn() }))
vi.mock('../api', () => ({ default: mocks }))
vi.mock('vue-i18n', () => ({ useI18n: () => ({ t: (key: string) => key }) }))

const sampleCase = {
  id: 'c1', subject_type: 'user', subject_id: 'u1', customer_name: 'Anh Bình', customer_code: 'KH01',
  type: 'product_defect', severity: 'high', title: 'Kính FF800 bị nứt', detail: 'Nhận hàng thấy nứt',
  evidence: 'kính nứt rồi em', images: '[{"url":"https://f1.zdn.vn/a.jpg","description":"kính nứt"}]',
  status: 'open', source: 'bot', followup_count: 1, assignee_name: '', assignee_id: '',
  customer_update: '', resolution: '', opened_session_id: 's1', created_at: new Date().toISOString(),
}

describe('Complaint cases panel', () => {
  beforeEach(() => {
    mocks.get.mockReset().mockImplementation(async (url: string) => {
      if (url.endsWith('/customer-cases')) return { data: { data: [sampleCase], total: 1, counts: { open: 1, resolved: 2 } } }
      if (url.endsWith('/customer-cases/c1')) return { data: { case: sampleCase, events: [] } }
      if (url.endsWith('/users')) return { data: [{ user_id: 'u-staff', name: 'Hậu', email: 'h@x' }] }
      return { data: {} }
    })
    mocks.patch.mockReset()
  })

  it('lists open cases first and filters by status', async () => {
    const wrapper = shallowMount(CasesPanel, { props: { tenantId: 't1' }, global: { mocks: { $t: (k: string) => k } } })
    await flushPromises()
    expect(mocks.get).toHaveBeenCalledWith('/tenants/t1/customer-cases', { params: expect.objectContaining({ status: 'active', page: 1 }) })
    const vm = wrapper.vm as any
    expect(vm.cases).toHaveLength(1)
    vm.status = 'resolved'
    await vm.load(1)
    expect(mocks.get).toHaveBeenLastCalledWith('/tenants/t1/customer-cases', { params: expect.objectContaining({ status: 'resolved' }) })
  })

  it('shows one customer\'s cases in the profile', async () => {
    shallowMount(CasesPanel, { props: { tenantId: 't1', compact: true, subjectType: 'group', subjectId: 'g1', customerCode: 'KH01' }, global: { mocks: { $t: (k: string) => k } } })
    await flushPromises()
    expect(mocks.get).toHaveBeenCalledWith('/tenants/t1/customer-cases', {
      params: expect.objectContaining({ status: 'all', subject_type: 'group', subject_id: 'g1', customer_code: 'KH01' }),
    })
  })

  it('saves status, assignee, customer update and note', async () => {
    mocks.patch.mockResolvedValue({ data: { case: { ...sampleCase, status: 'in_progress' } } })
    const wrapper = shallowMount(CaseDialog, { props: { modelValue: true, tenantId: 't1', caseId: 'c1' }, global: { mocks: { $t: (k: string) => k } } })
    await flushPromises()
    const vm = wrapper.vm as any
    expect(vm.item.title).toBe('Kính FF800 bị nứt')
    vm.form.status = 'in_progress'
    vm.form.assignee = { title: 'Hậu', value: 'u-staff' }
    vm.form.customer_update = 'Bên em đổi kính mới, giao thứ 5'
    vm.form.note = 'Đã gọi khách'
    await vm.save()
    expect(mocks.patch).toHaveBeenCalledWith('/tenants/t1/customer-cases/c1', expect.objectContaining({
      status: 'in_progress', assignee_name: 'Hậu', assignee_id: 'u-staff',
      customer_update: 'Bên em đổi kính mới, giao thứ 5', note: 'Đã gọi khách',
    }))
  })
})

describe('case labels', () => {
  it('parses images and measures waiting time', () => {
    expect(parseCaseImages('[{"url":"a"},{"x":1}]')).toEqual([{ url: 'a' }])
    expect(parseCaseImages('bad')).toEqual([])
    expect(Math.round(waitingHours(new Date(Date.now() - 3 * 3_600_000).toISOString()))).toBe(3)
  })
})
