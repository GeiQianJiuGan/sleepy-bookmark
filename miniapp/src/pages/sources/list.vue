<script setup lang="ts">
import { computed, ref } from 'vue'
import { onShow } from '@dcloudio/uni-app'
import {
  api,
  type CheckReport,
  type MonitorSummary,
  type Source,
  type SourceMetrics,
  type Subscription,
  type SyncResult,
} from '@/api'
import { account, accountError, isAdmin, refreshAccount, toastError } from '@/api/http'

type FilterKey = 'all' | 'comic' | 'novel' | 'disabled'

const sources = ref<Source[]>([])
const subscriptions = ref<Subscription[]>([])
const summary = ref<MonitorSummary | null>(null)
const loading = ref(true)
/** The subscription being synced, -1 while a new one is being added, -2 while syncing all. */
const syncing = ref(0)
const checkingAll = ref(false)
const checkingId = ref(0)
const adding = ref(false)
const url = ref('')
const filter = ref<FilterKey>('all')

function computeFallbackSummary(list: Source[]): MonitorSummary {
  let enabledSources = 0
  let disabledSources = 0
  let autoDisabledSources = 0
  let monitoredSources = 0
  let healthySources = 0
  let degradedSources = 0
  let unhealthySources = 0
  let totalRequests = 0
  let validRequests = 0
  let invalidRequests = 0
  let latencySum = 0

  for (const s of list) {
    if (s.enabled) enabledSources++
    else {
      disabledSources++
      if (s.autoDisabled) autoDisabledSources++
    }
    const m = s.metrics
    if (m && m.total > 0) {
      monitoredSources++
      totalRequests += m.total
      validRequests += m.valid
      invalidRequests += m.invalid
      latencySum += m.avgLatencyMs * m.total
      if (m.validRate >= 0.8) healthySources++
      else if (m.validRate >= 0.5) degradedSources++
      else unhealthySources++
    }
  }
  const validRate = totalRequests > 0 ? Math.round((validRequests / totalRequests) * 10000) / 10000 : 0
  const invalidRate = totalRequests > 0 ? Math.round((invalidRequests / totalRequests) * 10000) / 10000 : 0
  const avgLatencyMs = totalRequests > 0 ? Math.round(latencySum / totalRequests) : 0
  return {
    totalSources: list.length,
    enabledSources,
    disabledSources,
    autoDisabledSources,
    monitoredSources,
    healthySources,
    degradedSources,
    unhealthySources,
    totalRequests,
    validRequests,
    invalidRequests,
    validRate,
    invalidRate,
    avgLatencyMs,
  }
}

async function load() {
  try {
    const [list, subs, report] = await Promise.all([
      api.sources(),
      api.subscriptions(),
      api.sourceMetrics().catch(() => null),
    ])
    sources.value = list || []
    subscriptions.value = subs || []
    summary.value = report?.summary ?? computeFallbackSummary(sources.value)
  } catch (e) {
    toastError(e)
  } finally {
    loading.value = false
  }
}

function checkRole() {
  refreshAccount().catch(() => {})
}
onShow(() => {
  checkRole()
  load()
})

const counts = computed(() => {
  let comic = 0
  let novel = 0
  let disabled = 0
  let enabled = 0
  let autoDisabled = 0
  for (const s of sources.value) {
    if (s.type === 'comic') comic++
    else novel++
    if (s.enabled) enabled++
    else {
      disabled++
      if (s.autoDisabled) autoDisabled++
    }
  }
  return { all: sources.value.length, comic, novel, disabled, enabled, autoDisabled }
})

const filteredSources = computed(() => {
  switch (filter.value) {
    case 'comic':
      return sources.value.filter((s) => s.type === 'comic')
    case 'novel':
      return sources.value.filter((s) => s.type === 'novel')
    case 'disabled':
      return sources.value.filter((s) => !s.enabled)
    default:
      return sources.value
  }
})

const hasMetrics = computed(() => Boolean(summary.value && summary.value.totalRequests > 0))

