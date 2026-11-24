<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { onLoad } from '@dcloudio/uni-app'
import { api, type Mode, type TestReport } from '@/api'
import { toastError } from '@/api/http'
import { useAppStore } from '@/stores/app'

let id = 0
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
const testing = ref(false)
const busy = ref(false)

// A trial run only speaks for the rule it ran.
watch([type, baseUrl, text], () => (report.value = null))

onLoad(async (query) => {
  id = Number(query?.id) || 0
  if (query?.json) advanced.value = true
  if (!id) return
  try {
    const src = (await api.sources()).find((s) => s.id === id)
    if (!src) return
    name.value = src.name
    type.value = src.type
    baseUrl.value = String(src.rule.baseUrl ?? '')
    text.value = JSON.stringify(src.rule, null, 2)
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
    report.value = await api.testSource(rule(), keyword.value.trim(), id)
    return report.value
  } finally {
    testing.value = false
  }
}

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
  if (id) await api.updateSource(id, rule())
  else {
    // A rule named like an existing source replaces it instead of adding a duplicate.
    const { ids, updated = 0 } = await api.createSources(rule())
    if (updated) title = ids.length > updated ? `新增 ${ids.length - updated} 个，更新 ${updated} 个` : `已更新 ${updated} 个同名书源`
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
        await api.deleteSource(id)
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
  return out
})
</script>

<template>
  <view class="edit">
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
        <text class="muted">粘贴一个书源或书源数组；没写的部分按 auto 自动识别。格式见 docs/source-rule.md</text>
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

    <view class="actions">
      <view v-if="id" class="side"><wd-button plain type="error" @click="remove">删除</wd-button></view>
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
</style>
