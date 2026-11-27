<script setup lang="ts">
import { computed, nextTick, ref } from 'vue'
import { onLoad, onReachBottom, onShow } from '@dcloudio/uni-app'
import { api, type Book, type Chapter } from '@/api'
import { errorText, sourceDown, toastError } from '@/api/http'
import BookCover from '@/components/BookCover.vue'
import LoadState from '@/components/LoadState.vue'
import {
  activeDownload,
  downloadStats,
  enqueueChapters,
  formatBytes,
  networkAvailable,
  offlineAllChapters,
  offlineBook,
  offlineSupported,
  retryDownloads,
  setDownloadsPaused,
  type OfflineBook,
} from '@/utils/offline'
import { getChapterBadge, isChapterLocal, summarizeChapterStatuses } from '@/utils/chapterStatus'
import { getReadPosition } from '@/utils/readPosition'

const PAGE = 100

const bookIdRef = ref(0)
const offlineMode = ref(false)
const book = ref<Book | null>(null)
const onlineChapters = ref<Chapter[]>([])
const loadingToc = ref(true)
const bookError = ref('')
// Why the catalog is missing, and whether another source may have the book.
const tocError = ref('')
const tocSourceDown = ref(false)
const alternatives = ref<Book[] | null>(null)
const switching = ref(false)
let openSwitch = false
const reversed = ref(false)
const localOnly = ref(false)
const introOpen = ref(false)
const shown = ref(PAGE)

const localBook = computed(() => (bookIdRef.value ? offlineBook(bookIdRef.value) : undefined))
const localStats = computed(() => (localBook.value ? downloadStats(localBook.value) : null))
const activeDl = computed(() => (bookIdRef.value ? activeDownload(bookIdRef.value) : null))
const openDownloads = () => uni.navigateTo({ url: '/pages/downloads/downloads' })

const allChapters = computed<Chapter[]>(() => {
  if (offlineMode.value) return offlineAllChapters(bookIdRef.value)
  return onlineChapters.value
})

const statusSummary = computed(() => summarizeChapterStatuses(allChapters.value, localBook.value))

const filtered = computed(() => {
  if (!localOnly.value) return allChapters.value
  return allChapters.value.filter((c) => isChapterLocal(c.id, localBook.value))
})

const ordered = computed(() => (reversed.value ? [...filtered.value].reverse() : filtered.value))
const visible = computed(() => ordered.value.slice(0, shown.value))

const localPos = ref<ReturnType<typeof getReadPosition>>(null)
const currentChapterId = computed(() => {
  const lp = localPos.value
  const sp = book.value?.progress
  const serverAt = sp ? (sp.updatedAt || 0) * 1000 : 0
  if (lp && (!sp || lp.at >= serverAt - 2000)) return lp.chapterId
  return sp?.chapterId || 0
})
const currentChapterTitle = computed(() => {
  const lp = localPos.value
  const sp = book.value?.progress
  const serverAt = sp ? (sp.updatedAt || 0) * 1000 : 0
  if (lp?.chapterTitle && (!sp || lp.at >= serverAt - 2000)) return lp.chapterTitle
  return sp?.chapterTitle || ''
})
const canLocateCurrent = computed(() =>
  Boolean(currentChapterId.value && ordered.value.some((c) => c.id === currentChapterId.value)),
)
const canReadOffline = computed(() => statusSummary.value.localTotal > 0)

function bookFromOffline(local: OfflineBook): Book {
  return {
    id: local.book.id,
    sourceId: local.book.sourceId || 0,
    sourceName: local.book.sourceName,
    type: local.book.type,
    title: local.book.title,
    author: local.book.author,
    cover: local.book.cover,
    intro: '',
    latest: '',
  }
}

async function loadBook() {
  const id = bookIdRef.value
  const local = offlineBook(id)
  if (!book.value && local) {
    book.value = bookFromOffline(local)
    bookError.value = ''
    uni.setNavigationBarTitle({ title: local.book.title })
  }
  if (offlineMode.value) {
    if (!local) bookError.value = '本机没有这本书的离线内容'
    return
  }
  try {
    book.value = await api.book(id)
    bookError.value = ''
    uni.setNavigationBarTitle({ title: book.value.title })
  } catch (e) {
    if (local) {
      offlineMode.value = true
      if (!book.value) {
        book.value = bookFromOffline(local)
        uni.setNavigationBarTitle({ title: local.book.title })
      }
      bookError.value = ''
      return
    }
    if (!book.value) bookError.value = errorText(e)
  }
}

