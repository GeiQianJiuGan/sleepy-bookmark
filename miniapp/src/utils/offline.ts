// A book id belongs to one source on the server. Keep that identity with the catalog
// and use chapter ids (never titles or positions) for both the queue and content files.
import { reactive, ref, shallowReactive } from 'vue'
import { api, type Book, type Chapter, type ChapterContent } from '@/api'
import { absoluteUrl, ApiError, hasSession } from '@/api/http'
import { privacyAgreed } from './privacy'
import { cachedChapter } from './readCache'

type BookInfo = Pick<Book, 'id' | 'type' | 'title' | 'author' | 'cover' | 'sourceName'> & { sourceId?: number }
export interface DownloadChapter {
  id: number
  idx: number
  title: string
  bytes?: number
  error?: string
}

export interface OfflineBook {
  book: BookInfo
  chapters: Record<number, DownloadChapter>
  pending: Set<number>
  pauseReason: string
}

const LIST_KEY = 'offline:books'
const bookKey = (id: number) => `offline:book:${id}`
let fs: UniApp.FileSystemManager | undefined
let root = ''
// #ifdef MP-WEIXIN
fs = uni.getFileSystemManager()
root = `${(uni as any).env.USER_DATA_PATH}/offline`
// #endif
export const offlineSupported = !!fs
const dir = (id: number) => `${root}/${id}`
const chapterFile = (bookId: number, id: number) => `${dir(bookId)}/${id}.json`

const library = shallowReactive<Record<number, OfflineBook>>({})
const active = ref<{ bookId: number; chapterId: number; stage: string } | null>(null)
export const networkAvailable = ref(true)
export const managerError = ref('')
let loaded = false
let running = false

function message(e: unknown) {
  return e instanceof Error ? e.message : (e as { errMsg?: string })?.errMsg || String(e)
}

function ensureDir(path: string) {
  try {
    fs!.accessSync(path)
  } catch {
    fs!.mkdirSync(path, true)
  }
}

function restore() {
  if (loaded || !fs) return
  for (const id of (uni.getStorageSync(LIST_KEY) || []) as number[]) {
    const meta = uni.getStorageSync(bookKey(id))
    if (!meta || library[id]) continue
    const chapters: Record<number, DownloadChapter> = {}
    // Existing installations stored completed chapters in the per-book storage key.
    for (const [key, old] of Object.entries(meta.chapters || {})) {
      chapters[Number(key)] = { ...(old as DownloadChapter), id: Number(key) }
    }
    ensureDir(dir(id))
    const files = new Set(fs.readdirSync(dir(id)))
    for (const name of files) {
      if (!/^index-\d+\.json$/.test(name)) continue
      Object.assign(chapters, JSON.parse(fs.readFileSync(`${dir(id)}/${name}`, 'utf8') as string))
    }
    for (const c of Object.values(chapters)) {
      if (c.bytes !== undefined && !files.has(`${c.id}.json`)) {
        delete c.bytes
        c.error = '本机章节文件缺失，请重新下载'
      }
    }
    const pending = Object.values(chapters).filter(c => c.bytes === undefined && !c.error).sort((a, b) => a.idx - b.idx)
    library[id] = reactive({ book: meta.book, chapters, pending: new Set(pending.map(c => c.id)), pauseReason: meta.pauseReason || '' })
  }
  loaded = true
}

export function offlineBooks() {
  restore()
  return Object.values(library)
}

export function offlineBook(id: number) {
  restore()
  return library[id] as OfflineBook | undefined
}

export function offlineChapters(id: number): Chapter[] {
  return Object.values(offlineBook(id)?.chapters || {}).filter(c => c.bytes !== undefined)
    .sort((a, b) => a.idx - b.idx)
    .map(c => ({ id: c.id, bookId: id, idx: c.idx, title: c.title, cached: true }))
}

export function readOfflineChapter(bookId: number, id: number): ChapterContent | null {
  const b = offlineBook(bookId)
  if (!fs || !b || b.chapters[id]?.bytes === undefined) return null
  try {
    const content: ChapterContent = JSON.parse(fs.readFileSync(chapterFile(bookId, id), 'utf8') as string)
    if (content.id !== id || content.bookId !== bookId) throw new Error('章节编号不匹配')
    return content
  } catch {
    const failed = { ...b.chapters[id], bytes: undefined, error: '本机章节文件无法读取，请重新下载' }
    b.chapters[id] = failed
    try { saveRows(b, [failed]) } catch (e) { managerError.value = message(e) }
    return null
  }
}

