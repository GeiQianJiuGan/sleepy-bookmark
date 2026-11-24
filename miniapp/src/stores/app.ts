import { defineStore } from 'pinia'
import { reactive, ref, watch } from 'vue'
import type { Mode } from '@/api'

export const THEMES = {
  light: { name: '白天', bg: '#ffffff', fg: '#222222' },
  sepia: { name: '护眼', bg: '#f4ecd8', fg: '#4a3b2a' },
  green: { name: '青草', bg: '#cce8cf', fg: '#2b3a2e' },
  dark: { name: '夜间', bg: '#1a1a1a', fg: '#a8a8a8' },
} as const

export type Theme = keyof typeof THEMES

export const LINE_HEIGHTS = [
  { name: '紧凑', value: 1.5 },
  { name: '标准', value: 1.8 },
  { name: '宽松', value: 2.1 },
] as const

export type PageMode = 'scroll' | 'curl' | 'cover' | 'slide'
export type ComicMode = 'scroll' | 'page-ltr' | 'page-rtl'

export const PAGE_MODES: { value: PageMode; name: string }[] = [
  { value: 'scroll', name: '上下滑动' },
  { value: 'curl', name: '仿真翻页' },
  { value: 'cover', name: '左右覆盖' },
  { value: 'slide', name: '左右平移' },
]

export const COMIC_MODES: { value: ComicMode; name: string }[] = [
  { value: 'scroll', name: '条漫上下' },
  { value: 'page-ltr', name: '左右翻页' },
  { value: 'page-rtl', name: '日漫右向左' },
]

export interface ReaderSettings {
  fontSize: number
  lineHeight: number
  theme: Theme
  /** The theme that leaving the night mode goes back to. */
  dayTheme: Theme
  /** Novel page-turning mode & animation. */
  pageMode: PageMode
  /** When true in paged mode, tapping either side advances to the next page. */
  tapNextOnly: boolean
  /** Comic viewing direction / layout mode. */
  comicMode: ComicMode
  /** Whether comic pages are set apart by a gap. */
  comicGap: boolean
  /** Where comic pages are loaded from: whichever of the server and the source site is faster, or always the server. */
  imageRoute: 'auto' | 'server'
  /** Auto page-turn / auto-scroll speed (1 = slowest, 5 = fastest). */
  autoSpeed: number
}

export const useAppStore = defineStore('app', () => {
  /** The global novel / comic switch; every list page filters by it. */
  const mode = ref<Mode>(uni.getStorageSync('mode') === 'comic' ? 'comic' : 'novel')
  watch(mode, (m) => uni.setStorageSync('mode', m))

  const stored = uni.getStorageSync('reader') || {}
  const reader = reactive<ReaderSettings>({
    fontSize: stored.fontSize || 18,
    lineHeight: LINE_HEIGHTS.some((l) => l.value === stored.lineHeight) ? stored.lineHeight : 1.8,
    theme: stored.theme in THEMES ? stored.theme : 'light',
    dayTheme: stored.dayTheme in THEMES && stored.dayTheme !== 'dark' ? stored.dayTheme : 'light',
    pageMode: PAGE_MODES.some((p) => p.value === stored.pageMode) ? stored.pageMode : 'scroll',
    tapNextOnly: !!stored.tapNextOnly,
    comicMode: COMIC_MODES.some((c) => c.value === stored.comicMode) ? stored.comicMode : 'scroll',
    comicGap: !!stored.comicGap,
    imageRoute: stored.imageRoute === 'server' ? 'server' : 'auto',
    autoSpeed: typeof stored.autoSpeed === 'number' && stored.autoSpeed >= 1 && stored.autoSpeed <= 5 ? stored.autoSpeed : 3,
  })
  watch(reader, (r) => uni.setStorageSync('reader', { ...r }), { deep: true })

  /** Data saver: lists and book pages show the title instead of fetching the cover. */
  const dataSaver = ref(!!uni.getStorageSync('dataSaver'))
  watch(dataSaver, (on) => uni.setStorageSync('dataSaver', on))

  return { mode, reader, dataSaver }
})
