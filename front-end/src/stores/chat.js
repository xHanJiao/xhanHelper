import { defineStore } from 'pinia'
import { ref } from 'vue'

export const useChatStore = defineStore('chat', () => {
  const conversationId = ref(null)
  const agentId = ref(null)
  const agentName = ref(null)
  const llmParams = ref(null)
  const deepMode = ref(true)
  const messages = ref([])
  const streamingMessage = ref(null)
  // 流式中输入的待发消息：只在这份数组里，不写 messages（done 用 findLast(user) 回填真实 id）
  const pendingSends = ref([])
  const queuePaused = ref(false)
  let pendingSeq = 0
  const planNodes = ref({})
  const planEdges = ref([])
  const isStreaming = ref(false)
  const error = ref(null)
  const statusMessage = ref(null)
  const needsRefetch = ref(false)
  // 当前会话模式（work/plan）。新会话时只存在本地，首条消息随 init_mode 落库。
  const mode = ref('work')

  // 消息分页游标
  const oldestCursor = ref(null)
  const hasMoreBefore = ref(false)

  // 设置当前会话
  function setConversation(id) {
    conversationId.value = id
    messages.value = []
    streamingMessage.value = null
    planNodes.value = {}
    planEdges.value = []
    error.value = null
    oldestCursor.value = null
    hasMoreBefore.value = false
  }

  // 添加已固化的消息
  function addMessage(msg) {
    messages.value.push(msg)
  }

  // 设置历史消息
  function setMessages(msgs) {
    messages.value = msgs
  }

  // 流式中入队（entry: { content, imageIds }；agentId 在出队时取当前值）
  function enqueuePendingSend(entry) {
    pendingSends.value.push({
      ...entry,
      id: `pending-${Date.now()}-${++pendingSeq}`,
      created_at: new Date().toISOString()
    })
  }

  function removePendingSend(id) {
    const i = pendingSends.value.findIndex(p => p.id === id)
    if (i >= 0) pendingSends.value.splice(i, 1)
  }

  function takeNextPendingSend() {
    return pendingSends.value.shift() || null
  }

  // 返回清掉的条数，调用方决定要不要 toast
  function clearPendingSends() {
    const n = pendingSends.value.length
    pendingSends.value = []
    queuePaused.value = false
    return n
  }

  // 前置历史消息（用于向上翻页）
  function prependMessages(msgs) {
    messages.value = [...msgs, ...messages.value]
  }

  // 清空
  function reset() {
    conversationId.value = null
    agentId.value = null
    agentName.value = null
    llmParams.value = null
    deepMode.value = true
    messages.value = []
    streamingMessage.value = null
    pendingSends.value = []
    queuePaused.value = false
    planNodes.value = {}
    planEdges.value = []
    isStreaming.value = false
    error.value = null
    statusMessage.value = null
    needsRefetch.value = false
    oldestCursor.value = null
    hasMoreBefore.value = false
    mode.value = 'work'
  }

  return {
    conversationId,
    agentId,
    agentName,
    llmParams,
    deepMode,
    messages,
    streamingMessage,
    pendingSends,
    queuePaused,
    planNodes,
    planEdges,
    isStreaming,
    error,
    statusMessage,
    oldestCursor,
    hasMoreBefore,
    needsRefetch,
    mode,
    setConversation,
    addMessage,
    setMessages,
    prependMessages,
    enqueuePendingSend,
    removePendingSend,
    takeNextPendingSend,
    clearPendingSends,
    reset
  }
})
