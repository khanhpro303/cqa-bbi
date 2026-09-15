// @vitest-environment happy-dom

import { beforeEach, describe, expect, it, vi } from 'vitest'
import { createPinia, setActivePinia } from 'pinia'
import { useAuthStore } from '../stores/auth'

const apiMock = vi.hoisted(() => ({
  get: vi.fn(),
  post: vi.fn(),
}))

vi.mock('../api', () => ({ default: apiMock }))

describe('auth store login methods', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    vi.stubGlobal('localStorage', createMemoryStorage())
    vi.stubGlobal('sessionStorage', createMemoryStorage())
    apiMock.get.mockReset()
    apiMock.post.mockReset()
    apiMock.get.mockResolvedValue({
      data: { id: 'user-1', email: 'user@example.com', name: 'User', is_admin: false, language: 'vi' },
    })
  })

  it('preserves the existing email login contract', async () => {
    sessionStorage.setItem('cqa_skip_facebook_auto_login', '1')
    apiMock.post.mockResolvedValueOnce({ data: { access_token: 'email-jwt' } })
    const auth = useAuthStore()

    await auth.login('user@example.com', 'password')

    expect(apiMock.post).toHaveBeenCalledWith('/auth/login', {
      email: 'user@example.com',
      password: 'password',
    })
    expect(localStorage.getItem('cqa_access_token')).toBe('email-jwt')
    expect(sessionStorage.getItem('cqa_skip_facebook_auto_login')).toBeNull()
    expect(auth.user?.email).toBe('user@example.com')
  })

  it('exchanges a Facebook access token for the application JWT', async () => {
    sessionStorage.setItem('cqa_skip_facebook_auto_login', '1')
    apiMock.post.mockResolvedValueOnce({ data: { access_token: 'facebook-jwt' } })
    const auth = useAuthStore()

    await auth.loginWithFacebook('facebook-user-token')

    expect(apiMock.post).toHaveBeenCalledWith('/auth/facebook', {
      access_token: 'facebook-user-token',
    })
    expect(localStorage.getItem('cqa_access_token')).toBe('facebook-jwt')
    expect(sessionStorage.getItem('cqa_skip_facebook_auto_login')).toBeNull()
    expect(auth.user?.id).toBe('user-1')
  })

  it('prevents immediate Facebook auto-login after explicit logout', async () => {
    apiMock.post.mockResolvedValueOnce({ data: {} })
    const auth = useAuthStore()

    await auth.logout()

    expect(sessionStorage.getItem('cqa_skip_facebook_auto_login')).toBe('1')
  })
})

function createMemoryStorage(): Storage {
  const values = new Map<string, string>()
  return {
    get length() { return values.size },
    clear: () => values.clear(),
    getItem: (key) => values.get(key) ?? null,
    key: (index) => [...values.keys()][index] ?? null,
    removeItem: (key) => values.delete(key),
    setItem: (key, value) => values.set(key, String(value)),
  }
}
