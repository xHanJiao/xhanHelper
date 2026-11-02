<script setup>
import { computed, onBeforeUnmount, onMounted, watch } from 'vue'
import { useRouter, useRoute } from 'vue-router'
import { NConfigProvider, NMessageProvider } from 'naive-ui'
import { useThemeStore } from '@/stores/theme'
import { useAuthStore } from '@/stores/auth'
import AppShell from '@/layouts/AppShell.vue'

const theme = useThemeStore()
const auth = useAuthStore()
const router = useRouter()
const route = useRoute()

// token 失效时自动跳转登录页（覆盖心跳401/API401/跨tab登出等所有场景）
watch(() => auth.token, (newToken, oldToken) => {
  if (oldToken && !newToken) {
    if (route.path !== '/login' && !route.path.startsWith('/s/')) {
      router.push('/login')
    }
  }
})

// 代码块「复制」按钮在 v-html 里，只能靠事件代理；捕获阶段拦下，
// 避免同时触发消息选择模式或正文内链跳转。
async function onCodeCopyClick(e) {
  const btn = e.target?.closest?.('[data-code-copy]')
  if (!btn) return
  e.preventDefault()
  e.stopPropagation()
  const code = btn.closest('.code-block')?.querySelector('pre code')?.textContent || ''
  let copied = true
  try {
    await navigator.clipboard.writeText(code)
  } catch (err) {
    console.error('Copy code via clipboard API failed, falling back to execCommand', err)
    copied = false
    const ta = document.createElement('textarea')
    ta.value = code
    document.body.appendChild(ta)
    ta.select()
    try {
      copied = document.execCommand('copy')
    } catch (e) {
      console.error('Copy code fallback (execCommand) failed', e)
    } finally {
      ta.remove()
    }
  }
  btn.textContent = copied ? '已复制' : '复制失败'
  setTimeout(() => { btn.textContent = '复制' }, 1500)
}

onMounted(() => document.addEventListener('click', onCodeCopyClick, true))
onBeforeUnmount(() => document.removeEventListener('click', onCodeCopyClick, true))

const themeOverrides = computed(() => {
  const dark = theme.isDark
  return {
    common: {
      primaryColor: dark ? '#92C8B4' : '#2F7D63',
      primaryColorHover: dark ? '#B4E0CF' : '#245F4B',
      primaryColorPressed: dark ? '#7FB7A2' : '#1E4E3F',
      primaryColorSuppl: dark ? '#92C8B4' : '#2F7D63',
      bodyColor: dark ? '#1C1C1E' : '#F7EEDD',
      cardColor: dark ? '#242426' : '#FBF6EC',
      modalColor: dark ? '#242426' : '#FBF6EC',
      popoverColor: dark ? '#242426' : '#FBF6EC',
      textColorBase: dark ? '#FFFFFF' : '#3B2E1F',
      textColor2: dark ? 'rgba(255,255,255,0.66)' : 'rgba(59,46,31,0.64)',
      textColor3: dark ? 'rgba(255,255,255,0.40)' : 'rgba(59,46,31,0.42)',
      borderColor: dark ? 'rgba(255,255,255,0.12)' : 'rgba(59,46,31,0.12)'
    },
    Card: {
      borderRadius: '14px'
    }
  }
})
</script>

<template>
  <NConfigProvider :theme="theme.naiveTheme" :theme-overrides="themeOverrides">
    <NMessageProvider>
      <router-view v-if="route.meta.bare" />
      <AppShell v-else />
    </NMessageProvider>
  </NConfigProvider>
</template>
