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
  /** The source has no rules of its own: the server recognises its pages. */
  auto: boolean
  /** The subscription the source was last imported from. */
  subscriptionId?: number
  /** Recent failures to reach the source; null while it works. */
  health: { failures: number; lastError: string; lastErrorAt: number; pausedUntil: number } | null
}

export interface Subscription {
  id: number
  name: string
  url: string
  /** Unix seconds of the last sync that was tried. */
  syncedAt: number
  /** Why the last sync failed; empty when it worked. */
  error: string
  sources: number
}

export interface SyncResult {
  subscription: Subscription
  added: number
  updated: number
  /** Entries of the list that are not valid rules. */
  skipped: { name: string; error: string }[]
}

/** What a trial run of a source found, up to the step that failed. */
export interface TestReport {
  failedStep?: 'search' | 'recommend' | 'detail' | 'toc' | 'content'
  error?: string
  searchUrl?: string
  searchCount?: number
  book?: { title: string }
  detail?: { title: string; author: string; cover: string }
  chapterCount?: number
  firstChapter?: { title: string }
  content?: { text?: string; images?: string[] }
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

/** A recommended book: one entry however many sources list it. */
export interface FeedBook extends Book {
  sources: number
  reason?: string
}

export interface BookPage<T = Book> {
  total: number
  books: T[]
}

const q = encodeURIComponent

export const api = {
  /** Searches every enabled source, or only `source`; `exact` keeps the books of that very title. */
  search: (type: Mode, key: string, opt: { source?: number; exact?: boolean } = {}) =>
    request<SearchResult>(
      'GET',
      `/search?type=${type}&q=${q(key)}` + (opt.source ? `&source=${opt.source}` : '') + (opt.exact ? '&match=exact' : ''),
    ),
  /** One source's list, a page at a time. */
  rank: (source: number, offset: number, limit = 20) =>
    request<BookPage>('GET', `/rank?source=${source}&offset=${offset}&limit=${limit}`),
  /** Recommendations merged from the lists of all sources, without the books already read. */
  feed: (type: Mode, offset: number, limit = 20) =>
    request<BookPage<FeedBook>>('GET', `/feed?type=${type}&offset=${offset}&limit=${limit}`),
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
  createSources: (rules: unknown) => request<{ ids: number[]; updated?: number }>('POST', '/sources', rules),
  updateSource: (id: number, rule: unknown) => request('PUT', `/sources/${id}`, rule),
  setSourceEnabled: (id: number, enabled: boolean) => request('PUT', `/sources/${id}/enabled`, { enabled }),
  deleteSource: (id: number) => request('DELETE', `/sources/${id}`),
  /** Without a keyword the book to try comes from the source's home page or recommendations. */
  testSource: (rule: unknown, key: string) =>
    request<TestReport>('POST', key ? `/sources/test?q=${q(key)}` : '/sources/test', rule),

  subscriptions: () => request<Subscription[]>('GET', '/subscriptions'),
  /** Subscribing to a URL that is subscribed already syncs it. */
  subscribe: (url: string) => request<SyncResult>('POST', '/subscriptions', { url }),
  syncSubscription: (id: number) => request<SyncResult>('POST', `/subscriptions/${id}/sync`),
  deleteSubscription: (id: number, withSources: boolean) =>
    request('DELETE', `/subscriptions/${id}${withSources ? '?sources=1' : ''}`),
}
