<script setup>
import { ref, computed, watch, nextTick, onBeforeUnmount } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { useChatStore } from '@/stores/chat'
import { useConversationsStore } from '@/stores/conversations'
import { useChatSSE } from '@/composables/useChatSSE'
import { useScrollTopBar } from '@/composables/useScrollTopBar'
import { api } from '@/api/client'
import {
  NInput,
  NButton,
  useMessage
} from 'naive-ui'
import MessageBubble from '@/components/MessageBubble.vue'
import PlanGraph from '@/components/PlanGraph.vue'
import PageTopBar from '@/components/PageTopBar.vue'
import { useMobile } from '@/composables/useMobile'

const route = useRoute()
const router = useRouter()
const chat = useChatStore()
const convs = useConversationsStore()
const { sendMessage, cancel } = useChatSSE()
const nMessage = useMessage()
const { isMobile } = useMobile()

const inputValue = ref('')
const messagesEndRef = ref(null)
const streamingBubbleRef = ref(null)
const isLoadingHistory = ref(false)
const isFirstLoad = ref(true)
const messagesContainerRef = ref(null)
const isInitialScrollDone = ref(false)

const { isBarVisible, showBar } = useScrollTopBar(messagesContainerRef)

const currentTitle = computed(() => {
  const conv = convs.list.find(c => c.id === chat.conversationId)
  return conv?.title || '新对话'
})

const planVisible = computed(() => {
  return Object.keys(chat.planNodes).length >= 2
})

function scrollToBottom() {
  nextTick(() => {
    if (chat.isStreaming && streamingBubbleRef.value) {
      streamingBubbleRef.value.scrollIntoView({ block: 'start', behavior: 'smooth' })
    } else {
      messagesEndRef.value?.scrollIntoView({ behavior: 'smooth' })
    }
  })
}

async function loadHistory(convId, direction = 'before', cursor = null) {
  if (!convId) return
  isLoadingHistory.value = true
  try {
    let url = `/api/conversations/${convId}/messages-v2?limit=20`
    if (cursor) {
      url += `&cursor=${cursor}&direction=${direction}`
    }
    const res = await api.get(url)
    if (res) {
      const msgs = (res.messages || []).map(normalizeMessage)
      if (direction === 'before' && cursor) {
        // 在prepend之前保存当前滚动高度和scrollTop
        const scrollContainer = messagesContainerRef.value
        const oldScrollHeight = scrollContainer.scrollHeight
        const oldScrollTop = scrollContainer.scrollTop

        chat.prependMessages(msgs)

        // 在下一帧调整滚动位置，确保用户看到的内容不跳动
        await nextTick()
        const newScrollHeight = scrollContainer.scrollHeight
        // 保持原来的相对位置：新scrollTop = 旧scrollTop + (新高度 - 旧高度)
        scrollContainer.scrollTop = oldScrollTop + (newScrollHeight - oldScrollHeight)
      } else {
        chat.setMessages(msgs)
      }
      chat.oldestCursor = res.oldest_cursor
      chat.hasMoreBefore = res.has_more_before
    }
  } catch (e) {
    console.error('Failed to load history', e)
    nMessage.error('加载历史消息失败')
  } finally {
    isLoadingHistory.value = false
  }
}

function normalizeMessage(msg) {
  const normalized = { ...msg }
  if (msg.reasoning_content && !msg.reasoning) {
    normalized.reasoning = msg.reasoning_content
    normalized.reasoningDone = true
  }
  if (msg.tool_calls_json) {
    try {
      const rawTCs = typeof msg.tool_calls_json === 'string'
        ? JSON.parse(msg.tool_calls_json)
        : msg.tool_calls_json
      const tcs = (Array.isArray(rawTCs) ? rawTCs : []).map(tc => {
        const fullName = tc.function?.name || ''
        let tool = fullName
        let func = fullName
        const doubleIdx = fullName.lastIndexOf('__')
        if (doubleIdx > 0) {
          tool = fullName.substring(0, doubleIdx)
          func = fullName.substring(doubleIdx + 2)
        } else {
          const dotIdx = fullName.indexOf('.')
          if (dotIdx > 0) {
            tool = fullName.substring(0, dotIdx)
            func = fullName.substring(dotIdx + 1)
          }
        }
        let args = {}
        if (tc.function?.arguments) {
          try {
            args = JSON.parse(tc.function.arguments)
          } catch {
            args = tc.function.arguments
          }
        }
        return {
          tool,
          function: func,
          args,
          status: 'done',
          result: null
        }
      })
      normalized.toolCalls = tcs
    } catch {
      normalized.toolCalls = []
    }
  } else {
    normalized.toolCalls = normalized.toolCalls || []
  }
  return normalized
}

