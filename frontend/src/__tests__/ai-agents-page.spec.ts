// @vitest-environment happy-dom
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, shallowMount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import AIAgents from '../views/AIAgents.vue'
import AgentEditorDialog from '../components/ai-agents/AgentEditorDialog.vue'
import TeamEditorDialog from '../components/ai-agents/TeamEditorDialog.vue'
import AgentSwitchCard from '../components/ai-agents/AgentSwitchCard.vue'
import AIEngines from '../views/AIEngines.vue'
import { useAuthStore } from '../stores/auth'
import type { ToolInfo } from '../components/ai-agents/types'

const mocks = vi.hoisted(() => ({ get: vi.fn(), put: vi.fn(), post: vi.fn(), delete: vi.fn(), replace: vi.fn() }))
vi.mock('../api', () => ({ default: mocks }))
vi.mock('vue-router', () => ({
  useRoute: () => ({ params: { tenantId: 'tenant-1' }, query: {}, path: '/tenant-1/ai-agents' }),
  useRouter: () => ({ replace: mocks.replace }),
  createRouter: vi.fn(),
  createWebHistory: vi.fn(),
}))
vi.mock('vuetify', () => ({ useTheme: () => ({ global: { current: { value: { dark: false } } } }) }))
vi.mock('vue-i18n', () => ({
  useI18n: () => ({
    t: (key: string, params?: Record<string, unknown>) => params ? `${key}:${JSON.stringify(params)}` : key,
    te: (key: string) => key.startsWith('ag_tool_') || key.startsWith('ag_group_'),
  }),
}))

const both: Array<'public' | 'private'> = ['public', 'private']
const tools: ToolInfo[] = [
  { name: 'lookup_debt', group: 'debt', tiers: both, writes: false },
  { name: 'find_customer', group: 'customers', tiers: both, writes: false },
  { name: 'complaint_record', group: 'complaints', tiers: both, writes: true },
  { name: 'draft_order_create', group: 'orders', tiers: both, writes: true, hidden: true },
]
const customerAgent = { id: 'a1', key: 'tro-ly-khach-hang', name: 'Trợ lý khách hàng', description: '', audience: 'customer', runtime: 'langflow', langflow_flow_id: '', provider: '', model: '', tools: ['lookup_debt'], knowledge_tags: [], options: { legacy_source: 'ai_engine_system_prompt' }, is_active: true }
const staffAgent = { ...customerAgent, id: 'a2', key: 'tro-ly-noi-bo', name: 'Trợ lý nội bộ', audience: 'staff', options: {} }
const debtAgent = { ...customerAgent, id: 'a3', key: 'cong-no', name: 'Bot công nợ', options: {} }
const defaultTeam = {
  id: 't1', name: 'Mặc định', persona_prompt: '', router_prompt: '', default_agent_id: 'a1', is_default: true, is_active: true, options: {},
  members: [customerAgent, staffAgent],
  memberships: [{ team_id: 't1', agent_id: 'a1', position: 0, enabled: true }, { team_id: 't1', agent_id: 'a2', position: 1, enabled: true }],
  channels: [{ id: 'oa', name: 'OA BBI', channel_type: 'zalo_oa', ai_team_id: '' }],
  silent: [],
}
const debtTeam = {
  id: 't2', name: 'Công nợ', persona_prompt: 'Xưng em.', router_prompt: '', default_agent_id: 'a3', is_default: false, is_active: true, options: { sticky_minutes: 30 },
  members: [debtAgent],
  memberships: [{ team_id: 't2', agent_id: 'a3', position: 0, enabled: true }],
  channels: [{ id: 'pz', name: 'Zalo Lan', channel_type: 'personal_zalo_import', ai_team_id: 't2' }],
  silent: ['staff'],
}

function routeGet(overrides: Record<string, unknown> = {}) {
  return async (url: string) => {
    const table: Record<string, unknown> = {
      '/tenants/tenant-1/settings/ai-agents': { enabled: true, has_agents: true, drift: ['tro-ly-khach-hang'] },
      '/tenants/tenant-1/ai-agent-tools': { tools, runtimes: ['langflow'] },
      '/tenants/tenant-1/ai-agents': { agents: [customerAgent, staffAgent, debtAgent] },
      '/tenants/tenant-1/ai-teams': { teams: [defaultTeam, debtTeam] },
      '/tenants/tenant-1/users': [{ user_id: 'u1', name: 'Lan' }],
      '/tenants/tenant-1/ai-agents/a3': { ...debtAgent, system_prompt: 'Chỉ trả lời công nợ.\nDòng 2' },
      '/tenants/tenant-1/ai-agents/a3/revisions': { revisions: [
        { id: 'r2', agent_id: 'a3', name: 'Bot công nợ', description: '', system_prompt: 'Chỉ trả lời công nợ.\nDòng 2', edited_by: 'u1', created_at: '2026-11-28T03:00:00Z' },
        { id: 'r1', agent_id: 'a3', name: 'Bot nợ', description: 'cũ', system_prompt: 'Bản cũ', edited_by: '', created_at: '2026-11-27T03:00:00Z' },
      ] },
      ...overrides,
    }
    if (!(url in table)) throw new Error(`unexpected GET ${url}`)
    return { data: table[url] }
  }
}

