import { describe, expect, it } from 'vitest'
import {
  personalZaloGroupSettingsPatch,
  personalZaloHandoverGroupOptions,
  personalZaloHandoverStaffOptions,
  readComplaintGroupCustomers,
  readPersonalZaloGroupSettings,
} from '../components/channels/personalZaloGroupSettings'

describe('personal Zalo group settings', () => {
  it('defaults all personal group modes off and keeps configured modes, names and handover group', () => {
    expect(readPersonalZaloGroupSettings()).toEqual({
      personal_zalo_group_reply_enabled: false,
      group_assistant_mode: false,
      group_staff_call_only: false,
      group_bot_names: '',
      handover_alert_group_id: '',
      personal_zalo_handover_staff_id: '',
      complaint_cases: false,
      complaint_resolved_days: 14,
      complaint_streams: [],
      complaint_group_customers: {},
    })

    const settings = readPersonalZaloGroupSettings({
      personal_zalo_group_reply_enabled: true,
      group_assistant_mode: true,
      group_staff_call_only: true,
      group_bot_names: ' Bull;bot ',
      handover_alert_group_id: 'staff-group',
      personal_zalo_handover_staff_id: 'staff-person',
      complaint_cases: true,
      complaint_resolved_days: 30,
      complaint_streams: [{ key: '', name: ' Bảo hành ', description: ' Hàng lỗi ', alert_group_id: 'warranty-group' }],
      complaint_group_customers: { ' conversation-1 ': ' KH001 ' },
    })
    expect(personalZaloGroupSettingsPatch(settings)).toEqual({
      personal_zalo_group_reply_enabled: true,
      group_assistant_mode: true,
      group_staff_call_only: true,
      group_bot_names: 'Bull;bot',
      handover_alert_group_id: 'staff-group',
      personal_zalo_handover_staff_id: '',
      complaint_cases: true,
      complaint_resolved_days: 30,
      complaint_streams: [{ key: 'bao_hanh', name: 'Bảo hành', description: 'Hàng lỗi', alert_group_id: 'warranty-group' }],
      complaint_group_customers: { 'conversation-1': 'KH001' },
    })
  })

  it('keeps only valid source group to ERP customer mappings', () => {
    expect(readComplaintGroupCustomers({
      ' group-1 ': ' KH001 ',
      'group-2': '',
      '': 'KH003',
      'group-4': 42,
    })).toEqual({ 'group-1': 'KH001' })
  })

  it('keeps a selected group with an empty ERP code in the patch for server validation', () => {
    const settings = readPersonalZaloGroupSettings()
    settings.complaint_group_customers = { 'group-1': ' ' }

    expect(personalZaloGroupSettingsPatch(settings).complaint_group_customers).toEqual({ 'group-1': '' })
  })

  it('preserves the staff-only preference while assistant mode is off', () => {
    const settings = readPersonalZaloGroupSettings({
      group_assistant_mode: false,
      group_staff_call_only: true,
    })

    expect(personalZaloGroupSettingsPatch(settings)).toMatchObject({
      group_assistant_mode: false,
      group_staff_call_only: true,
    })
  })

  it('offers only active internal groups linked to a Zalo OA', () => {
    const options = personalZaloHandoverGroupOptions([
      { id: 'staff', name: 'Điều phối', zalo_group_id: '100', channel: { channel_type: 'zalo_oa', is_active: true, name: 'BBI OA' } },
      { id: 'customer', name: 'Khách A', zalo_group_id: '101', customer_codes: ['A'], channel: { channel_type: 'zalo_oa', is_active: true } },
      { id: 'personal', name: 'Cá nhân', zalo_group_id: '102', channel: { channel_type: 'personal_zalo_import', is_active: true } },
      { id: 'inactive', name: 'OA tắt', zalo_group_id: '103', channel: { channel_type: 'zalo_oa', is_active: false } },
      { id: 'unlinked', name: 'Chưa liên kết', channel: { channel_type: 'zalo_oa', is_active: true } },
    ])

    expect(options).toEqual([{ title: 'Điều phối · BBI OA', value: 'staff' }])
  })

  it('offers active whitelist staff tied to an active OA', () => {
    expect(personalZaloHandoverStaffOptions([
      { id: 's1', name: 'An', zalo_user_id: '123', status: 'active', channel_id: 'oa1', channel: { channel_type: 'zalo_oa', is_active: true } },
      { id: 's2', name: 'Bình', zalo_user_id: '456', status: 'pending', channel_id: 'oa1', channel: { channel_type: 'zalo_oa', is_active: true } },
      { id: 's3', name: 'Chi', zalo_user_id: '789', status: 'active', channel_id: 'oa2', channel: { channel_type: 'zalo_oa', is_active: true } },
      { id: 'global', name: 'Dũng', zalo_user_id: '999', status: 'active', channel_id: '' },
    ], ['oa1'])).toEqual([
      { title: 'An · 123', value: 's1' },
      { title: 'Dũng · 999', value: 'global' },
    ])
  })
})