async function toggle(s: Source, enabled: boolean) {
  try {
    await api.setSourceEnabled(s.id, enabled)
    s.enabled = enabled
    s.autoDisabled = false
    summary.value = computeFallbackSummary(sources.value)
  } catch (e) {
    toastError(e)
    load()
  }
}

const edit = (id?: number) => isAdmin.value && uni.navigateTo({ url: '/pages/sources/edit' + (id ? `?id=${id}` : '') })
const importJSON = () => uni.navigateTo({ url: '/pages/sources/edit?json=1' })

function add() {
  uni.showActionSheet({
    itemList: ['填名称和地址（自动识别）', '导入 JSON 规则（支持源包/阅读格式）', '订阅书源列表（网址）', '导出全部书源 JSON'],
    success: ({ tapIndex }) => {
      if (tapIndex === 0) edit()
      else if (tapIndex === 1) importJSON()
      else if (tapIndex === 2) adding.value = true
      else exportAll()
    },
  })
}

async function exportAll() {
  try {
    const rules = await api.exportSources()
    if (!rules.length) return uni.showToast({ title: '暂无书源可导出', icon: 'none' })
    const json = JSON.stringify(rules, null, 2)
    uni.setClipboardData({
      data: json,
      success: () => uni.showToast({ title: `已复制 ${rules.length} 个书源 JSON`, icon: 'none' }),
    })
  } catch (e) {
    toastError(e)
  }
}

function summarizeBatchCheck(r: CheckReport) {
  const disabled = r.autoDisabled || []
  const enabled = r.autoEnabled || []
  if (!disabled.length && !enabled.length) {
    uni.showToast({ title: '分析完成，各书源状态无变动', icon: 'none' })
    return
  }
  const lines: string[] = []
  if (enabled.length) {
    lines.push(`已恢复自动开启（${enabled.length}）：${enabled.slice(0, 6).join('、')}${enabled.length > 6 ? ' 等' : ''}`)
  }
  if (disabled.length) {
    lines.push(`准确率低于 50% 已停用（${disabled.length}）：${disabled.slice(0, 6).join('、')}${disabled.length > 6 ? ' 等' : ''}`)
  }
  uni.showModal({
    title: '书源分析完成',
    content: lines.join('\n'),
    showCancel: false,
  })
}

async function checkAll() {
  if (checkingAll.value || checkingId.value || !sources.value.length) return
  checkingAll.value = true
  try {
    const r = await api.checkSources()
    await load()
    summarizeBatchCheck(r)
  } catch (e) {
    toastError(e)
  } finally {
    checkingAll.value = false
  }
}

async function checkOne(s: Source) {
  if (checkingAll.value || checkingId.value) return
  checkingId.value = s.id
  try {
    const m: SourceMetrics = await api.checkSource(s.id)
    s.metrics = m
    if (typeof m.enabled === 'boolean') s.enabled = m.enabled
    s.autoDisabled = Boolean(m.autoDisabled)
    summary.value = computeFallbackSummary(sources.value)
    if (m.autoAction === 'enabled') {
      uni.showToast({ title: `「${s.name}」准确率恢复，已自动开启`, icon: 'none', duration: 2400 })
    } else if (m.autoAction === 'disabled') {
      uni.showToast({ title: `「${s.name}」准确率低于 50%，已自动停用`, icon: 'none', duration: 2400 })
    } else {
      uni.showToast({
        title: `「${s.name}」准确率 ${pct(m.validRate)} · ${m.lastLatencyMs}ms`,
        icon: 'none',
        duration: 2200,
      })
    }
  } catch (e) {
    toastError(e)
  } finally {
    checkingId.value = 0
  }
}

async function syncAll() {
  if (syncing.value || !subscriptions.value.length) return
  syncing.value = -2
  try {
    const r = await api.syncAllSubscriptions()
    const parts = [`同步 ${r.synced} 个`, `新增 ${r.added} 个`, `更新 ${r.updated} 个`]
    if (r.failed) parts.push(`失败 ${r.failed} 个`)
    uni.showToast({ title: parts.join('，'), icon: 'none' })
  } catch (e) {
    toastError(e)
  } finally {
    syncing.value = 0
    load()
  }
}

