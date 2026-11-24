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
      <view class="state" :class="{ failed }">{{ status }}</view>
      <view class="muted">服务器：{{ API_BASE }}</view>
      <wd-button v-if="failed" size="small" custom-style="margin-top: 20rpx" @click="login">重试</wd-button>
    </view>
    <wd-cell-group border>
      <wd-cell title="浏览历史" is-link clickable @click="go('/pages/history/history')" />
      <wd-cell title="书源管理" is-link clickable @click="go('/pages/sources/list')" />
      <wd-cell title="下载与缓存" is-link clickable @click="go('/pages/downloads/downloads')" />
    </wd-cell-group>
  </view>
</template>

<style lang="scss" scoped>
.account {
  padding: 48rpx 32rpx;
  margin-bottom: 20rpx;
  background: #fff;
}

.state {
  margin-bottom: 12rpx;
  font-size: 34rpx;
  font-weight: 600;

  &.failed {
    font-size: 28rpx;
    font-weight: 400;
    color: #c0392b;
  }
}
</style>
