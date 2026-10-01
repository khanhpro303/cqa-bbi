// @vitest-environment happy-dom

import { beforeEach, describe, expect, it, vi } from 'vitest'
import { createPinia, setActivePinia } from 'pinia'
import { useAuthStore } from '../stores/auth'

const apiMock = vi.hoisted(() => ({
  get: vi.fn(),
  post: vi.fn(),
  refresh: vi.fn(),
}))

vi.mock('../api', () => ({ default: apiMock }))
vi.mock('axios', () => ({ default: { post: apiMock.refresh } }))

describe('auth store login methods', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    vi.stubGlobal('localStorage', createMemoryStorage())
    vi.stubGlobal('sessionStorage', createMemoryStorage())
    apiMock.get.mockReset()
    apiMock.post.mockReset()
    apiMock.refresh.mockReset()
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
  it('accepts server OAuth using only the HttpOnly refresh cookie then loads the profile', async () => {
    apiMock.refresh.mockResolvedValue({ data: { access_token: 'redirect-jwt' } })
    const auth = useAuthStore()
    await auth.completeFacebookRedirect()
    expect(apiMock.refresh).toHaveBeenCalledWith('/api/v1/auth/refresh', {}, { withCredentials: true, timeout: 15000 })
    expect(apiMock.post).not.toHaveBeenCalled()
    expect(localStorage.getItem('cqa_access_token')).toBe('redirect-jwt')
    expect(auth.user?.id).toBe('user-1')
  })

  it.each([{}, { access_token: '' }, { access_token: 123 }])('rejects malformed OAuth completion without installing a session: %j', async (data) => {
    apiMock.refresh.mockResolvedValue({ data })
    const auth = useAuthStore()
    await expect(auth.completeFacebookRedirect()).rejects.toThrow('invalid_login_response')
    expect(auth.isAuthenticated).toBe(false)
    expect(apiMock.get).not.toHaveBeenCalled()
  })

  it('does not retry a rejected OAuth refresh through the API interceptor', async () => {
    apiMock.refresh.mockRejectedValue({ response: { status: 401 } })
    const auth = useAuthStore()
    await expect(auth.completeFacebookRedirect()).rejects.toEqual({ response: { status: 401 } })
    expect(apiMock.refresh).toHaveBeenCalledOnce()
    expect(auth.isAuthenticated).toBe(false)
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