async function loadToc(refresh = false) {
  const id = bookIdRef.value
  if (offlineMode.value) {
    loadingToc.value = false
    tocError.value = ''
    return
  }
  loadingToc.value = true
  try {
    onlineChapters.value = await api.chapters(id, refresh)
    tocError.value = ''
  } catch (e) {
    if (onlineChapters.value.length) {
      // The catalog on screen is still good; a failed refresh is not worth more than a hint.
      uni.showToast({ title: '暂时刷新不了，显示的是上次的目录', icon: 'none' })
    } else if (offlineAllChapters(id).length) {
      offlineMode.value = true
      tocError.value = ''
      uni.showToast({ title: '当前离线，已显示本机离线目录', icon: 'none' })
    } else {
      tocError.value = errorText(e)
      tocSourceDown.value = sourceDown(e)
    }
  } finally {
    loadingToc.value = false
  }
}

async function loadOnlineFull() {
  offlineMode.value = false
  localOnly.value = false
  shown.value = PAGE
  await Promise.all([loadBook(), loadToc(false)])
}

/** Looks the book up in the other sources and lets the reader move to one of them. */
async function switchSource() {
  if (switching.value) return
  switching.value = true
  uni.showLoading({ title: '正在其他书源中查找', mask: true })
  try {
    alternatives.value = await api.alternatives(bookIdRef.value)
  } catch {
    alternatives.value = []
  } finally {
    uni.hideLoading()
    switching.value = false
  }
  const found = alternatives.value
  if (!found.length) {
    uni.showToast({ title: '其他书源里没有找到这本书', icon: 'none' })
    return
  }
  uni.showActionSheet({
    itemList: found.slice(0, 6).map((b) => `${b.sourceName} · ${b.author || '未知作者'}`),
    success: async ({ tapIndex }) => {
      // One switch at a time: a second one would start from the same shelf entry.
      if (switching.value) return
      switching.value = true
      const to = found[tapIndex]
      uni.showLoading({ title: '正在换源', mask: true })
      try {
        await api.switchSource(bookIdRef.value, to.id)
        uni.hideLoading()
        uni.redirectTo({ url: `/pages/book/detail?id=${to.id}` })
      } catch (e) {
        uni.hideLoading()
        switching.value = false
        uni.showToast({ title: sourceDown(e) ? '这个书源也打不开，换一个试试' : errorText(e), icon: 'none' })
      }
    },
  })
}

onLoad((query) => {
  bookIdRef.value = Number(query?.id)
  offlineMode.value = query?.offline === '1' || (!networkAvailable.value && Boolean(offlineBook(bookIdRef.value)))
  openSwitch = query?.switch === '1'
  loadToc().then(() => {
    // Sent here by the reader after a chapter would not load.
    if (openSwitch && !offlineMode.value) switchSource()
    openSwitch = false
  })
})
// Reload on every show so the reading progress is current after coming back from the reader.
onShow(() => {
  loadBook()
  localPos.value = getReadPosition(bookIdRef.value)
})
onReachBottom(() => {
  if (shown.value < ordered.value.length) shown.value += PAGE
})

function read(chapterId?: number) {
  const id = bookIdRef.value
  if (offlineMode.value) {
    if (chapterId) {
      if (!isChapterLocal(chapterId, localBook.value)) {
        const dc = localBook.value?.chapters[chapterId]
        if (dc?.error && localBook.value) {
          retryChapter(chapterId)
          return
        }
        uni.showToast({ title: '该章节尚未下载完成', icon: 'none' })
        return
      }
      uni.navigateTo({ url: `/pages/reader/reader?offline=1&bookId=${id}&chapterId=${chapterId}` })
      return
    }
    if (!canReadOffline.value) {
      uni.showToast({ title: '暂无已下载完成的章节', icon: 'none' })
      return
    }
    const target =
      currentChapterId.value && isChapterLocal(currentChapterId.value, localBook.value)
        ? currentChapterId.value
        : 0
    uni.navigateTo({ url: `/pages/reader/reader?offline=1&bookId=${id}` + (target ? `&chapterId=${target}` : '') })
    return
  }
  uni.navigateTo({ url: `/pages/reader/reader?bookId=${id}` + (chapterId ? `&chapterId=${chapterId}` : '') })
}

