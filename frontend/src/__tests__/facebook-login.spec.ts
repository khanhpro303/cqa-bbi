// @vitest-environment happy-dom
import { afterEach, expect, it, vi } from 'vitest'
import { flushPromises, shallowMount } from '@vue/test-utils'
import Login from '../views/Login.vue'

const mocks = vi.hoisted(() => ({
  get: vi.fn(), load: vi.fn(), render: vi.fn(), status: vi.fn(), loginWithFacebook: vi.fn(),
}))
vi.mock('../api', () => ({ default: { get: mocks.get } }))
vi.mock('../stores/auth', () => ({ useAuthStore: () => ({ loginWithFacebook: mocks.loginWithFacebook }) }))
vi.mock('vue-router', () => ({ useRouter: () => ({ push: vi.fn() }) }))
vi.mock('vue-i18n', () => ({ useI18n: () => ({ t: (key: string) => key }) }))
vi.mock('../utils/facebook-sdk', () => ({
  loadFacebookSDK: mocks.load, renderFacebookLoginButton: mocks.render, getFacebookLoginStatus: mocks.status,
}))
afterEach(() => vi.clearAllMocks())

it('passes the configured Business login configuration to the existing Login button', async () => {
  const sdk = { init: vi.fn() }
  mocks.get.mockResolvedValue({ data: {
    enabled: true, app_id: 'app', api_version: 'v26.0', login_config_id: 'page-business-config',
  } })
  mocks.load.mockResolvedValue(sdk)
  mocks.status.mockResolvedValue({ status: 'unknown' })
  const view = shallowMount(Login, {
    global: {
      mocks: { $t: (key: string) => key },
      stubs: Object.fromEntries(['VCard', 'VCardTitle', 'VAlert', 'VForm', 'VTextField', 'VBtn', 'VDivider', 'VProgressCircular'].map(name => [name, true])),
      renderStubDefaultSlot: true,
    },
  })
  await flushPromises()
  expect(mocks.load).toHaveBeenCalledWith({ appId: 'app', apiVersion: 'v26.0', loginConfigId: 'page-business-config' })
  expect(mocks.render).toHaveBeenCalledWith(sdk, expect.any(HTMLElement), 'page-business-config')
  expect(mocks.loginWithFacebook).not.toHaveBeenCalled()
  view.unmount()
})
