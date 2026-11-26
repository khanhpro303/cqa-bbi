import { readJsonStorage, readLocalStorage, writeJsonStorage, writeLocalStorage } from '../../utils/storage'

export interface CachedDemoStatus {
  has_data: boolean
  is_demo: boolean
}

const bannerOffsetsKey = (tenantId: string) => `cqa_banner_offsets:${tenantId}`
const demoStatusKey = (tenantId: string) => `cqa_demo_status:${tenantId}`

/**
 * Returns undefined when the tenant's banner offsets have never been cached locally,
 * or the cached offsets string (empty string means server returned default/unset).
 */
export function readCachedBannerOffsets(tenantId: string): string | undefined {
  if (!tenantId) return undefined
  const cached = readLocalStorage(bannerOffsetsKey(tenantId))
  return cached === null ? undefined : cached
}

export function writeCachedBannerOffsets(tenantId: string, offsets: string | null | undefined): void {
  if (!tenantId) return
  writeLocalStorage(bannerOffsetsKey(tenantId), offsets ?? '')
}

export function readCachedDemoStatus(tenantId: string): CachedDemoStatus | null {
  if (!tenantId) return null
  const cached = readJsonStorage<Partial<CachedDemoStatus>>(demoStatusKey(tenantId))
  if (!cached || typeof cached.is_demo !== 'boolean') return null
  return { has_data: Boolean(cached.has_data), is_demo: cached.is_demo }
}

export function writeCachedDemoStatus(tenantId: string, status: CachedDemoStatus): void {
  if (!tenantId) return
  writeJsonStorage(demoStatusKey(tenantId), {
    has_data: Boolean(status.has_data),
    is_demo: Boolean(status.is_demo),
  })
}
