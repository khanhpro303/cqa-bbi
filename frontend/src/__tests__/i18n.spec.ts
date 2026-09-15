import { describe, it, expect } from 'vitest'
import vi from '../i18n/vi'
import en from '../i18n/en'

function flatten(messages: Record<string, unknown>, prefix = ''): Record<string, string> {
  return Object.fromEntries(Object.entries(messages).flatMap(([key, value]) => {
    const path = prefix ? `${prefix}.${key}` : key
    return typeof value === 'string' ? [[path, value]] : Object.entries(flatten(value as Record<string, unknown>, path))
  }))
}
const viFlat = flatten(vi)
const enFlat = flatten(en)

describe('i18n completeness', () => {
  const viKeys = Object.keys(viFlat).sort()
  const enKeys = Object.keys(enFlat).sort()

  it('should have the same number of keys in vi and en', () => {
    expect(viKeys.length).toBe(enKeys.length)
  })

  it('all vi keys should exist in en', () => {
    const missingInEn = viKeys.filter((key) => !enKeys.includes(key))
    expect(missingInEn).toEqual([])
  })

  it('all en keys should exist in vi', () => {
    const missingInVi = enKeys.filter((key) => !viKeys.includes(key))
    expect(missingInVi).toEqual([])
  })

  it('no empty values in vi', () => {
    const emptyVi = viKeys.filter((key) => !viFlat[key])
    expect(emptyVi).toEqual([])
  })

  it('no empty values in en', () => {
    const emptyEn = enKeys.filter((key) => !enFlat[key])
    expect(emptyEn).toEqual([])
  })
})
