<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { onLoad } from '@dcloudio/uni-app'
import { api, type Mode, type SourceMetrics, type TestReport } from '@/api'
import { toastError } from '@/api/http'
import { useAdminPage } from '@/utils/adminPage'
import LoadState from '@/components/LoadState.vue'
import { useAppStore } from '@/stores/app'

const { allowed, loading: roleLoading, error: roleError, refresh: refreshRole } = useAdminPage()
const sourceId = ref(0)
/** Simple: a name and an address, everything else recognised. Advanced: the rule as JSON. */
const advanced = ref(false)
const name = ref('')
const type = ref<Mode>(useAppStore().mode)
const baseUrl = ref('')
const text = ref('')
/** The source being edited has rules of its own, which saving in simple mode would drop. */
let handWritten = false
const keyword = ref('')
const report = ref<TestReport | null>(null)
const metrics = ref<SourceMetrics | null>(null)
const enabled = ref(true)
const autoDisabled = ref(false)
const checking = ref(false)
const testing = ref(false)
const busy = ref(false)

// A trial run only speaks for the rule it ran.
watch([type, baseUrl, text], () => (report.value = null))

onLoad((query) => {
  sourceId.value = Number(query?.id) || 0
  if (query?.json) advanced.value = true
})

let initialized = false
watch(allowed, async canEdit => {
  if (!canEdit || initialized || !sourceId.value) return
  try {
    const src = (await api.sources()).find((s) => s.id === sourceId.value)
    if (!src || !allowed.value) return
    initialized = true
    name.value = src.name
    type.value = src.type
    enabled.value = src.enabled
    autoDisabled.value = Boolean(src.autoDisabled)
    baseUrl.value = String(src.rule.baseUrl ?? '')
    text.value = JSON.stringify(src.rule, null, 2)
    metrics.value = src.metrics ?? null
    handWritten = !src.auto
    advanced.value = handWritten
  } catch (e) {
    toastError(e)
  }
})

