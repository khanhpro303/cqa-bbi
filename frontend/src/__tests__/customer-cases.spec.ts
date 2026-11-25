// @vitest-environment happy-dom
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, shallowMount } from '@vue/test-utils'
import CasesPanel from '../components/chat-memory/CasesPanel.vue'
import CaseDialog from '../components/chat-memory/CaseDialog.vue'
import { mergeCaseImages, parseCaseImages, waitingHours } from '../components/chat-memory/labels'

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
      if (url.endsWith('/users')) return { data: [{ user_id: 'u-staff', name: 'Hậu', email: 'h@x' }, { user_id: 'u-other', name: 'Hậu', email: 'hau2@x' }] }
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
    // Two staff named Hậu: the list tells them apart, the saved name stays plain.
    expect(vm.userOptions.map((o: any) => o.title)).toEqual(['Hậu (h@x)', 'Hậu (hau2@x)'])
    vm.form.assignee = vm.userOptions[0]
    vm.form.customer_update = 'Bên em đổi kính mới, giao thứ 5'
    vm.form.note = 'Đã gọi khách'
    await vm.save()
    expect(mocks.patch).toHaveBeenCalledWith('/tenants/t1/customer-cases/c1', {
      status: 'in_progress', assignee_name: 'Hậu', assignee_id: 'u-staff',
      customer_update: 'Bên em đổi kính mới, giao thứ 5', note: 'Đã gọi khách',
    })
  })

  it('moves a case to another stream and filters by stream', async () => {
    mocks.patch.mockResolvedValue({ data: { case: { ...sampleCase, stream: 'marketing' } } })
    const streams = [{ key: 'bao_hanh', name: 'Bảo hành' }, { key: 'marketing', name: 'Marketing' }]
    const wrapper = shallowMount(CaseDialog, { props: { modelValue: true, tenantId: 't1', caseId: 'c1', streams }, global: { mocks: { $t: (k: string) => k } } })
    await flushPromises()
    const vm = wrapper.vm as any
    expect(vm.form.stream).toBe('')
    vm.form.stream = 'marketing'
    await vm.save()
    expect(mocks.patch).toHaveBeenCalledWith('/tenants/t1/customer-cases/c1', { stream: 'marketing' })

    mocks.get.mockImplementation(async (url: string) => {
      if (url.endsWith('/customer-cases')) return { data: { data: [sampleCase], total: 1, counts: {}, streams } }
      return { data: {} }
    })
    const panel = shallowMount(CasesPanel, { props: { tenantId: 't1' }, global: { mocks: { $t: (k: string) => k } } })
    await flushPromises()
    const pvm = panel.vm as any
    expect(pvm.streamOptions.map((o: any) => o.value)).toEqual(['', 'bao_hanh', 'marketing', 'none'])
    pvm.stream = 'bao_hanh'
    await pvm.load(1)
    expect(mocks.get).toHaveBeenLastCalledWith('/tenants/t1/customer-cases', { params: expect.objectContaining({ stream: 'bao_hanh' }) })
  })

  it('saves only the note when only the note changed', async () => {
    mocks.patch.mockResolvedValue({ data: { case: sampleCase } })
    const wrapper = shallowMount(CaseDialog, { props: { modelValue: true, tenantId: 't1', caseId: 'c1' }, global: { mocks: { $t: (k: string) => k } } })
    await flushPromises()
    const vm = wrapper.vm as any
    vm.form.note = 'Đã gọi khách'
    await vm.save()
    expect(mocks.patch).toHaveBeenCalledWith('/tenants/t1/customer-cases/c1', { note: 'Đã gọi khách' })
  })

  it('does not resend the assignee when only their directory name changed', async () => {
    mocks.get.mockImplementation(async (url: string) => {
      if (url.endsWith('/customer-cases/c1')) return { data: { case: { ...sampleCase, assignee_name: 'Hậu cũ', assignee_id: 'u-staff' }, events: [] } }
      if (url.endsWith('/users')) return { data: [{ user_id: 'u-staff', name: 'Hậu mới', email: 'h@x' }] }
      return { data: {} }
    })
    mocks.patch.mockResolvedValue({ data: { case: sampleCase } })
    const wrapper = shallowMount(CaseDialog, { props: { modelValue: true, tenantId: 't1', caseId: 'c1' }, global: { mocks: { $t: (k: string) => k } } })
    await flushPromises()
    const vm = wrapper.vm as any
    vm.form.note = 'ghi chú'
    await vm.save()
    expect(mocks.patch).toHaveBeenCalledWith('/tenants/t1/customer-cases/c1', { note: 'ghi chú' })
  })

  it('keeps the case that was opened last', async () => {
    let releaseA: (v: unknown) => void = () => {}
    mocks.get.mockImplementation(async (url: string) => {
      if (url.endsWith('/customer-cases/a')) { await new Promise(r => { releaseA = r }); return { data: { case: { ...sampleCase, id: 'a', title: 'A' }, events: [] } } }
      if (url.endsWith('/customer-cases/b')) return { data: { case: { ...sampleCase, id: 'b', title: 'B' }, events: [] } }
      return { data: [] }
    })
    const wrapper = shallowMount(CaseDialog, { props: { modelValue: true, tenantId: 't1', caseId: 'a' }, global: { mocks: { $t: (k: string) => k } } })
    await wrapper.setProps({ caseId: 'b' })
    await flushPromises()
    releaseA(undefined)
    await flushPromises()
    expect((wrapper.vm as any).item.id).toBe('b')
  })

  it('loads archived evidence only when staff asks to view it and releases the blob on close', async () => {
    const imageId = '27d2a5ad-8d38-4f4f-93c3-d0f7bc673b81'
    const archivedCase = { ...sampleCase, images: JSON.stringify([{ image_id: imageId, description: 'kính bị nứt' }]) }
    mocks.get.mockImplementation(async (url: string) => {
      if (url.endsWith('/customer-cases/c1')) return { data: { case: archivedCase, events: [] } }
      if (url.endsWith('/users')) return { data: [] }
      if (url.endsWith(`/images/${imageId}`)) return { data: new Blob(['jpeg'], { type: 'image/jpeg' }) }
      return { data: {} }
    })
    const createURL = vi.spyOn(URL, 'createObjectURL').mockReturnValue('blob:case-image')
    const revokeURL = vi.spyOn(URL, 'revokeObjectURL')
    const wrapper = shallowMount(CaseDialog, { props: { modelValue: true, tenantId: 't1', caseId: 'c1' }, global: { mocks: { $t: (k: string) => k } } })
    await flushPromises()
    expect(mocks.get).not.toHaveBeenCalledWith(expect.stringContaining('/images/'), expect.anything())

    await wrapper.get('[data-testid="case-image-show"]').trigger('click')
    await flushPromises()
    expect(mocks.get).toHaveBeenLastCalledWith(
      `/tenants/t1/customer-cases/c1/images/${imageId}`,
      expect.objectContaining({ responseType: 'blob' }),
    )
    expect(createURL).toHaveBeenCalledTimes(1)
    expect(wrapper.get('[data-testid="case-image-preview"]').attributes('href')).toBe('blob:case-image')

    await wrapper.setProps({ modelValue: false })
    expect(revokeURL).toHaveBeenCalledWith('blob:case-image')
  })

  it('does not fall back to an expiring Zalo URL when archived evidence is gone', async () => {
    const imageId = '27d2a5ad-8d38-4f4f-93c3-d0f7bc673b81'
    const archivedCase = { ...sampleCase, images: JSON.stringify([{ image_id: imageId, url: 'https://f1.zdn.vn/a.jpg' }]) }
    mocks.get.mockImplementation(async (url: string) => {
      if (url.endsWith('/customer-cases/c1')) return { data: { case: archivedCase, events: [] } }
      if (url.endsWith('/users')) return { data: [] }
      if (url.endsWith(`/images/${imageId}`)) throw new Error('404')
      return { data: {} }
    })
    const wrapper = shallowMount(CaseDialog, { props: { modelValue: true, tenantId: 't1', caseId: 'c1' }, global: { mocks: { $t: (k: string) => k } } })
    await flushPromises()
    const vm = wrapper.vm as any
    await vm.loadCaseImage(vm.images[0])
    expect(vm.activeImageUrl).toBe('')
    expect(vm.imageErrorId).toBe(imageId)
    expect(mocks.get).toHaveBeenCalledTimes(3)
  })

  it('includes evidence attached on later case events', async () => {
    const imageId = '27d2a5ad-8d38-4f4f-93c3-d0f7bc673b81'
    mocks.get.mockImplementation(async (url: string) => {
      if (url.endsWith('/customer-cases/c1')) return {
        data: {
          case: { ...sampleCase, images: '[]' },
          events: [{ id: 'e1', kind: 'followup', images: JSON.stringify([{ image_id: imageId, description: 'ảnh bổ sung' }]) }],
        },
      }
      if (url.endsWith('/users')) return { data: [] }
      return { data: {} }
    })
    const wrapper = shallowMount(CaseDialog, { props: { modelValue: true, tenantId: 't1', caseId: 'c1' }, global: { mocks: { $t: (k: string) => k } } })
    await flushPromises()
    expect((wrapper.vm as any).images).toEqual([{ image_id: imageId, description: 'ảnh bổ sung' }])
  })

  it('ignores an older list response that finishes last', async () => {
    let releaseSlow: (v: unknown) => void = () => {}
    mocks.get.mockImplementation(async (url: string, opts: any) => {
      if (url.endsWith('/customer-cases') && opts?.params?.status === 'active') {
        await new Promise(r => { releaseSlow = r })
        return { data: { data: [sampleCase], total: 1, counts: {} } }
      }
      return { data: { data: [], total: 0, counts: {} } }
    })
    const wrapper = shallowMount(CasesPanel, { props: { tenantId: 't1' }, global: { mocks: { $t: (k: string) => k } } })
    const vm = wrapper.vm as any
    vm.status = 'resolved'
    await vm.load(1)
    releaseSlow(undefined)
    await flushPromises()
    expect(vm.cases).toHaveLength(0)
  })
})

