// Stores the exact last-viewed screen position per book (chapter, comic page + pixel offset,
// or novel scroll offset + ratio) with automatic LRU eviction when capacity or storage is tight.
import { watch } from 'vue'
import { accountScope, scopedKey } from './accountScope'
import { evictOldestReadCache } from './readCache'

export interface ReadPosition {
  bookId: number
  chapterId: number
  chapterTitle: string
  /** Comic page index (0-based) or novel pixel offset inside chapter. */
  position: number
  /** Pixel offset inside the current comic page (or inside the novel chapter). */
  offsetPx: number
  /** Fractional scroll position (0..1) inside the chapter, resilient to font size changes. */
  ratio: number
  isComic: boolean
  /** Last viewed timestamp in ms; drives LRU eviction and freshness comparison. */
  at: number
}

const storageKey = () => scopedKey('readpos:v1')
const MAX_ENTRIES = 100

let records: Record<number, ReadPosition> = {}
watch(accountScope, scope => {
  records = scope === null ? {} : uni.getStorageSync(storageKey()) || {}
}, { immediate: true, flush: 'sync' })

function evictLeastRecent(targetCount: number) {
  const ids = Object.keys(records)
    .map(Number)
    .sort((a, b) => (records[a]?.at || 0) - (records[b]?.at || 0))
  while (ids.length > targetCount) {
    const oldest = ids.shift()
    if (oldest !== undefined) delete records[oldest]
  }
}

function persist() {
  if (Object.keys(records).length > MAX_ENTRIES) {
    evictLeastRecent(MAX_ENTRIES)
  }
  try {
    uni.setStorageSync(storageKey(), records)
    return
  } catch {
    // Storage quota tight: drop oldest 30% of reading positions first.
    const keep = Math.max(10, Math.floor(Object.keys(records).length * 0.7))
    evictLeastRecent(keep)
  }
  try {
    uni.setStorageSync(storageKey(), records)
    return
  } catch {
    // Still tight: evict oldest unused novel chapter cache entries and retry once more.
    evictOldestReadCache(10)
    try {
      uni.setStorageSync(storageKey(), records)
    } catch {
      // Give up silently; reading continues without crashing.
    }
  }
}

export function getReadPosition(bookId: number): ReadPosition | null {
  const entry = records[bookId]
  return entry ? { ...entry } : null
}

export function saveReadPosition(pos: Omit<ReadPosition, 'at'>) {
  if (accountScope.value === null || !pos.bookId || !pos.chapterId) return
  records[pos.bookId] = { ...pos, at: Date.now() }
  persist()
}

export function removeReadPosition(bookId: number) {
  if (!records[bookId]) return
  delete records[bookId]
  persist()
}

export function readPositionCount(): number {
  return Object.keys(records).length
}

export function clearReadPositions() {
  if (accountScope.value === null) return
  records = {}
  try {
    uni.removeStorageSync(storageKey())
  } catch {
    // ignore
  }
}