export function downloadStats(b: OfflineBook) {
  let done = 0
  let bytes = 0
  let failed = 0
  const rows = Object.values(b.chapters)
  for (const c of rows) {
    if (c.bytes !== undefined) {
      done++
      bytes += c.bytes
    } else if (c.error) failed++
  }
  return { done, bytes, failed, total: rows.length }
}

export function activeDownload(id: number) {
  return active.value?.bookId === id ? active.value : null
}

export function chapterStatus(b: OfflineBook, c: DownloadChapter) {
  if (c.bytes !== undefined) return '已离线'
  if (c.error) return '下载失败'
  const current = activeDownload(b.book.id)
  if (current?.chapterId === c.id) return current.stage
  if (b.pauseReason) return '已暂停'
  if (managerError.value) return '等待继续'
  return networkAvailable.value ? '排队中' : '等待网络'
}

// Only rewrite the affected blocks, including for books with thousands of chapters.
// Rename after writing so an interrupted write leaves the previous index readable.
function saveRows(b: OfflineBook, rows: DownloadChapter[]) {
  const blocks = new Map<number, DownloadChapter[]>()
  for (const c of rows) {
    const block = Math.floor(c.id / 128)
    const batch = blocks.get(block)
    if (batch) batch.push(c)
    else blocks.set(block, [c])
  }
  for (const [block, changes] of blocks) {
    const path = `${dir(b.book.id)}/index-${block}.json`
    const entries: Record<number, DownloadChapter> = {}
    for (let id = block * 128; id < (block + 1) * 128; id++) {
      if (b.chapters[id]) entries[id] = b.chapters[id]
    }
    for (const c of changes) entries[c.id] = c
    fs!.writeFileSync(path + '.tmp', JSON.stringify(entries), 'utf8')
    fs!.renameSync(path + '.tmp', path)
    for (const c of changes) {
      b.chapters[c.id] = c
      if (c.bytes === undefined && !c.error) b.pending.add(c.id)
      else b.pending.delete(c.id)
    }
  }
}

function saveMeta(b: OfflineBook) {
  // Keep the old index until its chapters have been migrated into blocks.
  const old = uni.getStorageSync(bookKey(b.book.id))
  uni.setStorageSync(bookKey(b.book.id), { ...old, book: b.book, pauseReason: b.pauseReason })
}

export function enqueueChapters(book: BookInfo, chapters: Chapter[]) {
  if (!fs) throw new Error('当前平台不支持离线下载')
  if (!book.sourceId) throw new Error('请联网打开这本书后再添加下载')
  if (chapters.some(c => c.bookId !== book.id)) throw new Error('章节不属于当前书源的这本书')
  let b = offlineBook(book.id)
  if (b?.book.sourceId && b.book.sourceId !== book.sourceId) throw new Error('本机书源信息与目录不一致')
  const { id, type, title, author, cover, sourceId, sourceName } = book
  const info = { id, type, title, author, cover, sourceId, sourceName }
  if (!b) {
    ensureDir(dir(id))
    b = reactive({ book: info, chapters: {}, pending: new Set<number>(), pauseReason: '' })
    saveMeta(b)
    uni.setStorageSync(LIST_KEY, [...Object.keys(library).map(Number), id])
    library[id] = b
  } else {
    b.book = info
    saveMeta(b)
  }
  let added = 0
  const changes: DownloadChapter[] = []
  for (const c of chapters) {
    const old = b.chapters[c.id]
    if (old && !old.error && old.title === c.title && old.idx === c.idx) continue
    if (!old || old.error) added++
    changes.push({ id: c.id, idx: c.idx, title: c.title, bytes: old?.bytes })
  }
  saveRows(b, changes)
  resumeDownloads()
  return added
}

function downloadImage(url: string): Promise<string> {
  return new Promise((resolve, reject) => {
    uni.downloadFile({
      url: absoluteUrl(url),
      success: res => res.statusCode === 200 ? resolve(res.tempFilePath) : reject(new Error(`图片下载失败（${res.statusCode}）`)),
      fail: e => reject(new Error(e.errMsg)),
    })
  })
}

