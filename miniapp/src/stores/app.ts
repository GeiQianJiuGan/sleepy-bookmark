import { defineStore } from 'pinia'
import { reactive, ref, watch } from 'vue'
import type { Mode } from '@/api'

export const THEMES = {
  light: { name: '白天', bg: '#ffffff', fg: '#222222' },
  sepia: { name: '护眼', bg: '#f4ecd8', fg: '#4a3b2a' },
  green: { name: '青草', bg: '#cce8cf', fg: '#2b3a2e' },
  dark: { name: '夜间', bg: '#1a1a1a', fg: '#9a9a9a' },
} as const

export type Theme = keyof typeof THEMES

export const useAppStore = defineStore('app', () => {
  /** The global novel / comic switch; every list page filters by it. */
  const mode = ref<Mode>(uni.getStorageSync('mode') === 'comic' ? 'comic' : 'novel')
  watch(mode, (m) => uni.setStorageSync('mode', m))

  const stored = uni.getStorageSync('reader') || {}
  const reader = reactive<{ fontSize: number; theme: Theme }>({
    fontSize: stored.fontSize || 18,
    theme: stored.theme in THEMES ? stored.theme : 'light',
  })
  watch(reader, (r) => uni.setStorageSync('reader', { ...r }), { deep: true })

  return { mode, reader }
})
