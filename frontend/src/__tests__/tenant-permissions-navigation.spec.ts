// @vitest-environment happy-dom
import { expect, it, vi } from 'vitest'
import { createPinia, setActivePinia } from 'pinia'
import router, { clearTenantCache } from '../router'
import { useAuthStore } from '../stores/auth'

const api = vi.hoisted(() => ({ get: vi.fn() }))
vi.mock('../api', () => ({ default: api }))

it('keeps active permissions when a canceled tenant navigation finishes later', async () => {
  setActivePinia(createPinia())
  clearTenantCache()
  vi.stubGlobal('localStorage', { getItem: (key: string) => key === 'cqa_access_token' ? 'local-test-token' : null })
  const auth = useAuthStore()
  let finishB!: (value: { data: { role: string; permissions: string } }) => void
  let startB!: () => void
  const whenBStarts = new Promise<void>(resolve => { startB = resolve })
  api.get.mockImplementation(async (path: string) => {
    if (path === '/tenants/tenant-a/me') {
      return { data: { role: 'member', permissions: '{"settings":"r"}' } }
    }
    if (path === '/tenants/tenant-b/me') {
      startB()
      return new Promise(resolve => { finishB = resolve })
    }
    return { data: {} }
  })

  try {
    await router.push('/tenant-a')
    expect(auth.tenantPerms.role).toBe('member')
    const canceled = router.push('/tenant-b')
    await whenBStarts
    await router.push('/tenant-a/settings')
    finishB({ data: { role: 'owner', permissions: '{}' } })
    await canceled
    expect(router.currentRoute.value.path).toBe('/tenant-a/settings')
    expect(auth.tenantPerms.role).toBe('member')
    expect(auth.canEdit('settings')).toBe(false)
  } finally {
    vi.unstubAllGlobals()
    clearTenantCache()
  }
})
