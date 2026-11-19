import type { AlertOutput } from './CampaignAlertOutputs.vue'

export function readZaloGroupSettings(metadata: Record<string, unknown> = {}) {
  return {
    group_assistant_mode: metadata.group_assistant_mode === true,
    campaign_alerts_enabled: metadata.campaign_alerts_enabled === true,
    campaign_alert_group_id: typeof metadata.campaign_alert_group_id === 'string' ? metadata.campaign_alert_group_id : '',
    campaign_alert_outputs: Array.isArray(metadata.campaign_alert_outputs)
      ? (metadata.campaign_alert_outputs as AlertOutput[]).map((output) => ({ ...output }))
      : [],
    handover_alert_group_id: typeof metadata.handover_alert_group_id === 'string' ? metadata.handover_alert_group_id : '',
    chat_memory_issue_alerts: metadata.chat_memory_issue_alerts !== false,
    chat_memory_profile_in_prompt: metadata.chat_memory_profile_in_prompt === true,
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
  }
  return Object.fromEntries(
    (Object.keys(current) as (keyof ZaloGroupSettings)[])
      .filter((key) => JSON.stringify(current[key]) !== JSON.stringify(original[key]))
      .map((key) => [key, current[key]]),
  )
}
