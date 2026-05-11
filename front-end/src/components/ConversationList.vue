<script setup>
import { ref, computed, watch } from 'vue'
import { useRouter, useRoute } from 'vue-router'
import { useConversationsStore } from '@/stores/conversations'
import { useChatStore } from '@/stores/chat'
import { useAuthStore } from '@/stores/auth'
import { api } from '@/api/client'
import { useThemeStore } from '@/stores/theme'
import { NButton, NPopconfirm, NModal, NInput, useMessage } from 'naive-ui'

const isDev = import.meta.env.DEV

const router = useRouter()
const route = useRoute()
const convs = useConversationsStore()
const chat = useChatStore()
const theme = useThemeStore()
const message = useMessage()

const renameModalVisible = ref(false)
const renamingId = ref(null)
const renamingTitle = ref('')
const hoveredId = ref(null)
const extractingId = ref(null)
const confirmingId = ref(null)

const auth = useAuthStore()

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

const navItems = [
  { label: '智能体', path: '/agents' },
  { label: '记忆', path: '/memories' },
  { label: '笔记', path: '/notes' },
  { label: '提示词', path: '/prompts' }
]

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

async function handleExtractMemories(conv) {
  extractingId.value = conv.id
  try {
    const res = await api.post(`/api/conversations/${conv.id}/extract-memories`, {})
    if (res.status === 'extracting') {
      message.success('已触发记忆提取，后台执行中')
    } else {
      message.success('记忆提取完成')
    }
  } catch (e) {
    message.error('记忆提取失败')
  } finally {
    extractingId.value = null
  }
}
</script>

