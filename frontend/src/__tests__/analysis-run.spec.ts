import { describe, expect, it } from 'vitest'
import { emptyAnalysisRunReason } from '../utils/analysis-run'

describe('empty analysis run explanation', () => {
  it('distinguishes no eligible conversations from no messages', () => {
    expect(emptyAnalysisRunReason('success', JSON.stringify({conversations_analyzed:0, conversations_found:0}))).toContain('Không có hội thoại phù hợp')
    expect(emptyAnalysisRunReason('success', JSON.stringify({conversations_analyzed:0, conversations_found:2, empty_reason:'no_messages'}))).toContain('không có tin nhắn')
  })
  it('does not turn errors, active jobs, or non-analysis jobs into empty successes', () => {
    for (const status of ['error','running']) expect(emptyAnalysisRunReason(status, '{"conversations_analyzed":0}')).toBe('')
    for (const summary of ['{}', 'null', 'invalid', '{"conversations_analyzed":1}', '{"conversations_analyzed":0,"conversations_errors":1}']) expect(emptyAnalysisRunReason('success',summary)).toBe('')
  })
  it('explains old zero-count runs without claiming they passed', () => {
    expect(emptyAnalysisRunReason('success','{"conversations_analyzed":0}')).toContain('chưa phân tích được')
  })
})
