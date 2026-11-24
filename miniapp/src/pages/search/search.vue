<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { onShow } from '@dcloudio/uni-app'
import { api, type Book, type Recommendation, type SearchResult, type Source } from '@/api'
import { errorText } from '@/api/http'
import { useAppStore } from '@/stores/app'
import AppBar from '@/components/AppBar.vue'
import BookCover from '@/components/BookCover.vue'
import BookItem from '@/components/BookItem.vue'
import LoadState from '@/components/LoadState.vue'

const store = useAppStore()
const kind = computed(() => (store.mode === 'novel' ? '小说' : '漫画'))

// Filters: one source or all of them (0), and whether the title has to be the keyword.
const sources = ref<Source[]>([])
const sourceId = ref(0)
const exact = ref(false)
// The match mode sits in a panel that opens from the button next to the search box.
const showOptions = ref(false)
const sourceName = computed(() => sources.value.find((s) => s.id === sourceId.value)?.name || '全部书源')

// Before a search the page shows what each source recommends, in the order of the sources.
const lists = ref<Record<number, Recommendation>>({})
const homeLoading = ref(true)
const groups = computed(() =>
  sources.value
    .filter((s) => s.rule.recommend)
    .map((s) => lists.value[s.id])
    .filter((g) => g?.books.length),
)
const shownGroups = computed(() =>
  sourceId.value ? groups.value.filter((g) => g.sourceId === sourceId.value) : groups.value,
)

// Loads the sources and their recommendations. Both are extras of this page, so a failure only
// leaves what was there; the search itself reports its own errors. Each source is asked on its
// own, so that a slow site does not hold back the lists of the others.
async function loadHome() {
  const mode = store.mode
  const list = await api.sources(mode).catch(() => null)
  if (mode !== store.mode) return
  if (list) {
    sources.value = list.filter((s) => s.enabled)
    // The source that was searched is gone, and so are its books.
    if (sourceId.value && !sources.value.some((s) => s.id === sourceId.value)) pickSource(0)
  }
  await Promise.all(
    sources.value
      .filter((s) => s.rule.recommend)
      .map(async (s) => {
        const res = await api.recommend(mode, s.id).catch(() => null)
        if (res && mode === store.mode) lists.value = { ...lists.value, [s.id]: res[0] }
      }),
  )
  if (mode === store.mode) homeLoading.value = false
}

const keyword = ref('')
const books = ref<Book[]>([])
const errors = ref<SearchResult['errors']>([])
const loading = ref(false)
const searched = ref(false)
const error = ref('')
// Sources that did not answer are one quiet line; the details open on tap.
const showErrors = ref(false)
// Only the answer to the latest search is shown.
let latest = 0

async function search() {
  const key = keyword.value.trim()
  if (!key) return
  const seq = ++latest
  loading.value = true
  error.value = ''
  showErrors.value = false
  try {
    const res = await api.search(store.mode, key, { source: sourceId.value, exact: exact.value })
    if (seq !== latest) return
    books.value = res.books
    errors.value = res.errors
    searched.value = true
  } catch (e) {
    if (seq !== latest) return
    books.value = []
    errors.value = []
    error.value = errorText(e)
  } finally {
    if (seq === latest) loading.value = false
  }
}

function reset() {
  latest++
  books.value = []
  errors.value = []
  searched.value = false
  loading.value = false
  error.value = ''
}

// Whether every source that was asked failed, as opposed to some of them having no such book.
const allFailed = computed(
  () => errors.value.length > 0 && errors.value.length >= (sourceId.value ? 1 : sources.value.length || 1),
)
const searching = computed(() => loading.value || searched.value || !!error.value)

function pickSource(id: number) {
  sourceId.value = id
  if (searching.value) search()
}

function setExact(value: boolean) {
  if (exact.value === value) return
  exact.value = value
  if (searching.value) search()
}

const open = (b: Book) => uni.navigateTo({ url: `/pages/book/detail?id=${b.id}` })
const toSources = () => uni.navigateTo({ url: '/pages/sources/list' })

onShow(loadHome)
// An emptied search box goes back to the recommendations.
watch(keyword, (k) => {
  if (!k.trim() && searching.value) reset()
})
watch(
  () => store.mode,
  () => {
    reset()
    sources.value = []
    lists.value = {}
    sourceId.value = 0
    homeLoading.value = true
    loadHome()
    search()
  },
)
</script>

