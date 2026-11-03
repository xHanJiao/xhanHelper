<script setup>
import { computed, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { NPopover } from 'naive-ui'
import { useMobile } from '@/composables/useMobile'
import TopBarBtn from '@/components/TopBarBtn.vue'
import ConversationSwitcher from '@/components/ConversationSwitcher.vue'
import TopBarActions from '@/components/TopBarActions.vue'
import { useGoBack } from '@/composables/useGoBack'

const props = defineProps({
  title: { type: String, default: '' },
  visible: { type: Boolean, default: true },
  agentName: { type: String, default: '' },
  agentId: { type: String, default: '' },
  agents: { type: Array, default: () => [] },
  showConversation: { type: Boolean, default: false },
  // 顶栏内容与下方聊天卡片同宽对齐（仅桌面；手机卡片是全宽的）
  alignCard: { type: Boolean, default: false }
})

const emit = defineEmits(['select-agent', 'popover-open'])

const showAgentPopover = ref(false)
const route = useRoute()
const router = useRouter()
const { isDesktop } = useMobile()

// 有 PageHeader 的列表页不再渲染本组件，桌面顶栏由 PageHeader + TopBarActions 承担
const showBack = computed(() =>
  !props.showConversation && (isDesktop.value || route.meta.topLevel !== true)
)
// 顶级页（对话/灵阅/文档/我的）自己有标题，顶栏不再重复品牌名
const titleText = computed(() =>
  props.title || (props.showConversation || route.meta.topLevel ? '' : 'xhanHelper')
)
const goBack = useGoBack()

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

function newChat() {
  router.push('/chat')
}
</script>

<template>
  <header
    class="app-topbar"
    :class="{ 'align-card': alignCard }"
    :style="{
      height: visible ? '56px' : '0px',
      overflow: 'visible'
    }"
  >
    <div class="topbar-left">
      <button v-if="showBack" class="back-btn" type="button" @click="goBack">
        <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"><path d="M15 6l-6 6 6 6"/></svg>
        <span>返回</span>
      </button>

      <ConversationSwitcher v-if="showConversation" />
      <button v-if="showConversation" class="icon-btn" type="button" aria-label="新对话" title="新对话" @click="newChat">
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

      <TopBarActions />
    </div>
  </header>
</template>

<style scoped>
.app-topbar {
  --topbar-pad: 22px;
  padding: 0 var(--topbar-pad);
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
/* 与 .chat-card 同宽：卡片是 calc(100% - 48px) / max 1080，两侧最小 24 */
@media (min-width: 1100px) {
  .app-topbar.align-card { padding-inline: max(24px, calc((100% - 1080px) / 2)); }
  /* 会话胶囊 34px，旁边的「＋」跟着等高（全局 .icon-btn 是 32，手机上会撑到 38 当触控区） */
  .app-topbar .icon-btn { width: 34px; height: 34px; }
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
  white-space: nowrap; /* CJK 会在字间断行，窄屏把「返回」拆成上下两行 */
  flex-shrink: 0;
}
.back-btn:hover { background: var(--color-primary-soft); color: var(--color-primary); }
.back-btn svg { width: 15px; height: 15px; }
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
</style>
