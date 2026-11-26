export interface PersonalZaloGroupSettings {
  personal_zalo_group_reply_enabled: boolean
  group_bot_names: string
  handover_alert_group_id: string
  personal_zalo_handover_staff_id: string
}

export interface PersonalZaloHandoverStaff {
  id?: unknown
  channel_id?: unknown
  zalo_user_id?: unknown
  name?: unknown
  status?: unknown
  channel?: {
    channel_type?: unknown
    is_active?: unknown
  } | null
}

export interface PersonalZaloHandoverGroup {
  id?: unknown
  name?: unknown
  zalo_group_id?: unknown
  customer_code?: unknown
  customer_codes?: unknown
  customers?: unknown
  channel?: {
    channel_type?: unknown
    is_active?: unknown
    name?: unknown
  } | null
}

export function readPersonalZaloGroupSettings(metadata: Record<string, unknown> = {}): PersonalZaloGroupSettings {
  return {
    personal_zalo_group_reply_enabled: metadata.personal_zalo_group_reply_enabled === true,
    group_bot_names: typeof metadata.group_bot_names === 'string' ? metadata.group_bot_names : '',
    handover_alert_group_id: typeof metadata.handover_alert_group_id === 'string' ? metadata.handover_alert_group_id : '',
    personal_zalo_handover_staff_id: typeof metadata.personal_zalo_handover_staff_id === 'string'
      ? metadata.personal_zalo_handover_staff_id
      : '',
  }
}

export function personalZaloGroupSettingsPatch(settings: PersonalZaloGroupSettings): Record<string, unknown> {
  return {
    personal_zalo_group_reply_enabled: settings.personal_zalo_group_reply_enabled,
    group_bot_names: settings.group_bot_names.trim(),
    handover_alert_group_id: settings.handover_alert_group_id || '',
    personal_zalo_handover_staff_id: settings.handover_alert_group_id
      ? ''
      : settings.personal_zalo_handover_staff_id || '',
  }
}

export function personalZaloHandoverStaffOptions(staff: PersonalZaloHandoverStaff[], activeOAChannelIDs: string[]) {
  const activeOAIDs = new Set(activeOAChannelIDs)
  return staff
    .filter((member) => {
      if (member.status !== 'active' || !member.id || !member.zalo_user_id) return false
      const channelID = typeof member.channel_id === 'string' ? member.channel_id : ''
      if (channelID) return activeOAIDs.has(channelID) && member.channel?.channel_type === 'zalo_oa' && member.channel.is_active !== false
      return activeOAChannelIDs.length === 1
    })
    .map((member) => ({
      title: `${String(member.name || member.zalo_user_id)} · ${String(member.zalo_user_id)}`,
      value: String(member.id),
    }))
}

export function personalZaloHandoverGroupOptions(groups: PersonalZaloHandoverGroup[]) {
  return groups
    .filter((group) => {
      const hasCustomerLinks = Boolean(group.customer_code) ||
        (Array.isArray(group.customer_codes) && group.customer_codes.length > 0) ||
        (Array.isArray(group.customers) && group.customers.length > 0)
      return Boolean(group.id) && Boolean(group.zalo_group_id) && !hasCustomerLinks &&
        group.channel?.channel_type === 'zalo_oa' && group.channel.is_active !== false
    })
    .map((group) => ({
      title: typeof group.channel?.name === 'string' && group.channel.name
        ? `${String(group.name || '')} · ${group.channel.name}`
        : String(group.name || ''),
      value: String(group.id),
    }))
}
