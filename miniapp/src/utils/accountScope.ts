import { ref } from 'vue'

// The Go server puts the numeric account id in the JWT subject. Use it only to
// locate device files (also when offline). Permissions always come from /me.
function subject(token: string): number | null {
  try {
    const part = token.split('.')[1].replace(/-/g, '+').replace(/_/g, '/')
    const bytes = new Uint8Array(uni.base64ToArrayBuffer(part.padEnd(Math.ceil(part.length / 4) * 4, '=')))
    const id = Number(JSON.parse(String.fromCharCode(...bytes)).sub)
    return Number.isSafeInteger(id) && id > 0 ? id : null
  } catch {
    return null
  }
}

const existingId = subject(uni.getStorageSync('rtoken') || '')
const ownerKey = 'account:legacy-owner'
let legacyOwner = uni.getStorageSync(ownerKey)
if (legacyOwner === '' || legacyOwner == null) {
  // Keep old paths (including absolute comic image paths) for the session present
  // at upgrade. Unattributed files on a signed-out device stay unassigned.
  legacyOwner = existingId || 'unassigned'
  uni.setStorageSync(ownerKey, legacyOwner)
}

function scope(id: number | null) {
  return id === null ? null : id === legacyOwner ? '' : `-u${id}`
}

export const accountScope = ref(scope(existingId))
export const scopedKey = (key: string) => key + accountScope.value

export function bindLocalSession(refreshToken: string) {
  accountScope.value = scope(subject(refreshToken))
}
