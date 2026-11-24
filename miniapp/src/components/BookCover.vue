<script setup lang="ts">
import { ref, watch } from 'vue'
import type { Book } from '@/api'
import { absoluteUrl } from '@/api/http'

const props = defineProps<{ book: Pick<Book, 'title' | 'cover'> }>()
// A cover that fails to load (dead image link) falls back to the title card.
const broken = ref(false)
watch(
  () => props.book.cover,
  () => (broken.value = false),
)
</script>

<template>
  <view class="cover">
    <image
      v-if="book.cover && !broken"
      class="img"
      :src="absoluteUrl(book.cover)"
      mode="aspectFill"
      lazy-load
      @error="broken = true"
    />
    <view v-else class="card">{{ book.title }}</view>
  </view>
</template>

<style lang="scss" scoped>
.cover {
  position: relative;
  width: 100%;
  // 3:4, the usual cover ratio
  padding-top: 133%;
  overflow: hidden;
  border-radius: 10rpx;
  background: #e9e4da;
  box-shadow: 0 4rpx 14rpx rgba(60, 40, 20, 0.12);
}

.img,
.card {
  position: absolute;
  inset: 0;
  width: 100%;
  height: 100%;
}

.card {
  box-sizing: border-box;
  display: flex;
  align-items: center;
  justify-content: center;
  padding: 16rpx;
  font-size: 26rpx;
  line-height: 1.4;
  text-align: center;
  color: #7a6a55;
  background: linear-gradient(160deg, #f1ebdf, #ded4c2);
}
</style>
