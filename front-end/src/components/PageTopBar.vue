<script setup>
import { computed, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { NPopover } from 'naive-ui'
import { useMobile } from '@/composables/useMobile'
import { useAuthStore } from '@/stores/auth'
import { useThemeStore } from '@/stores/theme'
import TopBarBtn from '@/components/TopBarBtn.vue'
import ConversationSwitcher from '@/components/ConversationSwitcher.vue'
import FunctionMenu from '@/components/FunctionMenu.vue'

const props = defineProps({
  title: { type: String, default: '' },
  visible: { type: Boolean, default: true },
  agentName: { type: String, default: '' },
  agentId: { type: String, default: '' },
  agents: { type: Array, default: () => [] },
  showConversation: { type: Boolean, default: false }
})

const emit = defineEmits(['select-agent', 'popover-open'])

const showAgentPopover = ref(false)
const showUserPopover = ref(false)
const route = useRoute()
const router = useRouter()
const { isMobile, isDesktop } = useMobile()
const auth = useAuthStore()
const theme = useThemeStore()

const displayName = computed(() => auth.user?.username || '我')
const showBack = computed(() => !props.showConversation && route.meta.topLevel !== true)
// 顶级页（对话/灵阅/文档/我的）自己有标题，顶栏不再重复品牌名
const titleText = computed(() => props.title || (props.showConversation || route.meta.topLevel ? '' : 'xhanHelper'))

function handlePopoverShow(v) {
  showAgentPopover.value = v
  if (v) emit('popover-open')
}

function handleSelectAgent(agentId) {
  showAgentPopover.value = false
  emit('select-agent', agentId)
}

function handleUnbind() {
  showAgentPopover.value = false
  emit('select-agent', null)
}

function goBack() {
  if (window.history.length > 1) router.back()
  else router.push('/chat')
}

function newChat() {
  router.push('/chat')
  showUserPopover.value = false
}

async function logout() {
  showUserPopover.value = false
  await auth.logout()
  router.push('/login')
}
</script>

<template>
  <header
    class="app-topbar"
    :style="{
      height: visible ? '56px' : '0px',
      padding: visible ? '0 var(--topbar-pad)' : '0 var(--topbar-pad)',
      overflow: 'visible'
    }"
  >
    <div class="topbar-left">
      <button v-if="showBack" class="back-btn" type="button" @click="goBack">
        <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"><path d="M15 6l-6 6 6 6"/></svg>
        <span>返回</span>
      </button>

      <ConversationSwitcher v-if="showConversation" />
      <button v-if="showConversation" class="icon-btn" type="button" title="新对话" @click="newChat">
        <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round"><path d="M12 5v14M5 12h14"/></svg>
      </button>

      <div v-if="titleText" class="page-title">{{ titleText }}</div>
    </div>

    <div class="topbar-right">
      <!-- 已绑定 agent：切换/解绑 -->
      <NPopover
        v-if="agentId"
        trigger="click"
        :show="showAgentPopover"
        placement="bottom-end"
        @update:show="handlePopoverShow"
      >
        <template #trigger>
          <TopBarBtn :label="agentName ? agentName + ' ▾' : '智能体 ▾'" />
        </template>
        <div class="agent-popover">
          <div class="agent-popover-header">已绑定智能体</div>
          <div
            v-for="agent in agents"
            :key="agent.id"
            class="agent-popover-item"
            :class="{ active: agent.id === agentId }"
            @click="agent.id !== agentId && handleSelectAgent(agent.id)"
          >
            <span>{{ agent.name }}</span>
            <span v-if="agent.id === agentId" class="agent-popover-current">当前</span>
          </div>
          <div class="agent-popover-divider"></div>
          <div class="agent-popover-item unbind" @click="handleUnbind">解绑智能体</div>
        </div>
      </NPopover>

      <!-- 未绑定 agent：绑定入口 -->
      <NPopover
        v-if="!agentId && agents.length > 0"
        trigger="click"
        :show="showAgentPopover"
        placement="bottom-end"
        @update:show="handlePopoverShow"
      >
        <template #trigger>
          <TopBarBtn label="智能体 ▾" />
        </template>
        <div class="agent-popover">
          <div class="agent-popover-header">选择智能体</div>
          <div
            v-for="agent in agents"
            :key="agent.id"
            class="agent-popover-item"
            @click="handleSelectAgent(agent.id)"
          >
            <span>{{ agent.name }}</span>
          </div>
        </div>
      </NPopover>

      <slot name="right"></slot>

      <FunctionMenu v-if="isDesktop" />

      <button v-if="isDesktop" class="icon-btn" type="button" :title="theme.isDark ? '浅色' : '深色'" @click="theme.toggle()">
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
          <button class="avatar" type="button">{{ displayName.slice(0, 1).toUpperCase() }}</button>
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
  </header>
