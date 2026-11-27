<script setup lang="ts">
import { computed, ref, watch, type Ref } from 'vue'
import { onPullDownRefresh, onReachBottom, onShow } from '@dcloudio/uni-app'
import { api, type Book, type BookPage, type FeedBook, type SearchResult, type Source } from '@/api'
import { errorText } from '@/api/http'
import { useAppStore } from '@/stores/app'
import AppBar from '@/components/AppBar.vue'
import BookItem from '@/components/BookItem.vue'
import BookWaterfall from '@/components/BookWaterfall.vue'
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

// Before a search the page shows books to pick from: the recommendations merged from all
// sources, or the ranking of one source. Both load a page at a time as the reader scrolls.
type Pager = ReturnType<typeof usePager<Book>>
function usePager<T extends Book>(fetch: (offset: number) => Promise<BookPage<T>>) {
  const items = ref<T[]>([]) as Ref<T[]>
  const loading = ref(false)
  const failed = ref(false)
  const done = ref(false)
  // What the server has handed out, which is more than `items` when a book came twice.
  let offset = 0
  let seq = 0

  async function load(reset = false) {
    if (reset) {
      seq++
      offset = 0
      loading.value = done.value = false
    } else if (loading.value || done.value) return
    const mine = seq
    loading.value = true
    failed.value = false
    try {
      const res = await fetch(offset)
      if (mine !== seq) return
      offset += res.books.length
      const have = new Set(reset ? [] : items.value.map((b) => b.id))
      const fresh = res.books.filter((b) => !have.has(b.id))
      items.value = reset ? fresh : items.value.concat(fresh)
      done.value = !res.books.length || offset >= res.total
    } catch {
      // These lists are extras of the page: a quiet line with a retry, not a message.
      if (mine === seq) failed.value = true
    } finally {
      if (mine === seq) loading.value = false
    }
  }

  function clear() {
    seq++
    offset = 0
    items.value = []
    loading.value = failed.value = done.value = false
  }

  return { items, loading, failed, done, load, clear }
}

const tab = ref<'feed' | 'rank'>('feed')
const feed = usePager<FeedBook>((offset) => api.feed(store.mode, offset))
const rankSources = computed(() => sources.value.filter((s) => s.rule.recommend))
const rankSourceId = ref(0)
const rank = usePager<Book>((offset) => api.rank(rankSourceId.value, offset))
const pager = computed<Pager>(() => (tab.value === 'feed' ? feed : rank))
const sourcesLoaded = ref(false)
const sourcesFailed = ref(false)
// The enabled sources the lists on the page were made from.
let listedSources = ''

function pickTab(t: 'feed' | 'rank') {
  tab.value = t
  if (!pager.value.items.value.length) loadList(true)
}

function pickRankSource(id: number) {
  if (rankSourceId.value === id) return
  rankSourceId.value = id
  rank.clear()
  rank.load(true)
}

function loadList(reset = false) {
  if (tab.value === 'rank' && !rankSourceId.value) return Promise.resolve()
  return pager.value.load(reset)
}

// Loads the sources, and the list of the open tab when it is still empty. A failure only
// leaves what was there; the search itself reports its own errors.
async function loadHome() {
  const mode = store.mode
  const list = await api.sources(mode).catch(() => null)
  if (mode !== store.mode) return
  sourcesFailed.value = !list
  if (list) {
    sources.value = list.filter((s) => s.enabled)
    // A source was added, removed or switched: the lists may hold books that are gone.
    const ids = sources.value.map((s) => s.id).join()
    if (sourcesLoaded.value && ids !== listedSources) {
      feed.clear()
      rank.clear()
    }
    listedSources = ids
    sourcesLoaded.value = true
    // The source that was searched is gone, and so are its books.
    if (sourceId.value && !sources.value.some((s) => s.id === sourceId.value)) pickSource(0)
    if (!rankSources.value.some((s) => s.id === rankSourceId.value)) {
      rankSourceId.value = rankSources.value[0]?.id || 0
      rank.clear()
    }
  }
  if (!pager.value.items.value.length && !pager.value.loading.value) await loadList(true)
}

// The line under a list that did not load: the sources first when those are missing.
const listFailed = computed(() => pager.value.failed.value || (sourcesFailed.value && !sourcesLoaded.value))
const retryList = () => (sourcesLoaded.value ? loadList(!pager.value.items.value.length) : loadHome())

const keyword = ref('')
const books = ref<Book[]>([])
const errors = ref<SearchResult['errors']>([])
// A long result list is put on the page a screen at a time.
const PAGE = 20
const shown = ref(PAGE)
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
    shown.value = PAGE
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

// The book that was opened from the page, looked at again on the way back.
let opened = 0
const open = (b: Book) => {
  opened = b.id
  uni.navigateTo({ url: `/pages/book/detail?id=${b.id}` })
}

// A book that was put on the shelf or read is no longer a recommendation.
async function dropKnown() {
  const id = opened
  opened = 0
  if (!id || !feed.items.value.some((b) => b.id === id)) return
  const book = await api.book(id).catch(() => null)
  if (book?.favorite || book?.progress) feed.items.value = feed.items.value.filter((b) => b.id !== id)
}
const toSources = () => uni.navigateTo({ url: '/pages/sources/list' })

