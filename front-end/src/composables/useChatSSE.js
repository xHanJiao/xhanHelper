import { useChatStore } from '@/stores/chat'
import { useConversationsStore } from '@/stores/conversations'
import { useAuthStore } from '@/stores/auth'
import { api } from '@/api/client'

// 工具函数名 → 中文显示名（与 iOS 端一致）
const DISPLAY_NAMES = {
  create_bill: '记账',
  update_bill: '修改账单',
  cancel_bill: '作废账单',
  delete_bill: '删除账单',
  query_bills: '查询账单',
  get_monthly_stats: '月度统计',
  get_category_stats: '分类统计',
  get_current_time: '获取时间',
  evaluate: '计算',
  generate: '生成UUID',
  record_note: '记录笔记',
  search: '搜索笔记',
  update_note: '更新笔记',
  delete_note: '删除笔记',
  list_notes: '列出笔记',
  create_agent: '创建智能体',
  update_agent: '更新智能体',
  delete_agent: '删除智能体',
  list_agents: '列出智能体'
}

// 参数 key → 中文标签
const ARG_LABELS = [
  ['amount', '金额'],
  ['item', '项目'],
  ['category1', '分类'],
  ['topic', '主题'],
  ['name', '名称'],
  ['query', '查询']
]

function buildToolSummary(tc, result) {
  const parts = [DISPLAY_NAMES[tc.function] || tc.function]
  for (const [key, label] of ARG_LABELS) {
    const val = tc.args?.[key]
    if (val !== undefined && val !== null && val !== '') {
      const s = typeof val === 'object' ? JSON.stringify(val) : String(val)
      parts.push(`${label}: ${s.length > 20 ? s.slice(0, 20) + '...' : s}`)
    }
  }
  if (result) {
    parts.push(result.success ? '✅ 成功' : `❌ ${result.error || '失败'}`)
  }
  return parts.join(' | ')
}

