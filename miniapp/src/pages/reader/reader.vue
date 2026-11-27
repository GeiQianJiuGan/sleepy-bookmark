<script setup lang="ts">
import { computed, getCurrentInstance, nextTick, ref, watch } from 'vue'
import { onHide, onLoad, onReady, onUnload } from '@dcloudio/uni-app'
import { api, type Chapter, type ChapterContent } from '@/api'
import { absoluteUrl, errorText, sourceDown, toastError } from '@/api/http'
import {
  COMIC_MODES,
  LINE_HEIGHTS,
  PAGE_MODES,
  THEMES,
  useAppStore,
  type ComicMode,
  type PageMode,
  type Theme,
} from '@/stores/app'
import {
  activeDownload,
  downloadStats,
  enqueueChapters,
  offlineBook,
  offlineChapters,
  offlineSupported,
  readOfflineChapter,
  type OfflineBook,
} from '@/utils/offline'
import { cacheChapter, cachedChapter, inReadCache } from '@/utils/readCache'
import { getChapterBadge, summarizeChapterStatuses } from '@/utils/chapterStatus'
import { accountScope } from '@/utils/accountScope'
import { getReadPosition, saveReadPosition } from '@/utils/readPosition'

interface Loaded {
  id: number
  title: string
  nextId: number
  paragraphs?: string[]
  images?: string[]
  /** The pages through the server, to fall back on when one does not load from the source site. */
  proxied?: string[]
}

interface NovelPageLine {
  text: string
  indent: boolean
}

interface NovelPage {
  isFirst: boolean
  title: string
  lines: NovelPageLine[]
}

type PanelMode = 'main' | 'cache' | 'page' | 'settings'

const AHEAD = 3
const PROBE_TIMEOUT = 4000

const store = useAppStore()
const instance = getCurrentInstance()
const theme = computed(() => THEMES[store.reader.theme])
const night = computed(() => store.reader.theme === 'dark')
const themeNames = Object.keys(THEMES) as Theme[]

const win = uni.getSystemInfoSync()
const statusBar = win.statusBarHeight || 0
let barHeight = 44
const tipHeight = 26
let tipTop = statusBar + Math.round((barHeight - tipHeight) / 2)
// #ifdef MP-WEIXIN
const capsule = uni.getMenuButtonBoundingClientRect()
barHeight = (capsule.top - statusBar) * 2 + capsule.height
// Vertically centered on the exact same row as the WeChat capsule button.
tipTop = Math.round(capsule.top + (capsule.height - tipHeight) / 2)
// #endif
const novelPadTop = statusBar + barHeight + 8
const comicPadTop = statusBar

function paintBar() {
  // #ifdef MP-WEIXIN
  uni.setNavigationBarColor({ frontColor: night.value ? '#ffffff' : '#000000', backgroundColor: theme.value.bg })
  // #endif
}
onReady(paintBar)
watch(theme, paintBar)

let readerScope: string | null = null
let bookId = 0
const bookMeta = ref<OfflineBook['book'] | null>(null)
const isComic = ref(false)
const favorite = ref(false)
const togglingFav = ref(false)
const chapters = ref<Chapter[]>([])
const loaded = ref<Loaded[]>([])
const loading = ref(false)
const error = ref('')
const canSwitch = ref(false)

// Menu & sub-panels: 'main' shows the 4 circular quick buttons + chapter progress bar.
const menu = ref(false)
const panel = ref<PanelMode>('main')
const catalog = ref(false)
const catalogIntoView = ref('')

// Reading modes: vertical scroll vs horizontal paged.
const isNovelPaged = computed(() => !isComic.value && store.reader.pageMode !== 'scroll')
const isComicPaged = computed(() => isComic.value && store.reader.comicMode !== 'scroll')
const useScrollView = computed(() => !isNovelPaged.value && !isComicPaged.value)

// Current reading state.
let current = { chapterId: 0, position: 0 }
let saved = { chapterId: 0, position: 0 }

const nextId = computed(() => loaded.value[loaded.value.length - 1]?.nextId || 0)
const currentId = ref(0)
const currentPage = ref(0)
const currentTotalPages = ref(0)
const currentIndex = computed(() => chapters.value.findIndex((c) => c.id === currentId.value))
const activeChapter = computed(() => loaded.value.find((c) => c.id === currentId.value) || loaded.value[0] || null)

const title = computed(() => {
  const fromCatalog = chapters.value[currentIndex.value]?.title
  if (fromCatalog) return fromCatalog
  return activeChapter.value?.title || ''
})

// Scroll-mode chapter geometry for the current-chapter progress bar (0..100).
const chapterPercent = ref(0)
let activeChapterTop = 0
let activeChapterHeight = 1
let activeChapterIsFirst = true

// --- Novel Horizontal Pagination & Page-Turn Animations ---
const novelPageIdx = ref(0)
const novelPages = computed<NovelPage[]>(() => {
  if (!isNovelPaged.value) return []
  const ch = activeChapter.value
  if (!ch || ch.images?.length) return []
  const paras = (ch.paragraphs || []).map((p) => p.trim()).filter(Boolean)
  const fontSize = store.reader.fontSize
  const lineH = fontSize * store.reader.lineHeight
  const contentW = Math.max(200, win.windowWidth - 36)
  const contentH = Math.max(240, win.windowHeight - (novelPadTop + 36))
  const cpl = Math.max(10, Math.floor(contentW / fontSize))
  const maxUnits = Math.max(6, Math.floor(contentH / lineH))

  const pages: NovelPage[] = []
  let lines: NovelPageLine[] = []
  let used = 2.6 // reserve room for chapter title on page 0

  const flush = () => {
    pages.push({ isFirst: pages.length === 0, title: ch.title, lines })
    lines = []
    used = 0
  }

  for (const para of paras) {
    let text = para
    let firstChunk = true
    while (text.length > 0) {
      const cap = firstChunk ? Math.max(6, cpl - 2) : cpl
      const chunk = text.slice(0, cap)
      text = text.slice(cap)
      if (used + 1 > maxUnits && lines.length > 0) {
        flush()
      }
      lines.push({ text: chunk, indent: firstChunk })
      used += 1
      firstChunk = false
    }
    used += 0.45
  }
  if (lines.length || !pages.length) flush()
  return pages
})

watch(novelPages, (pages) => {
  if (!pages.length) return
  if (novelPageIdx.value >= pages.length) {
    novelPageIdx.value = pages.length - 1
  }
})

// Page-turn animation state for novel paged mode.
const turnAnim = ref<'' | 'next' | 'prev'>('')
const leavingPage = ref<NovelPage | null>(null)
const dragX = ref(0)
let animTimer: ReturnType<typeof setTimeout> | undefined

function triggerPageAnim(dir: 'next' | 'prev', prevSnapshot: NovelPage | null) {
  clearTimeout(animTimer)
  leavingPage.value = prevSnapshot
  turnAnim.value = dir
  animTimer = setTimeout(() => {
    turnAnim.value = ''
    leavingPage.value = null
  }, 280)
}

function goNovelPage(delta: number) {
  const total = novelPages.value.length
  if (!total) return
  const target = novelPageIdx.value + delta
  if (target >= 0 && target < total) {
    const snap = novelPages.value[novelPageIdx.value] || null
    novelPageIdx.value = target
    triggerPageAnim(delta > 0 ? 'next' : 'prev', snap)
    syncNovelPagedPosition()
    return
  }
  if (delta > 0) {
    const nextCh = chapters.value[currentIndex.value + 1]
    if (nextCh) show(nextCh.id, 0)
    else {
      stopAutoPlay()
      uni.showToast({ title: '已经是最后一章', icon: 'none' })
    }
  } else {
    const prevCh = chapters.value[currentIndex.value - 1]
    if (prevCh) show(prevCh.id, -1) // -1 signals landing on last page of previous chapter
    else uni.showToast({ title: '已经是第一章', icon: 'none' })
  }
}

function syncNovelPagedPosition() {
  if (!activeChapter.value) return
  const total = Math.max(1, novelPages.value.length)
  const ratio = total > 1 ? novelPageIdx.value / (total - 1) : 0
  chapterPercent.value = Math.round(ratio * 100)
  current = { chapterId: activeChapter.value.id, position: Math.round(ratio * 1000) }
  persistLocalPosition(ratio)
}

// Touch drag handling for novel paged mode.
let touchStartX = 0
let touchStartY = 0
let touchMoved = false

function onPagedTouchStart(e: any) {
  const t = e.touches?.[0]
  if (!t) return
  touchStartX = t.clientX
  touchStartY = t.clientY
  touchMoved = false
  dragX.value = 0
}

function onPagedTouchMove(e: any) {
  const t = e.touches?.[0]
  if (!t) return
  const dx = t.clientX - touchStartX
  const dy = t.clientY - touchStartY
  if (Math.abs(dx) > 12 && Math.abs(dx) > Math.abs(dy)) {
    touchMoved = true
    dragX.value = Math.max(-win.windowWidth * 0.45, Math.min(win.windowWidth * 0.45, dx))
  }
}

function onPagedTouchEnd(e: any) {
  if (!touchMoved) {
    dragX.value = 0
    return
  }
  const dx = dragX.value
  dragX.value = 0
  if (Math.abs(dx) > 48) {
    menu.value = false
    goNovelPage(dx < 0 ? 1 : -1)
  }
}

function onPagedTap(e: any) {
  if (touchMoved) return
  if (menu.value) {
    closeMenu()
    return
  }
  const x = e.detail?.x ?? e.changedTouches?.[0]?.clientX ?? win.windowWidth / 2
  const w = win.windowWidth
  if (x > w * 0.3 && x < w * 0.7) {
    openMenu()
    return
  }
  if (store.reader.tapNextOnly || x >= w * 0.7) goNovelPage(1)
  else goNovelPage(-1)
}

// --- Comic Paged Mode (LTR & RTL Manga) ---
const comicDisplayImages = computed(() => {
  const imgs = activeChapter.value?.images || []
  if (store.reader.comicMode === 'page-rtl') return [...imgs].reverse()
  return imgs
})

