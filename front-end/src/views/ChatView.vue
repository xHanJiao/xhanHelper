<script setup>
import { ref, computed, watch, nextTick, onBeforeUnmount, onMounted } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { useChatStore } from '@/stores/chat'
import { useConversationsStore } from '@/stores/conversations'
import { useChatSSE } from '@/composables/useChatSSE'
import { useScrollTopBar } from '@/composables/useScrollTopBar'
import { useDevice } from '@/composables/useDevice'
import { api } from '@/api/client'
import {
  NInput,
  NButton,
  NPopover,
  NInputNumber,
  NSwitch,
  useMessage
} from 'naive-ui'
import MessageBubble from '@/components/MessageBubble.vue'

import PageTopBar from '@/components/PageTopBar.vue'
import TopBarBtn from '@/components/TopBarBtn.vue'
import { useMobile } from '@/composables/useMobile'
import { useThemeStore } from '@/stores/theme'

const theme = useThemeStore()
const route = useRoute()
const router = useRouter()
const chat = useChatStore()
const convs = useConversationsStore()
const { sendMessage, cancel } = useChatSSE()
const nMessage = useMessage()
const { isMobile } = useMobile()
const { isIOS } = useDevice()

onMounted(() => {
  fetchLlmConfig()
  fetchAgents()
})

const inputValue = ref('')
const messagesEndRef = ref(null)
const streamingBubbleRef = ref(null)
const isLoadingHistory = ref(false)
const isFirstLoad = ref(true)
const messagesContainerRef = ref(null)
const mobileInputRef = ref(null)
const isInitialScrollDone = ref(false)
const isAwayFromBottom = ref(false)

const { isBarVisible, showBar } = useScrollTopBar(messagesContainerRef)

const inputVisible = ref(true)
const inputExpanded = ref(false)

if (isMobile.value) inputVisible.value = false
watch(isMobile, (val) => { if (val) inputVisible.value = false })

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

// LLM 参数（从后端 API 获取，不存 localStorage）
const showLlmSettings = ref(false)
const llmSettings = ref({ temperature: null, max_tokens: null, thinking: true, deepMode: false })

async function fetchLlmConfig() {
  try {
    const conv = convs.list.find(c => c.id === chat.conversationId)
    const agentId = conv?.agent_id || chat.agentId
    const qs = agentId ? `?agent_id=${encodeURIComponent(agentId)}` : ''
    const res = await api.get('/api/llm-config' + qs)
    if (res && typeof res === 'object') {
      llmSettings.value = {
        temperature: res.temperature ?? null,
        max_tokens: res.max_tokens ?? null,
        thinking: res.thinking ?? true,
        deepMode: llmSettings.value.deepMode
      }
    }
  } catch (e) {
    console.error('Failed to fetch LLM config', e)
  }
}

// agent_matched 后更新参数
watch(() => chat.llmParams, (params) => {
  if (params) {
    llmSettings.value = {
      temperature: params.temperature ?? llmSettings.value.temperature,
      max_tokens: params.max_tokens ?? llmSettings.value.max_tokens,
      thinking: params.thinking ?? llmSettings.value.thinking,
      deepMode: llmSettings.value.deepMode
    }
  }
})

// 智能体列表（用于切换/解绑）
const agentsList = ref([])

async function fetchAgents() {
  try {
    const res = await api.get('/api/agents')
    agentsList.value = (res.agents || []).map(a => ({ id: a.id, name: a.name }))
  } catch (e) {
    console.error('Failed to fetch agents', e)
  }
}

async function handleSelectAgent(agentId) {
  const convId = chat.conversationId
  if (!convId) return

  try {
    const body = { agent_id: agentId }
    const res = await api.patch(`/api/conversations/${convId}`, body)
    const newAgentId = res.agent_id || null
    const newAgentName = res.agent_name || ''

    // 更新 conversations store（列表和当前会话）
    convs.updateAgent(convId, newAgentId, newAgentName)

    // 更新 chat store
    chat.agentId = newAgentId
    chat.agentName = newAgentName

    // 刷新 LLM 配置
    fetchLlmConfig()
  } catch (e) {
    console.error('Failed to update agent binding', e)
  }
}

const currentTitle = computed(() => {
  const conv = convs.list.find(c => c.id === chat.conversationId)
  return conv?.title || '新对话'
})

const currentAgentName = computed(() => {
  const conv = convs.list.find(c => c.id === chat.conversationId)
  return conv?.agent_name || chat.agentName || ''
})

const currentAgentId = computed(() => {
  const conv = convs.list.find(c => c.id === chat.conversationId)
  return conv?.agent_id || chat.agentId || null
})

const planVisible = computed(() => {
  return Object.keys(chat.planNodes).length >= 2
})

