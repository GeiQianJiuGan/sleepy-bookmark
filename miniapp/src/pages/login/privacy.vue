<script setup lang="ts">
import { ref } from 'vue'
import { onShow } from '@dcloudio/uni-app'
import { hasSession, logout } from '@/api/http'
import { PRIVACY_VERSION, privacyAgreed, setPrivacyAgreed } from '@/utils/privacy'

const agreed = ref(false)
onShow(() => {
  agreed.value = privacyAgreed()
})

function withdraw() {
  uni.showModal({
    title: '撤回同意',
    content: hasSession() ? '撤回后将退出登录，重新同意后才能继续使用。' : '撤回后需要重新同意才能登录。',
    confirmText: '撤回',
    cancelText: '取消',
    success: (res) => {
      if (!res.confirm) return
      setPrivacyAgreed(false)
      logout()
    },
  })
}
</script>

<template>
  <view class="doc">
    <view class="h1">用户协议与隐私政策</view>
    <view class="muted">更新日期：{{ PRIVACY_VERSION }}</view>

    <view class="h2">一、服务说明</view>
    <view class="p">本小程序是个人自用的小说 / 漫画阅读工具，不提供广告，不对外运营。内容来自管理员配置的共享书源，本小程序不存储、不提供书源站点以外的内容。</view>

    <view class="h2">二、我们收集的信息</view>
    <view class="p">1. 微信登录凭证：登录时通过 wx.login 取得一次性 code，服务器用它向微信换取你的 openid，用于识别账号。管理员可在账号列表查看你的 OpenID。不获取昵称、头像、手机号、位置。</view>
    <view class="p">2. 使用记录：你加入书架的作品、浏览历史和阅读进度，保存在本小程序的服务器上，按账号保存，用于多设备同步。</view>
    <view class="p">3. 剪贴板：只有在「书源管理」中点击「从剪贴板粘贴」时，才会读取一次剪贴板内容用于导入书源。</view>

    <view class="h2">三、保存在本机的信息</view>
    <view class="p">登录凭证（token）、阅读设置、阅读缓存和离线下载保存在本机，可在「下载与缓存」中清除；退出登录会清除登录凭证。</view>

    <view class="h2">四、信息的使用与共享</view>
    <view class="p">以上信息只用于提供阅读、书架和同步功能，不用于广告或统计，不向任何第三方出售或提供。</view>
    <view class="p">服务器抓取书源内容时不会携带你的账号信息。漫画图片线路选择「源站直连」时，你的设备会直接向书源站点请求图片，对方可以看到你的网络地址；选择「只走服务器」可避免。</view>

    <view class="h2">五、你的权利</view>
    <view class="p">你可以随时把作品移出书架、在「浏览历史」中删除记录、在「我的」中退出登录，或在本页撤回同意。撤回同意后需要重新同意才能继续使用。</view>

    <view v-if="agreed" class="withdraw">
      <wd-button plain block @click="withdraw">撤回同意</wd-button>
    </view>
  </view>
</template>

<style lang="scss" scoped>
.doc {
  padding: 32rpx 40rpx 80rpx;
  line-height: 48rpx;
}

.h1 {
  font-size: 40rpx;
  font-weight: 600;
  line-height: 64rpx;
}

.h2 {
  margin: 40rpx 0 12rpx;
  font-size: 30rpx;
  font-weight: 600;
}

.p {
  margin-bottom: 12rpx;
  color: #5a534c;
}

.withdraw {
  margin-top: 64rpx;
}
</style>
