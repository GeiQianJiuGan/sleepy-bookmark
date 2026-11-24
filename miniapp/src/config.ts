import appConfig from '../../app.config.json'

/** 后端服务器地址（优先使用环境变量 VITE_API_BASE，否则读取根目录 app.config.json） */
export const API_BASE: string = (import.meta.env.VITE_API_BASE || appConfig.serverUrl).replace(/\/$/, '')

/** 微信小程序 AppID */
export const APP_ID: string = appConfig.appId

/** 请求超时时间（毫秒） */
export const REQUEST_TIMEOUT: number = appConfig.requestTimeout ?? 70000
