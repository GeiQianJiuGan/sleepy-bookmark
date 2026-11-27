import type { Chapter } from '@/api'
import { chapterStatus, type OfflineBook } from './offline'
import { inReadCache } from './readCache'

export type ChapterBadgeKind = 'downloaded' | 'downloading' | 'failed' | 'cached' | 'server'

export interface ChapterBadge {
  kind: ChapterBadgeKind
  label: string
  /** True when the chapter content is available on this device without network access. */
  local: boolean
}

export interface ChapterStatusSummary {
  downloaded: number
  cached: number
  serverCached: number
  localTotal: number
  total: number
}

/**
 * Returns the unified download/cache badge for a chapter across all TOC views.
 * Priority: offline download state > local reading cache > server cache.
 */
export function getChapterBadge(
  chapter: Pick<Chapter, 'id' | 'cached'>,
  localBook?: OfflineBook | null,
): ChapterBadge | null {
  const dc = localBook?.chapters[chapter.id]
  if (dc) {
    if (dc.bytes !== undefined) return { kind: 'downloaded', label: '已下载', local: true }
    if (dc.error) return { kind: 'failed', label: '下载失败', local: false }
    return { kind: 'downloading', label: chapterStatus(localBook, dc), local: false }
  }
  if (inReadCache(chapter.id)) {
    return { kind: 'cached', label: '已缓存', local: true }
  }
  if (chapter.cached) {
    return { kind: 'server', label: '云端缓存', local: false }
  }
  return null
}

/** True if the chapter is either offline-downloaded or in local reading cache. */
export function isChapterLocal(chapterId: number, localBook?: OfflineBook | null): boolean {
  if (localBook?.chapters[chapterId]?.bytes !== undefined) return true
  return inReadCache(chapterId)
}

/** Counts downloaded, locally cached, and server-cached chapters without double-counting. */
export function summarizeChapterStatuses(
  chapters: Pick<Chapter, 'id' | 'cached'>[],
  localBook?: OfflineBook | null,
): ChapterStatusSummary {
  let downloaded = 0
  let cached = 0
  let serverCached = 0
  for (const c of chapters) {
    if (localBook?.chapters[c.id]?.bytes !== undefined) {
      downloaded++
    } else if (inReadCache(c.id)) {
      cached++
    } else if (c.cached) {
      serverCached++
    }
  }
  return {
    downloaded,
    cached,
    serverCached,
    localTotal: downloaded + cached,
    total: chapters.length,
  }
}