async function toggleFavorite() {
  if (!book.value) return
  try {
    if (book.value.favorite) await api.removeFavorite(bookIdRef.value)
    else await api.addFavorite(bookIdRef.value)
    book.value.favorite = !book.value.favorite
    uni.showToast({ title: book.value.favorite ? '已加入书架' : '已移出书架', icon: 'none' })
  } catch (e) {
    toastError(e)
  }
}

async function cacheOnServer() {
  try {
    await api.cacheBook(bookIdRef.value)
    uni.showToast({ title: '已加入服务器缓存队列', icon: 'none' })
  } catch (e) {
    toastError(e)
  }
}

function download(list: Chapter[]) {
  if (!book.value || !list.length) return
  try {
    const added = enqueueChapters(book.value, list)
    uni.showToast({ title: added ? `已加入 ${added} 章` : '章节已离线或已在队列中', icon: 'none' })
  } catch (e) {
    toastError(e)
  }
}

function togglePause() {
  if (!localBook.value) return
  try {
    setDownloadsPaused(localBook.value, !localBook.value.pauseReason)
  } catch (e) {
    toastError(e)
  }
}

function retryChapter(chapterId?: number) {
  if (!localBook.value) return
  try {
    retryDownloads(localBook.value, chapterId)
    uni.showToast({ title: '已重新加入下载队列', icon: 'none' })
  } catch (e) {
    toastError(e)
  }
}

function toggleLocalOnly(only: boolean) {
  localOnly.value = only
  shown.value = PAGE
}

function locateCurrent() {
  const idx = ordered.value.findIndex((c) => c.id === currentChapterId.value)
  if (idx < 0) return
  if (shown.value <= idx) shown.value = idx + 20
  nextTick(() => {
    uni.pageScrollTo({
      selector: `#toc-${currentChapterId.value}`,
      duration: 220,
    })
  })
}

function chapterBadge(c: Chapter) {
  return getChapterBadge(c, localBook.value)
}
</script>

