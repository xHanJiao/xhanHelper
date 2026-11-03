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
    <!-- iOS 26/27 给主屏 PWA 顶部压的系统模糊：WebKit 只要发现「上端有 10px 以上的 fixed 元素」
         就不画这层模糊，并且拿它的 background-color 去涂状态栏。这个元素靠 background-clip: text
         做到「有颜色但一个像素都不画」，所以看不见。详见 docs/design/mobile-web-parity/01-*.md -->
    <div class="pwa-blur-killer" aria-hidden="true"></div>
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
.app-main {
  flex: 1;
  min-height: 0;
  display: flex;
  flex-direction: column;
}
/* WebKit 的判定条件（读源码得出，非公开 API）：
   - 采样点在上端中央、距顶 4px，命中的元素（或祖先）必须是 fixed/sticky；
   - 宽高都要 > 10px（thinBorderWidth），宽度还得 ≥ 视口的 90%；
   - opacity < 0.1 会被判为「几乎透明」而弃用；
   - 颜色取自 computed style 的 background-color，不是画出来的像素。
   所以 height 必须是 11px、z-index 拉满保证采样命中的是它，
   再用 background-clip: text 让空元素一个像素都不画（去掉这行就会变成压住顶部的一条实心带）。 */
.pwa-blur-killer {
  display: none;
}
@media (display-mode: standalone) and (max-width: 1099px) {
  .pwa-blur-killer {
    display: block;
    position: fixed;
    top: 0;
    left: 0;
    right: 0;
    height: 11px;
    z-index: 2147483647;
    pointer-events: none;
    background-color: var(--pwa-top-color);
    -webkit-background-clip: text;
    background-clip: text;
  }
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
