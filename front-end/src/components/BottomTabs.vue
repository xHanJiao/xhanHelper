<script setup>
import { computed } from 'vue'
import { useRoute, useRouter } from 'vue-router'

const route = useRoute()
const router = useRouter()

const tabs = [
  { key: 'chat', label: '对话', path: '/chat', icon: 'chat' },
  { key: 'artifacts', label: '灵阅', path: '/artifacts', icon: 'spark' },
  { key: 'documents', label: '文档', path: '/documents', icon: 'doc' },
  { key: 'me', label: '我的', path: '/me', icon: 'person' }
]

function isActive(tab) {
  if (tab.key === 'chat') return route.path === '/chat' || route.path.startsWith('/chat/')
  return route.path === tab.path || route.path.startsWith(tab.path + '/')
}

function go(tab) {
  if (router.currentRoute.value.path !== tab.path) router.push(tab.path)
}

const isHidden = computed(() => route.meta.hideTabs === true)
</script>

<template>
  <nav v-if="!isHidden" class="bottom-tabs" aria-label="主导航">
    <button
      v-for="tab in tabs"
      :key="tab.key"
      class="tab"
      :class="{ active: isActive(tab) }"
      type="button"
      @click="go(tab)"
    >
      <span class="tab-ico">
        <svg v-if="tab.icon === 'chat'" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"><path d="M21 12a8 8 0 0 1-8 8H5l-2 2V12a8 8 0 0 1 8-8h2a8 8 0 0 1 8 8z"/></svg>
        <svg v-else-if="tab.icon === 'spark'" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"><path d="M12 3l2.2 5.6L20 11l-5.8 2.4L12 19l-2.2-5.6L4 11l5.8-2.4L12 3z"/></svg>
        <svg v-else-if="tab.icon === 'doc'" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"><path d="M6 3h9l4 4v14H6z"/><path d="M14 3v5h5M9 13h7M9 17h5"/></svg>
        <svg v-else viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"><circle cx="12" cy="8" r="3.2"/><path d="M5 20c.8-3.4 3.4-5 7-5s6.2 1.6 7 5"/></svg>
      </span>
      <span>{{ tab.label }}</span>
    </button>
  </nav>
</template>

<style scoped>
.bottom-tabs {
  height: calc(var(--bottom-tabs-height) + var(--safe-bottom));
  padding: 4px 8px var(--safe-bottom);
  display: flex;
  align-items: flex-start;
  justify-content: space-around;
  background: rgba(252, 248, 240, 0.82);
  border-top: 0.5px solid var(--color-border);
  backdrop-filter: blur(14px);
  -webkit-backdrop-filter: blur(14px);
  flex-shrink: 0;
}
[data-theme="dark"] .bottom-tabs {
  background: rgba(30, 30, 32, 0.86);
}
.tab {
  flex: 1;
  border: 0;
  background: transparent;
  cursor: pointer;
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 3px;
  padding: 4px 0 0;
  color: var(--color-text-muted);
  font-size: 10px;
}
.tab-ico {
  width: 22px;
  height: 22px;
  display: grid;
  place-items: center;
  border-radius: 9px;
  transition: background var(--transition-fast), color var(--transition-fast);
}
.tab-ico svg {
  width: 19px;
  height: 19px;
}
.tab.active {
  color: var(--color-primary);
}
.tab.active .tab-ico {
  background: var(--color-primary-soft);
}
</style>
