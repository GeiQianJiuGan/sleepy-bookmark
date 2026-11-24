<script setup lang="ts">
import { ref, watch } from 'vue'
import { onPullDownRefresh, onShow } from '@dcloudio/uni-app'
import { api, type Book } from '@/api'
import { toastError } from '@/api/http'
import { useAppStore } from '@/stores/app'
import ModeSwitch from '@/components/ModeSwitch.vue'
import BookItem from '@/components/BookItem.vue'

const store = useAppStore()
const books = ref<Book[]>([])
const loading = ref(true)

async function load() {
  try {
    books.value = await api.favorites(store.mode)
  } catch (e) {
    toastError(e)
  } finally {
    loading.value = false
    uni.stopPullDownRefresh()
  }
}

onShow(load)
onPullDownRefresh(load)
watch(() => store.mode, load)

function open(b: Book) {
  uni.navigateTo({ url: b.progress ? `/pages/reader/reader?bookId=${b.id}` : `/pages/book/detail?id=${b.id}` })
}

const toSearch = () => uni.switchTab({ url: '/pages/search/search' })

function more(b: Book) {
  uni.showActionSheet({
    itemList: ['查看详情', '取消收藏'],
    success: async ({ tapIndex }) => {
      if (tapIndex === 0) {
        uni.navigateTo({ url: `/pages/book/detail?id=${b.id}` })
        return
      }
      try {
        await api.removeFavorite(b.id)
        await load()
      } catch (e) {
        toastError(e)
      }
    },
  })
}
</script>

<template>
  <view>
    <ModeSwitch />
    <view v-for="b in books" :key="b.id" @click="open(b)" @longpress="more(b)">
      <BookItem :book="b" />
    </view>
    <view v-if="!loading && !books.length" class="empty">
      <view>书架还是空的</view>
      <view class="muted" style="margin: 16rpx 0 40rpx">去搜索页找一本{{ store.mode === 'novel' ? '小说' : '漫画' }}，点「加入书架」</view>
      <wd-button size="small" @click="toSearch">去搜索</wd-button>
    </view>
  </view>
</template>
