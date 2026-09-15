export interface FacebookSDKConfig {
  appId: string
  apiVersion: string
}

export type FacebookLoginStatus = 'connected' | 'not_authorized' | 'unknown'

export interface FacebookLoginResponse {
  authResponse?: {
    accessToken?: string
    expiresIn?: number
    signedRequest?: string
    userID?: string
  }
  status: FacebookLoginStatus
}

interface FacebookSDK {
  init(config: { appId: string; cookie: boolean; xfbml: boolean; version: string }): void
  getLoginStatus(callback: (response: FacebookLoginResponse) => void): void
  XFBML?: {
    parse(rootNode?: HTMLElement): void
  }
}

declare global {
  interface Window {
    FB?: FacebookSDK
    fbAsyncInit?: () => void
    checkLoginState?: () => void
  }
}

const sdkElementID = 'facebook-jssdk'
let sdkPromise: Promise<FacebookSDK> | undefined

export function loadFacebookSDK(config: FacebookSDKConfig): Promise<FacebookSDK> {
  if (sdkPromise) return sdkPromise

  const loadingPromise = new Promise<FacebookSDK>((resolve, reject) => {
    let settled = false
    let timeoutID: number | undefined

    const fail = (code: string) => {
      if (settled) return
      settled = true
      if (timeoutID !== undefined) window.clearTimeout(timeoutID)
      document.getElementById(sdkElementID)?.remove()
      reject(new Error(code))
    }

    const initialize = () => {
      if (settled) return
      if (!window.FB) {
        fail('facebook_sdk_unavailable')
        return
      }
      try {
        window.FB.init({
          appId: config.appId,
          cookie: true,
          xfbml: true,
          version: config.apiVersion,
        })
      } catch {
        fail('facebook_sdk_init_failed')
        return
      }
      settled = true
      if (timeoutID !== undefined) window.clearTimeout(timeoutID)
      resolve(window.FB)
    }

    if (window.FB) {
      initialize()
      return
    }

    window.fbAsyncInit = initialize
    timeoutID = window.setTimeout(() => fail('facebook_sdk_load_timeout'), 15_000)

    const existingScript = document.getElementById(sdkElementID)
    if (existingScript) {
      existingScript.addEventListener('load', initialize, { once: true })
      existingScript.addEventListener('error', () => fail('facebook_sdk_load_failed'), { once: true })
      return
    }

    const script = document.createElement('script')
    script.id = sdkElementID
    script.async = true
    script.defer = true
    script.crossOrigin = 'anonymous'
    script.src = 'https://connect.facebook.net/en_US/sdk.js'
    script.onload = initialize
    script.onerror = () => fail('facebook_sdk_load_failed')
    document.head.appendChild(script)
  })

  sdkPromise = loadingPromise.catch((error) => {
    sdkPromise = undefined
    throw error
  })

  return sdkPromise
}

export async function getFacebookLoginStatus(sdk: FacebookSDK): Promise<FacebookLoginResponse> {
  return new Promise((resolve, reject) => {
    let settled = false
    const timeoutID = window.setTimeout(() => {
      if (settled) return
      settled = true
      reject(new Error('facebook_status_check_timeout'))
    }, 10_000)

    try {
      sdk.getLoginStatus((response) => {
        if (settled) return
        settled = true
        window.clearTimeout(timeoutID)
        resolve(response)
      })
    } catch {
      settled = true
      window.clearTimeout(timeoutID)
      reject(new Error('facebook_status_check_failed'))
    }
  })
}

export function renderFacebookLoginButton(sdk: FacebookSDK, container: HTMLElement): void {
  if (!sdk.XFBML?.parse) throw new Error('facebook_sdk_xfbml_unavailable')

  const button = document.createElement('fb:login-button')
  button.setAttribute('scope', 'public_profile,email')
  button.setAttribute('onlogin', 'checkLoginState();')
  container.replaceChildren(button)
  sdk.XFBML.parse(container)
}
