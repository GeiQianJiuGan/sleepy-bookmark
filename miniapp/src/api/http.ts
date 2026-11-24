import { API_BASE, REQUEST_TIMEOUT } from '@/config'

// The access token is short-lived; the refresh token ("rtoken") renews it without another wx.login.
const TOKEN_KEY = 'token'
const EXPIRES_KEY = 'tokenExpires'
const REFRESH_KEY = 'rtoken'
const LOGIN_PAGE = '/pages/login/login'

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

/** What the server needs to log this device in. */
interface Credential {
  code: string
  /** AppID the mini program is running under, so the server can tell when it is not its own. */
  appId?: string
}

// #ifdef MP-WEIXIN
function runningAppId(): string {
  try {
    return uni.getAccountInfoSync().miniProgram.appId
  } catch {
    return ''
  }
}
// #endif

function credential(): Promise<Credential> {
  // #ifdef MP-WEIXIN
  return new Promise((resolve, reject) => {
    // A code works once and for five minutes, so every attempt asks for a new one.
    uni.login({
      provider: 'weixin',
      timeout: 10_000,
      success: (res) => {
        if (res.code) resolve({ code: res.code, appId: runningAppId() })
        else reject(new ApiError(0, '微信登录失败：' + (res.errMsg || '没有拿到登录凭证')))
      },
      fail: (err) => reject(new ApiError(0, '微信登录失败：' + (err.errMsg || ''))),
    })
  })
  // #endif
  // #ifndef MP-WEIXIN
  // Outside WeChat only a backend started with DEV_LOGIN=1 accepts this.
  return Promise.resolve({ code: 'h5-dev' })
  // #endif
}

interface Session {
  token: string
  refreshToken: string
  /** Lifetime of the access token in seconds. */
  expiresIn: number
}

// Counts logouts, so that a login or refresh still in flight when the user logs out is discarded.
let epoch = 0

/** Stores the session a request started in the given epoch came back with. */
function saveSession(session: Session, started: number): string {
  if (started !== epoch) throw new ApiError(401, '已退出登录')
  uni.setStorageSync(TOKEN_KEY, session.token)
  uni.setStorageSync(EXPIRES_KEY, Date.now() + session.expiresIn * 1000)
  uni.setStorageSync(REFRESH_KEY, session.refreshToken)
  return session.token
}

function clearSession() {
  epoch++
  uni.removeStorageSync(TOKEN_KEY)
  uni.removeStorageSync(EXPIRES_KEY)
  uni.removeStorageSync(REFRESH_KEY)
}

/** True when a refresh token is stored, i.e. the user has logged in on this device. */
export const hasSession = () => !!uni.getStorageSync(REFRESH_KEY)

let leaving = false

/** Opens the login page, unless it is already open or being opened. */
export function toLogin(retry = true) {
  const pages = getCurrentPages()
  const current = pages[pages.length - 1]?.route || ''
  if (leaving || '/' + current === LOGIN_PAGE) return
  leaving = true
  uni.reLaunch({
    url: LOGIN_PAGE,
    fail: () => {
      // Navigating while the app is still launching can be refused: try once more shortly after.
      if (retry) setTimeout(() => toLogin(false), 300)
    },
    complete: () => {
      leaving = false
    },
  })
}

let pending: Promise<string> | null = null

/** Exchanges the refresh token for a new access token. Opens the login page when that is not possible. */
export function refreshSession(): Promise<string> {
  if (!pending) {
    const refreshToken = uni.getStorageSync(REFRESH_KEY)
    if (!refreshToken) {
      toLogin()
      return Promise.reject(new ApiError(401, '请先登录'))
    }
    const started = epoch
    pending = send<Session>('POST', '/auth/refresh', { refreshToken })
      .then((session) => saveSession(session, started))
      .catch((e) => {
        // Only a refusal ends the session; an unreachable server keeps it for the next try.
        if (e instanceof ApiError && e.status === 401 && started === epoch) {
          clearSession()
          toLogin()
        }
        throw e
      })
      .finally(() => {
        if (pending === mine) pending = null
      })
    const mine = pending
  }
  return pending
}

/** Returns a usable access token, renewing it when it is missing or about to expire. */
function accessToken(): Promise<string> {
  const token = uni.getStorageSync(TOKEN_KEY)
  const expires = Number(uni.getStorageSync(EXPIRES_KEY)) || 0
  if (token && Date.now() < expires - 60_000) return Promise.resolve(token)
  return refreshSession()
}

/** Logs in through wx.login. Only the login page calls this, after the user agreed to the privacy policy. */
export async function login(): Promise<void> {
  const started = epoch
  saveSession(await send<Session>('POST', '/auth/login', await credential()), started)
}

export function logout() {
  clearSession()
  // A refresh still in flight belongs to the session that just ended.
  pending = null
  toLogin()
}

export async function request<T = void>(method: Method, path: string, data?: unknown): Promise<T> {
  try {
    return await send<T>(method, path, data, await accessToken())
  } catch (e) {
    if (!(e instanceof ApiError)) throw e
    if (e.status === 401 && hasSession()) {
      // The access token was refused (expired early, server secret changed): renew it once.
      return send<T>(method, path, data, await refreshSession())
    }
    if (e.status === 0 && method === 'GET') {
      // A dropped connection (switching networks, waking from the background) usually works
      // on the second try, so reads do not bother the user with it.
      await sleep(800)
      return send<T>(method, path, data, await accessToken())
    }
    throw e
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