function scrollToBottom() {
  nextTick(() => {
    if (chat.isStreaming && streamingBubbleRef.value) {
      const container = messagesContainerRef.value
      if (!container) return
      // 用户离开底部超过 80px 时不强制滚动，避免打断阅读历史
      const distFromBottom = container.scrollHeight - container.scrollTop - container.clientHeight
      if (distFromBottom > 80) return
      container.scrollTop = container.scrollHeight
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
      chat.oldestCursor = res.oldest_cursor
      chat.hasMoreBefore = res.has_more_before
      // 批量获取关联记忆详情
      fetchAssociatedMemories()
    }
  } catch (e) {
    console.error('Failed to load history', e)
    nMessage.error('加载历史消息失败')
  } finally {
    isLoadingHistory.value = false
  }
}

async function fetchAssociatedMemories() {
  const allMemIds = new Set()
  for (const msg of chat.messages) {
    if (msg.memoryIds?.length) {
      for (const id of msg.memoryIds) allMemIds.add(id)
    }
  }
  if (allMemIds.size === 0) return
  try {
    const memories = await api.post('/api/memories/batch', { ids: [...allMemIds] })
    const memMap = {}
    if (Array.isArray(memories)) {
      for (const mem of memories) {
        memMap[mem.id] = mem
      }
    }
    for (const msg of chat.messages) {
      if (msg.memoryIds?.length) {
        msg.associatedMemories = msg.memoryIds.map(id => memMap[id]).filter(Boolean)
      }
    }
  } catch (e) {
    console.error('Failed to fetch associated memories', e)
  }
}

function normalizeMessage(msg) {
  const normalized = { ...msg }
  // 解析 memory_ids JSON 字符串
  if (msg.memory_ids) {
    try {
      normalized.memoryIds = typeof msg.memory_ids === 'string'
        ? JSON.parse(msg.memory_ids)
        : msg.memory_ids
    } catch {
      normalized.memoryIds = []
    }
  }
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
    inputVisible.value = true
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
    const currentConv = convs.list.find(c => c.id === newId)
    if (currentConv?.agent_id) {
      chat.agentId = currentConv.agent_id
      chat.agentName = currentConv.agent_name || ''
    } else {
      chat.agentId = null
      chat.agentName = null
    }
    fetchLlmConfig()
  } else if (newId && chat.needsRefetch) {
    inputVisible.value = true
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
    const currentConv2 = convs.list.find(c => c.id === newId)
    if (currentConv2?.agent_id) {
      chat.agentId = currentConv2.agent_id
      chat.agentName = currentConv2.agent_name || ''
    } else {
      chat.agentId = null
      chat.agentName = null
    }
    fetchLlmConfig()
  } else if (!newId) {
    inputVisible.value = true
    isInitialScrollDone.value = false
    if (chat.isStreaming) cancel()
    const savedAgentId = chat.agentId
    const savedAgentName = chat.agentName
    chat.reset()
    chat.agentId = savedAgentId
    chat.agentName = savedAgentName
    fetchLlmConfig()
  }
}, { immediate: true })

onBeforeUnmount(() => {
  if (chat.isStreaming) cancel()
})

watch(() => chat.streamingMessage?.content, () => scrollToBottom())
watch(() => chat.streamingMessage?.reasoning, () => scrollToBottom())
watch(inputExpanded, () => {
  const container = messagesContainerRef.value
  if (!container) return
  const distFromBottom = container.scrollHeight - container.scrollTop - container.clientHeight
  if (distFromBottom <= 80) scrollToBottom()
})

async function handleSend() {
  const content = inputValue.value.trim()
  if (!content || chat.isStreaming) return

  chat.addMessage({ id: Date.now().toString(), role: 'user', content })
  inputValue.value = ''
  inputExpanded.value = false
  // 把当前会话移到列表最前面
  if (chat.conversationId && currentTitle.value) {
    convs.prepend({ id: chat.conversationId, title: currentTitle.value })
  }
  // 立即滚到底部（不管用户在浏览什么位置）
  nextTick(() => {
    const el = messagesContainerRef.value
    if (el) el.scrollTop = el.scrollHeight
  })

  const conv = convs.list.find(c => c.id === chat.conversationId)
  const hasAgent = !!(conv?.agent_id)
  const opts = {
    conversationId: chat.conversationId,
    deepMode: !hasAgent ? llmSettings.value.deepMode : false,
    agentId: chat.agentId
  }
  // 无 Agent 模式时，带上用户设定的 LLM 参数
  if (!hasAgent && llmSettings.value.temperature !== null) {
    opts.temperature = llmSettings.value.temperature
  }
  if (!hasAgent && llmSettings.value.max_tokens !== null) {
    opts.maxTokens = llmSettings.value.max_tokens
  }
  if (llmSettings.value.thinking !== null) {
    opts.thinking = llmSettings.value.thinking
  }
  await sendMessage(content, opts)
}

