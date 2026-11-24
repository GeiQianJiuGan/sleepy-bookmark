// Offline downloads live in the mini program's user data directory (about 200 MB in total).
// Layout: <root>/<bookId>/<chapterId>.json plus <chapterId>_<n>.img for comic pages.
// A small per-book index is kept in storage so the download list works without a network.
import { api, type Book, type Chapter, type ChapterContent } from '@/api'
import { absoluteUrl } from '@/api/http'

export interface OfflineBook {
  book: Pick<Book, 'id' | 'type' | 'title' | 'author' | 'cover' | 'sourceName'>
  /** Downloaded chapters by id. */
  chapters: Record<number, { idx: number; title: string; bytes: number }>
}

const LIST_KEY = 'offline:books'
const bookKey = (id: number) => `offline:book:${id}`

let supported = false
let fs: any = null
let ROOT = ''
// #ifdef MP-WEIXIN
supported = true
fs = uni.getFileSystemManager()
ROOT = `${(uni as any).env.USER_DATA_PATH}/offline`
// #endif
export const offlineSupported = supported

const dir = (bookId: number) => `${ROOT}/${bookId}`
const chapterFile = (bookId: number, chapterId: number) => `${dir(bookId)}/${chapterId}.json`

export function offlineBookIds(): number[] {
  return uni.getStorageSync(LIST_KEY) || []
}

export function offlineBook(bookId: number): OfflineBook | null {
  return uni.getStorageSync(bookKey(bookId)) || null
}

export function offlineBooks(): OfflineBook[] {
  return offlineBookIds()
    .map(offlineBook)
    .filter((b): b is OfflineBook => !!b)
}

export function offlineBytes(b: OfflineBook): number {
  return Object.values(b.chapters).reduce((sum, c) => sum + c.bytes, 0)
}

/** The downloaded chapters of a book as a chapter list, for reading without a network. */
export function offlineChapters(bookId: number): Chapter[] {
  const b = offlineBook(bookId)
  if (!b) return []
  return Object.entries(b.chapters)
    .map(([id, c]) => ({ id: Number(id), bookId, idx: c.idx, title: c.title, cached: true }))
    .sort((a, b) => a.idx - b.idx)
}

export function readOfflineChapter(bookId: number, chapterId: number): ChapterContent | null {
  if (!offlineSupported || !offlineBook(bookId)?.chapters[chapterId]) return null
  try {
    return JSON.parse(fs.readFileSync(chapterFile(bookId, chapterId), 'utf8') as string)
  } catch {
    return null
  }
}

function fileSize(path: string): number {
  try {
    return (fs.statSync(path) as any).size || 0
  } catch {
    return 0
  }
}

function downloadTo(url: string, filePath: string): Promise<void> {
  return new Promise((resolve, reject) => {
    uni.downloadFile({
      url,
      filePath,
      success: (res) => (res.statusCode === 200 ? resolve() : reject(new Error(`图片下载失败（${res.statusCode}）`))),
      fail: (err) => reject(new Error('图片下载失败：' + (err.errMsg || ''))),
    } as UniApp.DownloadFileOption)
  })
}

async function saveChapter(entry: OfflineBook, ch: Chapter) {
  const bookId = entry.book.id
  const content = await api.chapter(ch.id)
  let bytes = 0
  if (content.images) {
    const local: string[] = []
    for (let i = 0; i < content.images.length; i++) {
      const path = `${dir(bookId)}/${ch.id}_${i}.img`
      await downloadTo(absoluteUrl(content.images[i]), path)
      bytes += fileSize(path)
      local.push(path)
    }
    content.images = local
    delete content.origins
  }
  const path = chapterFile(bookId, ch.id)
  fs.writeFileSync(path, JSON.stringify(content), 'utf8')
  entry.chapters[ch.id] = { idx: ch.idx, title: ch.title, bytes: bytes + fileSize(path) }
}

/**
 * Downloads the given chapters of a book, skipping the ones already on the device.
 * Returns the number of chapters that failed. Set `signal.cancelled` to stop early.
 */
export async function downloadChapters(
  book: OfflineBook['book'],
  chapters: Chapter[],
  onProgress: (done: number, total: number) => void,
  signal: { cancelled: boolean } = { cancelled: false },
): Promise<number> {
  if (!offlineSupported) throw new Error('当前平台不支持离线下载')
  const entry: OfflineBook = offlineBook(book.id) || {
    book: { id: book.id, type: book.type, title: book.title, author: book.author, cover: book.cover, sourceName: book.sourceName },
    chapters: {},
  }
  try {
    fs.mkdirSync(dir(book.id), true)
  } catch {
    // already exists
  }
  const ids = offlineBookIds()
  if (!ids.includes(book.id)) uni.setStorageSync(LIST_KEY, [...ids, book.id])

  let failed = 0
  for (let i = 0; i < chapters.length && !signal.cancelled; i++) {
    const saved = entry.chapters[chapters[i].id]
    if (saved) {
      // Follow the catalog: the source may have inserted or renamed chapters since the download.
      saved.idx = chapters[i].idx
      saved.title = chapters[i].title
    } else {
      try {
        await saveChapter(entry, chapters[i])
      } catch (e) {
        console.warn('offline download failed', chapters[i].title, e)
        failed++
      }
      // Persist as we go so an interrupted download keeps what it already has.
      uni.setStorageSync(bookKey(book.id), entry)
    }
    onProgress(i + 1, chapters.length)
  }
  uni.setStorageSync(bookKey(book.id), entry)
  return failed
}

export function removeOfflineBook(bookId: number) {
  if (offlineSupported) {
    try {
      fs.rmdirSync(dir(bookId), true)
    } catch {
      // nothing on disk
    }
  }
  uni.removeStorageSync(bookKey(bookId))
  uni.setStorageSync(
    LIST_KEY,
    offlineBookIds().filter((id) => id !== bookId),
  )
}

export function formatBytes(n: number): string {
  if (n < 1024 * 1024) return (n / 1024).toFixed(0) + ' KB'
  return (n / 1024 / 1024).toFixed(1) + ' MB'
}