const comicSwiperCurrent = computed(() => {
  const n = comicDisplayImages.value.length
  if (!n) return 0
  const idx = Math.min(Math.max(0, currentPage.value), n - 1)
  return store.reader.comicMode === 'page-rtl' ? n - 1 - idx : idx
})

function onComicSwiperChange(e: any) {
  const swiperIdx = Number(e.detail?.current ?? 0)
  const n = comicDisplayImages.value.length
  if (!n || !activeChapter.value) return
  const realPage = store.reader.comicMode === 'page-rtl' ? n - 1 - swiperIdx : swiperIdx
  currentPage.value = realPage
  currentTotalPages.value = n
  chapterPercent.value = n > 1 ? Math.round((realPage / (n - 1)) * 100) : 100
  current = { chapterId: activeChapter.value.id, position: realPage }
  persistLocalPosition(n > 1 ? realPage / (n - 1) : 0)
}

function goComicPage(delta: number) {
  const n = activeChapter.value?.images?.length || 0
  if (!n) return
  const target = currentPage.value + delta
  if (target >= 0 && target < n) {
    currentPage.value = target
    chapterPercent.value = n > 1 ? Math.round((target / (n - 1)) * 100) : 100
    current = { chapterId: activeChapter.value!.id, position: target }
    persistLocalPosition(n > 1 ? target / (n - 1) : 0)
    return
  }
  if (delta > 0) {
    const nextCh = chapters.value[currentIndex.value + 1]
    if (nextCh) show(nextCh.id, 0)
    else {
      stopAutoPlay()
      uni.showToast({ title: '已经是最后一话', icon: 'none' })
    }
  } else {
    const prevCh = chapters.value[currentIndex.value - 1]
    if (prevCh) show(prevCh.id, -1)
    else uni.showToast({ title: '已经是第一话', icon: 'none' })
  }
}

function onComicPagedTap(e: any) {
  if (menu.value) {
    closeMenu()
    return
  }
  const x = e.detail?.x ?? e.changedTouches?.[0]?.clientX ?? win.windowWidth / 2
  const w = win.windowWidth
  if (x > w * 0.3 && x < w * 0.7) {
    openMenu()
    return
  }
  if (store.reader.tapNextOnly) {
    goComicPage(1)
    return
  }
  const rtl = store.reader.comicMode === 'page-rtl'
  if (x >= w * 0.7) goComicPage(rtl ? -1 : 1)
  else goComicPage(rtl ? 1 : -1)
}

// Floating corner-tip progress text.
const tipProgress = computed(() => {
  if (isComic.value && currentTotalPages.value > 0) {
    return `${Math.min(currentPage.value + 1, currentTotalPages.value)}/${currentTotalPages.value}`
  }
  if (isNovelPaged.value && novelPages.value.length > 0) {
    return `${Math.min(novelPageIdx.value + 1, novelPages.value.length)}/${novelPages.value.length}`
  }
  if (!isComic.value && chapterPercent.value > 0) {
    return `${chapterPercent.value}%`
  }
  return ''
})

// Current-chapter progress slider bindings.
const sliderMax = computed(() => {
  if (isComic.value) return Math.max(1, currentTotalPages.value - 1)
  if (isNovelPaged.value) return Math.max(1, novelPages.value.length - 1)
  return 100
})

const sliderValue = computed(() => {
  if (isComic.value) return Math.min(currentPage.value, sliderMax.value)
  if (isNovelPaged.value) return Math.min(novelPageIdx.value, sliderMax.value)
  return Math.min(100, Math.max(0, chapterPercent.value))
})

const progressLabel = computed(() => {
  if (isComic.value && currentTotalPages.value > 0) {
    return `第 ${Math.min(currentPage.value + 1, currentTotalPages.value)} / ${currentTotalPages.value} 页`
  }
  if (isNovelPaged.value && novelPages.value.length > 0) {
    return `第 ${Math.min(novelPageIdx.value + 1, novelPages.value.length)} / ${novelPages.value.length} 页`
  }
  return `本章进度 ${chapterPercent.value}%`
})

const scrubbing = ref(false)
const scrubPreview = ref('')

function onSliderChanging(e: any) {
  const v = Number(e.detail?.value ?? 0)
  scrubbing.value = true
  if (isComic.value && currentTotalPages.value > 0) {
    scrubPreview.value = `第 ${Math.min(v + 1, currentTotalPages.value)} / ${currentTotalPages.value} 页`
  } else if (isNovelPaged.value && novelPages.value.length > 0) {
    scrubPreview.value = `第 ${Math.min(v + 1, novelPages.value.length)} / ${novelPages.value.length} 页`
  } else {
    scrubPreview.value = `本章进度 ${v}%`
  }
}

function onSliderChange(e: any) {
  const v = Number(e.detail?.value ?? 0)
  scrubbing.value = false
  scrubPreview.value = ''
  if (isComic.value) {
    const maxP = Math.max(0, currentTotalPages.value - 1)
    const p = Math.min(Math.max(0, Math.round(v)), maxP)
    currentPage.value = p
    chapterPercent.value = maxP > 0 ? Math.round((p / maxP) * 100) : 100
    if (activeChapter.value) {
      current = { chapterId: activeChapter.value.id, position: p }
      persistLocalPosition(maxP > 0 ? p / maxP : 0)
      if (!isComicPaged.value) {
        scrollTo({ id: `p-${activeChapter.value.id}-${p}` })
      }
    }
    return
  }
  if (isNovelPaged.value) {
    const maxP = Math.max(0, novelPages.value.length - 1)
    novelPageIdx.value = Math.min(Math.max(0, Math.round(v)), maxP)
    syncNovelPagedPosition()
    return
  }
  // Novel scroll mode: jump within active chapter by percentage (0..100).
  const pct = Math.min(100, Math.max(0, v))
  chapterPercent.value = pct
  const originY = activeChapterIsFirst ? novelPadTop : 0
  const chapterStartScroll = Math.max(0, lastTop + activeChapterTop - originY)
  const scrollable = Math.max(0, activeChapterHeight - win.windowHeight)
  const targetTop = Math.round(chapterStartScroll + (pct / 100) * scrollable)
  scrollTo({ top: targetTop })
}

// --- Comic image route probe ---
let route: 'server' | 'direct' | '' = ''
const probe = ref<{ server: string; direct: string } | null>(null)
let settle: ((r: 'server' | 'direct') => void) | null = null

function pickRoute(server: string, direct: string): Promise<'server' | 'direct'> {
  return new Promise((resolve) => {
    const timer = setTimeout(() => settle?.('server'), PROBE_TIMEOUT)
    settle = (r) => {
      clearTimeout(timer)
      settle = null
      probe.value = null
      resolve(r)
    }
    probe.value = { server, direct }
  })
}

const probed = (r: 'server' | 'direct') => settle?.(r)

let directFailures = 0
function pageFailed(ch: Loaded, i: number) {
  if (!ch.images || !ch.proxied || ch.images[i] === ch.proxied[i]) return
  ch.images[i] = ch.proxied[i]
  if (++directFailures >= 3) route = 'server'
}

watch(
  () => store.reader.imageRoute,
  (r) => {
    if (r !== 'server') return
    route = ''
    for (const ch of loaded.value) {
      if (ch.proxied) ch.images = ch.proxied
      ch.proxied = undefined
    }
  },
)

/** Reads a chapter from the device if it is there, otherwise from the server. */
async function fetchChapter(id: number): Promise<Loaded> {
  let c: ChapterContent | null = readOfflineChapter(bookId, id)
  let origins: string[] | undefined
  if (!c && !isComic.value) c = cachedChapter(id)
  if (!c && offlineOnly) throw new Error('本机章节文件无法读取，请重新下载')
  if (!c) {
    c = await api.chapter(id)
    cacheChapter(c)
    origins = c.origins
  }
  const i = chapters.value.findIndex((ch) => ch.id === id)
  const imgs = c.images?.length ? c.images.map(absoluteUrl) : undefined
  const out: Loaded = {
    id: c.id,
    title: c.title || chapters.value[i]?.title || '',
    nextId: i >= 0 ? chapters.value[i + 1]?.id || 0 : c.nextId,
    paragraphs: c.text ? c.text.split('\n') : undefined,
    images: imgs,
  }
  if (out.images?.length && origins?.length === out.images.length && store.reader.imageRoute === 'auto') {
    route = route || (await pickRoute(out.images[0], origins[0]))
    if (route === 'direct') {
      out.proxied = out.images
      out.images = [...origins]
    }
  }
  if (out.paragraphs) warm(id)
  return out
}

let warming = false
async function warm(fromId: number) {
  if (warming || offlineOnly) return
  warming = true
  try {
    const i = chapters.value.findIndex((ch) => ch.id === fromId)
    for (const ch of i < 0 ? [] : chapters.value.slice(i + 1, i + 1 + AHEAD)) {
      if (inReadCache(ch.id) || readOfflineChapter(bookId, ch.id)) continue
      cacheChapter(await api.chapter(ch.id))
    }
  } catch {
    // quiet prefetch
  } finally {
    warming = false
  }
}

// --- Scrolling & position restoration ---
const scrollTop = ref(0)
const intoView = ref('')
let lastTop = 0
let restoringComic: { chapterId: number; page: number; until: number } | null = null
let reanchorTimer: ReturnType<typeof setTimeout> | undefined
let ignoreScrollUntil = 0

async function scrollTo(target: { top?: number; id?: string }) {
  ignoreScrollUntil = Date.now() + 260
  scrollTop.value = lastTop
  intoView.value = ''
  await nextTick()
  if (target.id) intoView.value = target.id
  else scrollTop.value = Math.max(0, target.top || 0)
}

function onPageLoad(ch: Loaded, i: number) {
  const rc = restoringComic
  if (!rc || Date.now() > rc.until || ch.id !== rc.chapterId || i > rc.page) return
  clearTimeout(reanchorTimer)
  reanchorTimer = setTimeout(() => {
    if (restoringComic && Date.now() <= restoringComic.until) {
      scrollTo({ id: `p-${rc.chapterId}-${rc.page}` })
    }
  }, 80)
}