function handleStop() {
  cancel()
}

function handleScroll(e) {
  const container = e.target
  const distFromBottom = container.scrollHeight - container.scrollTop - container.clientHeight
  isAwayFromBottom.value = distFromBottom > 200

  // 上滑看历史时自动隐藏输入框，回到底部时恢复（滞后阈值防抖动）
  if (isInitialScrollDone.value && !isMobile.value) {
    if (distFromBottom > 200 && inputVisible.value) {
      inputVisible.value = false
    } else if (distFromBottom < 30 && !inputVisible.value) {
      inputVisible.value = true
    }
  }

  const threshold = container.clientHeight * 0.15
  if (isInitialScrollDone.value && container.scrollTop <= threshold && chat.hasMoreBefore && !isLoadingHistory.value) {
    loadHistory(chat.conversationId, 'before', chat.oldestCursor)
  }
}

function handleContainerClick(e) {
  if (!isIOS) return

  const container = messagesContainerRef.value
  if (!container) return

  const rect = container.getBoundingClientRect()
  const clickY = e.clientY - rect.top
  const topThird = rect.height / 3

  if (clickY <= topThird) {
    showBar()
  }
}
</script>

<template>
  <div style="display: flex; flex-direction: column; height: 100vh; background: var(--color-bg-page); min-width: 0;">
    <PageTopBar :title="currentTitle" :agent-id="currentAgentId" :agent-name="currentAgentName" :visible="!isMobile || isBarVisible" :agents="agentsList" @select-agent="handleSelectAgent" @popover-open="fetchAgents" >
      <template #right>
        <TopBarBtn :label="theme.isDark ? '浅色' : '深色'" @click="theme.toggle()" />
      </template>
    </PageTopBar>

    <div
      ref="messagesContainerRef"
      @scroll="handleScroll"
      @touchstart="isMobile ? showBar() : null"
      @click="handleContainerClick"
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
          />
        </div>

        <!-- PlanGraph 暂时隐藏，工具使用简单时图无意义 -->
        <!--
        <PlanGraph
          v-if="chat.streamingMessage || chat.messages.length > 0"
          :nodes="chat.planNodes"
          :edges="chat.planEdges"
          :visible="planVisible"
        />
        -->
        <div v-if="chat.error && !chat.streamingMessage" style="text-align: center; margin: var(--space-md) 0;">
          <NButton size="small" type="error" quaternary>
            {{ chat.error }}
          </NButton>
        </div>

        <div
          ref="messagesEndRef"
          :style="{
            height: inputExpanded ? (isMobile ? '75vh' : '35vh') : '15px',
            transition: 'height 0.2s ease-out'
          }"
        />
      </div>
    </div>

    <div
      v-if="isMobile && !inputVisible"
      @click="toggleInput"
      :style="{
        width: '60px',
        height: '30px',
        background: 'rgba(91, 106, 191, 0.85)',
        borderRadius: '30px 30px 0 0',
        display: 'flex',
        alignItems: 'flex-end',
        justifyContent: 'center',
        marginLeft: 'auto',
        marginRight: 'auto',
        cursor: 'pointer',
        boxShadow: '0 -2px 10px rgba(0,0,0,0.1)',
        color: 'white',
        fontSize: '20px',
        fontWeight: 'bold',
        flexShrink: 0
      }"
    >
      ↑
    </div>

    <!-- 查看历史消息时，滚动到底部按钮 -->
    <div
      v-if="!isMobile && !inputVisible && isAwayFromBottom"
      @click="() => { inputVisible = true; const el = messagesContainerRef.value; if (el) el.scrollTop = el.scrollHeight }"
      :style="{
        position: 'fixed',
        bottom: '24px',
        left: '50%',
        transform: 'translateX(-50%)',
        zIndex: 101,
        width: '40px',
        height: '40px',
        borderRadius: '50%',
        background: 'rgba(91, 106, 191, 0.8)',
        display: 'flex',
        alignItems: 'center',
        justifyContent: 'center',
        cursor: 'pointer',
        boxShadow: '0 2px 10px rgba(0,0,0,0.2)',
        color: 'white',
        fontSize: '20px',
        userSelect: 'none'
      }"
    >
      ↓
    </div>

    <div
      ref="mobileInputRef"
      :style="[
        !isMobile ? {
          padding: inputVisible ? 'var(--space-md) var(--space-lg)' : '0 var(--space-lg)',
          maxWidth: 'min(88%, 56rem)',
          marginLeft: 'auto',
          marginRight: 'auto',
          width: '100%',
          background: 'var(--color-bg-page)',
          borderTop: 'none',
          flexShrink: 0,
          height: inputVisible ? (inputExpanded ? '50vh' : 'auto') : '0',
          overflow: 'hidden',
          opacity: inputVisible ? 1 : 0,
          transform: inputVisible ? 'scaleY(1)' : 'scaleY(0.8)',
          transformOrigin: 'bottom right',
          transition: 'height 0.25s ease-out, padding 0.25s ease-out, opacity 0.2s ease-out, transform 0.25s ease-out'
        } : {
          padding: inputVisible ? 'var(--space-md) var(--space-lg)' : '0 var(--space-lg)',
          background: 'var(--color-bg-page)',
          borderTop: 'none',
          flexShrink: 0,
          height: inputVisible ? (inputExpanded ? '75vh' : 'auto') : '0',
          overflow: 'hidden',
          opacity: inputVisible ? 1 : 0,
          transition: 'height 0.25s ease-out, padding 0.25s ease-out, opacity 0.2s ease-out'
        }
      ]"
    >
      <div :style="{ border: `1px solid var(--color-bg-page)`, borderRadius: 'var(--radius-xl)', padding: 'var(--space-md) var(--space-md)', background: 'var(--color-surface)', height: '100%', display: 'flex', flexDirection: 'column', boxShadow: '0 2px 12px rgba(0,0,0,0.06)' }">
        <NInput
          v-model:value="inputValue"
          type="textarea"
          placeholder="发消息..."
          :disabled="chat.isStreaming"
          :autosize="(isMobile || inputExpanded) ? false : { minRows: 1, maxRows: 6 }"
          class="chat-input"
          @keydown="(e) => { if (e.key === 'Enter' && !e.shiftKey && !e.isComposing && !inputExpanded) { e.preventDefault(); handleSend() }}"
          :bordered="false"
          style="background: transparent; flex: 1;"
        />
        <div :style="{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginTop: '8px' }">
          <div>
            <NPopover trigger="click" :show="showLlmSettings" @update:show="(v) => showLlmSettings = v">
              <template #trigger>
                <NButton quaternary size="small" style="color: var(--color-text-muted)" @click="showLlmSettings = !showLlmSettings" title="LLM 参数">
                  ⚙
                </NButton>
              </template>
              <div style="width: 240px; padding: 4px 0;">
                <div v-if="currentAgentId" style="font-size: 12px; color: var(--color-text-muted); margin-bottom: 8px;">
                  参数由智能体控制，不可修改
                </div>
                <!-- 模型选择 -->
                <div style="margin-bottom: 6px; display: flex; align-items: center; justify-content: space-between;">
                  <div>
                    <span style="font-size: 13px; color: var(--color-text);">Pro 模型</span>
                    <div style="font-size: 11px; color: var(--color-text-muted);">v4-pro，推理更强但较慢</div>
                  </div>
                  <NSwitch
                    :value="llmSettings.deepMode"
                    @update:value="(v) => { llmSettings.deepMode = v }"
                    :disabled="!!currentAgentId"
                    size="small"
                  />
                </div>
                <div style="border-top: 1px solid var(--color-border); margin: 8px 0;"></div>
                <!-- 思考模式 -->
                <div style="margin-bottom: 6px; display: flex; align-items: center; justify-content: space-between;">
                  <span style="font-size: 13px; color: var(--color-text);">思考模式</span>
                  <NSwitch
                    :value="llmSettings.deepMode ? true : llmSettings.thinking"
                    @update:value="(v) => { llmSettings.thinking = v }"
                    :disabled="!!currentAgentId || llmSettings.deepMode"
                    size="small"
                  />
                </div>
                <div style="margin-bottom: 12px;">
                  <div style="font-size: 13px; margin-bottom: 4px; color: var(--color-text);">Temperature</div>
                  <NInputNumber
                    :value="llmSettings.temperature"
                    @update:value="(v) => { llmSettings.temperature = v }"
                    :min="0" :max="2" :step="0.1"
                    placeholder="默认 1.0"
                    size="small"
                    clearable
                    :disabled="!!currentAgentId"
                    style="width: 100%"
                  />
                </div>
                <div>
                  <div style="font-size: 13px; margin-bottom: 4px; color: var(--color-text);">Max Tokens</div>
                  <NInputNumber
                    :value="llmSettings.max_tokens"
                    @update:value="(v) => { llmSettings.max_tokens = v }"
                    :min="1" :step="100"
                    placeholder="默认 4096"
                    size="small"
                    clearable
                    :disabled="!!currentAgentId"
                    style="width: 100%"
                  />
                </div>
              </div>
            </NPopover>
            <NButton quaternary size="small" @click="toggleExpand" style="color: var(--color-text-muted)">
              {{ inputExpanded ? '⤡ 收起输入框' : '⤢ 展开输入框' }}
            </NButton>
            <NButton v-if="isMobile && inputVisible" quaternary size="small" @click="toggleInput" style="margin-left: 8px; color: var(--color-text-muted)">
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
