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

const inputVisible = ref(!isMobile)
const inputExpanded = ref(false)

function toggleInput() {
  inputVisible.value = !inputVisible.value
  if (inputVisible.value) {
    nextTick(() => {
      document.querySelector('.chat-input')?.focus()
    })
  }
}

function toggleExpand() {
  inputExpanded.value = !inputExpanded.value
}

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
        const scrollContainer = messagesContainerRef.value
        const oldScrollHeight = scrollContainer.scrollHeight
        const oldScrollTop = scrollContainer.scrollTop

        chat.prependMessages(msgs)

        await nextTick()
        const newScrollHeight = scrollContainer.scrollHeight
        scrollContainer.scrollTop = oldScrollTop + (newScrollHeight - oldScrollHeight)
      } else {
        chat.setMessages(msgs)
      }
      chat.oldestCursor = res.oldestCursor
      chat.hasMoreBefore = res.hasMoreBefore
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
    await nextTick()
    if (messagesContainerRef.value) {
      messagesContainerRef.value.scrollTop = messagesContainerRef.value.scrollHeight
    }
    isFirstLoad.value = false
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
  const threshold = container.clientHeight * 0.15
  if (isInitialScrollDone.value && container.scrollTop <= threshold && chat.hasMoreBefore && !isLoadingHistory.value) {
    loadHistory(chat.conversationId, 'before', chat.oldestCursor)
  }
}
</script>

<template>
  <div style="display: flex; flex-direction: column; height: 100vh; background: var(--color-bg-page); min-width: 0;">
    <PageTopBar :title="currentTitle" :visible="!isMobile || isBarVisible" />

    <div
      ref="messagesContainerRef"
      @scroll="handleScroll"
      @touchstart="isMobile ? showBar() : null"
      style="flex: 1; overflow-y: auto; padding: var(--space-lg); display: flex; justify-content: center;"
    >
      <div style="width: 100%; max-width: 900px;">
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

        <div
          ref="messagesEndRef"
          :style="{
            height: isMobile && inputVisible ? (inputExpanded ? '75vh' : '140px') : '15px',
            transition: 'height 0.2s ease-out'
          }"
        />
      </div>
    </div>

    <div
      v-if="isMobile"
      :style="{
        position: 'fixed',
        bottom: inputVisible ? (inputExpanded ? '75vh' : '120px') : '0',
        left: '50%',
        transform: 'translateX(-50%)',
        zIndex: 100,
        transition: 'bottom 0.2s ease-out'
      }"
    >
      <div
        v-if="!inputVisible"
        @click="toggleInput"
        :style="{
          width: '60px',
          height: '30px',
          background: 'rgba(91, 106, 191, 0.85)',
          borderRadius: '30px 30px 0 0',
          display: 'flex',
          alignItems: 'flex-end',
          justifyContent: 'center',
          paddingBottom: '0px',
          lineHeight: '24px',
          cursor: 'pointer',
          boxShadow: '0 -2px 10px rgba(0,0,0,0.1)',
          color: 'white',
          fontSize: '20px',
          fontWeight: 'bold'
        }"
      >
        ↑
      </div>
    </div>

    <div
      :style="[
        !isMobile ? {
          padding: 'var(--space-md) var(--space-lg)',
          background: 'var(--color-surface)',
          borderTop: '1px solid var(--color-border)',
          flexShrink: 0
        } : {
          position: 'fixed',
          left: 0,
          right: 0,
          bottom: 0,
          padding: 'var(--space-md) var(--space-lg)',
          background: 'var(--color-surface)',
          borderTop: '1px solid var(--color-border)',
          zIndex: 99,
          transform: inputVisible ? 'translateY(0)' : 'translateY(100%)',
          height: inputExpanded ? '75vh' : 'auto',
          transition: 'transform 0.2s ease-out, height 0.2s ease-out',
          overflow: 'hidden'
        }
      ]"
    >
      <div style="border: 1px solid var(--color-border); border-radius: var(--radius-xl); padding: var(--space-md) var(--space-md); background: var(--color-surface); height: 100%; display: flex; flex-direction: column;">
        <NInput
          v-model:value="inputValue"
          type="textarea"
          placeholder="发消息..."
          :disabled="chat.isStreaming"
          :autosize="isMobile && inputExpanded ? false : { minRows: 1, maxRows: 6 }"
          class="chat-input"
          @keydown="(e) => { if (e.key === 'Enter' && !e.shiftKey && !e.isComposing) { e.preventDefault(); handleSend() }}"
          :bordered="false"
          style="background: transparent; flex: 1;"
        />
        <div :style="{ display: 'flex', justifyContent: isMobile ? 'space-between' : 'flex-end', alignItems: 'center', marginTop: '8px' }">
          <div v-if="isMobile && inputVisible">
            <NButton quaternary size="small" @click="toggleExpand">
              {{ inputExpanded ? '⤡' : '⤢' }}
            </NButton>
            <NButton v-if="inputVisible" quaternary size="small" @click="toggleInput" style="margin-left: 8px;">
              收起
            </NButton>
          </div>
          <div style="display: flex; justify-content: flex-end; align-items: center;">
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
  </div>
</template>

<style scoped>
@keyframes spin {
  from { transform: rotate(0deg); }
  to { transform: rotate(360deg); }
}
</style>
