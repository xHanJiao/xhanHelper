import { useChatStore } from '@/stores/chat'
import { useConversationsStore } from '@/stores/conversations'
import { useAuthStore } from '@/stores/auth'
import { api } from '@/api/client'

export function useChatSSE() {
  const chat = useChatStore()
  const convs = useConversationsStore()
  const auth = useAuthStore()
  let abortController = null

  async function sendMessage(message, { conversationId, deepMode, agentId } = {}) {
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
      toolCalls: []
    }
    chat.planNodes = {}
    chat.planEdges = []

    try {
      const res = await api.stream('/api/chat', {
        message,
        conversation_id: conversationId || undefined,
        deep_mode: deepMode || false,
        agent_id: agentId || undefined,
        memory_threshold: 0.5,
        memory_limit: 8
      }, abortController.signal)

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
          dispatchEvent(currentEvent, raw)
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
      case 'status': {
        const s = JSON.parse(rawData)
        chat.statusMessage = s.message
        break
      }
      case 'agent_matched': {
        const am = JSON.parse(rawData)
        chat.agentId = am.agent_id
        chat.agentName = am.agent_name
        break
      }
      case 'done': {
        const d = JSON.parse(rawData)
        const finalMsg = {
          ...chat.streamingMessage,
          id: d.message_id
        }
        chat.addMessage(finalMsg)
        chat.streamingMessage = null

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