function pct(rate: number) {
  return `${Math.round((rate || 0) * 100)}%`
}

function rateClass(rate: number) {
  if (rate >= 0.8) return 'good'
  if (rate >= 0.5) return 'warn'
  return 'bad'
}

function barWidth(rate: number) {
  const n = Math.max(0, Math.min(100, Math.round((rate || 0) * 100)))
  return `${n}%`
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
  <view class="page">
    <view v-if="accountError" class="dash-empty" @click="checkRole">账号权限加载失败，点击重试</view>
    <view v-else-if="account && !isAdmin" class="dash-empty">所有成员共用书源，只有管理员可以修改书源和订阅。</view>
    <!-- Top monitoring & health dashboard -->
    <view v-if="sources.length" class="dashboard">
      <view class="dash-head">
        <view class="dash-title-wrap">
          <view class="dash-title">数据源监听</view>
          <view class="dash-sub">
            启用 {{ counts.enabled }} / {{ counts.all }}
            <text v-if="counts.autoDisabled" class="auto-count"> · 自动停用 {{ counts.autoDisabled }}</text>
          </view>
        </view>
        <view class="dash-actions">
          <wd-button v-if="isAdmin" size="small" :loading="checkingAll" @click="checkAll">
            {{ checkingAll ? '分析中' : '分析全部' }}
          </wd-button>
          <wd-button size="small" plain @click="exportAll">导出</wd-button>
        </view>
      </view>

      <view v-if="hasMetrics && summary" class="stats-grid">
        <view class="stat-box">
          <view class="stat-val">{{ summary.avgLatencyMs }}<text class="unit">ms</text></view>
          <view class="stat-lbl">平均响应</view>
        </view>
        <view class="stat-box">
          <view class="stat-val" :class="rateClass(summary.validRate)">{{ pct(summary.validRate) }}</view>
          <view class="stat-lbl">有效准确率</view>
        </view>
        <view class="stat-box">
          <view class="stat-val bad">{{ pct(summary.invalidRate) }}</view>
          <view class="stat-lbl">无效占比</view>
        </view>
        <view class="stat-box">
          <view class="stat-val">{{ summary.validRequests }}/{{ summary.totalRequests }}</view>
          <view class="stat-lbl">有效/总请求</view>
        </view>
      </view>
      <view v-else class="dash-empty">
        暂无采样记录，使用书源搜索或阅读后可查看统计
      </view>

      <view v-if="isAdmin" class="dash-rule">
        准确率低于 50% 自动停用；点击「分析」若恢复达标将自动重新开启
      </view>
    </view>

    <!-- Subscriptions section -->
    <view v-if="subscriptions.length || (isAdmin && adding)" class="section">
      <view class="section-head">
        <view class="section-title">书源订阅（{{ subscriptions.length }}）</view>
        <view v-if="isAdmin" class="section-tools">
          <text v-if="subscriptions.length > 1" class="text-btn" @click="syncAll">
            {{ syncing === -2 ? '同步中…' : '全部同步' }}
          </text>
          <text v-if="!adding" class="text-btn" @click="adding = true">+ 添加订阅</text>
        </view>
      </view>

      <view v-for="sub in subscriptions" :key="sub.id" class="sub-card" @click="isAdmin && manage(sub)">
        <view class="sub-main">
          <view class="sub-name ellipsis">{{ sub.name }}</view>
          <view class="sub-url ellipsis">{{ sub.url }}</view>
          <view v-if="sub.error" class="sub-err ellipsis">同步失败：{{ sub.error }}</view>
          <view v-else class="sub-meta">{{ sub.sources }} 个书源 · {{ when(sub.syncedAt) }}</view>
        </view>
        <wd-button
          v-if="isAdmin"
          size="small"
          plain
          :loading="syncing === sub.id"
          @click.stop="sync(sub.id, () => api.syncSubscription(sub.id))"
        >
          同步
        </wd-button>
      </view>

      <view v-if="isAdmin && adding" class="sub-form">
        <view class="row">
          <input v-model="url" class="url-input" placeholder="书源列表 JSON 订阅地址" />
          <text class="text-btn" @click="paste">粘贴</text>
        </view>
        <view class="form-tip">
          支持 GitHub / Gist / Gitee / GitLab 页面或 Raw 地址，支持本应用规则、源包及声明式阅读（Legado）JSON（最长等待 2 分钟）。
        </view>
        <view class="row form-btns">
          <wd-button size="small" plain @click="adding = false">取消</wd-button>
          <wd-button size="small" :loading="syncing === -1" @click="subscribe">订阅并导入</wd-button>
        </view>
      </view>
    </view>

    <!-- Filter bar for sources -->
    <view v-if="sources.length" class="filters">
      <view class="chip" :class="{ active: filter === 'all' }" @click="filter = 'all'">全部 {{ counts.all }}</view>
      <view class="chip" :class="{ active: filter === 'comic' }" @click="filter = 'comic'">漫画 {{ counts.comic }}</view>
      <view class="chip" :class="{ active: filter === 'novel' }" @click="filter = 'novel'">小说 {{ counts.novel }}</view>
      <view class="chip" :class="{ active: filter === 'disabled' }" @click="filter = 'disabled'">
        已停用 {{ counts.disabled }}
      </view>
    </view>

    <!-- Source cards -->
    <view v-if="filteredSources.length" class="source-list">
      <view
        v-for="s in filteredSources"
        :key="s.id"
        class="source-card"
        :class="{ disabled: !s.enabled }"
      >
        <view class="card-top">
          <view class="card-info" @click="edit(s.id)">
            <view class="title-line">
              <view class="tag" :class="s.type">{{ s.type === 'novel' ? '小说' : '漫画' }}</view>
              <view class="name ellipsis">{{ s.name }}</view>
              <view v-if="s.auto" class="pill muted-pill">自动</view>
              <view v-if="!s.enabled && s.autoDisabled" class="pill danger-pill">低准确率停用</view>
              <view v-else-if="!s.enabled" class="pill muted-pill">已停用</view>
            </view>
            <view class="url-line ellipsis">
              {{ s.rule.baseUrl }}{{ from(s) ? ' · ' + from(s) : '' }}
            </view>
          </view>

          <view v-if="isAdmin" class="card-controls">
            <view
              class="analyze-btn"
              :class="{ busy: checkingId === s.id }"
              @click.stop="checkOne(s)"
            >
              {{ checkingId === s.id ? '分析中' : '分析' }}
            </view>
            <wd-switch :model-value="s.enabled" size="19px" @change="toggle(s, $event.value)" />
          </view>
        </view>

        <!-- Metrics strip -->
        <view v-if="s.metrics && s.metrics.total > 0" class="metrics-strip" @click="edit(s.id)">
          <view class="bar-track">
            <view
              class="bar-fill"
              :class="rateClass(s.metrics.validRate)"
              :style="{ width: barWidth(s.metrics.validRate) }"
            />
          </view>
          <view class="metrics-meta">
            <view class="meta-left">
              <text class="rate-val" :class="rateClass(s.metrics.validRate)">
                准确率 {{ pct(s.metrics.validRate) }}
              </text>
              <text class="meta-sep">({{ s.metrics.valid }}/{{ s.metrics.total }})</text>
            </view>
            <view class="meta-right">
              均 {{ s.metrics.avgLatencyMs }}ms · 近 {{ s.metrics.lastLatencyMs }}ms
            </view>
          </view>
        </view>

        <!-- Health / last error hint -->
        <view v-if="s.health" class="err-hint ellipsis" @click="edit(s.id)">
          {{ s.health.pausedUntil * 1000 > Date.now() ? '连续失败，暂时跳过' : '最近搜索失败' }}：{{ s.health.lastError }}
        </view>
        <view v-else-if="s.metrics?.lastError && s.metrics.validRate < 0.5" class="err-hint ellipsis" @click="edit(s.id)">
          最近异常：{{ s.metrics.lastError }}
        </view>
      </view>
    </view>

    <view v-else-if="!loading && sources.length" class="empty">当前筛选下没有书源</view>
    <view v-else-if="!loading" class="empty">{{ isAdmin ? '还没有书源，可以新增、导入或订阅' : '暂无共享书源' }}</view>

    <view v-if="isAdmin" class="footer">
      <wd-button block @click="add">新增 / 导入 / 订阅书源</wd-button>
    </view>
  </view>
</template>

<style lang="scss" scoped>
.page {
  padding: 20rpx 24rpx calc(164rpx + env(safe-area-inset-bottom));
}

.dashboard {
  padding: 24rpx 26rpx 22rpx;
  border-radius: 20rpx;
  background: #fff;
  box-shadow: 0 4rpx 18rpx rgba(43, 38, 34, 0.05);
}

.dash-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 16rpx;
}