function simpleRule() {
  let url = baseUrl.value.trim()
  if (!name.value.trim()) throw new Error('请填写书源名称')
  if (!url) throw new Error('请填写书源地址')
  // An address typed without a scheme is taken as https.
  if (!/^https?:\/\//i.test(url)) url = 'https://' + url
  return { name: name.value.trim(), type: type.value, baseUrl: url }
}

function rule(): unknown {
  if (!advanced.value) return simpleRule()
  try {
    return JSON.parse(text.value)
  } catch (e) {
    throw new Error('JSON 格式错误：' + (e as Error).message)
  }
}

/** The rule in the JSON box, when it is one rule and not a list or something half typed. */
function written(): Record<string, unknown> | null {
  try {
    const v = JSON.parse(text.value)
    return v && typeof v === 'object' && !Array.isArray(v) ? v : null
  } catch {
    return null
  }
}

// The two modes edit one source: what was typed in one is there in the other.
function setMode(next: boolean) {
  if (next === advanced.value) return
  report.value = null
  const json = written()
  if (next) {
    // Whatever a rule leaves out is recognised, so a name, a type and an address are a rule already.
    const typed = { name: name.value.trim(), type: type.value, baseUrl: baseUrl.value.trim() }
    if (json) {
      if (typed.name) json.name = typed.name
      if (typed.baseUrl) json.baseUrl = typed.baseUrl
      text.value = JSON.stringify({ ...json, type: typed.type }, null, 2)
    } else if (!text.value.trim()) {
      text.value = JSON.stringify({ ...typed, baseUrl: typed.baseUrl || 'https://' }, null, 2)
    }
  } else if (json) {
    if (typeof json.name === 'string') name.value = json.name
    if (json.type === 'novel' || json.type === 'comic') type.value = json.type
    if (typeof json.baseUrl === 'string') baseUrl.value = json.baseUrl
  }
  advanced.value = next
}

async function run(action: () => Promise<void>) {
  if (busy.value) return
  busy.value = true
  try {
    await action()
  } catch (e) {
    toastError(e)
  } finally {
    busy.value = false
  }
}

async function tryOut(): Promise<TestReport> {
  testing.value = true
  report.value = null
  try {
    report.value = await api.testSource(rule(), keyword.value.trim(), sourceId.value)
    return report.value
  } finally {
    testing.value = false
  }
}

async function checkNow() {
  if (!sourceId.value || checking.value) return
  checking.value = true
  try {
    const m = await api.checkSource(sourceId.value, keyword.value.trim() || undefined)
    metrics.value = m
    if (typeof m.enabled === 'boolean') enabled.value = m.enabled
    autoDisabled.value = Boolean(m.autoDisabled)
    if (m.autoAction === 'enabled') {
      uni.showToast({ title: '准确率已恢复，已自动重新开启', icon: 'none', duration: 2400 })
    } else if (m.autoAction === 'disabled') {
      uni.showToast({ title: '准确率低于 50%，已自动停用', icon: 'none', duration: 2400 })
    } else {
      uni.showToast({ title: '分析完成', icon: 'none' })
    }
  } catch (e) {
    toastError(e)
  } finally {
    checking.value = false
  }
}

async function resetMetrics() {
  if (!sourceId.value) return
  try {
    await api.resetSourceMetrics(sourceId.value)
    metrics.value = null
    uni.showToast({ title: '统计已重置', icon: 'none' })
  } catch (e) {
    toastError(e)
  }
}

const ACTION_LABELS: Record<string, string> = {
  search: '搜索',
  recommend: '推荐',
  detail: '详情',
  toc: '目录',
  content: '正文/图集',
  image: '图片',
}

const pct = (n: number) => `${Math.round((n || 0) * 100)}%`
const rateClass = (n: number) => (n >= 0.8 ? 'good' : n >= 0.5 ? 'warn' : 'bad')

const actionRows = computed(() => {
  if (!metrics.value?.actions) return []
  return Object.entries(metrics.value.actions).map(([k, v]) => ({
    key: k,
    label: ACTION_LABELS[k] || k,
    ...v,
  }))
})

const test = () =>
  run(async () => {
    await tryOut()
  })

const confirm = (title: string, content: string, confirmText: string) =>
  new Promise<boolean>((resolve) =>
    uni.showModal({ title, content, confirmText, cancelText: '取消', success: (r) => resolve(r.confirm) }),
  )

async function store() {
  let title = '已保存'
  const payload = rule()
  if (sourceId.value) {
    await api.updateSource(sourceId.value, payload)
  } else {
    const isBatch = Array.isArray(payload) || (typeof payload === 'object' && payload !== null && 'sources' in payload)
    // A rule named like an existing source replaces it instead of adding a duplicate.
    const res = await api.createSources(payload, isBatch)
    const updated = res.updated ?? 0
    const added = res.added ?? res.ids.length - updated
    if (res.skipped?.length) {
      uni.showModal({
        title: `新增 ${added} 个，更新 ${updated} 个`,
        content: `跳过 ${res.skipped.length} 个无效条目：` + res.skipped.slice(0, 5).map((s) => `${s.name}（${s.error}）`).join('；'),
        showCancel: false,
        success: () => uni.navigateBack(),
      })
      return
    }
    if (updated || isBatch) title = `新增 ${added} 个，更新 ${updated} 个`
  }
  uni.showToast({ title, icon: 'none' })
  setTimeout(() => uni.navigateBack(), 500)
}

const save = () =>
  run(async () => {
    if (!advanced.value) {
      if (handWritten && !(await confirm('改回自动识别？', '这个书源现在用的是手写的规则，按简单模式保存会丢掉这些规则。', '改回自动')))
        return
      // A source that cannot be recognised is of no use: say so before it is saved.
      const r = report.value ?? (await tryOut())
      if (r.failedStep) {
        const go = await confirm(
          '自动识别没有通过',
          `${STEPS[r.failedStep]}：${r.error}\n\n这类站点需要在高级模式导入 JSON 规则。仍然保存吗？`,
          '仍然保存',
        )
        if (!go) return
      }
    }
    await store()
  })

function remove() {
  uni.showModal({
    title: '删除这个书源？',
    content: '来自该书源的书架、历史和缓存也会一并删除',
    confirmText: '删除',
    cancelText: '取消',
    success: ({ confirm }) => {
      if (!confirm) return
      run(async () => {
        await api.deleteSource(sourceId.value)
        uni.navigateBack()
      })
    },
  })
}

function paste() {
  uni.getClipboardData({ success: (res) => (text.value = res.data) })
}

const STEPS = { search: '搜索', recommend: '首页书单', detail: '书籍详情', toc: '章节目录', content: '正文' }

/** The trial run as lines a reader can check, instead of the raw report. */
const lines = computed(() => {
  const r = report.value
  if (!r) return []
  const out: { ok: boolean; text: string }[] = []
  if (r.searchUrl) out.push({ ok: true, text: `搜索地址：${r.searchUrl}` })
  if (r.book) out.push({ ok: true, text: `找到 ${r.searchCount} 本书，取第一本：${r.book.title}` })
  if (r.detail) out.push({ ok: true, text: `详情：${r.detail.title}${r.detail.author ? '，作者 ' + r.detail.author : ''}${r.detail.cover ? '，有封面' : '，没有封面'}` })
  if (r.chapterCount) out.push({ ok: true, text: `目录：${r.chapterCount} 章，第一章「${r.firstChapter?.title}」` })
  if (r.content?.images?.length) out.push({ ok: true, text: `图片：${r.content.images.length} 张` })
  else if (r.content?.text) out.push({ ok: true, text: `正文：${r.content.text.slice(0, 60)}…` })
  if (r.failedStep) out.push({ ok: false, text: `${STEPS[r.failedStep]}失败：${r.error}` })
  if (typeof r.latencyMs === 'number') out.push({ ok: !r.failedStep, text: `全流程耗时：${r.latencyMs} ms` })
  return out
})
</script>

<template>
  <LoadState v-if="!allowed" :loading="roleLoading" :error="roleError" empty="只有管理员可以修改书源和订阅" @retry="refreshRole" />
  <view v-else class="edit">
    <view class="modes">
      <view class="mode" :class="{ on: !advanced }" @click="setMode(false)">简单</view>
      <view class="mode" :class="{ on: advanced }" @click="setMode(true)">高级（JSON）</view>
    </view>

    <template v-if="!advanced">
      <view class="field">
        <text class="label">名称</text>
        <input v-model="name" class="input" placeholder="给书源起个名字" />
      </view>
      <view class="field">
        <text class="label">地址</text>
        <input v-model="baseUrl" class="input" placeholder="网站首页地址，如 https://example.com" @input="report = null" />
      </view>
      <view class="field">
        <text class="label">类型</text>
        <view class="types">
          <view class="type" :class="{ on: type === 'novel' }" @click="type = 'novel'">小说</view>
          <view class="type" :class="{ on: type === 'comic' }" @click="type = 'comic'">漫画</view>
        </view>
      </view>
      <view class="hint muted">
        搜索、书籍信息、目录和正文都由服务器按页面结构自动识别，不用填规则。内容由脚本加载、需要登录或有验证的网站识别不了，请改用高级模式导入 JSON 规则。
      </view>
    </template>

    <template v-else>
      <view class="bar">
        <text class="muted">粘贴单个书源、书源数组、{sources:[...]} 源包，或声明式阅读（Legado）JSON</text>
        <text class="link" @click="paste">从剪贴板粘贴</text>
      </view>
      <textarea v-model="text" class="json" :maxlength="-1" auto-height :show-confirm-bar="false" />
    </template>

    <view class="bar">
      <text class="muted">试一下（不会保存）：{{ advanced ? '依次执行搜索、目录、第一章正文' : '关键词可不填，不填则从首页取一本书' }}</text>
    </view>
    <view class="test">
      <input v-model="keyword" class="keyword" placeholder="搜索关键词" />
      <wd-button size="small" plain :loading="testing" @click="test">测试</wd-button>
    </view>
    <view v-if="testing" class="report muted">正在识别，可能需要十几秒…</view>
    <view v-else-if="lines.length" class="report">
      <view v-for="(l, i) in lines" :key="i" class="line" :class="{ bad: !l.ok }">{{ l.ok ? '✓' : '✗' }} {{ l.text }}</view>
    </view>

    <view v-if="sourceId" class="monitor-box">
      <view class="monitor-head">
        <view class="monitor-title-wrap">
          <text class="monitor-title">数据源监听指标</text>
          <text v-if="!enabled && autoDisabled" class="status-pill bad">低准确率自动停用</text>
          <text v-else-if="!enabled" class="status-pill muted-pill">已停用</text>
          <text v-else class="status-pill good">启用中</text>
        </view>
        <view class="monitor-btns">
          <wd-button size="small" :loading="checking" @click="checkNow">立即分析</wd-button>
          <wd-button v-if="metrics" size="small" plain @click="resetMetrics">重置</wd-button>
        </view>
      </view>
      <template v-if="metrics && metrics.total > 0">
        <view class="m-bar">
          <view class="m-bar-fill" :class="rateClass(metrics.validRate)" :style="{ width: pct(metrics.validRate) }" />
        </view>
        <view class="m-grid">
          <view class="m-item">
            <view class="m-val">{{ metrics.avgLatencyMs }}ms</view>
            <view class="m-lbl">平均响应</view>
          </view>
          <view class="m-item">
            <view class="m-val">{{ metrics.p95LatencyMs }}ms</view>
            <view class="m-lbl">P95 响应</view>
          </view>
          <view class="m-item">
            <view class="m-val" :class="rateClass(metrics.validRate)">{{ pct(metrics.validRate) }}</view>
            <view class="m-lbl">有效准确率</view>
          </view>
          <view class="m-item">
            <view class="m-val bad">{{ pct(metrics.invalidRate) }}</view>
            <view class="m-lbl">无效占比</view>
          </view>
        </view>
        <view class="m-sub muted">
          总请求 {{ metrics.total }} · 有效 {{ metrics.valid }} · 空结果 {{ metrics.empty }} · 报错 {{ metrics.error }} · 最近 {{ metrics.lastLatencyMs }}ms（最小 {{ metrics.minLatencyMs }}ms / 最大 {{ metrics.maxLatencyMs }}ms）
        </view>
        <view v-if="actionRows.length" class="m-actions">
          <view v-for="a in actionRows" :key="a.key" class="m-action-row">
            <text class="a-name">{{ a.label }}</text>
            <text class="muted">有效 {{ a.valid }}/{{ a.total }}（{{ pct(a.validRate) }}）</text>
            <text class="muted">均 {{ a.avgLatencyMs }}ms</text>
          </view>
        </view>
        <view v-if="metrics.lastError" class="m-err">最近错误：{{ metrics.lastError }}</view>
      </template>
      <view v-else class="muted m-sub">暂无采样记录，点击「立即分析」或使用该源搜索阅读后展示响应时间与准确率（低于 50% 自动停用，恢复后分析自动开启）</view>
    </view>

    <view class="actions">
      <view v-if="sourceId" class="side"><wd-button plain type="error" @click="remove">删除</wd-button></view>
      <view class="primary"><wd-button block :loading="busy" @click="save">保存</wd-button></view>
    </view>
  </view>
</template>

<style lang="scss" scoped>
.edit {
  // Room for the fixed action bar.
  padding-bottom: calc(160rpx + env(safe-area-inset-bottom));
}

.modes {
  display: flex;
  margin: 24rpx 32rpx 8rpx;
  padding: 6rpx;
  border-radius: 12rpx;
  background: var(--line);

  .mode {
    flex: 1;
    padding: 12rpx 0;
    border-radius: 8rpx;
    font-size: 28rpx;
    text-align: center;
    color: #8a8378;

    &.on {
      background: #fff;
      color: #c0392b;
    }
  }
}

.field {
  display: flex;
  align-items: center;
  min-height: 96rpx;
  padding: 0 32rpx;
  border-bottom: 1rpx solid var(--line);
  background: #fff;

  .label {
    flex: none;
    width: 110rpx;
    font-size: 28rpx;
  }

  .input {
    flex: 1;
    font-size: 28rpx;
  }
}

.types {
  display: flex;
  gap: 16rpx;

  .type {
    padding: 8rpx 32rpx;
    border: 1rpx solid var(--line);
    border-radius: 8rpx;
    font-size: 26rpx;

    &.on {
      border-color: #c0392b;
      color: #c0392b;
    }
  }
}

.hint {
  padding: 20rpx 32rpx 0;
  line-height: 1.6;
}

.bar {
  display: flex;
  justify-content: space-between;
  padding: 24rpx 32rpx 12rpx;

  .link {
    flex: none;
    margin-left: 24rpx;
    font-size: 24rpx;
    color: #c0392b;
  }
}

.json {
  box-sizing: border-box;
  width: 100%;
  min-height: 600rpx;
  padding: 24rpx 32rpx;
  background: #fff;
  font-family: Menlo, Consolas, monospace;
  font-size: 24rpx;
  line-height: 1.6;
}

.actions {
  position: fixed;
  right: 0;
  bottom: 0;
  left: 0;
  z-index: 10;
  display: flex;
  gap: 24rpx;
  padding: 20rpx 32rpx calc(20rpx + env(safe-area-inset-bottom));
  border-top: 1rpx solid var(--line);
  background: var(--paper);

  .side {
    flex: none;
  }

  .primary {
    flex: 1;
  }
}

.test {
  display: flex;
  align-items: center;
  gap: 24rpx;
  padding: 12rpx 32rpx;

  .keyword {
    flex: 1;
    height: 64rpx;
    padding: 0 20rpx;
    border-radius: 8rpx;
    background: #fff;
  }
}

.report {
  margin: 24rpx 32rpx;
  padding: 24rpx;
  border-radius: 8rpx;
  background: #fff;
  font-size: 24rpx;
  line-height: 1.7;
  word-break: break-all;

  .line.bad {
    color: #c0392b;
  }
}

.monitor-box {
  margin: 24rpx 32rpx;
  padding: 24rpx;
  border-radius: 12rpx;
  background: #fff;
}

.monitor-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12rpx;
  margin-bottom: 16rpx;
}

