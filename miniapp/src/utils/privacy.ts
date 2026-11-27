// Consent to the privacy policy. Nothing is sent to the server, and wx.login is not called,
// before the user has agreed.

/** Bump when the policy text changes in a way that needs the user to agree again. */
export const PRIVACY_VERSION = '2026-11-26'

const KEY = 'privacyAgreed'

export const privacyAgreed = () => uni.getStorageSync(KEY) === PRIVACY_VERSION

export function setPrivacyAgreed(agreed: boolean) {
  if (agreed) uni.setStorageSync(KEY, PRIVACY_VERSION)
  else uni.removeStorageSync(KEY)
}
