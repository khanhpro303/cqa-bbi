import { beforeEach, expect, it, vi } from 'vitest'
import { createPinia, setActivePinia } from 'pinia'
import { useJobStore } from '../stores/jobs'

const mocks = vi.hoisted(() => ({ get: vi.fn() }))
vi.mock('../api', () => ({ default: { get: mocks.get } }))
beforeEach(() => { setActivePinia(createPinia()); mocks.get.mockReset() })
it('keeps the latest tenant task list when earlier requests finish late', async () => {
  let oldResponse!: (value: any) => void
  mocks.get.mockImplementationOnce(() => new Promise(resolve => { oldResponse = resolve }))
  mocks.get.mockResolvedValueOnce({ data: [{ id: 'current-task', tenant_id: 'tenant-b' }] })
  const store = useJobStore()
  const oldRequest = store.fetchJobs('tenant-a')
  await store.fetchJobs('tenant-b')
  oldResponse({ data: [{ id: 'obsolete-task', tenant_id: 'tenant-a' }] })
  await oldRequest
  expect(store.jobs).toEqual([{ id: 'current-task', tenant_id: 'tenant-b' }])
})