let wanted = { chapterId: 0, position: 0 }

async function show(chapterId: number, position = 0, restoreRatio = 0) {
  if (loading.value) return
  restoringComic = null
  clearTimeout(reanchorTimer)
  wanted = { chapterId, position }
  loading.value = true
  error.value = ''
  try {
    const ch = await fetchChapter(chapterId)
    loaded.value = [ch]
    currentId.value = chapterId

    if (ch.images?.length) {
      const lastPage = ch.images.length - 1
      const pos = position < 0 ? lastPage : Math.min(Math.max(0, position), lastPage)
      current = { chapterId, position: pos }
      currentPage.value = pos
      currentTotalPages.value = ch.images.length
      chapterPercent.value = lastPage > 0 ? Math.round((pos / lastPage) * 100) : 100
      persistLocalPosition(lastPage > 0 ? pos / lastPage : 0)
      await nextTick()
      if (!isComicPaged.value) {
        if (pos > 0) {
          restoringComic = { chapterId, page: pos, until: Date.now() + 2500 }
          setTimeout(() => scrollTo({ id: `p-${chapterId}-${pos}` }), 60)
        } else {
          setTimeout(() => scrollTo({ top: 0 }), 30)
        }
      }
    } else if (isNovelPaged.value) {
      currentPage.value = 0
      currentTotalPages.value = 0
      await nextTick()
      const total = Math.max(1, novelPages.value.length)
      if (position < 0) {
        novelPageIdx.value = total - 1
      } else if (restoreRatio > 0) {
        novelPageIdx.value = Math.min(total - 1, Math.round(restoreRatio * (total - 1)))
      } else {
        novelPageIdx.value = 0
      }
      syncNovelPagedPosition()
    } else {
      const pos = Math.max(0, position)
      current = { chapterId, position: pos }
      currentPage.value = 0
      currentTotalPages.value = 0
      chapterPercent.value = 0
      persistLocalPosition(restoreRatio)
      await nextTick()
      if (pos > 0) {
        setTimeout(() => scrollTo({ top: pos }), 60)
      } else {
        setTimeout(() => scrollTo({ top: 0 }), 30)
      }
      setTimeout(measure, 220)
    }
  } catch (e) {
    error.value = errorText(e)
    canSwitch.value = sourceDown(e)
  } finally {
    loading.value = false
  }
}

function retry() {
  if (!opened) open()
  else if (loaded.value.length && useScrollView.value) appendNext()
  else show(wanted.chapterId, wanted.position)
}

async function appendNext() {
  if (loading.value || !nextId.value || !useScrollView.value) return
  loading.value = true
  error.value = ''
  try {
    loaded.value.push(await fetchChapter(nextId.value))
  } catch (e) {
    error.value = errorText(e)
    canSwitch.value = sourceDown(e)
  } finally {
    loading.value = false
  }
}

let opened = false
let firstChapter = 0
let offlineOnly = false

async function open() {
  let startId = firstChapter
  let position = 0
  let restoreRatio = 0
  loading.value = true
  error.value = ''
  let useLocal = offlineOnly
  let openError = '本机没有已下载的章节'
  if (!useLocal) {
    try {
      const [book, rawList] = await Promise.all([api.book(bookId), api.chapters(bookId)])
      const list = rawList || []
      bookMeta.value = {
        id: book.id,
        type: book.type,
        title: book.title,
        author: book.author,
        cover: book.cover,
        sourceName: book.sourceName,
        sourceId: book.sourceId,
      }
      isComic.value = book.type === 'comic'
      favorite.value = !!book.favorite
      chapters.value = list
      if (!startId) {
        const localPos = getReadPosition(bookId)
        const localValid = localPos && list.some((c) => c.id === localPos.chapterId) ? localPos : null
        const serverProg = book.progress && list.some((c) => c.id === book.progress!.chapterId) ? book.progress : null
        const serverAt = serverProg ? (serverProg.updatedAt || 0) * 1000 : 0
        if (localValid && (!serverProg || localValid.at >= serverAt - 2000)) {
          startId = localValid.chapterId
          position = localValid.position
          restoreRatio = localValid.ratio || 0
        } else if (serverProg) {
          startId = serverProg.chapterId
          position = serverProg.position
        }
      }
    } catch (e) {
      useLocal = true
      openError = errorText(e)
    }
  }
  if (useLocal) {
    const local = offlineBook(bookId)
    if (!local || !offlineChapters(bookId).length) {
      loading.value = false
      error.value = openError
      canSwitch.value = false
      return
    }
    bookMeta.value = local.book
    isComic.value = local.book.type === 'comic'
    chapters.value = offlineChapters(bookId)
    if (!startId) {
      const localPos = getReadPosition(bookId)
      if (localPos && chapters.value.some((c) => c.id === localPos.chapterId)) {
        startId = localPos.chapterId
        position = localPos.position
        restoreRatio = localPos.ratio || 0
      }
    }
  }
  opened = true
  loading.value = false
  startId = startId || chapters.value[0]?.id
  if (startId) show(startId, position, restoreRatio)
  else error.value = '这本书还没有目录'
}

onLoad((query) => {
  readerScope = accountScope.value
  bookId = Number(query?.bookId)
  offlineOnly = query?.offline === '1'
  firstChapter = Number(query?.chapterId) || 0
  open()
})

// Switching between scroll & paged mode re-opens current chapter cleanly.
watch(
  () => store.reader.pageMode,
  (next, prev) => {
    if (isComic.value || !currentId.value) return
    if ((next === 'scroll') !== (prev === 'scroll')) {
      show(currentId.value, 0, chapterPercent.value / 100)
    }
  },
)

watch(
  () => store.reader.comicMode,
  (next, prev) => {
    if (!isComic.value || !currentId.value) return
    if ((next === 'scroll') !== (prev === 'scroll')) {
      show(currentId.value, currentPage.value)
    }
  },
)

const toSwitch = () => {
  stopAutoPlay()
  saveProgress()
  uni.redirectTo({ url: `/pages/book/detail?id=${bookId}&switch=1` })
}

function goBack() {
  stopAutoPlay()
  saveProgress()
  if (getCurrentPages().length > 1) uni.navigateBack()
  else uni.switchTab({ url: '/pages/shelf/shelf' })
}

async function toggleFavorite() {
  if (!bookId || togglingFav.value) return
  togglingFav.value = true
  try {
    if (favorite.value) {
      await api.removeFavorite(bookId)
      favorite.value = false
      uni.showToast({ title: '已移出书架', icon: 'none' })
    } else {
      await api.addFavorite(bookId)
      favorite.value = true
      uni.showToast({ title: '已加入书架', icon: 'none' })
    }
  } catch (e) {
    toastError(e)
  } finally {
    togglingFav.value = false
  }
}

function persistLocalPosition(ratio = 0) {
  if (readerScope === null || readerScope !== accountScope.value || !bookId || !current.chapterId) return
  const chTitle =
    chapters.value.find((c) => c.id === current.chapterId)?.title ||
    loaded.value.find((c) => c.id === current.chapterId)?.title ||
    ''
  saveReadPosition({
    bookId,
    chapterId: current.chapterId,
    chapterTitle: chTitle,
    position: current.position,
    offsetPx: isComic.value ? 0 : current.position,
    ratio,
    isComic: isComic.value,
  })
}

let lastMeasureAt = 0
function measure() {
  if (!loaded.value.length || !useScrollView.value) return
  lastMeasureAt = Date.now()
  const refY = Math.round(win.windowHeight * 0.35)
  const q = uni.createSelectorQuery().in(instance?.proxy)
  q.selectAll('.chapter').boundingClientRect()
  if (isComic.value) {
    q.selectAll('.page').boundingClientRect()
  }
  q.exec((res) => {
    const rects = (res?.[0] || []) as UniApp.NodeInfo[]
    if (!rects.length) return

    let activeIdx = 0
    if (lastTop > 4) {
      let spanning = -1
      let lastAbove = 0
      for (let k = 0; k < rects.length; k++) {
        const t = rects[k].top ?? 0
        const b = rects[k].bottom ?? t + (rects[k].height || 0)
        if (t <= refY && b > refY) {
          spanning = k
          break
        }
        if (t <= refY) lastAbove = k
      }
      activeIdx = spanning >= 0 ? spanning : lastAbove
    }

    const rect = rects[activeIdx]
    const idMatch = typeof rect.id === 'string' && rect.id.startsWith('ch-') ? Number(rect.id.slice(3)) : 0
    const ch = (idMatch ? loaded.value.find((x) => x.id === idMatch) : loaded.value[activeIdx]) || loaded.value[0]
    if (!ch) return

    const top = rect.top ?? 0
    const height = Math.max(1, rect.height || 1)
    const bottom = rect.bottom ?? top + height
    activeChapterTop = top
    activeChapterHeight = height
    activeChapterIsFirst = activeIdx === 0
    let position = 0
    let ratio = 0

    if (ch.images?.length) {
      if (activeIdx === 0 && lastTop <= 4) {
        position = 0
      } else {
        const pageRects = (res?.[1] || []) as UniApp.NodeInfo[]
        const prefix = `p-${ch.id}-`
        let foundAny = false
        let spanningPage = -1
        let lastAbovePage = -1
        for (const pr of pageRects) {
          if (typeof pr.id !== 'string' || !pr.id.startsWith(prefix)) continue
          const pIdx = Number(pr.id.slice(prefix.length))
          if (Number.isNaN(pIdx)) continue
          foundAny = true
          const pt = pr.top ?? 0
          const pb = pr.bottom ?? pt + (pr.height || 0)
          if (pt <= refY && pb > refY) {
            spanningPage = pIdx
            break
          }
          if (pt <= refY) lastAbovePage = pIdx
        }
        if (spanningPage >= 0) {
          position = Math.min(ch.images.length - 1, spanningPage)
        } else if (bottom <= win.windowHeight + 16) {
          position = ch.images.length - 1
        } else if (lastAbovePage >= 0) {
          position = Math.min(ch.images.length - 1, lastAbovePage)
        } else if (foundAny) {
          position = 0
        } else {
          position = Math.min(ch.images.length - 1, Math.max(0, Math.floor((-top / height) * ch.images.length)))
        }
      }
      currentPage.value = position
      currentTotalPages.value = ch.images.length
      ratio = ch.images.length > 1 ? position / (ch.images.length - 1) : 1
      chapterPercent.value = Math.round(ratio * 100)
    } else {
      const originY = activeIdx === 0 ? novelPadTop : 0
      position = Math.max(0, Math.round(originY - top))
      const scrollable = Math.max(1, height - win.windowHeight)
      ratio = Math.min(1, Math.max(0, position / scrollable))
      chapterPercent.value = Math.round(ratio * 100)
      currentPage.value = 0
      currentTotalPages.value = 0
    }

    current = { chapterId: ch.id, position }
    if (currentId.value !== ch.id) {
      currentId.value = ch.id
    }
    persistLocalPosition(ratio)
  })
}

