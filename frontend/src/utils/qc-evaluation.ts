export interface QcEvaluationResult {
  result_type?: string
  severity?: string
  rule_name?: string
  evidence?: string
  detail?: string | Record<string, unknown> | null
}

export interface QcEvaluationGroup {
  job_run_id: string
  job_name?: string
  job_type?: string
  evaluated_at?: string
  results?: QcEvaluationResult[]
}

export interface QcSnapshot {
  jobRunId: string
  jobName: string
  evaluatedAt: string
  verdict: string
  score: number | null
  scoreDelta: number | null
  review: string
  violations: QcEvaluationResult[]
  sourceMessageCount: number | null
  sourceLastMessageAt: string
  transcriptSHA256: string
  promptSHA256: string
  aiProvider: string
  aiModel: string
}

function parseDetail(detail: QcEvaluationResult['detail']): Record<string, unknown> {
  if (!detail) return {}
  if (typeof detail === 'object') return detail
  try {
    const parsed = JSON.parse(detail)
    return parsed && typeof parsed === 'object' ? parsed : {}
  } catch {
    return {}
  }
}

function finiteNumber(value: unknown): number | null {
  return typeof value === 'number' && Number.isFinite(value) ? value : null
}

function stringValue(value: unknown): string {
  return typeof value === 'string' ? value : ''
}

export function buildQcTimeline(groups: QcEvaluationGroup[]): QcSnapshot[] {
  const snapshots = groups
    .filter(group => group.job_type === 'qc_analysis')
    .map((group): QcSnapshot => {
      const results = Array.isArray(group.results) ? group.results : []
      const evaluation = results.find(result => result.result_type === 'conversation_evaluation')
      const detail = parseDetail(evaluation?.detail)
      const messageCount = finiteNumber(detail.source_message_count)
      return {
        jobRunId: group.job_run_id,
        jobName: group.job_name || 'Đánh giá chất lượng',
        evaluatedAt: group.evaluated_at || '',
        verdict: evaluation?.severity || '',
        score: finiteNumber(detail.score),
        scoreDelta: null,
        review: evaluation?.evidence || stringValue(detail.review),
        violations: results.filter(result => result.result_type === 'qc_violation'),
        sourceMessageCount: messageCount == null ? null : Math.max(0, Math.trunc(messageCount)),
        sourceLastMessageAt: stringValue(detail.source_last_message_at),
        transcriptSHA256: stringValue(detail.transcript_sha256),
        promptSHA256: stringValue(detail.prompt_sha256),
        aiProvider: stringValue(detail.ai_provider),
        aiModel: stringValue(detail.ai_model),
      }
    })
    .sort((left, right) => Date.parse(right.evaluatedAt) - Date.parse(left.evaluatedAt))

  for (let index = 0; index < snapshots.length - 1; index += 1) {
    const current = snapshots[index]
    const previous = snapshots[index + 1]
    if (current.score != null && previous.score != null) {
      current.scoreDelta = current.score - previous.score
    }
  }
  return snapshots
}
