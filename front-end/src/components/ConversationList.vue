<script setup>
import { ref, computed, watch, onMounted, onUnmounted } from 'vue'
import { useRouter, useRoute } from 'vue-router'
import { useConversationsStore } from '@/stores/conversations'
import { useChatStore } from '@/stores/chat'
import { useAuthStore } from '@/stores/auth'
import { api } from '@/api/client'
import { NButton, NPopconfirm, NModal, NInput, useMessage } from 'naive-ui'

const router = useRouter()
const route = useRoute()
const convs = useConversationsStore()
const chat = useChatStore()
const message = useMessage()

const renameModalVisible = ref(false)
const renamingId = ref(null)
const renamingTitle = ref('')
const hoveredId = ref(null)
const confirmingId = ref(null)

const auth = useAuthStore()

const unreadCount = ref(0)
let unreadTimer = null

async function fetchUnreadCount() {
  try {
    const res = await api.get('/api/mailbox/unread-count')
    unreadCount.value = res.count || 0
  } catch (_) {}
}

watch(() => route.path, () => {
  fetchUnreadCount()
})

onMounted(() => {
  fetchUnreadCount()
  unreadTimer = setInterval(fetchUnreadCount, 30000)
})

onUnmounted(() => {
  if (unreadTimer) clearInterval(unreadTimer)
})

const activeId = computed(() => route.params.id)

function handleMouseLeave(conv) {
  if (confirmingId.value !== conv.id) {
    hoveredId.value = null
  }
}

const hasMore = computed(() => convs.list.length < convs.total)

function handleLoadMore() {
  convs.fetchList(convs.list.length)
}

async function handleNewConversation(agentId) {
  try {
    const res = await api.post('/api/conversations', agentId ? { agent_id: agentId } : {})
    chat.reset()
    convs.prepend({ id: res.id, title: res.title, agent_id: res.agent_id, agent_name: res.agent_name })
    router.push(`/chat/${res.id}`)
  } catch (e) {
    message.error('创建对话失败')
  }
}

const navExpanded = ref(false)
const adminExpanded = ref(false)
const historyExpanded = ref(true)

const navItems = [
  { label: '任务', path: '/missions' },
  { label: '账单', path: '/bills' },
  { label: '智能体', path: '/agents' },
  { label: '信箱', path: '/mailbox' },
  { label: '文档', path: '/documents' }
]

const adminItems = [
  { label: '记忆', path: '/memories' },
  { label: '笔记', path: '/notes' },
  { label: '主题', path: '/themes' },
  { label: '监控看板', path: '/admin', adminOnly: true }
]

const isAdmin = computed(() => auth.user?.isAdmin === true)

function isNavActive(path) {
  return route.path === path
}

watch(() => auth.token, (token) => {
  if (token) {
    convs.fetchList()
  }
}, { immediate: true })

function handleRename(conv) {
  renamingId.value = conv.id
  renamingTitle.value = conv.title
  renameModalVisible.value = true
}

async function confirmRename() {
  if (!renamingTitle.value.trim()) return
  const res = await convs.rename(renamingId.value, renamingTitle.value.trim())
  if (res.success) {
    message.success('重命名成功')
    renameModalVisible.value = false
  } else {
    message.error(res.message)
  }
}

async function handleDelete(conv) {
  const res = await convs.remove(conv.id)
  if (res.success) {
    message.success('删除成功')
    if (chat.conversationId === conv.id) {
      chat.reset()
    }
    if (activeId.value === conv.id) {
      router.push('/chat')
    }
  } else {
    message.error(res.message)
  }
}

function handleSelect(conv) {
  router.push(`/chat/${conv.id}`)
}
</script>