let scrollTimer: ReturnType<typeof setTimeout> | undefined
function onScroll(e: any) {
  lastTop = e.detail?.scrollTop ?? 0
  if (Date.now() < ignoreScrollUntil) return
  restoringComic = null
  clearTimeout(reanchorTimer)
  if (menu.value) closeMenu()
  if (Date.now() - lastMeasureAt >= 120) {
    measure()
  }
  clearTimeout(scrollTimer)
  scrollTimer = setTimeout(measure, 180)
}

function openMenu(target: PanelMode = 'main') {
  panel.value = target
  menu.value = true
}

function closeMenu() {
  menu.value = false
  panel.value = 'main'
}

function togglePanel(target: PanelMode) {
  panel.value = panel.value === target ? 'main' : target
}

function onTap(e: any) {
  if (menu.value) {
    closeMenu()
    return
  }
  if (autoPlay.value) {
    stopAutoPlay()
    openMenu('main')
    return
  }
  const x = e.detail?.x ?? e.changedTouches?.[0]?.clientX ?? e.touches?.[0]?.clientX ?? win.windowWidth / 2
  const y = e.detail?.y ?? e.changedTouches?.[0]?.clientY ?? e.touches?.[0]?.clientY ?? win.windowHeight / 2
  const inX = x > win.windowWidth * 0.1 && x < win.windowWidth * 0.9
  const inY = y > win.windowHeight * 0.12 && y < win.windowHeight * 0.88
  if (inX && inY) openMenu('main')
}

// --- Auto Page-Turn / Auto-Scroll ---
const autoPlay = ref(false)
let autoTimer: ReturnType<typeof setInterval> | undefined

const SCROLL_STEPS = [0.9, 1.5, 2.3, 3.5, 5.0]
const PAGE_INTERVALS = [10000, 7500, 5500, 3800, 2400]

function startAutoPlay() {
  stopAutoPlay()
  if (!loaded.value.length) return
  autoPlay.value = true
  closeMenu()
  const idx = Math.min(4, Math.max(0, store.reader.autoSpeed - 1))
  if (useScrollView.value) {
    let cursor = lastTop
    autoTimer = setInterval(() => {
      if (!autoPlay.value || menu.value) return
      cursor = Math.max(cursor, lastTop) + SCROLL_STEPS[idx]
      ignoreScrollUntil = Date.now() + 90
      scrollTop.value = Math.round(cursor)
      if (Date.now() - lastMeasureAt >= 450) measure()
    }, 50)
  } else {
    autoTimer = setInterval(() => {
      if (!autoPlay.value || menu.value || loading.value) return
      if (isComicPaged.value) goComicPage(1)
      else goNovelPage(1)
    }, PAGE_INTERVALS[idx])
  }
}

function stopAutoPlay() {
  autoPlay.value = false
  if (autoTimer) {
    clearInterval(autoTimer)
    autoTimer = undefined
  }
}

function toggleAutoPlay() {
  if (autoPlay.value) {
    stopAutoPlay()
    uni.showToast({ title: '已停止自动翻页', icon: 'none' })
  } else {
    startAutoPlay()
    uni.showToast({ title: '已开启自动翻页', icon: 'none' })
  }
}

function setAutoSpeed(speed: number) {
  store.reader.autoSpeed = Math.min(5, Math.max(1, speed))
  if (autoPlay.value) startAutoPlay()
}

// --- Offline Caching by Range (前50章 / 后50章 / 后面所有章节) ---
const localBook = computed(() => bookMeta.value && offlineBook(bookMeta.value.id))
const offlineCount = computed(() => localBook.value ? downloadStats(localBook.value).done : 0)
const downloading = computed(() => localBook.value && activeDownload(localBook.value.book.id))
const openDownloads = () => uni.navigateTo({ url: '/pages/downloads/downloads' })

const cacheRanges = computed(() => {
  const list = chapters.value
  const total = list.length
  const idx = Math.max(0, currentIndex.value)
  const prevStart = idx > 0 ? Math.max(0, idx - 50) : 0
  const prevEnd = idx > 0 ? idx : Math.min(50, total)
  const prevSlice = list.slice(prevStart, prevEnd)

  const nextSlice = list.slice(idx, Math.min(total, idx + 50))
  const restSlice = list.slice(idx)

  return {
    prev: {
      chapters: prevSlice,
      desc: prevSlice.length ? `第 ${prevStart + 1}-${prevEnd} 章（${prevSlice.length} 章）` : '暂无前置章节',
    },
    next: {
      chapters: nextSlice,
      desc: nextSlice.length ? `第 ${idx + 1}-${idx + nextSlice.length} 章（${nextSlice.length} 章）` : '暂无章节',
    },
    rest: {
      chapters: restSlice,
      desc: restSlice.length ? `第 ${idx + 1}-${total} 章（共 ${restSlice.length} 章）` : '暂无章节',
    },
  }
})

function startCache(targetChapters: Chapter[]) {
  if (!bookMeta.value || !targetChapters.length) return
  try {
    const added = enqueueChapters(bookMeta.value, targetChapters)
    uni.showToast({ title: added ? `已加入 ${added} 章` : '章节已离线或已在队列中', icon: 'none' })
  } catch (e) {
    toastError(e)
  }
}

async function cacheOnServer() {
  if (!bookId) return
  try {
    await api.cacheBook(bookId)
    uni.showToast({ title: '已加入服务器整本缓存队列', icon: 'none' })
  } catch (e) {
    toastError(e)
  }
}

// --- Progress persistence ---
function saveProgress() {
  if (readerScope === null || readerScope !== accountScope.value || !current.chapterId) return
  persistLocalPosition(chapterPercent.value / 100)
  if (current.chapterId === saved.chapterId && current.position === saved.position) return
  saved = { ...current }
  if (offlineOnly) return
  api.saveProgress(saved.chapterId, saved.position).catch(() => {
    // offline reading
  })
}

const saveTimer = setInterval(saveProgress, 4000)
onHide(() => {
  stopAutoPlay()
  saveProgress()
})
onUnload(() => {
  stopAutoPlay()
  clearInterval(saveTimer)
  clearTimeout(scrollTimer)
  clearTimeout(reanchorTimer)
  clearTimeout(animTimer)
  settle?.('server')
  saveProgress()
})

const catalogReversed = ref(false)
const catalogChapters = computed(() => (catalogReversed.value ? [...chapters.value].reverse() : chapters.value))
const catalogSummary = computed(() => summarizeChapterStatuses(chapters.value, localBook.value))

function catalogBadge(c: Chapter) {
  return getChapterBadge(c, localBook.value)
}

function jump(c?: Chapter) {
  if (!c) return
  stopAutoPlay()
  catalog.value = false
  closeMenu()
  show(c.id, 0)
}

function scrollCatalogToCurrent() {
  const list = catalogChapters.value
  if (!list.length) return
  const idx = list.findIndex((c) => c.id === currentId.value)
  const anchor = list[Math.max(0, (idx >= 0 ? idx : 0) - 3)]
  if (!anchor) return
  catalogIntoView.value = ''
  nextTick(() => {
    setTimeout(() => {
      catalogIntoView.value = `c-${anchor.id}`
    }, 120)
  })
}

function openCatalog() {
  stopAutoPlay()
  closeMenu()
  catalogIntoView.value = ''
  catalog.value = true
  scrollCatalogToCurrent()
}

function resize(delta: number) {
  store.reader.fontSize = Math.min(30, Math.max(14, store.reader.fontSize + delta))
}

function setTheme(name: Theme) {
  store.reader.theme = name
  if (name !== 'dark') store.reader.dayTheme = name
}

function setPageMode(mode: PageMode) {
  store.reader.pageMode = mode
}

function setComicMode(mode: ComicMode) {
  store.reader.comicMode = mode
}

const toggleNight = () => setTheme(night.value ? store.reader.dayTheme : 'dark')
</script>