const stubs = { global: { mocks: { $t: (key: string) => key }, renderStubDefaultSlot: true } }

function setRole(role: string) {
  setActivePinia(createPinia())
  useAuthStore().tenantPerms = { role, permissions: {} }
}

beforeEach(() => {
  setRole('owner')
  mocks.get.mockReset().mockImplementation(routeGet())
  mocks.put.mockReset().mockResolvedValue({ data: {} })
  mocks.post.mockReset().mockResolvedValue({ data: {} })
  mocks.delete.mockReset().mockResolvedValue({ data: {} })
  mocks.replace.mockReset()
})

describe('Agents AI page', () => {
  it('lists agents, teams and every chat channel with who answers', async () => {
    const wrapper = shallowMount(AIAgents, stubs)
    await flushPromises()
    const vm = wrapper.vm as any
    expect(vm.agents).toHaveLength(3)
    expect(wrapper.find('[data-testid="agent-row-cong-no"]').exists()).toBe(true)

    const rows = vm.channelRows
    expect(rows.map((r: any) => r.channel.id)).toEqual(['oa', 'pz'])
    const oa = rows.find((r: any) => r.channel.id === 'oa')
    expect(oa.selectValue).toBe('')
    expect(oa.picks.customer.key).toBe('tro-ly-khach-hang')
    expect(oa.picks.staff.key).toBe('tro-ly-noi-bo')
    const pz = rows.find((r: any) => r.channel.id === 'pz')
    expect(pz.selectValue).toBe('t2')
    expect(pz.picks.customer.key).toBe('cong-no')
    expect(pz.picks.staff).toBeNull()
    expect(vm.teamItems.map((i: any) => i.value)).toEqual(['', 't2'])

    vm.tab = 'teams'
    await flushPromises()
    expect(wrapper.find('[data-testid="team-silent-alert-t2"]').exists()).toBe(true)
    expect(wrapper.find('[data-testid="team-silent-alert-t1"]').exists()).toBe(false)
    expect(mocks.replace).toHaveBeenCalledWith({ query: { tab: 'teams' } })
  })

  it('moves a channel back to the default team', async () => {
    const wrapper = shallowMount(AIAgents, stubs)
    await flushPromises()
    await (wrapper.vm as any).setChannelTeam('pz', '')
    expect(mocks.put).toHaveBeenCalledWith('/tenants/tenant-1/channels/pz/ai-team', { team_id: '' })
    expect(mocks.get.mock.calls.filter(c => c[0] === '/tenants/tenant-1/ai-teams')).toHaveLength(2)
  })

  it('falls back to the default team when a channel points at a team that is off', async () => {
    mocks.get.mockImplementation(routeGet({ '/tenants/tenant-1/ai-teams': { teams: [defaultTeam, { ...debtTeam, is_active: false }] } }))
    const wrapper = shallowMount(AIAgents, stubs)
    await flushPromises()
    const pz = (wrapper.vm as any).channelRows.find((r: any) => r.channel.id === 'pz')
    expect(pz.ownInactive).toBe(true)
    expect(pz.picks.customer.key).toBe('tro-ly-khach-hang')
  })
})