<template>
  <div style="width: 280px; background: var(--color-surface); border-right: 1px solid var(--color-border); display: flex; flex-direction: column; height: 100%;">
    <!-- 新对话按钮 -->
    <div style="padding: var(--space-md) var(--space-md) 0;">
      <button
        @click="handleNewConversation"
        style="width: 100%; display: flex; align-items: center; justify-content: center; gap: 8px; padding: 10px 0; border: 1px solid var(--color-border); border-radius: 24px; background: var(--color-primary-soft); backdrop-filter: blur(8px); -webkit-backdrop-filter: blur(8px); color: var(--color-text-secondary); font-size: 14px; font-weight: 500; cursor: pointer; transition: all var(--transition-fast);"
        @mouseover="e => { e.currentTarget.style.background = 'var(--color-primary)'; e.currentTarget.style.borderColor = 'var(--color-primary)'; e.currentTarget.style.color = '#fff'; }"
        @mouseout="e => { e.currentTarget.style.background = 'var(--color-primary-soft)'; e.currentTarget.style.borderColor = 'var(--color-border)'; e.currentTarget.style.color = 'var(--color-text-secondary)'; }"
      >
        <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round">
          <line x1="12" y1="5" x2="12" y2="19"/>
          <line x1="5" y1="12" x2="19" y2="12"/>
        </svg>
        新对话
      </button>
    </div>

    <!-- 导航区 -->
    <div style="padding: var(--space-md) var(--space-md) var(--space-sm);">
      <div
        @click="navExpanded = !navExpanded"
        :style="{
          padding: 'var(--space-sm) var(--space-md)',
          borderRadius: 'var(--radius-md)',
          marginBottom: navExpanded ? 'var(--space-xs)' : '0',
          cursor: 'pointer',
          fontSize: '13px',
          color: 'var(--color-text-muted)',
          display: 'flex',
          alignItems: 'center',
          gap: 'var(--space-xs)',
          transition: 'background var(--transition-fast)'
        }"
        @mouseover="e => e.currentTarget.style.background = 'var(--color-surface-hover)'"
        @mouseout="e => e.currentTarget.style.background = 'transparent'"
      >
        <span style="transition: transform 0.2s;" :style="{ transform: navExpanded ? 'rotate(0deg)' : 'rotate(-90deg)' }">▾</span>
        <span>功能</span>
      </div>
      <template v-if="navExpanded">
        <div
          v-for="item in navItems"
          :key="item.path"
          @click="router.push(item.path)"
          :style="{
            padding: 'var(--space-sm) var(--space-md)',
            borderRadius: 'var(--radius-md)',
            marginBottom: 'var(--space-xs)',
            cursor: 'pointer',
            fontSize: '14px',
            color: isNavActive(item.path) ? 'var(--color-primary)' : 'var(--color-text-secondary)',
            background: isNavActive(item.path) ? 'var(--color-primary-soft)' : 'transparent',
            fontWeight: isNavActive(item.path) ? '500' : '400',
            transition: 'background var(--transition-fast), color var(--transition-fast)'
          }"
          @mouseover="e => { if (!isNavActive(item.path)) e.currentTarget.style.background = 'var(--color-surface-hover)' }"
          @mouseout="e => { if (!isNavActive(item.path)) e.currentTarget.style.background = 'transparent' }"
        >
          {{ item.label }}
          <span
            v-if="item.path === '/mailbox' && unreadCount > 0"
            :style="{
              display: 'inline-flex',
              alignItems: 'center',
              justifyContent: 'center',
              minWidth: '18px',
              height: '18px',
              padding: '0 5px',
              borderRadius: '9px',
              background: 'var(--color-danger)',
              color: '#fff',
              fontSize: '11px',
              fontWeight: '600',
              marginLeft: 'auto',
            }"
          >{{ unreadCount > 99 ? '99+' : unreadCount }}</span>
        </div>
      </template>
    </div>

    <!-- 管理 -->
    <div style="padding: var(--space-sm) var(--space-md) var(--space-sm);">
      <div
        @click="adminExpanded = !adminExpanded"
        :style="{
          padding: 'var(--space-sm) var(--space-md)',
          borderRadius: 'var(--radius-md)',
          marginBottom: adminExpanded ? 'var(--space-xs)' : '0',
          cursor: 'pointer',
          fontSize: '13px',
          color: 'var(--color-text-muted)',
          display: 'flex',
          alignItems: 'center',
          gap: 'var(--space-xs)',
          transition: 'background var(--transition-fast)'
        }"
        @mouseover="e => e.currentTarget.style.background = 'var(--color-surface-hover)'"
        @mouseout="e => e.currentTarget.style.background = 'transparent'"
      >
        <span style="transition: transform 0.2s;" :style="{ transform: adminExpanded ? 'rotate(0deg)' : 'rotate(-90deg)' }">▾</span>
        <span>管理</span>
      </div>
      <template v-if="adminExpanded">
        <div
          v-for="item in adminItems"
          :key="item.path"
          v-show="!item.adminOnly || isAdmin"
          @click="router.push(item.path)"
          :style="{
            padding: 'var(--space-sm) var(--space-md)',
            borderRadius: 'var(--radius-md)',
            marginBottom: 'var(--space-xs)',
            cursor: 'pointer',
            fontSize: '14px',
            color: isNavActive(item.path) ? 'var(--color-primary)' : 'var(--color-text-secondary)',
            background: isNavActive(item.path) ? 'var(--color-primary-soft)' : 'transparent',
            fontWeight: isNavActive(item.path) ? '500' : '400',
            transition: 'background var(--transition-fast), color var(--transition-fast)'
          }"
          @mouseover="e => { if (!isNavActive(item.path)) e.currentTarget.style.background = 'var(--color-surface-hover)' }"
          @mouseout="e => { if (!isNavActive(item.path)) e.currentTarget.style.background = 'transparent' }"
        >
          {{ item.label }}
        </div>
      </template>
    </div>

    <!-- 历史对话 -->
    <div style="padding: var(--space-sm) var(--space-md) var(--space-sm);">
      <div
        @click="historyExpanded = !historyExpanded"
        :style="{
          padding: 'var(--space-sm) var(--space-md)',
          borderRadius: 'var(--radius-md)',
          marginBottom: historyExpanded ? 'var(--space-xs)' : '0',
          cursor: 'pointer',
          fontSize: '13px',
          color: 'var(--color-text-muted)',
          display: 'flex',
          alignItems: 'center',
          gap: 'var(--space-xs)',
          transition: 'background var(--transition-fast)'
        }"
        @mouseover="e => e.currentTarget.style.background = 'var(--color-surface-hover)'"
        @mouseout="e => e.currentTarget.style.background = 'transparent'"
      >
        <span style="transition: transform 0.2s;" :style="{ transform: historyExpanded ? 'rotate(0deg)' : 'rotate(-90deg)' }">▾</span>
        <span>历史对话</span>
      </div>
    </div>

    <div v-if="historyExpanded" style="flex: 1; overflow-y: auto; padding: 0 var(--space-md);">
      <div
        v-for="conv in convs.list"
        :key="conv.id"
        @click="handleSelect(conv)"
        @mouseenter="hoveredId = conv.id"
        @mouseleave="handleMouseLeave(conv)"
        :style="{
          padding: 'var(--space-sm) var(--space-md)',
          borderRadius: 'var(--radius-md)',
          marginBottom: 'var(--space-xs)',
          cursor: 'pointer',
          display: 'flex',
          alignItems: 'center',
          justifyContent: 'space-between',
          background: activeId === conv.id ? 'var(--color-bg)' : 'transparent',
          fontWeight: activeId === conv.id ? '600' : '400',
          fontSize: '14px',
          color: activeId === conv.id ? 'var(--color-primary)' : 'var(--color-text-secondary)'
        }"
        @mouseover="e => e.currentTarget.style.background = activeId === conv.id ? 'var(--color-bg)' : 'var(--color-surface-hover)'"
        @mouseout="e => e.currentTarget.style.background = activeId === conv.id ? 'var(--color-bg)' : 'transparent'"
      >
        <div style="min-width: 0; flex: 1; overflow: hidden; text-overflow: ellipsis; white-space: nowrap;">
          {{ conv.title }}
        </div>
        <div v-if="hoveredId === conv.id" style="display: flex; gap: var(--space-xs); flex-shrink: 0;">
          <button @click.stop="handleRename(conv)" style="padding: var(--space-xs); border: none; background: none; cursor: pointer; border-radius: var(--radius-sm); display: flex; align-items: center; justify-content: center;" @mouseover="e => e.currentTarget.style.background = 'var(--color-surface-hover)'" @mouseout="e => e.currentTarget.style.background = 'none'">
            <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="#666" stroke-width="2">
              <path d="M11 4H4a2 2 0 0 0-2 2v14a2 2 0 0 0 2 2h14a2 2 0 0 0 2-2v-7"/>
              <path d="M18.5 2.5a2.121 2.121 0 0 1 3 3L12 15l-4 1 1-4 9.5-9.5z"/>
            </svg>
          </button>
          <NPopconfirm @positive-click="handleDelete(conv)" @update:show="(v) => confirmingId = v ? conv.id : null">
            <template #trigger>
              <button @click.stop style="padding: var(--space-xs); border: none; background: none; cursor: pointer; border-radius: var(--radius-sm); display: flex; align-items: center; justify-content: center;" @mouseover="e => e.currentTarget.style.background = 'var(--color-surface-hover)'" @mouseout="e => e.currentTarget.style.background = 'none'">
                <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="var(--color-danger)" stroke-width="2">
                  <path d="M3 6h18M19 6v14a2 2 0 0 1-2 2H7a2 2 0 0 1-2-2V6m3 0V4a2 2 0 0 1 2-2h4a2 2 0 0 1 2 2v2"/>
                  <line x1="10" y1="11" x2="10" y2="17"/>
                  <line x1="14" y1="11" x2="14" y2="17"/>
                </svg>
              </button>
            </template>
            确认删除此会话？
          </NPopconfirm>
        </div>
      </div>
      <div v-if="hasMore" style="padding: var(--space-xs) 0; text-align: center;">
        <NButton text size="small" :loading="convs.loading" @click="handleLoadMore" style="color: var(--color-text-muted);">加载更多</NButton>
      </div>
    </div>
  </div>

  <!-- 重命名弹窗 -->
  <NModal v-model:show="renameModalVisible" preset="card" title="重命名会话" style="width: 400px;">
    <NInput v-model:value="renamingTitle" placeholder="请输入新标题" @keyup.enter="confirmRename" />
    <div style="margin-top: var(--space-md); text-align: right;">
      <NButton style="margin-right: var(--space-sm);" @click="renameModalVisible = false">取消</NButton>
      <NButton type="primary" @click="confirmRename">确定</NButton>
    </div>
  </NModal>
</template>