<template>
  <view
    class="reader"
    :class="{ night }"
    :style="{ background: theme.bg, color: theme.fg, fontSize: store.reader.fontSize + 'px' }"
  >
    <!-- Floating top-left chapter tip: aligned with WeChat capsule row, flush left, rounded right -->
    <view
      v-if="loaded.length && !menu && title"
      class="corner-tip"
      :class="{ comic: isComic }"
      :style="{ top: tipTop + 'px', height: tipHeight + 'px', lineHeight: tipHeight + 'px' }"
      @click.stop="openMenu('main')"
    >
      <view class="corner-tip-text ellipsis">{{ title }}</view>
      <view v-if="tipProgress" class="corner-tip-meta">{{ tipProgress }}</view>
    </view>

    <!-- Mode A: Continuous Vertical Scroll (Novel scroll & Comic vertical strip) -->
    <scroll-view
      v-if="useScrollView"
      class="scroller"
      scroll-y
      enhanced
      :show-scrollbar="false"
      :scroll-top="scrollTop"
      :scroll-into-view="intoView"
      :lower-threshold="900"
      @scroll="onScroll"
      @scrolltolower="appendNext"
    >
      <view class="scroll-inner" :style="{ paddingTop: (isComic ? comicPadTop : novelPadTop) + 'px' }" @click="onTap">
        <view v-for="ch in loaded" :id="`ch-${ch.id}`" :key="ch.id" class="chapter">
          <view class="chapter-title" :class="{ comic: isComic }">{{ ch.title }}</view>
          <template v-if="ch.images">
            <image
              v-for="(img, i) in ch.images"
              :id="`p-${ch.id}-${i}`"
              :key="i"
              class="page"
              :class="{ gap: store.reader.comicGap }"
              :src="img"
              mode="widthFix"
              lazy-load
              @load="onPageLoad(ch, i)"
              @error="pageFailed(ch, i)"
            />
          </template>
          <view v-else class="text" :style="{ lineHeight: store.reader.lineHeight }">
            <view v-for="(p, i) in ch.paragraphs" :key="i" class="para">{{ p }}</view>
          </view>
        </view>

        <view class="tail">
          <wd-loading v-if="loading" />
          <template v-else-if="error">
            <view class="error">{{ loaded.length ? '下一章' : '这一章' }}没有加载出来</view>
            <view class="reason">{{ error }}</view>
            <view class="tail-actions" @click.stop>
              <wd-button size="small" @click="retry">重试</wd-button>
              <wd-button v-if="canSwitch" custom-style="margin-left: 24rpx" size="small" plain @click="toSwitch">换源</wd-button>
            </view>
          </template>
          <view v-else-if="nextId" class="tail-actions" @click.stop>
            <wd-button size="small" plain @click="appendNext">下一章</wd-button>
          </view>
          <view v-else-if="loaded.length" class="muted">已经是最后一章</view>
        </view>
      </view>
    </scroll-view>

    <!-- Mode B: Novel Horizontal Paged Mode (slide / cover / curl animations) -->
    <view
      v-else-if="isNovelPaged"
      class="paged-stage"
      :class="`anim-${store.reader.pageMode}`"
      @touchstart="onPagedTouchStart"
      @touchmove="onPagedTouchMove"
      @touchend="onPagedTouchEnd"
      @click="onPagedTap"
    >
      <view v-if="loading && !loaded.length" class="stage-center"><wd-loading /></view>
      <view v-else-if="error && !loaded.length" class="stage-center">
        <view class="error">这一章没有加载出来</view>
        <view class="reason">{{ error }}</view>
        <view class="tail-actions" @click.stop>
          <wd-button size="small" @click="retry">重试</wd-button>
          <wd-button v-if="canSwitch" custom-style="margin-left: 24rpx" size="small" plain @click="toSwitch">换源</wd-button>
        </view>
      </view>
      <template v-else-if="novelPages.length">
        <!-- Current page sheet -->
        <view
          class="paged-sheet current-sheet"
          :class="{ 'enter-next': turnAnim === 'next', 'enter-prev': turnAnim === 'prev' }"
          :style="{
            background: theme.bg,
            color: theme.fg,
            paddingTop: novelPadTop + 'px',
            transform: dragX ? `translateX(${dragX}px)` : '',
          }"
        >
          <view v-if="novelPages[novelPageIdx]?.isFirst" class="chapter-title paged-title">
            {{ novelPages[novelPageIdx].title }}
          </view>
          <view class="paged-text" :style="{ lineHeight: store.reader.lineHeight }">
            <view
              v-for="(line, i) in novelPages[novelPageIdx]?.lines"
              :key="i"
              class="paged-line"
              :class="{ indent: line.indent }"
            >
              {{ line.text }}
            </view>
          </view>
        </view>

        <!-- Leaving page overlay for slide / cover / curl transitions -->
        <view
          v-if="leavingPage && turnAnim"
          class="paged-sheet leaving-sheet"
          :class="`leave-${turnAnim}`"
          :style="{ background: theme.bg, color: theme.fg, paddingTop: novelPadTop + 'px' }"
        >
          <view v-if="leavingPage.isFirst" class="chapter-title paged-title">{{ leavingPage.title }}</view>
          <view class="paged-text" :style="{ lineHeight: store.reader.lineHeight }">
            <view
              v-for="(line, i) in leavingPage.lines"
              :key="i"
              class="paged-line"
              :class="{ indent: line.indent }"
            >
              {{ line.text }}
            </view>
          </view>
        </view>
      </template>
    </view>

    <!-- Mode C: Comic Horizontal Paged Mode (LTR & RTL Manga) -->
    <view v-else class="comic-paged-stage" @click="onComicPagedTap">
      <view v-if="loading && !loaded.length" class="stage-center"><wd-loading /></view>
      <view v-else-if="error && !loaded.length" class="stage-center">
        <view class="error">这一话没有加载出来</view>
        <view class="reason">{{ error }}</view>
        <view class="tail-actions" @click.stop>
          <wd-button size="small" @click="retry">重试</wd-button>
          <wd-button v-if="canSwitch" custom-style="margin-left: 24rpx" size="small" plain @click="toSwitch">换源</wd-button>
        </view>
      </view>
      <swiper
        v-else-if="comicDisplayImages.length"
        class="comic-swiper"
        :current="comicSwiperCurrent"
        :duration="240"
        @change="onComicSwiperChange"
      >
        <swiper-item v-for="(img, idx) in comicDisplayImages" :key="idx" class="comic-slide">
          <image
            class="comic-slide-img"
            :src="img"
            mode="aspectFit"
            lazy-load
            @error="activeChapter && pageFailed(activeChapter, store.reader.comicMode === 'page-rtl' ? comicDisplayImages.length - 1 - idx : idx)"
          />
        </swiper-item>
      </swiper>
    </view>

    <!-- Floating auto-play control pill when auto-turning is active -->
    <view v-if="autoPlay && !menu" class="auto-pill" @click.stop="stopAutoPlay">
      <text class="auto-dot" />
      <text>自动翻页中 · 速度{{ store.reader.autoSpeed }} · 点此暂停</text>
    </view>

    <!-- Both routes of the first page; the one that loads first is used for the book. -->
    <view v-if="probe" class="probe">
      <image :src="probe.server" @load="probed('server')" />
      <image :src="probe.direct" @load="probed('direct')" />
    </view>

    <!-- Top bar: visible when menu is open or nothing is loaded yet -->
    <view v-if="menu || !loaded.length" class="bar top" :style="{ paddingTop: statusBar + 'px' }">
      <view class="nav" :style="{ height: barHeight + 'px' }">
        <view class="back" @click="goBack"><view class="chevron" /></view>
        <view class="name ellipsis">{{ title }}</view>
      </view>
    </view>

    <!-- Bottom Toolbar (Reference Layout: floating quick buttons + chapter progress bar + reusable 4-icon dock) -->
    <view v-if="menu" class="toolbar-overlay" @click.stop>
      <!-- MAIN PANEL: 4 compact floating circular quick buttons slightly above transparent chapter progress slider -->
      <template v-if="panel === 'main'">
        <!-- Chapter progress tooltip pill shown while scrubbing -->
        <view v-if="scrubbing" class="progress-badge-wrap">
          <view class="progress-badge ellipsis">{{ scrubPreview }}</view>
        </view>

        <view class="quick-row">
          <view class="quick-circle" :class="{ active: favorite }" @click="toggleFavorite">
            <text class="q-glyph">{{ favorite ? '★' : '☆' }}</text>
          </view>
          <view class="quick-circle" @click="toSwitch">
            <text class="q-glyph">⇄</text>
          </view>
          <view class="quick-circle" :class="{ active: night }" @click="toggleNight">
            <text class="q-glyph">{{ night ? '☀' : '☾' }}</text>
          </view>
          <view class="quick-circle" :class="{ active: autoPlay }" @click="toggleAutoPlay">
            <text class="q-glyph">{{ autoPlay ? '❚❚' : '▶' }}</text>
          </view>
        </view>

        <!-- Shoulder buttons (上一章 / 下一章) + transparent current-chapter slider -->
        <view class="progress-bar">
          <view
            class="shoulder left"
            :class="{ off: currentIndex <= 0 }"
            @click="jump(chapters[currentIndex - 1])"
          >
            上一章
          </view>
          <view class="slider-box">
            <slider
              class="chapter-slider"
              :min="0"
              :max="sliderMax"
              :value="sliderValue"
              activeColor="#c0392b"
              :backgroundColor="night ? 'rgba(255, 255, 255, 0.35)' : 'rgba(43, 38, 34, 0.45)'"
              block-color="#c0392b"
              :block-size="14"
              @changing="onSliderChanging"
              @change="onSliderChange"
            />
          </view>
          <view
            class="shoulder right"
            :class="{ off: currentIndex >= chapters.length - 1 }"
            @click="jump(chapters[currentIndex + 1])"
          >
            下一章
          </view>
        </view>
      </template>

      <!-- CACHE SUB-PANEL: 前50章 / 后50章 / 后面所有章节 -->
      <view v-else-if="panel === 'cache'" class="sub-panel">
        <view class="sub-head">
          <text class="sub-title">离线缓存（{{ isComic ? '漫画画页' : '小说正文' }}）</text>
          <text class="sub-meta">本机已离线 {{ offlineCount }}/{{ chapters.length }} 章</text>
        </view>

        <view v-if="localBook" class="cache-progress" @click="openDownloads">
          <view v-if="downloading" class="cache-progress-text">{{ localBook.chapters[downloading.chapterId]?.title }} · {{ downloading.stage }}</view>
          <view class="cache-stop">查看离线章节与进度 ›</view>
        </view>

        <view v-if="offlineSupported && !offlineOnly" class="cache-options">
          <view class="cache-card" @click="startCache(chapters.slice(currentIndex, currentIndex + 1))">
            <view class="cache-card-title">当前章节</view>
            <view class="cache-card-desc ellipsis">{{ title }}</view>
          </view>
          <view class="cache-card" @click="startCache(cacheRanges.prev.chapters)">
            <view class="cache-card-title">前 50 章</view>
            <view class="cache-card-desc">{{ cacheRanges.prev.desc }}</view>
          </view>
          <view class="cache-card" @click="startCache(cacheRanges.next.chapters)">
            <view class="cache-card-title">后 50 章</view>
            <view class="cache-card-desc">{{ cacheRanges.next.desc }}</view>
          </view>
          <view class="cache-card" @click="startCache(cacheRanges.rest.chapters)">
            <view class="cache-card-title">后面所有章节</view>
            <view class="cache-card-desc">{{ cacheRanges.rest.desc }}</view>
          </view>
        </view>

        <view class="cache-footer">
          <view class="outline-btn full" @click="cacheOnServer">
            {{ isComic ? '服务器缓存整本漫画（不占手机空间）' : '加入服务器整本缓存' }}
          </view>
        </view>
      </view>

      <!-- PAGE-TURN SUB-PANEL (Reference Image 2 layout, adapted for Novel vs Comic) -->
      <view v-else-if="panel === 'page'" class="sub-panel">
        <template v-if="!isComic">
          <scroll-view scroll-x enhanced :show-scrollbar="false" class="mode-strip">
            <view class="mode-strip-inner">
              <view
                v-for="m in PAGE_MODES"
                :key="m.value"
                class="mode-chip"
                :class="{ active: store.reader.pageMode === m.value }"
                @click="setPageMode(m.value)"
              >
                {{ m.name }}
              </view>
            </view>
          </scroll-view>
          <view class="toggle-grid">
            <view
              class="outline-btn"
              :class="{ active: store.reader.tapNextOnly }"
              @click="store.reader.tapNextOnly = !store.reader.tapNextOnly"
            >
              全屏点击锁定下一页
            </view>
            <view class="outline-btn" :class="{ active: autoPlay }" @click="toggleAutoPlay">
              {{ autoPlay ? '关闭自动翻页' : '开启自动翻页' }}
            </view>
          </view>
          <view class="page-hint">
            {{
              store.reader.pageMode === 'scroll'
                ? '当前为上下连续滑动阅读；切换左右模式可体验翻页动画与手势翻页'
                : store.reader.tapNextOnly
                  ? '左右翻页中：点击屏幕两侧均进入下一页，点击中间唤起菜单'
                  : '左右翻页中：点击左侧上一页、右侧下一页、中间唤起菜单，支持左右滑动'
            }}
          </view>
        </template>

        <template v-else>
          <scroll-view scroll-x enhanced :show-scrollbar="false" class="mode-strip">
            <view class="mode-strip-inner">
              <view
                v-for="m in COMIC_MODES"
                :key="m.value"
                class="mode-chip"
                :class="{ active: store.reader.comicMode === m.value }"
                @click="setComicMode(m.value)"
              >
                {{ m.name }}
              </view>
            </view>
          </scroll-view>
          <view class="toggle-grid">
            <view
              class="outline-btn"
              :class="{ active: store.reader.comicGap }"
              @click="store.reader.comicGap = !store.reader.comicGap"
            >
              页间距：{{ store.reader.comicGap ? '有间距' : '无缝拼接' }}
            </view>
            <view
              class="outline-btn"
              :class="{ active: store.reader.tapNextOnly }"
              @click="store.reader.tapNextOnly = !store.reader.tapNextOnly"
            >
              {{ store.reader.tapNextOnly ? '点击翻页：全屏下一页' : '点击翻页：左右分区' }}
            </view>
          </view>
          <view class="page-hint">
            {{
              store.reader.comicMode === 'scroll'
                ? '条漫模式：适合国漫/韩漫上下连续浏览'
                : store.reader.comicMode === 'page-rtl'
                  ? '日漫模式：向右滑动或点左侧进入下一页'
                  : '左右翻页：向左滑动或点右侧进入下一页'
            }}
          </view>
        </template>
      </view>

      <!-- SETTINGS SUB-PANEL: Typography / Style / Speed (Novel vs Comic) -->
      <view v-else-if="panel === 'settings'" class="sub-panel">
        <template v-if="!isComic">
          <view class="set-row">
            <text class="set-label">字号</text>
            <view class="set-pills">
              <view class="set-pill" @click="resize(-1)">A-</view>
              <text class="set-size">{{ store.reader.fontSize }}</text>
              <view class="set-pill" @click="resize(1)">A+</view>
            </view>
          </view>
          <view class="set-row">
            <text class="set-label">行距</text>
            <view class="set-pills">
              <view
                v-for="l in LINE_HEIGHTS"
                :key="l.value"
                class="set-pill"
                :class="{ active: store.reader.lineHeight === l.value }"
                @click="store.reader.lineHeight = l.value"
              >
                {{ l.name }}
              </view>
            </view>
          </view>
        </template>
        <template v-else>
          <view class="set-row">
            <text class="set-label">线路</text>
            <view class="set-pills">
              <view
                class="set-pill"
                :class="{ active: store.reader.imageRoute === 'auto' }"
                @click="store.reader.imageRoute = 'auto'"
              >
                自动选快的
              </view>
              <view
                class="set-pill"
                :class="{ active: store.reader.imageRoute === 'server' }"
                @click="store.reader.imageRoute = 'server'"
              >
                只走服务器
              </view>
            </view>
          </view>
          <view class="set-row">
            <text class="set-label">页间距</text>
            <view class="set-pills">
              <view class="set-pill" :class="{ active: !store.reader.comicGap }" @click="store.reader.comicGap = false">
                无
              </view>
              <view class="set-pill" :class="{ active: store.reader.comicGap }" @click="store.reader.comicGap = true">
                有
              </view>
            </view>
          </view>
        </template>

        <view class="set-row">
          <text class="set-label">风格</text>
          <view class="set-pills">
            <view
              v-for="name in themeNames"
              :key="name"
              class="swatch"
              :class="{ active: store.reader.theme === name }"
              :style="{ background: THEMES[name].bg, color: THEMES[name].fg }"
              @click="setTheme(name)"
            >
              {{ THEMES[name].name }}
            </view>
          </view>
        </view>

        <view class="set-row">
          <text class="set-label">自动速</text>
          <view class="set-pills">
            <view
              v-for="s in [1, 2, 3, 4, 5]"
              :key="s"
              class="set-pill speed-pill"
              :class="{ active: store.reader.autoSpeed === s }"
              @click="setAutoSpeed(s)"
            >
              {{ s }}
            </view>
          </view>
        </view>
      </view>

      <!-- Reusable Bottom 4-Icon Dock -->
      <view class="dock">
        <view class="dock-item" @click="openCatalog">
          <view class="dock-icon icon-list">
            <view class="ln" />
            <view class="ln" />
            <view class="ln" />
          </view>
          <text class="dock-lbl">目录</text>
        </view>
        <view class="dock-item" :class="{ active: panel === 'cache' || downloading }" @click="togglePanel('cache')">
          <view class="dock-icon icon-download">
            <view class="dl-stem" />
            <view class="dl-arrow" />
            <view class="dl-tray" />
          </view>
          <text class="dock-lbl">{{ downloading ? '下载中' : '缓存' }}</text>
        </view>
        <view class="dock-item" :class="{ active: panel === 'page' }" @click="togglePanel('page')">
          <view class="dock-icon icon-book">
            <view class="bk-half left" />
            <view class="bk-half right" />
          </view>
          <text class="dock-lbl">{{ isComic ? '模式' : '翻页' }}</text>
        </view>
        <view class="dock-item" :class="{ active: panel === 'settings' }" @click="togglePanel('settings')">
          <view class="dock-icon icon-aa">{{ isComic ? '⚙' : 'Aa' }}</view>
          <text class="dock-lbl">设置</text>
        </view>
      </view>
    </view>

    <!-- Catalog Drawer -->
    <wd-popup v-model="catalog" position="left">
      <view class="catalog-wrap" :class="{ night }">
        <view class="catalog-head" :style="{ paddingTop: statusBar + 12 + 'px' }">
          <view class="catalog-head-top">
            <view class="catalog-title">目录（{{ chapters.length }}）</view>
            <view class="catalog-actions">
              <text class="catalog-locate" @click="catalogReversed = !catalogReversed">{{ catalogReversed ? '正序' : '倒序' }}</text>
              <text class="catalog-locate" @click="scrollCatalogToCurrent">定位当前</text>
            </view>
          </view>
          <view
            v-if="catalogSummary.downloaded || catalogSummary.cached || catalogSummary.serverCached"
            class="catalog-stats"
          >
            <text v-if="catalogSummary.downloaded" class="catalog-stat downloaded">已下载 {{ catalogSummary.downloaded }}</text>
            <text v-if="catalogSummary.cached" class="catalog-stat cached">已缓存 {{ catalogSummary.cached }}</text>
            <text v-if="catalogSummary.serverCached" class="catalog-stat server">云端 {{ catalogSummary.serverCached }}</text>
          </view>
        </view>
        <scroll-view
          scroll-y
          enhanced
          :show-scrollbar="false"
          class="catalog-scroll"
          :scroll-into-view="catalogIntoView"
        >
          <view
            v-for="c in catalogChapters"
            :id="`c-${c.id}`"
            :key="c.id"
            class="catalog-item"
            :class="{ current: c.id === currentId }"
            @click="jump(c)"
          >
            <view class="catalog-name ellipsis">{{ c.title }}</view>
            <view class="catalog-badges">
              <text v-if="c.id === currentId" class="catalog-badge current">当前</text>
              <text v-if="catalogBadge(c)" class="catalog-badge" :class="catalogBadge(c)!.kind">
                {{ catalogBadge(c)!.label }}
              </text>
            </view>
          </view>
        </scroll-view>
      </view>
    </wd-popup>
  </view>
