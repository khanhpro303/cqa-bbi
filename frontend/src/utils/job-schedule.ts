const dayNames = ['Chủ nhật', 'Thứ 2', 'Thứ 3', 'Thứ 4', 'Thứ 5', 'Thứ 6', 'Thứ 7']

/** Formats an analysis schedule exactly as it is configured on the job. */
export function formatJobSchedule(type: string, cron: string): string {
  if (type === 'after_sync') return 'Sau mỗi lần đồng bộ'
  if (type === 'manual' || !cron) return 'Thủ công'

  const parts = cron.trim().split(/\s+/)
  if (parts.length < 5) return cron

  const [min, hour, dom, , dow] = parts
  const time = `${hour.padStart(2, '0')}:${min.padStart(2, '0')}`
  if (dow === '*' && dom === '*') return `Hàng ngày lúc ${time}`
  if (dow === '1-5' && dom === '*') return `Thứ 2-6 lúc ${time}`
  if (dow === '0-6' && dom === '*') return `Hàng ngày lúc ${time}`
  if (dow !== '*' && dom === '*') {
    const days = dow.split(',').map(d => dayNames[parseInt(d)] || d).join(', ')
    return `${days} lúc ${time}`
  }
  if (dom !== '*' && dow === '*') return `Ngày ${dom} hàng tháng lúc ${time}`
  return cron
}
