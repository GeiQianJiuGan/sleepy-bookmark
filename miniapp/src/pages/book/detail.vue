<script setup lang="ts">
import { computed, ref } from 'vue'
import { onLoad, onReachBottom, onShow } from '@dcloudio/uni-app'
import { api, type Book, type Chapter } from '@/api'
import { errorText, sourceDown, toastError } from '@/api/http'
import BookCover from '@/components/BookCover.vue'
import LoadState from '@/components/LoadState.vue'
import { downloadChapters, offlineBook, offlineSupported } from '@/utils/offline'

const PAGE = 100

let bookId = 0
const book = ref<Book | null>(null)
const chapters = ref<Chapter[]>([])
const loadingToc = ref(true)
const bookError = ref('')
// Why the catalog is missing, and whether another source may have the book.
const tocError = ref('')
const tocSourceDown = ref(false)
const alternatives = ref<Book[] | null>(null)
const switching = ref(false)
let openSwitch = false
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
    bookError.value = ''
    uni.setNavigationBarTitle({ title: book.value.title })
  } catch (e) {
    if (!book.value) bookError.value = errorText(e)
  }
}

async function loadToc(refresh = false) {
  loadingToc.value = true
  try {
    chapters.value = await api.chapters(bookId, refresh)
    tocError.value = ''
  } catch (e) {
    if (chapters.value.length) {
      // The catalog on screen is still good; a failed refresh is not worth more than a hint.
      uni.showToast({ title: '暂时刷新不了，显示的是上次的目录', icon: 'none' })
    } else {
      tocError.value = errorText(e)
      tocSourceDown.value = sourceDown(e)
    }
  } finally {
    loadingToc.value = false
  }
}

/** Looks the book up in the other sources and lets the reader move to one of them. */
async function switchSource() {
  if (switching.value) return
  switching.value = true
  uni.showLoading({ title: '正在其他书源中查找' })
  try {
    alternatives.value = await api.alternatives(bookId)
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
      const to = found[tapIndex]
      uni.showLoading({ title: '正在换源' })
      try {
        await api.switchSource(bookId, to.id)
        uni.hideLoading()
        uni.redirectTo({ url: `/pages/book/detail?id=${to.id}` })
      } catch (e) {
        uni.hideLoading()
        uni.showToast({ title: sourceDown(e) ? '这个书源也打不开，换一个试试' : errorText(e), icon: 'none' })
      }
    },
  })
}

onLoad((query) => {
  bookId = Number(query?.id)
  openSwitch = query?.switch === '1'
  loadToc().then(() => {
    // Sent here by the reader after a chapter would not load.
    if (openSwitch) switchSource()
    openSwitch = false
  })
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
      <view class="cover"><BookCover :book="book" /></view>
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
      <wd-button size="small" plain @click="switchSource">换源</wd-button>
    </view>

    <view class="toc-head">
      <text>目录（{{ chapters.length }}）</text>
      <view>
        <text class="link" @click="loadToc(true)">刷新</text>
        <text class="link" @click="reversed = !reversed">{{ reversed ? '正序' : '倒序' }}</text>
      </view>
    </view>
    <LoadState
      v-if="!chapters.length"
      :loading="loadingToc"
      :error="tocError"
      empty="这本书还没有目录"
      @retry="loadToc()"
    >
      <template v-if="tocSourceDown" #actions>
        <wd-button size="small" plain @click="switchSource">换源</wd-button>
      </template>
    </LoadState>
    <view v-for="c in visible" :key="c.id" class="chapter" @click="read(c.id)">
      <text class="ellipsis" :class="{ current: book.progress?.chapterId === c.id }">{{ c.title }}</text>
      <text v-if="downloaded[c.id]" class="muted">已下载</text>
      <text v-else-if="c.cached" class="muted">已缓存</text>
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
    color: var(--brand);
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
    color: var(--brand);
  }

  .muted {
    flex: none;
    margin-left: 16rpx;
  }
}
</style>
