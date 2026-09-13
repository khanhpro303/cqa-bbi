import { describe, expect, it } from 'vitest'
import {
  MESSENGER_INSIGHTS_PROFILE,
  createMessengerInsightsConfig,
  isClassificationConfigValid,
  parseClassificationConfig,
  serializeClassificationConfig,
} from '../utils/classification-config'

describe('classification config', () => {
  it('preserves legacy array format', () => {
    const raw = '[{"name":"Hỏi giá","description":"Khách hỏi giá","severity":"CAN_CAI_THIEN"}]'
    const parsed = parseClassificationConfig(raw)

    expect(parsed.profile).toBeUndefined()
    expect(parsed.rules).toHaveLength(1)
    expect(JSON.parse(serializeClassificationConfig(parsed))).toBeInstanceOf(Array)
  })

  it('preserves the Messenger profile object', () => {
    const parsed = parseClassificationConfig({
      profile: MESSENGER_INSIGHTS_PROFILE,
      rules: [{ name: 'Feedback', description: 'Khách góp ý', severity: 'CAN_CAI_THIEN' }],
    })

    expect(parsed.profile).toBe(MESSENGER_INSIGHTS_PROFILE)
    expect(JSON.parse(serializeClassificationConfig(parsed))).toEqual({
      profile: MESSENGER_INSIGHTS_PROFILE,
      rules: parsed.rules,
    })
  })

  it('creates a valid one-click template covering the three requested groups', () => {
    const config = createMessengerInsightsConfig()
    const names = config.rules.map(rule => rule.name)

    expect(config.profile).toBe(MESSENGER_INSIGHTS_PROFILE)
    expect(names.some(name => name.startsWith('Nhu cầu'))).toBe(true)
    expect(names.some(name => name.startsWith('Feedback'))).toBe(true)
    expect(names.some(name => name.startsWith('Quality mess'))).toBe(true)
    expect(isClassificationConfigValid(serializeClassificationConfig(config))).toBe(true)
  })
})
