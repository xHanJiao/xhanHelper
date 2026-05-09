import { defineStore } from 'pinia'
import { ref, watch } from 'vue'
import { darkTheme } from 'naive-ui'

const KEY = 'xhanhelper_theme'

export const useThemeStore = defineStore('theme', () => {
  const isDark = ref(false)
  const naiveTheme = ref(null)

  function load() {
    try {
      isDark.value = localStorage.getItem(KEY) === 'dark'
      naiveTheme.value = isDark.value ? darkTheme : null
      document.documentElement.setAttribute('data-theme', isDark.value ? 'dark' : 'light')
    } catch {}
  }

  function toggle() {
    isDark.value = !isDark.value
    naiveTheme.value = isDark.value ? darkTheme : null
    document.documentElement.setAttribute('data-theme', isDark.value ? 'dark' : 'light')
    try { localStorage.setItem(KEY, isDark.value ? 'dark' : 'light') } catch {}
  }

  load()

  return { isDark, naiveTheme, toggle }
})
