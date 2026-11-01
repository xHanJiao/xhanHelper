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
  const planNodes = ref({})
  const planEdges = ref([])
  const isStreaming = ref(false)
  const error = ref(null)
  const statusMessage = ref(null)
  const needsRefetch = ref(false)
  // 当前会话模式（work/plan）与无痕意图。新会话时只存在本地，首条消息随 init_mode/ephemeral 落库。
  const mode = ref('work')
  const ephemeral = ref(false)

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
    planNodes.value = {}
    planEdges.value = []
    isStreaming.value = false
    error.value = null
    statusMessage.value = null
    needsRefetch.value = false
    oldestCursor.value = null
    hasMoreBefore.value = false
    mode.value = 'work'
    ephemeral.value = false
  }

  return {
    conversationId,
    agentId,
    agentName,
    llmParams,
    deepMode,
    messages,
    streamingMessage,
    planNodes,
    planEdges,
    isStreaming,
    error,
    statusMessage,
    oldestCursor,
    hasMoreBefore,
    needsRefetch,
    mode,
    ephemeral,
    setConversation,
    addMessage,
    setMessages,
    prependMessages,
    reset
  }
})
