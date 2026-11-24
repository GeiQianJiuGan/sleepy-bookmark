<script setup lang="ts">
import { ref } from 'vue'
import { onShow } from '@dcloudio/uni-app'
import { api, type Source } from '@/api'
import { toastError } from '@/api/http'

const sources = ref<Source[]>([])
const loading = ref(true)

async function load() {
  try {
    sources.value = await api.sources()
  } catch (e) {
    toastError(e)
  } finally {
    loading.value = false
  }
}

onShow(load)

async function toggle(s: Source, enabled: boolean) {
  try {
    await api.setSourceEnabled(s.id, enabled)
    s.enabled = enabled
  } catch (e) {
    toastError(e)
    load()
  }
}

const edit = (id?: number) => uni.navigateTo({ url: '/pages/sources/edit' + (id ? `?id=${id}` : '') })
</script>

<template>
  <view>
    <view v-for="s in sources" :key="s.id" class="source">
      <view class="main" @click="edit(s.id)">
        <view class="name">
          <text class="tag" :class="s.type">{{ s.type === 'novel' ? '小说' : '漫画' }}</text>
          {{ s.name }}
        </view>
        <view class="muted ellipsis">{{ s.rule.baseUrl }}</view>
        <view v-if="s.health" class="health ellipsis">
          {{ s.health.pausedUntil * 1000 > Date.now() ? '搜索连续失败，暂时跳过' : '最近一次搜索失败' }}：{{ s.health.lastError }}
        </view>
      </view>
      <wd-switch :model-value="s.enabled" size="20px" @change="toggle(s, $event.value)" />
    </view>
    <view v-if="!loading && !sources.length" class="empty">还没有书源，点下方按钮新增或导入</view>
    <view class="footer">
      <wd-button block @click="edit()">新增 / 导入书源</wd-button>
    </view>
  </view>
</template>

<style lang="scss" scoped>
.source {
  display: flex;
  align-items: center;
  padding: 24rpx 32rpx;
  background: #fff;
  border-bottom: 1rpx solid var(--line);
}

.main {
  flex: 1;
  min-width: 0;
  margin-right: 24rpx;
}

.name {
  margin-bottom: 8rpx;
  font-size: 30rpx;
}

.tag {
  margin-right: 12rpx;
  padding: 2rpx 12rpx;
  border-radius: 6rpx;
  font-size: 22rpx;
  color: #fff;
  background: #2980b9;

  &.comic {
    background: #8e44ad;
  }
}

.health {
  margin-top: 6rpx;
  font-size: 22rpx;
  color: #b9770e;
}

.footer {
  padding: 40rpx 32rpx;
}
</style>
