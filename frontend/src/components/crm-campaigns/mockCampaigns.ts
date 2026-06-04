// Mock data layer for the CRM Campaign feature (frontend-first, no backend yet).
// Each exported function returns a Promise with a small delay so the UI exercises
// its loading states. When the backend is ready (see docs/api/crm-campaigns.md),
// replace the bodies with `api.get/post` calls — the signatures stay the same.

import type {
  Campaign,
  CampaignFormState,
  CampaignStats,
  SelectOption,
} from './types'

const NETWORK_DELAY_MS = 300

function delay<T>(value: T, ms = NETWORK_DELAY_MS): Promise<T> {
  return new Promise((resolve) => setTimeout(() => resolve(value), ms))
}

function uid(prefix: string): string {
  return `${prefix}_${Math.random().toString(36).slice(2, 9)}`
}

// --- Mock reference data (would come from /crm/groups and Zalo OA channels) ---

export const MOCK_GMF_GROUPS: SelectOption[] = [
  { id: 'grp_a', name: 'GMF · Khách VIP Miền Nam' },
  { id: 'grp_b', name: 'GMF · Đại lý Hà Nội' },
  { id: 'grp_c', name: 'GMF · Khách mua sỉ' },
  { id: 'grp_d', name: 'GMF · Khách mới 30 ngày' },
]

export const MOCK_OA_CHANNELS: SelectOption[] = [
  { id: 'oa_bbi', name: 'Bulldog Helmets VietNam (OA)' },
]

// --- In-memory campaign store ---

const today = new Date()
function isoDaysFromNow(days: number, hour = 9, minute = 0): string {
  const d = new Date(today)
  d.setDate(d.getDate() + days)
  d.setHours(hour, minute, 0, 0)
  return d.toISOString()
}

let campaigns: Campaign[] = [
  {
    id: 'cmp_flash66',
    name: 'Flash Sale 6/6',
    description: 'Ưu đãi 6.6 cho nón fullface',
    channelId: 'oa_bbi',
    status: 'active',
    message: {
      text: 'BBI FLASH SALE 6.6 🔥 Giảm đến 40% toàn bộ nón fullface. Xem ngay:',
      link: 'https://bulldoghelmets.vn/flash-sale',
      imageName: 'flash-sale-66.jpg',
    },
    segments: [
      {
        id: uid('seg'),
        groupId: 'grp_a',
        groupName: 'GMF · Khách VIP Miền Nam',
        scheduleKind: 'recurring',
        cron: '0 9 * * *',
        nextRunAt: isoDaysFromNow(1, 9),
      },
      {
        id: uid('seg'),
        groupId: 'grp_b',
        groupName: 'GMF · Đại lý Hà Nội',
        scheduleKind: 'recurring',
        cron: '0 14 * * 1,3,5',
        nextRunAt: isoDaysFromNow(2, 14),
      },
      {
        id: uid('seg'),
        groupId: 'grp_c',
        groupName: 'GMF · Khách mua sỉ',
        scheduleKind: 'once',
        runAt: isoDaysFromNow(3, 20),
        nextRunAt: isoDaysFromNow(3, 20),
      },
    ],
    sentThisMonth: 1240,
    createdAt: isoDaysFromNow(-5, 10),
  },
  {
    id: 'cmp_triangt6',
    name: 'Tri ân khách hàng tháng 6',
    description: '',
    channelId: 'oa_bbi',
    status: 'draft',
    message: {
      text: 'Cảm ơn quý khách đã đồng hành cùng BBI 💙 Nhận voucher tri ân tại link bên dưới.',
      link: 'https://bulldoghelmets.vn/tri-an',
    },
    segments: [
      {
        id: uid('seg'),
        groupId: 'grp_d',
        groupName: 'GMF · Khách mới 30 ngày',
        scheduleKind: 'recurring',
        cron: '0 8 1 * *',
      },
    ],
    sentThisMonth: 0,
    createdAt: isoDaysFromNow(-1, 16),
  },
]

// --- Simulated API ---

export function listCampaigns(): Promise<Campaign[]> {
  return delay(campaigns.map((c) => ({ ...c })))
}

export function saveCampaign(form: CampaignFormState): Promise<Campaign> {
  const segments = form.segments.map((s) => ({
    ...s,
    nextRunAt: s.scheduleKind === 'once' ? s.runAt : isoDaysFromNow(1),
  }))

  if (form.id) {
    const idx = campaigns.findIndex((c) => c.id === form.id)
    if (idx >= 0) {
      const updated: Campaign = {
        ...campaigns[idx],
        name: form.name,
        description: form.description,
        channelId: form.channelId,
        message: { ...form.message },
        segments,
      }
      campaigns = campaigns.map((c, i) => (i === idx ? updated : c))
      return delay({ ...updated })
    }
  }

  const created: Campaign = {
    id: uid('cmp'),
    name: form.name,
    description: form.description,
    channelId: form.channelId,
    status: 'draft',
    message: { ...form.message },
    segments,
    sentThisMonth: 0,
    createdAt: new Date().toISOString(),
  }
  campaigns = [created, ...campaigns]
  return delay({ ...created })
}

export function setCampaignStatus(
  id: string,
  status: Campaign['status'],
): Promise<Campaign | undefined> {
  campaigns = campaigns.map((c) => (c.id === id ? { ...c, status } : c))
  return delay(campaigns.find((c) => c.id === id))
}

export function deleteCampaign(id: string): Promise<void> {
  campaigns = campaigns.filter((c) => c.id !== id)
  return delay(undefined)
}

export function sendNow(id: string): Promise<{ sent: number }> {
  // Simulate a broadcast: bump the monthly counter.
  let sent = 0
  campaigns = campaigns.map((c) => {
    if (c.id !== id) return c
    sent = c.segments.length * 120
    return { ...c, status: 'active', sentThisMonth: c.sentThisMonth + sent }
  })
  return delay({ sent })
}

export function getStats(month: string): Promise<CampaignStats> {
  // `month` = 'YYYY-MM'; mock ignores the value beyond realistic numbers.
  void month
  const byDay = Array.from({ length: 14 }, (_, i) => ({
    date: isoDaysFromNow(-13 + i).split('T')[0],
    sent: Math.round(200 + Math.random() * 900),
  }))
  const messagesSentThisMonth = byDay.reduce((sum, d) => sum + d.sent, 0)
  return delay({
    campaignsThisMonth: campaigns.length,
    messagesSentThisMonth,
    successRate: 98.2,
    upcomingRuns: campaigns
      .filter((c) => c.status === 'active')
      .reduce((sum, c) => sum + c.segments.length, 0),
    byDay,
    recent: campaigns.slice(0, 5).map((c) => ({
      id: c.id,
      name: c.name,
      status: c.status,
      sent: c.sentThisMonth,
      fail: c.status === 'active' ? Math.round(c.sentThisMonth * 0.018) : 0,
      lastRunAt: c.status === 'active' ? isoDaysFromNow(0, 9) : undefined,
    })),
  })
}
