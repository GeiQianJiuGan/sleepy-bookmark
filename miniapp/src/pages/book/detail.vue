<script setup lang="ts">
import { computed, ref } from 'vue'
import { onLoad, onReachBottom, onShow } from '@dcloudio/uni-app'
import { api, type Book, type Chapter } from '@/api'
import { absoluteUrl, toastError } from '@/api/http'
import { downloadChapters, offlineBook, offlineSupported } from '@/utils/offline'

const PAGE = 100

let bookId = 0
const book = ref<Book | null>(null)
const chapters = ref<Chapter[]>([])
const loadingToc = ref(true)
const reversed = ref(false)
const shown = ref(PAGE)
const downloaded = ref<Record<number, unknown>>({})
const downloading = ref('')
const signal = { cancelled: false }

const ordered = computed(() => (reversed.value ? [...chapters.value].reverse() : chapters.value))
const visible = computed(() => ordered.value.slice(0, shown.value))

async function loadBook() {
  try {
    book.value = await api.book(bookId)
    uni.setNavigationBarTitle({ title: book.value.title })
  } catch (e) {
    toastError(e)
  }
}

async function loadToc(refresh = false) {
  loadingToc.value = true
  try {
    chapters.value = await api.chapters(bookId, refresh)
  } catch (e) {
    toastError(e)
  } finally {
    loadingToc.value = false
  }
}

onLoad((query) => {
  bookId = Number(query?.id)
  loadToc()
})
// Reload on every show so the reading progress is current after coming back from the reader.
onShow(() => {
  loadBook()
  downloaded.value = offlineBook(bookId)?.chapters || {}
})
onReachBottom(() => {
  if (shown.value < chapters.value.length) shown.value += PAGE
})

function read(chapterId?: number) {
  uni.navigateTo({ url: `/pages/reader/reader?bookId=${bookId}` + (chapterId ? `&chapterId=${chapterId}` : '') })
}

async function toggleFavorite() {
  if (!book.value) return
  try {
    if (book.value.favorite) await api.removeFavorite(bookId)
    else await api.addFavorite(bookId)
    book.value.favorite = !book.value.favorite
    uni.showToast({ title: book.value.favorite ? '已加入书架' : '已移出书架', icon: 'none' })
  } catch (e) {
    toastError(e)
  }
}

async function cacheOnServer() {
  try {
    await api.cacheBook(bookId)
    uni.showToast({ title: '已加入服务器缓存队列', icon: 'none' })
  } catch (e) {
    toastError(e)
  }
}

async function download() {
  if (!book.value || !chapters.value.length) return
  if (downloading.value) {
    signal.cancelled = true
    return
  }
  signal.cancelled = false
  downloading.value = '准备下载…'
  try {
    const failed = await downloadChapters(
      book.value,
      chapters.value,
      (done, total) => (downloading.value = `下载中 ${done}/${total}（点击停止）`),
      signal,
    )
    uni.showToast({ title: failed ? `${failed} 章下载失败，可再点一次重试` : '下载完成', icon: 'none' })
  } catch (e) {
    toastError(e)
  } finally {
    downloading.value = ''
    downloaded.value = offlineBook(bookId)?.chapters || {}
  }
}
</script>

<template>
  <view v-if="book">
    <view class="head">
      <image v-if="book.cover" class="cover" :src="absoluteUrl(book.cover)" mode="aspectFill" />
      <view class="meta">
        <view class="title">{{ book.title }}</view>
        <view class="muted">{{ book.author || '未知作者' }}</view>
        <view class="muted">来源：{{ book.sourceName }}</view>
        <view v-if="book.latest" class="muted ellipsis">最新：{{ book.latest }}</view>
        <view v-if="book.progress" class="muted ellipsis">读到：{{ book.progress.chapterTitle }}</view>
      </view>
    </view>
    <view v-if="book.intro" class="intro">{{ book.intro }}</view>

    <view class="actions">
      <wd-button size="small" :disabled="!chapters.length" @click="read()">{{ book.progress ? '继续阅读' : '开始阅读' }}</wd-button>
      <wd-button size="small" plain @click="toggleFavorite">{{ book.favorite ? '移出书架' : '加入书架' }}</wd-button>
      <wd-button v-if="offlineSupported" size="small" plain @click="download">{{ downloading || '离线下载' }}</wd-button>
      <wd-button size="small" plain @click="cacheOnServer">服务器缓存</wd-button>
    </view>

    <view class="toc-head">
      <text>目录（{{ chapters.length }}）</text>
      <view>
        <text class="link" @click="loadToc(true)">刷新</text>
        <text class="link" @click="reversed = !reversed">{{ reversed ? '正序' : '倒序' }}</text>
      </view>
    </view>
    <view v-if="loadingToc" class="empty"><wd-loading /></view>
    <view v-for="c in visible" :key="c.id" class="chapter" @click="read(c.id)">
      <text class="ellipsis" :class="{ current: book.progress?.chapterId === c.id }">{{ c.title }}</text>
      <text v-if="downloaded[c.id]" class="muted">已下载</text>
      <text v-else-if="c.cached" class="muted">已缓存</text>
    </view>
  </view>
  <view v-else class="empty"><wd-loading /></view>
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
  height: 266rpx;
  margin-right: 28rpx;
  border-radius: 8rpx;
  background: #eee;
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

.intro {
  padding: 0 32rpx 24rpx;
  background: #fff;
  color: #555;
  line-height: 1.7;
}

.actions {
  display: flex;
  flex-wrap: wrap;
  gap: 16rpx;
  padding: 0 32rpx 32rpx;
  background: #fff;
}

.toc-head {
  display: flex;
  justify-content: space-between;
  padding: 24rpx 32rpx;
  font-weight: 600;

  .link {
    margin-left: 32rpx;
    font-weight: 400;
    color: #c0392b;
  }
}

.chapter {
  display: flex;
  justify-content: space-between;
  padding: 24rpx 32rpx;
  background: #fff;
  border-bottom: 1rpx solid #f0f0f0;

  .ellipsis {
    flex: 1;
    min-width: 0;
  }

  .current {
    color: #c0392b;
  }

  .muted {
    flex: none;
    margin-left: 16rpx;
  }
}
</style>