<template>
  <div style="width: 280px; background: var(--color-surface); border-right: 1px solid var(--color-border); display: flex; flex-direction: column; height: 100%;">
    <!-- 顶部：用户信息和新对话按钮 -->
    <div style="padding: var(--space-lg) var(--space-md);">
      <!-- 用户头像 -->
      <div style="display: flex; align-items: center; gap: var(--space-md); margin-bottom: var(--space-md);">
        <div style="width: 48px; height: 48px; border-radius: 50%; background: linear-gradient(135deg, #667eea 0%, #764ba2 100%); display: flex; align-items: center; justify-content: center; color: #fff; font-weight: bold; font-size: 18px;">我</div>
        <div style="font-size: 18px; font-weight: 600;">xhanHelper</div>
        <button @click="theme.toggle()" style="margin-left: auto; padding: var(--space-xs); border: none; background: none; cursor: pointer; border-radius: var(--radius-sm); font-size: 18px;" :title="theme.isDark ? '切到亮色' : '切到暗色'">
          {{ theme.isDark ? '☀️' : '🌙' }}
        </button>
      </div>

      <!-- 新对话按钮 -->
      <button @click="handleNewConversation" style="width: 100%; padding: var(--space-md) var(--space-md); border: none; border-radius: var(--radius-lg); background: linear-gradient(135deg, #667eea 0%, #764ba2 100%); color: #fff; font-size: 15px; font-weight: 600; cursor: pointer; display: flex; align-items: center; gap: var(--space-md);">
        <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
          <path d="M12 5v14M5 12h14"/>
        </svg>
        新对话
      </button>
    </div>

    <!-- 导航区 -->
    <div style="padding: 0 var(--space-md) var(--space-md);">
      <div
        v-for="item in navItems"
        :key="item.path"
        @click="router.push(item.path)"
        :style="{
          padding: 'var(--space-md) var(--space-md)',
          borderRadius: 'var(--radius-md)',
          marginBottom: 'var(--space-xs)',
          cursor: 'pointer',
          fontSize: '15px',
          color: isNavActive(item.path) ? '#667eea' : 'var(--color-text)',
          background: isNavActive(item.path) ? '#f0f0ff' : 'transparent',
          fontWeight: isNavActive(item.path) ? '600' : '400'
        }"
        @mouseover="e => { if (!isNavActive(item.path)) e.currentTarget.style.background = 'var(--color-surface-hover)' }"
        @mouseout="e => { if (!isNavActive(item.path)) e.currentTarget.style.background = 'transparent' }"
      >
        {{ item.label }}
      </div>
    </div>

    <!-- 历史对话标题 -->
    <div style="padding: 0 var(--space-lg) var(--space-md); color: var(--color-text-muted); font-size: 14px; font-weight: 600;">历史对话</div>

    <!-- 对话列表 -->
    <div style="flex: 1; overflow-y: auto; padding: 0 var(--space-md);">
      <div
        v-for="conv in convs.list"
        :key="conv.id"
        @click="handleSelect(conv)"
        @mouseenter="hoveredId = conv.id"
        @mouseleave="handleMouseLeave(conv)"
        :style="{
          padding: 'var(--space-md) var(--space-md)',
          borderRadius: 'var(--radius-md)',
          marginBottom: 'var(--space-xs)',
          cursor: 'pointer',
          display: 'flex',
          alignItems: 'center',
          justifyContent: 'space-between',
          background: activeId === conv.id ? 'var(--color-bg)' : 'transparent',
          fontWeight: activeId === conv.id ? '600' : '400'
        }"
        @mouseover="e => e.currentTarget.style.background = activeId === conv.id ? 'var(--color-bg)' : 'var(--color-surface-hover)'"
        @mouseout="e => e.currentTarget.style.background = activeId === conv.id ? 'var(--color-bg)' : 'transparent'"
      >
        <div style="display: flex; align-items: center; gap: var(--space-md); min-width: 0; flex: 1;">
          <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="#666" stroke-width="2">
            <path d="M21 15a2 2 0 0 1-2 2H7l-4 4V5a2 2 0 0 1 2-2h14a2 2 0 0 1 2 2z"/>
          </svg>
          <div style="overflow: hidden; text-overflow: ellipsis; white-space: nowrap; font-size: 15px; color: var(--color-text);">{{ conv.title }}</div>
        </div>

        <!-- 操作按钮 - hover 显示 -->
        <div v-if="hoveredId === conv.id" style="display: flex; gap: var(--space-xs); flex-shrink: 0;">
          <button @click.stop="handleRename(conv)" style="padding: var(--space-xs); border: none; background: none; cursor: pointer; border-radius: var(--radius-sm); display: flex; align-items: center; justify-content: center;" @mouseover="e => e.currentTarget.style.background = 'var(--color-surface-hover)'" @mouseout="e => e.currentTarget.style.background = 'none'">
            <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="#666" stroke-width="2">
              <path d="M11 4H4a2 2 0 0 0-2 2v14a2 2 0 0 0 2 2h14a2 2 0 0 0 2-2v-7"/>
              <path d="M18.5 2.5a2.121 2.121 0 0 1 3 3L12 15l-4 1 1-4 9.5-9.5z"/>
            </svg>
          </button>
          <button v-if="isDev" @click.stop="handleExtractMemories(conv)" :disabled="extractingId === conv.id" style="padding: var(--space-xs); border: none; background: none; cursor: pointer; border-radius: var(--radius-sm); display: flex; align-items: center; justify-content: center;" @mouseover="e => e.currentTarget.style.background = 'var(--color-surface-hover)'" @mouseout="e => e.currentTarget.style.background = 'none'">
            <svg width="16" height="16" viewBox="0 0 24 24" fill="none" :stroke="extractingId === conv.id ? '#999' : '#666'" stroke-width="2">
              <path d="M12 2a5 5 0 0 0-5 5v1a6 6 0 0 0-2 4.5V14h14v-1.5a6 6 0 0 0-2-4.5V7a5 5 0 0 0-5-5z"/>
              <path d="M10 19v1a2 2 0 0 0 4 0v-1"/>
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
    </div>

    <div v-if="hasMore" style="padding: var(--space-xs) var(--space-md) var(--space-md); text-align: center;">
      <NButton text size="small" :loading="convs.loading" @click="handleLoadMore" style="color: var(--color-text-muted);">
        加载更多
      </NButton>
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