<template>
  <view v-if="book">
    <view class="head">
      <view class="cover"><BookCover :book="book" /></view>
      <view class="meta">
        <view class="title">{{ book.title }}</view>
        <view class="muted">{{ book.author || '未知作者' }}</view>
        <view class="muted">来源：{{ book.sourceName }}</view>
        <view v-if="offlineMode && localStats" class="muted">
          本机已下载 {{ localStats.done }}/{{ localStats.total }} 章 · {{ formatBytes(localStats.bytes) }}
        </view>
        <view v-else-if="book.latest" class="muted ellipsis">最新：{{ book.latest }}</view>
        <view v-if="currentChapterTitle" class="muted ellipsis">读到：{{ currentChapterTitle }}</view>
      </view>
    </view>

    <view v-if="offlineMode" class="offline-banner">
      <view class="offline-banner-text">
        <text class="offline-dot" />
        <text v-if="activeDl">正在下载：{{ localBook?.chapters[activeDl.chapterId]?.title }} · {{ activeDl.stage }}</text>
        <text v-else-if="localBook?.pauseReason">{{ localBook.pauseReason }}</text>
        <text v-else>离线目录模式 · 点击已下载章节即可离线阅读</text>
      </view>
      <text class="link" @click="loadOnlineFull">在线目录</text>
    </view>

    <view v-if="book.intro" class="intro" :class="{ folded: !introOpen }" @click="introOpen = !introOpen">
      {{ book.intro }}
    </view>

    <view class="actions">
      <wd-button
        size="small"
        :disabled="offlineMode ? !canReadOffline : !allChapters.length"
        @click="read()"
      >
        {{ currentChapterId ? '继续阅读' : '开始阅读' }}
      </wd-button>
      <template v-if="offlineMode">
        <wd-button v-if="localBook?.pending.size" size="small" plain @click="togglePause">
          {{ localBook.pauseReason ? '继续下载' : '暂停下载' }}
        </wd-button>
        <wd-button v-if="localStats?.failed" size="small" plain @click="retryChapter()">
          重试失败（{{ localStats.failed }}）
        </wd-button>
        <wd-button size="small" plain @click="loadOnlineFull">查看在线完整目录</wd-button>
      </template>
      <template v-else>
        <wd-button size="small" plain @click="toggleFavorite">{{ book.favorite ? '移出书架' : '加入书架' }}</wd-button>
        <wd-button v-if="offlineSupported" size="small" plain :disabled="!allChapters.length" @click="download(allChapters)">
          整本离线下载
        </wd-button>
        <wd-button v-if="localBook" size="small" plain @click="openDownloads">查看离线进度</wd-button>
        <wd-button size="small" plain @click="cacheOnServer">服务器缓存</wd-button>
        <wd-button size="small" plain @click="switchSource">换源</wd-button>
      </template>
    </view>

    <view class="toc-bar">
      <view class="toc-head">
        <view class="toc-title-wrap">
          <text class="toc-title">目录（{{ allChapters.length }}）</text>
          <view v-if="statusSummary.downloaded || statusSummary.cached || statusSummary.serverCached" class="toc-stats">
            <text v-if="statusSummary.downloaded" class="stat-chip downloaded">已下载 {{ statusSummary.downloaded }}</text>
            <text v-if="statusSummary.cached" class="stat-chip cached">已缓存 {{ statusSummary.cached }}</text>
            <text v-if="statusSummary.serverCached && !offlineMode" class="stat-chip server">云端 {{ statusSummary.serverCached }}</text>
          </view>
        </view>
        <view class="toc-tools">
          <text v-if="canLocateCurrent" class="link" @click="locateCurrent">定位当前</text>
          <text v-if="!offlineMode" class="link" @click="loadToc(true)">刷新</text>
          <text class="link" @click="reversed = !reversed">{{ reversed ? '正序' : '倒序' }}</text>
        </view>
      </view>
      <view v-if="statusSummary.localTotal > 0 && statusSummary.localTotal < allChapters.length" class="toc-filter">
        <text class="filter-pill" :class="{ active: !localOnly }" @click="toggleLocalOnly(false)">
          全部（{{ allChapters.length }}）
        </text>
        <text class="filter-pill" :class="{ active: localOnly }" @click="toggleLocalOnly(true)">
          已下载/已缓存（{{ statusSummary.localTotal }}）
        </text>
      </view>
    </view>

    <LoadState
      v-if="!allChapters.length"
      :loading="loadingToc"
      :error="tocError"
      :empty="offlineMode ? '本机还没有离线章节' : '这本书还没有目录'"
      @retry="loadToc()"
    >
      <template v-if="tocSourceDown" #actions>
        <wd-button size="small" plain @click="switchSource">换源</wd-button>
      </template>
    </LoadState>

    <view
      v-for="c in visible"
      :id="`toc-${c.id}`"
      :key="c.id"
      class="chapter"
      :class="{
        current: currentChapterId === c.id,
        unavailable: offlineMode && !isChapterLocal(c.id, localBook),
      }"
      hover-class="pressed"
      @click="read(c.id)"
    >
      <view class="chapter-main">
        <text class="ellipsis chapter-title" :class="{ current: currentChapterId === c.id }">{{ c.title }}</text>
        <view v-if="offlineMode && localBook?.chapters[c.id]?.error" class="chapter-error ellipsis">
          {{ localBook.chapters[c.id].error }}
        </view>
      </view>
      <view class="chapter-right">
        <text v-if="currentChapterId === c.id" class="badge current">当前</text>
        <text
          v-if="chapterBadge(c)"
          class="badge"
          :class="chapterBadge(c)!.kind"
          @click.stop="chapterBadge(c)!.kind === 'downloading' ? openDownloads() : read(c.id)"
        >
          {{ chapterBadge(c)!.label }}
        </text>
        <text
          v-if="localBook?.chapters[c.id]?.error"
          class="link action"
          @click.stop="retryChapter(c.id)"
        >
          重试
        </text>
        <text
          v-else-if="!offlineMode && offlineSupported && !localBook?.chapters[c.id]"
          class="link action"
          @click.stop="download([c])"
        >
          下载
        </text>
      </view>
    </view>

    <view v-if="visible.length < ordered.length" class="load-more link" @click="shown += PAGE">
      加载更多章节（剩余 {{ ordered.length - visible.length }} 章）
    </view>
  </view>
  <LoadState v-else :loading="!bookError" :error="bookError" @retry="loadBook" />
