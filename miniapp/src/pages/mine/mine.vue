<script setup lang="ts">
import { ref } from 'vue'
import { onShow } from '@dcloudio/uni-app'
import { API_BASE } from '@/config'
import { ensureLogin } from '@/api/http'

const status = ref('登录中…')
const failed = ref(false)

async function login() {
  status.value = '登录中…'
  try {
    await ensureLogin()
    status.value = '已登录'
    failed.value = false
  } catch (e) {
    status.value = e instanceof Error ? e.message : String(e)
    failed.value = true
  }
}

onShow(login)

const go = (url: string) => uni.navigateTo({ url })
</script>

<template>
  <view>
    <view class="account">
      <view class="avatar">阅</view>
      <view class="who">
        <view class="state" :class="{ failed }">{{ status }}</view>
        <view class="muted ellipsis">{{ API_BASE }}</view>
      </view>
      <wd-button v-if="failed" size="small" plain @click="login">重试</wd-button>
    </view>
    <view class="menu">
      <wd-cell-group border>
        <wd-cell title="浏览历史" label="最近读过的小说和漫画" is-link clickable @click="go('/pages/history/history')" />
        <wd-cell title="书源管理" label="新增、导入、启停书源" is-link clickable @click="go('/pages/sources/list')" />
        <wd-cell title="下载与缓存" label="离线内容和占用空间" is-link clickable @click="go('/pages/downloads/downloads')" />
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

  &.failed {
    font-size: 28rpx;
    font-weight: 400;
    color: var(--brand);
  }
}

.menu {
  margin: 0 24rpx;
  overflow: hidden;
  border-radius: 16rpx;
}
</style>
