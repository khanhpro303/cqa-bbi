import { createPinia, setActivePinia } from 'pinia'
import { describe, expect, it, vi } from 'vitest'
import { readZaloGroupSettings, zaloGroupSettingsPatch } from '../components/channels/zaloGroupSettings'
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
