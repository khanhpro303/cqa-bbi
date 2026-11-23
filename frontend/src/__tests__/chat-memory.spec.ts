// @vitest-environment happy-dom
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, shallowMount } from '@vue/test-utils'
import ChatMemory from '../views/ChatMemory.vue'
import { highestSeverity, issueTypeLabelKey, parseIssues } from '../components/chat-memory/labels'

const mocks = vi.hoisted(() => ({ get: vi.fn(), post: vi.fn(), delete: vi.fn() }))
vi.mock('../api', () => ({ default: mocks }))
vi.mock('vue-router', () => ({ useRoute: () => ({ params: { tenantId: 'tenant-1' }, query: {} }) }))
vi.mock('vue-i18n', () => ({ useI18n: () => ({ t: (key: string) => key }) }))

describe('ChatMemory page', () => {
  beforeEach(() => {
    mocks.get.mockReset().mockImplementation(async (url: string) => {
      if (url.endsWith('/profiles')) {
        return { data: { data: [{ id: 'p1', subject_type: 'user', subject_id: 'u1', customer_name: 'Anh Bình', profile: '{"address_form":"anh","interests":["E-24","FF818"],"custom":"x"}', card: 'Xưng anh', sessions_count: 2 }], total: 1 } }
      }
      return { data: { data: [], total: 0 } }
    })
  })

  it('opens on the complaints tab', async () => {
    const wrapper = shallowMount(ChatMemory, { global: { mocks: { $t: (k: string) => k }, renderStubDefaultSlot: true } })
    await flushPromises()
    expect((wrapper.vm as any).tab).toBe('cases')
    expect(mocks.get).not.toHaveBeenCalledWith('/tenants/tenant-1/chat-memory/profiles', expect.anything())
  })

  it('loads profiles and renders the profile fields with labels', async () => {
    const wrapper = shallowMount(ChatMemory, { global: { mocks: { $t: (k: string) => k }, renderStubDefaultSlot: true } })
    await flushPromises()
    ;(wrapper.vm as any).tab = 'profiles'
    await flushPromises()
    expect(mocks.get).toHaveBeenCalledWith('/tenants/tenant-1/chat-memory/profiles', expect.anything())
    const vm = wrapper.vm as any
    expect(vm.profiles).toHaveLength(1)
    const rows = vm.profileRows(vm.profiles[0].profile)
    expect(rows.map((r: any) => r.label)).toEqual(['cm_profile_address_form', 'cm_profile_interests', 'custom'])
    expect(rows[1].value).toBe('E-24, FF818')
  })

  it('filters sessions by high-severity issues', async () => {
    const wrapper = shallowMount(ChatMemory, { global: { mocks: { $t: (k: string) => k }, renderStubDefaultSlot: true } })
    await flushPromises()
    const vm = wrapper.vm as any
    vm.issueFilter = 'high'
    await vm.loadSessions(1)
    expect(mocks.get).toHaveBeenLastCalledWith('/tenants/tenant-1/chat-memory/sessions', { params: expect.objectContaining({ issues: 'high', page: 1 }) })
  })
})

describe('chat memory labels', () => {
  it('parses issues and picks the highest severity', () => {
    const issues = parseIssues('[{"type":"delivery","severity":"medium","detail":"a"},{"type":"x","severity":"high","detail":"b"},{"bad":1}]')
    expect(issues).toHaveLength(2)
    expect(highestSeverity(issues)).toBe('high')
    expect(issueTypeLabelKey('delivery')).toBe('cm_issue_type_delivery')
    expect(issueTypeLabelKey('x')).toBe('cm_issue_type_other')
    expect(parseIssues('not json')).toEqual([])
  })
})
