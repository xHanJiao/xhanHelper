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

const navExpanded = ref(true)
const userInfoExpanded = ref(true)
const adminExpanded = ref(true)
const historyExpanded = ref(true)

const navItems = [
  { label: '账单', path: '/bills' },
  { label: '智能体', path: '/agents' },
  { label: '信箱', path: '/mailbox' },
  { label: '文档', path: '/documents' }
]

const userInfoItems = [
  { label: '记忆', path: '/memories' },
  { label: '笔记', path: '/notes' },
  { label: '主题', path: '/themes' }
]

const adminItems = [
  { label: '监控看板', path: '/admin' }
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

function handleNewConversation() {
  if (route.path === '/chat' && !route.params.id) {
    chat.reset()
  }
  router.push('/chat')
}

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
          @click="handleNewConversation"
          style="padding: var(--space-sm) var(--space-md); border-radius: var(--radius-md); margin-bottom: var(--space-xs); cursor: pointer; font-size: 14px; color: var(--color-text-secondary); font-weight: 400; display: flex; align-items: center;"
          @mouseover="e => e.currentTarget.style.background = 'var(--color-surface-hover)'"
          @mouseout="e => e.currentTarget.style.background = 'transparent'"
        >新对话</div>
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

    <!-- 用户信息管理 -->
    <div style="padding: var(--space-sm) var(--space-md) var(--space-sm);">
      <div
        @click="userInfoExpanded = !userInfoExpanded"
        :style="{
          padding: 'var(--space-sm) var(--space-md)',
          borderRadius: 'var(--radius-md)',
          marginBottom: userInfoExpanded ? 'var(--space-xs)' : '0',
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
        <span style="transition: transform 0.2s;" :style="{ transform: userInfoExpanded ? 'rotate(0deg)' : 'rotate(-90deg)' }">▾</span>
        <span>用户信息管理</span>
      </div>
      <template v-if="userInfoExpanded">
        <div
          v-for="item in userInfoItems"
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
        </div>
      </template>
    </div>

    <!-- 管理区（仅管理员可见） -->
    <div v-if="isAdmin" style="padding: var(--space-sm) var(--space-md) var(--space-sm);">
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
        <span>系统监控</span>
      </div>
      <template v-if="adminExpanded">
        <div
          v-for="item in adminItems"
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
          <span v-if="conv.theme_title" title="关联主题: {{ conv.theme_title }}" style="cursor: default;"> 📝</span>
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