describe('Agent editor', () => {
  const base = { tenantId: 'tenant-1', catalog: tools, runtimes: ['langflow'], teamNames: [] as string[] }

  it('creates an agent with a key made from its name and only the chosen tools', async () => {
    mocks.post.mockResolvedValue({ data: { ...debtAgent, id: 'new' } })
    const wrapper = shallowMount(AgentEditorDialog, { props: { ...base, modelValue: true, agentId: '' }, ...stubs })
    await flushPromises()
    const vm = wrapper.vm as any
    vm.draft.name = 'Bot công nợ'
    ;(wrapper.vm as any).$.setupState.onNameInput('Bot công nợ')
    vm.draft.tools = ['lookup_debt', 'find_customer']
    vm.draft.system_prompt = 'Chỉ trả lời công nợ.'
    vm.draft.provider = 'openai'
    await vm.save()
    expect(mocks.post).toHaveBeenCalledWith('/tenants/tenant-1/ai-agents', expect.objectContaining({
      key: 'bot-cong-no', name: 'Bot công nợ', audience: 'customer', runtime: 'langflow',
      tools: ['lookup_debt', 'find_customer'], provider: '', model: '', system_prompt: 'Chỉ trả lời công nợ.',
    }))
    expect(wrapper.emitted('saved')?.[0]?.[0]).toMatchObject({ id: 'new' })
    expect(wrapper.emitted('update:modelValue')?.at(-1)).toEqual([false])
  })

  it('shows a backend field error next to the field', async () => {
    mocks.put.mockRejectedValue({ response: { status: 400, data: { error: 'invalid_agent', field: 'langflow_flow_id', message: 'agent phục vụ khách không được chạy trên flow nội bộ' } } })
    const wrapper = shallowMount(AgentEditorDialog, { props: { ...base, modelValue: true, agentId: 'a3' }, ...stubs })
    await flushPromises()
    const vm = wrapper.vm as any
    expect(vm.draft.system_prompt).toBe('Chỉ trả lời công nợ.\nDòng 2')
    vm.draft.langflow_flow_id = 'internal-flow'
    await vm.save()
    expect(mocks.put).toHaveBeenCalledWith('/tenants/tenant-1/ai-agents/a3', expect.objectContaining({ langflow_flow_id: 'internal-flow', key: 'cong-no' }))
    expect(vm.errors).toEqual({ langflow_flow_id: 'agent phục vụ khách không được chạy trên flow nội bộ' })
    expect(wrapper.emitted('saved')).toBeUndefined()
  })

  it('requires a system prompt for the native runtime', async () => {
    const wrapper = shallowMount(AgentEditorDialog, { props: { ...base, runtimes: ['langflow', 'native'], modelValue: true, agentId: '' }, ...stubs })
    await flushPromises()
    const vm = wrapper.vm as any
    vm.draft.name = 'Bot native'
    vm.draft.key = 'bot-native'
    vm.draft.runtime = 'native'
    vm.draft.provider = '9router'
    vm.draft.model = 'cx/gpt-5'
    await vm.save()
    expect(mocks.post).not.toHaveBeenCalled()
    expect(vm.errors.system_prompt).toBe('ag_field_prompt_native_required')
    vm.draft.system_prompt = 'Trả lời ngắn.'
    mocks.post.mockResolvedValue({ data: { id: 'n1', name: 'Bot native' } })
    await vm.save()
    expect(mocks.post).toHaveBeenCalledWith('/tenants/tenant-1/ai-agents', expect.objectContaining({ runtime: 'native', provider: '9router', model: 'cx/gpt-5' }))
  })

  it('offers the document tags and saves them in their stored form', async () => {
    const wrapper = shallowMount(AgentEditorDialog, {
      props: { ...base, modelValue: true, agentId: 'a3', knowledgeTags: [{ tag: 'công nợ', documents: 3 }] },
      ...stubs,
    })
    await flushPromises()
    const vm = wrapper.vm as any
    expect(wrapper.get('[data-testid="agent-tags"]').attributes('hint')).toBe('ag_field_tags_hint_live')
    vm.draft.knowledge_tags = ['Công Nợ', 'công nợ', ' Bảo hành ']
    mocks.put.mockResolvedValue({ data: { ...debtAgent } })
    await vm.save()
    expect(mocks.put.mock.calls[0][1].knowledge_tags).toEqual(['bảo hành', 'công nợ'])
  })

  it('refuses bad keys and flow ids before calling the API', async () => {
    const wrapper = shallowMount(AgentEditorDialog, { props: { ...base, modelValue: true, agentId: '' }, ...stubs })
    await flushPromises()
    const vm = wrapper.vm as any
    vm.draft.name = 'X'
    vm.draft.key = 'Bad Key'
    vm.draft.langflow_flow_id = 'a/b'
    await vm.save()
    expect(mocks.post).not.toHaveBeenCalled()
    expect(Object.keys(vm.errors).sort()).toEqual(['key', 'langflow_flow_id'])
  })

  it('compares a revision with the draft and puts it back in the draft', async () => {
    const wrapper = shallowMount(AgentEditorDialog, { props: { ...base, modelValue: true, agentId: 'a3', userNames: { u1: 'Lan' } }, ...stubs })
    await flushPromises()
    const vm = wrapper.vm as any
    vm.tab = 'revisions'
    await flushPromises()
    expect(mocks.get).toHaveBeenCalledWith('/tenants/tenant-1/ai-agents/a3/revisions')
    // The previous version is selected, not the one in use.
    expect(vm.selectedRevision.id).toBe('r1')
    expect(wrapper.text()).toContain('Lan')
    vm.restoreRevision(vm.selectedRevision)
    expect(vm.draft.system_prompt).toBe('Bản cũ')
    expect(vm.draft.name).toBe('Bot nợ')
    expect(vm.tab).toBe('config')
    expect(vm.dirty).toBe(true)
    expect(mocks.put).not.toHaveBeenCalled()
  })

  it('asks before discarding unsaved edits', async () => {
    const wrapper = shallowMount(AgentEditorDialog, { props: { ...base, modelValue: true, agentId: 'a3' }, ...stubs })
    await flushPromises()
    const vm = wrapper.vm as any
    vm.requestClose()
    expect(wrapper.emitted('update:modelValue')?.at(-1)).toEqual([false])
    vm.draft.system_prompt = 'sửa'
    vm.requestClose()
    expect(vm.confirmDiscard).toBe(true)
    expect(wrapper.emitted('update:modelValue')).toHaveLength(1)
  })

  it('cannot delete an agent that is in a team', async () => {
    const wrapper = shallowMount(AgentEditorDialog, { props: { ...base, modelValue: true, agentId: 'a3', teamNames: ['Công nợ'] }, ...stubs })
    await flushPromises()
    // The delete button sits in the tooltip activator; the tooltip is live
    // only while the agent is in a team.
    expect(wrapper.get('v-tooltip').attributes('disabled')).toBe('false')
    const free = shallowMount(AgentEditorDialog, { props: { ...base, modelValue: true, agentId: 'a3' }, ...stubs })
    await flushPromises()
    expect(free.get('v-tooltip').attributes('disabled')).toBe('true')
  })
})

