<script setup lang="ts">
import { ref } from 'vue'
import { onShow } from '@dcloudio/uni-app'
import { api, type Source, type Subscription, type SyncResult } from '@/api'
import { toastError } from '@/api/http'

const sources = ref<Source[]>([])
const subscriptions = ref<Subscription[]>([])
const loading = ref(true)
/** The subscription being synced, -1 while a new one is being added. */
const syncing = ref(0)
const adding = ref(false)
const url = ref('')

async function load() {
  try {
    ;[sources.value, subscriptions.value] = await Promise.all([api.sources(), api.subscriptions()])
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
const importJSON = () => uni.navigateTo({ url: '/pages/sources/edit?json=1' })

function add() {
  uni.showActionSheet({
    itemList: ['填名称和地址（自动识别）', '导入 JSON 规则', '订阅书源列表（网址）'],
    success: ({ tapIndex }) => {
      if (tapIndex === 0) edit()
      else if (tapIndex === 1) importJSON()
      else adding.value = true
    },
  })
}

function told(r: SyncResult) {
  const parts = [`新增 ${r.added} 个`, `更新 ${r.updated} 个`]
  if (!r.skipped.length) return uni.showToast({ title: parts.join('，'), icon: 'none' })
  // What was left out is said in full: a toast would cut it short.
  uni.showModal({
    title: parts.join('，'),
    content: `跳过 ${r.skipped.length} 个不是本应用格式的书源：` + r.skipped.slice(0, 5).map((s) => `${s.name}（${s.error}）`).join('；'),
    showCancel: false,
  })
}

async function sync(id: number, action: () => Promise<SyncResult>) {
  if (syncing.value) return
  syncing.value = id
  try {
    told(await action())
    adding.value = false
    url.value = ''
  } catch (e) {
    toastError(e)
  } finally {
    syncing.value = 0
    load()
  }
}

function subscribe() {
  const u = url.value.trim()
  if (!u) return uni.showToast({ title: '请填写书源列表的地址', icon: 'none' })
  sync(-1, () => api.subscribe(u))
}

function paste() {
  uni.getClipboardData({ success: (res) => (url.value = res.data.trim()) })
}

function manage(sub: Subscription) {
  uni.showActionSheet({
    itemList: ['立即同步', '取消订阅，保留书源', '取消订阅，并删除它的书源'],
    success: ({ tapIndex }) => {
      if (tapIndex === 0) return sync(sub.id, () => api.syncSubscription(sub.id))
      const withSources = tapIndex === 2
      uni.showModal({
        title: '取消订阅？',
        content: withSources ? `「${sub.name}」导入的 ${sub.sources} 个书源，连同来自它们的书架、历史和缓存都会删除` : '已导入的书源会保留，只是不再同步',
        confirmText: withSources ? '删除' : '取消订阅',
        cancelText: '返回',
        success: async ({ confirm }) => {
          if (!confirm) return
          try {
            await api.deleteSubscription(sub.id, withSources)
          } catch (e) {
            toastError(e)
          }
          load()
        },
      })
    },
  })
}

function when(seconds: number) {
  if (!seconds) return '还没有同步过'
  const d = new Date(seconds * 1000)
  const two = (n: number) => String(n).padStart(2, '0')
  return `${d.getMonth() + 1}-${two(d.getDate())} ${two(d.getHours())}:${two(d.getMinutes())} 同步`
}

const from = (s: Source) => subscriptions.value.find((sub) => sub.id === s.subscriptionId)?.name
</script>

<template>
  <view class="list">
    <template v-if="subscriptions.length || adding">
      <view class="heading muted">订阅</view>
      <view v-for="sub in subscriptions" :key="sub.id" class="source" @click="manage(sub)">
        <view class="main">
          <view class="name">{{ sub.name }}</view>
          <view class="muted ellipsis">{{ sub.url }}</view>
          <view v-if="sub.error" class="health">同步失败：{{ sub.error }}</view>
          <view v-else class="muted">{{ sub.sources }} 个书源 · {{ when(sub.syncedAt) }}</view>
        </view>
        <wd-button size="small" plain :loading="syncing === sub.id" @click.stop="sync(sub.id, () => api.syncSubscription(sub.id))">
          同步
        </wd-button>
      </view>
      <view v-if="adding" class="subscribe">
        <view class="row">
          <input v-model="url" class="url" placeholder="书源列表的地址（JSON 文件）" />
          <text class="link" @click="paste">粘贴</text>
        </view>
        <view class="muted tip">
          可以是 GitHub 上 JSON 文件的页面地址或 Raw 地址。列表里是本应用格式的书源（一个或一组）；之后点「同步」可按列表更新。
        </view>
        <view class="row buttons">
          <wd-button size="small" plain @click="adding = false">取消</wd-button>
          <wd-button size="small" :loading="syncing === -1" @click="subscribe">订阅并导入</wd-button>
        </view>
      </view>
      <view class="heading muted">书源</view>
    </template>

    <view v-for="s in sources" :key="s.id" class="source">
      <view class="main" @click="edit(s.id)">
        <view class="name">
          <text class="tag" :class="s.type">{{ s.type === 'novel' ? '小说' : '漫画' }}</text>
          {{ s.name }}
          <text v-if="s.auto" class="auto">自动识别</text>
        </view>
        <view class="muted ellipsis">{{ s.rule.baseUrl }}{{ from(s) ? ' · 来自订阅「' + from(s) + '」' : '' }}</view>
        <view v-if="s.health" class="health ellipsis">
          {{ s.health.pausedUntil * 1000 > Date.now() ? '搜索连续失败，暂时跳过' : '最近一次搜索失败' }}：{{ s.health.lastError }}
        </view>
      </view>
      <wd-switch :model-value="s.enabled" size="20px" @change="toggle(s, $event.value)" />
    </view>
    <view v-if="!loading && !sources.length" class="empty">还没有书源，点下方按钮新增：填一个名称和网站地址就可以</view>
    <view class="footer">
      <wd-button block @click="add">新增书源</wd-button>
    </view>
  </view>
</template>

<style lang="scss" scoped>
.heading {
  padding: 24rpx 32rpx 12rpx;
}

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

.auto {
  margin-left: 12rpx;
  padding: 2rpx 12rpx;
  border: 1rpx solid var(--line);
  border-radius: 6rpx;
  font-size: 20rpx;
  color: #8a8378;
}

.health {
  margin-top: 6rpx;
  font-size: 22rpx;
  color: #b9770e;
}

.subscribe {
  padding: 24rpx 32rpx;
  background: #fff;
  border-bottom: 1rpx solid var(--line);

  .row {
    display: flex;
    align-items: center;
    gap: 20rpx;
  }

  .url {
    flex: 1;
    height: 72rpx;
    padding: 0 20rpx;
    border-radius: 8rpx;
    background: var(--paper);
    font-size: 26rpx;
  }

  .link {
    flex: none;
    font-size: 26rpx;
    color: #c0392b;
  }

  .tip {
    margin: 16rpx 0;
    line-height: 1.6;
  }

  .buttons {
    justify-content: flex-end;
  }
}

.list {
  // Room for the fixed footer.
  padding-bottom: calc(160rpx + env(safe-area-inset-bottom));
}

.footer {
  position: fixed;
  right: 0;
  bottom: 0;
  left: 0;
  z-index: 10;
  padding: 20rpx 32rpx calc(20rpx + env(safe-area-inset-bottom));
  border-top: 1rpx solid var(--line);
  background: var(--paper);
}
</style>
