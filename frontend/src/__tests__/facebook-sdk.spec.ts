// @vitest-environment happy-dom

import { beforeEach, describe, expect, it, vi } from 'vitest'

describe('Facebook JavaScript SDK', () => {
  beforeEach(() => {
    vi.restoreAllMocks()
    vi.resetModules()
    document.head.innerHTML = ''
    delete window.FB
    delete window.fbAsyncInit
  })

  it('loads asynchronously and initializes with the backend config', async () => {
    const { loadFacebookSDK } = await import('../utils/facebook-sdk')
    const init = vi.fn()
    const appendChild = vi.spyOn(document.head, 'appendChild').mockImplementation((node) => node)

    const pendingSDK = loadFacebookSDK({ appId: '123', apiVersion: 'v26.0' })
    const script = appendChild.mock.calls[0][0] as HTMLScriptElement

    expect(script).not.toBeNull()
    expect(script.async).toBe(true)
    expect(script.src).toBe('https://connect.facebook.net/en_US/sdk.js')

    window.FB = { init, getLoginStatus: vi.fn() }
    window.fbAsyncInit?.()
    const sdk = await pendingSDK

    expect(sdk).toBe(window.FB)
    expect(init).toHaveBeenCalledWith({
      appId: '123',
      cookie: true,
      xfbml: true,
      version: 'v26.0',
    })
  })

  it('can retry after the SDK script fails to load', async () => {
    const { loadFacebookSDK } = await import('../utils/facebook-sdk')
    const appendChild = vi.spyOn(document.head, 'appendChild').mockImplementation((node) => node)

    const firstAttempt = loadFacebookSDK({ appId: '123', apiVersion: 'v26.0' })
    const firstScript = appendChild.mock.calls[0][0] as HTMLScriptElement
    firstScript.onerror?.(new Event('error'))
    await expect(firstAttempt).rejects.toThrow('facebook_sdk_load_failed')

    const secondAttempt = loadFacebookSDK({ appId: '123', apiVersion: 'v26.0' })
    const init = vi.fn()
    window.FB = { init, getLoginStatus: vi.fn() }
    window.fbAsyncInit?.()

    await expect(secondAttempt).resolves.toBe(window.FB)
    expect(appendChild).toHaveBeenCalledTimes(2)
    expect(init).toHaveBeenCalledOnce()
  })

  it('rejects cleanly when FB.init throws', async () => {
    const { loadFacebookSDK } = await import('../utils/facebook-sdk')
    window.FB = {
      init: vi.fn(() => { throw new Error('bad Facebook configuration') }),
      getLoginStatus: vi.fn(),
    }

    await expect(loadFacebookSDK({ appId: '123', apiVersion: 'v26.0' }))
      .rejects.toThrow('facebook_sdk_init_failed')
  })

  it('returns the connected login status and access token', async () => {
    const { getFacebookLoginStatus } = await import('../utils/facebook-sdk')
    const response = {
      status: 'connected' as const,
      authResponse: {
        accessToken: 'existing-facebook-token',
        expiresIn: 3600,
        signedRequest: 'signed-request',
        userID: 'fb-user-1',
      },
    }
    const sdk = {
      init: vi.fn(),
      getLoginStatus: vi.fn((callback: (value: typeof response) => void) => callback(response)),
    }

    await expect(getFacebookLoginStatus(sdk)).resolves.toEqual(response)
    expect(sdk.getLoginStatus).toHaveBeenCalledOnce()
  })

  it.each(['not_authorized', 'unknown'] as const)('returns the %s login status without an auth token', async (status) => {
    const { getFacebookLoginStatus } = await import('../utils/facebook-sdk')
    const sdk = {
      init: vi.fn(),
      getLoginStatus: vi.fn((callback: (value: { status: typeof status }) => void) => callback({ status })),
    }

    await expect(getFacebookLoginStatus(sdk)).resolves.toEqual({ status })
  })

  it('renders the configured XFBML login button and parses only its container', async () => {
    const { renderFacebookLoginButton } = await import('../utils/facebook-sdk')
    const parse = vi.fn()
    const sdk = {
      init: vi.fn(),
      getLoginStatus: vi.fn(),
      XFBML: { parse },
    }
    const container = document.createElement('div')

    renderFacebookLoginButton(sdk, container)

    const button = container.firstElementChild
    expect(button?.tagName.toLowerCase()).toBe('fb:login-button')
    expect(button?.getAttribute('scope')).toBe('public_profile,email')
    expect(button?.getAttribute('onlogin')).toBe('checkLoginState();')
    expect(parse).toHaveBeenCalledWith(container)
  })
})