async function saveChapter(b: OfflineBook, id: number) {
  const bookId = b.book.id
  const c = (b.book.type === 'novel' && cachedChapter(id)) || await api.chapter(id)
  if (library[bookId] !== b) return
  if (c.bookId !== bookId || c.id !== id) throw new Error('返回的章节与下载目录不一致')
  const content = { ...c }
  let bytes = 0
  const images: string[] = []
  const path = chapterFile(bookId, id)
  try {
    if (b.book.type === 'comic') {
      if (!c.images?.length) throw new Error('书源未返回漫画图片')
      for (const [i, url] of c.images.entries()) {
        active.value!.stage = `下载图片 ${i + 1}/${c.images.length}`
        const tmp = await downloadImage(url)
        if (library[bookId] !== b) return
        const imagePath = `${dir(bookId)}/${id}_${i}.img`
        images.push(imagePath)
        fs!.copyFileSync(tmp, imagePath)
        bytes += (fs!.statSync(imagePath) as UniApp.Stats).size
      }
      content.images = images
      delete content.origins
    } else if (!c.text?.trim()) throw new Error('书源未返回章节正文')
    active.value!.stage = '保存到本机'
    fs!.writeFileSync(path, JSON.stringify(content), 'utf8')
    bytes += (fs!.statSync(path) as UniApp.Stats).size
    saveRows(b, [{ ...b.chapters[id], bytes, error: undefined }])
  } catch (e) {
    for (const file of [...images, path]) {
      try { fs!.unlinkSync(file) } catch { /* file may not have been written */ }
    }
    throw e
  }
}

async function drain() {
  if (running || !fs) return
  running = true
  try {
    while (networkAvailable.value && hasSession() && privacyAgreed()) {
      const b = offlineBooks().find(book => !book.pauseReason && book.pending.size)
      if (!b) break
      const id = b.pending.values().next().value as number
      active.value = { bookId: b.book.id, chapterId: id, stage: b.book.type === 'novel' ? '获取正文' : '获取画页' }
      try {
        await saveChapter(b, id)
        // Cached chapters can complete without any network IO. Let page updates run.
        await new Promise(resolve => setTimeout(resolve, 0))
      } catch (e) {
        if (library[b.book.id] !== b) continue
        if (!networkAvailable.value || !hasSession()) break
        // A server connection failure affects the whole queue. Leave this chapter queued.
        if (e instanceof ApiError && e.status === 0) {
          managerError.value = message(e)
          break
        }
        try {
          saveRows(b, [{ ...b.chapters[id], error: message(e) }])
        } catch (writeError) {
          b.pauseReason = '本机无法保存下载进度：' + message(writeError)
          saveMeta(b)
        }
      }
    }
  } catch (e) {
    managerError.value = message(e)
  } finally {
    active.value = null
    running = false
  }
}

export function resumeDownloads() {
  managerError.value = ''
  void drain()
}

export function setDownloadsPaused(b: OfflineBook, paused: boolean) {
  b.pauseReason = paused ? '已暂停' : ''
  saveMeta(b)
  if (!paused) resumeDownloads()
}

export function retryDownloads(b: OfflineBook, chapterId?: number) {
  const failed = Object.values(b.chapters).filter(c => c.error && (!chapterId || c.id === chapterId))
  saveRows(b, failed.map(c => ({ ...c, error: undefined })))
  setDownloadsPaused(b, false)
}

export function removeOfflineBook(id: number) {
  const b = offlineBook(id)
  if (!b) return
  // An in-flight response checks object identity before writing, even if re-added.
  fs!.rmdirSync(dir(id), true)
  delete library[id]
  uni.setStorageSync(LIST_KEY, Object.keys(library).map(Number))
  uni.removeStorageSync(bookKey(id))
}

export function initDownloads() {
  if (!fs) return
  uni.onNetworkStatusChange(res => {
    networkRevision++
    networkAvailable.value = res.isConnected
    if (res.isConnected) resumeDownloads()
  })
}

let networkRevision = 0

/** A suspended app can miss a connectivity event. Sample again on every app show. */
export function wakeDownloads() {
  if (!fs) return
  const revision = ++networkRevision
  uni.getNetworkType({
    success: res => {
      if (revision !== networkRevision) return
      networkAvailable.value = res.networkType !== 'none'
      if (networkAvailable.value) resumeDownloads()
    },
    fail: () => {
      if (revision === networkRevision) resumeDownloads()
    },
  })
}

export function formatBytes(n: number): string {
  if (n < 1024 * 1024) return (n / 1024).toFixed(0) + ' KB'
  return (n / 1024 / 1024).toFixed(1) + ' MB'
}
