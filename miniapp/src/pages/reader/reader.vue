<script setup lang="ts">
import { computed, getCurrentInstance, nextTick, ref, watch } from 'vue'
import { onHide, onLoad, onReady, onUnload } from '@dcloudio/uni-app'
import { api, type Chapter, type ChapterContent } from '@/api'
import { absoluteUrl, errorText, sourceDown } from '@/api/http'
import { LINE_HEIGHTS, THEMES, useAppStore, type Theme } from '@/stores/app'
import { offlineBook, offlineChapters, readOfflineChapter } from '@/utils/offline'
import { cacheChapter, cachedChapter, inReadCache } from '@/utils/readCache'

interface Loaded {
  id: number
  title: string
  nextId: number
  paragraphs?: string[]
  images?: string[]
  /** The pages through the server, to fall back on when one does not load from the source site. */
  proxied?: string[]
}

// How many novel chapters after the one being read are fetched into the reading cache.
const AHEAD = 3
// How long the two routes of a comic page may take before the server is used.
const PROBE_TIMEOUT = 4000

const store = useAppStore()
const instance = getCurrentInstance()
const theme = computed(() => THEMES[store.reader.theme])
const night = computed(() => store.reader.theme === 'dark')
const themeNames = Object.keys(THEMES) as Theme[]

const win = uni.getSystemInfoSync()
const statusBar = win.statusBarHeight || 0
let barHeight = 44
// #ifdef MP-WEIXIN
const capsule = uni.getMenuButtonBoundingClientRect()
barHeight = (capsule.top - statusBar) * 2 + capsule.height
// #endif
// The strip kept clear at the top: pages never scroll under the status bar and the capsule button.
const headHeight = statusBar + barHeight

// The status bar icons follow the page colour.
function paintBar() {
  // #ifdef MP-WEIXIN
  uni.setNavigationBarColor({ frontColor: night.value ? '#ffffff' : '#000000', backgroundColor: theme.value.bg })
  // #endif
}
onReady(paintBar)
watch(theme, paintBar)

let bookId = 0
const isComic = ref(false)
const chapters = ref<Chapter[]>([])
const loaded = ref<Loaded[]>([])
const loading = ref(false)
const error = ref('')
// Set when the failure is the source site's, so that switching source is worth offering.
const canSwitch = ref(false)
// The menu: a top bar and the settings, opened by a tap in the middle of the screen.
const menu = ref(false)
const catalog = ref(false)

// Where the reader currently is. `position` is a pixel offset inside a novel chapter
// and a page index inside a comic chapter.
let current = { chapterId: 0, position: 0 }
let saved = { chapterId: 0, position: 0 }

const nextId = computed(() => loaded.value[loaded.value.length - 1]?.nextId || 0)
const currentId = ref(0)
const currentIndex = computed(() => chapters.value.findIndex((c) => c.id === currentId.value))
const title = computed(() => chapters.value[currentIndex.value]?.title || '')

// --- Comic pages: from the server or straight from the source site, whichever answers first.
// The server wins for what it has saved; the source site usually wins for the rest, unless it
// refuses requests that do not come from its own pages.
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

// A page that the source site did not serve is asked from the server; after a few of those
// the rest of the book goes through the server too.
let directFailures = 0
function pageFailed(ch: Loaded, i: number) {
  if (!ch.images || !ch.proxied || ch.images[i] === ch.proxied[i]) return
  ch.images[i] = ch.proxied[i]
  if (++directFailures >= 3) route = 'server'
}

