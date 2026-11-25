import { createPinia, setActivePinia } from 'pinia'
import { describe, expect, it, vi } from 'vitest'
import { readZaloGroupSettings, streamKey, zaloGroupSettingsPatch } from '../components/channels/zaloGroupSettings'
import { useChannelStore, type Channel } from '../stores/channels'

const api = vi.hoisted(() => ({ put: vi.fn() }))
vi.mock('../api', () => ({ default: api }))

describe('Zalo group settings', () => {
  it('preserves other settings in the list when the dialog is reopened before the refresh', async () => {
    setActivePinia(createPinia())
    const store = useChannelStore()
    const original = {
      session_keyword: 'Chào Bull',
      handover_alert_group_id: 'handover-group',
      chat_memory_issue_alerts: false,
    }
    store.channels = [{ id: 'oa', channel_type: 'zalo_oa', metadata: JSON.stringify(original) } as Channel]
    api.put.mockResolvedValueOnce({})

    const settings = readZaloGroupSettings(original)
    settings.group_assistant_mode = true
    const metadata = JSON.stringify(zaloGroupSettingsPatch(settings, original))
    await store.updateChannel('tenant', 'oa', { metadata })

    expect(api.put).toHaveBeenCalledWith('/tenants/tenant/channels/oa', {
      metadata: '{"group_assistant_mode":true}',
    })
    expect(JSON.parse(store.channels[0]!.metadata)).toEqual({ ...original, group_assistant_mode: true })
  })

  it('keeps the source metadata intact while editing notification outputs and clears them when disabled', () => {
    const original = {
      campaign_alerts_enabled: true,
      campaign_alert_group_id: 'campaign-group',
      campaign_alert_outputs: [{ type: 'email', to: 'old@example.com' }],
    }
    const settings = readZaloGroupSettings(original)
    settings.campaign_alert_outputs[0]!.to = 'new@example.com'
    expect(zaloGroupSettingsPatch(settings, original)).toEqual({
      campaign_alert_outputs: [{ type: 'email', to: 'new@example.com' }],
    })
    expect(original.campaign_alert_outputs[0]!.to).toBe('old@example.com')

    settings.campaign_alerts_enabled = false
    expect(zaloGroupSettingsPatch(settings, original)).toEqual({
      campaign_alerts_enabled: false,
      campaign_alert_group_id: '',
      campaign_alert_outputs: [],
    })
  })

  it('treats a missing staff switch as on and only saves staff settings the user changed', () => {
    const original = { session_keyword: 'Chào Bull' }
    const settings = readZaloGroupSettings(original)
    expect(settings.group_staff_call_only).toBe(true)
    expect(settings.group_bot_names).toBe('')
    expect(zaloGroupSettingsPatch(settings, original)).toEqual({})

    settings.group_bot_names = 'Bull;bot'
    settings.group_staff_call_only = false
    expect(zaloGroupSettingsPatch(settings, original)).toEqual({
      group_staff_call_only: false,
      group_bot_names: 'Bull;bot',
    })
  })
})

describe('complaint cases switch', () => {
  it('is off until turned on', () => {
    expect(readZaloGroupSettings({}).complaint_cases).toBe(false)
    expect(readZaloGroupSettings({ complaint_cases: true }).complaint_cases).toBe(true)
  })
})

describe('unanswered business questions', () => {
  it('enables the staff fallback without choosing another alert group', () => {
    const original = { handover_alert_group_id: 'complaints', campaign_alerts_enabled: true, campaign_alert_group_id: 'campaign' }
    const settings = readZaloGroupSettings(original)
    expect(settings.unanswered_handover_enabled).toBe(false)
    expect(settings.unanswered_handover_group_id).toBe('')

    settings.unanswered_handover_enabled = true
    expect(zaloGroupSettingsPatch(settings, original)).toEqual({ unanswered_handover_enabled: true })

    settings.unanswered_handover_group_id = 'management'
    expect(zaloGroupSettingsPatch(settings, original)).toEqual({
      unanswered_handover_enabled: true,
      unanswered_handover_group_id: 'management',
    })
  })

  it('keeps the selected group when disabled and allows clearing it separately', () => {
    const original = { unanswered_handover_enabled: true, unanswered_handover_group_id: 'management' }
    const settings = readZaloGroupSettings(original)
    settings.unanswered_handover_enabled = false
    expect(zaloGroupSettingsPatch(settings, original)).toEqual({ unanswered_handover_enabled: false })
    settings.unanswered_handover_group_id = ''
    expect(zaloGroupSettingsPatch(settings, original)).toEqual({
      unanswered_handover_enabled: false,
      unanswered_handover_group_id: '',
    })
  })
})

describe('complaint days and streams', () => {
  it('reads defaults and clamps days', () => {
    expect(readZaloGroupSettings({}).complaint_resolved_days).toBe(14)
    expect(readZaloGroupSettings({ complaint_resolved_days: 30 }).complaint_resolved_days).toBe(30)
    expect(readZaloGroupSettings({ complaint_resolved_days: 500 }).complaint_resolved_days).toBe(90)
    expect(readZaloGroupSettings({}).complaint_streams).toEqual([])
  })

  it('keys new streams from their name and keeps saved keys', () => {
    const original = { complaint_streams: [{ key: 'bao_hanh', name: 'Bảo hành', alert_group_id: 'g1' }] }
    const settings = readZaloGroupSettings(original)
    settings.complaint_streams[0].name = 'Bảo hành, đổi trả'
    settings.complaint_streams.push({ key: '', name: 'Đội Marketing', description: ' Khuyến mãi ', alert_group_id: '' })
    settings.complaint_streams.push({ key: '', name: '  ', description: '', alert_group_id: '' })
    settings.complaint_resolved_days = 7
    expect(zaloGroupSettingsPatch(settings, original)).toEqual({
      complaint_resolved_days: 7,
      complaint_streams: [
        { key: 'bao_hanh', name: 'Bảo hành, đổi trả', description: '', alert_group_id: 'g1' },
        { key: 'doi_marketing', name: 'Đội Marketing', description: 'Khuyến mãi', alert_group_id: '' },
      ],
    })
  })

  it('makes keys unique', () => {
    expect(streamKey('Bảo hành', new Set(['bao_hanh']))).toBe('bao_hanh_2')
    expect(streamKey('!!!', new Set())).toBe('luong')
  })

  it('sends nothing when streams are unchanged', () => {
    const original = { complaint_streams: [{ key: 'sp', name: 'Sản phẩm', description: '', alert_group_id: '' }] }
    expect(zaloGroupSettingsPatch(readZaloGroupSettings(original), original)).toEqual({})
  })
})
