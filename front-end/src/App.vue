<script setup>
import { computed } from 'vue'
import { NConfigProvider, NMessageProvider } from 'naive-ui'
import { useThemeStore } from '@/stores/theme'
import { useMobile } from '@/composables/useMobile'
import ConversationList from '@/components/ConversationList.vue'

const theme = useThemeStore()
const { isMobile, drawerOpen, closeDrawer, sidebarCollapsed } = useMobile()

const themeOverrides = computed(() => ({
  common: {
    primaryColor: '#5b6abf',
    primaryColorHover: '#4a58a8',
    primaryColorPressed: '#3f4d96',
    primaryColorSuppl: '#5b6abf'
  }
}))
</script>

<template>
  <NConfigProvider :theme="theme.naiveTheme" :theme-overrides="themeOverrides">
    <NMessageProvider>
      <div style="display: flex; height: 100vh; width: 100vw; overflow: hidden;">
        <!-- 桌面端侧栏 -->
        <div v-if="!isMobile" :style="{ width: sidebarCollapsed ? '0' : '280px', overflow: 'hidden', transition: 'width 0.25s ease', flexShrink: 0 }">
          <ConversationList />
        </div>

        <!-- 移动端抽屉覆盖 -->
        <template v-if="isMobile">
          <div
            v-if="drawerOpen"
            style="position: fixed; inset: 0; z-index: 900; background: rgba(0,0,0,0.4);"
            @click="closeDrawer"
          />
          <div
            :style="{
              position: 'fixed', top: 0, left: 0, bottom: 0, zIndex: 1000,
              width: '280px', transform: drawerOpen ? 'translateX(0)' : 'translateX(-100%)',
              transition: 'transform 0.25s ease'
            }"
          >
            <ConversationList />
          </div>
        </template>

        <div style="flex: 1; min-width: 0; display: flex; flex-direction: column;">
          <router-view />
        </div>
      </div>
    </NMessageProvider>
  </NConfigProvider>
</template>

<style>
html, body, #app {
  margin: 0;
  padding: 0;
  height: 100%;
  width: 100%;
}
</style>