</template>

<style scoped>
.app-topbar {
  --topbar-pad: 22px;
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 10px;
  flex-shrink: 0;
  position: relative;
  z-index: 30;
  transition: height 0.2s ease-out, padding 0.2s ease-out;
}
@media (max-width: 1099px) {
  .app-topbar { --topbar-pad: 14px; padding-top: var(--safe-top); box-sizing: content-box; }
}
.topbar-left {
  display: flex;
  align-items: center;
  gap: 9px;
  min-width: 0;
}
.topbar-right {
  display: flex;
  align-items: center;
  gap: 7px;
  flex: 0 0 auto;
}
.page-title {
  font-size: 13.5px;
  font-weight: 600;
  color: var(--color-text-secondary);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}
.back-btn {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  border: 0;
  background: transparent;
  color: var(--color-text-secondary);
  font-size: 12.5px;
  cursor: pointer;
  padding: 5px 4px;
  border-radius: 8px;
}
.back-btn:hover { background: var(--color-primary-soft); color: var(--color-primary); }
.back-btn svg { width: 15px; height: 15px; }
.icon-btn {
  width: 32px;
  height: 32px;
  border-radius: 999px;
  border: 0.5px solid var(--color-border);
  background: rgba(255, 255, 255, 0.24);
  display: grid;
  place-items: center;
  cursor: pointer;
  color: var(--color-text-secondary);
  padding: 0;
  transition: background var(--transition-fast), color var(--transition-fast);
}
[data-theme="dark"] .icon-btn { background: rgba(255, 255, 255, 0.04); }
.icon-btn:hover { background: var(--color-primary-soft); color: var(--color-primary); }
.icon-btn svg { width: 16px; height: 16px; }
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
.agent-popover { min-width: 180px; }
.agent-popover-header { font-size: 12px; color: var(--color-text-muted); padding: 4px 12px; margin-bottom: 4px; }
.agent-popover-item {
  padding: 6px 12px;
  cursor: pointer;
  display: flex;
  align-items: center;
  justify-content: space-between;
  color: var(--color-text);
  border-radius: var(--radius-sm);
}
.agent-popover-item:hover { background: var(--color-surface-hover); }
.agent-popover-item.active { color: var(--color-primary); cursor: default; }
.agent-popover-current { font-size: 12px; color: var(--color-text-muted); }
.agent-popover-divider { border-top: 1px solid var(--color-border); margin: 4px 0; }
.agent-popover-item.unbind { color: var(--color-danger); }
.user-popover { min-width: 150px; }
.user-name { font-size: 12px; font-weight: 650; padding: 5px 10px 7px; }
.user-item { padding: 7px 10px; border-radius: 8px; font-size: 12.5px; cursor: pointer; }
.user-item:hover { background: var(--color-surface-hover); }
.user-item.danger { color: var(--color-danger); }
.user-divider { border-top: 1px solid var(--color-border); margin: 4px 0; }
</style>
