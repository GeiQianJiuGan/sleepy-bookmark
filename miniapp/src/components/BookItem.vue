<script setup lang="ts">
import { computed } from 'vue'
import type { Book } from '@/api'
import { absoluteUrl } from '@/api/http'

const props = defineProps<{ book: Book; showIntro?: boolean }>()

const subtitle = computed(() => {
  const p = props.book.progress
  if (p?.chapterTitle) return `读到：${p.chapterTitle}`
  return props.book.latest ? `最新：${props.book.latest}` : ''
})
</script>

<template>
  <view class="book">
    <image v-if="book.cover" class="cover" :src="absoluteUrl(book.cover)" mode="aspectFill" lazy-load />
    <view v-else class="cover placeholder">{{ book.title.slice(0, 1) }}</view>
    <view class="info">
      <view class="title ellipsis">{{ book.title }}</view>
      <view class="muted ellipsis">{{ book.author || '未知作者' }} · {{ book.sourceName }}</view>
      <view v-if="showIntro && book.intro" class="muted intro">{{ book.intro }}</view>
      <view v-if="subtitle" class="muted ellipsis">{{ subtitle }}</view>
    </view>
  </view>
</template>

<style lang="scss" scoped>
.book {
  display: flex;
  padding: 24rpx;
  background: #fff;
  border-bottom: 1rpx solid #f0f0f0;
}

.cover {
  flex: none;
  width: 140rpx;
  height: 186rpx;
  border-radius: 8rpx;
  background: #eee;
}

.placeholder {
  display: flex;
  align-items: center;
  justify-content: center;
  font-size: 56rpx;
  color: #bbb;
}

.info {
  flex: 1;
  min-width: 0;
  margin-left: 24rpx;
  display: flex;
  flex-direction: column;
  justify-content: space-between;
}

.title {
  font-size: 32rpx;
  font-weight: 600;
}

.intro {
  display: -webkit-box;
  -webkit-line-clamp: 2;
  -webkit-box-orient: vertical;
  overflow: hidden;
}
</style>
