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
    books.value = await api.history(store.mode)
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

const open = (b: Book) => uni.navigateTo({ url: `/pages/reader/reader?bookId=${b.id}` })

function remove(b?: Book) {
  uni.showModal({
    title: b ? `删除《${b.title}》的浏览记录？` : '清空全部浏览记录？',
    content: b ? '' : '小说和漫画的记录都会被清空',
    success: async ({ confirm }) => {
      if (!confirm) return
      try {
        await api.removeHistory(b?.id)
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
    <view v-for="b in books" :key="b.id" @click="open(b)" @longpress="remove(b)">
      <BookItem :book="b" />
    </view>
    <view v-if="books.length" class="footer">
      <view class="muted">长按可删除单条记录</view>
      <wd-button size="small" plain type="error" @click="remove()">清空全部</wd-button>
    </view>
    <view v-if="!loading && !books.length" class="empty">还没有浏览记录</view>
  </view>
</template>

<style lang="scss" scoped>
.footer {
  padding: 40rpx;
  text-align: center;

  .muted {
    margin-bottom: 24rpx;
  }
}
</style>
