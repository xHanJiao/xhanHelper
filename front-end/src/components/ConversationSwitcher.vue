<script setup>
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { NInput, useMessage } from 'naive-ui'
import { useConversationsStore } from '@/stores/conversations'
import { useChatStore } from '@/stores/chat'
import { api } from '@/api/client'
import { useMobile } from '@/composables/useMobile'

const route = useRoute()
const router = useRouter()
const convs = useConversationsStore()
const chat = useChatStore()
const message = useMessage()
const { isMobile } = useMobile()

const open = ref(false)
const keyword = ref('')
const rootRef = ref(null)

const currentId = computed(() => route.params.id || '')
const current = computed(() => convs.list.find(c => c.id === currentId.value))
const label = computed(() => current.value?.title || (currentId.value ? '对话' : '新对话'))
const hasMore = computed(() => convs.list.length < convs.total)
const filtered = computed(() => {
  const q = keyword.value.trim().toLowerCase()
  if (!q) return convs.list
  return convs.list.filter(c => (c.title || '').toLowerCase().includes(q))
})

function formatTime(value) {
  if (!value) return ''
  const d = new Date(value)
  if (Number.isNaN(d.getTime())) return ''
  const now = new Date()
  const sameDay = d.getFullYear() === now.getFullYear() && d.getMonth() === now.getMonth() && d.getDate() === now.getDate()
  return sameDay ? d.toLocaleTimeString('zh-CN', { hour: '2-digit', minute: '2-digit' }) : d.toLocaleDateString('zh-CN', { month: '2-digit', day: '2-digit' })
}

async function refresh() {
  if (!convs.list.length) await convs.fetchList()
}

function toggle() {
  open.value = !open.value
  if (open.value) refresh()
}

function select(conv) {
  open.value = false
  if (route.params.id !== conv.id) router.push(`/chat/${conv.id}`)
}

async function createConversation() {
  try {
    const res = await api.post('/api/conversations', {})
    chat.reset()
    convs.prepend({ id: res.id, title: res.title, agent_id: res.agent_id, agent_name: res.agent_name })
    open.value = false
    router.push(`/chat/${res.id}`)
  } catch (e) {
    message.error('创建对话失败')
  }
}

async function pin(conv) {
  const res = await convs.togglePin(conv.id, !conv.pinned)
  if (!res.success) message.error(res.message)
}

async function rename(conv) {
  const title = window.prompt('重命名会话', conv.title || '')
  if (title === null) return
  const next = title.trim()
  if (!next) return
  const res = await convs.rename(conv.id, next)
  if (!res.success) message.error(res.message)
}

async function archive(conv) {
  if (!window.confirm(`归档对话「${conv.title || '未命名'}」？归档后可在设置里的已归档会话中恢复。`)) return
  const res = await convs.remove(conv.id)
  if (!res.success) {
    message.error(res.message)
    return
  }
  if (currentId.value === conv.id) {
    chat.reset()
    router.push('/chat')
  }
}

function onDocClick(e) {
  if (!open.value) return
  if (rootRef.value && !rootRef.value.contains(e.target)) open.value = false
}

onMounted(() => {
  document.addEventListener('click', onDocClick)
  refresh()
})
onBeforeUnmount(() => document.removeEventListener('click', onDocClick))
watch(() => route.fullPath, () => { open.value = false })
</script>

<template>
  <div ref="rootRef" class="conv-switcher">
    <button class="conv-pill" type="button" @click.stop="toggle">
      <span class="conv-title">{{ label }}</span>
      <svg class="chev" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round"><path d="M6 9l6 6 6-6"/></svg>
    </button>

    <div v-if="open" class="conv-panel" :class="{ mobile: isMobile }" @click.stop>
      <div class="conv-search">
        <NInput v-model:value="keyword" size="small" clearable placeholder="搜索会话">
          <template #prefix>
            <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" style="width:14px;height:14px"><circle cx="11" cy="11" r="7"/><path d="M20 20l-4-4"/></svg>
          </template>
        </NInput>
      </div>

      <button class="conv-new" type="button" @click="createConversation">
        <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" style="width:15px;height:15px"><path d="M12 5v14M5 12h14"/></svg>
        新对话
      </button>

      <div class="conv-list">
        <div
          v-for="conv in filtered"
          :key="conv.id"
          class="conv-row"
          :class="{ active: conv.id === currentId }"
        >
          <div class="conv-main" @click="select(conv)">
            <span class="conv-name">{{ conv.title || '未命名对话' }}</span>
            <span class="conv-time">{{ formatTime(conv.updated_at) }}</span>
          </div>
          <div class="conv-actions">
            <button type="button" :title="conv.pinned ? '取消置顶' : '置顶'" @click="pin(conv)">{{ conv.pinned ? '↓' : '↑' }}</button>
            <button type="button" title="重命名" @click="rename(conv)">✎</button>
            <button type="button" title="归档" @click="archive(conv)">▣</button>
          </div>
        </div>
        <div v-if="!filtered.length" class="conv-empty">没有会话</div>
      </div>

      <div class="conv-foot">
        <button v-if="hasMore" type="button" @click="convs.fetchList(convs.list.length)">加载更多</button>
        <span>已归档会话在设置里</span>
      </div>
    </div>
  </div>
