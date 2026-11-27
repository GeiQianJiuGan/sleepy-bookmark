// The reading cache keeps the text of novel chapters on the device: the ones that were read and
// the few after them, so that reading on is instant and survives a bad connection. It has a size
// limit, and the chapters read longest ago make room for new ones.
// Offline downloads are a separate store (utils/offline.ts) that nothing evicts, and comics are
// not cached on the device at all: their pages are too big for it.
import { ref, watch } from 'vue'
import { accountScope, scopedKey } from './accountScope'
import type { ChapterContent } from '@/api'

interface Entry {
  bytes: number
  at: number
}

const indexKey = () => scopedKey('readcache:index')
const storageKey = (id: number) => scopedKey(`readcache:${id}`)
let fs: UniApp.FileSystemManager | undefined
let ROOT = ''
let limit = 2 * 1024 * 1024
// #ifdef MP-WEIXIN
fs = uni.getFileSystemManager()
ROOT = `${(uni as any).env.USER_DATA_PATH}/readcache`
limit = 30 * 1024 * 1024
// #endif
export const READ_CACHE_LIMIT = limit

const file = (id: number) => `${scopedKey(ROOT)}/${id}.json`
let index: Record<number, Entry> = {}
const version = ref(0)
watch(accountScope, scope => {
  index = scope === null ? {} : uni.getStorageSync(indexKey()) || {}
  version.value++
  if (fs && scope !== null) {
    try { fs.mkdirSync(scopedKey(ROOT), true) } catch { /* already exists */ }
  }
}, { immediate: true, flush: 'sync' })
const saveIndex = () => {
  version.value++
  if (accountScope.value !== null) uni.setStorageSync(indexKey(), index)
}

function utf8Bytes(s: string): number {
  let n = 0
  for (let i = 0; i < s.length; i++) {
    const c = s.charCodeAt(i)
    n += c < 0x80 ? 1 : c < 0x800 ? 2 : 3
  }
  return n
}

function drop(id: number) {
  try {
    if (fs) fs.unlinkSync(file(id))
    else uni.removeStorageSync(storageKey(id))
  } catch {
    // already gone
  }
  delete index[id]
}

export const readCacheBytes = () => {
  void version.value
  return Object.values(index).reduce((sum, e) => sum + e.bytes, 0)
}
export const readCacheCount = () => {
  void version.value
  return Object.keys(index).length
}
export const inReadCache = (id: number) => {
  void version.value
  return !!index[id]
}

/** Returns a cached chapter and marks it as just read. */
export function cachedChapter(id: number): ChapterContent | null {
  if (!index[id]) return null
  try {
    const raw = fs ? (fs.readFileSync(file(id), 'utf8') as string) : uni.getStorageSync(storageKey(id))
    const c = JSON.parse(raw) as ChapterContent
    index[id].at = Date.now()
    saveIndex()
    return c
  } catch {
    drop(id)
    saveIndex()
    return null
  }
}

/** Stores a novel chapter, evicting the chapters read longest ago until it fits. */
export function cacheChapter(c: ChapterContent) {
  if (!c.text || accountScope.value === null) return
  const raw = JSON.stringify(c)
  const bytes = utf8Bytes(raw)
  if (bytes > limit) return
  const others = Object.keys(index)
    .map(Number)
    .filter((id) => id !== c.id)
    .sort((a, b) => index[a].at - index[b].at)
  let total = readCacheBytes() - (index[c.id]?.bytes || 0)
  while (total + bytes > limit && others.length) {
    const id = others.shift()!
    total -= index[id].bytes
    drop(id)
  }
  try {
    if (fs) fs.writeFileSync(file(c.id), raw, 'utf8')
    else uni.setStorageSync(storageKey(c.id), raw)
    index[c.id] = { bytes, at: Date.now() }
  } catch {
    // The device is full: reading goes on from the server.
    drop(c.id)
  }
  saveIndex()
}

export function clearReadCache() {
  Object.keys(index).map(Number).forEach(drop)
  saveIndex()
}

/** Drops the least-recently-read novel cache entries to free local storage when space runs low. */
export function evictOldestReadCache(count = 5): number {
  const oldest = Object.keys(index)
    .map(Number)
    .sort((a, b) => index[a].at - index[b].at)
    .slice(0, count)
  oldest.forEach(drop)
  if (oldest.length) saveIndex()
  return oldest.length
}
