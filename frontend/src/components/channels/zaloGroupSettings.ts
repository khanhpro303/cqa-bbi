import type { AlertOutput } from './CampaignAlertOutputs.vue'

export interface ComplaintStream {
  key: string
  name: string
  description: string
  alert_group_id: string
}

export const DEFAULT_COMPLAINT_RESOLVED_DAYS = 14
export const MAX_COMPLAINT_RESOLVED_DAYS = 90

function readStreams(raw: unknown): ComplaintStream[] {
  if (!Array.isArray(raw)) return []
  return raw
    .filter((s) => s && typeof s === 'object')
    .map((s: any) => ({
      key: typeof s.key === 'string' ? s.key : '',
      name: typeof s.name === 'string' ? s.name : '',
      description: typeof s.description === 'string' ? s.description : '',
      alert_group_id: typeof s.alert_group_id === 'string' ? s.alert_group_id : '',
    }))
}

// streamKey turns a stream name into a stable key: "Bảo hành" → "bao_hanh".
export function streamKey(name: string, taken: Set<string>) {
  const base = name
    .normalize('NFD')
    .replace(/[\u0300-\u036f]/g, '')
    .replace(/đ/g, 'd')
    .replace(/Đ/g, 'D')
    .toLowerCase()
    .replace(/[^a-z0-9]+/g, '_')
    .replace(/^_+|_+$/g, '')
    .slice(0, 36) || 'luong'
  let key = base
  // "none" is reserved for the cases page's "no stream" filter.
  for (let i = 2; taken.has(key) || key === 'none'; i++) key = `${base}_${i}`
  return key
}

// normalizeStreams drops unnamed rows and gives new rows a key; a saved
// stream keeps its key so cases filed under it stay linked.
export function normalizeStreams(streams: ComplaintStream[]): ComplaintStream[] {
  const named = streams.filter((s) => s.name.trim())
  const taken = new Set(named.map((s) => s.key).filter(Boolean))
  return named.map((s) => {
    const key = s.key || streamKey(s.name, taken)
    taken.add(key)
    return { key, name: s.name.trim(), description: s.description.trim(), alert_group_id: s.alert_group_id || '' }
  })
}

function readResolvedDays(raw: unknown) {
  const n = typeof raw === 'number' ? Math.round(raw) : NaN
  if (!Number.isFinite(n) || n < 1) return DEFAULT_COMPLAINT_RESOLVED_DAYS
  return Math.min(n, MAX_COMPLAINT_RESOLVED_DAYS)
}

export function readZaloGroupSettings(metadata: Record<string, unknown> = {}) {
  return {
    group_assistant_mode: metadata.group_assistant_mode === true,
    group_staff_call_only: metadata.group_staff_call_only !== false,
    group_bot_names: typeof metadata.group_bot_names === 'string' ? metadata.group_bot_names : '',
    campaign_alerts_enabled: metadata.campaign_alerts_enabled === true,
    campaign_alert_group_id: typeof metadata.campaign_alert_group_id === 'string' ? metadata.campaign_alert_group_id : '',
    campaign_alert_outputs: Array.isArray(metadata.campaign_alert_outputs)
      ? (metadata.campaign_alert_outputs as AlertOutput[]).map((output) => ({ ...output }))
      : [],
    handover_alert_group_id: typeof metadata.handover_alert_group_id === 'string' ? metadata.handover_alert_group_id : '',
    chat_memory_issue_alerts: metadata.chat_memory_issue_alerts !== false,
    chat_memory_profile_in_prompt: metadata.chat_memory_profile_in_prompt === true,
    complaint_cases: metadata.complaint_cases === true,
    complaint_resolved_days: readResolvedDays(metadata.complaint_resolved_days),
    complaint_streams: readStreams(metadata.complaint_streams),
  }
}

export type ZaloGroupSettings = ReturnType<typeof readZaloGroupSettings>

export function zaloGroupSettingsPatch(settings: ZaloGroupSettings, metadata: Record<string, unknown>) {
  const original = readZaloGroupSettings(metadata)
  const current = {
    ...settings,
    campaign_alert_group_id: settings.campaign_alerts_enabled ? settings.campaign_alert_group_id || '' : '',
    campaign_alert_outputs: settings.campaign_alerts_enabled ? settings.campaign_alert_outputs : [],
    handover_alert_group_id: settings.handover_alert_group_id || '',
    complaint_resolved_days: readResolvedDays(Number(settings.complaint_resolved_days)),
    complaint_streams: normalizeStreams(settings.complaint_streams),
  }
  return Object.fromEntries(
    (Object.keys(current) as (keyof ZaloGroupSettings)[])
      .filter((key) => JSON.stringify(current[key]) !== JSON.stringify(original[key]))
      .map((key) => [key, current[key]]),
  )
}
