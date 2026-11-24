<script setup lang="ts">
import { computed } from 'vue'
import type { Book } from '@/api'
import BookCover from './BookCover.vue'

const props = defineProps<{ book: Book; showIntro?: boolean }>()

const subtitle = computed(() => {
  const p = props.book.progress
  if (p?.chapterTitle) return `读到：${p.chapterTitle}`
  return props.book.latest ? `最新：${props.book.latest}` : ''
})
</script>

<template>
  <view class="book">
    <view class="cover"><BookCover :book="book" /></view>
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
  margin: 0 24rpx 20rpx;
  padding: 24rpx;
  border-radius: 16rpx;
  background: #fff;
}

.cover {
  flex: none;
  width: 140rpx;
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
  line-height: 1.5;
}
</style>