describe('Team editor', () => {
  const agents = [customerAgent, staffAgent, debtAgent] as any[]

  it('saves members in their new order and warns about silent audiences', async () => {
    mocks.put.mockResolvedValue({ data: { ...debtTeam } })
    const wrapper = shallowMount(TeamEditorDialog, { props: { modelValue: true, tenantId: 'tenant-1', team: debtTeam as any, agents }, ...stubs })
    await flushPromises()
    const vm = wrapper.vm as any
    expect(vm.silent).toEqual(['staff'])
    expect(wrapper.find('[data-testid="team-silent-staff"]').exists()).toBe(true)
    vm.addMember('a2')
    expect(vm.silent).toEqual([])
    expect(vm.picks.staff.key).toBe('tro-ly-noi-bo')
    vm.move(1, 0)
    await vm.save()
    expect(mocks.put).toHaveBeenCalledWith('/tenants/tenant-1/ai-teams/t2', {
      name: 'Công nợ', persona_prompt: 'Xưng em.', router_prompt: '', default_agent_id: 'a3', is_active: true,
      options: { sticky_minutes: 30 },
      members: [{ agent_id: 'a2', position: 0, enabled: true }, { agent_id: 'a3', position: 1, enabled: true }],
    })
  })

  it('clears the default agent when it leaves the team and blocks deleting teams in use', async () => {
    const wrapper = shallowMount(TeamEditorDialog, { props: { modelValue: true, tenantId: 'tenant-1', team: debtTeam as any, agents }, ...stubs })
    await flushPromises()
    const vm = wrapper.vm as any
    expect(vm.deleteBlockedReason).toContain('Zalo Lan')
    vm.removeMember(0)
    expect(vm.draft.default_agent_id).toBe('')
    expect(vm.silent).toEqual(['customer', 'staff'])

    const def = shallowMount(TeamEditorDialog, { props: { modelValue: true, tenantId: 'tenant-1', team: defaultTeam as any, agents }, ...stubs })
    await flushPromises()
    expect((def.vm as any).deleteBlockedReason).toBe('ag_team_delete_default')
  })

  it('creates a team', async () => {
    mocks.post.mockResolvedValue({ data: { id: 't3', name: 'Marketing' } })
    const wrapper = shallowMount(TeamEditorDialog, { props: { modelValue: true, tenantId: 'tenant-1', team: null, agents }, ...stubs })
    await flushPromises()
    const vm = wrapper.vm as any
    await vm.save()
    expect(vm.errors.name).toBe('ag_err_name')
    vm.draft.name = 'Marketing'
    vm.addMember('a1')
    await vm.save()
    expect(mocks.post).toHaveBeenCalledWith('/tenants/tenant-1/ai-teams', expect.objectContaining({
      name: 'Marketing', options: {}, members: [{ agent_id: 'a1', position: 0, enabled: true }],
    }))
  })
})

