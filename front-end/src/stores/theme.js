import { defineStore } from 'pinia'
import { ref } from 'vue'
import { darkTheme } from 'naive-ui'

const KEY = 'xhanhelper_theme'

function isNightTime() {
  const h = new Date().getHours()
  return h >= 18 || h < 8
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
      const saved = localStorage.getItem(KEY)
      isDark.value = saved !== null ? saved === 'dark' : isNightTime()
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

  load()

  return { isDark, naiveTheme, toggle }
})
