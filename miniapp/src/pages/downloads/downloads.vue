<script setup lang="ts">
import { computed, ref } from 'vue'
import { onShow } from '@dcloudio/uni-app'
import { api, type Task } from '@/api'
import { formatBytes, offlineBooks, offlineBytes, offlineSupported, removeOfflineBook, type OfflineBook } from '@/utils/offline'

const STATUS: Record<Task['status'], string> = { queued: '排队中', running: '进行中', done: '已完成', failed: '失败' }

const books = ref<OfflineBook[]>([])
const tasks = ref<Task[]>([])
const total = computed(() => books.value.reduce((sum, b) => sum + offlineBytes(b), 0))

async function load() {
  books.value = offlineBooks()
  try {
    tasks.value = await api.tasks()
  } catch {
    // Offline: the device list above still works.
    tasks.value = []
  }
}

onShow(load)

const count = (b: OfflineBook) => Object.keys(b.chapters).length
const open = (b: OfflineBook) => uni.navigateTo({ url: `/pages/reader/reader?bookId=${b.book.id}` })

function remove(b: OfflineBook) {
  uni.showModal({
    title: `删除《${b.book.title}》的离线内容？`,
    success: ({ confirm }) => {
      if (!confirm) return
      removeOfflineBook(b.book.id)
      load()
    },
  })
}
</script>

<template>
  <view>
    <view class="section">
      <text>本机离线下载</text>
      <text class="muted">已用 {{ formatBytes(total) }} / 约 200 MB</text>
    </view>
    <view v-for="b in books" :key="b.book.id" class="item">
      <view class="main" @click="open(b)">
        <view class="ellipsis">{{ b.book.title }}</view>
        <view class="muted">{{ count(b) }} 章 · {{ formatBytes(offlineBytes(b)) }}</view>
      </view>
      <text class="link" @click="remove(b)">删除</text>
    </view>
    <view v-if="!books.length" class="hint muted">
      {{ offlineSupported ? '在书籍详情页点「离线下载」，没有网络时也能阅读' : '离线下载仅在微信小程序中可用' }}
    </view>

    <view class="section">
      <text>服务器缓存任务</text>
      <text class="link" @click="load">刷新</text>
    </view>
    <view v-for="t in tasks" :key="t.id" class="item">
      <view class="main">
        <view class="ellipsis">{{ t.title }}</view>
        <view class="muted">
          {{ STATUS[t.status] }} · {{ t.done }}/{{ t.total }}<text v-if="t.failed"> · 失败 {{ t.failed }}</text>
        </view>
        <view v-if="t.error" class="muted ellipsis">{{ t.error }}</view>
      </view>
    </view>
    <view v-if="!tasks.length" class="hint muted">在书籍详情页点「服务器缓存」，服务器会提前抓好全本</view>
  </view>
</template>

<style lang="scss" scoped>
.section {
  display: flex;
  justify-content: space-between;
  align-items: center;
  padding: 32rpx 32rpx 16rpx;
  font-weight: 600;

  .muted,
  .link {
    font-weight: 400;
  }
}

.link {
  flex: none;
  margin-left: 24rpx;
  color: #c0392b;
}

.item {
  display: flex;
  align-items: center;
  padding: 24rpx 32rpx;
  background: #fff;
  border-bottom: 1rpx solid #f0f0f0;

  .main {
    flex: 1;
    min-width: 0;
  }
}

.hint {
  padding: 24rpx 32rpx;
}
</style>
