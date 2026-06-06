<script setup>
import { ref, onMounted } from 'vue'
import { api } from '@/api/client'
import { NButton, NSpin, NEmpty, NTag, useMessage } from 'naive-ui'
import PageTopBar from '@/components/PageTopBar.vue'
import TopBarBtn from '@/components/TopBarBtn.vue'
import ListItemCard from '@/components/ListItemCard.vue'

const message = useMessage()
const items = ref([])
const loading = ref(false)

async function fetchList() {
  loading.value = true
  try {
    const res = await api.get('/api/missions')
    items.value = res.items || []
  } catch (_) {
  } finally {
    loading.value = false
  }
}

const triggering = ref({})
async function triggerCheck(item) {
  triggering.value[item.id] = true
  try {
    await api.post(`/api/missions/${item.id}/check`)
    message.success('任务检查已触发，稍后查看信箱')
  } catch (e) {
    message.error('触发失败: ' + (e.message || ''))
  } finally {
    triggering.value[item.id] = false
  }
}

const discovering = ref(false)
async function triggerDiscover() {
  discovering.value = true
  try {
    await api.post('/api/missions/discover')
    message.success('Mission 发现已触发，稍后刷新查看')
  } catch (e) {
    message.error('触发失败: ' + (e.message || ''))
  } finally {
    discovering.value = false
  }
}

async function handleStatusChange(item, status) {
  try {
    await api.patch(`/api/missions/${item.id}`, { status })
    item.status = status
    const labels = { active: '已激活', paused: '已暂停', completed: '已完成', rejected: '已拒绝' }
    message.success(labels[status] || '操作成功')
  } catch (e) {
    message.error('操作失败: ' + (e.message || ''))
  }
}

function formatTime(ts) {
  if (!ts) return ''
  return new Date(ts).toLocaleString('zh-CN', { month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit' })
}

function statusColor(s) {
  return { active: 'success', paused: 'warning', completed: 'default', rejected: 'error' }[s] || 'default'
}
function statusLabel(s) {
  return { active: '运行中', paused: '已暂停', completed: '已完成', rejected: '已拒绝' }[s] || s
}

function cronLabel(c) {
  return { daily: '每天', weekly: '每周', '2daily': '每两天' }[c] || c
}

function sourceLabel(item) {
  return item.source === 'auto' ? '自动发现' : '手动创建'
}

function buildContent(item) {
  let text = item.goal
  if (item.context) text += '\n\n' + item.context
  if (item.last_summary) text += '\n\n上次检查: ' + item.last_summary
  return text
}

function buildTags(item) {
  return [
    { text: statusLabel(item.status), type: statusColor(item.status) },
    { text: cronLabel(item.check_cron), type: 'default' },
    { text: sourceLabel(item), type: item.source === 'auto' ? 'info' : 'default' },
  ]
}

function buildMetadata(item) {
  let parts = []
  parts.push('检查 ' + (item.check_count || 0) + ' 次')
  if (item.next_check_at) parts.push('下次检查 ' + formatTime(item.next_check_at))
  if (item.last_check_at) parts.push('上次检查 ' + formatTime(item.last_check_at))
  return parts.join(' · ')
}

function buildActions(item) {
  const actions = []
  switch (item.status) {
    case 'active':
      actions.push({ key: 'check', label: '立即检查', onClick: () => triggerCheck(item) })
      actions.push({ key: 'pause', label: '暂停', onClick: () => handleStatusChange(item, 'paused') })
      actions.push({ key: 'complete', label: '完成', confirm: '确定标记为已完成？', onClick: () => handleStatusChange(item, 'completed') })
      break
    case 'paused':
      actions.push({ key: 'resume', label: '恢复', onClick: () => handleStatusChange(item, 'active') })
      actions.push({ key: 'complete', label: '完成', confirm: '确定标记为已完成？', onClick: () => handleStatusChange(item, 'completed') })
      break
    case 'completed':
    case 'rejected':
      actions.push({ key: 'reactivate', label: '重新激活', onClick: () => handleStatusChange(item, 'active') })
      break
  }
  return actions
}

onMounted(fetchList)
</script>

<template>
  <div style="display: flex; flex-direction: column; height: 100vh; background: var(--color-bg-page); min-width: 0;">
    <PageTopBar title="任务追踪" :visible="true">
      <template #right>
        <TopBarBtn label="触发发现" @click="triggerDiscover" />
        <TopBarBtn label="刷新" @click="fetchList" />
      </template>
    </PageTopBar>
    <div style="flex: 1; overflow-y: auto; padding: var(--space-lg);">
      <NSpin v-if="loading && items.length === 0" />
      <NEmpty v-else-if="items.length === 0" description="暂无任务。在对话中告诉助手「帮我盯着...」，或等待系统自动发现。" style="margin-top: 80px;" />
      <div v-else style="display: flex; flex-direction: column; gap: var(--space-md); max-width: 800px; margin: 0 auto;">
        <ListItemCard
          v-for="item in items" :key="item.id"
          :title="item.goal"
          :content="buildContent(item)"
          :tags="buildTags(item)"
          :metadata="buildMetadata(item)"
          :actions="buildActions(item)"
        />
      </div>
    </div>
  </div>
</template>
