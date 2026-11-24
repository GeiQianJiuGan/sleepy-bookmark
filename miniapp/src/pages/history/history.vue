<script setup lang="ts">
import { ref, watch } from 'vue'
import { onPullDownRefresh, onShow } from '@dcloudio/uni-app'
import { api, type Book } from '@/api'
import { errorText, toastError } from '@/api/http'
import { useAppStore } from '@/stores/app'
import AppBar from '@/components/AppBar.vue'
import BookItem from '@/components/BookItem.vue'
import LoadState from '@/components/LoadState.vue'

const store = useAppStore()
const books = ref<Book[]>([])
const loading = ref(true)
const error = ref('')

async function load() {
  try {
    books.value = await api.history(store.mode)
    error.value = ''
  } catch (e) {
    error.value = errorText(e)
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
    confirmText: b ? '删除' : '清空',
    cancelText: '取消',
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
    <AppBar title="浏览历史" back />
    <view v-for="b in books" :key="b.id" @click="open(b)" @longpress="remove(b)">
      <BookItem :book="b" />
    </view>
    <view v-if="books.length" class="footer">
      <view class="muted">长按可删除单条记录</view>
      <wd-button size="small" plain type="error" @click="remove()">清空全部</wd-button>
    </view>
    <LoadState v-if="!books.length" :loading="loading" :error="error" empty="还没有浏览记录" @retry="load" />
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
