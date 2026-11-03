<script setup>
import { computed } from 'vue'
import { useRoute } from 'vue-router'
import { useMobile } from '@/composables/useMobile'
import { useKeyboardInset } from '@/composables/useKeyboardInset'
import BottomTabs from '@/components/BottomTabs.vue'

const route = useRoute()
const { isMobile } = useMobile()
const adminBlocked = computed(() => route.meta.adminOnly === true && isMobile.value)
const { inset: keyboardInset } = useKeyboardInset(isMobile)
const keyboardOpen = computed(() => keyboardInset.value > 0)
</script>

<template>
  <div class="app-shell" :style="{ '--keyboard-inset': keyboardInset + 'px' }">
    <!-- iOS 26/27 给主屏 PWA 顶部压的系统模糊：最顶端有真实绘制元素时系统改画实心，
         伪元素不行（踩过），所以这里必须是真的 div。浏览器/内嵌 WebView 下 display:none。 -->
    <div class="pwa-top-shield" aria-hidden="true"></div>
    <div v-if="adminBlocked" class="admin-blocked">
      <div class="admin-card">
        <div class="admin-icon">⌘</div>
        <h2>请在桌面端打开</h2>
        <p>管理后台只在桌面端提供。手机端请使用对话、灵阅、文档和「我的」。</p>
        <button type="button" @click="$router.push('/chat')">返回对话</button>
      </div>
    </div>
    <div v-else class="app-main">
      <router-view />
    </div>
    <BottomTabs v-if="isMobile && !keyboardOpen && !route.meta.hideTabs && !route.meta.adminOnly" />
  </div>
</template>

<style scoped>
.app-shell {
  height: calc(100dvh - var(--keyboard-inset, 0px));
  display: flex;
  flex-direction: column;
  min-height: 0;
  position: relative;
  z-index: 1;
}
/* 高度取 safe-top：各页顶部都留了这段 padding，色块只盖状态栏、不压内容。
   z-index 低于 lightbox(3000) / naive 弹窗，高于顶栏(30)。 */
.pwa-top-shield {
  display: none;
}
@media (display-mode: standalone) and (max-width: 1099px) {
  .pwa-top-shield {
    display: block;
    position: fixed;
    top: 0;
    left: 0;
    right: 0;
    height: var(--safe-top);
    background: var(--color-bg);
    pointer-events: none;
    z-index: 900;
  }
}
.app-main {
  flex: 1;
  min-height: 0;
  display: flex;
  flex-direction: column;
}
.admin-blocked {
  flex: 1;
  display: grid;
  place-items: center;
  padding: 24px;
}
.admin-card {
  max-width: 360px;
  text-align: center;
  padding: 26px 24px;
  border-radius: 14px;
  background: var(--color-surface);
  border: 0.5px solid var(--color-border);
  box-shadow: var(--shadow-md);
}
.admin-icon {
  width: 44px;
  height: 44px;
  margin: 0 auto 12px;
  border-radius: 14px;
  display: grid;
  place-items: center;
  background: var(--color-primary-soft);
  color: var(--color-primary);
  font-size: 20px;
}
.admin-card h2 { font-size: 17px; margin: 0 0 8px; }
.admin-card p { font-size: 12.5px; color: var(--color-text-secondary); line-height: 1.6; margin: 0 0 16px; }
.admin-card button {
  border: 0;
  border-radius: 999px;
  background: var(--color-primary);
  color: #fff;
  padding: 8px 16px;
  font-size: 12.5px;
  cursor: pointer;
}
[data-theme="dark"] .admin-card button { color: #10221B; }
</style>
