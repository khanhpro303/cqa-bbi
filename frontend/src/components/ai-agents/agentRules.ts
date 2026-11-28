// Client-side mirrors of the agent rules in backend/agents, used to show
// the effect of an edit before it is saved. The backend stays the judge.
import type { Agent, Audience, Tier, ToolInfo } from './types'

export const AUDIENCES: Audience[] = ['customer', 'staff', 'all']

// Order of tool groups in the editor.
export const TOOL_GROUP_ORDER = ['products', 'orders', 'debt', 'customers', 'knowledge', 'complaints']

export const CHAT_CHANNEL_TYPES = ['zalo_oa', 'personal_zalo_import']

// audienceTiers maps who an agent serves to the security tiers its tools
// must work in: staff chat 1:1 on the private tier, everything else is public.
export function audienceTiers(audience: Audience): Tier[] {
  if (audience === 'staff') return ['private']
  if (audience === 'all') return ['public', 'private']
  return ['public']
}

export function toolUsable(tool: ToolInfo, audience: Audience): boolean {
  return audienceTiers(audience).every(tier => tool.tiers.includes(tier))
}

export interface ToolGroup {
  group: string
  tools: Array<{ info: ToolInfo, usable: boolean }>
}

// groupTools lists the catalog by group for the checklist. Hidden tools are
// left out unless the agent already has them, so they can still be removed.
export function groupTools(catalog: ToolInfo[], selected: string[], audience: Audience): ToolGroup[] {
  const groups = new Map<string, ToolGroup>()
  for (const info of catalog) {
    if (info.hidden && !selected.includes(info.name)) continue
    let g = groups.get(info.group)
    if (!g) {
      g = { group: info.group, tools: [] }
      groups.set(info.group, g)
    }
    g.tools.push({ info, usable: toolUsable(info, audience) })
  }
  const rank = (group: string) => {
    const i = TOOL_GROUP_ORDER.indexOf(group)
    return i < 0 ? TOOL_GROUP_ORDER.length : i
  }
  return [...groups.values()].sort((a, b) => rank(a.group) - rank(b.group) || a.group.localeCompare(b.group))
}

// unusableTools returns the selected tools the audience may not use (the
// backend refuses to save them).
export function unusableTools(catalog: ToolInfo[], selected: string[], audience: Audience): string[] {
  return selected.filter(name => {
    const info = catalog.find(t => t.name === name)
    return !info || !toolUsable(info, audience)
  })
}

export function serves(agent: Pick<Agent, 'audience'>, audience: 'customer' | 'staff'): boolean {
  return agent.audience === 'all' || agent.audience === audience
}

export interface MemberState {
  agent: Pick<Agent, 'audience' | 'is_active'>
  enabled: boolean
}

// silentAudiences lists the audiences no enabled, active member serves: on
// the team's channels the bot stays silent for them and people answer.
export function silentAudiences(members: MemberState[]): Array<'customer' | 'staff'> {
  const out: Array<'customer' | 'staff'> = []
  for (const audience of ['customer', 'staff'] as const) {
    if (!members.some(m => m.enabled && m.agent.is_active && serves(m.agent, audience))) out.push(audience)
  }
  return out
}

// slugify turns a Vietnamese name into an agent key: lower-case ASCII
// letters, digits and dashes, at most 63 characters.
export function slugify(name: string): string {
  return name
    .normalize('NFD')
    .replace(/[̀-ͯ]/g, '')
    .replace(/[đĐ]/g, 'd')
    .toLowerCase()
    .replace(/[^a-z0-9]+/g, '-')
    .replace(/^-+|-+$/g, '')
    .slice(0, 63)
    .replace(/-+$/g, '')
}

// normalizeTag mirrors knowledge.NormalizeTag: NFC, lower case, single
// spaces, so a typed tag matches the stored document tags.
export function normalizeTag(tag: string): string {
  return tag.normalize('NFC').split(/\s+/).filter(Boolean).join(' ').toLowerCase()
}

export function normalizeTags(tags: unknown[]): string[] {
  return [...new Set(tags.map(t => normalizeTag(String(t ?? ''))).filter(Boolean))].sort()
}

export const AGENT_KEY_PATTERN = /^[a-z0-9][a-z0-9-]{0,62}$/
export const FLOW_ID_PATTERN = /^[A-Za-z0-9-]{0,64}$/
export const MAX_PROMPT_CHARS = 120_000

