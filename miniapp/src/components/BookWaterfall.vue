<script setup lang="ts">
// Two columns of book cards; each card goes to the column that is shorter so far, so cards
// of different heights pack without gaps and a card never moves when more are appended.
import { computed } from 'vue'
import type { Book } from '@/api'
import BookCover from './BookCover.vue'

type Card = Book & { reason?: string }
const props = defineProps<{ books: Card[]; ranked?: boolean }>()
defineEmits<{ open: [book: Book] }>()

// Rough card heights in rpx, from the same numbers as the styles below.
function height(b: Card) {
  const lines = (text: string, perLine: number, max: number) => Math.min(max, Math.ceil(text.length / perLine))
  return 454 + 20 + lines(b.title, 10, 2) * 40 + 38 + (b.intro ? 8 + lines(b.intro, 12, 3) * 36 : 0) + (b.reason ? 48 : 0) + 44
}

const columns = computed(() => {
  const cols: { book: Card; rank: number }[][] = [[], []]
  const tall = [0, 0]
  props.books.forEach((book, i) => {
    const c = tall[1] < tall[0] ? 1 : 0
    cols[c].push({ book, rank: i + 1 })
    tall[c] += height(book)
  })
  return cols
})
</script>

<template>
  <view class="waterfall">
    <view v-for="(col, c) in columns" :key="c" class="column">
      <view v-for="{ book, rank } in col" :key="book.id" class="card" hover-class="pressed" @click="$emit('open', book)">
        <view class="cover">
          <BookCover :book="book" flat />
          <view v-if="ranked" class="rank" :class="{ top: rank <= 3 }">{{ rank }}</view>
        </view>
        <view class="body">
          <view class="title">{{ book.title }}</view>
          <!-- A ranking is one source's list, which the page names above it. -->
          <view class="muted ellipsis">{{ book.author || '未知作者' }}{{ ranked ? '' : ` · ${book.sourceName}` }}</view>
          <view v-if="book.intro" class="muted intro">{{ book.intro }}</view>
          <view v-if="book.reason" class="reason">{{ book.reason }}</view>
        </view>
      </view>
    </view>
  </view>
</template>

<style lang="scss" scoped>
.waterfall {
  display: flex;
  align-items: flex-start;
  padding: 0 24rpx;
}

.column {
  flex: 1;
  min-width: 0;

  & + & {
    margin-left: 20rpx;
  }
}

.card {
  margin-bottom: 20rpx;
  overflow: hidden;
  border-radius: 20rpx;
  background: #fff;

  &.pressed {
    opacity: 0.8;
  }
}

.cover {
  position: relative;
}

.rank {
  position: absolute;
  top: 0;
  left: 0;
  min-width: 48rpx;
  height: 44rpx;
  font-size: 24rpx;
  font-weight: 700;
  line-height: 44rpx;
  text-align: center;
  color: #fff;
  border-radius: 0 0 16rpx 0;
  background: rgba(43, 38, 34, 0.62);

  &.top {
    background: var(--brand);
  }
}

.body {
  padding: 20rpx 20rpx 24rpx;
}

.title {
  display: -webkit-box;
  overflow: hidden;
  font-size: 28rpx;
  font-weight: 600;
  line-height: 40rpx;
  -webkit-box-orient: vertical;
  -webkit-line-clamp: 2;
}

.muted {
  margin-top: 6rpx;
  font-size: 22rpx;
  line-height: 32rpx;
}

.intro {
  display: -webkit-box;
  margin-top: 8rpx;
  overflow: hidden;
  line-height: 36rpx;
  -webkit-box-orient: vertical;
  -webkit-line-clamp: 3;
}

.reason {
  display: inline-block;
  margin-top: 12rpx;
  padding: 0 14rpx;
  font-size: 20rpx;
  line-height: 36rpx;
  color: var(--brand);
  border-radius: 18rpx;
  background: #f6e3df;
}
</style>
