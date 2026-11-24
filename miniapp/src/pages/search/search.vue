<script setup lang="ts">
import { ref, watch } from 'vue'
import { api, type Book, type SearchResult } from '@/api'
import { errorText } from '@/api/http'
import { useAppStore } from '@/stores/app'
import ModeSwitch from '@/components/ModeSwitch.vue'
import BookItem from '@/components/BookItem.vue'
import LoadState from '@/components/LoadState.vue'

const store = useAppStore()
const keyword = ref('')
const books = ref<Book[]>([])
const errors = ref<SearchResult['errors']>([])
const loading = ref(false)
const searched = ref(false)
const error = ref('')
// Sources that did not answer are one quiet line; the details open on tap.
const showErrors = ref(false)

async function search() {
  const key = keyword.value.trim()
  if (!key) return
  loading.value = true
  error.value = ''
  showErrors.value = false
  try {
    const res = await api.search(store.mode, key)
    books.value = res.books
    errors.value = res.errors
    searched.value = true
  } catch (e) {
    books.value = []
    errors.value = []
    error.value = errorText(e)
  } finally {
    loading.value = false
  }
}

const open = (b: Book) => uni.navigateTo({ url: `/pages/book/detail?id=${b.id}` })

function reset() {
  books.value = []
  errors.value = []
  searched.value = false
  error.value = ''
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
    <LoadState v-if="loading || error" :loading="loading" :error="error" @retry="search" />
    <template v-else>
      <view v-for="b in books" :key="b.id" @click="open(b)">
        <BookItem :book="b" show-intro />
      </view>
      <LoadState
        v-if="searched && !books.length"
        :empty="errors.length ? '书源暂时都没有返回结果，稍后再试' : '没有找到相关内容，换个关键词试试'"
      >
        <wd-button custom-style="margin-top: 36rpx" size="small" plain @click="search">再搜一次</wd-button>
      </LoadState>
      <view v-if="errors.length" class="source-errors" @click="showErrors = !showErrors">
        <view class="muted">{{ errors.length }} 个书源暂时没有返回结果 {{ showErrors ? '▴' : '▾' }}</view>
        <template v-if="showErrors">
          <view v-for="e in errors" :key="e.source" class="muted detail">{{ e.source }}：{{ e.error }}</view>
        </template>
      </view>
    </template>
  </view>
</template>

<style lang="scss" scoped>
.source-errors {
  padding: 28rpx 32rpx 48rpx;
  text-align: center;

  .detail {
    margin-top: 12rpx;
    text-align: left;
  }
}
</style>
