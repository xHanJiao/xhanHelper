import { defineStore } from 'pinia'
import { ref } from 'vue'
import { darkTheme } from 'naive-ui'
import { isMacEmbedded } from '@/utils/embed'

const KEY = 'xhanhelper_theme'

function isNightTime() {
  const h = new Date().getHours()
  return h >= 18 || h < 8
}

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

  function load() {
    try {
      if (isMacEmbedded()) {
        // 内嵌 WebView：app 外观经窗口 effectiveAppearance 传导到媒体查询，
        // app 是主题唯一来源，忽略 localStorage/时间启发
        isDark.value = systemPrefersDark()
      } else {
        const saved = localStorage.getItem(KEY)
        isDark.value = saved !== null ? saved === 'dark' : isNightTime()
      }
    } catch {
      isDark.value = isNightTime()
    }
    apply(isDark.value, naiveTheme)
  }

  function toggle() {
    isDark.value = !isDark.value
    apply(isDark.value, naiveTheme)
    try { localStorage.setItem(KEY, isDark.value ? 'dark' : 'light') } catch {}
  }

  // 内嵌模式：app 切主题 → 媒体查询变化 → 页面即时跟随
  if (typeof window !== 'undefined') {
    const mq = window.matchMedia('(prefers-color-scheme: dark)')
    mq.addEventListener('change', (e) => {
      if (isMacEmbedded()) {
        isDark.value = e.matches
        apply(isDark.value, naiveTheme)
      }
    })
  }

  load()

  return { isDark, naiveTheme, toggle }
})
