<script setup lang="ts">
import { ref, watch } from 'vue'
import { onPullDownRefresh, onShow } from '@dcloudio/uni-app'
import { api, type Book } from '@/api'
import { errorText, toastError } from '@/api/http'
import { useAppStore } from '@/stores/app'
import { getReadPosition, removeReadPosition } from '@/utils/readPosition'
import AppBar from '@/components/AppBar.vue'
import BookCover from '@/components/BookCover.vue'
import LoadState from '@/components/LoadState.vue'

const store = useAppStore()
const books = ref<Book[]>([])
const loading = ref(true)
const error = ref('')
// Bump onShow so local readPosition captions refresh immediately after coming back from reader.
const tick = ref(0)

async function load() {
  tick.value++
  try {
    books.value = await api.favorites(store.mode)
    error.value = ''
  } catch (e) {
    // Only an empty page says that loading failed; a shelf that is already on screen stays.
    error.value = errorText(e)
  } finally {
    loading.value = false
    uni.stopPullDownRefresh()
  }
}

function switchMode() {
  books.value = []
  loading.value = true
  load()
}

onShow(load)
onPullDownRefresh(load)
watch(() => store.mode, switchMode)

function latestProgress(b: Book) {
  void tick.value
  const local = getReadPosition(b.id)
  const server = b.progress
  const serverAt = server ? (server.updatedAt || 0) * 1000 : 0
  if (local && (!server || local.at >= serverAt - 2000)) {
    return { chapterTitle: local.chapterTitle, isComic: local.isComic, page: local.position }
  }
  if (server?.chapterTitle) {
    return { chapterTitle: server.chapterTitle, isComic: b.type === 'comic', page: server.position }
  }
  return null
}

function open(b: Book) {
  const hasProgress = Boolean(latestProgress(b))
  uni.navigateTo({ url: hasProgress ? `/pages/reader/reader?bookId=${b.id}` : `/pages/book/detail?id=${b.id}` })
}

const toSearch = () => uni.switchTab({ url: '/pages/search/search' })

function more(b: Book) {
  uni.showActionSheet({
    itemList: ['查看详情', '移出书架'],
    success: async ({ tapIndex }) => {
      if (tapIndex === 0) {
        uni.navigateTo({ url: `/pages/book/detail?id=${b.id}` })
        return
      }
      try {
        await api.removeFavorite(b.id)
        removeReadPosition(b.id)
        books.value = books.value.filter((x) => x.id !== b.id)
      } catch (e) {
        toastError(e)
      }
    },
  })
}

function caption(b: Book): string {
  const p = latestProgress(b)
  if (!p?.chapterTitle) return '未读'
  return p.chapterTitle
}
</script>

<template>
  <view>
    <AppBar title="书架" />
    <view v-if="books.length" class="grid">
      <view v-for="b in books" :key="b.id" class="cell" hover-class="pressed" @click="open(b)" @longpress="more(b)">
        <BookCover :book="b" />
        <view class="title ellipsis">{{ b.title }}</view>
        <view class="caption ellipsis" :class="{ unread: !latestProgress(b) }">{{ caption(b) }}</view>
      </view>
    </view>
    <LoadState
      v-else
      :loading="loading"
      :error="error"
      :empty="`书架还是空的，去找一本${store.mode === 'novel' ? '小说' : '漫画'}吧`"
      @retry="switchMode"
    >
      <wd-button custom-style="margin-top: 36rpx" size="small" @click="toSearch">去搜索</wd-button>
    </LoadState>
  </view>
</template>

<style lang="scss" scoped>
.grid {
  display: grid;
  grid-template-columns: repeat(3, minmax(0, 1fr));
  gap: 32rpx 24rpx;
  padding: 20rpx 28rpx 48rpx;
}

.cell {
  min-width: 0;

  &.pressed {
    opacity: 0.78;
  }
}

.title {
  margin-top: 14rpx;
  font-size: 26rpx;
  font-weight: 500;
  line-height: 36rpx;
  color: #2b2622;
}

.caption {
  margin-top: 4rpx;
  font-size: 22rpx;
  line-height: 30rpx;
  color: #8a8378;

  &.unread {
    opacity: 0.65;
  }
}
</style>