</template>

<style lang="scss" scoped>
.reader {
  position: relative;
  display: flex;
  flex-direction: column;
  height: 100vh;
  overflow: hidden;
  --menu-bg: rgba(255, 255, 255, 0.96);
  --menu-solid: #fff;
  --menu-fg: #2b2622;
  --menu-soft: #f1ede6;
  --menu-border: rgba(43, 38, 34, 0.14);
  --menu-muted: #8a8178;

  &.night {
    --menu-bg: rgba(28, 28, 30, 0.95);
    --menu-solid: #1e1e20;
    --menu-fg: #d6d6d6;
    --menu-soft: #2e2e32;
    --menu-border: rgba(255, 255, 255, 0.16);
    --menu-muted: #8e8e93;
  }
}

.corner-tip {
  position: fixed;
  left: 0;
  z-index: 3;
  display: flex;
  align-items: center;
  max-width: 54vw;
  padding: 0 20rpx 0 16rpx;
  border-radius: 0 26rpx 26rpx 0;
  background: rgba(255, 255, 255, 0.78);
  color: rgba(43, 38, 34, 0.82);
  font-size: 22rpx;
  box-shadow: 2rpx 4rpx 12rpx rgba(0, 0, 0, 0.08);
  backdrop-filter: blur(8px);

  .night & {
    background: rgba(36, 36, 36, 0.78);
    color: rgba(220, 220, 220, 0.86);
  }

  &.comic {
    background: rgba(0, 0, 0, 0.46);
    color: rgba(255, 255, 255, 0.94);
  }
}

