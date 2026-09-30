// @vitest-environment happy-dom
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, shallowMount } from '@vue/test-utils'
import FacebookAccountManager from '../components/FacebookAccountManager.vue'

const mocks = vi.hoisted(() => ({ get: vi.fn(), post: vi.fn(), delete: vi.fn(), loadFacebookSDK: vi.fn() }))

vi.mock('../api', () => ({ default: mocks }))
vi.mock('../utils/facebook-sdk', () => ({
  loadFacebookSDK: mocks.loadFacebookSDK,
  requestFacebookLogin: vi.fn(),
}))
vi.mock('vue-i18n', () => ({ useI18n: () => ({ t: (key: string) => key }) }))

function mountManager(active = true, showUnavailable = false) {
  return shallowMount(FacebookAccountManager, {
    props: { active, showUnavailable },
    global: {
      stubs: Object.fromEntries([
        'VAlert', 'VBtn', 'VIcon', 'VSpacer', 'VChip', 'VTextField', 'VSnackbar',
      ].map(name => [name, true])),
      renderStubDefaultSlot: true,
    },
  })
}

describe('FacebookAccountManager', () => {
  beforeEach(() => {
    mocks.get.mockReset()
    mocks.post.mockReset()
    mocks.delete.mockReset()
    mocks.loadFacebookSDK.mockReset().mockResolvedValue({})
  })

  it('loads the current link state and SDK only after the section becomes active', async () => {
    mocks.get.mockImplementation(async (url: string) => ({
      data: url === '/profile/facebook'
        ? { enabled: true, linked: false }
        : { enabled: true, app_id: 'app-id', api_version: 'v26.0' },
    }))
    const wrapper = mountManager(false)
    await flushPromises()
    expect(mocks.get).not.toHaveBeenCalled()

    await wrapper.setProps({ active: true })
    await flushPromises()
    expect(mocks.get).toHaveBeenNthCalledWith(1, '/profile/facebook')
    expect(mocks.get).toHaveBeenNthCalledWith(2, '/auth/facebook/config')
    expect(mocks.loadFacebookSDK).toHaveBeenCalledWith({ appId: 'app-id', apiVersion: 'v26.0' })
    expect(wrapper.text()).toContain('facebook_account')
  })

  it('explains unavailable Facebook sign-in in the Settings module', async () => {
    mocks.get.mockResolvedValue({ data: { enabled: false, linked: false } })
    const wrapper = mountManager(true, true)
    await flushPromises()
    expect(wrapper.text()).toContain('facebook_login_not_configured')
    expect(mocks.get).toHaveBeenCalledTimes(1)
  })

  it('does not show the unavailable notice below an enabled linking form', async () => {
    mocks.get.mockImplementation(async (url: string) => ({
      data: url === '/profile/facebook'
        ? { enabled: true, linked: false }
        : { enabled: true, app_id: 'app-id', api_version: 'v26.0' },
    }))
    const wrapper = mountManager(true, true)
    await flushPromises()
    expect(wrapper.text()).toContain('facebook_link_action')
    expect(wrapper.text()).not.toContain('facebook_login_not_configured')
  })
})
