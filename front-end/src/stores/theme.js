import { defineStore } from 'pinia'
import { ref } from 'vue'
import { darkTheme } from 'naive-ui'
import { isMacEmbedded } from '@/utils/embed'

const KEY = 'xhanhelper_theme'

function systemPrefersDark() {
  return window.matchMedia('(prefers-color-scheme: dark)').matches
}

function apply(isDark, naiveTheme) {
  naiveTheme.value = isDark ? darkTheme : null
  document.documentElement.setAttribute('data-theme', isDark ? 'dark' : 'light')
}

export const useThemeStore = defineStore('theme', () => {
  const isDark = ref(false)
  const naiveTheme = ref(null)
  // 'light' | 'dark' | 'system'。内嵌 macOS WebView 恒为 system：app 外观经窗口传导，app 是唯一来源。
  const mode = ref('system')

  function paint() {
    const followSystem = isMacEmbedded() || mode.value === 'system'
    isDark.value = followSystem ? systemPrefersDark() : mode.value === 'dark'
    apply(isDark.value, naiveTheme)
  }

  function load() {
    // 分享页是公开独立页，始终以浅色为默认，且不读用户偏好
    if (typeof window !== 'undefined' && window.location.pathname.startsWith('/s/')) {
      isDark.value = false
      apply(false, naiveTheme)
      return
    }
    try {
      const saved = localStorage.getItem(KEY)
      mode.value = !isMacEmbedded() && (saved === 'light' || saved === 'dark') ? saved : 'system'
    } catch (e) {
      console.error('Failed to read theme preference', e)
      mode.value = 'system'
    }
    paint()
  }

  function setMode(m) {
    if (isMacEmbedded()) return
    mode.value = m === 'light' || m === 'dark' ? m : 'system'
    paint()
    try { localStorage.setItem(KEY, mode.value) } catch (e) {
      console.error('Failed to save theme preference', e)
    }
  }

  function toggle() {
    setMode(isDark.value ? 'light' : 'dark')
  }

  // 仅当前会话生效，不写 localStorage（分享页开关用）
  function set(v) {
    isDark.value = !!v
    apply(isDark.value, naiveTheme)
  }

  if (typeof window !== 'undefined') {
    const sharePage = window.location.pathname.startsWith('/s/')
    // 内嵌模式：app 切主题 → 媒体查询变化 → 页面即时跟随；跟随系统时同理
    window.matchMedia('(prefers-color-scheme: dark)').addEventListener('change', () => {
      if (!sharePage && (isMacEmbedded() || mode.value === 'system')) paint()
    })
  }

  load()

  return { isDark, naiveTheme, mode, toggle, set, setMode }
})