.monitor-title-wrap {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 10rpx;
}

.monitor-title {
  font-size: 28rpx;
  font-weight: 600;
}

.status-pill {
  padding: 2rpx 12rpx;
  border-radius: 8rpx;
  font-size: 20rpx;

  &.good {
    background: #eafaf1;
    color: #239b56;
  }

  &.bad {
    background: #fdecea;
    color: #c0392b;
  }

  &.muted-pill {
    background: #f1ede6;
    color: #8a8378;
  }
}

.monitor-btns {
  flex: none;
  display: flex;
  gap: 12rpx;
}

.m-bar {
  height: 8rpx;
  margin-bottom: 18rpx;
  border-radius: 4rpx;
  background: #f1ece4;
  overflow: hidden;
}

.m-bar-fill {
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

.m-grid {
  display: grid;
  grid-template-columns: repeat(4, 1fr);
  gap: 12rpx;
  text-align: center;
}

.m-val {
  font-size: 26rpx;
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

.m-lbl {
  margin-top: 4rpx;
  font-size: 20rpx;
  color: #8a8378;
}

.m-sub {
  margin-top: 14rpx;
  font-size: 22rpx;
  line-height: 1.6;
}

.m-actions {
  margin-top: 12rpx;
  padding-top: 12rpx;
  border-top: 1rpx solid var(--line);
}

.m-action-row {
  display: flex;
  justify-content: space-between;
  padding: 6rpx 0;
  font-size: 22rpx;
}

.a-name {
  min-width: 110rpx;
  font-weight: 500;
}

.m-err {
  margin-top: 10rpx;
  font-size: 22rpx;
  color: #c0392b;
  word-break: break-all;
}
</style>
