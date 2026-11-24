<script setup lang="ts">
import { computed, getCurrentInstance, nextTick, ref } from 'vue'
import { onHide, onLoad, onPageScroll, onReachBottom, onUnload } from '@dcloudio/uni-app'
import { api, type Chapter, type ChapterContent } from '@/api'
import { absoluteUrl, errorText, sourceDown } from '@/api/http'
import { THEMES, useAppStore, type Theme } from '@/stores/app'
import { offlineBook, offlineChapters, readOfflineChapter } from '@/utils/offline'

interface Loaded {
  id: number
  title: string
  nextId: number
  paragraphs?: string[]
  images?: string[]
}

const store = useAppStore()
const instance = getCurrentInstance()
const theme = computed(() => THEMES[store.reader.theme])
const themeNames = Object.keys(THEMES) as Theme[]

let bookId = 0
const isComic = ref(false)
const chapters = ref<Chapter[]>([])
const loaded = ref<Loaded[]>([])
const loading = ref(false)
const error = ref('')
// Set when the failure is the source site's, so that switching source is worth offering.
const canSwitch = ref(false)
const panel = ref(false)
const catalog = ref(false)

// Where the reader currently is. `position` is a pixel offset inside a novel chapter
// and a page index inside a comic chapter.
let current = { chapterId: 0, position: 0 }
let saved = { chapterId: 0, position: 0 }

const nextId = computed(() => loaded.value[loaded.value.length - 1]?.nextId || 0)
const currentId = ref(0)

/** Reads a chapter from the device if it was downloaded, otherwise from the server. */
async function fetchChapter(id: number): Promise<Loaded> {
  const c: ChapterContent = readOfflineChapter(bookId, id) || (await api.chapter(id))
  // The catalog decides what comes next: a downloaded chapter still carries the nextId
  // from the day it was saved, which is 0 for what was then the last chapter.
  const i = chapters.value.findIndex((ch) => ch.id === id)
  return {
    id: c.id,
    title: c.title,
    nextId: i >= 0 ? chapters.value[i + 1]?.id || 0 : c.nextId,
    paragraphs: c.text?.split('\n'),
    images: c.images?.map(absoluteUrl),
  }
}

let wanted = { chapterId: 0, position: 0 }

async function show(chapterId: number, position = 0) {
  if (loading.value) return
  wanted = { chapterId, position }
  loading.value = true
  error.value = ''
  try {
    loaded.value = [await fetchChapter(chapterId)]
    current = { chapterId, position }
    currentId.value = chapterId
    uni.setNavigationBarTitle({ title: loaded.value[0].title })
    await nextTick()
    const target = isComic.value
      ? { selector: `#p-${chapterId}-${position}` }
      : { scrollTop: position }
    setTimeout(() => uni.pageScrollTo({ ...target, duration: 0 }), 50)
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
    const local = offlineBook(bookId)
    if (!local) {
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

onReachBottom(appendNext)

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
        if ((r.top ?? 0) <= 80) i = k
      })
      const ch = loaded.value[i]
      const top = rects[i].top ?? 0
      const height = rects[i].height || 1
      const position = ch.images
        ? Math.min(ch.images.length - 1, Math.max(0, Math.floor((-top / height) * ch.images.length)))
        : Math.max(0, Math.round(-top))
      current = { chapterId: ch.id, position }
      if (currentId.value !== ch.id) {
        currentId.value = ch.id
        uni.setNavigationBarTitle({ title: ch.title })
      }
    })
    .exec()
}

let scrollTimer: ReturnType<typeof setTimeout> | undefined
onPageScroll(() => {
  clearTimeout(scrollTimer)
  scrollTimer = setTimeout(measure, 300)
})

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
  saveProgress()
})

function jump(c: Chapter) {
  catalog.value = false
  show(c.id)
}

function openCatalog() {
  panel.value = false
  catalog.value = true
}

function resize(delta: number) {
  store.reader.fontSize = Math.min(30, Math.max(14, store.reader.fontSize + delta))
}
</script>

<template>
  <view class="reader" :style="{ background: theme.bg, color: theme.fg, fontSize: store.reader.fontSize + 'px' }">
    <view @click="panel = true">
      <view v-for="ch in loaded" :key="ch.id" class="chapter">
        <view class="chapter-title" :class="{ comic: isComic }">{{ ch.title }}</view>
        <template v-if="ch.images">
          <image
            v-for="(img, i) in ch.images"
            :id="`p-${ch.id}-${i}`"
            :key="i"
            class="page"
            :src="img"
            mode="widthFix"
            lazy-load
          />
        </template>
        <view v-else class="text">
          <view v-for="(p, i) in ch.paragraphs" :key="i" class="para">{{ p }}</view>
        </view>
      </view>
    </view>

    <view class="tail">
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

    <wd-popup v-model="panel" position="bottom" safe-area-inset-bottom>
      <view class="panel">
        <view class="row">
          <text class="label">字号</text>
          <wd-button size="small" plain @click="resize(-1)">A-</wd-button>
          <text class="size">{{ store.reader.fontSize }}</text>
          <wd-button size="small" plain @click="resize(1)">A+</wd-button>
        </view>
        <view class="row">
          <text class="label">背景</text>
          <view
            v-for="name in themeNames"
            :key="name"
            class="swatch"
            :class="{ active: store.reader.theme === name }"
            :style="{ background: THEMES[name].bg, color: THEMES[name].fg }"
            @click="store.reader.theme = name"
          >
            {{ THEMES[name].name }}
          </view>
        </view>
        <wd-button block @click="openCatalog">目录</wd-button>
      </view>
    </wd-popup>

    <wd-popup v-model="catalog" position="left">
      <scroll-view scroll-y class="catalog" :scroll-into-view="catalog ? `c-${currentId}` : ''">
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
  min-height: 100vh;
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
  line-height: 1.8;
  text-indent: 2em;
  text-align: justify;
}

.page {
  display: block;
  width: 100%;
  // Keeps not-yet-loaded pages from collapsing to nothing.
  min-height: 300rpx;
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
    opacity: 0.6;
  }
}

.panel {
  padding: 32rpx;
  color: #222;
  font-size: 28rpx;

  .row {
    display: flex;
    align-items: center;
    margin-bottom: 32rpx;
  }

  .label {
    width: 100rpx;
    color: #8a8a8a;
  }

  .size {
    width: 100rpx;
    text-align: center;
  }

  .swatch {
    margin-right: 20rpx;
    padding: 12rpx 28rpx;
    border: 2rpx solid #ddd;
    border-radius: 8rpx;

    &.active {
      border-color: var(--brand);
    }
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
  border-bottom: 1rpx solid #f0f0f0;

  &.current {
    color: var(--brand);
  }
}
</style>