export function useChatSSE() {
  const chat = useChatStore()
  const convs = useConversationsStore()
  const auth = useAuthStore()
  let abortController = null

  async function sendMessage(message, { conversationId, deepMode, agentId, temperature, maxTokens, thinking } = {}) {
    if (chat.isStreaming) return

    abortController = new AbortController()
    chat.isStreaming = true
    chat.error = null
    chat.statusMessage = null
    chat.streamingMessage = {
      role: 'assistant',
      content: '',
      reasoning: '',
      reasoningDone: false,
      toolCalls: [],
      verifyStatus: null,  // { status: 'checking'|'done'|'retry', retry: 0, reason: '' }
      created_at: new Date().toISOString()
    }
    chat.planNodes = {}
    chat.planEdges = []

    try {
      const body = {
        message,
        conversation_id: conversationId || undefined,
        deep_mode: deepMode || false,
        agent_id: agentId || undefined,
        memory_threshold: 0.5,
        memory_limit: 8
      }
      if (temperature !== undefined && temperature !== null) {
        body.temperature = temperature
      }
      if (maxTokens !== undefined && maxTokens !== null) {
        body.max_tokens = maxTokens
      }
      if (thinking !== undefined && thinking !== null) {
        body.thinking = thinking
      }
      const res = await api.stream('/api/chat', body, abortController.signal)

      if (!res.ok) {
        if (res.status === 401) {
          auth.logout()
          return
        }
        throw new Error(`HTTP ${res.status}`)
      }

      await parseSSEStream(res)
    } catch (err) {
      if (err.name === 'AbortError') return
      if (err.name === 'AuthError') {
        auth.logout()
        return
      }
      console.error('Chat error', err)
      chat.error = err.message || '网络连接失败，请重试'
    } finally {
      chat.isStreaming = false
    }
  }

  async function parseSSEStream(res) {
    const reader = res.body.getReader()
    const decoder = new TextDecoder()
    let buffer = ''
    let currentEvent = ''

    while (true) {
      const { done, value } = await reader.read()
      if (done) break

      buffer += decoder.decode(value, { stream: true })
      const lines = buffer.split('\n')
      buffer = lines.pop() // 保留不完整的行

      for (const line of lines) {
        if (line.startsWith('event: ')) {
          currentEvent = line.slice(7).trim()
        } else if (line.startsWith('data: ')) {
          const raw = line.slice(6)
          try {
            dispatchEvent(currentEvent, raw)
          } catch (e) {
            console.error('SSE dispatch error', currentEvent, e)
          }
        }
        // 忽略注释行（:开头）和空行
      }
    }
  }

  function dispatchEvent(event, rawData) {
    switch (event) {
      case 'message': {
        const parsed = JSON.parse(rawData)
        chat.streamingMessage.content += parsed.delta
        chat.statusMessage = null // 正文开始推送，清除「正在分析」提示
        break
      }
      case 'thought_delta':
        chat.streamingMessage.reasoning += rawData
        break
      case 'thought_done':
        chat.streamingMessage.reasoningDone = true
        break
      case 'tool_call': {
        const tc = JSON.parse(rawData)
        chat.streamingMessage.toolCalls.push({ ...tc, status: 'running', result: null })
        break
      }
      case 'tool_result': {
        const tr = JSON.parse(rawData)
        const last = chat.streamingMessage.toolCalls.findLast(tc => tc.status === 'running')
        if (last) {
          last.status = tr.success ? 'done' : 'failed'
          last.result = tr
        }
        break
      }
      case 'plan_node_start': {
        const node = JSON.parse(rawData)
        chat.planNodes[node.id] = { label: node.label, status: 'running' }
        break
      }
      case 'plan_node_done': {
        const node = JSON.parse(rawData)
        chat.planNodes[node.id] = { label: node.label, status: node.status }
        break
      }
      case 'plan': {
        const graph = JSON.parse(rawData)
        chat.planNodes = {}
        for (const n of graph.nodes || []) {
          chat.planNodes[n.id] = { label: n.label, status: n.status }
        }
        chat.planEdges = graph.edges || []
        break
      }
      case 'verify': {
        try {
          chat.streamingMessage.verifyStatus = JSON.parse(rawData)
        } catch {
          chat.streamingMessage.verifyStatus = { status: 'checking', retry: 0, reason: '' }
        }
        break
      }
      case 'status': {
        try {
          const s = JSON.parse(rawData)
          chat.statusMessage = s.message || s.status || rawData
        } catch {
          chat.statusMessage = rawData
        }
        break
      }
      case 'agent_matched': {
        const am = JSON.parse(rawData)
        chat.agentId = am.agent_id
        chat.agentName = am.agent_name
        chat.llmParams = am.params || null
        break
      }
      case 'done': {
        const d = JSON.parse(rawData)
        const finalMsg = {
          ...chat.streamingMessage,
          id: d.message_id,
          toolCalls: []
        }
        chat.addMessage(finalMsg)
        chat.streamingMessage = null

        // 将前端临时 ID 替换为后端真实数据库 ID
        if (d.user_message_id) {
          const userMsg = chat.messages.findLast(m => m.role === 'user')
          if (userMsg) userMsg.id = d.user_message_id
        }

        // 将关联记忆 ID 挂到用户消息上，并批量获取记忆详情
        if (d.memory_ids && d.memory_ids.length > 0) {
          const userMsg = chat.messages.findLast(m => m.role === 'user')
          if (userMsg) {
            userMsg.memoryIds = d.memory_ids
            api.post('/api/memories/batch', { ids: d.memory_ids }).then(memories => {
              userMsg.associatedMemories = Array.isArray(memories) ? memories : []
            }).catch(err => {
              console.error('Failed to fetch associated memories', err)
            })
          }
        }

        if (!chat.conversationId && d.conversation_id) {
          chat.conversationId = d.conversation_id
          convs.prepend({ id: d.conversation_id, title: d.title || '新对话', agent_id: chat.agentId, agent_name: chat.agentName })
        }
        if (d.title) {
          convs.updateTitle(d.conversation_id, d.title)
        }
        if (d.error) {
          chat.error = d.error
        }
        break
      }
      case 'error':
        chat.error = rawData
        break
    }
  }

  function cancel() {
    if (chat.isStreaming) {
      chat.needsRefetch = true
    }
    abortController?.abort()
  }

  return { sendMessage, cancel }
}

export { buildToolSummary }
