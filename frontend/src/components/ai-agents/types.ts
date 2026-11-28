// Shapes of the specialist agent API (docs/admin/agents.md).

export type Audience = 'customer' | 'staff' | 'all'
export type Tier = 'public' | 'private'

export interface AgentOptions {
  max_tool_calls?: number
  temperature?: number | null
  legacy_source?: string
}

export interface Agent {
  id: string
  tenant_id?: string
  key: string
  name: string
  description: string
  audience: Audience
  runtime: string
  langflow_flow_id: string
  provider: string
  model: string
  system_prompt?: string
  tools: string[]
  knowledge_tags: string[]
  options: AgentOptions
  is_active: boolean
}

export interface ToolInfo {
  name: string
  group: string
  tiers: Tier[]
  writes: boolean
  hidden?: boolean
}

export interface AgentRevision {
  id: string
  agent_id: string
  name: string
  description: string
  system_prompt: string
  edited_by: string
  created_at: string
}

export interface TeamOptions {
  sticky_minutes?: number
  disable_handoff?: boolean
}

export interface TeamMembership {
  team_id: string
  agent_id: string
  position: number
  enabled: boolean
}

export interface TeamChannel {
  id: string
  name: string
  channel_type: string
  // The team the channel itself points at; empty when it follows the default.
  ai_team_id: string
}

export interface Team {
  id: string
  name: string
  persona_prompt: string
  router_prompt: string
  default_agent_id: string
  is_default: boolean
  is_active: boolean
  options: TeamOptions
  members: Agent[]
  memberships: TeamMembership[]
  channels: TeamChannel[]
  silent: string[]
}

export interface MemberInput {
  agent_id: string
  position: number
  enabled: boolean
}

export interface TeamInput {
  name: string
  persona_prompt: string
  router_prompt: string
  default_agent_id: string
  is_active: boolean
  options: TeamOptions
  members: MemberInput[]
}

// A document tag and how many chatbot documents carry it.
export interface KnowledgeTag {
  tag: string
  documents: number
}

export interface AgentSettings {
  enabled: boolean
  has_agents: boolean
  drift: string[]
}

// A 400 from the agents API names the field to show the message next to.
export interface FieldError {
  field: string
  message: string
}

export function fieldErrorOf(err: unknown): FieldError | null {
  const data = (err as { response?: { status?: number, data?: { error?: string, field?: string, message?: string } } })?.response
  if (data?.status === 400 && data.data?.error === 'invalid_agent' && data.data.field) {
    return { field: data.data.field, message: data.data.message || '' }
  }
  return null
}

export function errorCodeOf(err: unknown): string {
  return (err as { response?: { data?: { error?: string } } })?.response?.data?.error || ''
}

export function emptyAgent(): Agent {
  return {
    id: '', key: '', name: '', description: '', audience: 'customer', runtime: 'langflow',
    langflow_flow_id: '', provider: '', model: '', system_prompt: '', tools: [], knowledge_tags: [],
    options: {}, is_active: true,
  }
}