.dash-title-wrap {
  min-width: 0;
}

.dash-title {
  font-size: 30rpx;
  font-weight: 700;
  color: #2b2622;
}

.dash-sub {
  margin-top: 4rpx;
  font-size: 22rpx;
  color: #8a8378;
}

.auto-count {
  color: #c0392b;
  font-weight: 500;
}

.dash-actions {
  flex: none;
  display: flex;
  align-items: center;
  gap: 12rpx;
}

.stats-grid {
  display: grid;
  grid-template-columns: repeat(4, minmax(0, 1fr));
  gap: 12rpx;
  margin-top: 22rpx;
  padding: 18rpx 12rpx;
  border-radius: 14rpx;
  background: #faf8f4;
  text-align: center;
}

.stat-val {
  font-size: 28rpx;
  font-weight: 700;
  color: #2b2622;

  .unit {
    font-size: 20rpx;
    font-weight: 400;
    margin-left: 2rpx;
  }

  &.good {
    color: #239b56;
  }

  &.warn {
    color: #d68910;
  }

  &.bad {
    color: #c0392b;
  }
}

.stat-lbl {
  margin-top: 6rpx;
  font-size: 20rpx;
  color: #8a8378;
}

.dash-empty {
  margin-top: 18rpx;
  padding: 18rpx 20rpx;
  border-radius: 12rpx;
  background: #faf8f4;
  font-size: 23rpx;
  line-height: 1.5;
  color: #8a8378;
}