</template>

<style lang="scss" scoped>
.head {
  display: flex;
  padding: 32rpx;
  background: #fff;
}

.cover {
  flex: none;
  width: 200rpx;
  margin-right: 28rpx;
}

.meta {
  flex: 1;
  min-width: 0;

  .title {
    margin-bottom: 12rpx;
    font-size: 36rpx;
    font-weight: 600;
  }

  .muted {
    line-height: 1.8;
  }
}

.offline-banner {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 16rpx;
  padding: 16rpx 32rpx;
  background: #fdf6ec;
  border-top: 1rpx solid #f5e6cf;
  border-bottom: 1rpx solid #f5e6cf;
  font-size: 24rpx;
  color: #8a5a14;
}

.offline-banner-text {
  flex: 1;
  min-width: 0;
  display: flex;
  align-items: center;
  gap: 12rpx;
}

.offline-dot {
  flex: none;
  width: 12rpx;
  height: 12rpx;
  border-radius: 50%;
  background: #2f7d57;
}

.intro {
  padding: 0 32rpx;
  border-bottom: 24rpx solid #fff;
  background: #fff;
  color: #555;
  line-height: 1.7;

  // Long introductions show three lines until tapped.
  &.folded {
    display: -webkit-box;
    overflow: hidden;
    -webkit-box-orient: vertical;
    -webkit-line-clamp: 3;
  }
}

.actions {
  display: flex;
  flex-wrap: wrap;
  gap: 16rpx;
  padding: 20rpx 32rpx 32rpx;
  background: #fff;
}

.toc-bar {
  padding: 24rpx 32rpx 18rpx;
}

.toc-head {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 16rpx;
}

.toc-title-wrap {
  flex: 1;
  min-width: 0;
}

.toc-title {
  font-weight: 600;
  font-size: 30rpx;
}

.toc-stats {
  display: flex;
  flex-wrap: wrap;
  gap: 10rpx;
  margin-top: 10rpx;
}

.stat-chip {
  padding: 2rpx 14rpx;
  border-radius: 16rpx;
  font-size: 21rpx;

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
    background: #ece7df;
  }
}

.toc-tools {
  flex: none;
  display: flex;
  align-items: center;
  gap: 24rpx;
  font-size: 26rpx;
}

.toc-filter {
  display: flex;
  gap: 16rpx;
  margin-top: 18rpx;
}

.filter-pill {
  padding: 8rpx 22rpx;
  border-radius: 28rpx;
  font-size: 23rpx;
  color: #6b635a;
  background: #ece7df;

  &.active {
    color: #fff;
    background: var(--brand);
    font-weight: 500;
  }
}

.chapter {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 16rpx;
  padding: 24rpx 32rpx;
  background: #fff;
  border-bottom: 1rpx solid var(--line);

  &.current {
    background: #fdf6f5;
  }

  &.unavailable {
    .chapter-title {
      color: #9e968d;
    }
  }

  &.pressed {
    background: #faf7f2;
  }
}

.chapter-main {
  flex: 1;
  min-width: 0;
}

.chapter-title {
  display: block;

  &.current {
    color: var(--brand);
    font-weight: 600;
  }
}

.chapter-error {
  margin-top: 6rpx;
  font-size: 22rpx;
  color: var(--brand);
}

.chapter-right {
  flex: none;
  display: flex;
  align-items: center;
  gap: 10rpx;
}

.badge {
  flex: none;
  padding: 4rpx 14rpx;
  border-radius: 16rpx;
  font-size: 20rpx;
  line-height: 1.4;
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

.action {
  margin-left: 6rpx;
  font-size: 24rpx;
}

.load-more {
  padding: 28rpx 32rpx 48rpx;
  text-align: center;
  font-size: 26rpx;
}
</style>