.corner-tip-text {
  flex: 1;
  min-width: 0;
}

.corner-tip-meta {
  flex: none;
  margin-left: 12rpx;
  font-size: 20rpx;
  opacity: 0.85;
}

.scroller {
  flex: 1;
  width: 100%;
  height: 0;
  scrollbar-width: none;

  :deep(::-webkit-scrollbar) {
    display: none;
    width: 0;
    height: 0;
  }
}

.scroll-inner {
  box-sizing: border-box;
  min-height: 100vh;
}

.chapter-title {
  padding: 28rpx 36rpx 20rpx;
  font-size: 1.2em;
  font-weight: 600;

  &.comic {
    padding: 20rpx 36rpx;
    font-size: 26rpx;
    opacity: 0.65;
  }
}

.text {
  padding: 0 36rpx;
}

.para {
  margin-bottom: 0.8em;
  text-indent: 2em;
  text-align: justify;
}

.page {
  display: block;
  width: 100%;
  min-height: 300rpx;

  &.gap {
    margin-bottom: 16rpx;
  }
}

/* --- Novel Paged Mode & Animations --- */
.paged-stage {
  position: relative;
  flex: 1;
  width: 100%;
  height: 0;
  overflow: hidden;
  perspective: 1600px;
}

.stage-center {
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  height: 100%;
  padding: 0 48rpx;
  text-align: center;
}

.paged-sheet {
  position: absolute;
  inset: 0;
  box-sizing: border-box;
  padding: 0 36rpx 40rpx;
  overflow: hidden;
  transition: transform 0.14s ease-out;
}

.paged-title {
  padding: 0 0 24rpx;
}

.paged-line {
  text-align: justify;

  &.indent {
    text-indent: 2em;
  }
}

.leaving-sheet {
  z-index: 2;
  pointer-events: none;
}

.anim-slide {
  .leaving-sheet.leave-next {
    animation: slideOutLeft 0.26s ease-out forwards;
  }
  .leaving-sheet.leave-prev {
    animation: slideOutRight 0.26s ease-out forwards;
  }
  .current-sheet.enter-next {
    animation: slideInRight 0.26s ease-out;
  }
  .current-sheet.enter-prev {
    animation: slideInLeft 0.26s ease-out;
  }
}

.anim-cover {
  .leaving-sheet {
    box-shadow: 12rpx 0 32rpx rgba(0, 0, 0, 0.24);
  }
  .leaving-sheet.leave-next {
    animation: slideOutLeft 0.26s cubic-bezier(0.22, 0.61, 0.36, 1) forwards;
  }
  .leaving-sheet.leave-prev {
    z-index: 1;
  }
  .current-sheet.enter-prev {
    z-index: 3;
    box-shadow: 12rpx 0 32rpx rgba(0, 0, 0, 0.24);
    animation: slideInLeft 0.26s cubic-bezier(0.22, 0.61, 0.36, 1);
  }
}

.anim-curl {
  .leaving-sheet {
    transform-origin: left center;
    box-shadow: 16rpx 0 40rpx rgba(0, 0, 0, 0.28);
  }
  .leaving-sheet.leave-next {
    animation: curlOutLeft 0.28s ease-in-out forwards;
  }
  .leaving-sheet.leave-prev {
    z-index: 1;
  }
  .current-sheet.enter-prev {
    z-index: 3;
    transform-origin: left center;
    box-shadow: 16rpx 0 40rpx rgba(0, 0, 0, 0.28);
    animation: curlInLeft 0.28s ease-in-out;
  }
}

@keyframes slideOutLeft {
  from {
    transform: translateX(0);
  }
  to {
    transform: translateX(-100%);
  }
}

@keyframes slideOutRight {
  from {
    transform: translateX(0);
  }
  to {
    transform: translateX(100%);
  }
}

@keyframes slideInRight {
  from {
    transform: translateX(100%);
  }
  to {
    transform: translateX(0);
  }
}

@keyframes slideInLeft {
  from {
    transform: translateX(-100%);
  }
  to {
    transform: translateX(0);
  }
}

@keyframes curlOutLeft {
  0% {
    transform: translateX(0) rotateY(0deg);
    opacity: 1;
  }
  100% {
    transform: translateX(-85%) rotateY(-28deg);
    opacity: 0.4;
  }
}

@keyframes curlInLeft {
  0% {
    transform: translateX(-85%) rotateY(-28deg);
    opacity: 0.6;
  }
  100% {
    transform: translateX(0) rotateY(0deg);
    opacity: 1;
  }
}

/* --- Comic Paged Mode --- */
.comic-paged-stage {
  position: relative;
  flex: 1;
  width: 100%;
  height: 0;
  background: #111;
}

.comic-swiper {
  width: 100%;
  height: 100%;
}

.comic-slide {
  display: flex;
  align-items: center;
  justify-content: center;
  width: 100%;
  height: 100%;
}

.comic-slide-img {
  width: 100%;
  height: 100%;
}

/* --- Auto-play floating pill --- */
.auto-pill {
  position: fixed;
  right: 24rpx;
  bottom: calc(36rpx + env(safe-area-inset-bottom));
  z-index: 4;
  display: flex;
  align-items: center;
  gap: 10rpx;
  padding: 12rpx 22rpx;
  border-radius: 32rpx;
  background: rgba(43, 38, 34, 0.8);
  color: #fff;
  font-size: 22rpx;
  backdrop-filter: blur(8px);
}

.auto-dot {
  width: 12rpx;
  height: 12rpx;
  border-radius: 50%;
  background: #e74c3c;
}

.tail {
  padding: 72rpx 48rpx calc(96rpx + env(safe-area-inset-bottom));
  font-size: 28rpx;
  text-align: center;

  .error {
    margin-bottom: 12rpx;
    font-size: 30rpx;
  }

  .reason {
    margin-bottom: 32rpx;
    font-size: 24rpx;
    opacity: 0.65;
    line-height: 1.6;
  }
}

.tail-actions {
  display: inline-flex;
  align-items: center;
  justify-content: center;
}

.probe {
  position: absolute;
  left: -10px;
  top: -10px;
  width: 1px;
  height: 1px;
  overflow: hidden;
  opacity: 0;
  pointer-events: none;
}

/* --- Top Bar --- */
.bar {
  position: fixed;
  right: 0;
  left: 0;
  z-index: 5;
  font-size: 28rpx;
  color: var(--menu-fg);
  background: var(--menu-bg);
  box-shadow: 0 2rpx 16rpx rgba(0, 0, 0, 0.08);
  backdrop-filter: blur(12px);
}

.top {
  top: 0;

  .nav {
    display: flex;
    align-items: center;
    padding-right: 220rpx;
  }

  .back {
    flex: none;
    display: flex;
    align-items: center;
    justify-content: center;
    width: 88rpx;
    height: 100%;
  }

  .chevron {
    width: 20rpx;
    height: 20rpx;
    border-bottom: 4rpx solid currentColor;
    border-left: 4rpx solid currentColor;
    transform: translateX(6rpx) rotate(45deg);
  }

  .name {
    flex: 1;
    min-width: 0;
    font-size: 30rpx;
    font-weight: 500;
  }
}

/* --- Bottom Toolbar Overlay (Reference Images 1 & 2) --- */
.toolbar-overlay {
  position: fixed;
  right: 0;
  bottom: 0;
  left: 0;
  z-index: 6;
  color: var(--menu-fg);
  font-size: 26rpx;
}

/* Floating 4 compact circular quick buttons: slightly narrower than the middle progress bar */
.quick-row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  width: 400rpx;
  margin: 0 auto 12rpx;
}

.quick-circle {
  display: flex;
  align-items: center;
  justify-content: center;
  width: 66rpx;
  height: 66rpx;
  border-radius: 50%;
  background: var(--menu-solid);
  color: var(--menu-fg);
  border: 1rpx solid var(--menu-border);
  box-shadow: 0 4rpx 14rpx rgba(0, 0, 0, 0.16);

  &.active {
    background: var(--brand);
    border-color: var(--brand);
    color: #fff;
  }
}

.q-glyph {
  font-size: 28rpx;
  line-height: 1;
}

/* Floating chapter progress pill (shown while scrubbing) */
.progress-badge-wrap {
  display: flex;
  justify-content: center;
  padding: 0 36rpx 12rpx;
}

.progress-badge {
  max-width: 72vw;
  padding: 6rpx 22rpx;
  border-radius: 26rpx;
  background: var(--menu-bg);
  color: var(--menu-fg);
  font-size: 22rpx;
  border: 1rpx solid var(--menu-border);
  box-shadow: 0 4rpx 12rpx rgba(0, 0, 0, 0.1);
}

/* Progress bar row: transparent center with solid rounded shoulder tabs */
.progress-bar {
  display: flex;
  align-items: flex-end;
  background: transparent;
}

