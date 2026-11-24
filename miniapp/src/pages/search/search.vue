<script setup lang="ts">
import { ref, watch } from 'vue'
import { api, type Book, type SearchResult } from '@/api'
import { toastError } from '@/api/http'
import { useAppStore } from '@/stores/app'
import ModeSwitch from '@/components/ModeSwitch.vue'
import BookItem from '@/components/BookItem.vue'

const store = useAppStore()
const keyword = ref('')
const books = ref<Book[]>([])
const errors = ref<SearchResult['errors']>([])
const loading = ref(false)
const searched = ref(false)

async function search() {
  const key = keyword.value.trim()
  if (!key) return
  loading.value = true
  try {
    const res = await api.search(store.mode, key)
    books.value = res.books
    errors.value = res.errors
    searched.value = true
  } catch (e) {
    toastError(e)
  } finally {
    loading.value = false
  }
}

const open = (b: Book) => uni.navigateTo({ url: `/pages/book/detail?id=${b.id}` })

function reset() {
  books.value = []
  errors.value = []
  searched.value = false
}

watch(
  () => store.mode,
  () => {
    reset()
    search()
  },
)
</script>

<template>
  <view>
    <ModeSwitch />
    <wd-search
      v-model="keyword"
      hide-cancel
      :placeholder="store.mode === 'novel' ? '搜索小说：书名 / 作者' : '搜索漫画：漫画名 / 作者'"
      @search="search"
      @clear="reset"
    />
    <view v-if="loading" class="empty"><wd-loading /></view>
    <template v-else>
      <view v-for="e in errors" :key="e.source" class="source-error">{{ e.source }}：{{ e.error }}</view>
      <view v-for="b in books" :key="b.id" @click="open(b)">
        <BookItem :book="b" show-intro />
      </view>
      <view v-if="searched && !books.length" class="empty">没有找到结果，可以到「我的 › 书源管理」检查书源</view>
    </template>
  </view>
</template>

<style lang="scss" scoped>
.source-error {
  padding: 12rpx 24rpx;
  font-size: 24rpx;
  color: #b9770e;
  background: #fef5e7;
}
</style>
