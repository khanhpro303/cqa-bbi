// @vitest-environment happy-dom
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, shallowMount, type VueWrapper } from '@vue/test-utils'
import FacebookAccountManager from '../components/FacebookAccountManager.vue'

const mocks = vi.hoisted(() => ({
  get: vi.fn(), post: vi.fn(), delete: vi.fn(), replace: vi.fn(),
  route: { name: 'settings', path: '/tenant-1/settings', query: {} as Record<string, string>, hash: '' },
}))
vi.mock('../api', () => ({ default: mocks }))
vi.mock('vue-router', () => ({ useRoute: () => mocks.route, useRouter: () => ({ replace: mocks.replace }) }))
vi.mock('vue-i18n', () => ({ useI18n: () => ({ t: (key: string) => key }) }))

let wrapper: VueWrapper
function mountManager(active = true, showUnavailable = false) {
  wrapper = shallowMount(FacebookAccountManager, {
    props: { active, showUnavailable },
    global: {
      stubs: Object.fromEntries([
        'VAlert', 'VBtn', 'VIcon', 'VSpacer', 'VChip', 'VTextField', 'VSnackbar',
      ].map(name => [name, true])),
      renderStubDefaultSlot: true,
    },
  })
  return wrapper
}

async function clickLink() {
  wrapper.getComponent({ name: 'VTextField' }).vm.$emit('update:modelValue', 'cqa-password')
  await wrapper.vm.$nextTick()
  await wrapper.get('v-btn-stub').trigger('click')
  await flushPromises()
}

describe('FacebookAccountManager redirect linking', () => {
  beforeEach(() => {
    vi.restoreAllMocks()
    mocks.get.mockReset().mockResolvedValue({ data: { enabled: true, linked: false } })
    mocks.post.mockReset()
    mocks.delete.mockReset()
    mocks.replace.mockReset().mockResolvedValue(undefined)
    mocks.route.name = 'settings'
    mocks.route.path = '/tenant-1/settings'
    mocks.route.query = {}
    mocks.route.hash = ''
  })
  afterEach(() => wrapper?.unmount())

  it('loads only account status when active without requiring the Facebook SDK', async () => {
    mountManager(false)
    await flushPromises()
    expect(mocks.get).not.toHaveBeenCalled()
    await wrapper.setProps({ active: true })
    await flushPromises()
    expect(mocks.get).toHaveBeenCalledExactlyOnceWith('/profile/facebook', { timeout: 15000 })
    expect(wrapper.text()).toContain('facebook_link_action')
  })

  it('does not show the unavailable notice under an enabled form', async () => {
    mountManager(true, true)
    await flushPromises()
    expect(wrapper.text()).not.toContain('facebook_login_not_configured')
  })

  it('explains unavailable Facebook sign-in', async () => {
    mocks.get.mockResolvedValue({ data: { enabled: false, linked: false } })
    mountManager(true, true)
    await flushPromises()
    expect(wrapper.text()).toContain('facebook_login_not_configured')
  })

  it('verifies the CQA password then navigates in the same window without an access token', async () => {
    const assign = vi.spyOn(window.location, 'assign').mockImplementation(() => {})
    const url = 'https://www.facebook.com/v26.0/dialog/oauth?state=link_random&response_type=code'
    mocks.post.mockResolvedValue({ data: { redirect_url: url } })
    mountManager()
    await flushPromises()
    await clickLink()
    expect(mocks.post).toHaveBeenCalledWith('/profile/facebook/link/start', {
      current_password: 'cqa-password', return_path: '/tenant-1/settings',
    }, { timeout: 15000, signal: expect.any(AbortSignal) })
    expect(assign).toHaveBeenCalledExactlyOnceWith(url)
    expect(wrapper.text()).not.toContain('facebook_link_success')
  })

  it('returns avatar flows to the account picker instead of an arbitrary route', async () => {
    mocks.route.name = 'messages'
    mocks.route.path = '/tenant-1/messages'
    vi.spyOn(window.location, 'assign').mockImplementation(() => {})
    mocks.post.mockResolvedValue({ data: { redirect_url: 'https://www.facebook.com/v26.0/dialog/oauth' } })
    mountManager()
    await flushPromises()
    await clickLink()
    expect(mocks.post.mock.calls[0]?.[1].return_path).toBe('/')
  })

  it.each([
    ['wrong_current_password', 'wrong_password'],
    ['facebook_account_already_linked', 'facebook_account_already_linked'],
  ])('displays %s and releases the loading button', async (code, message) => {
    mocks.post.mockRejectedValue({ response: { data: { error: code } } })
    mountManager()
    await flushPromises()
    await clickLink()
    expect(wrapper.text()).toContain(message)
    expect(wrapper.get('v-btn-stub').attributes('loading')).toBe('false')
  })

  it('bounds API waiting and distinguishes it from the old popup timeout', async () => {
    mocks.post.mockRejectedValue({ code: 'ECONNABORTED' })
    mountManager()
    await flushPromises()
    await clickLink()
    expect(wrapper.text()).toContain('facebook_link_start_timeout')
    expect(wrapper.text()).not.toContain('facebook_login_timeout')
  })

  it('does not redirect if a pending request completes after cancellation', async () => {
    const assign = vi.spyOn(window.location, 'assign').mockImplementation(() => {})
    let resolve!: (value: any) => void
    mocks.post.mockImplementation(() => new Promise(r => { resolve = r }))
    mountManager()
    await flushPromises()
    await clickLink()
    await wrapper.setProps({ active: false })
    expect(mocks.post.mock.calls[0]?.[2].signal.aborted).toBe(true)
    resolve({ data: { redirect_url: 'https://www.facebook.com/v26.0/dialog/oauth' } })
    await flushPromises()
    expect(assign).not.toHaveBeenCalled()
  })

  it.each(['https://www.facebook.com.evil.test/', 'javascript:alert(1)', 'https://www.facebook.com:4430/'])('rejects unsafe redirect %s', async url => {
    const assign = vi.spyOn(window.location, 'assign').mockImplementation(() => {})
    mocks.post.mockResolvedValue({ data: { redirect_url: url } })
    mountManager()
    await flushPromises()
    await clickLink()
    expect(assign).not.toHaveBeenCalled()
    expect(wrapper.text()).toContain('facebook_link_failed')
  })

  it('shows persistent callback success only after verifying linked status and cleans the query', async () => {
    mocks.route.query = { facebook_link: 'success', keep: '1' }
    mocks.get.mockResolvedValue({ data: { enabled: true, linked: true, facebook_name: 'Test Person' } })
    mountManager()
    await flushPromises()
    expect(wrapper.text()).toContain('facebook_link_success')
    expect(wrapper.text()).toContain('Test Person')
    expect(mocks.replace).toHaveBeenCalledWith({ path: '/tenant-1/settings', query: { keep: '1' }, hash: '' })
  })

  it.each([
    ['oauth_denied', 'facebook_link_cancelled'],
    ['session_expired', 'facebook_link_session_expired'],
    ['browser_mismatch', 'facebook_link_session_expired'],
    ['success', 'facebook_link_failed'],
  ])('explains callback %s and never fabricates success', async (code, message) => {
    mocks.route.query = { facebook_link: code }
    mountManager()
    await flushPromises()
    expect(wrapper.text()).toContain(message)
    expect(wrapper.text()).not.toContain('facebook_link_success')
  })
})
