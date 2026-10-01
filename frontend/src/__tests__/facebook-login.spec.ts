// @vitest-environment happy-dom
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, shallowMount } from '@vue/test-utils'
import Login from '../views/Login.vue'

const mocks = vi.hoisted(() => ({
  get: vi.fn(), post: vi.fn(), complete: vi.fn(), login: vi.fn(), push: vi.fn(), replace: vi.fn(),
  route: { query: {} as Record<string, string>, hash: '' },
}))
vi.mock('../api', () => ({ default: { get: mocks.get, post: mocks.post } }))
vi.mock('../stores/auth', () => ({ useAuthStore: () => ({ completeFacebookRedirect: mocks.complete, login: mocks.login }) }))
vi.mock('vue-router', () => ({ useRouter: () => ({ push: mocks.push, replace: mocks.replace }), useRoute: () => mocks.route }))
vi.mock('vue-i18n', () => ({ useI18n: () => ({ t: (key: string) => key }) }))

function mountLogin() {
  return shallowMount(Login, {
    global: {
      mocks: { $t: (key: string) => key },
      stubs: Object.fromEntries(['VCard', 'VCardTitle', 'VAlert', 'VForm', 'VTextField', 'VBtn', 'VDivider'].map(name => [name, true])),
      renderStubDefaultSlot: true,
    },
  })
}
beforeEach(() => {
  vi.resetAllMocks()
  mocks.route.query = {}
  mocks.get.mockResolvedValue({ data: { enabled: true } })
  mocks.complete.mockResolvedValue(undefined)
})
afterEach(() => vi.restoreAllMocks())

describe('Facebook redirect sign-in', () => {
  it('starts same-tab server OAuth without an iframe or browser Facebook token', async () => {
    const assign = vi.spyOn(window.location, 'assign').mockImplementation(() => {})
    const redirect = 'https://www.facebook.com/v26.0/dialog/oauth?config_id=business&response_type=code&state=signin_random'
    mocks.post.mockResolvedValue({ data: { redirect_url: redirect } })
    const view = mountLogin()
    await flushPromises()
    expect(view.find('iframe').exists()).toBe(false)
    expect(mocks.post).not.toHaveBeenCalled()
    await view.findAllComponents({ name: 'VBtn' })[1]!.trigger('click')
    await flushPromises()
    expect(mocks.post).toHaveBeenCalledWith('/auth/facebook/start', {}, expect.objectContaining({ timeout: 15000, signal: expect.any(AbortSignal) }))
    expect(assign).toHaveBeenCalledWith(redirect)
    expect(mocks.complete).not.toHaveBeenCalled()
    view.unmount()
  })

  it.each(['https://evil.test/', 'http://www.facebook.com/', 'https://user@www.facebook.com/', 'https://www.facebook.com:8443/'])('rejects unsafe redirect %s and permits retry', async (redirect_url) => {
    const assign = vi.spyOn(window.location, 'assign').mockImplementation(() => {})
    mocks.post.mockResolvedValue({ data: { redirect_url } })
    const view = mountLogin()
    await flushPromises()
    const button = view.findAllComponents({ name: 'VBtn' })[1]!
    await button.trigger('click')
    await flushPromises()
    expect(assign).not.toHaveBeenCalled()
    expect(view.text()).toContain('facebook_login_failed')
    expect(button.attributes('loading')).toBe('false')
    view.unmount()
  })

  it('completes only the explicit successful callback using the secure cookie', async () => {
    mocks.route.query = { facebook_login: 'success', other: 'keep' }
    const view = mountLogin()
    await flushPromises()
    expect(mocks.replace).toHaveBeenNthCalledWith(1, { path: '/login', query: { other: 'keep' }, hash: '' })
    expect(mocks.complete).toHaveBeenCalledOnce()
    expect(mocks.replace).toHaveBeenLastCalledWith('/')
    view.unmount()
  })

  it('does not accept a success query without a valid authenticated cookie', async () => {
    mocks.route.query = { facebook_login: 'success' }
    mocks.complete.mockRejectedValue({ response: { status: 401 } })
    const view = mountLogin()
    await flushPromises()
    expect(mocks.replace).not.toHaveBeenCalledWith('/')
    expect(view.text()).toContain('facebook_login_failed')
    view.unmount()
  })

  it.each([
    ['facebook_account_not_linked', 'facebook_account_not_linked'],
    ['facebook_service_unavailable', 'facebook_service_unavailable'],
    ['session_expired', 'facebook_signin_session_expired'],
    ['browser_mismatch', 'facebook_signin_session_expired'],
    ['invalid_state', 'facebook_signin_session_expired'],
    ['oauth_denied', 'facebook_signin_cancelled'],
  ])('shows a specific %s callback error without authenticating', async (code, message) => {
    mocks.route.query = { facebook_login: code }
    const view = mountLogin()
    await flushPromises()
    expect(view.text()).toContain(message)
    expect(mocks.complete).not.toHaveBeenCalled()
    expect(mocks.replace).toHaveBeenCalledWith({ path: '/login', query: {}, hash: '' })
    view.unmount()
  })

  it('releases loading after a failed start and prevents duplicate concurrent requests', async () => {
    let reject!: (error: unknown) => void
    mocks.post.mockReturnValue(new Promise((_, rejectPromise) => { reject = rejectPromise }))
    const view = mountLogin()
    await flushPromises()
    const button = view.findAllComponents({ name: 'VBtn' })[1]!
    await button.trigger('click')
    await button.trigger('click')
    expect(mocks.post).toHaveBeenCalledOnce()
    reject({ response: { data: { error: 'facebook_service_unavailable' } } })
    await flushPromises()
    expect(button.attributes('loading')).toBe('false')
    expect(view.text()).toContain('facebook_service_unavailable')
    view.unmount()
  })

  it('keeps local sign-in available without automatically signing in to Facebook', async () => {
    const view = mountLogin()
    await flushPromises()
    view.findAllComponents({ name: 'VTextField' })[0]!.vm.$emit('update:modelValue', 'user@test.com')
    view.findAllComponents({ name: 'VTextField' })[1]!.vm.$emit('update:modelValue', 'password')
    view.findComponent({ name: 'VForm' }).vm.$emit('submit', { preventDefault: vi.fn() })
    await flushPromises()
    expect(mocks.login).toHaveBeenCalledWith('user@test.com', 'password')
    expect(mocks.push).toHaveBeenCalledWith('/')
    expect(mocks.complete).not.toHaveBeenCalled()
    view.unmount()
  })
})