.dash-rule {
  margin-top: 16rpx;
  font-size: 21rpx;
  line-height: 1.45;
  color: #9a9186;
}

.section {
  margin-top: 24rpx;
}

.section-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  margin-bottom: 12rpx;
  padding: 0 6rpx;
}

.section-title {
  font-size: 26rpx;
  font-weight: 600;
  color: #6b6359;
}

.section-tools {
  display: flex;
  align-items: center;
  gap: 24rpx;
}

.text-btn {
  font-size: 24rpx;
  color: var(--brand);
}

.sub-card {
  display: flex;
  align-items: center;
  gap: 16rpx;
  margin-bottom: 14rpx;
  padding: 20rpx 24rpx;
  border-radius: 16rpx;
  background: #fff;
}

.sub-main {
  flex: 1;
  min-width: 0;
}

.sub-name {
  font-size: 28rpx;
  font-weight: 600;
  color: #2b2622;
}

.sub-url {
  margin-top: 4rpx;
  font-size: 22rpx;
  color: #8a8378;
}

.sub-meta {
  margin-top: 6rpx;
  font-size: 21rpx;
  color: #9a9186;
}

.sub-err {
  margin-top: 6rpx;
  font-size: 21rpx;
  color: #c0392b;
}

.sub-form {
  margin-bottom: 14rpx;
  padding: 22rpx 24rpx;
  border-radius: 16rpx;
  background: #fff;

  .row {
    display: flex;
    align-items: center;
    gap: 16rpx;
  }

  .url-input {
    flex: 1;
    height: 70rpx;
    padding: 0 20rpx;
    border-radius: 10rpx;
    background: var(--paper);
    font-size: 25rpx;
  }

  .form-tip {
    margin: 14rpx 0 16rpx;
    font-size: 22rpx;
    line-height: 1.55;
    color: #8a8378;
  }

  .form-btns {
    justify-content: flex-end;
  }
}

