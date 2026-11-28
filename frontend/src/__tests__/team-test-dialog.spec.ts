// @vitest-environment happy-dom
import { beforeEach, expect, it, vi } from 'vitest'
import { flushPromises, shallowMount } from '@vue/test-utils'
import TeamTestDialog from '../components/ai-agents/TeamTestDialog.vue'
import type { Agent, Audience, Team } from '../components/ai-agents/types'

const mocks = vi.hoisted(() => ({ post: vi.fn() }))
vi.mock('../api', () => ({ default: mocks }))
vi.mock('vue-i18n', () => ({
  useI18n: () => ({ t: (key: string) => key, te: (key: string) => key.startsWith('ag_test_') }),
}))

const agent = (id: string, key: string, audience: Audience = 'customer'): Agent => ({
  id, key, name: key, description: '', audience, runtime: 'langflow', langflow_flow_id: '', provider: '', model: '',
  tools: [], knowledge_tags: [], options: {}, is_active: true,
})
const team: Team = {
  id: 't1', name: 'Mặc định', persona_prompt: '', router_prompt: '', default_agent_id: 'a1', is_default: true, is_active: true, options: {},
  members: [agent('a1', 'ban-hang'), agent('a2', 'cong-no'), agent('a3', 'noi-bo', 'staff')],
  memberships: [
    { team_id: 't1', agent_id: 'a1', position: 0, enabled: true },
    { team_id: 't1', agent_id: 'a2', position: 1, enabled: true },
    { team_id: 't1', agent_id: 'a3', position: 2, enabled: true },
  ],
  channels: [], silent: [],
}

beforeEach(() => mocks.post.mockReset())

function mountDialog() {
  return shallowMount(TeamTestDialog, {
    props: { modelValue: false, tenantId: 'tenant-1', team, canStaff: false },
    global: { mocks: { $t: (key: string) => key } },
  })
}

it('routes a message and continues the chat with the chosen agent', async () => {
  const wrapper = mountDialog()
  await wrapper.setProps({ modelValue: true })
  const vm = wrapper.vm as unknown as { message: string, currentAgent: string, reply: boolean, run: () => Promise<void>, result: { agent?: { key: string } } | null }
  mocks.post.mockResolvedValueOnce({ data: {
    agent: { id: 'a2', key: 'cong-no', name: 'cong-no' }, reason: 'router', router_reason: 'hỏi công nợ', routed: true,
    candidates: [{ id: 'a1', key: 'ban-hang', name: 'ban-hang' }, { id: 'a2', key: 'cong-no', name: 'cong-no' }],
  } })
  vm.message = 'em còn nợ bao nhiêu?'
  await vm.run()
  await flushPromises()
  expect(mocks.post).toHaveBeenCalledWith('/tenants/tenant-1/ai-agents/test', {
    team_id: 't1', audience: 'customer', message: 'em còn nợ bao nhiêu?', current_agent: '', reply: false, session_id: '',
  })
  expect(vm.result?.agent?.key).toBe('cong-no')
  expect(vm.currentAgent).toBe('cong-no')

  mocks.post.mockResolvedValueOnce({ data: {
    agent: { id: 'a2', key: 'cong-no', name: 'cong-no' }, reason: 'router_kept', candidates: [],
    reply: 'Dạ anh còn nợ...', session_id: '5d0e8f0e-1111-4c3a-9d55-0123456789ab',
    usage: { provider: 'claude', model: 'm', input_tokens: 10, output_tokens: 5, estimated: false, cost_usd: 0.001 },
  } })
  vm.reply = true
  vm.message = 'còn hạn thanh toán không?'
  await vm.run()
  await flushPromises()
  expect(mocks.post.mock.calls[1][1]).toMatchObject({ current_agent: 'cong-no', reply: true })
  expect(wrapper.get('[data-testid="test-reply-text"]').text()).toBe('Dạ anh còn nợ...')
})

it('keeps the routing result when the reply cannot run', async () => {
  const wrapper = mountDialog()
  await wrapper.setProps({ modelValue: true })
  const vm = wrapper.vm as unknown as { message: string, reply: boolean, run: () => Promise<void> }
  mocks.post.mockRejectedValueOnce({ response: { data: {
    error: 'console_flow_not_configured',
    routing: { agent: { id: 'a1', key: 'ban-hang', name: 'ban-hang' }, reason: 'team_default', candidates: [] },
  } } })
  vm.reply = true
  vm.message = 'chào shop'
  await vm.run()
  await flushPromises()
  expect(wrapper.get('[data-testid="test-error"]').text()).toBe('ag_test_err_console_flow_not_configured')
  expect(wrapper.get('[data-testid="test-agent"]').text()).toBe('ban-hang')
})
