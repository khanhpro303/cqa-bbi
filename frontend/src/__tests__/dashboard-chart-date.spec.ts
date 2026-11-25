import { expect, it } from 'vitest'
import { formatDashboardDate } from '../components/dashboard/chartDate'

it.each([
  '2026-10-26',
  '2026-10-26T00:00:00+07:00',
  '2026-10-26T23:00:00-04:00',
  '2026-10-26T00:00:00Z',
  '2026-10-26 00:00:00',
])('keeps the API calendar day for %s', date => {
  expect(formatDashboardDate(date)).toBe('26/10')
  expect(formatDashboardDate(date, true)).toBe('26/10/2026')
})
