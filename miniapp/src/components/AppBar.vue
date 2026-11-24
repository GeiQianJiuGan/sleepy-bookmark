<script setup lang="ts">
// The top bar of the pages that follow the novel / comic mode. It replaces the native navigation
// bar so that the mode switch can sit in the top left corner, like the icon of the app in use.
import { useAppStore } from '@/stores/app'

defineProps<{ title?: string; back?: boolean }>()
const store = useAppStore()

const win = uni.getSystemInfoSync()
const top = win.statusBarHeight || 0
let height = 44
let right = 0
// #ifdef MP-WEIXIN
// Line the row up with the capsule button and keep clear of it.
const capsule = uni.getMenuButtonBoundingClientRect()
height = (capsule.top - top) * 2 + capsule.height
right = win.windowWidth - capsule.left
// #endif

function toggle() {
  store.mode = store.mode === 'novel' ? 'comic' : 'novel'
}

function goBack() {
  if (getCurrentPages().length > 1) uni.navigateBack()
  else uni.switchTab({ url: '/pages/mine/mine' })
}
</script>

<template>
  <view class="bar" :style="{ paddingTop: top + 'px' }">
    <view class="row" :style="{ height: height + 'px', paddingRight: right + 'px' }">
      <view v-if="back" class="back" @click="goBack"><view class="chevron" /></view>
      <view class="mode" :class="{ 'after-back': back }" hover-class="pressed" @click="toggle">
        <view class="badge" :class="store.mode">
          <image class="icon" :class="{ on: store.mode === 'novel' }" src="/static/mode/novel.png" />
          <image class="icon" :class="{ on: store.mode === 'comic' }" src="/static/mode/comic.png" />
        </view>
        <view class="name">{{ store.mode === 'novel' ? '小说' : '漫画' }}</view>
        <image class="swap" src="/static/mode/swap.png" />
      </view>
      <view v-if="title" class="title">{{ title }}</view>
    </view>
  </view>
</template>

<style lang="scss" scoped>
.bar {
  position: sticky;
  top: 0;
  z-index: 10;
  background: var(--paper);
}

.row {
  position: relative;
  display: flex;
  align-items: center;
  box-sizing: border-box;
}

.back {
  display: flex;
  align-items: center;
  justify-content: center;
  width: 72rpx;
  height: 100%;
}

.chevron {
  width: 18rpx;
  height: 18rpx;
  margin-left: 12rpx;
  border-left: 4rpx solid #2b2622;
  border-bottom: 4rpx solid #2b2622;
  transform: rotate(45deg);
}

.mode {
  position: relative;
  z-index: 1;
  display: flex;
  align-items: center;
  height: 100%;
  padding: 0 20rpx 0 24rpx;

  &.after-back {
    padding-left: 4rpx;
  }

  &.pressed {
    opacity: 0.6;
  }
}

.badge {
  position: relative;
  width: 60rpx;
  height: 60rpx;
  border-radius: 16rpx;
  transition: background 0.25s;

  &.novel {
    background: linear-gradient(145deg, #d4513f, #b03024);
    box-shadow: 0 4rpx 12rpx rgba(192, 57, 43, 0.3);
  }

  &.comic {
    background: linear-gradient(145deg, #3f8bad, #28607c);
    box-shadow: 0 4rpx 12rpx rgba(47, 111, 143, 0.3);
  }
}

.icon {
  position: absolute;
  top: 10rpx;
  left: 10rpx;
  width: 40rpx;
  height: 40rpx;
  opacity: 0;
  transform: rotate(-90deg) scale(0.4);
  transition:
    opacity 0.25s,
    transform 0.25s;

  &.on {
    opacity: 1;
    transform: none;
  }
}

.name {
  margin-left: 16rpx;
  font-size: 34rpx;
  font-weight: 700;
  letter-spacing: 2rpx;
}

.swap {
  width: 28rpx;
  height: 28rpx;
  margin-left: 8rpx;
}

.title {
  position: absolute;
  left: 0;
  right: 0;
  font-size: 32rpx;
  font-weight: 500;
  text-align: center;
  pointer-events: none;
}
</style>
