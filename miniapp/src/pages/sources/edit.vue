<script setup lang="ts">
import { ref } from 'vue'
import { onLoad } from '@dcloudio/uni-app'
import { api } from '@/api'
import { toastError } from '@/api/http'

const TEMPLATE = {
  name: '',
  type: 'novel',
  baseUrl: 'https://',
  search: { url: '/search?q={{key}}', list: 'css:.result li', title: 'css:a', bookUrl: 'css:a@href' },
  detail: { author: '', cover: '', intro: '' },
  toc: { list: 'css:#list dd a', name: '@text', url: '@href' },
  content: { body: 'css:#content', nextPage: '', purify: [] },
}

let id = 0
const text = ref(JSON.stringify(TEMPLATE, null, 2))
const keyword = ref('')
const report = ref('')
const busy = ref(false)

onLoad(async (query) => {
  id = Number(query?.id) || 0
  if (!id) return
  try {
    const src = (await api.sources()).find((s) => s.id === id)
    if (src) text.value = JSON.stringify(src.rule, null, 2)
  } catch (e) {
    toastError(e)
  }
})

function parse(): unknown {
  try {
    return JSON.parse(text.value)
  } catch (e) {
    throw new Error('JSON 格式错误：' + (e as Error).message)
  }
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

const save = () =>
  run(async () => {
    const rule = parse()
    // A new entry may be an array, which imports several sources at once.
    let title = '已保存'
    if (id) await api.updateSource(id, rule)
    else {
      // A rule named like an existing source replaces it instead of adding a duplicate.
      const { ids, updated = 0 } = await api.createSources(rule)
      if (updated) title = ids.length > updated ? `新增 ${ids.length - updated} 个，更新 ${updated} 个` : `已更新 ${updated} 个同名书源`
    }
    uni.showToast({ title, icon: 'none' })
    setTimeout(() => uni.navigateBack(), 500)
  })

const test = () =>
  run(async () => {
    if (!keyword.value.trim()) throw new Error('请先输入测试用的搜索关键词')
    report.value = '测试中…'
    try {
      report.value = JSON.stringify(await api.testSource(parse(), keyword.value.trim()), null, 2)
    } catch (e) {
      report.value = ''
      throw e
    }
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
</script>

<template>
  <view class="edit">
    <view class="bar">
      <text class="muted">书源规则（JSON），格式见仓库 docs/source-rule.md</text>
      <text class="link" @click="paste">从剪贴板粘贴</text>
    </view>
    <textarea v-model="text" class="json" :maxlength="-1" auto-height :show-confirm-bar="false" />

    <view class="bar"><text class="muted">测试：依次执行搜索、目录、第一章正文（不会保存）</text></view>
    <view class="test">
      <input v-model="keyword" class="keyword" placeholder="搜索关键词" />
      <wd-button size="small" plain :loading="busy" @click="test">测试</wd-button>
    </view>
    <view v-if="report" class="report">{{ report }}</view>

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
  font-family: Menlo, Consolas, monospace;
  font-size: 22rpx;
  white-space: pre-wrap;
  word-break: break-all;
}
</style>