</template>

<style scoped>
.conv-switcher {
  position: relative;
  min-width: 0;
}
.conv-pill {
  height: 34px;
  max-width: 340px;
  padding: 0 12px 0 14px;
  border-radius: 999px;
  border: 0.5px solid var(--color-border);
  background: rgba(255, 255, 255, 0.36);
  display: inline-flex;
  align-items: center;
  gap: 8px;
  cursor: pointer;
  font-size: 13.5px;
  font-weight: 550;
  color: var(--color-text);
  transition: background var(--transition-fast), border-color var(--transition-fast);
}
[data-theme="dark"] .conv-pill {
  background: rgba(255, 255, 255, 0.05);
}
.conv-pill:hover {
  background: var(--color-primary-soft);
  border-color: var(--color-border-strong);
}
.conv-title {
  min-width: 0;
  max-width: 260px;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}
.chev {
  width: 14px;
  height: 14px;
  opacity: 0.45;
  flex: 0 0 auto;
}
.conv-panel {
  position: absolute;
  top: calc(100% + 8px);
  left: 0;
  width: 380px;
  max-width: calc(100vw - 32px);
  padding: 10px;
  border-radius: 14px;
  background: rgba(252, 248, 240, 0.96);
  border: 0.5px solid var(--color-border-strong);
  box-shadow: var(--shadow-lg);
  backdrop-filter: blur(18px) saturate(1.1);
  -webkit-backdrop-filter: blur(18px) saturate(1.1);
  z-index: 800;
}
[data-theme="dark"] .conv-panel {
  background: rgba(38, 38, 40, 0.96);
}
.conv-panel.mobile {
  position: fixed;
  top: calc(var(--safe-top) + 54px);
  left: 12px;
  right: 12px;
  width: auto;
  max-width: none;
  max-height: calc(100dvh - var(--safe-top) - 90px);
  overflow-y: auto;
}
.conv-search { margin-bottom: 7px; }
.conv-new {
  width: 100%;
  display: flex;
  align-items: center;
  gap: 8px;
  border: 0;
  background: transparent;
  border-radius: 9px;
  padding: 9px 10px;
  font-size: 13px;
  font-weight: 600;
  color: var(--color-primary);
  cursor: pointer;
  text-align: left;
}
.conv-new:hover { background: var(--color-primary-soft); }
.conv-list { max-height: 320px; overflow-y: auto; }
.conv-row {
  display: flex;
  align-items: center;
  gap: 4px;
  border-radius: 9px;
  padding: 2px 4px 2px 0;
}
.conv-row:hover,
.conv-row.active { background: var(--color-primary-soft); }
.conv-main {
  flex: 1;
  min-width: 0;
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 8px 8px 8px 10px;
  cursor: pointer;
}
.conv-name {
  flex: 1;
  min-width: 0;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
  font-size: 12.5px;
}
.conv-time {
  font-size: 10px;
  color: var(--color-text-muted);
  flex: 0 0 auto;
}
.conv-actions {
  display: none;
  gap: 1px;
  flex: 0 0 auto;
}
.conv-row:hover .conv-actions { display: flex; }
.conv-actions button {
  border: 0;
  background: transparent;
  color: var(--color-text-muted);
  cursor: pointer;
  width: 24px;
  height: 24px;
  border-radius: 6px;
  font-size: 12px;
}
.conv-actions button:hover {
  background: rgba(0, 0, 0, 0.06);
  color: var(--color-primary);
}
[data-theme="dark"] .conv-actions button:hover { background: rgba(255, 255, 255, 0.08); }
.conv-empty {
  text-align: center;
  padding: 26px 0;
  font-size: 12px;
  color: var(--color-text-muted);
}
.conv-foot {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
  padding: 8px 4px 2px;
  border-top: 0.5px solid var(--color-border);
  margin-top: 7px;
  font-size: 10.5px;
  color: var(--color-text-muted);
}
.conv-foot button {
  border: 0;
  background: transparent;
  color: var(--color-primary);
  cursor: pointer;
  padding: 0;
  font-size: 10.5px;
}
</style>
