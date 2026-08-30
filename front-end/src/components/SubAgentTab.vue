<script setup>
import { ref, onMounted, h } from 'vue'
import { useRouter } from 'vue-router'
import { NButton, NCard, NTag, NSpin, NEmpty } from 'naive-ui'
import { api } from '@/api/client'

const router = useRouter()

const rows = ref([])
const loading = ref(false)
const hasMore = ref(false)
const pageSize = 20
const expandedId = ref(null)
const expandedMsgs = ref({})
const msgLoading = ref(false)

const typeLabel = { web_explore: '联网调研', file_explore: '文件检索', user_context: '用户信息' }
const typeColor = { web_explore: 'info', file_explore: 'warning', user_context: 'primary' }
const stateLabel = { completed: '已完成', timeout: '超时', failed: '失败' }
const stateColor = { completed: 'success', timeout: 'warning', failed: 'error' }
const roleLabel = { user: '用户', assistant: '助手', tool: '工具' }
const roleColor = { user: 'info', assistant: 'success', tool: 'default' }

async function fetchList() {
  loading.value = true
  try {
    const offset = rows.value.length
    const res = await api.get(`/api/admin/subagents?limit=${pageSize}&offset=${offset}`)
    rows.value.push(...(res.items || []))
    hasMore.value = !!res.has_more
  } catch (_) {
  } finally {
    loading.value = false
  }
}

async function toggleExpand(row) {
  if (expandedId.value === row.conv_id) {
    expandedId.value = null
    return
  }
  expandedId.value = row.conv_id
  if (!expandedMsgs.value[row.conv_id]) {
    msgLoading.value = true
    try {
      const res = await api.get(`/api/admin/subagents/${row.conv_id}/messages`)
      expandedMsgs.value[row.conv_id] = res
    } catch (_) {
      expandedMsgs.value[row.conv_id] = { messages: [], first_user_msg_id: '' }
    } finally {
      msgLoading.value = false
    }
  }
}

function fmtTokens(n) {
  if (n == null) return null
  return n.toLocaleString()
}

function fmtTime(ts) {
  if (!ts) return '-'
  const d = new Date(ts)
  const p = n => String(n).padStart(2, '0')
  return `${d.getFullYear()}-${p(d.getMonth() + 1)}-${p(d.getDate())} ${p(d.getHours())}:${p(d.getMinutes())}`
}

function parseToolCalls(raw) {
  if (!raw) return []
  try {
    const tcs = typeof raw === 'string' ? JSON.parse(raw) : raw
    return (tcs || []).map(tc => tc.function?.name || '(无函数名)')
  } catch {
    return []
  }
}

function debugAnchor(res) {
  if (res?.first_user_msg_id) return res.first_user_msg_id
  return res?.messages?.find(m => m.role === 'user')?.id || ''
}

function openDebug(row) {
  const anchor = debugAnchor(expandedMsgs.value[row.conv_id])
  if (!anchor) return
  router.push(`/admin/message-debug/${anchor}`)
}

onMounted(fetchList)
</script>

