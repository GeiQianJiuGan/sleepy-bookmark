<script setup lang="ts">
import { computed, ref } from 'vue'
import { onShow } from '@dcloudio/uni-app'
import { api, type Task } from '@/api'
import {
  activeDownload, chapterStatus, downloadStats, formatBytes, managerError, networkAvailable,
  offlineBooks, offlineSupported, removeOfflineBook, resumeDownloads, retryDownloads,
  setDownloadsPaused, type OfflineBook,
} from '@/utils/offline'
import { toastError } from '@/api/http'

import { clearReadCache, READ_CACHE_LIMIT, readCacheBytes, readCacheCount } from '@/utils/readCache'
import { clearReadPositions, readPositionCount } from '@/utils/readPosition'

const STATUS: Record<Task['status'], string> = { queued: '排队中', running: '进行中', done: '已完成', failed: '失败' }

const books = computed(() => offlineBooks().map(b => ({ b, stats: downloadStats(b), active: activeDownload(b.book.id) })))
const tasks = ref<Task[]>([])
const total = computed(() => books.value.reduce((sum, item) => sum + item.stats.bytes, 0))
const chapterCount = computed(() => books.value.reduce((sum, item) => sum + item.stats.done, 0))
const expanded = ref(0)
const shown = ref(100)
const rows = computed(() => {
  const item = books.value.find(item => item.b.book.id === expanded.value)
  return item ? Object.values(item.b.chapters).sort((a, b) => a.idx - b.idx).slice(0, shown.value) : []
})

function toggle(id: number) {
  expanded.value = expanded.value === id ? 0 : id
  shown.value = 100
}

function change(action: () => void) {
  try { action() } catch (e) { toastError(e) }
}

function pause(b: OfflineBook) {
  change(() => setDownloadsPaused(b, !b.pauseReason))
}

function retry(b: OfflineBook, id?: number) {
  change(() => retryDownloads(b, id))
}

// The reading cache looks after itself; the page only shows how full it is.
const cache = ref({ bytes: 0, count: 0 })
const posCount = ref(0)

function clearCache() {
  clearReadCache()
  cache.value = { bytes: 0, count: 0 }
}

function clearPositions() {
  clearReadPositions()
  posCount.value = 0
  uni.showToast({ title: '已清空本地阅读位置记录', icon: 'none' })
}

async function load() {
  cache.value = { bytes: readCacheBytes(), count: readCacheCount() }
  posCount.value = readPositionCount()
  try {
    tasks.value = await api.tasks()
  } catch {
    // Offline: the device list above still works.
    tasks.value = []
  }
}

onShow(load)

function open(b: OfflineBook, chapterId = 0) {
  uni.navigateTo({ url: `/pages/reader/reader?offline=1&bookId=${b.book.id}&chapterId=${chapterId}` })
}

function remove(b: OfflineBook) {
  uni.showModal({
    title: `删除《${b.book.title}》的离线内容？`,
    confirmText: '删除',
    cancelText: '取消',
    success: ({ confirm }) => {
      if (!confirm) return
      change(() => removeOfflineBook(b.book.id))
    },
  })
}
</script>

<template>
  <view>
    <view class="section">
      <text>离线文章</text>
      <text class="muted">已离线 {{ chapterCount }} 章 · {{ formatBytes(total) }}</text>
    </view>
    <view v-if="!networkAvailable" class="hint muted">等待网络连接后继续下载</view>
    <view v-if="managerError" class="hint">{{ managerError }}<text class="link" @click="resumeDownloads">继续下载</text></view>
    <view v-for="item in books" :key="item.b.book.id" class="book">
      <view class="item">
        <view class="main" @click="toggle(item.b.book.id)">
          <view class="ellipsis">{{ item.b.book.title }}</view>
          <view class="muted">{{ item.b.book.sourceName }} · 已离线 {{ item.stats.done }}/{{ item.stats.total }} 章</view>
          <progress v-if="item.stats.done < item.stats.total" :percent="Math.floor(100 * item.stats.done / item.stats.total)" activeColor="#c0392b" stroke-width="3" class="progress" />
          <view v-if="item.active" class="current">{{ item.b.chapters[item.active.chapterId]?.title }} · {{ item.active.stage }}</view>
          <view v-if="item.b.pauseReason" class="muted">{{ item.b.pauseReason }}</view>
          <view v-if="item.stats.failed" class="muted">失败 {{ item.stats.failed }} 章</view>
        </view>
        <text class="link" @click="toggle(item.b.book.id)">{{ expanded === item.b.book.id ? '收起' : '章节' }}</text>
      </view>
      <view class="controls">
        <text v-if="item.stats.done" @click="open(item.b)">离线阅读</text>
        <text v-if="item.b.pending.size" @click="pause(item.b)">{{ item.b.pauseReason ? '继续下载' : '暂停下载' }}</text>
        <text v-if="item.stats.failed" @click="retry(item.b)">重试失败章节</text>
        <text @click="remove(item.b)">删除</text>
      </view>
      <view v-if="expanded === item.b.book.id">
        <view v-for="c in rows" :key="c.id" class="item">
          <view class="main">
            <view class="ellipsis">{{ c.title }}</view>
            <view class="muted">{{ chapterStatus(item.b, c) }}<text v-if="c.error"> · {{ c.error }}</text></view>
          </view>
          <text v-if="c.bytes !== undefined" class="link" @click="open(item.b, c.id)">阅读</text>
          <text v-if="c.error" class="link" @click="retry(item.b, c.id)">重试</text>
        </view>
        <view v-if="rows.length < item.stats.total" class="hint link" @click="shown += 100">显示更多章节</view>
      </view>
    </view>
    <view v-if="!books.length" class="hint muted">
      {{ offlineSupported ? '在书籍目录或阅读器中选择要离线的章节' : '离线下载仅在微信小程序中可用' }}
    </view>

    <view class="section">
      <text>阅读缓存</text>
      <text class="muted">已用 {{ formatBytes(cache.bytes) }} / {{ formatBytes(READ_CACHE_LIMIT) }}</text>
    </view>
    <view class="item">
      <view class="main">
        <view>{{ cache.count }} 章小说</view>
        <view class="muted">读过的和后面几章会自动缓存，满了先清最久没读的</view>
      </view>
      <text v-if="cache.count" class="link" @click="clearCache">清空</text>
    </view>
    <view class="item">
      <view class="main">
        <view>{{ posCount }} 本阅读位置记录</view>
        <view class="muted">退出阅读时记录所在画面位置，再次进入自动恢复；空间不足时自动淘汰久未看的记录</view>
      </view>
      <text v-if="posCount" class="link" @click="clearPositions">清空</text>
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
    <view v-if="!tasks.length" class="hint muted">在书籍详情页点「服务器缓存」，服务器会提前抓好全本；加入书架的漫画会自动保存</view>
  </view>
</template>

<style lang="scss" scoped>
.book { margin-bottom: 16rpx; background: #fff; }
.progress { margin: 12rpx 0; }
.current { margin-top: 8rpx; color: var(--brand); font-size: 24rpx; }
.controls { display: flex; flex-wrap: wrap; gap: 32rpx; padding: 18rpx 32rpx; color: var(--brand); font-size: 26rpx; }
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
  border-bottom: 1rpx solid var(--line);

  .main {
    flex: 1;
    min-width: 0;
  }
}

.hint {
  padding: 24rpx 32rpx;
}
</style>
