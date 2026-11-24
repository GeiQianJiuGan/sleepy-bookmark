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
  background: #fff;
  border-bottom: 1rpx solid #eee;
}

.tab {
  padding: 22rpx 60rpx 18rpx;
  font-size: 30rpx;
  color: #8a8a8a;
  border-bottom: 4rpx solid transparent;

  &.active {
    color: #c0392b;
    font-weight: 600;
    border-bottom-color: #c0392b;
  }
}
</style>