// moveItem returns a copy of list with the item at from moved to to.
export function moveItem<T>(list: T[], from: number, to: number): T[] {
  if (to < 0 || to >= list.length || from === to) return list.slice()
  const out = list.slice()
  const [item] = out.splice(from, 1)
  out.splice(to, 0, item)
  return out
}

export interface MemberPick<A> {
  agent: A & Pick<Agent, 'id' | 'audience' | 'is_active'>
  enabled: boolean
}

// pickAgent mirrors Team.Pick: the only candidate, else the team default
// when it serves the audience, else the first candidate. null = silent.
export function pickAgent<A>(members: MemberPick<A>[], defaultAgentId: string, audience: 'customer' | 'staff'): (A & Pick<Agent, 'id' | 'audience' | 'is_active'>) | null {
  const candidates = members.filter(m => m.enabled && m.agent.is_active && serves(m.agent, audience)).map(m => m.agent)
  if (candidates.length === 0) return null
  if (candidates.length === 1) return candidates[0]
  return candidates.find(a => a.id === defaultAgentId) || candidates[0]
}

export interface DiffLine {
  kind: 'same' | 'add' | 'del' | 'gap'
  text: string
}

// diffLines compares two texts line by line (longest common subsequence)
// and keeps `context` unchanged lines around each change; longer unchanged
// runs collapse into a 'gap' line holding the number of lines skipped.
export function diffLines(before: string, after: string, context = 2): DiffLine[] {
  const a = before.split('\n')
  const b = after.split('\n')
  // Trim the common head and tail so long prompts with a small edit stay cheap.
  let head = 0
  while (head < a.length && head < b.length && a[head] === b[head]) head++
  let tail = 0
  while (tail < a.length - head && tail < b.length - head && a[a.length - 1 - tail] === b[b.length - 1 - tail]) tail++
  const am = a.slice(head, a.length - tail)
  const bm = b.slice(head, b.length - tail)
  const lines: DiffLine[] = a.slice(0, head).map(text => ({ kind: 'same' as const, text }))
  if (am.length * bm.length > 4_000_000) {
    // Too large to align: show the middle as removed then added.
    am.forEach(text => lines.push({ kind: 'del', text }))
    bm.forEach(text => lines.push({ kind: 'add', text }))
  } else {
    const n = am.length
    const m = bm.length
    const lcs: Uint32Array[] = Array.from({ length: n + 1 }, () => new Uint32Array(m + 1))
    for (let i = n - 1; i >= 0; i--) {
      for (let j = m - 1; j >= 0; j--) {
        lcs[i][j] = am[i] === bm[j] ? lcs[i + 1][j + 1] + 1 : Math.max(lcs[i + 1][j], lcs[i][j + 1])
      }
    }
    let i = 0
    let j = 0
    while (i < n && j < m) {
      if (am[i] === bm[j]) {
        lines.push({ kind: 'same', text: am[i] }); i++; j++
      } else if (lcs[i + 1][j] >= lcs[i][j + 1]) {
        lines.push({ kind: 'del', text: am[i] }); i++
      } else {
        lines.push({ kind: 'add', text: bm[j] }); j++
      }
    }
    while (i < n) lines.push({ kind: 'del', text: am[i++] })
    while (j < m) lines.push({ kind: 'add', text: bm[j++] })
  }
  a.slice(a.length - tail).forEach(text => lines.push({ kind: 'same', text }))

  // Collapse unchanged runs away from changes.
  const keep = lines.map(l => l.kind !== 'same')
  const near = keep.map((_, idx) => {
    for (let k = Math.max(0, idx - context); k <= Math.min(lines.length - 1, idx + context); k++) {
      if (keep[k]) return true
    }
    return false
  })
  const out: DiffLine[] = []
  let skipped = 0
  lines.forEach((line, idx) => {
    if (near[idx]) {
      if (skipped) out.push({ kind: 'gap', text: String(skipped) })
      skipped = 0
      out.push(line)
    } else {
      skipped++
    }
  })
  if (skipped) out.push({ kind: 'gap', text: String(skipped) })
  return out
}

export function diffStats(lines: DiffLine[]): { added: number, removed: number } {
  return {
    added: lines.filter(l => l.kind === 'add').length,
    removed: lines.filter(l => l.kind === 'del').length,
  }
}