.shoulder {
  flex: none;
  padding: 20rpx 26rpx;
  font-size: 24rpx;
  font-weight: 500;
  color: var(--menu-fg);
  background: var(--menu-solid);
  box-shadow: 0 -2rpx 10rpx rgba(0, 0, 0, 0.06);

  &.left {
    border-radius: 0 28rpx 0 0;
  }

  &.right {
    border-radius: 28rpx 0 0 0;
  }

  &.off {
    color: var(--menu-muted);
    opacity: 0.55;
  }
}

.slider-box {
  flex: 1;
  min-width: 0;
  padding: 0 12rpx 8rpx;
  background: transparent;
}

.chapter-slider {
  margin: 0;
}

/* Expanded Sub-Panels (Cache / Page / Settings) */
.sub-panel {
  padding: 28rpx 32rpx 22rpx;
  background: var(--menu-bg);
  border-top: 1rpx solid var(--menu-border);
  border-radius: 24rpx 24rpx 0 0;
  box-shadow: 0 -4rpx 20rpx rgba(0, 0, 0, 0.08);
  backdrop-filter: blur(12px);
}

.sub-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  margin-bottom: 20rpx;
}

.sub-title {
  font-size: 28rpx;
  font-weight: 600;
}

.sub-meta {
  font-size: 22rpx;
  color: var(--menu-muted);
}

.cache-options {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 16rpx;
}

.cache-card {
  padding: 20rpx 14rpx;
  border: 1rpx solid var(--menu-border);
  border-radius: 14rpx;
  background: var(--menu-soft);
  text-align: center;
}

.cache-card-title {
  font-size: 26rpx;
  font-weight: 600;
  color: var(--menu-fg);
}

.cache-card-desc {
  margin-top: 8rpx;
  font-size: 20rpx;
  color: var(--menu-muted);
  line-height: 1.4;
}

.cache-progress {
  margin-bottom: 16rpx;
  padding: 18rpx 20rpx;
  border-radius: 14rpx;
  background: var(--menu-soft);
  text-align: center;
}

.cache-progress-text {
  font-size: 24rpx;
  margin-bottom: 14rpx;
}

.cache-stop {
  display: inline-block;
  margin-top: 16rpx;
  padding: 8rpx 28rpx;
  border-radius: 24rpx;
  border: 1rpx solid var(--brand);
  color: var(--brand);
  font-size: 23rpx;
}

.cache-footer {
  margin-top: 18rpx;
}

/* Page-Turn Mode Strip (Reference Image 2) */
.mode-strip {
  width: 100%;
  white-space: nowrap;
  margin-bottom: 22rpx;
}

.mode-strip-inner {
  display: inline-flex;
  gap: 16rpx;
}

.mode-chip {
  flex: none;
  padding: 16rpx 30rpx;
  border: 1rpx solid var(--menu-border);
  border-radius: 14rpx;
  font-size: 26rpx;
  color: var(--menu-fg);
  background: transparent;

  &.active {
    border-color: var(--brand);
    color: var(--brand);
    background: rgba(192, 57, 43, 0.08);
    font-weight: 600;
  }
}

.toggle-grid {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 16rpx;
}

.outline-btn {
  padding: 18rpx 16rpx;
  border: 1rpx solid var(--menu-border);
  border-radius: 14rpx;
  font-size: 24rpx;
  text-align: center;
  color: var(--menu-fg);

  &.active {
    border-color: var(--brand);
    color: var(--brand);
    background: rgba(192, 57, 43, 0.08);
    font-weight: 500;
  }

  &.full {
    width: 100%;
    box-sizing: border-box;
  }
}

.page-hint {
  margin-top: 16rpx;
  font-size: 22rpx;
  line-height: 1.5;
  color: var(--menu-muted);
}

/* Settings Rows */
.set-row {
  display: flex;
  align-items: center;
  gap: 16rpx;
  padding: 12rpx 0;
}

.set-label {
  flex: none;
  width: 88rpx;
  font-size: 25rpx;
  color: var(--menu-muted);
}

.set-pills {
  flex: 1;
  display: flex;
  align-items: center;
  gap: 14rpx;
}

.set-pill {
  flex: 1;
  padding: 12rpx 0;
  border-radius: 12rpx;
  border: 1rpx solid var(--menu-border);
  background: var(--menu-soft);
  text-align: center;
  font-size: 24rpx;

  &.active {
    border-color: var(--brand);
    color: var(--brand);
    background: rgba(192, 57, 43, 0.08);
    font-weight: 600;
  }
}

.set-size {
  min-width: 64rpx;
  text-align: center;
  font-size: 26rpx;
  font-weight: 600;
}

.swatch {
  flex: 1;
  padding: 12rpx 0;
  border: 2rpx solid var(--menu-border);
  border-radius: 12rpx;
  text-align: center;
  font-size: 24rpx;

  &.active {
    border-color: var(--brand);
    font-weight: 600;
  }
}

/* Reusable Bottom 4-Icon Dock */
.dock {
  display: flex;
  align-items: center;
  padding: 16rpx 0 calc(16rpx + env(safe-area-inset-bottom));
  background: var(--menu-solid);
  border-top: 1rpx solid var(--menu-border);
}

.dock-item {
  flex: 1;
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 8rpx;
  color: var(--menu-fg);

  &.active {
    color: var(--brand);
  }
}

.dock-icon {
  position: relative;
  display: flex;
  align-items: center;
  justify-content: center;
  width: 44rpx;
  height: 40rpx;
}

.dock-lbl {
  font-size: 22rpx;
  line-height: 1;
}

/* CSS-drawn crisp icons matching Image 1 & 2 */
.icon-list {
  flex-direction: column;
  justify-content: space-between;
  padding: 6rpx 4rpx;
  box-sizing: border-box;

  .ln {
    width: 34rpx;
    height: 3rpx;
    border-radius: 2rpx;
    background: currentColor;
  }
}

.icon-download {
  .dl-stem {
    position: absolute;
    top: 4rpx;
    width: 3rpx;
    height: 18rpx;
    background: currentColor;
  }

  .dl-arrow {
    position: absolute;
    top: 12rpx;
    width: 12rpx;
    height: 12rpx;
    border-right: 3rpx solid currentColor;
    border-bottom: 3rpx solid currentColor;
    transform: rotate(45deg);
  }

  .dl-tray {
    position: absolute;
    bottom: 4rpx;
    width: 30rpx;
    height: 10rpx;
    border: 3rpx solid currentColor;
    border-top: none;
    border-radius: 0 0 4rpx 4rpx;
  }
}

.icon-book {
  gap: 0;

  .bk-half {
    width: 16rpx;
    height: 28rpx;
    border: 3rpx solid currentColor;

    &.left {
      border-right-width: 1.5rpx;
      border-radius: 4rpx 0 0 4rpx;
    }

    &.right {
      border-left-width: 1.5rpx;
      border-radius: 0 4rpx 4rpx 0;
    }
  }
}

.icon-aa {
  font-size: 30rpx;
  font-weight: 500;
  line-height: 1;
}

/* Catalog Drawer */
.catalog-wrap {
  display: flex;
  flex-direction: column;
  width: 76vw;
  height: 100vh;
  background: #fff;
  color: #222;

  &.night {
    background: #1e1e20;
    color: #d6d6d6;

    .catalog-head,
    .catalog-item {
      border-bottom-color: rgba(255, 255, 255, 0.1);
    }

    .catalog-item.current {
      background: rgba(192, 57, 43, 0.16);
    }

    .catalog-stat.downloaded,
    .catalog-badge.downloaded {
      color: #81c784;
      background: rgba(76, 175, 80, 0.18);
    }

    .catalog-stat.cached,
    .catalog-badge.cached {
      color: #64b5f6;
      background: rgba(33, 150, 243, 0.18);
    }

    .catalog-stat.server,
    .catalog-badge.server {
      color: #a0a0a6;
      background: rgba(255, 255, 255, 0.08);
    }
  }
}

.catalog-head {
  flex: none;
  padding: 24rpx 32rpx 18rpx;
  border-bottom: 1rpx solid var(--line);
}

.catalog-head-top {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 16rpx;
}

.catalog-title {
  font-size: 30rpx;
  font-weight: 600;
}

.catalog-actions {
  display: flex;
  align-items: center;
  gap: 20rpx;
}

.catalog-locate {
  font-size: 24rpx;
  color: var(--brand);
}

.catalog-stats {
  display: flex;
  flex-wrap: wrap;
  gap: 10rpx;
  margin-top: 12rpx;
}

.catalog-stat {
  padding: 2rpx 12rpx;
  border-radius: 14rpx;
  font-size: 20rpx;

  &.downloaded {
    color: #2f7d57;
    background: #e7f4ed;
  }

  &.cached {
    color: #1d6fa5;
    background: #e6f2fa;
  }

  &.server {
    color: #7a7268;
    background: #f2efe9;
  }
}

.catalog-scroll {
  flex: 1;
  height: 0;
  font-size: 28rpx;
}

.catalog-item {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12rpx;
  padding: 24rpx 28rpx;
  border-bottom: 1rpx solid var(--line);

  &.current {
    background: #fdf4f2;
    color: var(--brand);
    font-weight: 600;
  }
}

.catalog-name {
  flex: 1;
  min-width: 0;
}

.catalog-badges {
  flex: none;
  display: flex;
  align-items: center;
  gap: 8rpx;
}

.catalog-badge {
  flex: none;
  padding: 2rpx 12rpx;
  border-radius: 16rpx;
  font-size: 20rpx;
  font-weight: 500;

  &.current {
    color: var(--brand);
    background: #f6e3df;
  }

  &.downloaded {
    color: #2f7d57;
    background: #e7f4ed;
  }

  &.cached {
    color: #1d6fa5;
    background: #e6f2fa;
  }

  &.server {
    color: #7a7268;
    background: #f2efe9;
  }

  &.downloading {
    color: #b26a00;
    background: #fff3e0;
  }

  &.failed {
    color: #c0392b;
    background: #fdecea;
  }
}
</style>