describe('case labels', () => {
  it('parses images and measures waiting time', () => {
    expect(parseCaseImages('[{"url":"https://f1.zdn.vn/a.jpg"},{"x":1}]')).toEqual([{ url: 'https://f1.zdn.vn/a.jpg' }])
    // Only Zalo CDN images are shown: opening a case must not make the
    // browser load an arbitrary address.
    expect(parseCaseImages(JSON.stringify([
      { url: 'http://f1.zdn.vn/a.jpg' }, { url: 'https://evil.example/x.png' },
      { url: 'https://f1.zdn.vn.evil.example/x.png' }, { url: 'https://user@f1.zdn.vn/x.png' }, { url: 'javascript:alert(1)' },
    ]))).toEqual([])
    expect(parseCaseImages('bad')).toEqual([])
    expect(parseCaseImages('[{"image_id":"27d2a5ad-8d38-4f4f-93c3-d0f7bc673b81","description":"nứt"}]')).toEqual([
      { image_id: '27d2a5ad-8d38-4f4f-93c3-d0f7bc673b81', description: 'nứt' },
    ])
    expect(Math.round(waitingHours(new Date(Date.now() - 3 * 3_600_000).toISOString()))).toBe(3)
  })

  it('merges case and history evidence and prefers an archived image id', () => {
    const legacy = { url: 'https://f1.zdn.vn/a.jpg', description: 'cũ' }
    const archived = { image_id: '27d2a5ad-8d38-4f4f-93c3-d0f7bc673b81', url: legacy.url, description: 'đã lưu' }
    expect(mergeCaseImages([legacy], [archived], [archived])).toEqual([archived])
  })
})
