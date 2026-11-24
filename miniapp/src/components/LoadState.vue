<script setup lang="ts">
// The one place a page shows that it is loading, failed or empty, instead of an error toast.
defineProps<{
  loading?: boolean
  /** Failure message; shown with a retry button. */
  error?: string
  /** Shown when there is neither a failure nor content. */
  empty?: string
}>()
defineEmits<{ retry: [] }>()
</script>

<template>
  <view v-if="loading" class="state"><wd-loading /></view>
  <view v-else-if="error" class="state">
    <view class="title">没有加载出来</view>
    <view class="muted reason">{{ error }}</view>
    <view class="actions">
      <wd-button size="small" @click="$emit('retry')">重试</wd-button>
      <slot name="actions" />
    </view>
  </view>
  <view v-else-if="empty" class="state">
    <view class="muted">{{ empty }}</view>
    <slot />
  </view>
</template>

<style lang="scss" scoped>
.state {
  padding: 140rpx 60rpx;
  text-align: center;
}

.title {
  font-size: 30rpx;
  color: #555;
}

.reason {
  margin: 16rpx 0 36rpx;
  line-height: 1.6;
}

.actions {
  display: flex;
  justify-content: center;
  gap: 24rpx;
}
</style>
