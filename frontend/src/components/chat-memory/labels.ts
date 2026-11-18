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
