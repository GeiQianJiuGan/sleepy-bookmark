<script setup lang="ts">
import { useAppStore } from '@/stores/app'

const store = useAppStore()
const tabs = [
  { value: 'novel', label: '小说' },
  { value: 'comic', label: '漫画' },
] as const
</script>

<template>
  <view class="mode-switch">
    <view
      v-for="t in tabs"
      :key="t.value"
      class="tab"
      :class="{ active: store.mode === t.value }"
      @click="store.mode = t.value"
    >
      {{ t.label }}
    </view>
  </view>
</template>

<style lang="scss" scoped>
.mode-switch {
  position: sticky;
  top: 0;
  z-index: 10;
  display: flex;
  justify-content: center;
  padding: 16rpx 0;
  background: var(--paper);
}

.tab {
  padding: 10rpx 56rpx;
  font-size: 28rpx;
  color: #8a8178;
  background: #ebe6de;
  transition: all 0.2s;

  &:first-child {
    border-radius: 32rpx 0 0 32rpx;
  }

  &:last-child {
    border-radius: 0 32rpx 32rpx 0;
  }

  &.active {
    color: #fff;
    font-weight: 600;
    background: var(--brand);
  }
}
</style>
