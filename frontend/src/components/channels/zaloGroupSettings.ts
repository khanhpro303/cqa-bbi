import type { AlertOutput } from './CampaignAlertOutputs.vue'

// A complaint stream (luồng xử lý): the decision model files each case under
// one, and its alert goes to the stream's group (or the handover group).
export interface ComplaintStream {
  key: string
  name: string
  description: string
  alert_group_id: string
}

// streamKey makes a stable slug from a stream name ("Bảo hành" → "bao_hanh").
export function streamKey(name: string): string {
  return name
    .replace(/đ/g, 'd')
    .replace(/Đ/g, 'D')
    .normalize('NFD')
    .replace(/[\u0300-\u036f]/g, '')
    .toLowerCase()
    .replace(/[^a-z0-9]+/g, '_')
    .replace(/^_+|_+$/g, '')
    .slice(0, 40)
    .replace(/_+$/, '')
}

function readStreams(value: unknown): ComplaintStream[] {
  if (!Array.isArray(value)) return []
  return value
    .filter((row): row is Record<string, unknown> => !!row && typeof row === 'object')
    .map((row) => ({
      key: typeof row.key === 'string' ? row.key : '',
      name: typeof row.name === 'string' ? row.name : '',
      description: typeof row.description === 'string' ? row.description : '',
      alert_group_id: typeof row.alert_group_id === 'string' ? row.alert_group_id : '',
    }))
}

// cleanStreams drops unnamed rows and gives new rows a key from their name.
function cleanStreams(streams: ComplaintStream[]): ComplaintStream[] {
  return streams
    .filter((row) => row.name.trim() !== '')
    .map((row) => ({
      key: row.key || streamKey(row.name),
      name: row.name.trim(),
      description: row.description.trim(),
      alert_group_id: row.alert_group_id || '',
    }))
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
    complaint_streams: cleanStreams(settings.complaint_streams),
  }
  return Object.fromEntries(
    (Object.keys(current) as (keyof ZaloGroupSettings)[])
      .filter((key) => JSON.stringify(current[key]) !== JSON.stringify(original[key]))
      .map((key) => [key, current[key]]),
  )
}
