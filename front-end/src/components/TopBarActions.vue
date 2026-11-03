<script setup>
import { computed, ref } from 'vue'
import { useRouter } from 'vue-router'
import { NPopover } from 'naive-ui'
import { useMobile } from '@/composables/useMobile'
import { useAuthStore } from '@/stores/auth'
import { useThemeStore } from '@/stores/theme'
import { isMacEmbedded } from '@/utils/embed'
import FunctionMenu from '@/components/FunctionMenu.vue'

// 全局入口：功能菜单 / 主题 / 头像。手机只有主题（功能菜单与头像在底部 Tab / 我的）。列表页并入页头，其余页由 PageTopBar 复用。
const router = useRouter()
const { isDesktop } = useMobile()
const auth = useAuthStore()
const theme = useThemeStore()
// 内嵌 WebView 的外观由 app 决定，切了不生效
const isEmbedded = isMacEmbedded()

const showUserPopover = ref(false)
const displayName = computed(() => auth.user?.username || '我')

async function logout() {
  showUserPopover.value = false
  await auth.logout()
  router.push('/login')
}
</script>

<template>
  <div class="tb-actions">
    <FunctionMenu v-if="isDesktop" />

    <button
      v-if="!isEmbedded"
      class="icon-btn"
      type="button"
      :aria-label="theme.isDark ? '切到浅色' : '切到深色'"
      :title="theme.isDark ? '浅色' : '深色'"
      @click="theme.toggle()"
    >
      <svg v-if="theme.isDark" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round"><circle cx="12" cy="12" r="4"/><path d="M12 2v2M12 20v2M4.9 4.9l1.4 1.4M17.7 17.7l1.4 1.4M2 12h2M20 12h2M4.9 19.1l1.4-1.4M17.7 6.3l1.4-1.4"/></svg>
      <svg v-else viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round"><path d="M20 14.5A8 8 0 0 1 9.5 4a8 8 0 1 0 10.5 10.5z"/></svg>
    </button>

    <NPopover
      v-if="isDesktop"
      trigger="click"
      :show="showUserPopover"
      placement="bottom-end"
      @update:show="v => showUserPopover = v"
    >
      <template #trigger>
        <button class="avatar" type="button" :aria-label="displayName" :title="displayName">{{ displayName.slice(0, 1).toUpperCase() }}</button>
      </template>
      <div class="user-popover">
        <div class="user-name">{{ displayName }}</div>
        <div class="user-item" @click="router.push('/settings'); showUserPopover = false">设置</div>
        <div class="user-item" @click="router.push('/me'); showUserPopover = false">我的</div>
        <div class="user-divider"></div>
        <div class="user-item danger" @click="logout">退出登录</div>
      </div>
    </NPopover>
  </div>
</template>

<style scoped>
.tb-actions { display: flex; align-items: center; gap: 7px; flex: 0 0 auto; }
.avatar {
  width: 30px;
  height: 30px;
  border-radius: 50%;
  border: 0.5px solid var(--color-border);
  background: var(--color-primary-soft);
  color: var(--color-primary);
  font-size: 12px;
  font-weight: 700;
  cursor: pointer;
}
.user-popover { min-width: 150px; }
.user-name { font-size: 12px; font-weight: 650; padding: 5px 10px 7px; }
.user-item { padding: 7px 10px; border-radius: 8px; font-size: 12.5px; cursor: pointer; }
.user-item:hover { background: var(--color-surface-hover); }
.user-item.danger { color: var(--color-danger); }
.user-divider { border-top: 1px solid var(--color-border); margin: 4px 0; }
</style>
