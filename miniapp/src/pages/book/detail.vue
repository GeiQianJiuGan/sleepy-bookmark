<script setup lang="ts">
import { computed, ref } from 'vue'
import { onLoad, onReachBottom, onShow } from '@dcloudio/uni-app'
import { api, type Book, type Chapter } from '@/api'
import { errorText, sourceDown, toastError } from '@/api/http'
import BookCover from '@/components/BookCover.vue'
import LoadState from '@/components/LoadState.vue'
import { chapterStatus, enqueueChapters, offlineBook, offlineSupported } from '@/utils/offline'
import { getReadPosition } from '@/utils/readPosition'

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
const introOpen = ref(false)
const shown = ref(PAGE)
const localBook = computed(() => book.value && offlineBook(book.value.id))
const openDownloads = () => uni.navigateTo({ url: '/pages/downloads/downloads' })

const ordered = computed(() => (reversed.value ? [...chapters.value].reverse() : chapters.value))
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
  uni.showLoading({ title: '正在其他书源中查找', mask: true })
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
      // One switch at a time: a second one would start from the same shelf entry.
      if (switching.value) return
      switching.value = true
      const to = found[tapIndex]
      uni.showLoading({ title: '正在换源', mask: true })
      try {
        await api.switchSource(bookId, to.id)
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
  localPos.value = getReadPosition(bookId)
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

function download(list: Chapter[]) {
  if (!book.value || !list.length) return
  try {
    const added = enqueueChapters(book.value, list)
    uni.showToast({ title: added ? `已加入 ${added} 章` : '章节已离线或已在队列中', icon: 'none' })
  } catch (e) {
    toastError(e)
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
        <view v-if="currentChapterTitle" class="muted ellipsis">读到：{{ currentChapterTitle }}</view>
      </view>
    </view>
    <view v-if="book.intro" class="intro" :class="{ folded: !introOpen }" @click="introOpen = !introOpen">
      {{ book.intro }}
    </view>

    <view class="actions">
      <wd-button size="small" :disabled="!chapters.length" @click="read()">{{ currentChapterId ? '继续阅读' : '开始阅读' }}</wd-button>
      <wd-button size="small" plain @click="toggleFavorite">{{ book.favorite ? '移出书架' : '加入书架' }}</wd-button>
      <wd-button v-if="offlineSupported" size="small" plain :disabled="!chapters.length" @click="download(chapters)">整本离线下载</wd-button>
      <wd-button v-if="localBook" size="small" plain @click="openDownloads">查看离线进度</wd-button>
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
      <text class="ellipsis" :class="{ current: currentChapterId === c.id }">{{ c.title }}</text>
      <text v-if="localBook && localBook.chapters[c.id]" class="muted" @click.stop="openDownloads">{{ chapterStatus(localBook, localBook.chapters[c.id]) }}</text>
      <view v-else>
        <text v-if="c.cached" class="muted">已缓存</text>
        <text v-if="offlineSupported" class="link" @click.stop="download([c])">下载</text>
      </view>
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
  border-bottom: 1rpx solid var(--line);

  .ellipsis {
    flex: 1;
    min-width: 0;
  }

  .current {
    color: var(--brand);
  }

  .link {
    margin-left: 16rpx;
  }

  .muted {
    flex: none;
    margin-left: 16rpx;
  }
}
</style>