<template>
  <view>
    <AppBar title="搜索" />
    <view class="search-row">
      <wd-search
        v-model="keyword"
        hide-cancel
        custom-style="flex: 1; padding-right: 0; background: transparent; --wot-search-input-bg: #fff"
        :placeholder="exact ? `精确搜索：完整的${kind}名` : `搜索${kind}：名称 / 作者`"
        @search="search"
        @clear="reset"
      />
      <view class="more" :class="{ open: showOptions, on: exact }" @click="showOptions = !showOptions">
        <view class="slider" />
        <view class="slider low" />
      </view>
    </view>

    <view v-if="showOptions" class="options">
      <view class="row">
        <view class="label">匹配方式</view>
        <view class="match">
          <view class="option" :class="{ active: !exact }" @click="setExact(false)">模糊</view>
          <view class="option" :class="{ active: exact }" @click="setExact(true)">精确</view>
        </view>
      </view>
      <view class="muted">
        {{ exact ? `只显示${kind}名和关键词完全一致的结果` : `${kind}名或作者包含关键词，按相关度排序` }}
      </view>
    </view>

    <scroll-view class="chips" scroll-x :show-scrollbar="false">
      <view class="chip" :class="{ active: !sourceId }" @click="pickSource(0)">全部书源</view>
      <view
        v-for="s in sources"
        :key="s.id"
        class="chip"
        :class="{ active: sourceId === s.id }"
        @click="pickSource(s.id)"
      >
        {{ s.name }}
      </view>
    </scroll-view>

    <template v-if="searching">
      <LoadState v-if="loading || error" :loading="loading" :error="error" @retry="search" />
      <template v-else>
        <view v-if="books.length" class="summary muted">
          {{ sourceName }} · {{ exact ? '精确' : '模糊' }} · {{ books.length }} 本
        </view>
        <view v-for="b in books" :key="b.id" @click="open(b)">
          <BookItem :book="b" show-intro />
        </view>
        <LoadState
          v-if="!books.length"
          :empty="
            allFailed
              ? '书源暂时没有返回结果，稍后再试'
              : exact
                ? `没有叫这个名字的${kind}，试试模糊搜索`
                : '没有找到相关内容，换个关键词试试'
          "
        >
          <wd-button v-if="exact && !allFailed" custom-style="margin-top: 36rpx" size="small" @click="setExact(false)">
            改用模糊搜索
          </wd-button>
          <wd-button v-else custom-style="margin-top: 36rpx" size="small" plain @click="search">再搜一次</wd-button>
        </LoadState>
        <view v-if="errors.length" class="source-errors" @click="showErrors = !showErrors">
          <view class="muted">{{ errors.length }} 个书源暂时没有返回结果 {{ showErrors ? '▴' : '▾' }}</view>
          <template v-if="showErrors">
            <view v-for="e in errors" :key="e.source" class="muted detail">{{ e.source }}：{{ e.error }}</view>
          </template>
        </view>
      </template>
    </template>

    <template v-else>
      <view v-for="g in shownGroups" :key="g.sourceId" class="section">
        <view class="head">
          <view class="bar" />
          <view class="source ellipsis">{{ g.sourceName }}</view>
          <view class="muted">推荐 · 前 {{ g.books.length }}</view>
        </view>
        <scroll-view class="shelf" scroll-x :show-scrollbar="false">
          <view v-for="(b, i) in g.books" :key="b.id" class="card" @click="open(b)">
            <view class="cover">
              <BookCover :book="b" />
              <view class="rank" :class="{ top: i < 3 }">{{ i + 1 }}</view>
            </view>
            <view class="title">{{ b.title }}</view>
            <view class="muted ellipsis">{{ b.author || '未知作者' }}</view>
          </view>
        </scroll-view>
      </view>

      <!-- Placeholders in the shape of a section while the first lists load. -->
      <view v-if="homeLoading && !shownGroups.length" class="section">
        <view class="head"><view class="bar" /><view class="ghost line" /></view>
        <view class="shelf ghosts">
          <view v-for="n in 4" :key="n" class="card"><view class="ghost block" /></view>
        </view>
      </view>
      <view v-else-if="!shownGroups.length" class="hint">
        <template v-if="!sources.length">
          <view class="muted">还没有启用的{{ kind }}书源</view>
          <wd-button custom-style="margin-top: 36rpx" size="small" @click="toSources">去书源管理</wd-button>
        </template>
        <view v-else class="muted">
          {{ sourceId ? `「${sourceName}」` : '这些书源' }}暂时没有推荐，输入关键词直接搜索
        </view>
      </view>
    </template>
  </view>
</template>

<style lang="scss" scoped>
.search-row {
  display: flex;
  align-items: center;
}

