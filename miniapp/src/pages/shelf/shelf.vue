<script setup lang="ts">
import { ref, watch } from 'vue'
import { onPullDownRefresh, onShow } from '@dcloudio/uni-app'
import { api, type Book } from '@/api'
import { errorText, toastError } from '@/api/http'
import { useAppStore } from '@/stores/app'
import AppBar from '@/components/AppBar.vue'
import BookCover from '@/components/BookCover.vue'
import LoadState from '@/components/LoadState.vue'

const store = useAppStore()
const books = ref<Book[]>([])
const loading = ref(true)
const error = ref('')

async function load() {
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

function open(b: Book) {
  uni.navigateTo({ url: b.progress ? `/pages/reader/reader?bookId=${b.id}` : `/pages/book/detail?id=${b.id}` })
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
        books.value = books.value.filter((x) => x.id !== b.id)
      } catch (e) {
        toastError(e)
      }
    },
  })
}

function caption(b: Book): string {
  return b.progress?.chapterTitle || '未读'
}
</script>

<template>
  <view>
    <AppBar title="书架" />
    <view v-if="books.length" class="grid">
      <view v-for="b in books" :key="b.id" class="cell" @click="open(b)" @longpress="more(b)">
        <BookCover :book="b" />
        <view class="title">{{ b.title }}</view>
        <view class="muted ellipsis">{{ caption(b) }}</view>
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
  display: flex;
  flex-wrap: wrap;
  padding: 12rpx 16rpx 40rpx;
}

.cell {
  box-sizing: border-box;
  width: 33.333%;
  padding: 16rpx;
}

.title {
  display: -webkit-box;
  margin-top: 14rpx;
  overflow: hidden;
  font-size: 26rpx;
  line-height: 1.4;
  -webkit-box-orient: vertical;
  -webkit-line-clamp: 2;
}

.muted {
  margin-top: 6rpx;
  font-size: 22rpx;
}
</style>
