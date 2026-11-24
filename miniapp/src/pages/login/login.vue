<script setup lang="ts">
import { ref } from 'vue'
import { onLoad } from '@dcloudio/uni-app'
import { errorText, login } from '@/api/http'
import { privacyAgreed, setPrivacyAgreed } from '@/utils/privacy'

const agreed = ref(privacyAgreed())
const asking = ref(false)
const busy = ref(false)
const error = ref('')
// Name of the privacy guideline registered for this mini program in the WeChat console, if any.
const contract = ref('')

onLoad(() => {
  // #ifdef MP-WEIXIN
  uni.getPrivacySetting?.({
    success: (res) => {
      contract.value = res.privacyContractName || ''
    },
  })
  // #endif
  // First visit: ask for consent before anything else.
  asking.value = !agreed.value
})

function agree() {
  agreed.value = true
  asking.value = false
  setPrivacyAgreed(true)
}

function decline() {
  agreed.value = false
  asking.value = false
  setPrivacyAgreed(false)
}

function toggle() {
  if (agreed.value) decline()
  else asking.value = true
}

async function submit() {
  if (busy.value) return
  if (!agreed.value) {
    asking.value = true
    return
  }
  busy.value = true
  error.value = ''
  try {
    await login()
    uni.switchTab({ url: '/pages/shelf/shelf' })
  } catch (e) {
    error.value = errorText(e)
  } finally {
    busy.value = false
  }
}

const openPolicy = () => uni.navigateTo({ url: '/pages/login/privacy' })

function openContract() {
  // #ifdef MP-WEIXIN
  uni.openPrivacyContract({ fail: openPolicy })
  // #endif
}
</script>

<template>
  <view class="page">
    <view class="hero">
      <view class="logo">阅</view>
      <view class="name">书签睡着了</view>
      <view class="muted">自用小说 / 漫画阅读器</view>
    </view>

    <view class="actions">
      <wd-button block size="large" :loading="busy" @click="submit">微信登录</wd-button>
      <view v-if="error" class="error">{{ error }}</view>
      <view class="consent">
        <view class="check" :class="{ on: agreed }" @click="toggle"><view v-if="agreed" class="tick" /></view>
        <view class="text">
          <text @click="toggle">我已阅读并同意</text>
          <text class="link" @click="openPolicy">《用户协议与隐私政策》</text>
          <text v-if="contract" class="link" @click="openContract">{{ contract }}</text>
        </view>
      </view>
    </view>

    <view v-if="asking" class="mask">
      <view class="sheet">
        <view class="title">用户协议与隐私政策</view>
        <view class="body">
          <view>登录前请阅读并同意以下内容：</view>
          <view>· 登录时使用微信登录凭证获取你的 openid，仅用于识别账号，不获取昵称、头像、手机号。</view>
          <view>· 书架、浏览历史和阅读进度保存在本小程序的服务器上，用于多设备同步。</view>
          <view>· 阅读缓存和离线下载保存在本机，可在「下载与缓存」中清除。</view>
          <view>· 不接入广告和统计，不向第三方提供你的信息。</view>
          <view class="more">
            完整内容见
            <text class="link" @click="openPolicy">《用户协议与隐私政策》</text>
            <text v-if="contract" class="link" @click="openContract">{{ contract }}</text>
          </view>
        </view>
        <view class="buttons">
          <button class="btn plain" hover-class="pressed" @click="decline">不同意</button>
          <!-- The open-type also records the consent with WeChat's own privacy check. -->
          <button id="agree-btn" class="btn" hover-class="pressed" open-type="agreePrivacyAuthorization" @click="agree">
            同意
          </button>
        </view>
      </view>
    </view>
  </view>
</template>

<style lang="scss" scoped>
.page {
  display: flex;
  flex-direction: column;
  min-height: 100vh;
  padding: 0 64rpx;
  box-sizing: border-box;
}

.hero {
  flex: 1;
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
}

.logo {
  width: 168rpx;
  height: 168rpx;
  border-radius: 40rpx;
  font-size: 88rpx;
  line-height: 168rpx;
  text-align: center;
  color: #fff;
  background: var(--brand);
}

.name {
  margin: 36rpx 0 12rpx;
  font-size: 44rpx;
  font-weight: 600;
}

.actions {
  padding-bottom: 160rpx;
}

.error {
  margin-top: 24rpx;
  font-size: 26rpx;
  text-align: center;
  color: var(--brand);
}

.consent {
  display: flex;
  margin-top: 36rpx;
  font-size: 24rpx;
  line-height: 36rpx;
  color: #8a8a8a;
}

.check {
  flex: none;
  width: 32rpx;
  height: 32rpx;
  margin: 2rpx 14rpx 0 0;
  border: 2rpx solid #c4bdb3;
  border-radius: 50%;
  box-sizing: border-box;

  &.on {
    border-color: var(--brand);
    background: var(--brand);
  }
}

.tick {
  width: 14rpx;
  height: 8rpx;
  margin: 8rpx 0 0 7rpx;
  border-left: 3rpx solid #fff;
  border-bottom: 3rpx solid #fff;
  transform: rotate(-45deg);
}

.mask {
  position: fixed;
  inset: 0;
  z-index: 10;
  display: flex;
  align-items: center;
  justify-content: center;
  background: rgba(0, 0, 0, 0.45);
}

.sheet {
  width: 600rpx;
  padding: 44rpx 40rpx 36rpx;
  border-radius: 24rpx;
  background: #fff;
}

.title {
  margin-bottom: 24rpx;
  font-size: 34rpx;
  font-weight: 600;
  text-align: center;
}

.body {
  font-size: 26rpx;
  line-height: 44rpx;
  color: #5a534c;
}

.more {
  margin-top: 16rpx;
}

.buttons {
  display: flex;
  gap: 24rpx;
  margin-top: 36rpx;
}

.btn {
  flex: 1;
  height: 84rpx;
  margin: 0;
  padding: 0;
  border-radius: 42rpx;
  font-size: 30rpx;
  line-height: 84rpx;
  color: #fff;
  background: var(--brand);

  &::after {
    border: none;
  }

  &.plain {
    color: #5a534c;
    background: #f2eee8;
  }

  &.pressed {
    opacity: 0.75;
  }
}
</style>
