import { request } from './http'

export type Mode = 'novel' | 'comic'

export interface Progress {
  chapterId: number
  chapterIdx: number
  chapterTitle: string
  position: number
  updatedAt: number
}

export interface Book {
  id: number
  sourceId: number
  sourceName: string
  type: Mode
  title: string
  author: string
  cover: string
  intro: string
  latest: string
  favorite?: boolean
  chapterCount?: number
  progress?: Progress | null
}

export interface Chapter {
  id: number
  bookId: number
  idx: number
  title: string
  cached: boolean
}

export interface ChapterContent extends Chapter {
  /** Novel text, one paragraph per line. */
  text?: string
  /** Comic pages: server-relative proxy URLs, or local file paths for offline chapters. */
  images?: string[]
  /** The same pages at the source site, for loading them without the server in between. */
  origins?: string[]
  prevId: number
  nextId: number
}

export interface Source {
  id: number
  name: string
  type: Mode
  enabled: boolean
  updatedAt: number
  rule: Record<string, unknown>
  /** Recent failures to reach the source; null while it works. */
  health: { failures: number; lastError: string; lastErrorAt: number; pausedUntil: number } | null
}

export interface Task {
  id: number
  bookId: number
  title: string
  status: 'queued' | 'running' | 'done' | 'failed'
  total: number
  done: number
  failed: number
  error?: string
}

export interface SearchResult {
  books: Book[]
  /** Sources that returned nothing; kind is "paused" for one skipped after repeated failures. */
  errors: { source: string; error: string; kind: string }[]
}

export interface Recommendation {
  sourceId: number
  sourceName: string
  books: Book[]
}

const q = encodeURIComponent

export const api = {
  /** Searches every enabled source, or only `source`; `exact` keeps the books of that very title. */
  search: (type: Mode, key: string, opt: { source?: number; exact?: boolean } = {}) =>
    request<SearchResult>(
      'GET',
      `/search?type=${type}&q=${q(key)}` + (opt.source ? `&source=${opt.source}` : '') + (opt.exact ? '&match=exact' : ''),
    ),
  /** The first books of one source's recommendation list; empty when it has none or cannot be read. */
  recommend: (type: Mode, source: number) =>
    request<Recommendation[]>('GET', `/recommend?type=${type}&source=${source}`),
  book: (id: number) => request<Book>('GET', `/books/${id}`),
  chapters: (bookId: number, refresh = false) =>
    request<Chapter[]>('GET', `/books/${bookId}/chapters${refresh ? '?refresh=1' : ''}`),
  chapter: (id: number) => request<ChapterContent>('GET', `/chapters/${id}`),
  /** The same book in other sources. */
  alternatives: (bookId: number) => request<Book[]>('GET', `/books/${bookId}/alternatives`),
  /** Moves the shelf entry and reading progress of a book to the same book in another source. */
  switchSource: (bookId: number, to: number) => request('POST', `/books/${bookId}/switch`, { to }),
  cacheBook: (bookId: number) => request<Task>('POST', `/books/${bookId}/cache`),
  tasks: () => request<Task[]>('GET', '/tasks'),

  favorites: (type: Mode) => request<Book[]>('GET', `/favorites?type=${type}`),
  addFavorite: (bookId: number) => request('PUT', `/favorites/${bookId}`),
  removeFavorite: (bookId: number) => request('DELETE', `/favorites/${bookId}`),

  history: (type: Mode) => request<Book[]>('GET', `/history?type=${type}`),
  saveProgress: (chapterId: number, position: number) => request('PUT', '/history', { chapterId, position }),
  removeHistory: (bookId?: number) => request('DELETE', bookId ? `/history/${bookId}` : '/history'),

  sources: (type?: Mode) => request<Source[]>('GET', type ? `/sources?type=${type}` : '/sources'),
  createSources: (rules: unknown) => request<{ ids: number[] }>('POST', '/sources', rules),
  updateSource: (id: number, rule: unknown) => request('PUT', `/sources/${id}`, rule),
  setSourceEnabled: (id: number, enabled: boolean) => request('PUT', `/sources/${id}/enabled`, { enabled }),
  deleteSource: (id: number) => request('DELETE', `/sources/${id}`),
  testSource: (rule: unknown, key: string) =>
    request<Record<string, unknown>>('POST', `/sources/test?q=${q(key)}`, rule),
}