<template>
  <div>
    <div style="display: flex; justify-content: space-between; align-items: center; margin-bottom: var(--space-sm);">
      <span style="font-size: 12px; color: var(--color-text-muted);">已加载 {{ rows.length }} 条 · {{ hasMore ? '可继续加载' : '已全部' }}</span>
      <NButton size="small" @click="rows = []; fetchList()" :loading="loading">刷新</NButton>
    </div>

    <NSpin :show="loading && rows.length === 0">
      <NEmpty v-if="!loading && rows.length === 0" description="暂无子任务记录" style="margin-top: 80px;" />
      <div v-else style="display: flex; flex-direction: column; gap: var(--space-sm);">
        <NCard v-for="row in rows" :key="row.conv_id" size="small" :hoverable="true" style="cursor: pointer;" @click="toggleExpand(row)">
          <div style="display: flex; align-items: center; gap: var(--space-sm); flex-wrap: wrap;">
            <NTag v-if="row.subagent_type" size="small" :type="typeColor[row.subagent_type] || 'default'">{{ typeLabel[row.subagent_type] || row.subagent_type }}</NTag>
            <NTag v-if="row.result?.state" size="small" :type="stateColor[row.result.state] || 'default'">{{ stateLabel[row.result.state] || row.result.state }}</NTag>
            <span style="font-weight: 600; font-size: 14px; min-width: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap;">{{ row.title || '(无标题)' }}</span>
            <span style="font-size: 11px; color: var(--color-text-muted);">{{ fmtTime(row.created_at) }}</span>
          </div>
          <div style="display: flex; align-items: center; gap: var(--space-md); margin-top: 4px; font-size: 12px; color: var(--color-text-muted); flex-wrap: wrap;">
            <span>{{ row.msg_count }} 条消息</span>
            <span v-if="row.cost?.total_tokens">token {{ fmtTokens(row.cost.total_tokens) }}（{{ row.cost.call_count }} 次调用）</span>
            <span v-if="row.parent_conv_id" style="font-family: monospace;">父会话 {{ row.parent_conv_id.slice(0, 8) }}...</span>
            <span v-if="row.result?.state === 'failed' || row.result?.state === 'timeout'" style="color: var(--color-danger); margin-left: auto; max-width: 50%; overflow: hidden; text-overflow: ellipsis; white-space: nowrap;" :title="row.result?.error">{{ row.result?.error }}</span>
            <span v-else style="margin-left: auto;">{{ expandedId === row.conv_id ? '▼' : '▶' }}</span>
          </div>

          <!-- 展开的消息区 -->
          <div v-if="expandedId === row.conv_id" style="margin-top: var(--space-md);" @click.stop>
            <div style="display: flex; gap: var(--space-sm); align-items: center; margin-bottom: var(--space-sm);">
              <NButton size="tiny" type="primary" @click="openDebug(row)">msgDebug 调试</NButton>
              <span style="font-size: 11px; color: var(--color-text-muted);">以该子任务首条用户消息为锚点</span>
            </div>
            <NSpin :show="msgLoading">
              <div v-if="expandedMsgs[row.conv_id]?.messages?.length" style="display: flex; flex-direction: column; gap: 6px; max-height: 60vh; overflow-y: auto;">
                <div v-for="m in expandedMsgs[row.conv_id].messages" :key="m.id" style="flex-shrink: 0; border: 1px solid var(--color-border); border-radius: var(--radius-md); overflow: hidden;">
                  <div style="display: flex; align-items: center; gap: 8px; padding: 4px 10px; background: var(--color-bg-page); border-bottom: 1px solid var(--color-border);">
                    <span style="font-size: 11px; padding: 1px 6px; border-radius: 4px; background: var(--color-primary); color: #fff;">{{ roleLabel[m.role] || m.role }}</span>
                    <span v-if="m.tool_calls_json" style="font-size: 11px; color: var(--color-text-muted);">调用: {{ parseToolCalls(m.tool_calls_json).join(', ') }}</span>
                    <span v-if="m.role === 'tool' && m.tool_call_id" style="font-size: 11px; color: var(--color-text-muted); font-family: monospace;">{{ m.tool_call_id.slice(0, 12) }}...</span>
                    <span style="font-size: 11px; color: var(--color-text-muted); margin-left: auto;">{{ fmtTime(m.created_at) }}</span>
                  </div>
                  <pre style="max-height: 300px; overflow-y: auto; white-space: pre-wrap; word-break: break-all; margin: 0; padding: 8px 10px; font-size: 12px; line-height: 1.5; color: var(--color-text-secondary);">{{ m.content || (m.role === 'assistant' && m.tool_calls_json ? '(纯工具调用)' : '(空)') }}</pre>
                </div>
              </div>
              <div v-else style="color: var(--color-text-muted); padding: 12px; font-size: 12px;">暂无消息</div>
            </NSpin>
          </div>
        </NCard>

        <NButton v-if="hasMore" size="small" block @click="fetchList()" :loading="loading">加载更多</NButton>
      </div>
    </NSpin>
  </div>
</template>
