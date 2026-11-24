import { API_BASE } from '@/config'

const TOKEN_KEY = 'token'

export class ApiError extends Error {
  constructor(public status: number, message: string) {
    super(message)
  }
}

type Method = 'GET' | 'POST' | 'PUT' | 'DELETE'

function send<T>(method: Method, path: string, data?: unknown, token?: string): Promise<T> {
  return new Promise((resolve, reject) => {
    uni.request({
      url: API_BASE + '/api' + path,
      method,
      data: data as any,
      header: token ? { Authorization: 'Bearer ' + token } : {},
      timeout: 70000,
      success: (res) => {
        if (res.statusCode >= 200 && res.statusCode < 300) {
          resolve(res.data as T)
        } else {
          reject(new ApiError(res.statusCode, (res.data as any)?.message || `请求失败（${res.statusCode}）`))
        }
      },
      fail: (err) => reject(new ApiError(0, '网络不可用：' + (err.errMsg || ''))),
    })
  })
}

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
    if (!(e instanceof ApiError) || e.status !== 401) throw e
    // The token expired or the server secret changed: log in again once.
    uni.removeStorageSync(TOKEN_KEY)
    return send<T>(method, path, data, await ensureLogin())
  }
}

/** Turns a server-relative path such as /api/img?... into an absolute URL. */
export function absoluteUrl(path: string): string {
  return path && path.startsWith('/') ? API_BASE + path : path
}

export function toastError(e: unknown) {
  uni.showToast({ title: e instanceof Error ? e.message : String(e), icon: 'none', duration: 3000 })
}