describe('Agent switch', () => {
  const props = { tenantId: 'tenant-1', canSwitch: true, agents: [customerAgent] as any[], teams: [defaultTeam, debtTeam] as any[] }

  it('turns agents on after confirming and warns about silent teams', async () => {
    mocks.put.mockResolvedValue({ data: { enabled: true, has_agents: true, drift: [] } })
    const wrapper = shallowMount(AgentSwitchCard, { props: { ...props, settings: { enabled: false, has_agents: true, drift: [] } }, ...stubs })
    const vm = wrapper.vm as any
    vm.askSwitch(true)
    expect(vm.confirmSwitch).toBe(true)
    expect(vm.silentTeams).toEqual(['Công nợ (ag_audience_staff)'])
    expect(mocks.put).not.toHaveBeenCalled()
    await vm.applySwitch()
    expect(mocks.put).toHaveBeenCalledWith('/tenants/tenant-1/settings/ai-agents', { enabled: true })
    expect(wrapper.emitted('update:settings')?.[0]).toEqual([{ enabled: true, has_agents: true, drift: [] }])
    expect(wrapper.emitted('changed')).toHaveLength(1)
  })

  it('copies the AI Engine prompts again for drifted agents', async () => {
    mocks.post.mockResolvedValue({ data: { created: false, refreshed: ['tro-ly-khach-hang'] } })
    const wrapper = shallowMount(AgentSwitchCard, { props: { ...props, settings: { enabled: false, has_agents: true, drift: ['tro-ly-khach-hang'] } }, ...stubs })
    expect(wrapper.find('[data-testid="agent-drift"]').exists()).toBe(true)
    expect((wrapper.vm as any).driftNames).toBe('Trợ lý khách hàng')
    await (wrapper.vm as any).refresh()
    expect(mocks.post).toHaveBeenCalledWith('/tenants/tenant-1/settings/ai-agents/migrate', { refresh: true })
    expect(mocks.get).toHaveBeenCalledWith('/tenants/tenant-1/settings/ai-agents')
  })

  it('offers copying the prompts before the first switch, to owners and admins only', async () => {
    const off = { enabled: false, has_agents: false, drift: [] }
    const owner = shallowMount(AgentSwitchCard, { props: { ...props, settings: off }, ...stubs })
    expect(owner.find('[data-testid="agent-migrate"]').exists()).toBe(true)
    const viewer = shallowMount(AgentSwitchCard, { props: { ...props, canSwitch: false, settings: off }, ...stubs })
    expect(viewer.find('[data-testid="agent-migrate"]').exists()).toBe(false)
    expect(viewer.get('[data-testid="agent-switch"]').attributes('disabled')).toBe('true')
  })
})

describe('AI Engine page while agents are on', () => {
  it('shows the two chatbot prompts read-only and keeps them on save', async () => {
    mocks.get.mockImplementation(async (url: string) => ({
      data: url.endsWith('/settings/ai-agents') ? { enabled: true } : url.endsWith('/settings') ? { settings: {
        ai_engine_system_prompt: 'public prompt',
        ai_engine_system_prompt_internal: 'internal prompt',
        ai_engine_system_prompt_crm_analysis: 'Zalo prompt',
      } } : [],
    }))
    const wrapper = shallowMount(AIEngines, { ...stubs, global: { ...stubs.global, stubs: { RouterLink: true } } })
    await flushPromises()
    const vm = wrapper.vm as any
    expect(wrapper.find('[data-testid="agents-on-notice"]').exists()).toBe(true)
    vm.openPromptModal('public')
    expect(vm.promptModalReadonly).toBe(true)
    vm.promptModalText = 'changed'
    vm.savePromptModal()
    expect(vm.langflow.systemPrompt).toBe('public prompt')
    // Other prompts stay editable.
    vm.openPromptModal('crm_analysis')
    expect(vm.promptModalReadonly).toBe(false)
    vm.promptModalText = 'new crm'
    vm.savePromptModal()
    expect(vm.langflow.systemPromptCrmAnalysis).toBe('new crm')
  })
})
