// Types for the CRM Campaign (Chiến dịch CRM) feature.
// These mirror the backend payloads served from /tenants/:tenantId/crm/campaigns…
// (see backend/api/handlers/crm_campaigns.go). The real API lives in campaignsApi.ts.

// Zalo text payload limit (mirror of backend/channels/zalo_oa.go zaloMaxTextRunes).
// Used for the live character counter + "sẽ bị chia nhiều tin" warning.
export const ZALO_MAX_TEXT_RUNES = 1800

export type ScheduleKind = 'recurring' | 'once'
export type CampaignStatus = 'draft' | 'active' | 'paused' | 'done'

// One "lượt gửi": a GMF group paired with its own schedule.
// A campaign holds many of these (đa phân khúc).
export interface CampaignSegment {
  id: string
  groupId: string // -> CRMGroup.id (nhóm GMF)
  groupName: string // hiển thị, denormalised for the table
  scheduleKind: ScheduleKind
  cron?: string // khi recurring — cron string từ CronPicker
  runAt?: string // ISO datetime — khi once
  nextRunAt?: string // tính sẵn ở mock để hiển thị "lượt gửi kế tiếp"
}

export interface CampaignMessage {
  text: string
  link?: string
  // Mock chỉ giữ tên file. Gửi ảnh thật cần endpoint attachment Zalo (phase sau).
  imageName?: string
}

export interface Campaign {
  id: string
  name: string
  description?: string
  channelId: string // Zalo OA channel
  status: CampaignStatus
  message: CampaignMessage
  segments: CampaignSegment[]
  sentThisMonth: number
  createdAt: string
}

export interface CampaignRun {
  id: string
  campaignId: string
  segmentId: string
  startedAt: string
  sentCount: number
  failCount: number
  status: 'running' | 'success' | 'error'
}

export interface CampaignStats {
  campaignsThisMonth: number
  messagesSentThisMonth: number
  successRate: number // 0..100
  upcomingRuns: number
  byDay: { date: string; sent: number }[]
  recent: CampaignRecentRow[]
}

export interface CampaignRecentRow {
  id: string
  name: string
  status: CampaignStatus
  sent: number
  fail: number
  lastRunAt?: string
}

// Lightweight option used by the group <v-select> and OA <v-select>.
export interface SelectOption {
  id: string
  name: string
}

// Form shape used by CampaignFormDialog before it becomes a Campaign.
export interface CampaignFormState {
  id?: string
  name: string
  description: string
  channelId: string
  message: CampaignMessage
  segments: CampaignSegment[]
}
