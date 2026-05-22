<script setup>
import { ref, computed } from 'vue'
import { useMobile } from '@/composables/useMobile'
import TopBarBtn from '@/components/TopBarBtn.vue'
import { NPopover } from 'naive-ui'

const props = defineProps({
  title: { type: String, default: '' },
  visible: { type: Boolean, default: true },
  agentName: { type: String, default: '' },
  agentId: { type: String, default: '' },
  agents: { type: Array, default: () => [] }
})

const emit = defineEmits(['select-agent', 'popover-open'])

const showAgentPopover = ref(false)

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

const { isMobile, toggleDrawer, toggleSidebar, sidebarCollapsed } = useMobile()

const sidebarIcon = computed(() => {
  if (isMobile.value) return '☰'
  return sidebarCollapsed.value ? '☰' : '«'
})

const agentLabel = computed(() => {
  const name = props.agentName || props.agentId
  return name ? name + ' ▾' : ''
})
</script>

<template>
  <div
    :style="{
      height: visible ? (isMobile ? '56px' : 'auto') : '0',
      padding: visible ? 'var(--space-md) var(--space-lg)' : '0 var(--space-lg)',
      borderBottom: visible ? '1px solid var(--color-border)' : 'none',
      background: 'var(--color-surface)',
      display: 'flex',
      alignItems: 'center',
      justifyContent: 'space-between',
      flexShrink: 0,
      overflow: 'hidden',
      transition: 'height 0.2s ease-out, padding 0.2s ease-out, border-width 0.2s ease-out'
    }"
  >
    <span style="font-size: var(--font-size-lg); font-weight: 500; color: var(--color-text); display: flex; align-items: center; gap: var(--space-xs);">
      <TopBarBtn :icon="sidebarIcon" variant="text" @click="isMobile ? toggleDrawer() : toggleSidebar()" />
      {{ title }}
    </span>
    <span style="display: flex; align-items: center; gap: var(--space-xs);">
      <NPopover
        v-if="agentId"
        trigger="click"
        :show="showAgentPopover"
        @update:show="handlePopoverShow"
      >
        <template #trigger>
          <TopBarBtn icon="🤖" :label="agentLabel" />
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
          <div class="agent-popover-item unbind" @click="handleUnbind">
            解绑智能体
          </div>
        </div>
      </NPopover>
      <slot name="right"></slot>
    </span>
  </div>
</template>

<style scoped>
.agent-popover {
  min-width: 180px;
}
.agent-popover-header {
  font-size: 12px;
  color: var(--color-text-muted);
  padding: 4px 12px;
  margin-bottom: 4px;
}
.agent-popover-item {
  padding: 6px 12px;
  cursor: pointer;
  display: flex;
  align-items: center;
  justify-content: space-between;
  color: var(--color-text);
  border-radius: var(--radius-sm);
  transition: background 0.1s;
}
.agent-popover-item:hover {
  background: var(--color-surface-hover);
}
.agent-popover-item.active {
  color: var(--color-primary);
  cursor: default;
}
.agent-popover-current {
  font-size: 12px;
  color: var(--color-text-muted);
}
.agent-popover-divider {
  border-top: 1px solid var(--color-border);
  margin: 4px 0;
}
.agent-popover-item.unbind {
  color: var(--color-danger);
}
</style>
