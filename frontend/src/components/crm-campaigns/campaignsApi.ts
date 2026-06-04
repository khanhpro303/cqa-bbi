// Real API layer for the CRM Campaign feature (Chiến dịch CRM).
// Replaces the former in-memory mock — function signatures are unchanged so the
// components keep working. Backend: /tenants/:tenantId/crm/campaigns…
import api from '../../api'
import router from '../../router'
import type {
  Campaign,
  CampaignFormState,
  CampaignStats,
  GroupMember,
  GroupMembersResponse,
} from './types'

function tenantId(): string {
  return router.currentRoute.value.params.tenantId as string
}

function base(): string {
  return `/tenants/${tenantId()}/crm/campaigns`
}

// Shape returned by the backend (snake_case) before we normalise it.
interface RawGroupMember {
  zalo_user_id?: string
  name?: string
  avatar?: string
  is_employee?: boolean
  customer_code?: string
}

function toGroupMember(m: RawGroupMember): GroupMember {
  return {
    zaloUserId: m.zalo_user_id ?? '',
    name: m.name ?? '',
    avatar: m.avatar,
    isEmployee: m.is_employee,
    customerCode: m.customer_code,
  }
}

// listGroupMembers fetches the live GMF group members (employees + customers),
// resolved against CRM names, for the mention picker. Reuses the existing
// CRM groups members endpoint. Members without a Zalo user ID can't be tagged
// and are dropped.
export async function listGroupMembers(groupId: string): Promise<GroupMembersResponse> {
  const { data } = await api.get<{ employees?: RawGroupMember[]; customers?: RawGroupMember[] }>(
    `/tenants/${tenantId()}/crm/groups/${groupId}/members`,
  )
  const keep = (m: GroupMember) => !!m.zaloUserId
  return {
    employees: (data.employees ?? []).map(toGroupMember).filter(keep),
    customers: (data.customers ?? []).map(toGroupMember).filter(keep),
  }
}

export async function listCampaigns(): Promise<Campaign[]> {
  const { data } = await api.get<Campaign[]>(base())
  return data ?? []
}

// uploadCampaignImages sends the picked image files (multipart field "images")
// and returns their stored tenant-relative paths, in input order.
export async function uploadCampaignImages(files: File[]): Promise<string[]> {
  if (files.length === 0) return []
  const formData = new FormData()
  for (const file of files) formData.append('images', file)
  const { data } = await api.post<{ images: string[] }>(`${base()}/upload`, formData, {
    headers: { 'Content-Type': 'multipart/form-data' },
  })
  return data.images ?? []
}

export async function saveCampaign(form: CampaignFormState): Promise<Campaign> {
  if (form.id) {
    const { data } = await api.put<Campaign>(`${base()}/${form.id}`, form)
    return data
  }
  const { data } = await api.post<Campaign>(base(), form)
  return data
}

export async function setCampaignStatus(
  id: string,
  status: Campaign['status'],
): Promise<Campaign | undefined> {
  const { data } = await api.post<Campaign>(`${base()}/${id}/status`, { status })
  return data
}

export async function deleteCampaign(id: string): Promise<void> {
  await api.delete(`${base()}/${id}`)
}

export async function sendNow(id: string): Promise<{ sent: number }> {
  const { data } = await api.post<{ sent: number; fail: number }>(`${base()}/${id}/send-now`)
  return { sent: data.sent }
}

export async function getStats(
  dateFrom: string,
  dateTo: string,
  campaignId?: string,
): Promise<CampaignStats> {
  const params: Record<string, string> = { from: dateFrom, to: dateTo }
  if (campaignId) params.campaignId = campaignId
  const { data } = await api.get<CampaignStats>(`${base()}/stats`, { params })
  return data
}
