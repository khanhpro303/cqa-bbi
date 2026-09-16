export function emptyAnalysisRunReason(status: string, rawSummary: string): string {
  if (status !== 'success') return ''
  let summary: Record<string, unknown>
  try { summary = JSON.parse(rawSummary) } catch { return '' }
  if (!summary || summary.conversations_analyzed !== 0 || Number(summary.conversations_errors || 0) > 0) return ''
  if (summary.empty_reason === 'no_matching_conversations' || summary.conversations_found === 0) {
    return 'Không có hội thoại phù hợp với phạm vi chạy. Hội thoại đã phân tích có thể bị loại khi chạy thử hoặc chọn chưa phân tích.'
  }
  if (summary.empty_reason === 'no_messages') return 'Có hội thoại nhưng không có tin nhắn trong phạm vi chạy để phân tích.'
  return 'Lần chạy này chưa phân tích được hội thoại nào nên không có kết quả.'
}
