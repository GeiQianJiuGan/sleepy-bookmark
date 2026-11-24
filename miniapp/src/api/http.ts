import { API_BASE, REQUEST_TIMEOUT } from '@/config'

const TOKEN_KEY = 'token'

export class ApiError extends Error {
  /**
   * @param status HTTP status, 0 when the server could not be reached.
   * @param code   Kind of source failure reported by the server: timeout, network, busy,
   *               blocked, not_found, http or rule. Empty for everything else.
   */
  constructor(public status: number, message: string, public code = '') {
    super(message)
  }
}

/** True when trying another source may help: the source site itself is failing. */
export function sourceDown(e: unknown): boolean {
  return e instanceof ApiError && ['timeout', 'network', 'busy', 'blocked', 'not_found'].includes(e.code)
}

type Method = 'GET' | 'POST' | 'PUT' | 'DELETE'

function send<T>(method: Method, path: string, data?: unknown, token?: string): Promise<T> {
  return new Promise((resolve, reject) => {
    uni.request({
      url: API_BASE + '/api' + path,
      method,
      data: data as any,
      header: token ? { Authorization: 'Bearer ' + token } : {},
      timeout: REQUEST_TIMEOUT,
      success: (res) => {
        if (res.statusCode >= 200 && res.statusCode < 300) {
          resolve(res.data as T)
        } else {
          const body = res.data as any
          reject(new ApiError(res.statusCode, body?.message || `请求失败（${res.statusCode}）`, body?.code || ''))
        }
      },
      fail: () => reject(new ApiError(0, '连接不上服务器，请检查网络')),
    })
  })
}

const sleep = (ms: number) => new Promise((resolve) => setTimeout(resolve, ms))

function loginCode(): Promise<string> {
  // #ifdef MP-WEIXIN
  return new Promise((resolve, reject) => {
    uni.login({
      provider: 'weixin',
      success: (res) => resolve(res.code),
      fail: (err) => reject(new ApiError(0, '微信登录失败：' + (err.errMsg || ''))),
    })
  })
  // #endif
  // #ifndef MP-WEIXIN
  // Outside WeChat only a backend started with DEV_LOGIN=1 accepts this.
  return Promise.resolve('h5-dev')
  // #endif
}

let pending: Promise<string> | null = null

/** Returns the session token, logging in through wx.login when there is none. */
export function ensureLogin(): Promise<string> {
  const token = uni.getStorageSync(TOKEN_KEY)
  if (token) return Promise.resolve(token)
  if (!pending) {
    pending = loginCode()
      .then((code) => send<{ token: string }>('POST', '/auth/login', { code }))
      .then((res) => {
        uni.setStorageSync(TOKEN_KEY, res.token)
        return res.token
      })
      .finally(() => {
        pending = null
      })
  }
  return pending
}

export async function request<T = void>(method: Method, path: string, data?: unknown): Promise<T> {
  try {
    return await send<T>(method, path, data, await ensureLogin())
  } catch (e) {
    if (!(e instanceof ApiError)) throw e
    if (e.status === 401) {
      // The token expired or the server secret changed: log in again once.
      uni.removeStorageSync(TOKEN_KEY)
    } else if (e.status === 0 && method === 'GET') {
      // A dropped connection (switching networks, waking from the background) usually works
      // on the second try, so reads do not bother the user with it.
      await sleep(800)
    } else {
      throw e
    }
    return send<T>(method, path, data, await ensureLogin())
  }
}

export const errorText = (e: unknown) => (e instanceof Error ? e.message : String(e))

/** Turns a server-relative path such as /api/img?... into an absolute URL. */
export function absoluteUrl(path: string): string {
  return path && path.startsWith('/') ? API_BASE + path : path
}

export function toastError(e: unknown) {
  uni.showToast({ title: errorText(e), icon: 'none', duration: 2500 })
}
