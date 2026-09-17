import { describe, expect, it } from 'vitest'
import { buildQcTimeline } from '../utils/qc-evaluation'

describe('QC evaluation timeline', () => {
  it('sorts snapshots newest first and calculates score changes from the prior run', () => {
    const timeline = buildQcTimeline([
      {
        job_run_id: 'oldest', job_name: 'QC', job_type: 'qc_analysis', evaluated_at: '2026-09-17T08:00:00Z',
        results: [{ result_type: 'conversation_evaluation', severity: 'FAIL', evidence: 'Ban đầu', detail: '{"score":70}' }],
      },
      {
        job_run_id: 'latest', job_name: 'QC', job_type: 'qc_analysis', evaluated_at: '2026-09-17T10:00:00Z',
        results: [{
          result_type: 'conversation_evaluation', severity: 'FAIL', evidence: 'Mới nhất',
          detail: '{"score":62,"source_message_count":24,"source_last_message_at":"2026-09-17T09:58:00Z","ai_provider":"openai","ai_model":"gpt-5.6","transcript_sha256":"abcdef123456"}',
        }],
      },
      {
        job_run_id: 'middle', job_name: 'QC', job_type: 'qc_analysis', evaluated_at: '2026-09-17T09:00:00Z',
        results: [
          { result_type: 'conversation_evaluation', severity: 'PASS', evidence: 'Tốt hơn', detail: '{"score":80}' },
          { result_type: 'qc_violation', severity: 'CAN_CAI_THIEN', rule_name: 'Phản hồi', evidence: 'Trả lời chậm' },
        ],
      },
    ])

    expect(timeline.map(item => item.jobRunId)).toEqual(['latest', 'middle', 'oldest'])
    expect(timeline.map(item => item.scoreDelta)).toEqual([-18, 10, null])
    expect(timeline[0]).toMatchObject({
      score: 62,
      sourceMessageCount: 24,
      sourceLastMessageAt: '2026-09-17T09:58:00Z',
      aiProvider: 'openai',
      aiModel: 'gpt-5.6',
      transcriptSHA256: 'abcdef123456',
    })
    expect(timeline[1].violations).toHaveLength(1)
  })

  it('keeps legacy snapshots usable when detail metadata is missing or invalid', () => {
    const timeline = buildQcTimeline([{
      job_run_id: 'legacy', job_name: 'QC cũ', job_type: 'qc_analysis', evaluated_at: '2026-09-16T08:00:00Z',
      results: [{ result_type: 'conversation_evaluation', severity: 'SKIP', evidence: 'Bỏ qua', detail: '{invalid' }],
    }])

    expect(timeline).toHaveLength(1)
    expect(timeline[0]).toMatchObject({ score: null, scoreDelta: null, sourceMessageCount: null, verdict: 'SKIP' })
  })
})
