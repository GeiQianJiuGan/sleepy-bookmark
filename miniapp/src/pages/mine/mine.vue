<script setup lang="ts">
import { API_BASE } from '@/config'
import { onShow } from '@dcloudio/uni-app'
import { account, accountError, isAdmin, logout, refreshAccount } from '@/api/http'
import AppBar from '@/components/AppBar.vue'
import { useAppStore } from '@/stores/app'

const store = useAppStore()

function loadAccount() {
  refreshAccount().catch(() => {})
}
onShow(loadAccount)

function confirmLogout() {
  uni.showModal({
    title: '退出登录',
    content: '本账号的离线章节和阅读位置会保留，再次登录同一微信账号后可继续使用。',
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
        <view v-if="account" class="state">{{ isAdmin ? '管理员' : '成员' }} · 账号 #{{ account.id }}</view>
        <view v-else-if="accountError" class="link" @click="loadAccount">账号信息加载失败，点击重试</view>
        <view v-else class="state">正在读取账号…</view>
        <view class="muted ellipsis">{{ API_BASE }}</view>
      </view>
    </view>
    <view class="menu">
      <wd-cell-group border>
        <wd-cell title="浏览历史" label="最近读过的小说和漫画" is-link clickable @click="go('/pages/history/history')" />
        <wd-cell :title="isAdmin ? '书源管理' : '共享书源'" :label="isAdmin ? '管理书源和订阅' : '查看书源和订阅'" is-link clickable @click="go('/pages/sources/list')" />
        <wd-cell v-if="isAdmin" title="账号列表" label="查看注册成员、复制 OpenID" is-link clickable @click="go('/pages/mine/users')" />
        <wd-cell title="下载与缓存" label="离线文章、章节进度和占用空间" is-link clickable @click="go('/pages/downloads/downloads')" />
      </wd-cell-group>
    </view>
    <view class="menu">
      <wd-cell-group border>
        <wd-cell title="省流模式" label="不加载封面，用书名代替" center>
          <wd-switch v-model="store.dataSaver" size="20px" />
        </wd-cell>
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
