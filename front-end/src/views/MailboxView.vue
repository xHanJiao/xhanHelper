<script setup>
import { ref, onMounted } from 'vue'
import { useRouter } from 'vue-router'
import { api } from '@/api/client'
import { NCard, NTag, NSpin, NEmpty, useMessage } from 'naive-ui'
import PageTopBar from '@/components/PageTopBar.vue'
import TopBarBtn from '@/components/TopBarBtn.vue'

const router = useRouter()
const navigating = ref(false)
const message = useMessage()
const items = ref([])
const total = ref(0)
const loading = ref(false)
const unreadCount = ref(0)

async function fetchItems() {
  loading.value = true
  try {
    const res = await api.get('/api/mailbox?limit=50')
    items.value = res.items || []
    total.value = res.total || 0
    await fetchUnreadCount()
  } catch (e) {
    message.error('获取信箱失败: ' + (e.message || '未知错误'))
  } finally {
    loading.value = false
  }
}

async function fetchUnreadCount() {
  try {
    const res = await api.get('/api/mailbox/unread-count')
    unreadCount.value = res.count || 0
  } catch (_) {}
}

async function markRead(item) {
  if (item.is_read) return
  try {
    await api.patch(`/api/mailbox/${item.id}/read`)
    item.is_read = true
    unreadCount.value = Math.max(0, unreadCount.value - 1)
  } catch (_) {}
}

async function navigateToChat(item) {
  if (navigating.value) return
  // 无跳转目标（如待办提醒），直接标已读
  if (!item.entity_type || !item.entity_id) {
    await markRead(item)
    return
  }
  navigating.value = true
  try {
    const res = await api.post(`/api/mailbox/${item.id}/chat`)
    if (res.conversation_id) {
      await markRead(item)
      router.push({
        path: `/chat/${res.conversation_id}`,
        query: res.greeting ? { greeting: JSON.stringify(res.greeting) } : {}
      })
    }
  } catch (e) {
    message.error('跳转失败: ' + (e.message || ''))
  } finally {
    navigating.value = false
  }
}

function typeLabel(type) {
  const map = { daily_brief: '日报', theme_finding: '主题发现', candidate_theme: '候选主题', mission_suggest: '任务建议', mission_check: '任务追踪', reminder: '待办提醒' }
  return map[type] || type
}

function typeColor(type) {
  const map = { daily_brief: 'info', theme_finding: 'success', candidate_theme: 'warning', mission_suggest: 'info', mission_check: 'success', reminder: 'warning' }
  return map[type] || 'default'
}

function tagColor(tag) {
  const map = { '候选主题': 'warning', '已采纳': 'success', '已归档': 'default', '已关闭': 'error' }
  return map[tag] || 'default'
}

function formatTime(ts) {
  if (!ts) return ''
  const d = new Date(ts)
  return d.toLocaleString('zh-CN', { month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit' })
}

onMounted(fetchItems)
</script>

<template>
  <div style="display: flex; flex-direction: column; height: 100vh; background: var(--color-bg-page); min-width: 0;">
    <PageTopBar title="信箱" :visible="true">
      <template #right>
        <span v-if="unreadCount > 0" style="font-size: 13px; color: var(--color-primary); font-weight: 500;">
          {{ unreadCount }} 条未读
        </span>
        <TopBarBtn label="刷新" @click="fetchItems" />
      </template>
    </PageTopBar>
    <div style="flex: 1; overflow-y: auto; padding: var(--space-lg);">
      <NSpin v-if="loading && items.length === 0" />
      <NEmpty v-else-if="items.length === 0" description="信箱为空" style="margin-top: 80px;" />
      <div v-else style="display: flex; flex-direction: column; gap: var(--space-md);">
        <NCard
          v-for="item in items" :key="item.id"
          size="small"
          :style="{ opacity: item.is_read ? 0.7 : 1, borderLeft: item.is_read ? '3px solid transparent' : '3px solid var(--color-primary)' }"
          @click="navigateToChat(item)"
        >
          <div style="display: flex; justify-content: space-between; align-items: flex-start; gap: var(--space-md);">
            <div style="flex: 1; min-width: 0;">
              <div style="display: flex; align-items: center; gap: var(--space-sm); margin-bottom: var(--space-xs);">
                <span style="font-weight: 600; font-size: 15px;">{{ item.title }}</span>
                <NTag v-for="(tag, i) in (item.tags && item.tags.length > 0 ? item.tags : [typeLabel(item.type)])" :key="i" :type="item.tags && item.tags.length > 0 ? tagColor(tag) : typeColor(item.type)" size="small">{{ tag }}</NTag>
                <span v-if="!item.is_read" style="width: 8px; height: 8px; border-radius: 50%; background: var(--color-primary);" />
              </div>
              <div style="font-size: 13px; color: var(--color-text-secondary); white-space: pre-wrap; line-height: 1.5;">{{ item.content }}</div>
              <div style="font-size: 11px; color: var(--color-text-muted); margin-top: var(--space-sm);">
                {{ item.source ? '来源: ' + item.source + ' · ' : '' }}{{ formatTime(item.created_at) }}
              </div>
            </div>
          </div>
        </NCard>
      </div>
    </div>
  </div>
</template>