watch(() => route.params.id, async (newId, oldId) => {
  if (oldId && chat.isStreaming) {
    cancel()
  }
  if (newId && newId !== chat.conversationId) {
    isFirstLoad.value = true
    isInitialScrollDone.value = false
    chat.setConversation(newId)
    await loadHistory(newId)
    // 初始加载后滚动到最新消息 - 不要用smooth滚动
    await nextTick()
    if (messagesContainerRef.value) {
      messagesContainerRef.value.scrollTop = messagesContainerRef.value.scrollHeight
    }
    isFirstLoad.value = false
    // 延迟一下才允许触发自动加载
    setTimeout(() => { isInitialScrollDone.value = true }, 200)
  } else if (newId && chat.needsRefetch) {
    isFirstLoad.value = true
    isInitialScrollDone.value = false
    await loadHistory(newId)
    await nextTick()
    if (messagesContainerRef.value) {
      messagesContainerRef.value.scrollTop = messagesContainerRef.value.scrollHeight
    }
    chat.needsRefetch = false
    isFirstLoad.value = false
    setTimeout(() => { isInitialScrollDone.value = true }, 200)
  } else if (!newId) {
    if (chat.isStreaming) cancel()
    const savedAgentId = chat.agentId
    chat.reset()
    chat.agentId = savedAgentId
  }
}, { immediate: true })

onBeforeUnmount(() => {
  if (chat.isStreaming) cancel()
})

watch(() => chat.streamingMessage?.content, () => scrollToBottom())
watch(() => chat.streamingMessage?.reasoning, () => scrollToBottom())
// 注意：移除了对 messages.length 的监听，避免加载历史消息后自动滚到底部

async function handleSend() {
  const content = inputValue.value.trim()
  if (!content || chat.isStreaming) return

  chat.addMessage({ id: Date.now().toString(), role: 'user', content })
  inputValue.value = ''
  scrollToBottom()

  await sendMessage(content, {
    conversationId: chat.conversationId,
    deepMode: false,
    agentId: chat.agentId
  })
}

function handleStop() {
  cancel()
}

function handleScroll(e) {
  const container = e.target
  // 只有在初始滚动完成后才允许自动加载
  // 快到顶端就开始加载，用容器高度的15%判断，适配移动端和PC端
  const threshold = container.clientHeight * 0.15
  if (isInitialScrollDone.value && container.scrollTop <= threshold && chat.hasMoreBefore && !isLoadingHistory.value) {
    loadHistory(chat.conversationId, 'before', chat.oldestCursor)
  }
}
</script>

<template>
  <div style="display: flex; flex-direction: column; height: 100vh; background: var(--color-bg-page); min-width: 0;">
    <!-- 顶部工具栏 -->
    <PageTopBar :title="currentTitle" :visible="!isMobile || isBarVisible" />

    <!-- 消息区域 -->
    <div
      ref="messagesContainerRef"
      @scroll="handleScroll"
      @touchstart="isMobile ? showBar() : null"
      style="flex: 1; overflow-y: auto; padding: var(--space-lg); display: flex; justify-content: center;"
    >
      <div style="width: 100%; max-width: 900px;">
        <!-- 顶部加载指示器 -->
        <div v-if="isLoadingHistory" style="text-align: center; padding: 20px 0;">
          <div style="width: 24px; height: 24px; border: 2px solid var(--color-border); border-top-color: var(--color-primary); border-radius: 50%; animation: spin 0.8s linear infinite; display: inline-block;"></div>
        </div>

        <div v-if="chat.messages.length === 0 && !chat.streamingMessage" style="padding: 100px 0; text-align: center; color: var(--color-text-muted);">
          开始聊天吧
        </div>

        <MessageBubble
          v-for="msg in chat.messages"
          :key="msg.id"
          :message="msg"
          :is-history="true"
        />
        <div ref="streamingBubbleRef" v-if="chat.streamingMessage">
          <MessageBubble
            :message="chat.streamingMessage"
            :is-history="false"
            :status-message="chat.statusMessage"
          />
        </div>

        <PlanGraph
          v-if="chat.streamingMessage || chat.messages.length > 0"
          :nodes="chat.planNodes"
          :edges="chat.planEdges"
          :visible="planVisible"
        />

        <div v-if="chat.error && !chat.streamingMessage" style="text-align: center; margin: var(--space-md) 0;">
          <NButton size="small" type="error" quaternary>
            {{ chat.error }}
          </NButton>
        </div>

        <div ref="messagesEndRef" style="height: 15px;" />
      </div>
    </div>

    <!-- 输入区域 -->
      <div style="padding: var(--space-md) var(--space-lg); background: var(--color-surface); border-top: 1px solid var(--color-border); flex-shrink: 0;">
        <div style="border: 1px solid var(--color-border); border-radius: var(--radius-xl); padding: var(--space-md) var(--space-md); background: var(--color-surface);">
          <NInput
            v-model:value="inputValue"
            type="textarea"
            placeholder="发消息..."
            :disabled="chat.isStreaming"
            :autosize="{ minRows: 1, maxRows: 6 }"
            @keydown="(e) => { if (e.key === 'Enter' && !e.shiftKey && !e.isComposing) { e.preventDefault(); handleSend() }}"
            :bordered="false"
            style="background: transparent;"
          />
          <div style="display: flex; justify-content: flex-end; align-items: center; margin-top: 8px;">
            <NButton
              v-if="!chat.isStreaming"
              type="primary"
              size="small"
              :disabled="!inputValue.trim()"
              @click="handleSend"
              style="border-radius: 50%; width: 40px; height: 40px; padding: 0;"
            >↑</NButton>
            <NButton
              v-else
              type="error"
              size="small"
              @click="handleStop"
              style="border-radius: 50%; width: 40px; height: 40px; padding: 0;"
            >■</NButton>
          </div>
        </div>
      </div>
  </div>
</template>

<style scoped>
@keyframes spin {
  from { transform: rotate(0deg); }
  to { transform: rotate(360deg); }
}
</style>
