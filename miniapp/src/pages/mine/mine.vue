<script setup lang="ts">
import { API_BASE } from '@/config'
import { logout } from '@/api/http'
import AppBar from '@/components/AppBar.vue'

function confirmLogout() {
  uni.showModal({
    title: '退出登录',
    content: '退出后需要重新登录。本机的阅读缓存和离线下载会保留。',
    confirmText: '退出',
    cancelText: '取消',
    success: (res) => {
      if (res.confirm) logout()
    },
  })
}

const go = (url: string) => uni.navigateTo({ url })
</script>

<template>
  <view>
    <AppBar title="我的" />
    <view class="account">
      <view class="avatar">阅</view>
      <view class="who">
        <view class="state">已登录</view>
        <view class="muted ellipsis">{{ API_BASE }}</view>
      </view>
    </view>
    <view class="menu">
      <wd-cell-group border>
        <wd-cell title="浏览历史" label="最近读过的小说和漫画" is-link clickable @click="go('/pages/history/history')" />
        <wd-cell title="书源管理" label="新增、导入、启停书源" is-link clickable @click="go('/pages/sources/list')" />
        <wd-cell title="下载与缓存" label="离线内容和占用空间" is-link clickable @click="go('/pages/downloads/downloads')" />
      </wd-cell-group>
    </view>
    <view class="menu">
      <wd-cell-group border>
        <wd-cell title="用户协议与隐私政策" is-link clickable @click="go('/pages/login/privacy')" />
        <wd-cell title="退出登录" clickable @click="confirmLogout" />
      </wd-cell-group>
    </view>
  </view>
</template>

<style lang="scss" scoped>
.account {
  display: flex;
  align-items: center;
  margin: 24rpx;
  padding: 40rpx 32rpx;
  border-radius: 16rpx;
  background: #fff;
}

.avatar {
  flex: none;
  width: 96rpx;
  height: 96rpx;
  margin-right: 28rpx;
  border-radius: 50%;
  font-size: 44rpx;
  line-height: 96rpx;
  text-align: center;
  color: #fff;
  background: var(--brand);
}

.who {
  flex: 1;
  min-width: 0;
}

.state {
  margin-bottom: 8rpx;
  font-size: 34rpx;
  font-weight: 600;
}

.menu {
  margin: 0 24rpx 24rpx;
  overflow: hidden;
  border-radius: 16rpx;
}
</style>