.filters {
  display: flex;
  align-items: center;
  gap: 14rpx;
  margin: 24rpx 0 16rpx;
  overflow-x: auto;
}

.chip {
  flex: none;
  padding: 10rpx 22rpx;
  border-radius: 28rpx;
  background: #ebe6de;
  font-size: 23rpx;
  color: #6f665d;

  &.active {
    background: var(--brand);
    color: #fff;
    font-weight: 500;
  }
}

.source-list {
  display: flex;
  flex-direction: column;
  gap: 16rpx;
}

.source-card {
  padding: 22rpx 24rpx;
  border-radius: 18rpx;
  background: #fff;
  box-shadow: 0 2rpx 10rpx rgba(43, 38, 34, 0.04);

  &.disabled {
    background: #fcfbfa;
    border: 1rpx solid #ece6dc;
    box-shadow: none;

    .name {
      color: #7c756c;
    }
  }
}

.card-top {
  display: flex;
  align-items: center;
  gap: 16rpx;
}

.card-info {
  flex: 1;
  min-width: 0;
}

.title-line {
  display: flex;
  align-items: center;
  gap: 10rpx;
  min-width: 0;
}

.tag {
  flex: none;
  padding: 2rpx 10rpx;
  border-radius: 6rpx;
  font-size: 20rpx;
  font-weight: 500;
  color: #fff;
  background: #2980b9;

  &.comic {
    background: #8e44ad;
  }
}

.name {
  flex: 0 1 auto;
  min-width: 0;
  font-size: 29rpx;
  font-weight: 600;
  color: #2b2622;
}

.pill {
  flex: none;
  padding: 2rpx 10rpx;
  border-radius: 6rpx;
  font-size: 19rpx;
  line-height: 1.4;
}

.muted-pill {
  background: #f1ede6;
  color: #8a8378;
}

.danger-pill {
  background: #fdecea;
  color: #c0392b;
  font-weight: 500;
}

.url-line {
  margin-top: 8rpx;
  font-size: 22rpx;
  color: #9a9186;
}

.card-controls {
  flex: none;
  display: flex;
  align-items: center;
  gap: 14rpx;
}

.analyze-btn {
  padding: 6rpx 18rpx;
  border: 1rpx solid #e2d9cd;
  border-radius: 24rpx;
  background: #faf8f4;
  font-size: 22rpx;
  color: #5c544b;

  &.busy {
    opacity: 0.55;
  }
}

.metrics-strip {
  margin-top: 16rpx;
  padding-top: 14rpx;
  border-top: 1rpx solid #f3eee7;
}

.bar-track {
  height: 8rpx;
  border-radius: 4rpx;
  background: #f1ece4;
  overflow: hidden;
}

.bar-fill {
  height: 100%;
  border-radius: 4rpx;

  &.good {
    background: #27ae60;
  }

  &.warn {
    background: #e09f24;
  }

  &.bad {
    background: #c0392b;
  }
}

.metrics-meta {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12rpx;
  margin-top: 10rpx;
  font-size: 21rpx;
  color: #8a8378;
}

.meta-left {
  display: flex;
  align-items: center;
  gap: 6rpx;
}

.rate-val {
  font-weight: 600;

  &.good {
    color: #239b56;
  }

  &.warn {
    color: #c87f0a;
  }

  &.bad {
    color: #c0392b;
  }
}

.meta-sep {
  color: #9a9186;
}

.meta-right {
  flex: none;
  color: #8a8378;
}

.err-hint {
  margin-top: 10rpx;
  padding: 8rpx 14rpx;
  border-radius: 8rpx;
  background: #fdf6ec;
  font-size: 21rpx;
  color: #b9770e;
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
