export function formatDashboardDate(value: string, includeYear = false): string {
  const parts = /^(\d{4})-(\d{2})-(\d{2})(?=$|[T ])/.exec(value)
  if (!parts) return value

  // Keep the calendar day assigned by the API, even if the timestamp has an offset.
  const [, year, month, day] = parts
  return includeYear ? `${day}/${month}/${year}` : `${day}/${month}`
}
