import { useRouter } from 'vue-router'
import { useChatStore } from '@/stores/chat'
import { useConversationsStore } from '@/stores/conversations'
import { useAuthStore } from '@/stores/auth'
import { api } from '@/api/client'

// 旧格式兜底映射（新工具由后端 FillDisplay 下发，勿在此新增条目）
const DISPLAY_NAMES = {
  create_bill: '记账', update_bill: '修改账单', cancel_bill: '作废账单',
  delete_bill: '删除账单', query_bills: '查询账单', get_monthly_stats: '月度统计',
  get_category_stats: '分类统计', get_current_time: '获取时间', evaluate: '计算',
  generate: '生成UUID', record_memory: '记录记忆', search: '搜索笔记',
  update_note: '更新笔记', delete_note: '删除笔记', list_notes: '列出笔记',
  create_agent: '创建智能体', update_agent: '更新智能体', delete_agent: '删除智能体',
  list_agents: '列出智能体'
}
function buildToolSummary(tc, result) {
  const display = tc.display || DISPLAY_NAMES[tc.function] || tc.function
  if (result) {
    return `${display} ${result.success ? '✅' : `❌ ${result.error || '失败'}`}`
  }
  return display
}

export function useChatSSE() {
  const chat = useChatStore()
  const convs = useConversationsStore()
  const auth = useAuthStore()
  const router = useRouter()
  let abortController = null
  // failStream 本次收尾的消息 id：done 只认它，避免把真实 id/灵阅卡片
  // 补到上一轮「停止」遗留的 local- 消息上（那条永远不会有真实 id）。
  let failedLocalId = null

  // 流式失败时别把气泡留在「流式中」：有正文或思考就收尾成普通消息，再露出可重试的错误块。
  // 服务端随后仍会发 done（带本轮灵阅卡片），由 done 分支补到这条本地消息上。
  function failStream(message) {
    const partial = chat.streamingMessage
    failedLocalId = null
    if (partial?.content || partial?.reasoning) {
      failedLocalId = `local-${Date.now()}`
      chat.addMessage({ ...partial, id: failedLocalId, toolCalls: [] })
    }
    chat.streamingMessage = null
    chat.statusMessage = null
    chat.error = message
  }

  async function sendMessage(message, { conversationId, deepMode, agentId, temperature, maxTokens, thinking, attachments, initMode, ephemeral } = {}) {
    if (chat.isStreaming) return

    // 新会话在 done 回传 id 后要入会话列表，用发送这一刻的模式快照（流式中途再点开关不该改写已落库的值）
    const createdMode = initMode || 'work'
    const createdEphemeral = !!ephemeral

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
        // 空串而不是 undefined：undefined 会被 JSON.stringify 丢掉，服务端就按「老入口」落默认会话
        conversation_id: conversationId ?? '',
        deep_mode: deepMode || false,
        agent_id: agentId || undefined
      }
      if (attachments && attachments.length) body.attachments = attachments
      // 仅新会话生效：服务端对已有会话忽略这两个字段
      if (initMode) body.init_mode = initMode
      if (ephemeral) body.ephemeral = true
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
      failStream(err.message || '网络连接失败，请重试')
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
        // 新服务端带 tool_call_id：按 id 配对，别赌事件顺序（客户端工具后发会错位）。
        // 旧服务端没有该字段 → 回退「最后一条 running」。
        const matchByID = tr.tool_call_id
          ? chat.streamingMessage.toolCalls.find(tc => tc.id === tr.tool_call_id && tc.status === 'running')
          : null
        const last = matchByID || chat.streamingMessage.toolCalls.findLast(tc => tc.status === 'running')
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
        // 服务端可能「先 error 再 done」（收尾轮未收敛 / 另一台设备停止），
        // failStream 已经把半截消息收尾了；这时只更新元数据，别再推一条空消息。
        if (chat.streamingMessage) {
          const finalMsg = {
            ...chat.streamingMessage,
            id: d.message_id,
            toolCalls: []
          }
          // 本轮产出的灵阅卡片：done 直接带标题摘要，不用等下一次拉消息列表
          if (Array.isArray(d.artifacts) && d.artifacts.length) {
            finalMsg.artifacts = d.artifacts
          }
          chat.addMessage(finalMsg)
        } else {
          // failStream 已把半截消息收尾成普通消息：卡片和真实 id 补到它上面，
          // 否则本轮灵阅卡片要等重新拉消息列表才出来。
          const partial = failedLocalId ? chat.messages.find(m => m.id === failedLocalId) : null
          if (partial) {
            if (d.message_id) partial.id = d.message_id
            if (Array.isArray(d.artifacts) && d.artifacts.length) partial.artifacts = d.artifacts
          }
        }
        failedLocalId = null
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
          // 会话懒创建后让路由跟上：否则 URL 停在 /chat，顶栏「新对话」推到同一路由等于没点
          router.replace(`/chat/${d.conversation_id}`)
          convs.prepend({
            id: d.conversation_id,
            title: d.title || '新对话',
            agent_id: chat.agentId,
            agent_name: chat.agentName,
            mode: createdMode,
            ephemeral: createdEphemeral
          })
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
        failStream(rawData)
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
