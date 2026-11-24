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
  errors: { source: string; error: string }[]
}

const q = encodeURIComponent

export const api = {
  search: (type: Mode, key: string, page = 1) =>
    request<SearchResult>('GET', `/search?type=${type}&q=${q(key)}&page=${page}`),
  book: (id: number) => request<Book>('GET', `/books/${id}`),
  chapters: (bookId: number, refresh = false) =>
    request<Chapter[]>('GET', `/books/${bookId}/chapters${refresh ? '?refresh=1' : ''}`),
  chapter: (id: number) => request<ChapterContent>('GET', `/chapters/${id}`),
  cacheBook: (bookId: number) => request<Task>('POST', `/books/${bookId}/cache`),
  tasks: () => request<Task[]>('GET', '/tasks'),

  favorites: (type: Mode) => request<Book[]>('GET', `/favorites?type=${type}`),
  addFavorite: (bookId: number) => request('PUT', `/favorites/${bookId}`),
  removeFavorite: (bookId: number) => request('DELETE', `/favorites/${bookId}`),

  history: (type: Mode) => request<Book[]>('GET', `/history?type=${type}`),
  saveProgress: (chapterId: number, position: number) => request('PUT', '/history', { chapterId, position }),
  removeHistory: (bookId?: number) => request('DELETE', bookId ? `/history/${bookId}` : '/history'),

  sources: () => request<Source[]>('GET', '/sources'),
  createSources: (rules: unknown) => request<{ ids: number[] }>('POST', '/sources', rules),
  updateSource: (id: number, rule: unknown) => request('PUT', `/sources/${id}`, rule),
  setSourceEnabled: (id: number, enabled: boolean) => request('PUT', `/sources/${id}/enabled`, { enabled }),
  deleteSource: (id: number) => request('DELETE', `/sources/${id}`),
  testSource: (rule: unknown, key: string) =>
    request<Record<string, unknown>>('POST', `/sources/test?q=${q(key)}`, rule),
}