// Two slider lines with a knob each; the knobs fill in while exact matching is on.
.more {
  flex: none;
  display: flex;
  flex-direction: column;
  justify-content: center;
  width: 68rpx;
  height: 68rpx;
  margin: 0 24rpx 0 16rpx;
  padding: 0 16rpx;
  box-sizing: border-box;
  color: #8a8178;
  border-radius: 34rpx;
  background: #fff;

  &.on,
  &.open {
    color: var(--brand);
  }

  &.open {
    background: #f6e3df;
  }
}

.slider {
  position: relative;
  height: 4rpx;
  margin: 7rpx 0;
  border-radius: 2rpx;
  background: currentColor;

  &::after {
    content: '';
    position: absolute;
    top: -5rpx;
    left: 4rpx;
    width: 10rpx;
    height: 10rpx;
    border: 3rpx solid currentColor;
    border-radius: 50%;
    background: #fff;
  }

  &.low::after {
    left: auto;
    right: 4rpx;
  }
}

.more.on .slider::after {
  background: currentColor;
}

.options {
  margin: 0 24rpx 20rpx;
  padding: 20rpx 28rpx 24rpx;
  border-radius: 20rpx;
  background: #fff;

  .row {
    display: flex;
    align-items: center;
    justify-content: space-between;
    margin-bottom: 12rpx;
  }

  .label {
    font-size: 28rpx;
  }
}

.chips {
  width: auto;
  margin: 0 24rpx;
  padding: 4rpx 0 20rpx;
  white-space: nowrap;
}

.chip {
  display: inline-block;
  max-width: 280rpx;
  margin-right: 16rpx;
  padding: 10rpx 24rpx;
  overflow: hidden;
  font-size: 24rpx;
  line-height: 1.4;
  text-overflow: ellipsis;
  vertical-align: top;
  color: #6f665d;
  border-radius: 28rpx;
  background: #ebe6de;

  &.active {
    color: #fff;
    background: var(--brand);
  }
}

.match {
  display: flex;
  padding: 4rpx;
  border-radius: 28rpx;
  background: #ebe6de;
}

.option {
  padding: 6rpx 20rpx;
  font-size: 24rpx;
  line-height: 1.4;
  color: #8a8178;
  border-radius: 24rpx;

  &.active {
    color: var(--brand);
    font-weight: 600;
    background: #fff;
  }
}

.summary {
  padding: 4rpx 32rpx 16rpx;
}

.section {
  margin: 4rpx 24rpx 24rpx;
  padding: 28rpx 0 24rpx;
  border-radius: 20rpx;
  background: #fff;
}

.head {
  display: flex;
  align-items: center;
  padding: 0 28rpx 24rpx;

  .bar {
    flex: none;
    width: 8rpx;
    height: 30rpx;
    margin-right: 16rpx;
    border-radius: 4rpx;
    background: var(--brand);
  }

  .source {
    flex: 1;
    min-width: 0;
    font-size: 30rpx;
    font-weight: 600;
  }

  .muted {
    flex: none;
    margin-left: 16rpx;
  }
}

.shelf {
  width: 100%;
  white-space: nowrap;
}

.card {
  display: inline-block;
  width: 176rpx;
  margin-right: 24rpx;
  vertical-align: top;
  white-space: normal;

  &:first-child {
    margin-left: 28rpx;
  }

  .muted {
    margin-top: 4rpx;
    font-size: 22rpx;
  }
}

.cover {
  position: relative;
}

.rank {
  position: absolute;
  top: 0;
  left: 0;
  min-width: 40rpx;
  height: 40rpx;
  font-size: 22rpx;
  font-weight: 700;
  line-height: 40rpx;
  text-align: center;
  color: #fff;
  border-radius: 10rpx 0 14rpx 0;
  background: rgba(43, 38, 34, 0.62);

  &.top {
    background: var(--brand);
  }
}

.title {
  display: -webkit-box;
  height: 72rpx;
  margin-top: 14rpx;
  overflow: hidden;
  font-size: 26rpx;
  line-height: 36rpx;
  -webkit-box-orient: vertical;
  -webkit-line-clamp: 2;
}

.ghosts {
  overflow: hidden;
}

.ghost {
  border-radius: 10rpx;
  background: #f0ece5;

  &.line {
    width: 200rpx;
    height: 30rpx;
  }

  &.block {
    padding-top: 133%;
  }
}

.hint {
  padding: 120rpx 60rpx;
  text-align: center;
}

.source-errors {
  padding: 28rpx 32rpx 48rpx;
  text-align: center;

  .detail {
    margin-top: 12rpx;
    text-align: left;
  }
}
</style>
