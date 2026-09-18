import { describe, expect, it } from 'vitest'
import { formatJobSchedule } from '../utils/job-schedule'

describe('formatJobSchedule', () => {
  it('renders after-sync jobs from their schedule type, ignoring a retained cron value', () => {
    expect(formatJobSchedule('after_sync', '0 7 * * *')).toBe('Sau mỗi lần đồng bộ')
  })

  it('keeps cron and manual schedules distinct', () => {
    expect(formatJobSchedule('cron', '0 7 * * *')).toBe('Hàng ngày lúc 07:00')
    expect(formatJobSchedule('manual', '0 7 * * *')).toBe('Thủ công')
  })
})
