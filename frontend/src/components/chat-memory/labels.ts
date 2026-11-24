export interface ChatProfile {
  id: string
  channel_id: string
  subject_type: 'user' | 'group'
  subject_id: string
  customer_name: string
  customer_code: string
  profile: string
  card: string
  sessions_count: number
  last_session_id: string
  last_session_at: string | null
}

export interface ChatSession {
  id: string
  channel_id: string
  subject_type: 'user' | 'group'
  subject_id: string
  customer_name: string
  customer_code: string
  agent_type: string
  started_at: string
  last_activity_at: string
  inbound_count: number
  outbound_count: number
  status: string
  summary: string
  outcome: string
  sentiment: string
  intents: string
  products: string
  issues: string
  summary_error?: string
  alerted_at: string | null
}

export interface SessionIssue {
  type: string
  severity: string
  detail: string
  evidence?: string
}

export interface TranscriptLine {
  id: string
  speaker: 'customer' | 'bot' | 'staff'
  sender_name: string
  content: string
  content_type: string
  sent_at: string
}

export const profileFieldKeys = [
  'address_form', 'communication_style', 'active_hours', 'interests',
  'price_behavior', 'service_notes', 'risk_flags',
]

const issueTypes = ['complaint', 'delivery', 'product_defect', 'price', 'payment', 'bot_failure', 'staff_request']

export function parseList(raw: string | null | undefined): string[] {
  try {
    const v = JSON.parse(raw || '[]')
    return Array.isArray(v) ? v.filter(x => typeof x === 'string' && x.trim()) : []
  } catch {
    return []
  }
}

export function parseIssues(raw: string | null | undefined): SessionIssue[] {
  try {
    const v = JSON.parse(raw || '[]')
    return Array.isArray(v) ? v.filter(x => x && typeof x.detail === 'string') : []
  } catch {
    return []
  }
}

export function highestSeverity(issues: SessionIssue[]): string {
  if (issues.some(i => i.severity === 'high')) return 'high'
  if (issues.some(i => i.severity === 'medium')) return 'medium'
  return issues.length ? 'low' : ''
}

export function issueTypeLabelKey(type: string): string {
  return issueTypes.includes(type) ? `cm_issue_type_${type}` : 'cm_issue_type_other'
}

export function severityColor(s: string): string {
  return s === 'high' ? 'error' : s === 'medium' ? 'warning' : 'grey'
}

export function sentimentColor(s: string): string {
  return s === 'negative' ? 'error' : s === 'positive' ? 'success' : 'grey'
}

export interface CaseImage {
  url: string
  description?: string
  at?: string
}

export interface CustomerCase {
  id: string
  channel_id: string
  subject_type: 'user' | 'group'
  subject_id: string
  zalo_user_id: string
  zalo_group_id: string
  reporter_name: string
  customer_name: string
  customer_code: string
  type: string
  stream: string
  severity: string
  title: string
  detail: string
  evidence: string
  images: string
  status: string
  source: string
  opened_session_id: string
  assignee_id: string
  assignee_name: string
  customer_update: string
  resolution: string
  followup_count: number
  last_followup_at: string | null
  resolved_at: string | null
  created_at: string
  updated_at: string
}

export interface CaseEvent {
  id: string
  kind: string
  actor: string
  actor_name: string
  session_id: string
  text: string
  images: string
  created_at: string
}

export const caseTypes = ['complaint', 'delivery', 'product_defect', 'price', 'payment', 'staff_request', 'other']
export const caseStatuses = ['open', 'in_progress', 'resolved', 'cancelled']

// Customer images come from Zalo's CDN. Anything else is not shown, so
// opening a case never makes the browser load an arbitrary address.
const zaloImageHosts = ['zdn.vn', 'zadn.vn', 'zalo.me', 'zaloapp.com', 'zalo.vn']

export function isZaloImageURL(raw: string): boolean {
  try {
    const u = new URL(raw)
    if (u.protocol !== 'https:' || u.username || u.password || (u.port && u.port !== '443')) return false
    const host = u.hostname.toLowerCase()
    return zaloImageHosts.some(s => host === s || host.endsWith('.' + s))
  } catch {
    return false
  }
}

export function parseCaseImages(raw: string | null | undefined): CaseImage[] {
  try {
    const v = JSON.parse(raw || '[]')
    return Array.isArray(v) ? v.filter(x => x && typeof x.url === 'string' && isZaloImageURL(x.url)) : []
  } catch {
    return []
  }
}

export function caseStatusColor(s: string): string {
  return s === 'open' ? 'error' : s === 'in_progress' ? 'warning' : s === 'resolved' ? 'success' : 'grey'
}

export function caseEventIcon(kind: string): string {
  switch (kind) {
    case 'opened': return 'mdi-alert-circle-outline'
    case 'followup': return 'mdi-message-reply-text-outline'
    case 'reopened': return 'mdi-restore-alert'
    case 'linked': return 'mdi-link-variant'
    case 'status': return 'mdi-swap-horizontal'
    case 'update': return 'mdi-message-arrow-right-outline'
    case 'assign': return 'mdi-account-arrow-right-outline'
    case 'note': return 'mdi-note-text-outline'
    default: return 'mdi-pencil-outline'
  }
}

// waitingHours is how long a case has waited since it was opened, in hours.
export function waitingHours(createdAt: string, now = Date.now()): number {
  return Math.max(0, (now - new Date(createdAt).getTime()) / 3_600_000)
}

export interface CaseStream {
  key: string
  name: string
}

// streamName shows a case's stream; a stream removed from the settings
// shows its key.
export function streamName(streams: CaseStream[], key?: string | null) {
  if (!key) return ''
  return streams.find(s => s.key === key)?.name || key
}