// Choosing the server applies to the pages on screen as well, not only to the chapters to come.
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
  if (!c) {
    c = isComic.value ? null : cachedChapter(id)
    if (!c) {
      c = await api.chapter(id)
      cacheChapter(c)
      origins = c.origins
    }
  }
  // The catalog decides what comes next: a stored chapter still carries the nextId
  // from the day it was saved, which is 0 for what was then the last chapter.
  const i = chapters.value.findIndex((ch) => ch.id === id)
  const out: Loaded = {
    id: c.id,
    title: c.title,
    nextId: i >= 0 ? chapters.value[i + 1]?.id || 0 : c.nextId,
    paragraphs: c.text?.split('\n'),
    images: c.images?.map(absoluteUrl),
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

// Fetches the next few novel chapters into the reading cache, one at a time and quietly.
let warming = false
async function warm(fromId: number) {
  if (warming) return
  warming = true
  try {
    const i = chapters.value.findIndex((ch) => ch.id === fromId)
    for (const ch of i < 0 ? [] : chapters.value.slice(i + 1, i + 1 + AHEAD)) {
      if (inReadCache(ch.id) || readOfflineChapter(bookId, ch.id)) continue
      cacheChapter(await api.chapter(ch.id))
    }
  } catch {
    // It will be fetched when the reader gets there.
  } finally {
    warming = false
  }
}

// --- Scrolling. The page is one scroll-view, so that its scrollbar can be hidden everywhere.
const scrollTop = ref(0)
const intoView = ref('')
let lastTop = 0

async function scrollTo(target: { top?: number; id?: string }) {
  // The bound values only take effect when they change.
  scrollTop.value = lastTop
  intoView.value = ''
  await nextTick()
  if (target.id) intoView.value = target.id
  else scrollTop.value = target.top || 0
}

let wanted = { chapterId: 0, position: 0 }

async function show(chapterId: number, position = 0) {
  if (loading.value) return
  wanted = { chapterId, position }
  loading.value = true
  error.value = ''
  try {
    const ch = await fetchChapter(chapterId)
    loaded.value = [ch]
    current = { chapterId, position }
    currentId.value = chapterId
    await nextTick()
    // A novel offset is measured from the top of the screen, and the chapter starts below the head strip.
    setTimeout(() => scrollTo(ch.images ? { id: `p-${chapterId}-${position}` } : { top: position ? position + headHeight : 0 }), 50)
  } catch (e) {
    error.value = errorText(e)
    canSwitch.value = sourceDown(e)
  } finally {
    loading.value = false
  }
}

function retry() {
  if (!opened) open()
  else if (loaded.value.length) appendNext()
  else show(wanted.chapterId, wanted.position)
}

async function appendNext() {
  if (loading.value || !nextId.value) return
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

async function open() {
  let startId = firstChapter
  let position = 0
  loading.value = true
  error.value = ''
  try {
    const [book, list] = await Promise.all([api.book(bookId), api.chapters(bookId)])
    isComic.value = book.type === 'comic'
    chapters.value = list
    if (!startId && book.progress && list.some((c) => c.id === book.progress!.chapterId)) {
      startId = book.progress.chapterId
      position = book.progress.position
    }
  } catch (e) {
    // No network: fall back to what was downloaded to the device.
    // A download that has no chapters yet is no fallback; retrying must load the catalog again.
    const local = offlineBook(bookId)
    if (!local || !Object.keys(local.chapters).length) {
      loading.value = false
      error.value = errorText(e)
      canSwitch.value = false
      return
    }
    isComic.value = local.book.type === 'comic'
    chapters.value = offlineChapters(bookId)
  }
  opened = true
  loading.value = false
  startId = startId || chapters.value[0]?.id
  if (startId) show(startId, position)
  else error.value = '这本书还没有目录'
}

onLoad((query) => {
  bookId = Number(query?.bookId)
  firstChapter = Number(query?.chapterId) || 0
  open()
})

const toSwitch = () => uni.redirectTo({ url: `/pages/book/detail?id=${bookId}&switch=1` })

function goBack() {
  if (getCurrentPages().length > 1) uni.navigateBack()
  else uni.switchTab({ url: '/pages/shelf/shelf' })
}

function measure() {
  uni
    .createSelectorQuery()
    .in(instance?.proxy)
    .selectAll('.chapter')
    .boundingClientRect((res) => {
      const rects = res as UniApp.NodeInfo[]
      if (!rects?.length || rects.length !== loaded.value.length) return
      let i = 0
      rects.forEach((r, k) => {
        if ((r.top ?? 0) <= headHeight + 80) i = k
      })
      const ch = loaded.value[i]
      const top = rects[i].top ?? 0
      const height = rects[i].height || 1
      const position = ch.images
        ? Math.min(ch.images.length - 1, Math.max(0, Math.floor((-top / height) * ch.images.length)))
        : Math.max(0, Math.round(-top))
      current = { chapterId: ch.id, position }
      currentId.value = ch.id
    })
    .exec()
}

let scrollTimer: ReturnType<typeof setTimeout> | undefined
function onScroll(e: any) {
  lastTop = e.detail.scrollTop
  menu.value = false
  clearTimeout(scrollTimer)
  scrollTimer = setTimeout(measure, 300)
}

// A tap in the middle of the screen opens the menu; any tap closes it.
function onTap(e: any) {
  if (menu.value) {
    menu.value = false
    return
  }
  const x = e.detail?.x ?? e.touches?.[0]?.clientX ?? win.windowWidth / 2
  const y = e.detail?.y ?? e.touches?.[0]?.clientY ?? win.windowHeight / 2
  const inMiddle = (v: number, size: number) => v > size * 0.25 && v < size * 0.75
  if (inMiddle(x, win.windowWidth) && inMiddle(y, win.windowHeight)) menu.value = true
}

function saveProgress() {
  if (!current.chapterId || (current.chapterId === saved.chapterId && current.position === saved.position)) return
  saved = { ...current }
  api.saveProgress(saved.chapterId, saved.position).catch(() => {
    // Offline reading: the progress is simply not synced.
  })
}

const saveTimer = setInterval(saveProgress, 5000)
onHide(saveProgress)
onUnload(() => {
  clearInterval(saveTimer)
  clearTimeout(scrollTimer)
  settle?.('server')
  saveProgress()
})

function jump(c?: Chapter) {
  if (!c) return
  catalog.value = false
  menu.value = false
  show(c.id)
}

function openCatalog() {
  menu.value = false
  catalog.value = true
}

function resize(delta: number) {
  store.reader.fontSize = Math.min(30, Math.max(14, store.reader.fontSize + delta))
}

function setTheme(name: Theme) {
  store.reader.theme = name
  if (name !== 'dark') store.reader.dayTheme = name
}

const toggleNight = () => setTheme(night.value ? store.reader.dayTheme : 'dark')
</script>

<template>
  <view
    class="reader"
    :class="{ night }"
    :style="{ background: theme.bg, color: theme.fg, fontSize: store.reader.fontSize + 'px' }"
  >
    <view class="head" :style="{ height: headHeight + 'px', paddingTop: statusBar + 'px' }">
      <view class="head-title ellipsis">{{ title }}</view>
    </view>
    <scroll-view
      class="scroller"
      scroll-y
      enhanced
      :show-scrollbar="false"
      :scroll-top="scrollTop"
      :scroll-into-view="intoView"
      :lower-threshold="900"
      @scroll="onScroll"
      @scrolltolower="appendNext"
      @click="onTap"
    >
      <view v-for="ch in loaded" :key="ch.id" class="chapter">
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
            @error="pageFailed(ch, i)"
          />
        </template>
        <view v-else class="text" :style="{ lineHeight: store.reader.lineHeight }">
          <view v-for="(p, i) in ch.paragraphs" :key="i" class="para">{{ p }}</view>
        </view>
      </view>

      <view class="tail" @click.stop>
        <wd-loading v-if="loading" />
        <template v-else-if="error">
          <view class="error">{{ loaded.length ? '下一章' : '这一章' }}没有加载出来</view>
          <view class="reason">{{ error }}</view>
          <wd-button size="small" @click="retry">重试</wd-button>
          <wd-button v-if="canSwitch" custom-style="margin-left: 24rpx" size="small" plain @click="toSwitch">换源</wd-button>
        </template>
        <wd-button v-else-if="nextId" size="small" plain @click="appendNext">下一章</wd-button>
        <view v-else-if="loaded.length" class="muted">已经是最后一章</view>
      </view>
    </scroll-view>

    <!-- Both routes of the first page; the one that loads first is used for the book. -->
    <view v-if="probe" class="probe">
      <image :src="probe.server" @load="probed('server')" />
      <image :src="probe.direct" @load="probed('direct')" />
    </view>

    <!-- Without a chapter on screen the bar stays, so that there is a way back. -->
    <view v-if="menu || !loaded.length" class="bar top" :style="{ paddingTop: statusBar + 'px' }">
      <view class="nav" :style="{ height: barHeight + 'px' }">
        <view class="back" @click="goBack"><view class="chevron" /></view>
        <view class="name ellipsis">{{ title }}</view>
      </view>
    </view>

    <view v-if="menu" class="bar bottom">
      <view class="steps">
        <view class="step" :class="{ off: currentIndex <= 0 }" @click="jump(chapters[currentIndex - 1])">上一章</view>
        <view class="where">{{ currentIndex + 1 }} / {{ chapters.length }}</view>
        <view class="step" :class="{ off: currentIndex >= chapters.length - 1 }" @click="jump(chapters[currentIndex + 1])">
          下一章
        </view>
      </view>

      <template v-if="!isComic">
        <view class="row">
          <text class="label">字号</text>
          <view class="pill" @click="resize(-1)">A-</view>
          <text class="size">{{ store.reader.fontSize }}</text>
          <view class="pill" @click="resize(1)">A+</view>
        </view>
        <view class="row">
          <text class="label">行距</text>
          <view
            v-for="l in LINE_HEIGHTS"
            :key="l.value"
            class="pill"
            :class="{ active: store.reader.lineHeight === l.value }"
            @click="store.reader.lineHeight = l.value"
          >
            {{ l.name }}
          </view>
        </view>
      </template>
      <template v-else>
        <view class="row">
          <text class="label">页间距</text>
          <view class="pill" :class="{ active: !store.reader.comicGap }" @click="store.reader.comicGap = false">无</view>
          <view class="pill" :class="{ active: store.reader.comicGap }" @click="store.reader.comicGap = true">有</view>
        </view>
        <view class="row">
          <text class="label">图片线路</text>
          <view class="pill" :class="{ active: store.reader.imageRoute === 'auto' }" @click="store.reader.imageRoute = 'auto'">
            自动选快的
          </view>
          <view class="pill" :class="{ active: store.reader.imageRoute === 'server' }" @click="store.reader.imageRoute = 'server'">
            只走服务器
          </view>
        </view>
      </template>
      <view class="row">
        <text class="label">背景</text>
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

      <view class="tools">
        <view class="tool" @click="openCatalog">目录</view>
        <view class="tool" @click="toggleNight">{{ night ? '日间模式' : '夜间模式' }}</view>
      </view>
    </view>

    <wd-popup v-model="catalog" position="left">
      <scroll-view scroll-y class="catalog" :scroll-into-view="catalog ? `c-${currentId}` : ''">
        <view :style="{ height: statusBar + 'px' }" />
        <view
          v-for="c in chapters"
          :id="`c-${c.id}`"
          :key="c.id"
          class="catalog-item ellipsis"
          :class="{ current: c.id === currentId }"
          @click="jump(c)"
        >
          {{ c.title }}
        </view>
      </scroll-view>
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
  // The menu takes the colours of a sheet of paper, or of the night.
  --menu-bg: #fff;
  --menu-fg: #2b2622;
  --menu-soft: #f1ede6;
  --menu-muted: #8a8178;

  &.night {
    --menu-bg: #242424;
    --menu-fg: #c8c8c8;
    --menu-soft: #333;
    --menu-muted: #7d7d7d;
  }
}

.head {
  flex: none;
  box-sizing: border-box;
  display: flex;
  align-items: center;
  // Stays clear of the capsule button on the right.
  padding-right: 220rpx;
  padding-left: 36rpx;
}

.head-title {
  font-size: 24rpx;
  opacity: 0.55;
}

.scroller {
  flex: 1;
  height: 0;

  // show-scrollbar covers the mini program; this covers the browsers.
  scrollbar-width: none;

  :deep(::-webkit-scrollbar) {
    display: none;
    width: 0;
    height: 0;
  }
}

.chapter-title {
  padding: 40rpx 36rpx 20rpx;
  font-size: 1.2em;
  font-weight: 600;

  &.comic {
    padding: 20rpx 36rpx;
    font-size: 28rpx;
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
  // Keeps not-yet-loaded pages from collapsing to nothing.
  min-height: 300rpx;

  &.gap {
    margin-bottom: 16rpx;
  }
}

.tail {
  padding: 60rpx 0 120rpx;
  text-align: center;

  .error {
    font-size: 28rpx;
  }

  .reason {
    margin: 12rpx 40rpx 32rpx;
    font-size: 24rpx;
    line-height: 1.6;
    opacity: 0.85;
  }
}

.probe {
  position: absolute;
  left: -10px;
  width: 1px;
  height: 1px;
  overflow: hidden;
  opacity: 0;
}

.bar {
  position: absolute;
  left: 0;
  right: 0;
  z-index: 5;
  font-size: 28rpx;
  color: var(--menu-fg);
  background: var(--menu-bg);

  &.top {
    top: 0;
    box-shadow: 0 2rpx 16rpx rgba(0, 0, 0, 0.08);
  }

  &.bottom {
    bottom: 0;
    padding: 12rpx 32rpx calc(24rpx + env(safe-area-inset-bottom));
    border-radius: 28rpx 28rpx 0 0;
    box-shadow: 0 -4rpx 24rpx rgba(0, 0, 0, 0.12);
  }
}

.nav {
  display: flex;
  align-items: center;

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
    margin-left: 10rpx;
    border-left: 4rpx solid currentColor;
    border-bottom: 4rpx solid currentColor;
    transform: rotate(45deg);
  }

  .name {
    flex: 1;
    min-width: 0;
    // Stays clear of the capsule button of the mini program.
    margin-right: 220rpx;
    font-size: 30rpx;
    font-weight: 600;
  }
}

.steps {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 16rpx 0 20rpx;

  .step {
    padding: 12rpx 8rpx;

    &.off {
      opacity: 0.35;
    }
  }

  .where {
    font-size: 24rpx;
    color: var(--menu-muted);
  }
}

.row {
  display: flex;
  align-items: center;
  margin-bottom: 24rpx;

  .label {
    flex: none;
    width: 132rpx;
    color: var(--menu-muted);
  }

  .size {
    width: 96rpx;
    text-align: center;
  }
}

.pill {
  margin-right: 16rpx;
  padding: 10rpx 32rpx;
  font-size: 26rpx;
  border: 2rpx solid transparent;
  border-radius: 32rpx;
  background: var(--menu-soft);

  &.active {
    color: var(--brand);
    border-color: var(--brand);
  }
}

.swatch {
  margin-right: 16rpx;
  padding: 10rpx 28rpx;
  font-size: 26rpx;
  border: 2rpx solid var(--menu-soft);
  border-radius: 32rpx;

  &.active {
    border-color: var(--brand);
  }
}

.tools {
  display: flex;
  margin-top: 8rpx;
  border-top: 1rpx solid var(--menu-soft);

  .tool {
    flex: 1;
    padding: 28rpx 0 8rpx;
    text-align: center;
  }
}

.catalog {
  width: 70vw;
  height: 100vh;
  color: #222;
  font-size: 28rpx;
}

.catalog-item {
  padding: 24rpx 32rpx;
  border-bottom: 1rpx solid var(--line);

  &.current {
    color: var(--brand);
  }
}
</style>
