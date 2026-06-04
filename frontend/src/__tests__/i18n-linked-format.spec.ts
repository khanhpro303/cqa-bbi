import { describe, it, expect } from 'vitest'
import { baseCompile } from '@intlify/message-compiler'
import vi from '../i18n/vi'
import en from '../i18n/en'

// vue-i18n treats '@' as "linked message" syntax (@:key, @.mod:key). An unescaped
// literal '@' in a message throws "Invalid linked format" when the production
// runtime compiles it at render time (parser onError throws). This blanked the
// campaign "Nội dung tin nhắn" step: MessageComposer renders t('campaign_mention_sample'),
// whose value was '@Thành viên'. Literal '@' must be escaped as {'@'}.
//
// NOTE: vue-i18n's full (dev) build is lenient and compiles lazily, so a plain
// t() call does NOT reproduce this. We compile with an error-throwing handler,
// which mirrors the strict production runtime path.
function assertCompiles(messages: Record<string, string>, locale: string): void {
  for (const [key, value] of Object.entries(messages)) {
    if (typeof value !== 'string') continue
    expect(
      () => baseCompile(value, { onError: (e) => { throw e } }),
      `[${locale}] key "${key}" = ${JSON.stringify(value)} fails to compile`,
    ).not.toThrow()
  }
}

describe('i18n messages compile under the strict (production) vue-i18n compiler', () => {
  it('every vi message compiles', () => assertCompiles(vi as Record<string, string>, 'vi'))
  it('every en message compiles', () => assertCompiles(en as Record<string, string>, 'en'))
})
