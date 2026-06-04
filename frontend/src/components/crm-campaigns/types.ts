// Types for the CRM Campaign (Chiến dịch CRM) feature.
// These mirror the backend payloads served from /tenants/:tenantId/crm/campaigns…
// (see backend/api/handlers/crm_campaigns.go). The real API lives in campaignsApi.ts.

// Zalo text payload limit (mirror of backend/channels/zalo_oa.go zaloMaxTextRunes).
// Used for the live character counter + "sẽ bị chia nhiều tin" warning.
export const ZALO_MAX_TEXT_RUNES = 1800

export type ScheduleKind = 'recurring' | 'once'
export type CampaignStatus = 'draft' | 'active' | 'paused' | 'done'

// Per-segment tag mode (WHO gets @mentioned in the GMF group message).
export type MentionMode = 'none' | 'all' | 'selected'
// Message-level tag placement (WHERE the mentions land in the sent text).
export type MentionPlacement = 'prefix' | 'inline'

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
  // Tag thành viên trong đúng nhóm GMF này. zaloUserId gắn theo nhóm nên lựa chọn
  // nằm ở segment (mỗi segment = 1 nhóm).
  mentionMode?: MentionMode // mặc định 'none'
  mentionUserIds?: string[] // các zaloUserId khi mode = 'selected'
}

export interface CampaignMessage {
  text: string
  link?: string
  // Tenant-relative paths of uploaded images (served via /api/v1/files/<path>).
  // Populated after upload; broadcast attaches them in order via Zalo.
  images?: string[]
  // Optional text-only "nhắc lại" message. When set, each segment's first
  // successful send uses `text`; every later (recurring) send uses this instead.
  reminderText?: string
  // Vị trí chèn @mention: 'prefix' = dòng chào đầu tin; 'inline' = thay token {tag}.
  mentionPlacement?: MentionPlacement // mặc định 'prefix'
  // Lời chào dùng cho placement 'prefix' (vd "Xin chào").
  mentionGreeting?: string
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

// One taggable member of a GMF group, resolved live from Zalo + CRM names.
// Backend: GET /tenants/:tenantId/crm/groups/:id/members.
export interface GroupMember {
  zaloUserId: string
  name: string
  avatar?: string
  isEmployee?: boolean
  customerCode?: string
}

export interface GroupMembersResponse {
  employees: GroupMember[]
  customers: GroupMember[]
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