onShow(() => {
  dropKnown()
  loadHome()
})
onReachBottom(() => {
  if (searching.value) shown.value = Math.min(shown.value + PAGE, books.value.length)
  else loadList()
})
onPullDownRefresh(async () => {
  if (searching.value) await search()
  else {
    // loadHome fills an empty list itself; a list that is there is read again.
    const had = pager.value.items.value.length > 0
    await loadHome()
    if (had) await loadList(true)
  }
  uni.stopPullDownRefresh()
})
// An emptied search box goes back to the recommendations.
watch(keyword, (k) => {
  if (!k.trim() && searching.value) reset()
})
watch(
  () => store.mode,
  () => {
    reset()
    sources.value = []
    sourcesLoaded.value = sourcesFailed.value = false
    sourceId.value = 0
    rankSourceId.value = 0
    feed.clear()
    rank.clear()
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
      <view v-if="sources.length" class="row scope">
        <view class="label">搜索范围</view>
      </view>
      <view v-if="sources.length" class="wrap">
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
      </view>
    </view>

    <view v-if="!searching" class="tabs">
      <view class="tab" :class="{ active: tab === 'feed' }" @click="pickTab('feed')">推荐</view>
      <view class="tab" :class="{ active: tab === 'rank' }" @click="pickTab('rank')">排行</view>
      <view class="muted tip">{{ tab === 'feed' ? '各书源榜单合并去重' : '按书源看榜单' }}</view>
    </view>

    <scroll-view v-if="searching" class="chips" scroll-x enhanced :show-scrollbar="false">
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
    <scroll-view v-else-if="tab === 'rank' && rankSources.length" class="chips" scroll-x enhanced :show-scrollbar="false">
      <view
        v-for="s in rankSources"
        :key="s.id"
        class="chip"
        :class="{ active: rankSourceId === s.id }"
        @click="pickRankSource(s.id)"
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
        <view v-for="b in books.slice(0, shown)" :key="b.id" @click="open(b)">
          <BookItem :book="b" show-intro />
        </view>
        <view v-if="books.length > PAGE" class="foot muted">
          {{ shown < books.length ? '上滑继续加载' : `已显示全部 ${books.length} 本` }}
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
      <BookWaterfall :books="pager.items.value" :ranked="tab === 'rank'" @open="open" />

      <!-- Placeholders in the shape of the cards while the first page loads. -->
      <view v-if="!pager.items.value.length && !listFailed && (pager.loading.value || !sourcesLoaded)" class="ghosts">
        <view v-for="n in 4" :key="n" class="ghost"><view class="block" /><view class="line" /><view class="line short" /></view>
      </view>
      <view v-else-if="listFailed" class="foot muted" @click="retryList">
        没有加载出来，点这里重试
      </view>
      <view v-else-if="pager.loading.value" class="foot"><wd-loading size="40rpx" /></view>
      <view v-else-if="pager.items.value.length" class="foot muted">
        {{ pager.done.value ? `已显示全部 ${pager.items.value.length} 本` : '上滑继续加载' }}
      </view>
      <view v-else class="hint">
        <template v-if="!sources.length">
          <view class="muted">还没有启用的{{ kind }}书源</view>
          <wd-button custom-style="margin-top: 36rpx" size="small" @click="toSources">查看共享书源</wd-button>
        </template>
        <view v-else class="muted">
          {{ tab === 'feed' ? '暂时没有新的推荐' : '这个书源暂时没有榜单' }}，输入关键词直接搜索
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

  .scope {
    margin-top: 24rpx;
  }

  // All sources at once, on as many lines as they take.
  .wrap {
    display: flex;
    flex-wrap: wrap;

    .chip {
      margin-bottom: 12rpx;

      &:not(.active) {
        background: #f3efe8;
      }
    }
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

.tabs {
  display: flex;
  align-items: baseline;
  padding: 4rpx 32rpx 20rpx;
}

.tab {
  position: relative;
  margin-right: 40rpx;
  padding-bottom: 12rpx;
  font-size: 30rpx;
  color: #8a8178;

  &.active {
    font-size: 34rpx;
    font-weight: 700;
    color: #2b2622;

    &::after {
      content: '';
      position: absolute;
      left: 50%;
      bottom: 0;
      width: 36rpx;
      height: 6rpx;
      margin-left: -18rpx;
      border-radius: 3rpx;
      background: var(--brand);
    }
  }
}

.tip {
  flex: 1;
  text-align: right;
}

.ghosts {
  display: flex;
  flex-wrap: wrap;
  justify-content: space-between;
  padding: 0 24rpx;
}

.ghost {
  width: calc(50% - 10rpx);
  margin-bottom: 20rpx;
  padding-bottom: 24rpx;
  overflow: hidden;
  border-radius: 20rpx;
  background: #fff;

  .block {
    padding-top: 133%;
    background: #f0ece5;
  }

  .line {
    height: 28rpx;
    margin: 20rpx 20rpx 0;
    border-radius: 8rpx;
    background: #f0ece5;

    &.short {
      width: 40%;
    }
  }
}

.foot {
  padding: 12rpx 32rpx 48rpx;
  text-align: center;
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
