<script setup>
import { computed, watch } from 'vue'
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
